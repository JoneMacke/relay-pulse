package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MonitorStore 提供 monitors.d/ 文件级 CRUD 操作。
// 所有写操作通过 mutex 串行化，使用 AtomicWriteYAML 确保崩溃安全。
// 写入后由 fsnotify 自动触发热更新，无需手动调用 reload。
type MonitorStore struct {
	dir string     // monitors.d/ 绝对路径
	mu  sync.Mutex // 写操作串行化
}

// NewMonitorStore 创建 MonitorStore。dir 是 monitors.d/ 的绝对路径。
func NewMonitorStore(dir string) *MonitorStore {
	return &MonitorStore{dir: dir}
}

// Dir 返回 monitors.d/ 目录路径。
func (s *MonitorStore) Dir() string {
	return s.dir
}

// validateKeySegment 校验 PSC 字段不含路径分隔符或目录穿越字符。
func validateKeySegment(field, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s 不能为空", field)
	}
	if strings.ContainsAny(value, "/\\") {
		return fmt.Errorf("%s 不能包含路径分隔符", field)
	}
	if value == "." || value == ".." || strings.Contains(value, "..") {
		return fmt.Errorf("%s 不能包含 '..'", field)
	}
	return nil
}

// SanitizeMonitorKey 规范化并校验 monitor file key，防止路径穿越。
func SanitizeMonitorKey(key string) (string, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	provider, service, channel, err := ParseMonitorFileKey(key)
	if err != nil {
		return "", err
	}
	if err := validateKeySegment("provider", provider); err != nil {
		return "", err
	}
	if err := validateKeySegment("service", service); err != nil {
		return "", err
	}
	if err := validateKeySegment("channel", channel); err != nil {
		return "", err
	}
	return MonitorFileKeyFromPSC(provider, service, channel), nil
}

// findExistingPath 查找 key 对应的 .yaml 或 .yml 文件。
func (s *MonitorStore) findExistingPath(key string) (string, error) {
	for _, ext := range []string{".yaml", ".yml"} {
		path := filepath.Join(s.dir, key+ext)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", nil
}

// MonitorSummary 是列表 API 返回的精简摘要。
type MonitorSummary struct {
	Key         string `json:"key"`
	ChannelID   string `json:"channel_id,omitempty"` // 通道稳定 id（跨产品 join 锚，供 rpdiag sampler 发现）
	Provider    string `json:"provider"`
	Service     string `json:"service"`
	Channel     string `json:"channel"`
	ChannelName string `json:"channel_name,omitempty"`
	ModelCount  int    `json:"model_count"`
	Disabled    bool   `json:"disabled"`
	Hidden      bool   `json:"hidden"`
	Board       string `json:"board"`
	Category    string `json:"category"`
	Template    string `json:"template"`
	Source      string `json:"source"`
	Revision    int64  `json:"revision"`
	UpdatedAt   string `json:"updated_at"`

	// LatestProbe 是该监测项下所有 model 最新一条探测记录的快照（按 timestamp 取最大）。
	// 由 api 层在 List 之后注入；store.List 本身不填充（store 层不依赖 storage / runtime config）。
	// nil 表示没有任何探测记录（新创建或刚归档的通道）。
	LatestProbe *LatestProbeSnapshot `json:"latest_probe,omitempty"`
}

// LatestProbeSnapshot 列表页"列表活化"用的最新探测快照。
type LatestProbeSnapshot struct {
	Status    int    `json:"status"` // 1=绿 2=黄 0=红
	SubStatus string `json:"sub_status,omitempty"`
	HTTPCode  int    `json:"http_code,omitempty"`
	Latency   int    `json:"latency"`         // ms
	Timestamp int64  `json:"timestamp"`       // Unix 秒
	Model     string `json:"model,omitempty"` // 这条记录归属的 model（多 model 通道用得着）
}

// List 列出 monitors.d/ 下所有监测文件的摘要。
func (s *MonitorStore) List() ([]MonitorSummary, error) {
	_, files, err := loadMonitorsDir(filepath.Dir(s.dir))
	if err != nil {
		return nil, err
	}

	summaries := make([]MonitorSummary, 0, len(files))
	for _, f := range files {
		if len(f.Monitors) == 0 {
			continue
		}

		root := f.Monitors[0]
		// 找到父通道（无 parent 字段的那个）
		for _, m := range f.Monitors {
			if strings.TrimSpace(m.Parent) == "" {
				root = m
				break
			}
		}

		summaries = append(summaries, MonitorSummary{
			Key:         f.Key,
			ChannelID:   f.Metadata.ChannelID,
			Provider:    root.Provider,
			Service:     root.Service,
			Channel:     root.Channel,
			ChannelName: root.ChannelName,
			ModelCount:  len(f.Monitors),
			Disabled:    root.Disabled,
			Hidden:      root.Hidden,
			Board:       root.Board,
			Category:    root.Category,
			Template:    root.Template,
			Source:      f.Metadata.Source,
			Revision:    f.Metadata.Revision,
			UpdatedAt:   f.Metadata.UpdatedAt,
		})
	}
	return summaries, nil
}

// Get 读取指定 key 的监测文件。key 格式: provider--service--channel
func (s *MonitorStore) Get(key string) (*MonitorFile, error) {
	var err error
	key, err = SanitizeMonitorKey(key)
	if err != nil {
		return nil, err
	}

	path, err := s.findExistingPath(key)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil
	}

	file, err := loadMonitorFile(path)
	if err != nil {
		return nil, err
	}
	file.Path = path
	file.Key = key
	return &file, nil
}

// Create 创建新监测文件。PSC 不能已存在于 monitors.d/ 中。
func (s *MonitorStore) Create(file *MonitorFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, err := DeriveMonitorFileKey(*file)
	if err != nil {
		return fmt.Errorf("推导 PSC key 失败: %w", err)
	}
	key, err = SanitizeMonitorKey(key)
	if err != nil {
		return fmt.Errorf("PSC key 无效: %w", err)
	}

	existing, err := s.findExistingPath(key)
	if err != nil {
		return err
	}
	if existing != "" {
		return fmt.Errorf("PSC %s 已存在", key)
	}

	path := filepath.Join(s.dir, key+".yaml")

	now := time.Now().UTC().Format(time.RFC3339)
	file.Metadata.Revision = 1
	if file.Metadata.CreatedAt == "" {
		file.Metadata.CreatedAt = now
	}
	file.Metadata.UpdatedAt = now

	// 生成缺失的稳定 id（幂等：已有则不动）。与回填 CLI 共用同一生成逻辑。
	BackfillFileIDs(file)

	if err := AtomicWriteYAML(path, file); err != nil {
		return err
	}

	file.Path = path
	file.Key = key
	return nil
}

// preserveAdminHiddenFields 把 admin PUT 不应改写的持久化字段从 existing 合并到 updated：
// 既含不参与 JSON round-trip 的 json:"-" 字段，也含对客户端可见但系统维护、不可变的稳定 id。
func preserveAdminHiddenFields(updated, existing *MonitorFile) {
	// channel_id 文件级不可变：无条件从 existing 还原（legacy 为空则保持空，回填交给 CLI）。
	updated.Metadata.ChannelID = existing.Metadata.ChannelID

	// root 对 root
	updatedRoot := findRootMonitor(updated.Monitors)
	existingRoot := findRootMonitor(existing.Monitors)
	if updatedRoot != nil && existingRoot != nil {
		copyAdminHiddenFields(updatedRoot, existingRoot)
	}

	// child 按双通道匹配：model_id 优先，展示名兜底（zero regression for legacy children without id）。
	existingByID := make(map[string]*ServiceConfig, len(existing.Monitors))    // parent + NUL + model_id
	existingByModel := make(map[string]*ServiceConfig, len(existing.Monitors)) // parent + NUL + model（展示名）
	for i := range existing.Monitors {
		if strings.TrimSpace(existing.Monitors[i].Parent) == "" {
			continue
		}
		if key, ok := childMatchKeyByModelID(existing.Monitors[i]); ok {
			existingByID[key] = &existing.Monitors[i]
		}
		existingByModel[childMatchKeyByModel(existing.Monitors[i])] = &existing.Monitors[i]
	}
	for i := range updated.Monitors {
		if strings.TrimSpace(updated.Monitors[i].Parent) == "" {
			continue
		}
		// Pass 1: 按 model_id 匹配（跨展示名改名保留 hidden fields）
		if key, ok := childMatchKeyByModelID(updated.Monitors[i]); ok {
			if src, hit := existingByID[key]; hit {
				copyAdminHiddenFields(&updated.Monitors[i], src)
				continue
			}
		}
		// Pass 2: 按展示名匹配（legacy 无 id / id 未命中时的兜底）
		if src, ok := existingByModel[childMatchKeyByModel(updated.Monitors[i])]; ok {
			copyAdminHiddenFields(&updated.Monitors[i], src)
		}
		// 新增 child（无匹配）不继承，删除 child（不在 updated 中）自然消失
	}
}

// findRootMonitor 返回第一个无 parent 字段的监测项指针。
func findRootMonitor(monitors []ServiceConfig) *ServiceConfig {
	for i := range monitors {
		if strings.TrimSpace(monitors[i].Parent) == "" {
			return &monitors[i]
		}
	}
	return nil
}

// childMatchKeyByModelID 返回按 parent+model_id 的稳定匹配键。
// model_id 为空时 ok=false，调用方应回退到展示名匹配。
func childMatchKeyByModelID(m ServiceConfig) (string, bool) {
	id := strings.TrimSpace(m.ModelID)
	if id == "" {
		return "", false
	}
	return strings.TrimSpace(m.Parent) + "\x00" + id, true
}

// childMatchKeyByModel 返回按 parent+展示名 的匹配键（legacy 兜底）。
func childMatchKeyByModel(m ServiceConfig) string {
	return strings.TrimSpace(m.Parent) + "\x00" + strings.TrimSpace(m.Model)
}

// copyAdminHiddenFields 把 src 中 admin PUT 不应改写的监测行字段复制到 dst：
// 含 json:"-" 持久化字段，以及对客户端可见但不可变的 model_id（强制从 existing 还原以保证 id 不可变）。
// 注意：KeyType 和 AutoColdExempt 已改为 JSON 可见字段，通过 API round-trip 传递，无需在此回填。
func copyAdminHiddenFields(dst, src *ServiceConfig) {
	dst.ModelID = src.ModelID
	dst.EnvVarName = src.EnvVarName
	dst.RequestModel = src.RequestModel
	dst.SkipURLValidation = src.SkipURLValidation
	dst.URLPattern = src.URLPattern
}

// Update 更新监测文件。使用 revision 乐观锁防止并发覆盖。
func (s *MonitorStore) Update(key string, file *MonitorFile, expectedRevision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var err error
	key, err = SanitizeMonitorKey(key)
	if err != nil {
		return err
	}

	path, err := s.findExistingPath(key)
	if err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("PSC %s 不存在", key)
	}

	existing, err := loadMonitorFile(path)
	if err != nil {
		return err
	}

	if existing.Metadata.Revision != expectedRevision {
		return fmt.Errorf("revision 不匹配: 期望 %d，实际 %d（文件已被其他操作修改）",
			expectedRevision, existing.Metadata.Revision)
	}

	// 校验 PSC 不可变：更新后的内容推导出的 key 必须与 URL key 一致
	newKey, err := DeriveMonitorFileKey(*file)
	if err != nil {
		return fmt.Errorf("推导 PSC key 失败: %w", err)
	}
	newKey, err = SanitizeMonitorKey(newKey)
	if err != nil {
		return fmt.Errorf("PSC key 无效: %w", err)
	}
	if newKey != key {
		return fmt.Errorf("PSC 不可变更: %s -> %s", key, newKey)
	}

	// 回填 json:"-" 字段，防止 admin API round-trip 丢失
	preserveAdminHiddenFields(file, &existing)

	file.Metadata.Revision = expectedRevision + 1
	file.Metadata.CreatedAt = existing.Metadata.CreatedAt // 保留创建时间
	file.Metadata.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := AtomicWriteYAML(path, file); err != nil {
		return err
	}

	file.Path = path
	file.Key = key
	return nil
}

// Delete 归档删除：移动到 monitors.d/.archive/{filename}.{timestamp}.yaml。
func (s *MonitorStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var err error
	key, err = SanitizeMonitorKey(key)
	if err != nil {
		return err
	}

	path, err := s.findExistingPath(key)
	if err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("PSC %s 不存在", key)
	}

	archiveDir := filepath.Join(s.dir, ".archive")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return fmt.Errorf("创建 .archive 目录失败: %w", err)
	}

	ts := time.Now().UTC().Format("20060102T150405Z")
	archiveName := fmt.Sprintf("%s.%s%s", key, ts, filepath.Ext(path))
	archivePath := filepath.Join(archiveDir, archiveName)

	if err := os.Rename(path, archivePath); err != nil {
		return fmt.Errorf("归档文件失败: %w", err)
	}

	return nil
}
