package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// MonitorsDirName monitors.d/ 目录名
	MonitorsDirName = "monitors.d"
)

// MonitorFileMetadata 描述 monitor 文件的元信息。
type MonitorFileMetadata struct {
	Source    string `yaml:"source,omitempty" json:"source,omitempty"`
	Revision  int64  `yaml:"revision" json:"revision"`
	CreatedAt string `yaml:"created_at,omitempty" json:"created_at,omitempty"`
	UpdatedAt string `yaml:"updated_at,omitempty" json:"updated_at,omitempty"`

	// ChannelID 通道稳定唯一 id（ch_<uuidv4>），系统生成、不可变、跨产品 join 锚。
	// 空表示历史文件待回填（cmd/backfillids 或 admin 创建时补）。
	ChannelID string `yaml:"channel_id,omitempty" json:"channel_id,omitempty"`
}

// MonitorFile 是 monitors.d/ 中单个文件的结构。
type MonitorFile struct {
	Metadata MonitorFileMetadata `yaml:"metadata" json:"metadata"`
	Monitors []ServiceConfig     `yaml:"monitors" json:"monitors"`

	// 运行时字段，不序列化到 YAML
	Path string `yaml:"-" json:"-"`
	Key  string `yaml:"-" json:"-"`
}

// MonitorFileKeyFromPSC 从 PSC 三元组生成文件 key（不含路径和后缀）。
// 格式: provider--service--channel
func MonitorFileKeyFromPSC(provider, service, channel string) string {
	return strings.ToLower(provider) + "--" + strings.ToLower(service) + "--" + strings.ToLower(channel)
}

// MonitorFileNameFromPSC 从 PSC 三元组生成完整文件名。
func MonitorFileNameFromPSC(provider, service, channel string) string {
	return MonitorFileKeyFromPSC(provider, service, channel) + ".yaml"
}

// ParseMonitorFileKey 从文件 key 解析出 PSC 三元组。
func ParseMonitorFileKey(key string) (provider, service, channel string, err error) {
	parts := strings.SplitN(key, "--", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("无效的 monitor file key: %s", key)
	}
	return parts[0], parts[1], parts[2], nil
}

// loadMonitorsDir 读取 monitors.d/ 下的所有 monitor 文件并返回合并后的 ServiceConfig 列表。
// 目录不存在时静默返回空列表；存在但内容非法时返回错误。
// 同时返回 MonitorFile 列表供 admin API 使用。
func loadMonitorsDir(configDir string) ([]ServiceConfig, []MonitorFile, error) {
	dirPath := filepath.Join(configDir, MonitorsDirName)

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("读取 %s 失败: %w", MonitorsDirName, err)
	}

	var (
		allMonitors []ServiceConfig
		allFiles    []MonitorFile
		errs        []error
		seenKeys    = make(map[string]string) // key → filename, 用于检测目录内重复
	)

	for _, entry := range entries {
		name := entry.Name()

		// 跳过子目录（如 .archive/）和非 YAML 文件
		if entry.IsDir() || !isMonitorDefinitionFile(name) {
			continue
		}

		fullPath := filepath.Join(dirPath, name)
		file, err := loadMonitorFile(fullPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}

		// 从文件内容推导 PSC key
		key, err := DeriveMonitorFileKey(file)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}

		// 检测目录内 PSC 冲突
		if prev, exists := seenKeys[key]; exists {
			errs = append(errs, fmt.Errorf("%s: 与 %s 重复定义同一 PSC %s", name, prev, key))
			continue
		}

		file.Path = fullPath
		file.Key = key
		seenKeys[key] = name
		allFiles = append(allFiles, file)
		allMonitors = append(allMonitors, file.Monitors...)
	}

	if len(errs) > 0 {
		return nil, nil, errors.Join(errs...)
	}
	return allMonitors, allFiles, nil
}

// loadMonitorFile 读取并解析单个 monitor 文件。
func loadMonitorFile(path string) (MonitorFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return MonitorFile{}, fmt.Errorf("读取文件失败: %w", err)
	}

	var file MonitorFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return MonitorFile{}, fmt.Errorf("解析 YAML 失败: %w", err)
	}
	if len(file.Monitors) == 0 {
		return MonitorFile{}, fmt.Errorf("monitors 不能为空")
	}
	return file, nil
}

// DeriveMonitorFileKey 从 MonitorFile 推导 PSC key。
// 文件内所有 monitor 必须属于同一 PSC（通过 parent 继承后）。
// DeriveMonitorFileKey 从 MonitorFile 内容推导 PSC key（provider--service--channel）。
// 子通道先执行 parent 继承以填充 PSC 字段；所有 monitor 必须属于同一 PSC。
func DeriveMonitorFileKey(file MonitorFile) (string, error) {
	if len(file.Monitors) == 0 {
		return "", fmt.Errorf("monitors 不能为空")
	}

	// 对子通道执行 parent 继承以填充 PSC 字段
	normalized := make([]ServiceConfig, len(file.Monitors))
	copy(normalized, file.Monitors)
	tmp := AppConfig{Monitors: normalized}
	if err := tmp.preprocessParentInheritance(); err != nil {
		return "", fmt.Errorf("预处理父子继承失败: %w", err)
	}

	var key string
	for i := range tmp.Monitors {
		m := tmp.Monitors[i]
		p := strings.TrimSpace(m.Provider)
		s := strings.TrimSpace(m.Service)
		c := strings.TrimSpace(m.Channel)
		if p == "" || s == "" || c == "" {
			return "", fmt.Errorf("monitor[%d]: provider/service/channel 不能为空", i)
		}

		current := MonitorFileKeyFromPSC(p, s, c)
		if key == "" {
			key = current
		} else if current != key {
			return "", fmt.Errorf("文件内监测项必须属于同一 PSC，发现 %s 与 %s", key, current)
		}
	}
	return key, nil
}

// isMonitorDefinitionFile 检查文件名是否是合法的 monitor 定义文件。
// 跳过隐藏文件、临时文件和备份文件。
func isMonitorDefinitionFile(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") {
		return false
	}
	if strings.HasSuffix(name, ".tmp") || strings.HasSuffix(name, ".bak") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}
