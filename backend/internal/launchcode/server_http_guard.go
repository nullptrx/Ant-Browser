package launchcode

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// browserRequestGuardMiddleware 拒绝来自网页的跨站请求和 DNS rebinding 请求。
// Launch API 只服务本机脚本与工具；任意网页都能向 127.0.0.1 发请求，
// 仅校验来源 IP 无法区分“本机脚本”和“用户浏览器里打开的恶意页面”。
func (s *LaunchServer) browserRequestGuardMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reason := browserRequestRejectReason(r); reason != "" {
			writeJSON(w, http.StatusForbidden, map[string]interface{}{
				"ok":    false,
				"error": "forbidden: " + reason,
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func browserRequestRejectReason(r *http.Request) string {
	// DNS rebinding 时 Host 为攻击者域名；HTTP/1.0 无 Host 的本地工具放行。
	if host := strings.TrimSpace(r.Host); host != "" && !isLoopbackHost(host) {
		return "host must be 127.0.0.1 or localhost"
	}

	// 现代浏览器对所有请求（含 <img>、表单等无 Origin 的 GET）都会携带 Sec-Fetch-Site；
	// none 表示用户在地址栏直接打开，非浏览器客户端不发送该头。
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
	case "", "none", "same-origin":
	default:
		return "cross-site browser requests are not allowed"
	}

	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && !isSameLoopbackOrigin(origin, r.Host) {
		return "cross-origin browser requests are not allowed"
	}
	return ""
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isSameLoopbackOrigin(origin, requestHost string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		return false
	}
	return isLoopbackHost(parsed.Host) && strings.EqualFold(parsed.Host, strings.TrimSpace(requestHost))
}
