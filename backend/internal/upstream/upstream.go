// Package upstream 保存全局前置代理（仅支持 HTTP / SOCKS5），为后端所有出站连接提供统一的拨号器与 HTTP 代理设置。
// 启用后所有出站都必须经过前置代理，连接失败不会回退直连；访问本机回环地址不受影响。
package upstream

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	xproxy "golang.org/x/net/proxy"
)

const (
	SchemeHTTP   = "http"
	SchemeSOCKS5 = "socks5"
)

// Proxy 描述一个前置代理。
type Proxy struct {
	Scheme   string
	Host     string
	Port     string
	Username string
	Password string
}

// Parse 解析 http://[user:pass@]host:port 或 socks5://[user:pass@]host:port。
func Parse(raw string) (*Proxy, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("前置代理地址为空")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("前置代理地址格式错误: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http":
		scheme = SchemeHTTP
	case "socks5", "socks5h", "socks":
		scheme = SchemeSOCKS5
	default:
		return nil, fmt.Errorf("前置代理仅支持 http:// 或 socks5://，当前为 %q", u.Scheme)
	}
	host := u.Hostname()
	port := u.Port()
	if host == "" || port == "" {
		return nil, errors.New("前置代理地址需包含主机和端口，如 socks5://127.0.0.1:7890")
	}
	p := &Proxy{Scheme: scheme, Host: host, Port: port}
	if u.User != nil {
		p.Username = u.User.Username()
		p.Password, _ = u.User.Password()
	}
	return p, nil
}

// Address 返回 host:port。
func (p *Proxy) Address() string {
	return net.JoinHostPort(p.Host, p.Port)
}

// URL 返回带凭据的代理地址，可直接用于 http.Transport.Proxy。
func (p *Proxy) URL() *url.URL {
	u := &url.URL{Scheme: p.Scheme, Host: p.Address()}
	if p.Username != "" || p.Password != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	return u
}

// HasAuth 表示前置代理是否需要账号密码。
func (p *Proxy) HasAuth() bool {
	return p.Username != "" || p.Password != ""
}

// ToolURL 返回给外部工具（如 git / curl）使用的代理地址；SOCKS5 使用 socks5h 让代理端解析域名，避免本地 DNS 泄漏。
func (p *Proxy) ToolURL() string {
	u := p.URL()
	if p.Scheme == SchemeSOCKS5 {
		u.Scheme = "socks5h"
	}
	return u.String()
}

// Fingerprint 用于区分不同前置代理配置（如桥接进程的复用 key），不暴露明文凭据。
func (p *Proxy) Fingerprint() string {
	sum := sha256.Sum256([]byte(p.URL().String()))
	return hex.EncodeToString(sum[:8])
}

// Source 返回当前配置中的前置代理开关与地址，每次取用时读取，配置被整体替换后也能立即生效。
type Source func() (enabled bool, raw string)

type state struct {
	enabled bool
	raw     string
	proxy   *Proxy
	err     error
}

var (
	source atomic.Pointer[Source]
	cached atomic.Pointer[state]
)

// SetSource 设置前置代理配置来源（应用启动时设置一次）。
func SetSource(src Source) {
	if src == nil {
		source.Store(nil)
		return
	}
	source.Store(&src)
}

// Configure 直接指定前置代理（测试或无配置来源时使用），会清除 SetSource。
func Configure(enabled bool, raw string) error {
	source.Store(nil)
	st := resolve(enabled, raw)
	cached.Store(st)
	return st.err
}

func resolve(enabled bool, raw string) *state {
	raw = strings.TrimSpace(raw)
	st := &state{enabled: enabled, raw: raw}
	if !enabled {
		return st
	}
	if raw == "" {
		st.err = errors.New("已启用前置代理但未填写地址")
		return st
	}
	st.proxy, st.err = Parse(raw)
	return st
}

func load() *state {
	if srcPtr := source.Load(); srcPtr != nil {
		enabled, raw := (*srcPtr)()
		raw = strings.TrimSpace(raw)
		if st := cached.Load(); st != nil && st.enabled == enabled && st.raw == raw {
			return st
		}
		st := resolve(enabled, raw)
		cached.Store(st)
		return st
	}
	if st := cached.Load(); st != nil {
		return st
	}
	return &state{}
}

// Current 返回当前前置代理，未启用（或配置无效）时为 nil。
func Current() *Proxy {
	return load().proxy
}

// ConfigError 在启用了前置代理但地址无效时返回错误；调用方应拒绝出站而不是回退直连。
func ConfigError() error {
	if err := load().err; err != nil {
		return fmt.Errorf("前置代理配置无效: %w", err)
	}
	return nil
}

// KeySuffix 返回参与桥接 key 计算的前置代理标识，前置代理变化时桥接会重建。
func KeySuffix() string {
	if p := Current(); p != nil {
		return "\x00upstream:" + p.Fingerprint()
	}
	return ""
}

var loopbackBypassDisabled atomic.Bool

// SetLoopbackBypassForTesting 仅供测试：关闭回环地址豁免，让发往 127.0.0.1 的测试流量也经过前置代理。
func SetLoopbackBypassForTesting(enabled bool) {
	loopbackBypassDisabled.Store(!enabled)
}

// IsLoopbackHost 判断目标是否为本机回环地址（这类连接不走前置代理）。
func IsLoopbackHost(host string) bool {
	if loopbackBypassDisabled.Load() {
		return false
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return false
}

// ProxyFunc 包装 http.Transport.Proxy：启用前置代理时非回环目标一律走前置代理，否则使用 fallback。
// 仅适用于没有自定义 DialContext 的 Transport，自定义拨号的场景请使用 Dialer。
func ProxyFunc(fallback func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		if req != nil && req.URL != nil && !IsLoopbackHost(req.URL.Hostname()) {
			if err := ConfigError(); err != nil {
				return nil, err
			}
			if p := Current(); p != nil {
				return p.URL(), nil
			}
		}
		if fallback != nil {
			return fallback(req)
		}
		return nil, nil
	}
}

// NewTransport 返回遵循前置代理的 Transport；未启用时沿用环境变量代理。
func NewTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = ProxyFunc(http.ProxyFromEnvironment)
	return t
}

// NewDirectTransport 返回遵循前置代理的 Transport；未启用时直连（不读环境变量代理）。
func NewDirectTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = ProxyFunc(nil)
	return t
}

// HTTPClient 返回遵循前置代理的 http.Client。
func HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: NewTransport()}
}

// Dialer 是遵循前置代理的 TCP 拨号器，同时实现 x/net/proxy 的 Dialer 与 ContextDialer。
type Dialer struct {
	Timeout time.Duration
}

func (d Dialer) Dial(network, addr string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, addr)
}

func (d Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	base := &net.Dialer{Timeout: d.Timeout}
	p := Current()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if IsLoopbackHost(host) {
		return base.DialContext(ctx, network, addr)
	}
	if err := ConfigError(); err != nil {
		return nil, err
	}
	if p == nil {
		return base.DialContext(ctx, network, addr)
	}
	if !strings.HasPrefix(network, "tcp") {
		return nil, fmt.Errorf("前置代理仅支持 TCP 连接，无法拨号 %s %s", network, addr)
	}
	if p.Scheme == SchemeSOCKS5 {
		var auth *xproxy.Auth
		if p.HasAuth() {
			auth = &xproxy.Auth{User: p.Username, Password: p.Password}
		}
		socks, err := xproxy.SOCKS5("tcp", p.Address(), auth, base)
		if err != nil {
			return nil, err
		}
		conn, err := socks.(xproxy.ContextDialer).DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("经前置代理连接 %s 失败: %w", addr, err)
		}
		return conn, nil
	}
	return dialHTTPConnect(ctx, base, p, addr)
}

func dialHTTPConnect(ctx context.Context, base *net.Dialer, p *Proxy, addr string) (net.Conn, error) {
	conn, err := base.DialContext(ctx, "tcp", p.Address())
	if err != nil {
		return nil, fmt.Errorf("连接前置代理 %s 失败: %w", p.Address(), err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
		defer conn.SetDeadline(time.Time{})
	}
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	if p.HasAuth() {
		token := base64.StdEncoding.EncodeToString([]byte(p.Username + ":" + p.Password))
		req.Header.Set("Proxy-Authorization", "Basic "+token)
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("向前置代理发送 CONNECT 失败: %w", err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("读取前置代理 CONNECT 响应失败: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("前置代理拒绝连接 %s: %s", addr, resp.Status)
	}
	if reader.Buffered() > 0 {
		return &bufferedConn{Conn: conn, reader: reader}, nil
	}
	return conn, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}
