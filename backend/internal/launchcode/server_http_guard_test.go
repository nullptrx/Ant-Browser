package launchcode

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func serveGuardedHealth(t *testing.T, server *LaunchServer, host string, headers map[string]string) int {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:19876/api/health", nil)
	request.RemoteAddr = "127.0.0.1:50000"
	request.Host = host
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	server.buildHandler(true).ServeHTTP(response, request)
	return response.Code
}

func TestBrowserRequestGuardAllowsLocalClients(t *testing.T) {
	server := NewLaunchServer(nil, nil, nil, 0)
	cases := []struct {
		name    string
		host    string
		headers map[string]string
	}{
		{name: "script", host: "127.0.0.1:19876"},
		{name: "localhost", host: "localhost:19876"},
		{name: "ipv6 loopback", host: "[::1]:19876"},
		{name: "address bar", host: "127.0.0.1:19876", headers: map[string]string{"Sec-Fetch-Site": "none"}},
		{name: "same origin", host: "127.0.0.1:19876", headers: map[string]string{"Origin": "http://127.0.0.1:19876", "Sec-Fetch-Site": "same-origin"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := serveGuardedHealth(t, server, tc.host, tc.headers); code != http.StatusOK {
				t.Fatalf("status = %d, want 200", code)
			}
		})
	}
}

func TestBrowserRequestGuardRejectsWebPages(t *testing.T) {
	server := NewLaunchServer(nil, nil, nil, 0)
	cases := []struct {
		name    string
		host    string
		headers map[string]string
	}{
		{name: "dns rebinding", host: "attacker.example:19876"},
		{name: "cross site fetch", host: "127.0.0.1:19876", headers: map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{name: "same site local page", host: "127.0.0.1:19876", headers: map[string]string{"Sec-Fetch-Site": "same-site"}},
		{name: "foreign origin", host: "127.0.0.1:19876", headers: map[string]string{"Origin": "https://attacker.example"}},
		{name: "null origin", host: "127.0.0.1:19876", headers: map[string]string{"Origin": "null"}},
		{name: "other local port", host: "127.0.0.1:19876", headers: map[string]string{"Origin": "http://127.0.0.1:3000"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := serveGuardedHealth(t, server, tc.host, tc.headers); code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", code)
			}
		})
	}
}

func TestAPIAuthRejectsRequestsWhenEnabledWithoutKey(t *testing.T) {
	server := NewLaunchServer(nil, nil, nil, 0)
	server.SetAPIAuthConfig(APIAuthConfig{Enabled: true})

	if code := serveGuardedHealth(t, server, "127.0.0.1:19876", nil); code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
}

func TestAPIAuthAcceptsConfiguredKey(t *testing.T) {
	server := NewLaunchServer(nil, nil, nil, 0)
	server.SetAPIAuthConfig(APIAuthConfig{Enabled: true, APIKey: "secret"})

	if code := serveGuardedHealth(t, server, "127.0.0.1:19876", nil); code != http.StatusUnauthorized {
		t.Fatalf("missing key status = %d, want 401", code)
	}
	if code := serveGuardedHealth(t, server, "127.0.0.1:19876", map[string]string{DefaultAPIKeyHeader: "secret"}); code != http.StatusOK {
		t.Fatalf("valid key status = %d, want 200", code)
	}
}
