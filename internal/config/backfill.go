package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// BackfillReport 汇总一次目录级稳定 id 回填的结果。
type BackfillReport struct {
	FilesScanned    int // 扫描的 monitor 定义文件数
	FilesChanged    int // 实际补了 id 的文件数
	ChannelIDsAdded int // 新补的 channel_id 数（每文件至多 1）
	ModelIDsAdded   int // 新补的 model_id 数（按监测行计）
}

// BackfillDir 给 monitorsDir 下每个 monitor 定义文件补齐缺失的 channel_id/model_id。
// dryRun=true 时只统计将发生的改动、不落盘。任一文件解析失败即 fail-fast 返回——
// 回填是一次性运维动作，坏文件须显式暴露，不静默跳过。
// 单文件经 AtomicWriteYAML 原子写入；中途失败已写入的文件因回填幂等可安全重跑续补。
func BackfillDir(monitorsDir string, dryRun bool) (BackfillReport, error) {
	entries, err := os.ReadDir(monitorsDir)
	if err != nil {
		return BackfillReport{}, fmt.Errorf("读取 %s 失败: %w", monitorsDir, err)
	}

	var report BackfillReport
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !isMonitorDefinitionFile(name) {
			continue
		}
		report.FilesScanned++

		path := filepath.Join(monitorsDir, name)
		file, err := loadMonitorFile(path)
		if err != nil {
			return report, fmt.Errorf("%s: %w", name, err)
		}

		// 补前先数空缺数（BackfillFileIDs 会就地填充）。
		channelMissing := 0
		if file.Metadata.ChannelID == "" {
			channelMissing = 1
		}
		modelMissing := 0
		for i := range file.Monitors {
			if file.Monitors[i].ModelID == "" {
				modelMissing++
			}
		}

		if !BackfillFileIDs(&file) {
			continue
		}
		report.FilesChanged++
		report.ChannelIDsAdded += channelMissing
		report.ModelIDsAdded += modelMissing

		if !dryRun {
			if err := AtomicWriteYAML(path, &file); err != nil {
				return report, fmt.Errorf("写入 %s 失败: %w", name, err)
			}
		}
	}
	return report, nil
}
