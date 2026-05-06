package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"

	"monitor/internal/config"
	"monitor/internal/identity"
	"monitor/internal/monitor"
)

func main() {
	// 配置模式 flags
	provider := flag.String("provider", "", "Provider name (config mode)")
	service := flag.String("service", "", "Service name (config mode)")
	channel := flag.String("channel", "", "Channel name (optional, defaults to service)")
	model := flag.String("model", "", "Model name override")
	configFile := flag.String("config", "config.yaml", "Config file path")

	// 独立模式 flags
	urlFlag := flag.String("url", "", "API endpoint URL (standalone mode)")
	keyFlag := flag.String("key", "", "API key (standalone mode)")
	typeFlag := flag.String("type", "", "Service type: cc/cx/gm (standalone mode)")
	bodyFlag := flag.String("body", "", "Body template file path, e.g. templates/cc-haiku-tiny.json (standalone mode, overrides built-in body)")
	successFlag := flag.String("success", "", "Success keyword to check in response (standalone mode, overrides built-in value)")

	verbose := flag.Bool("v", false, "Verbose output")

	flag.Parse()

	// 判定模式：独立模式 vs 配置模式
	standaloneFlags := 0
	if *urlFlag != "" {
		standaloneFlags++
	}
	if *keyFlag != "" {
		standaloneFlags++
	}
	if *typeFlag != "" {
		standaloneFlags++
	}

	standaloneMode := standaloneFlags == 3

	if standaloneFlags > 0 && standaloneFlags < 3 {
		fmt.Println("❌ 独立模式必须同时提供 -url、-key、-type 三个参数")
		fmt.Println("用法: go run ./cmd/verify -type cc -url <url> -key <key> [-model <name>] [-body <file>] [-success <keyword>] [-v]")
		os.Exit(1)
	}

	var target *config.ServiceConfig

	if standaloneMode {
		// 独立模式：内嵌模板构建 ServiceConfig
		svcType := strings.ToLower(strings.TrimSpace(*typeFlag))
		if svcType == "gm" && *model != "" {
			fmt.Println("⚠️  Gemini model 应在 -url 路径中指定，-model 参数将被忽略")
		}
		var err error
		target, err = buildStandaloneConfig(svcType, *urlFlag, *keyFlag, *model)
		if err != nil {
			fmt.Printf("❌ %v\n", err)
			os.Exit(1)
		}

		// 如果指定了 -body，从文件加载替换内嵌的请求体
		if *bodyFlag != "" {
			bodyContent, err := os.ReadFile(*bodyFlag)
			if err != nil {
				fmt.Printf("❌ 读取 body 文件失败: %v\n", err)
				os.Exit(1)
			}
			target.Body = string(bodyContent)
		}

		// 如果指定了 -success，覆盖响应校验关键字
		if *successFlag != "" {
			target.SuccessContains = *successFlag
		}
	} else {
		// 配置模式：从 config.yaml 查找
		if *provider == "" || *service == "" {
			fmt.Println("用法:")
			fmt.Println("  配置模式: go run ./cmd/verify -provider <name> -service <name> [-channel <name>] [-model <name>] [-config <path>] [-v]")
			fmt.Println("  独立模式: go run ./cmd/verify -type cc -url <url> -key <key> [-model <name>] [-body <file>] [-success <keyword>] [-v]")
			fmt.Println()
			fmt.Println("示例:")
			fmt.Println("  go run ./cmd/verify -provider 88code -service cx -channel vip3 -model gpt-5.1-codex-mini -v")
			fmt.Println("  go run ./cmd/verify -type cc -url https://api.example.com/v1/messages -key sk-xxx -v")
			os.Exit(1)
		}

		if *channel == "" {
			*channel = *service
		}

		// 加载 .env 文件（仅用于本地开发，不覆盖已有环境变量）
		if err := config.LoadDotenvFromConfigDir(*configFile, *verbose); err != nil {
			fmt.Printf("⚠️  %v\n", err)
		}

		// 加载配置
		loader := config.NewLoader()
		cfg, err := loader.Load(*configFile)
		if err != nil {
			fmt.Printf("❌ 加载配置失败: %v\n", err)
			os.Exit(1)
		}

		// 查找检测项
		for i := range cfg.Monitors {
			m := &cfg.Monitors[i]
			if m.Provider == *provider && m.Service == *service && m.Channel == *channel {
				if *model != "" {
					if m.Model == *model {
						target = m
						break
					}
				} else {
					target = m
					break
				}
			}
		}

		if target == nil {
			if *model != "" {
				fmt.Printf("❌ 未找到检测项: provider=%s, service=%s, channel=%s, model=%s\n", *provider, *service, *channel, *model)
			} else {
				fmt.Printf("❌ 未找到检测项: provider=%s, service=%s, channel=%s\n", *provider, *service, *channel)
			}
			os.Exit(1)
		}
	}

	// 变量注入：替换模板占位符（使用真实 UserIDManager，确保 device_id 等字段非空）
	uidMgr := identity.NewUserIDManager()
	probeURL, probeBody, probeHeaders, probeSuccessContains, _, _ := monitor.InjectVariables(target, uidMgr)

	// 构建输出标识
	var targetInfo string
	if standaloneMode {
		displayURL := probeURL
		// GM 模式 URL 中包含 API key，脱敏显示
		if strings.ToLower(*typeFlag) == "gm" {
			if u, err := url.Parse(probeURL); err == nil {
				q := u.Query()
				if k := q.Get("key"); k != "" {
					q.Set("key", k[:min(6, len(k))]+"***")
					u.RawQuery = q.Encode()
					displayURL = u.String()
				}
			}
		}
		targetInfo = fmt.Sprintf("type=%s, url=%s", strings.ToLower(*typeFlag), displayURL)
		if target.Model != "" {
			targetInfo += fmt.Sprintf(", model=%s", target.Model)
		}
	} else {
		targetInfo = fmt.Sprintf("provider=%s, service=%s, channel=%s", target.Provider, target.Service, target.Channel)
		if target.Model != "" {
			targetInfo += fmt.Sprintf(", model=%s", target.Model)
		}
	}
	fmt.Printf("🔍 验证检测项: %s\n", targetInfo)
	fmt.Println("========================================")

	if *verbose {
		fmt.Printf("📋 配置信息:\n")
		if target.Model != "" {
			fmt.Printf("  Model: %s\n", target.Model)
		}
		verboseURL := probeURL
		// GM 模式 URL 中包含 API key，脱敏显示
		if standaloneMode && strings.ToLower(*typeFlag) == "gm" {
			if u, err := url.Parse(probeURL); err == nil {
				q := u.Query()
				if k := q.Get("key"); k != "" {
					q.Set("key", k[:min(6, len(k))]+"***")
					u.RawQuery = q.Encode()
					verboseURL = u.String()
				}
			}
		}
		fmt.Printf("  URL: %s\n", verboseURL)
		fmt.Printf("  Method: %s\n", target.Method)
		fmt.Printf("  Success Contains: %s\n", probeSuccessContains)
		fmt.Printf("  Headers:\n")
		for k, v := range probeHeaders {
			// 隐藏 API key
			if strings.Contains(strings.ToLower(k), "key") || strings.Contains(strings.ToLower(k), "auth") {
				v = v[:min(10, len(v))] + "..."
			}
			fmt.Printf("    %s: %s\n", k, v)
		}
		fmt.Printf("  Body (%d bytes):\n", len(probeBody))
		if len(probeBody) > 200 {
			fmt.Printf("    %s...\n", probeBody[:200])
		} else {
			fmt.Printf("    %s\n", probeBody)
		}
		fmt.Println()
	}

	// 构建请求
	var body io.Reader
	if probeBody != "" {
		trimmedBody := strings.TrimSpace(probeBody)
		body = bytes.NewBufferString(trimmedBody)
	}

	req, err := http.NewRequest(target.Method, probeURL, body)
	if err != nil {
		fmt.Printf("❌ 构建请求失败: %v\n", err)
		os.Exit(1)
	}

	// 设置 headers（使用原始大小写）
	for k, v := range probeHeaders {
		req.Header[k] = []string{v}
	}

	// 打印实际请求 headers
	if *verbose {
		fmt.Println("📨 实际请求 Headers:")
		for k, v := range req.Header {
			fmt.Printf("    %s: %s\n", k, v)
		}
		fmt.Println()
	}

	fmt.Println("📤 发送请求...")
	start := time.Now()

	client := &http.Client{
		Timeout: 120 * time.Second, // 流式响应可能较长
		Transport: &http.Transport{
			Proxy:              http.ProxyFromEnvironment,
			DisableCompression: false, // 允许自动解压缩
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("❌ 请求失败: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	latency := time.Since(start)

	fmt.Println()
	fmt.Printf("📥 响应 (HTTP %d, %dms):\n", resp.StatusCode, latency.Milliseconds())

	// 打印响应 headers
	if *verbose {
		fmt.Println("📥 响应 Headers:")
		for k, v := range resp.Header {
			fmt.Printf("    %s: %s\n", k, v)
		}
		fmt.Println()
	}

	// 使用带缓冲的 reader，便于魔术头识别与后续 Peek
	rawReader := bufio.NewReader(resp.Body)

	decodedBody, decodeClose, decodeErr := decodeResponseBody(resp, rawReader)
	if decodeErr != nil && *verbose {
		fmt.Printf("⚠️  响应解压失败，使用原始响应体: %v\n", decodeErr)
	}
	if decodeClose != nil {
		defer decodeClose()
	}

	// 使用 bufio.Reader 包装，支持 Peek 检测
	bufferedBody := bufio.NewReader(decodedBody)

	// 检测是否为 SSE 流式响应
	// 1. 优先根据 Content-Type 判断
	// 2. 某些服务端可能未正确设置 Content-Type，使用启发式检测作为 fallback
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	isSSE := strings.Contains(contentType, "text/event-stream")

	// 启发式检测：如果 Content-Type 不是 SSE，尝试 peek 开头内容
	if !isSSE {
		// Peek 可能返回 n < 512 且 err == io.EOF（短响应），仍用拿到的字节判断
		peeked, _ := bufferedBody.Peek(512)
		if len(peeked) > 0 {
			// Gemini 的 SSE 可能只有 "data:" 行，没有 "event:" 行；因此只要看起来像 SSE 的 data 行就认为是 SSE
			if bytes.HasPrefix(peeked, []byte("data:")) || bytes.Contains(peeked, []byte("\ndata:")) {
				isSSE = true
				if *verbose {
					fmt.Println("ℹ️  Content-Type 未指定 SSE，但内容符合 SSE 格式")
				}
			}
		}
	}

	var respStr string
	if isSSE {
		fmt.Println("🌊 检测到流式响应 (SSE):")
		fmt.Println("────────────────────────────────")
		aggregatedText, assembledJSON, readErr := readSSEStream(bufferedBody, *verbose)
		if readErr != nil {
			fmt.Printf("\n⚠️  读取流遇到错误: %v\n", readErr)
		}
		fmt.Println()
		fmt.Println("────────────────────────────────")

		// 输出组装后的完整 JSON
		fmt.Println("\n📋 组装后的完整响应:")
		// 格式化 JSON 输出
		var prettyJSON bytes.Buffer
		if err := json.Indent(&prettyJSON, []byte(assembledJSON), "", "  "); err == nil {
			fmt.Println(prettyJSON.String())
		} else {
			fmt.Println(assembledJSON)
		}

		respStr = aggregatedText
	} else {
		// 非 SSE：一次性读取
		respBody, readErr := io.ReadAll(bufferedBody)
		if readErr != nil {
			fmt.Printf("❌ 读取响应失败: %v\n", readErr)
			os.Exit(1)
		}
		respBody = brotliFallbackIfNeeded(resp, respBody, *verbose)
		respStr = string(respBody)
		if len(respStr) > 500 && !*verbose {
			fmt.Println(respStr[:500] + "...")
		} else {
			fmt.Println(respStr)
		}
		fmt.Println()
	}

	// 判断结果
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if probeSuccessContains != "" {
			if strings.Contains(respStr, probeSuccessContains) {
				fmt.Printf("✅ 成功! HTTP %d, 延迟 %dms, 响应包含 '%s'\n", resp.StatusCode, latency.Milliseconds(), probeSuccessContains)
			} else {
				fmt.Printf("⚠️  HTTP %d 但响应不包含 '%s'\n", resp.StatusCode, probeSuccessContains)
				os.Exit(1)
			}
		} else {
			fmt.Printf("✅ 成功! HTTP %d, 延迟 %dms\n", resp.StatusCode, latency.Milliseconds())
		}
	} else {
		fmt.Printf("❌ 失败! HTTP %d, 延迟 %dms\n", resp.StatusCode, latency.Milliseconds())
		os.Exit(1)
	}
}

// buildStandaloneConfig 根据服务类型构建独立模式的 ServiceConfig。
func buildStandaloneConfig(svcType, rawURL, apiKey, model string) (*config.ServiceConfig, error) {
	switch svcType {
	case "cc":
		return buildCCConfig(rawURL, apiKey, model)
	case "cx":
		return buildCXConfig(rawURL, apiKey, model)
	case "gm":
		return buildGMConfig(rawURL, apiKey)
	default:
		return nil, fmt.Errorf("未知的 -type: %s（仅支持 cc/cx/gm）", svcType)
	}
}

// buildCCConfig 构建 Anthropic Messages API 的 ServiceConfig。
func buildCCConfig(rawURL, apiKey, model string) (*config.ServiceConfig, error) {
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}
	body, err := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 100,
		"stream":     false,
		"system":     []map[string]any{{"type": "text", "text": "Always say 'pong'."}},
		"messages":   []map[string]any{{"role": "user", "content": []map[string]any{{"type": "text", "text": "ping"}}}},
		"tools":      []any{},
	})
	if err != nil {
		return nil, fmt.Errorf("构建 cc 请求体失败: %w", err)
	}
	return &config.ServiceConfig{
		Provider:   "standalone",
		Service:    "cc",
		Channel:    "cc",
		Model:      model,
		BaseURL:    rawURL,
		URLPattern: "{{BASE_URL}}",
		Method:     "POST",
		Headers: map[string]string{
			"authorization":     "Bearer " + apiKey,
			"anthropic-version": "2023-06-01",
			"Content-Type":      "application/json",
		},
		Body:            string(body),
		SuccessContains: "pong",
	}, nil
}

// buildCXConfig 构建 OpenAI Responses API 的 ServiceConfig。
func buildCXConfig(rawURL, apiKey, model string) (*config.ServiceConfig, error) {
	if model == "" {
		model = "gpt-5-codex"
	}
	body, err := json.Marshal(map[string]any{
		"model": model,
		"input": []map[string]any{
			{"role": "system", "content": `You are an echo bot. Always say "pong".`},
			{"role": "user", "content": "ping"},
		},
		"stream": false,
	})
	if err != nil {
		return nil, fmt.Errorf("构建 cx 请求体失败: %w", err)
	}
	return &config.ServiceConfig{
		Provider:   "standalone",
		Service:    "cx",
		Channel:    "cx",
		Model:      model,
		BaseURL:    rawURL,
		URLPattern: "{{BASE_URL}}",
		Method:     "POST",
		Headers: map[string]string{
			"Authorization": "Bearer " + apiKey,
			"Content-Type":  "application/json",
		},
		Body:            string(body),
		SuccessContains: "pong",
	}, nil
}

// buildGMConfig 构建 Gemini API 的 ServiceConfig。
func buildGMConfig(rawURL, apiKey string) (*config.ServiceConfig, error) {
	body, err := json.Marshal(map[string]any{
		"contents": []map[string]any{{
			"role": "user",
			"parts": []map[string]any{
				{"text": "ping\n\nOnly respond: pong\n\n"},
			},
		}},
		"generationConfig": map[string]any{
			"temperature":     0,
			"maxOutputTokens": 5,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("构建 gm 请求体失败: %w", err)
	}
	return &config.ServiceConfig{
		Provider:   "standalone",
		Service:    "gm",
		Channel:    "gm",
		BaseURL:    rawURL,
		URLPattern: "{{BASE_URL}}?key={{API_KEY}}",
		APIKey:     apiKey,
		Method:     "POST",
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body:            string(body),
		SuccessContains: "pong",
	}, nil
}

// readSSEStream 逐行读取 SSE 流，实时输出文本内容
// 返回：(累积语义文本, 组装后的完整JSON, 错误)
func readSSEStream(r io.Reader, verbose bool) (string, string, error) {
	reader := bufio.NewReader(r)

	var (
		eventName string
		dataLines []string
		aggregate strings.Builder
		chunkNum  int

		// 用于组装完整消息的字段（Anthropic API）
		messageBase       map[string]any   // 从 message_start 获取
		contentBlocks     []map[string]any // 累积的内容块
		currentBlockIndex int              = -1
		currentBlockText  strings.Builder
		finalDelta        map[string]any // 从 message_delta 获取

		// 用于组装完整响应的字段（OpenAI Responses API）
		openAIResponse map[string]any // 从 response.created/completed 获取

		// 用于组装完整响应的字段（Gemini API）
		geminiResponse map[string]any // 记录最后一个 candidates 响应块（用于展示/调试）
	)

	// extractGeminiText 从 Gemini SSE 的 JSON payload 中提取文本：
	// candidates[].content.parts[].text
	extractGeminiText := func(obj map[string]any) (text string, ok bool) {
		rawCandidates, has := obj["candidates"]
		if !has {
			return "", false
		}
		candidates, ok := rawCandidates.([]any)
		if !ok {
			return "", false
		}
		var b strings.Builder
		for _, c := range candidates {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			content, ok := cm["content"].(map[string]any)
			if !ok {
				continue
			}
			parts, ok := content["parts"].([]any)
			if !ok {
				continue
			}
			for _, p := range parts {
				pm, ok := p.(map[string]any)
				if !ok {
					continue
				}
				if t, ok := pm["text"].(string); ok && t != "" {
					b.WriteString(t)
				}
			}
		}
		return b.String(), true
	}

	// flushEvent 处理一个完整的 SSE 事件
	flushEvent := func() {
		if eventName == "" && len(dataLines) == 0 {
			return
		}

		payload := strings.Join(dataLines, "\n")

		if verbose {
			chunkNum++
			fmt.Printf("\n[chunk %d] event=%q\n", chunkNum, eventName)
			if len(payload) > 200 {
				fmt.Printf("  data: %s...\n", payload[:200])
			} else {
				fmt.Printf("  data: %s\n", payload)
			}
		}

		if payload != "" && payload != "[DONE]" {
			var obj map[string]any
			if err := json.Unmarshal([]byte(payload), &obj); err == nil {
				// 根据事件类型处理
				switch eventName {
				case "message_start":
					if msg, ok := obj["message"].(map[string]any); ok {
						messageBase = msg
					}
				case "content_block_start":
					if idx, ok := obj["index"].(float64); ok {
						currentBlockIndex = int(idx)
						currentBlockText.Reset()
					}
					if block, ok := obj["content_block"].(map[string]any); ok {
						contentBlocks = append(contentBlocks, block)
					}
				case "content_block_delta":
					if delta, ok := obj["delta"].(map[string]any); ok {
						if text, ok := delta["text"].(string); ok {
							fmt.Print(text) // 实时输出
							aggregate.WriteString(text)
							currentBlockText.WriteString(text)
							if verbose {
								fmt.Printf("  → text: %q\n", text)
							}
						}
					}
				case "content_block_stop":
					// 将累积的文本更新到对应的内容块
					if currentBlockIndex >= 0 && currentBlockIndex < len(contentBlocks) {
						contentBlocks[currentBlockIndex]["text"] = currentBlockText.String()
					}
				case "message_delta":
					finalDelta = obj
				// OpenAI Responses API: 捕获 response 对象用于最终组装
				case "response.created", "response.in_progress", "response.completed", "response.failed":
					if resp, ok := obj["response"].(map[string]any); ok {
						openAIResponse = resp
					}
				case "response.output_text.delta":
					// OpenAI Responses API: {"delta":"pong",...}
					if text, ok := obj["delta"].(string); ok {
						fmt.Print(text) // 实时输出
						aggregate.WriteString(text)
						if verbose {
							fmt.Printf("  → text: %q\n", text)
						}
					}
				case "response.output_text.done":
					// OpenAI Responses API: {"text":"pong",...}
					if text, ok := obj["text"].(string); ok {
						// text 是完整文本，通常已经包含在增量中，这里仅作兜底
						// 如果 aggregate 为空才追加（避免重复）
						if aggregate.Len() == 0 {
							fmt.Print(text)
							aggregate.WriteString(text)
							if verbose {
								fmt.Printf("  → text (fallback): %q\n", text)
							}
						}
					}
				default:
					// Gemini SSE: 没有 event: 行（eventName 为空），尝试从 candidates 提取文本
					if eventName == "" {
						if text, ok := extractGeminiText(obj); ok {
							// 记录最后一个 Gemini payload 方便最终组装/展示
							geminiResponse = obj
							if text != "" {
								fmt.Print(text)
								aggregate.WriteString(text)
								if verbose {
									fmt.Printf("  → text (gemini): %q\n", text)
								}
							}
						}
					}
				}
			} else {
				// 非 JSON payload，按原始文本处理
				text := payload
				fmt.Print(text)
				aggregate.WriteString(text)
				if verbose {
					fmt.Printf("  → text: %q\n", text)
				}
			}
		}

		eventName = ""
		dataLines = dataLines[:0]
	}

	for {
		line, err := reader.ReadString('\n')

		if err != nil && err != io.EOF {
			flushEvent()
			return aggregate.String(), assembleMessage(messageBase, contentBlocks, finalDelta, openAIResponse, geminiResponse), err
		}

		line = strings.TrimRight(line, "\r\n")

		switch {
		case line == "":
			flushEvent()
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}

		if err == io.EOF {
			flushEvent()
			return aggregate.String(), assembleMessage(messageBase, contentBlocks, finalDelta, openAIResponse, geminiResponse), nil
		}
	}
}

// assembleMessage 将 SSE 事件组装成完整的消息 JSON
// 支持 Anthropic API (message_start/content_block_*/message_delta)、OpenAI Responses API (response.*) 和 Gemini API (candidates)
func assembleMessage(base map[string]any, contents []map[string]any, delta map[string]any, openAIResponse map[string]any, geminiResponse map[string]any) string {
	// Anthropic API: 使用 message_start 的 base
	if base != nil {
		// 设置内容块
		if len(contents) > 0 {
			base["content"] = contents
		}

		// 合并 message_delta 中的字段
		if delta != nil {
			if d, ok := delta["delta"].(map[string]any); ok {
				for k, v := range d {
					base[k] = v
				}
			}
			if usage, ok := delta["usage"].(map[string]any); ok {
				base["usage"] = usage
			}
		}

		result, err := json.Marshal(base)
		if err != nil {
			return "{}"
		}
		return string(result)
	}

	// OpenAI Responses API: 使用 response.created/completed 的 response 对象
	if openAIResponse != nil {
		result, err := json.Marshal(openAIResponse)
		if err != nil {
			return "{}"
		}
		return string(result)
	}

	// Gemini API: 直接返回最后一个 candidates payload（方便查看 usageMetadata/finishReason 等）
	if geminiResponse != nil {
		result, err := json.Marshal(geminiResponse)
		if err != nil {
			return "{}"
		}
		return string(result)
	}

	return "{}"
}

// decodeResponseBody 根据 Content-Encoding 或魔术头解压响应体，返回解压后的 reader 和可选的关闭函数。
func decodeResponseBody(resp *http.Response, reader *bufio.Reader) (io.Reader, func(), error) {
	encoding := strings.ToLower(resp.Header.Get("Content-Encoding"))

	switch {
	case strings.Contains(encoding, "br"):
		return brotli.NewReader(reader), nil, nil
	case strings.Contains(encoding, "zstd"):
		decoder, err := zstd.NewReader(reader)
		if err != nil {
			return reader, nil, err
		}
		return decoder, func() { decoder.Close() }, nil
	case strings.Contains(encoding, "gzip"):
		gr, err := gzip.NewReader(reader)
		if err != nil {
			return reader, nil, err
		}
		return gr, func() { _ = gr.Close() }, nil
	case strings.Contains(encoding, "deflate"):
		zr, err := zlib.NewReader(reader)
		if err != nil {
			return reader, nil, err
		}
		return zr, func() { _ = zr.Close() }, nil
	default:
		// 兜底：Content-Encoding 为空时，根据魔术头判断 gzip/zstd
		if peeked, err := reader.Peek(4); err == nil && len(peeked) > 0 {
			if len(peeked) >= 2 && peeked[0] == 0x1f && peeked[1] == 0x8b {
				gr, err := gzip.NewReader(reader)
				if err != nil {
					return reader, nil, err
				}
				return gr, func() { _ = gr.Close() }, nil
			}
			if len(peeked) >= 4 && peeked[0] == 0x28 && peeked[1] == 0xB5 && peeked[2] == 0x2F && peeked[3] == 0xFD {
				decoder, err := zstd.NewReader(reader)
				if err != nil {
					return reader, nil, err
				}
				return decoder, func() { decoder.Close() }, nil
			}
		}
		return reader, nil, nil
	}
}

func brotliFallbackIfNeeded(resp *http.Response, data []byte, verbose bool) []byte {
	if len(data) == 0 {
		return data
	}
	if strings.TrimSpace(resp.Header.Get("Content-Encoding")) != "" {
		return data
	}
	if !looksBinary(data) {
		return data
	}

	decoded, err := tryDecompressBrotli(data)
	if err != nil {
		if verbose {
			fmt.Printf("ℹ️  Brotli 兜底解压失败，保留原始响应体: %v\n", err)
		}
		return data
	}
	if verbose {
		fmt.Println("ℹ️  已应用 Brotli 兜底解压")
	}
	return decoded
}

func tryDecompressBrotli(data []byte) ([]byte, error) {
	reader := brotli.NewReader(bytes.NewReader(data))
	return io.ReadAll(reader)
}

func looksBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	sample := data
	if len(sample) > 2048 {
		sample = sample[:2048]
	}

	var nonPrintable int
	for _, b := range sample {
		if b == 0x00 {
			return true
		}
		if b < 0x09 || (b > 0x0D && b < 0x20) {
			nonPrintable++
		}
	}

	return float64(nonPrintable)/float64(len(sample)) > 0.2
}
