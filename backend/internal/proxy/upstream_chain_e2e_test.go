package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ant-chrome/backend/internal/config"
	"ant-chrome/backend/internal/upstream"
	"ant-chrome/backend/internal/upstream/upstreamtest"
)

// 端到端验证：客户端 -> 本地桥接(xray/mihomo) -> 前置代理 -> 节点 -> 目标。
// 依赖仓库 bin/<goos>-<goarch>/ 下的真实内核，缺失时跳过。

// repoKernelPath 把仓库内核复制到临时目录并设为可执行，避免测试改动仓库文件权限。
func repoKernelPath(t *testing.T, name string) string {
	t.Helper()
	wd, _ := os.Getwd()
	src := filepath.Join(wd, "..", "..", "..", "bin", runtime.GOOS+"-"+runtime.GOARCH, name)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("未找到 %s，跳过端到端测试", src)
	}
	dst := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return dst
}

func e2eConfig(t *testing.T) *config.Config {
	cfg := config.DefaultConfig()
	cfg.Browser.UserDataRoot = t.TempDir()
	return cfg
}

func e2eTarget(t *testing.T) *httptest.Server {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(target.Close)
	return target
}

func getThrough(t *testing.T, bridgeURL string, target string) {
	t.Helper()
	proxyURL, err := url.Parse(bridgeURL)
	if err != nil {
		t.Fatalf("bridge url %q: %v", bridgeURL, err)
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	var lastErr error
	for i := 0; i < 20; i++ {
		resp, err := client.Get(target)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) == "ok" {
				return
			}
			lastErr = nil
		} else {
			lastErr = err
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("经桥接 %s 访问 %s 失败: %v", bridgeURL, target, lastErr)
}

func hasTarget(targets []string, want string) bool {
	for _, item := range targets {
		if item == want {
			return true
		}
	}
	return false
}

type e2eKernel struct {
	name    string
	acquire func(t *testing.T, src string) string
}

func e2eKernels(t *testing.T) []e2eKernel {
	return []e2eKernel{
		{name: "xray", acquire: func(t *testing.T, src string) string {
			cfg := e2eConfig(t)
			cfg.Browser.XrayBinaryPath = repoKernelPath(t, "xray")
			mgr := NewXrayManager(cfg, t.TempDir())
			t.Cleanup(mgr.StopAll)
			bridgeURL, _, err := mgr.AcquireBridge(src, nil, "")
			if err != nil {
				t.Fatalf("xray AcquireBridge: %v", err)
			}
			return bridgeURL
		}},
		{name: "mihomo", acquire: func(t *testing.T, src string) string {
			cfg := e2eConfig(t)
			cfg.Browser.ClashBinaryPath = repoKernelPath(t, "mihomo")
			mgr := NewClashManager(cfg, t.TempDir())
			t.Cleanup(mgr.StopAll)
			bridgeURL, _, err := mgr.AcquireNodeBridge(src, nil, "")
			if err != nil {
				t.Fatalf("mihomo AcquireNodeBridge: %v", err)
			}
			return bridgeURL
		}},
	}
}

func useE2EUpstream(t *testing.T, raw string) {
	t.Helper()
	if err := upstream.Configure(true, raw); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upstream.Configure(false, "") })
}

func TestUpstreamE2ENodeTrafficGoesThroughUpstream(t *testing.T) {
	for _, kernel := range e2eKernels(t) {
		for _, upstreamKind := range []string{"socks5", "http"} {
			for _, nodeAuth := range []bool{true, false} {
				name := kernel.name + "/upstream-" + upstreamKind
				if nodeAuth {
					name += "/node-auth"
				} else {
					name += "/node-noauth"
				}
				t.Run(name, func(t *testing.T) {
					target := e2eTarget(t)
					user, pass := "", ""
					if nodeAuth {
						user, pass = "nodeuser", "nodepass"
					}
					node := upstreamtest.StartHTTP(t, user, pass)
					var up *upstreamtest.Server
					if upstreamKind == "socks5" {
						up = upstreamtest.StartSOCKS5(t, "up", "pw")
					} else {
						up = upstreamtest.StartHTTP(t, "up", "pw")
					}
					useE2EUpstream(t, up.URL)

					bridgeURL := kernel.acquire(t, node.URL)
					getThrough(t, bridgeURL, target.URL)

					if !hasTarget(up.Targets(), node.Addr) {
						t.Fatalf("前置代理未收到连接节点 %s 的请求，实际: %v", node.Addr, up.Targets())
					}
					if node.Hits() == 0 {
						t.Fatal("节点未收到请求")
					}
				})
			}
		}
	}
}

func TestUpstreamE2EDirectGoesThroughUpstream(t *testing.T) {
	for _, kernel := range e2eKernels(t) {
		t.Run(kernel.name, func(t *testing.T) {
			target := e2eTarget(t)
			up := upstreamtest.StartSOCKS5(t, "up", "pw")
			useE2EUpstream(t, up.URL)

			src := UpstreamDirectReplacement("direct://")
			if src != up.URL {
				t.Fatalf("direct 未替换为前置代理: %q", src)
			}
			bridgeURL := kernel.acquire(t, src)
			getThrough(t, bridgeURL, target.URL)
			targetAddr := strings.TrimPrefix(target.URL, "http://")
			if !hasTarget(up.Targets(), targetAddr) {
				t.Fatalf("直连流量未经前置代理到达目标 %s，实际: %v", targetAddr, up.Targets())
			}
		})
	}
}

func TestUpstreamE2EBridgeKeyChangesWithUpstream(t *testing.T) {
	src := "http://u:p@10.0.0.1:8080"
	_ = upstream.Configure(false, "")
	without := computeNodeKey(src)
	useE2EUpstream(t, "socks5://127.0.0.1:1080")
	with := computeNodeKey(src)
	_ = upstream.Configure(true, "socks5://127.0.0.1:1081")
	other := computeNodeKey(src)
	if without == with || with == other {
		t.Fatal("前置代理变化时桥接 key 应随之变化")
	}
}

func TestUpstreamSingBoxConfigIsValid(t *testing.T) {
	binary := repoKernelPath(t, "sing-box")
	up := "socks5://up:pw@127.0.0.1:1080"
	useE2EUpstream(t, up)
	cfg := e2eConfig(t)
	mgr := NewSingBoxManager(cfg, t.TempDir())
	outbound := map[string]interface{}{
		"type":        "hysteria2",
		"tag":         "proxy-out",
		"server":      "example.com",
		"server_port": 443,
		"password":    "secret",
		"tls":         map[string]interface{}{"enabled": true, "server_name": "example.com"},
	}
	path, err := mgr.buildConfig("upstream-check", outbound, 18080)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(binary, "check", "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("sing-box check 失败: %v\n%s", err, output)
	}
	data, _ := os.ReadFile(path)
	var parsed map[string]interface{}
	_ = json.Unmarshal(data, &parsed)
	if !strings.Contains(string(data), `"detour": "ant-upstream"`) {
		t.Fatalf("节点未通过 detour 走前置代理:\n%s", data)
	}

	_ = upstream.Configure(true, "http://127.0.0.1:8080")
	if _, err := mgr.buildConfig("upstream-check-http", outbound, 18081); err == nil || !strings.Contains(err.Error(), "SOCKS5") {
		t.Fatalf("HTTP 前置代理承载 hysteria2 应报错，实际: %v", err)
	}
}

func TestUpstreamInvalidConfigFailsClosedForBridges(t *testing.T) {
	_ = upstream.Configure(true, "https://bad:1")
	t.Cleanup(func() { _ = upstream.Configure(false, "") })
	if _, _, err := applyXrayUpstream([]interface{}{map[string]interface{}{"protocol": "vmess"}}); err == nil {
		t.Fatal("xray 应拒绝无效前置代理")
	}
	if _, err := applyMihomoUpstream(map[string]interface{}{"type": "vmess"}); err == nil {
		t.Fatal("mihomo 应拒绝无效前置代理")
	}
	if err := applySingBoxUpstream(map[string]interface{}{}, map[string]interface{}{"type": "tuic"}); err == nil {
		t.Fatal("sing-box 应拒绝无效前置代理")
	}
}
