// migrate 工具将 config.yaml 中的 monitors（以及旧版 managed_monitors.yaml）迁移到 monitors.d/ 目录。
// managed_monitors.yaml 已废弃，此工具保留对其读取以支持一次性迁移。
//
// 用法:
//
//	go run ./cmd/migrate -config config.yaml [-dry-run]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"monitor/internal/config"
)

func main() {
	configFile := flag.String("config", "config.yaml", "配置文件路径")
	dryRun := flag.Bool("dry-run", false, "仅预览，不实际写入")
	flag.Parse()

	if err := run(*configFile, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}

func run(configFile string, dryRun bool) error {
	absPath, err := filepath.Abs(configFile)
	if err != nil {
		return fmt.Errorf("解析路径失败: %w", err)
	}
	configDir := filepath.Dir(absPath)

	// 1. 读取 config.yaml 原始内容
	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", configFile, err)
	}

	var cfg config.AppConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("解析 %s 失败: %w", configFile, err)
	}

	// 2. 收集所有需要迁移的 monitors
	type monitorSource struct {
		monitors []config.ServiceConfig
		source   string
	}

	var sources []monitorSource

	if len(cfg.Monitors) > 0 {
		sources = append(sources, monitorSource{
			monitors: cfg.Monitors,
			source:   "config.yaml",
		})
	}

	// 读取旧版 managed_monitors.yaml（已废弃，仅迁移用途）
	managedFile := "managed_monitors.yaml"
	managedPath := filepath.Join(configDir, managedFile)
	if managedData, err := os.ReadFile(managedPath); err == nil {
		var managed struct {
			Monitors []config.ServiceConfig `yaml:"monitors"`
		}
		if err := yaml.Unmarshal(managedData, &managed); err == nil && len(managed.Monitors) > 0 {
			sources = append(sources, monitorSource{
				monitors: managed.Monitors,
				source:   managedFile,
			})
		}
	}

	if len(sources) == 0 {
		fmt.Println("没有需要迁移的 monitors")
		return nil
	}

	// 3. 按 PSC 分组
	type pscGroup struct {
		key      string
		source   string
		monitors []config.ServiceConfig
	}
	groups := make(map[string]*pscGroup)

	// 先对所有 monitors 执行 parent 继承以获取完整 PSC
	var allMonitors []config.ServiceConfig
	for _, s := range sources {
		allMonitors = append(allMonitors, s.monitors...)
	}
	tmp := config.AppConfig{Monitors: make([]config.ServiceConfig, len(allMonitors))}
	copy(tmp.Monitors, allMonitors)

	// 按 PSC 分组（使用原始 monitors，parent 继承只用于 key 推导）
	idx := 0
	for _, s := range sources {
		for i := range s.monitors {
			m := s.monitors[i]
			// 获取完整的 PSC（对子通道需要从 parent 继承）
			p := strings.TrimSpace(m.Provider)
			svc := strings.TrimSpace(m.Service)
			ch := strings.TrimSpace(m.Channel)

			// 对子通道从 parent 路径解析
			if parent := strings.TrimSpace(m.Parent); parent != "" {
				parts := strings.Split(parent, "/")
				if len(parts) == 3 {
					if p == "" {
						p = parts[0]
					}
					if svc == "" {
						svc = parts[1]
					}
					if ch == "" {
						ch = parts[2]
					}
				}
			}

			if p == "" || svc == "" || ch == "" {
				fmt.Fprintf(os.Stderr, "警告: 跳过不完整的 monitor[%d] (来自 %s): provider=%s service=%s channel=%s\n",
					i, s.source, p, svc, ch)
				idx++
				continue
			}

			rawKey := config.MonitorFileKeyFromPSC(p, svc, ch)
			key, err := config.SanitizeMonitorKey(rawKey)
			if err != nil {
				fmt.Fprintf(os.Stderr, "警告: 跳过不安全的 PSC key %q (来自 %s): %v\n", rawKey, s.source, err)
				idx++
				continue
			}
			g, ok := groups[key]
			if !ok {
				g = &pscGroup{key: key, source: s.source}
				groups[key] = g
			}
			g.monitors = append(g.monitors, m)
			idx++
		}
	}

	// 4. 创建 monitors.d/ 目录
	monitorsDirPath := filepath.Join(configDir, config.MonitorsDirName)

	fmt.Printf("迁移计划:\n")
	fmt.Printf("  源: %s\n", configFile)
	for _, s := range sources {
		fmt.Printf("  - %s: %d monitors\n", s.source, len(s.monitors))
	}
	fmt.Printf("  目标: %s/\n", config.MonitorsDirName)
	fmt.Printf("  文件数: %d\n", len(groups))
	fmt.Println()

	for key, g := range groups {
		filename := key + ".yaml"
		fmt.Printf("  → %s/%s (%d monitors, 来自 %s)\n", config.MonitorsDirName, filename, len(g.monitors), g.source)
	}
	fmt.Println()

	if dryRun {
		fmt.Println("--dry-run 模式，不实际写入")
		return nil
	}

	// 创建目录
	if err := os.MkdirAll(monitorsDirPath, 0755); err != nil {
		return fmt.Errorf("创建 %s 目录失败: %w", config.MonitorsDirName, err)
	}

	// 5. 写入每个 PSC 文件
	now := time.Now().UTC().Format(time.RFC3339)
	for key, g := range groups {
		file := config.MonitorFile{
			Metadata: config.MonitorFileMetadata{
				Source:    "migration",
				Revision:  1,
				CreatedAt: now,
				UpdatedAt: now,
			},
			Monitors: g.monitors,
		}

		// 铸入稳定 id（channel_id 文件级 / model_id 每行），与 MonitorStore.Create / 回填 CLI
		// 共用同一幂等逻辑。否则迁移产物缺 model_id，会触发 CheckRuntimeModelIDs fail-closed
		// 在启动期中止、或热更新期跳过整份配置。
		config.BackfillFileIDs(&file)

		path := filepath.Join(monitorsDirPath, key+".yaml")
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("文件已存在: %s（请先手动处理）", path)
		}

		if err := config.AtomicWriteYAML(path, file); err != nil {
			return fmt.Errorf("写入 %s 失败: %w", path, err)
		}
		fmt.Printf("  ✓ %s\n", path)
	}

	fmt.Println()
	fmt.Println("迁移完成！")
	fmt.Println()
	fmt.Println("后续操作:")
	fmt.Printf("  1. 从 %s 中删除 monitors: 段落\n", configFile)
	if _, err := os.Stat(managedPath); err == nil {
		fmt.Printf("  2. 删除 %s\n", managedPath)
	}
	fmt.Println("  3. 重启服务或等待热更新生效")

	return nil
}
