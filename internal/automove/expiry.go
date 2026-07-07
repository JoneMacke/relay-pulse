package automove

import (
	"strings"
	"time"
)

// sponsorExpiryTZ 是赞助到期判断使用的运营业务时区（中国 UTC+8，无夏令时）。
// 用固定偏移而非 time.LoadLocation("Asia/Shanghai")，避免精简 Alpine 镜像缺 tzdata 导致启动期解析失败；
// 与仓库既有先例一致（notifier/internal/notifier/channel_telegram.go 同样用 FixedZone 表示 CST）。
var sponsorExpiryTZ = time.FixedZone("CST", 8*60*60)

// isSponsorExpired 判断赞助到期日（monitor.ExpiresAt，格式 YYYY-MM-DD）相对 nowUTC 是否已过期。
// 到期日当天仍有效，次日 00:00（CST）起判定过期——这是运营对外承诺的口径（docs/user/sponsorship.md）。
// 注意：不要用 UTC 天数截断实现这个判断——UTC 与 CST 相差 8 小时，会让"次日"晚触发最多 8 小时。
// 空值或解析失败一律视为未过期（不误伤未配置到期日的通道）。
func isSponsorExpired(expiresAt string, nowUTC time.Time) bool {
	expiresAt = strings.TrimSpace(expiresAt)
	if expiresAt == "" {
		return false
	}

	expiresDate, err := time.ParseInLocation("2006-01-02", expiresAt, sponsorExpiryTZ)
	if err != nil {
		return false
	}

	cutoff := expiresDate.AddDate(0, 0, 1) // 到期日次日 00:00 CST
	return !nowUTC.Before(cutoff)
}
