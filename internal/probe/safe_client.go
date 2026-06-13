package probe

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"monitor/internal/monitor"
)

// newSafeHTTPClient 创建专用的安全 HTTP 客户端：
// - 禁用重定向（避免 3xx 跳转绕过 SSRF 校验）
// - 自定义 DialContext：在实际连接时校验目标 IP（防 DNS rebinding）
// - 禁用环境代理（避免通过代理访问内网资源）
func newSafeHTTPClient(guard *SSRFGuard) *http.Client {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		Proxy: nil, // 禁用代理，避免绕过 SSRF 防护
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("invalid address: %w", err)
			}

			// 先做一次解析校验（防止解析直接指向内网）
			if ip := net.ParseIP(host); ip != nil {
				if !ip.IsGlobalUnicast() || guard.isPrivateIP(ip) {
					return nil, fmt.Errorf("blocked IP: %s", ip.String())
				}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err != nil {
					return nil, err
				}
				// 连接后再次校验 remote IP（兜底抵御 DNS rebinding/解析差异）
				if tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
					if tcpAddr.IP == nil || !tcpAddr.IP.IsGlobalUnicast() || guard.isPrivateIP(tcpAddr.IP) {
						_ = conn.Close()
						return nil, fmt.Errorf("blocked remote IP: %s", tcpAddr.IP.String())
					}
				}
				return conn, nil
			}

			// 域名需要先解析
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("DNS lookup failed for %s: %w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("DNS lookup returned no IP addresses for %s", host)
			}

			// 尝试连接每个解析出的 IP（仅尝试公网 IP）
			var lastErr error
			for _, ip := range ips {
				if ip == nil || !ip.IsGlobalUnicast() || guard.isPrivateIP(ip) {
					continue
				}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err != nil {
					lastErr = err
					continue
				}
				// 连接成功后再次校验 remote IP
				if tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
					if tcpAddr.IP == nil || !tcpAddr.IP.IsGlobalUnicast() || guard.isPrivateIP(tcpAddr.IP) {
						_ = conn.Close()
						lastErr = fmt.Errorf("blocked remote IP: %s", tcpAddr.IP.String())
						continue
					}
				}
				return conn, nil
			}

			if lastErr != nil {
				return nil, lastErr
			}
			return nil, fmt.Errorf("no public IPs available for host: %s", host)
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       30 * time.Second,
		// 与定时探测保持同一口径：每次测试都走新连接。
		DisableKeepAlives: true,
		// 显式禁用 HTTP/2，避免多路复用导致"冷启动"口径失真。
		TLSNextProto: make(map[string]func(string, *tls.Conn) http.RoundTripper),
	}

	return &http.Client{
		Transport: transport,
		// 禁用自动重定向（避免跳转到内网绕过 SSRF 校验）
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// newProxyHTTPClient 创建**仅供管理员 inline 探测**使用的显式代理客户端。
//
// 不变量：
//   - 代理模式下上游目标 IP 由代理负责解析/连接，safe DialContext 的上游 SSRF 校验天然失效；
//     这里**不**额外加严 proxy host 校验——proxy 恒来自管理员保存的通道配置（请求不可覆盖），
//     与 scheduler 真实探测用的 cfg.Proxy 完全同源；若比 scheduler 更严会出现"调度能探、
//     管理员测失败"的口径不一致。
//   - 仍保留 inline safe client 的诊断口径：禁自动重定向、TLS 握手 / 响应头超时；禁 keep-alive
//     与 HTTP/2 由 monitor.NewExplicitProxyTransport 的基础配置保证。
func newProxyHTTPClient(proxyURL string) (*http.Client, error) {
	transport, err := monitor.NewExplicitProxyTransport(proxyURL)
	if err != nil {
		return nil, err
	}
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 10 * time.Second

	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}
