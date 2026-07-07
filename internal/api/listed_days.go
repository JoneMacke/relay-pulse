package api

import (
	"strings"
	"time"
)

// listedDaysTZ 是"收录天数"计算使用的运营业务时区（中国 UTC+8，无夏令时）。
// 用固定偏移而非 time.LoadLocation("Asia/Shanghai")，避免精简 Alpine 镜像缺 tzdata；
// 与仓库既有先例一致（automove.sponsorExpiryTZ、notifier/internal/notifier/channel_telegram.go）。
var listedDaysTZ = time.FixedZone("CST", 8*60*60)

// listedDaysSince 计算 listed_since（monitor.ListedSince，格式 YYYY-MM-DD）到 nowUTC
// 经过的 CST 日历日天数——收录当天为 0，CST 次日零点起变 1，以此类推。
// 空值或解析失败返回 nil（字段不输出，与既有行为一致）；未来日期钳制为 0。
func listedDaysSince(listedSince string, nowUTC time.Time) *int {
	listedSince = strings.TrimSpace(listedSince)
	if listedSince == "" {
		return nil
	}

	listedDate, err := time.ParseInLocation("2006-01-02", listedSince, listedDaysTZ)
	if err != nil {
		return nil
	}

	todayCST := nowUTC.In(listedDaysTZ)
	todayMidnight := time.Date(todayCST.Year(), todayCST.Month(), todayCST.Day(), 0, 0, 0, 0, listedDaysTZ)

	days := int(todayMidnight.Sub(listedDate) / (24 * time.Hour))
	if days < 0 {
		days = 0
	}
	return &days
}
