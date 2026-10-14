package backend

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ant-chrome/backend/internal/logger"
	"ant-chrome/backend/internal/upstream"
)

const (
	upstreamProxyTestURL       = "http://www.gstatic.com/generate_204"
	upstreamProxyTestTimeout   = 10 * time.Second
	browserCoreReleaseListURL  = "https://api.github.com/repos/adryfish/fingerprint-chromium/releases?per_page=30"
	browserCoreReleaseMaxBytes = 8 << 20
)

// UpstreamProxySettings 前置代理设置（仅支持 http:// 与 socks5://）。
type UpstreamProxySettings struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
}

// UpstreamProxyTestResult 前置代理连通性测试结果。
type UpstreamProxyTestResult struct {
	Ok        bool   `json:"ok"`
	LatencyMs int64  `json:"latencyMs"`
	Error     string `json:"error"`
}

// GetUpstreamProxySettings 读取前置代理设置。
func (a *App) GetUpstreamProxySettings() UpstreamProxySettings {
	if a.config == nil {
		return UpstreamProxySettings{}
	}
	return UpstreamProxySettings{Enabled: a.config.UpstreamProxy.Enabled, URL: a.config.UpstreamProxy.URL}
}

// SaveUpstreamProxySettings 保存前置代理设置；启用时校验地址。运行中的实例与已建立的桥接保持原状，重启后生效。
func (a *App) SaveUpstreamProxySettings(settings UpstreamProxySettings) error {
	if a.config == nil {
		return fmt.Errorf("配置未加载")
	}
	rawURL := strings.TrimSpace(settings.URL)
	// 关闭时也保留地址方便下次开启，但只要填了就必须合法，避免开启时才发现错误。
	if settings.Enabled || rawURL != "" {
		if _, err := upstream.Parse(rawURL); err != nil {
			return err
		}
	}
	a.config.UpstreamProxy.Enabled = settings.Enabled
	a.config.UpstreamProxy.URL = rawURL
	if err := a.config.Save(a.resolveAppPath("config.yaml")); err != nil {
		return fmt.Errorf("保存前置代理设置失败: %w", err)
	}
	logger.New("Upstream").Info("前置代理设置已保存", logger.F("enabled", settings.Enabled))
	return nil
}

// TestUpstreamProxy 用给定地址（不必已保存）经前置代理请求测试地址，返回连通性与延迟。
func (a *App) TestUpstreamProxy(rawURL string) UpstreamProxyTestResult {
	p, err := upstream.Parse(rawURL)
	if err != nil {
		return UpstreamProxyTestResult{Error: err.Error()}
	}
	client := &http.Client{
		Timeout:   upstreamProxyTestTimeout,
		Transport: &http.Transport{Proxy: http.ProxyURL(p.URL())},
	}
	start := time.Now()
	resp, err := client.Get(upstreamProxyTestURL)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return UpstreamProxyTestResult{LatencyMs: latency, Error: err.Error()}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return UpstreamProxyTestResult{LatencyMs: latency, Error: fmt.Sprintf("测试地址返回 %s", resp.Status)}
	}
	return UpstreamProxyTestResult{Ok: true, LatencyMs: latency}
}

// BrowserCoreReleaseList 代前端拉取浏览器内核 Release 列表（遵循前置代理），返回 GitHub API 原始 JSON。
func (a *App) BrowserCoreReleaseList() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, browserCoreReleaseListURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := upstream.HTTPClient(0).Do(req)
	if err != nil {
		return "", fmt.Errorf("获取 Release 列表失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub Releases 请求失败: %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, browserCoreReleaseMaxBytes))
	if err != nil {
		return "", fmt.Errorf("读取 Release 列表失败: %w", err)
	}
	return string(data), nil
}
