// Package upstreamtest 提供测试用的 HTTP / SOCKS5 代理服务器，记录经过的连接，用于验证流量是否走了前置代理。
package upstreamtest

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Server 是一个测试代理服务器。
type Server struct {
	// URL 为带凭据的代理地址，如 socks5://u:p@127.0.0.1:1080。
	URL      string
	Addr     string
	hits     atomic.Int64
	mu       sync.Mutex
	targets  []string
	listener net.Listener
}

// Hits 返回成功建立的代理连接数。
func (s *Server) Hits() int64 {
	return s.hits.Load()
}

// Targets 返回经过该代理访问过的目标地址。
func (s *Server) Targets() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.targets...)
}

func (s *Server) record(target string) {
	s.hits.Add(1)
	s.mu.Lock()
	s.targets = append(s.targets, target)
	s.mu.Unlock()
}

func listen(t testing.TB) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

func proxyURL(scheme, user, pass, addr string) string {
	if user == "" && pass == "" {
		return scheme + "://" + addr
	}
	return scheme + "://" + user + ":" + pass + "@" + addr
}

func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
	a.Close()
	b.Close()
}

// StartHTTP 启动一个 HTTP 代理（支持 CONNECT 与普通 HTTP 转发），user/pass 非空时校验 Basic 认证。
func StartHTTP(t testing.TB, user, pass string) *Server {
	t.Helper()
	ln := listen(t)
	s := &Server{Addr: ln.Addr().String(), listener: ln}
	s.URL = proxyURL("http", user, pass, s.Addr)
	expected := ""
	if user != "" || pass != "" {
		expected = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expected != "" && r.Header.Get("Proxy-Authorization") != expected {
			w.Header().Set("Proxy-Authenticate", `Basic realm="test"`)
			http.Error(w, "proxy auth required", http.StatusProxyAuthRequired)
			return
		}
		if r.Method == http.MethodConnect {
			target, err := net.Dial("tcp", r.Host)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				target.Close()
				http.Error(w, "hijack unsupported", http.StatusInternalServerError)
				return
			}
			client, _, err := hijacker.Hijack()
			if err != nil {
				target.Close()
				return
			}
			s.record(r.Host)
			_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
			pipe(client, target)
			return
		}
		r.RequestURI = ""
		r.Header.Del("Proxy-Authorization")
		resp, err := (&http.Transport{Proxy: nil}).RoundTrip(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		s.record(r.URL.Host)
		for k, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return s
}

// StartSOCKS5 启动一个仅支持 CONNECT 的 SOCKS5 代理，user/pass 非空时要求用户名密码认证。
func StartSOCKS5(t testing.TB, user, pass string) *Server {
	t.Helper()
	ln := listen(t)
	s := &Server{Addr: ln.Addr().String(), listener: ln}
	s.URL = proxyURL("socks5", user, pass, s.Addr)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serveSOCKS5(conn, user, pass)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *Server) serveSOCKS5(conn net.Conn, user, pass string) {
	r := bufio.NewReader(conn)
	fail := func() { conn.Close() }
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil || header[0] != 5 {
		fail()
		return
	}
	methods := make([]byte, header[1])
	if _, err := io.ReadFull(r, methods); err != nil {
		fail()
		return
	}
	needAuth := user != "" || pass != ""
	want := byte(0x00)
	if needAuth {
		want = 0x02
	}
	if !strings.ContainsRune(string(methods), rune(want)) {
		_, _ = conn.Write([]byte{5, 0xff})
		fail()
		return
	}
	_, _ = conn.Write([]byte{5, want})
	if needAuth {
		ver := make([]byte, 2)
		if _, err := io.ReadFull(r, ver); err != nil {
			fail()
			return
		}
		name := make([]byte, ver[1])
		_, _ = io.ReadFull(r, name)
		plen := make([]byte, 1)
		_, _ = io.ReadFull(r, plen)
		pw := make([]byte, plen[0])
		_, _ = io.ReadFull(r, pw)
		if string(name) != user || string(pw) != pass {
			_, _ = conn.Write([]byte{1, 1})
			fail()
			return
		}
		_, _ = conn.Write([]byte{1, 0})
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(r, req); err != nil || req[1] != 1 {
		_, _ = conn.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		fail()
		return
	}
	var host string
	switch req[3] {
	case 1:
		ip := make([]byte, 4)
		_, _ = io.ReadFull(r, ip)
		host = net.IP(ip).String()
	case 3:
		l := make([]byte, 1)
		_, _ = io.ReadFull(r, l)
		name := make([]byte, l[0])
		_, _ = io.ReadFull(r, name)
		host = string(name)
	case 4:
		ip := make([]byte, 16)
		_, _ = io.ReadFull(r, ip)
		host = net.IP(ip).String()
	default:
		fail()
		return
	}
	portBytes := make([]byte, 2)
	_, _ = io.ReadFull(r, portBytes)
	target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBytes))))
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		_, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		fail()
		return
	}
	s.record(target)
	_, _ = conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	if r.Buffered() > 0 {
		buffered, _ := r.Peek(r.Buffered())
		_, _ = upstream.Write(buffered)
	}
	pipe(conn, upstream)
}
