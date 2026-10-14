package backend

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"ant-chrome/backend/internal/config"
	"ant-chrome/backend/internal/upstream"
	"ant-chrome/backend/internal/upstream/upstreamtest"
)

func newUpstreamTestApp(t *testing.T) *App {
	t.Helper()
	app := NewApp(t.TempDir())
	app.config = config.DefaultConfig()
	upstream.SetSource(func() (bool, string) {
		return app.config.UpstreamProxy.Enabled, app.config.UpstreamProxy.URL
	})
	upstream.SetLoopbackBypassForTesting(false)
	t.Cleanup(func() {
		_ = upstream.Configure(false, "")
		upstream.SetLoopbackBypassForTesting(true)
	})
	return app
}

func TestSaveUpstreamProxySettingsValidatesAndPersists(t *testing.T) {
	app := newUpstreamTestApp(t)
	if err := app.SaveUpstreamProxySettings(UpstreamProxySettings{Enabled: true, URL: "vmess://abc"}); err == nil {
		t.Fatal("应拒绝非 http/socks5 前置代理")
	}
	if err := app.SaveUpstreamProxySettings(UpstreamProxySettings{Enabled: true, URL: ""}); err == nil {
		t.Fatal("启用时地址不能为空")
	}
	if err := app.SaveUpstreamProxySettings(UpstreamProxySettings{Enabled: true, URL: " socks5://u:p@10.0.0.1:1080 "}); err != nil {
		t.Fatal(err)
	}
	if got := app.GetUpstreamProxySettings(); !got.Enabled || got.URL != "socks5://u:p@10.0.0.1:1080" {
		t.Fatalf("settings = %+v", got)
	}
	if p := upstream.Current(); p == nil || p.Host != "10.0.0.1" {
		t.Fatalf("保存后前置代理未生效: %+v", p)
	}
	data, err := os.ReadFile(app.resolveAppPath("config.yaml"))
	if err != nil || !strings.Contains(string(data), "upstream_proxy:") {
		t.Fatalf("配置未写入文件: %v\n%s", err, data)
	}
	if err := app.SaveUpstreamProxySettings(UpstreamProxySettings{Enabled: false, URL: "socks5://u:p@10.0.0.1:1080"}); err != nil {
		t.Fatal(err)
	}
	if upstream.Current() != nil {
		t.Fatal("关闭后前置代理应失效")
	}
}

func TestTestUpstreamProxy(t *testing.T) {
	app := newUpstreamTestApp(t)
	good := upstreamtest.StartHTTP(t, "u", "p")
	// 测试地址是公网 gstatic，这里只验证错误路径与参数校验，避免依赖外网。
	if res := app.TestUpstreamProxy("ftp://x:1"); res.Ok || res.Error == "" {
		t.Fatalf("非法地址应报错: %+v", res)
	}
	bad := strings.Replace(good.URL, "u:p", "u:wrong", 1)
	if res := app.TestUpstreamProxy(bad); res.Ok {
		t.Fatalf("错误密码不应测试通过: %+v", res)
	}
}

func TestProxyCoreDirectClientUsesUpstream(t *testing.T) {
	app := newUpstreamTestApp(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()
	up := upstreamtest.StartSOCKS5(t, "u", "p")
	app.config.UpstreamProxy = config.UpstreamProxyConfig{Enabled: true, URL: up.URL}

	for _, proxyConfig := range []string{"", "__direct__"} {
		client, _, err := app.unifiedProxyCoreHTTPClient(5*time.Second, proxyConfig)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Get(target.URL)
		if err != nil {
			t.Fatalf("proxyConfig=%q: %v", proxyConfig, err)
		}
		resp.Body.Close()
	}
	if up.Hits() < 2 {
		t.Fatalf("直连下载未经前置代理，hits=%d", up.Hits())
	}
}

func TestGitUpstreamArgs(t *testing.T) {
	newUpstreamTestApp(t)
	args, err := gitUpstreamArgs([]string{"clone", "git@github.com:a/b.git", "/tmp/x"})
	if err != nil || len(args) != 3 {
		t.Fatalf("未启用前置代理时应原样返回: %v %v", args, err)
	}
	_ = upstream.Configure(true, "socks5://u:p@10.0.0.1:1080")
	if _, err := gitUpstreamArgs([]string{"clone", "git@github.com:a/b.git", "/tmp/x"}); err == nil {
		t.Fatal("启用前置代理时应拒绝 SSH 地址")
	}
	args, err = gitUpstreamArgs([]string{"clone", "https://github.com/a/b.git", "/tmp/x"})
	if err != nil || args[0] != "-c" || args[1] != "http.proxy=socks5h://u:p@10.0.0.1:1080" {
		t.Fatalf("args = %v, err = %v", args, err)
	}
}
