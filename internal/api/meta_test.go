package api

import (
	"strings"
	"testing"

	"monitor/internal/config"
)

// TestParseRequestPath 测试路径解析
func TestParseRequestPath(t *testing.T) {
	tests := []struct {
		name               string
		path               string
		expectedLang       string
		expectedSlug       string
		expectedIsProvider bool
	}{
		{
			name:               "中文首页",
			path:               "/",
			expectedLang:       "zh-CN",
			expectedSlug:       "",
			expectedIsProvider: false,
		},
		{
			name:               "英文首页",
			path:               "/en/",
			expectedLang:       "en-US",
			expectedSlug:       "",
			expectedIsProvider: false,
		},
		{
			name:               "俄文首页",
			path:               "/ru/",
			expectedLang:       "ru-RU",
			expectedSlug:       "",
			expectedIsProvider: false,
		},
		{
			name:               "日文首页",
			path:               "/ja/",
			expectedLang:       "ja-JP",
			expectedSlug:       "",
			expectedIsProvider: false,
		},
		{
			name:               "中文服务商页面",
			path:               "/p/foxcode",
			expectedLang:       "zh-CN",
			expectedSlug:       "foxcode",
			expectedIsProvider: true,
		},
		{
			name:               "英文服务商页面",
			path:               "/en/p/foxcode",
			expectedLang:       "en-US",
			expectedSlug:       "foxcode",
			expectedIsProvider: true,
		},
		{
			name:               "俄文服务商页面",
			path:               "/ru/p/88code",
			expectedLang:       "ru-RU",
			expectedSlug:       "88code",
			expectedIsProvider: true,
		},
		{
			name:               "日文服务商页面",
			path:               "/ja/p/easy-chat",
			expectedLang:       "ja-JP",
			expectedSlug:       "easy-chat",
			expectedIsProvider: true,
		},
		{
			name:               "无效语言前缀",
			path:               "/de/p/test",
			expectedLang:       "zh-CN",
			expectedSlug:       "",
			expectedIsProvider: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			langCode, slug, isProvider := parseRequestPath(tt.path)
			if langCode != tt.expectedLang {
				t.Errorf("parseRequestPath(%q) langCode = %v, want %v", tt.path, langCode, tt.expectedLang)
			}
			if slug != tt.expectedSlug {
				t.Errorf("parseRequestPath(%q) slug = %v, want %v", tt.path, slug, tt.expectedSlug)
			}
			if isProvider != tt.expectedIsProvider {
				t.Errorf("parseRequestPath(%q) isProvider = %v, want %v", tt.path, isProvider, tt.expectedIsProvider)
			}
		})
	}
}

// TestIsValidProviderSlug 测试 slug 验证
func TestIsValidProviderSlug(t *testing.T) {
	tests := []struct {
		name  string
		slug  string
		valid bool
	}{
		{
			name:  "有效 slug - 纯小写字母",
			slug:  "foxcode",
			valid: true,
		},
		{
			name:  "有效 slug - 数字开头",
			slug:  "88code",
			valid: true,
		},
		{
			name:  "有效 slug - 连字符分隔",
			slug:  "easy-chat",
			valid: true,
		},
		{
			name:  "有效 slug - 多个连字符",
			slug:  "my-super-provider-2024",
			valid: true,
		},
		{
			name:  "无效 slug - 空字符串",
			slug:  "",
			valid: false,
		},
		{
			name:  "无效 slug - 大写字母",
			slug:  "FoxCode",
			valid: false,
		},
		{
			name:  "无效 slug - 连字符开头",
			slug:  "-foxcode",
			valid: false,
		},
		{
			name:  "无效 slug - 连字符结尾",
			slug:  "foxcode-",
			valid: false,
		},
		{
			name:  "无效 slug - 包含特殊字符",
			slug:  "fox_code",
			valid: false,
		},
		{
			name:  "无效 slug - XSS 尝试",
			slug:  `"><script>alert(1)</script>`,
			valid: false,
		},
		{
			name:  "无效 slug - 路径穿越",
			slug:  "../../../etc/passwd",
			valid: false,
		},
		{
			name:  "无效 slug - 超长",
			slug:  strings.Repeat("a", 101),
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidProviderSlug(tt.slug)
			if result != tt.valid {
				t.Errorf("isValidProviderSlug(%q) = %v, want %v", tt.slug, result, tt.valid)
			}
		})
	}
}

// TestGetMetaContent 测试 meta 内容生成
func TestGetMetaContent(t *testing.T) {
	tests := []struct {
		name              string
		langCode          string
		slug              string
		providerName      string
		isProviderPage    bool
		staticPath        string
		expectedTitlePart string
		expectedDescPart  string
	}{
		{
			name:              "中文首页",
			langCode:          "zh-CN",
			slug:              "",
			providerName:      "",
			isProviderPage:    false,
			expectedTitlePart: "RelayPulse - 实时监测API中转服务可用性矩阵",
			expectedDescPart:  "实时监测全球 LLM 中转服务",
		},
		{
			name:              "英文首页",
			langCode:          "en-US",
			slug:              "",
			providerName:      "",
			isProviderPage:    false,
			expectedTitlePart: "RelayPulse - Real-time availability matrix",
			expectedDescPart:  "Real-time monitoring of LLM relay",
		},
		{
			name:              "中文联系页",
			langCode:          "zh-CN",
			isProviderPage:    false,
			staticPath:        "contact",
			expectedTitlePart: "联系我们 | RelayPulse",
			expectedDescPart:  "申请收录、变更通道配置或提交反馈",
		},
		{
			name:              "英文联系页",
			langCode:          "en-US",
			isProviderPage:    false,
			staticPath:        "contact",
			expectedTitlePart: "Contact Us | RelayPulse",
			expectedDescPart:  "Apply for listing, request changes, or submit feedback",
		},
		{
			name:              "俄文联系页",
			langCode:          "ru-RU",
			isProviderPage:    false,
			staticPath:        "contact",
			expectedTitlePart: "Связаться с нами | RelayPulse",
			expectedDescPart:  "Подать заявку на добавление",
		},
		{
			name:              "日文联系页",
			langCode:          "ja-JP",
			isProviderPage:    false,
			staticPath:        "contact",
			expectedTitlePart: "お問い合わせ | RelayPulse",
			expectedDescPart:  "掲載申請、変更リクエスト",
		},
		{
			name:              "中文服务商页面",
			langCode:          "zh-CN",
			slug:              "foxcode",
			providerName:      "FoxCode",
			isProviderPage:    true,
			expectedTitlePart: "FoxCode 服务可用性监测",
			expectedDescPart:  "实时监测 FoxCode 的 API 可用性",
		},
		{
			name:              "英文服务商页面",
			langCode:          "en-US",
			slug:              "foxcode",
			providerName:      "FoxCode",
			isProviderPage:    true,
			expectedTitlePart: "FoxCode Service Availability",
			expectedDescPart:  "Monitor FoxCode API availability",
		},
		{
			name:              "XSS 转义测试",
			langCode:          "zh-CN",
			slug:              "test",
			providerName:      `<script>alert(1)</script>`,
			isProviderPage:    true,
			expectedTitlePart: "&lt;script&gt;", // 应该被转义
			expectedDescPart:  "&lt;script&gt;",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := getMetaContent(tt.langCode, tt.slug, tt.providerName, tt.isProviderPage, tt.staticPath)

			if !strings.Contains(meta.Title, tt.expectedTitlePart) {
				t.Errorf("getMetaContent() title = %q, want to contain %q", meta.Title, tt.expectedTitlePart)
			}

			if !strings.Contains(meta.Description, tt.expectedDescPart) {
				t.Errorf("getMetaContent() description = %q, want to contain %q", meta.Description, tt.expectedDescPart)
			}

			if meta.Slug != tt.slug {
				t.Errorf("getMetaContent() slug = %q, want %q", meta.Slug, tt.slug)
			}

			if meta.ProviderName != tt.providerName {
				t.Errorf("getMetaContent() providerName = %q, want %q", meta.ProviderName, tt.providerName)
			}

			if meta.StaticPath != tt.staticPath {
				t.Errorf("getMetaContent() staticPath = %q, want %q", meta.StaticPath, tt.staticPath)
			}
		})
	}
}

// TestReplaceHtmlLang 测试 HTML lang 属性替换
func TestReplaceHtmlLang(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		newLang  string
		expected string
	}{
		{
			name:     "替换为中文",
			html:     `<html lang="en">`,
			newLang:  "zh-CN",
			expected: `<html lang="zh-CN">`,
		},
		{
			name:     "替换为英文",
			html:     `<html lang="zh-CN">`,
			newLang:  "en-US",
			expected: `<html lang="en-US">`,
		},
		{
			name:     "完整 HTML 文档",
			html:     `<!doctype html><html lang="en"><head></head></html>`,
			newLang:  "ru-RU",
			expected: `<!doctype html><html lang="ru-RU"><head></head></html>`,
		},
		{
			name:     "未找到 lang 属性",
			html:     `<html><head></head></html>`,
			newLang:  "zh-CN",
			expected: `<html><head></head></html>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := replaceHtmlLang(tt.html, tt.newLang)
			if result != tt.expected {
				t.Errorf("replaceHtmlLang() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestReplaceBetween 测试通用内容替换
func TestReplaceBetween(t *testing.T) {
	tests := []struct {
		name       string
		html       string
		start      string
		end        string
		newContent string
		expected   string
	}{
		{
			name:       "替换 title",
			html:       `<title>Old Title</title>`,
			start:      "<title>",
			end:        "</title>",
			newContent: "New Title",
			expected:   `<title>New Title</title>`,
		},
		{
			name:       "未找到起始标记",
			html:       `<div>content</div>`,
			start:      "<title>",
			end:        "</title>",
			newContent: "New",
			expected:   `<div>content</div>`,
		},
		{
			name:       "未找到结束标记",
			html:       `<title>Old Title`,
			start:      "<title>",
			end:        "</title>",
			newContent: "New",
			expected:   `<title>Old Title`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := replaceBetween(tt.html, tt.start, tt.end, tt.newContent)
			if result != tt.expected {
				t.Errorf("replaceBetween() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestReplaceMetaDescription 测试 meta description 替换
func TestReplaceMetaDescription(t *testing.T) {
	tests := []struct {
		name           string
		html           string
		newDescription string
		expected       string
	}{
		{
			name:           "替换描述",
			html:           `<meta name="description" content="Old description">`,
			newDescription: "New description",
			expected:       `<meta name="description" content="New description">`,
		},
		{
			name:           "未找到 meta 标签",
			html:           `<head></head>`,
			newDescription: "New description",
			expected:       `<head></head>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := replaceMetaDescription(tt.html, tt.newDescription)
			if result != tt.expected {
				t.Errorf("replaceMetaDescription() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestInjectMetaTags 测试完整的 meta 注入逻辑
func TestInjectMetaTags(t *testing.T) {
	// 模拟配置
	cfg := &config.AppConfig{
		PublicBaseURL: "https://relaypulse.top",
		Monitors: []config.ServiceConfig{
			{
				Provider:     "FoxCode",
				ProviderSlug: "foxcode",
			},
			{
				Provider:     "88Code",
				ProviderSlug: "88code",
			},
		},
	}

	// 模拟 index.html
	indexHTML := `<!doctype html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="description" content="Default description">
<title>Default Title</title>
</head>
<body></body>
</html>`

	tests := []struct {
		name               string
		path               string
		expectedLang       string
		expectedTitlePart  string
		expectedIsNotFound bool
		expectedHTMLParts  []string
	}{
		{
			name:               "中文首页",
			path:               "/",
			expectedLang:       "zh-CN",
			expectedTitlePart:  "RelayPulse - 实时监测",
			expectedIsNotFound: false,
		},
		{
			name:               "中文联系页",
			path:               "/contact",
			expectedLang:       "zh-CN",
			expectedTitlePart:  "联系我们 | RelayPulse",
			expectedIsNotFound: false,
			expectedHTMLParts: []string{
				`<link rel="canonical" href="https://relaypulse.top/contact">`,
				`<link rel="alternate" hreflang="zh-CN" href="https://relaypulse.top/contact">`,
				`<link rel="alternate" hreflang="en" href="https://relaypulse.top/en/contact">`,
				`<link rel="alternate" hreflang="x-default" href="https://relaypulse.top/contact">`,
				`<meta property="og:url" content="https://relaypulse.top/contact">`,
				`"@type": "ContactPage"`,
			},
		},
		{
			name:               "存在的服务商页面",
			path:               "/p/foxcode",
			expectedLang:       "zh-CN",
			expectedTitlePart:  "FoxCode 服务可用性监测",
			expectedIsNotFound: false,
		},
		{
			name:               "不存在的服务商页面",
			path:               "/p/nonexistent",
			expectedLang:       "zh-CN",
			expectedTitlePart:  "页面未找到",
			expectedIsNotFound: true,
		},
		{
			name:               "XSS 尝试",
			path:               `/p/"><script>alert(1)</script>`,
			expectedLang:       "zh-CN",
			expectedTitlePart:  "页面未找到",
			expectedIsNotFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html, isNotFound := injectMetaTags(indexHTML, tt.path, cfg)

			if isNotFound != tt.expectedIsNotFound {
				t.Errorf("injectMetaTags(%q) isNotFound = %v, want %v", tt.path, isNotFound, tt.expectedIsNotFound)
			}

			if !strings.Contains(html, tt.expectedLang) {
				t.Errorf("injectMetaTags(%q) html missing lang = %q", tt.path, tt.expectedLang)
			}

			if !strings.Contains(html, tt.expectedTitlePart) {
				t.Errorf("injectMetaTags(%q) html missing title part = %q", tt.path, tt.expectedTitlePart)
			}

			for _, part := range tt.expectedHTMLParts {
				if !strings.Contains(html, part) {
					t.Errorf("injectMetaTags(%q) html missing expected part = %q", tt.path, part)
				}
			}

			// 验证 XSS 防护
			if strings.Contains(html, "<script>") || strings.Contains(html, "alert(") {
				t.Errorf("injectMetaTags(%q) contains unescaped script tags", tt.path)
			}
		})
	}
}

// TestInjectNoindexForInvalidPaths 测试非白名单路径注入 noindex
func TestInjectNoindexForInvalidPaths(t *testing.T) {
	cfg := &config.AppConfig{
		PublicBaseURL: "https://relaypulse.top",
		Monitors: []config.ServiceConfig{
			{Provider: "FoxCode", ProviderSlug: "foxcode"},
		},
	}

	indexHTML := `<!doctype html>
<html lang="en">
<head>
<meta name="description" content="Default">
<title>Default</title>
</head>
<body></body>
</html>`

	tests := []struct {
		name           string
		path           string
		expectNoindex  bool
		expectNotFound bool
	}{
		// 有效首页：不应注入 noindex
		{name: "根路径", path: "/", expectNoindex: false, expectNotFound: false},
		{name: "英文首页", path: "/en/", expectNoindex: false, expectNotFound: false},
		{name: "俄文首页", path: "/ru/", expectNoindex: false, expectNotFound: false},
		{name: "日文首页", path: "/ja/", expectNoindex: false, expectNotFound: false},

		// 有效静态页面：不应注入 noindex
		{name: "联系页", path: "/contact", expectNoindex: false, expectNotFound: false},
		{name: "英文联系页", path: "/en/contact", expectNoindex: false, expectNotFound: false},

		// 有效服务商页面：不应注入 noindex
		{name: "有效服务商", path: "/p/foxcode", expectNoindex: false, expectNotFound: false},
		{name: "英文有效服务商", path: "/en/p/foxcode", expectNoindex: false, expectNotFound: false},

		// 无效服务商：返回 404 + noindex
		{name: "无效服务商", path: "/p/invalid", expectNoindex: true, expectNotFound: true},

		// 非白名单路径：注入 noindex 但不是 404
		{name: "随机路径", path: "/foo", expectNoindex: true, expectNotFound: false},
		{name: "多级随机路径", path: "/foo/bar", expectNoindex: true, expectNotFound: false},
		{name: "语言前缀下的随机路径", path: "/en/foo", expectNoindex: true, expectNotFound: false},
		{name: "语言前缀下的多级路径", path: "/en/foo/bar", expectNoindex: true, expectNotFound: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html, isNotFound := injectMetaTags(indexHTML, tt.path, cfg)

			hasNoindex := strings.Contains(html, `noindex`)

			if hasNoindex != tt.expectNoindex {
				t.Errorf("path=%q: noindex=%v, want %v", tt.path, hasNoindex, tt.expectNoindex)
			}

			if isNotFound != tt.expectNotFound {
				t.Errorf("path=%q: isNotFound=%v, want %v", tt.path, isNotFound, tt.expectNotFound)
			}
		})
	}
}

// TestGeneratePageMetaCanonicalSafety 测试 canonical URL 基于 meta 数据生成，不依赖原始 path
func TestGeneratePageMetaCanonicalSafety(t *testing.T) {
	tests := []struct {
		name              string
		meta              MetaData
		baseURL           string
		expectedCanonical string
	}{
		{
			name: "服务商页面：使用 slug 构建 canonical",
			meta: MetaData{
				Title:          "Test",
				Description:    "Test",
				Language:       Language{Code: "zh-CN", PathPrefix: "", HreflangTag: "zh-CN"},
				Slug:           "foxcode",
				ProviderName:   "FoxCode",
				IsProviderPage: true,
			},
			baseURL:           "https://relaypulse.top",
			expectedCanonical: `    <link rel="canonical" href="https://relaypulse.top/p/foxcode">`,
		},
		{
			name: "首页：使用语言前缀构建 canonical",
			meta: MetaData{
				Title:          "Test",
				Description:    "Test",
				Language:       Language{Code: "en-US", PathPrefix: "en", HreflangTag: "en"},
				IsProviderPage: false,
			},
			baseURL:           "https://relaypulse.top",
			expectedCanonical: `    <link rel="canonical" href="https://relaypulse.top/en/">`,
		},
		{
			name: "静态联系页：使用 staticPath 构建 canonical",
			meta: MetaData{
				Title:          "Test",
				Description:    "Test",
				Language:       Language{Code: "en-US", PathPrefix: "en", HreflangTag: "en"},
				IsProviderPage: false,
				StaticPath:     "contact",
			},
			baseURL:           "https://relaypulse.top",
			expectedCanonical: `    <link rel="canonical" href="https://relaypulse.top/en/contact">`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pageMeta := generatePageMeta(tt.meta, tt.baseURL)

			// 主断言：验证 canonical URL 与预期完全一致
			if pageMeta.Canonical != tt.expectedCanonical {
				t.Errorf("Canonical mismatch:\n  got:  %q\n  want: %q", pageMeta.Canonical, tt.expectedCanonical)
			}

			// 安全检查：验证 canonical 不包含原始 path 的恶意部分
			if strings.Contains(pageMeta.Canonical, "<script>") || strings.Contains(pageMeta.Canonical, "alert(") {
				t.Errorf("Canonical contains unescaped script: %s", pageMeta.Canonical)
			}

			// 安全检查：验证 OpenGraph URL 也不包含恶意脚本
			if strings.Contains(pageMeta.OpenGraph, "<script>") || strings.Contains(pageMeta.OpenGraph, "alert(") {
				t.Errorf("OpenGraph contains unescaped script: %s", pageMeta.OpenGraph)
			}
		})
	}
}

// TestParseStaticPath 测试白名单静态页解析（剥语言前缀）
func TestParseStaticPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{path: "/", expected: ""},
		{path: "/en/", expected: ""},
		{path: "/contact", expected: "contact"},
		{path: "/en/contact", expected: "contact"},
		{path: "/ru/contact", expected: "contact"},
		{path: "/ja/contact", expected: "contact"},
		{path: "/contact/apply", expected: ""}, // 表单子页不在白名单
		{path: "/en/contact/apply", expected: ""},
		{path: "/de/contact", expected: ""}, // 不支持的语言前缀
		{path: "/foo", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := parseStaticPath(tt.path); got != tt.expected {
				t.Errorf("parseStaticPath(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}

// TestIsValidHomePath 测试首页路径验证
func TestIsValidHomePath(t *testing.T) {
	tests := []struct {
		path  string
		valid bool
	}{
		// 有效首页路径
		{path: "/", valid: true},
		{path: "", valid: true},
		{path: "/en/", valid: true},
		{path: "/en", valid: true},
		{path: "/ru/", valid: true},
		{path: "/ja/", valid: true},

		// 有效静态页面路径
		{path: "/contact", valid: true},
		{path: "/en/contact", valid: true},

		// 表单页不索引
		{path: "/contact/apply", valid: false},
		{path: "/contact/change", valid: false},

		// 无效路径
		{path: "/foo", valid: false},
		{path: "/foo/bar", valid: false},
		{path: "/en/foo", valid: false},
		{path: "/p/foxcode", valid: false},
		{path: "/de", valid: false}, // 不支持的语言
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := isValidHomePath(tt.path)
			if result != tt.valid {
				t.Errorf("isValidHomePath(%q) = %v, want %v", tt.path, result, tt.valid)
			}
		})
	}
}
