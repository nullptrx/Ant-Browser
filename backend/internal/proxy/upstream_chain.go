package proxy

import (
	"fmt"
	"strconv"
	"strings"

	"ant-chrome/backend/internal/upstream"
)

// upstreamOutboundTag 是各内核配置中前置代理出站的标签 / 名称。
const upstreamOutboundTag = "ant-upstream"

// UpstreamDirectReplacement 在启用前置代理时把直连（空或 direct://）替换为前置代理本身，保证直连流量也经过前置代理。
func UpstreamDirectReplacement(src string) string {
	p := upstream.Current()
	if p == nil {
		return src
	}
	trimmed := strings.TrimSpace(src)
	if trimmed == "" || strings.EqualFold(trimmed, "direct://") {
		return p.URL().String()
	}
	return src
}

// upstreamIsSelf 判断代理服务器是否就是前置代理本身（直连改走前置代理时桥接的就是它，不能再叠加一层）。
func upstreamIsSelf(p *upstream.Proxy, host string, port string) bool {
	if p == nil {
		return false
	}
	return strings.EqualFold(strings.Trim(strings.TrimSpace(host), "[]"), p.Host) && strings.TrimSpace(port) == p.Port
}

// upstreamUDPError 前置代理为 HTTP 时无法承载基于 UDP 的协议。
func upstreamUDPError(p *upstream.Proxy, protocol string) error {
	if p == nil || p.Scheme != upstream.SchemeHTTP {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "hysteria", "hysteria2", "hy2", "tuic", "wireguard":
		return fmt.Errorf("前置代理为 HTTP，无法承载基于 UDP 的 %s 协议，请改用 SOCKS5 前置代理", protocol)
	}
	return nil
}

func portString(value interface{}) string {
	switch v := value.(type) {
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.Itoa(int(v))
	case string:
		return strings.TrimSpace(v)
	}
	return ""
}

func portInt(p *upstream.Proxy) int {
	port, _ := strconv.Atoi(p.Port)
	return port
}

// ---------- Xray ----------

func xrayUpstreamOutbound(p *upstream.Proxy) map[string]interface{} {
	server := map[string]interface{}{"address": p.Host, "port": portInt(p)}
	if p.HasAuth() {
		server["users"] = []interface{}{map[string]interface{}{"user": p.Username, "pass": p.Password}}
	}
	protocol := "socks"
	if p.Scheme == upstream.SchemeHTTP {
		protocol = "http"
	}
	return map[string]interface{}{
		"protocol": protocol,
		"tag":      upstreamOutboundTag,
		"settings": map[string]interface{}{"servers": []interface{}{server}},
	}
}

// xrayOutboundServer 返回出站直连的服务器地址（socks/http/trojan/ss 用 servers，vmess/vless 用 vnext）。
func xrayOutboundServer(outbound map[string]interface{}) (string, string) {
	settings, _ := outbound["settings"].(map[string]interface{})
	for _, field := range []string{"servers", "vnext"} {
		list, _ := settings[field].([]interface{})
		if len(list) == 0 {
			continue
		}
		if first, ok := list[0].(map[string]interface{}); ok {
			host, _ := first["address"].(string)
			return host, portString(first["port"])
		}
	}
	return "", ""
}

// applyXrayUpstream 让所有直接拨号服务器的出站通过 sockopt.dialerProxy 经前置代理连接。
// 已通过 proxySettings 依附其他出站的（如链式第二跳）由其所依附的出站负责，不重复处理。
// 返回能到达前置代理的出站标签（供 DNS 路由使用），未启用前置代理时为空。
func applyXrayUpstream(outbounds []interface{}) ([]interface{}, string, error) {
	p := upstream.Current()
	if err := upstream.ConfigError(); err != nil {
		return nil, "", err
	}
	if p == nil {
		return outbounds, "", nil
	}
	chained := false
	selfTag := ""
	for _, item := range outbounds {
		outbound, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		protocol, _ := outbound["protocol"].(string)
		switch protocol {
		case "freedom", "direct", "blackhole", "dns":
			continue
		}
		if _, hasProxySettings := outbound["proxySettings"]; hasProxySettings {
			continue
		}
		host, port := xrayOutboundServer(outbound)
		if upstreamIsSelf(p, host, port) {
			selfTag, _ = outbound["tag"].(string)
			continue
		}
		stream, _ := outbound["streamSettings"].(map[string]interface{})
		if stream == nil {
			stream = map[string]interface{}{}
			outbound["streamSettings"] = stream
		}
		sockopt, _ := stream["sockopt"].(map[string]interface{})
		if sockopt == nil {
			sockopt = map[string]interface{}{}
			stream["sockopt"] = sockopt
		}
		sockopt["dialerProxy"] = upstreamOutboundTag
		chained = true
	}
	if chained {
		return append(outbounds, xrayUpstreamOutbound(p)), upstreamOutboundTag, nil
	}
	return outbounds, selfTag, nil
}

// xrayUpstreamDNS 启用前置代理时 Xray 内置 DNS 改用 TCP 查询并经 viaTag（通往前置代理的出站）发出，避免直连 DNS 泄漏。
func xrayUpstreamDNS(cfg map[string]interface{}, viaTag string) {
	if upstream.Current() == nil || viaTag == "" {
		return
	}
	cfg["dns"] = map[string]interface{}{
		"tag":     "dns-internal",
		"servers": []interface{}{"tcp://1.1.1.1:53", "tcp://8.8.8.8:53"},
	}
	routing, _ := cfg["routing"].(map[string]interface{})
	if routing == nil {
		routing = map[string]interface{}{}
		cfg["routing"] = routing
	}
	rules, _ := routing["rules"].([]interface{})
	routing["rules"] = append([]interface{}{map[string]interface{}{
		"type":        "field",
		"inboundTag":  []string{"dns-internal"},
		"outboundTag": viaTag,
	}}, rules...)
}

// ---------- sing-box ----------

func singBoxUpstreamOutbound(p *upstream.Proxy) map[string]interface{} {
	outbound := map[string]interface{}{
		"tag":         upstreamOutboundTag,
		"server":      p.Host,
		"server_port": portInt(p),
	}
	if p.Scheme == upstream.SchemeHTTP {
		outbound["type"] = "http"
	} else {
		outbound["type"] = "socks"
		outbound["version"] = "5"
	}
	if p.HasAuth() {
		outbound["username"] = p.Username
		outbound["password"] = p.Password
	}
	return outbound
}

// applySingBoxUpstream 让节点出站经 detour 走前置代理，DNS 也改为经前置代理的 TCP 查询。
func applySingBoxUpstream(cfg map[string]interface{}, node map[string]interface{}) error {
	if err := upstream.ConfigError(); err != nil {
		return err
	}
	p := upstream.Current()
	if p == nil {
		return nil
	}
	nodeType, _ := node["type"].(string)
	if err := upstreamUDPError(p, nodeType); err != nil {
		return err
	}
	node["detour"] = upstreamOutboundTag
	outbounds, _ := cfg["outbounds"].([]interface{})
	cfg["outbounds"] = append(outbounds, singBoxUpstreamOutbound(p))
	cfg["dns"] = map[string]interface{}{
		"servers": []interface{}{
			map[string]interface{}{"type": "tcp", "tag": "public-dns", "server": "1.1.1.1", "detour": upstreamOutboundTag},
			map[string]interface{}{"type": "tcp", "tag": "backup-dns", "server": "8.8.8.8", "detour": upstreamOutboundTag},
		},
		"final":    "public-dns",
		"strategy": "ipv4_only",
	}
	if route, ok := cfg["route"].(map[string]interface{}); ok {
		if rules, ok := route["rules"].([]interface{}); ok {
			for _, item := range rules {
				if rule, ok := item.(map[string]interface{}); ok && rule["protocol"] == "dns" {
					rule["outbound"] = upstreamOutboundTag
				}
			}
		}
	}
	return nil
}

// ---------- mihomo ----------

func mihomoUpstreamProxy(p *upstream.Proxy) map[string]interface{} {
	entry := map[string]interface{}{
		"name":   upstreamOutboundTag,
		"server": p.Host,
		"port":   portInt(p),
	}
	if p.Scheme == upstream.SchemeHTTP {
		entry["type"] = "http"
	} else {
		entry["type"] = "socks5"
		entry["udp"] = true
	}
	if p.HasAuth() {
		entry["username"] = p.Username
		entry["password"] = p.Password
	}
	return entry
}

// applyMihomoUpstream 给节点加上 dialer-proxy 经前置代理连接，返回需要额外写入 proxies 的条目。
func applyMihomoUpstream(node map[string]interface{}) ([]interface{}, error) {
	if err := upstream.ConfigError(); err != nil {
		return nil, err
	}
	p := upstream.Current()
	if p == nil {
		return nil, nil
	}
	server, _ := node["server"].(string)
	if upstreamIsSelf(p, server, portString(node["port"])) {
		return nil, nil
	}
	nodeType, _ := node["type"].(string)
	if err := upstreamUDPError(p, nodeType); err != nil {
		return nil, err
	}
	node["dialer-proxy"] = upstreamOutboundTag
	return []interface{}{mihomoUpstreamProxy(p)}, nil
}
