package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"monitor/internal/logger"
)

// isValidCategory 检查 category 是否为有效值
func isValidCategory(category string) bool {
	normalized := strings.ToLower(strings.TrimSpace(category))
	return normalized == "commercial" || normalized == "public"
}

// validateURL 验证 URL 格式和协议安全性
func validateURL(rawURL, fieldName string) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil
	}

	parsed, err := url.ParseRequestURI(trimmed)
	if err != nil {
		return fmt.Errorf("%s 格式无效: %w", fieldName, err)
	}

	// 只允许 http 和 https 协议
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("%s 只支持 http:// 或 https:// 协议，收到: %s", fieldName, parsed.Scheme)
	}

	// 非 HTTPS 警告
	if scheme == "http" {
		logger.Warn("config", "检测到非 HTTPS URL", "field", fieldName, "url", trimmed)
	}

	return nil
}

// validateNoPrivateIPURL 检查 URL 主机部分是否为私有/特殊网络 IP。
// 仅检查 IP 字面量，不做 DNS 解析；hostname 直接跳过。
func validateNoPrivateIPURL(rawURL, fieldName string) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil // 语法问题由其他校验器处理
	}

	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return nil
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return nil // hostname 不做 DNS 解析
	}

	if isPrivateOrSpecialIP(ip) {
		return fmt.Errorf("%s 指向私有/特殊网络 IP %s", fieldName, host)
	}
	return nil
}

// isPrivateOrSpecialIP 检查 IP 是否属于私有或特殊用途网段
func isPrivateOrSpecialIP(ip net.IP) bool {
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	// RFC 6598 Carrier-Grade NAT: 100.64.0.0/10
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return true
		}
	}
	return false
}

// ValidateProviderSlug 验证已规范化的 provider slug（URL/机器键 slug）格式：仅 [a-z0-9-]、
// 不允许连续连字符、不以连字符开头或结尾、长度 ≤100。**不**负责 TrimSpace/小写化（调用方保证）。
// 作为 loader 归一化的权威 slug 规则，同时供 onboarding 发布门复用以在写盘前 fail-closed
// （否则派生出 a--b/超长的 slug 会「写盘成功、热加载被拒」）。
func ValidateProviderSlug(slug string) error {
	if slug == "" {
		return fmt.Errorf("slug 不能为空")
	}

	// 检查长度上限
	if len(slug) > 100 {
		return fmt.Errorf("长度超过限制（当前 %d，最大 100）", len(slug))
	}

	// 检查字符合法性
	prevIsHyphen := false
	for i, c := range slug {
		isLower := c >= 'a' && c <= 'z'
		isDigit := c >= '0' && c <= '9'
		isHyphen := c == '-'

		if !isLower && !isDigit && !isHyphen {
			return fmt.Errorf("包含非法字符 '%c' (位置 %d)，仅允许小写字母、数字、连字符", c, i)
		}

		// 检查连续连字符
		if isHyphen && prevIsHyphen {
			return fmt.Errorf("不允许连续连字符（位置 %d）", i)
		}

		prevIsHyphen = isHyphen
	}

	// 不能以连字符开头或结尾
	if slug[0] == '-' || slug[len(slug)-1] == '-' {
		return fmt.Errorf("不能以连字符开头或结尾")
	}

	return nil
}

// validateBaseURL 验证 baseURL 格式和协议
func validateBaseURL(baseURL string) error {
	if baseURL == "" {
		return fmt.Errorf("baseURL 不能为空")
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("URL 格式无效: %w", err)
	}

	// 只允许 http 和 https 协议
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("只支持 http:// 或 https:// 协议，收到: %s", parsed.Scheme)
	}

	// Host 不能为空
	if parsed.Host == "" {
		return fmt.Errorf("URL 缺少主机名")
	}

	// 非 HTTPS 警告
	if scheme == "http" {
		logger.Warn("config", "public_base_url 使用了非加密的 http:// 协议", "url", baseURL)
	}

	return nil
}

// validateProxyURL 验证代理 URL 格式
// 支持 http, https, socks5, socks 协议
func validateProxyURL(rawURL string) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("proxy 格式无效: %w", err)
	}

	// 检查协议
	// 注意：url.Parse 对于 "proxy.example.com:8080" 会将其解析为 Opaque URL，
	// 此时 Scheme 为 "proxy.example.com"，Host 为空。需要检测这种情况。
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "" {
		return fmt.Errorf("proxy 缺少协议（需要 http://, https://, socks5:// 或 socks://）")
	}

	switch scheme {
	case "http", "https", "socks5", "socks":
		// 有效协议（socks 是 socks5 的别名）
	default:
		return fmt.Errorf("proxy 协议无效 '%s'（仅支持 http, https, socks5, socks）", scheme)
	}

	// Host 不能为空（包括检测 Opaque URL 的情况）
	// 对于 "proxy.example.com:8080"，parsed.Host 为空但 parsed.Opaque 不为空
	if parsed.Host == "" {
		return fmt.Errorf("proxy 缺少主机地址（需要 host:port）")
	}

	// Hostname 不能为空（捕获 "socks5://:1080" 这种情况）
	// parsed.Host = ":1080"，但 parsed.Hostname() = ""
	if parsed.Hostname() == "" {
		return fmt.Errorf("proxy 缺少主机名")
	}

	// SOCKS5 代理必须指定端口（proxy.SOCKS5 需要完整的 host:port）
	if (scheme == "socks5" || scheme == "socks") && parsed.Port() == "" {
		return fmt.Errorf("proxy SOCKS5 代理必须指定端口（如 socks5://host:1080）")
	}

	// 不允许 path（容忍结尾的 "/"）
	if parsed.Path != "" && parsed.Path != "/" {
		return fmt.Errorf("proxy 不支持路径（path）")
	}

	// 不允许 query/fragment
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("proxy 不支持 query/fragment")
	}

	return nil
}
