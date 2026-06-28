// backfillids 给 monitors.d/ 下的 monitor 定义文件幂等补齐缺失的稳定 id
// （channel_id 文件级 / model_id 每行）。已有 id 绝不覆盖。
//
// 用法:
//
//	go run ./cmd/backfillids -dir ./monitors.d [-dry-run]
package main

import (
	"flag"
	"fmt"
	"os"

	"monitor/internal/config"
)

func main() {
	dir := flag.String("dir", "./monitors.d", "monitors.d 目录路径")
	dryRun := flag.Bool("dry-run", false, "仅预览，不实际写入")
	flag.Parse()

	report, err := config.BackfillDir(*dir, *dryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}

	if *dryRun {
		fmt.Println("[dry-run] 仅预览，未写入任何文件")
	}
	fmt.Printf("扫描 %d 文件，改动 %d 文件，补 channel_id %d、model_id %d\n",
		report.FilesScanned, report.FilesChanged, report.ChannelIDsAdded, report.ModelIDsAdded)
}
