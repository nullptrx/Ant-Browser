package upstream_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ant-chrome/backend/internal/upstream"
	"ant-chrome/backend/internal/upstream/upstreamtest"
)

func useUpstream(t *testing.T, raw string) {
	t.Helper()
	upstream.SetLoopbackBypassForTesting(false)
	if err := upstream.Configure(true, raw); err != nil {
		t.Fatalf("Configure(%q): %v", raw, err)
	}
	t.Cleanup(func() {
		_ = upstream.Configure(false, "")
		upstream.SetLoopbackBypassForTesting(true)
	})
}

func TestParse(t *testing.T) {
	cases := []struct {
		raw     string
		scheme  string
		hasAuth bool
		wantErr bool
	}{
		{"socks5://127.0.0.1:1080", upstream.SchemeSOCKS5, false, false},
		{"socks5h://u:p@proxy.example.com:1080", upstream.SchemeSOCKS5, true, false},
		{"http://u:p%40ss@10.0.0.1:8080", upstream.SchemeHTTP, true, false},
		{"https://10.0.0.1:443", "", false, true},
		{"vmess://abc", "", false, true},
		{"socks5://127.0.0.1", "", false, true},
		{"", "", false, true},
	}
	for _, tc := range cases {
		p, err := upstream.Parse(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Parse(%q) expected error", tc.raw)
			}
			continue
		}
		if err != nil || p.Scheme != tc.scheme || p.HasAuth() != tc.hasAuth {
			t.Errorf("Parse(%q) = %+v, %v", tc.raw, p, err)
		}
	}
	if p, _ := upstream.Parse("http://u:p%40ss@10.0.0.1:8080"); p.Password != "p@ss" {
		t.Errorf("password not unescaped: %q", p.Password)
	}
}

func TestDisabledHasNoKeySuffixAndDialsDirect(t *testing.T) {
	_ = upstream.Configure(false, "")
	if upstream.Current() != nil || upstream.KeySuffix() != "" {
		t.Fatal("expected disabled upstream")
	}
}

func TestDialerAndTransportRouteThroughUpstream(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()
	targetAddr := strings.TrimPrefix(target.URL, "http://")

	for _, kind := range []string{"socks5", "http"} {
		t.Run(kind, func(t *testing.T) {
			var srv *upstreamtest.Server
			if kind == "socks5" {
				srv = upstreamtest.StartSOCKS5(t, "user", "pass")
			} else {
				srv = upstreamtest.StartHTTP(t, "user", "pass")
			}
			useUpstream(t, srv.URL)

			conn, err := upstream.Dialer{Timeout: 5 * time.Second}.DialContext(context.Background(), "tcp", targetAddr)
			if err != nil {
				t.Fatalf("dial via upstream: %v", err)
			}
			conn.Close()

			resp, err := upstream.HTTPClient(5 * time.Second).Get(target.URL)
			if err != nil {
				t.Fatalf("http via upstream: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != "ok" {
				t.Fatalf("body = %q", body)
			}
			if srv.Hits() < 2 {
				t.Fatalf("upstream hits = %d, want >= 2 (dial + http)", srv.Hits())
			}
		})
	}
}

func TestUpstreamWrongCredentialsFailsWithoutFallback(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	for _, kind := range []string{"socks5", "http"} {
		t.Run(kind, func(t *testing.T) {
			var srv *upstreamtest.Server
			if kind == "socks5" {
				srv = upstreamtest.StartSOCKS5(t, "user", "pass")
			} else {
				srv = upstreamtest.StartHTTP(t, "user", "pass")
			}
			useUpstream(t, strings.Replace(srv.URL, "user:pass", "user:wrong", 1))
			_, err := upstream.Dialer{Timeout: 3 * time.Second}.Dial("tcp", strings.TrimPrefix(target.URL, "http://"))
			if err == nil {
				t.Fatal("expected failure with wrong credentials, got direct connection")
			}
		})
	}
}

func TestLoopbackBypassesUpstream(t *testing.T) {
	srv := upstreamtest.StartSOCKS5(t, "", "")
	if err := upstream.Configure(true, srv.URL); err != nil {
		t.Fatal(err)
	}
	defer upstream.Configure(false, "")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	resp, err := upstream.HTTPClient(3 * time.Second).Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if srv.Hits() != 0 {
		t.Fatalf("loopback request went through upstream (%d hits)", srv.Hits())
	}
}

func TestSourceIsReadLazilyAndInvalidConfigFailsClosed(t *testing.T) {
	enabled, raw := false, ""
	upstream.SetSource(func() (bool, string) { return enabled, raw })
	defer upstream.Configure(false, "")

	if upstream.Current() != nil || upstream.ConfigError() != nil {
		t.Fatal("expected disabled")
	}
	enabled, raw = true, "socks5://u:p@10.0.0.1:1080"
	if p := upstream.Current(); p == nil || p.Host != "10.0.0.1" {
		t.Fatalf("source change not picked up: %+v", p)
	}
	enabled, raw = true, "https://bad:1"
	if upstream.Current() != nil || upstream.ConfigError() == nil {
		t.Fatal("invalid enabled config should report an error")
	}
	if _, err := (upstream.Dialer{Timeout: time.Second}).Dial("tcp", "93.184.216.34:80"); err == nil || !strings.Contains(err.Error(), "前置代理配置无效") {
		t.Fatalf("dial should fail closed, got %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	if _, err := upstream.ProxyFunc(nil)(req); err == nil {
		t.Fatal("ProxyFunc should fail closed on invalid config")
	}
}
