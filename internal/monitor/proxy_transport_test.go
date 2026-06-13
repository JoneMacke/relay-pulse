package monitor

import (
	"net/http"
	"testing"
)

func TestNewExplicitProxyTransport(t *testing.T) {
	t.Run("空 URL 必须报错（绝不回退 ProxyFromEnvironment）", func(t *testing.T) {
		if _, err := NewExplicitProxyTransport(""); err == nil {
			t.Fatal("空 proxyURL 应返回错误")
		}
		if _, err := NewExplicitProxyTransport("   "); err == nil {
			t.Fatal("纯空白 proxyURL 应返回错误")
		}
	})

	t.Run("http 代理设置 Proxy 函数且禁 keep-alive/HTTP2", func(t *testing.T) {
		tr, err := NewExplicitProxyTransport("http://proxy.example.com:8080")
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		if tr.Proxy == nil {
			t.Error("http 代理应设置 Proxy 函数")
		}
		if !tr.DisableKeepAlives {
			t.Error("应禁用 keep-alive（冷启动口径）")
		}
		if tr.TLSNextProto == nil || len(tr.TLSNextProto) != 0 {
			t.Error("应显式禁用 HTTP/2（空 TLSNextProto map）")
		}
	})

	t.Run("socks5 代理走 DialContext", func(t *testing.T) {
		tr, err := NewExplicitProxyTransport("socks5://user:pass@127.0.0.1:1080")
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		if tr.DialContext == nil && tr.Dial == nil { //nolint:staticcheck
			t.Error("socks5 应设置 dialer")
		}
		if tr.Proxy != nil {
			t.Error("socks5 不应设置 http Proxy 函数")
		}
	})

	t.Run("不支持的协议报错", func(t *testing.T) {
		if _, err := NewExplicitProxyTransport("ftp://x:1"); err == nil {
			t.Error("不支持的协议应报错")
		}
	})
}

// 确保返回值满足 RoundTripper（createTransport 委托它时的隐式约束）。
var _ http.RoundTripper = (*http.Transport)(nil)
