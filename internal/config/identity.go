package config

import (
	"fmt"

	"github.com/google/uuid"
)

// 通道/模型稳定 id 的语义前缀：跨产品自描述、防止两类 id 混用。
const (
	channelIDPrefix = "ch_"
	modelIDPrefix   = "md_"
)

// NewChannelID 生成通道级稳定 id（ch_<uuidv4>）。
func NewChannelID() string { return channelIDPrefix + uuid.NewString() }

// NewModelID 生成监测行级稳定 id（md_<uuidv4>）。
func NewModelID() string { return modelIDPrefix + uuid.NewString() }

// IsValidChannelID 判断是否为带通道前缀的合法 id。
func IsValidChannelID(id string) bool { return isValidPrefixedUUID(id, channelIDPrefix) }

// IsValidModelID 判断是否为带模型前缀的合法 id。
func IsValidModelID(id string) bool { return isValidPrefixedUUID(id, modelIDPrefix) }

// isValidPrefixedUUID 校验 id = 指定前缀 + 合法 uuid。
// 仅拒绝畸形输入（前缀错误 / 空主体 / 非 uuid）；不强制 v4/小写/非 nil——
// 生成路径已是 uuid.NewString()（v4），收紧只增噪不增安全。
func isValidPrefixedUUID(id, prefix string) bool {
	if len(id) <= len(prefix) || id[:len(prefix)] != prefix {
		return false
	}
	_, err := uuid.Parse(id[len(prefix):])
	return err == nil
}

// CollectModelIDs 跨文件收集所有非空 model_id，返回 id→定位串映射；
// 发现全局重复（同一 model_id 出现在两处）返回 error，错误信息含该重复 id。
// 空 model_id 跳过（回填前的现有行合法，空值由 validate 另判）。
func CollectModelIDs(files []MonitorFile) (map[string]string, error) {
	seen := make(map[string]string)
	for _, f := range files {
		if err := collectModelIDsInto(seen, f.Monitors); err != nil {
			return nil, err
		}
	}
	return seen, nil
}

// collectModelIDsInto 把一组监测行的非空 model_id 累积进 seen，撞重即返回 error。
// 抽出供扁平的 AppConfig.Monitors（validate.go）与按文件的 CollectModelIDs 复用同一去重核。
func collectModelIDsInto(seen map[string]string, monitors []ServiceConfig) error {
	for _, m := range monitors {
		if m.ModelID == "" {
			continue
		}
		if prev, ok := seen[m.ModelID]; ok {
			return fmt.Errorf("model_id 重复: %s 同时用于 %s 和 %s", m.ModelID, prev, modelIDLocation(m))
		}
		seen[m.ModelID] = modelIDLocation(m)
	}
	return nil
}

// modelIDLocation 给出监测行的人类可读定位串，用于重复/校验报错。
func modelIDLocation(m ServiceConfig) string {
	return fmt.Sprintf("%s/%s/%s/%s", m.Provider, m.Service, m.Channel, m.Model)
}
