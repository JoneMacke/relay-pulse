// Package displayname 提供中转商展示名（provider_name / channel_name）的统一安全校验，
// 作为收录(onboarding)与变更请求(change)两条**非管理员**输入路径的单一真相源。
//
// 校验策略（provider 与 channel 同族，唯一区别是 provider 必填、channel 可空）：
//  1. 拒绝无效 UTF-8。
//  2. 剥除首尾「Unicode 空白 ∪ 危险不可见字符(Cc/Cf/Zl/Zp)」——统一前后端 trim 口径，
//     消除 BOM(U+FEFF：Go TrimSpace 不剥 / JS .trim() 剥) 与 NEL(U+0085：反向) 的漂移。
//  3. 拒绝**剩余内部**含控制符(Cc)/格式符(Cf，含 bidi 方向控制符与 ZWSP/ZWJ/BOM 等零宽)/行段分隔符(Zl/Zp)，
//     防不可见字符欺骗、显示方向劫持、状态页折行。
//  4. 必填/可空语义判定。
//  5. 长度按 rune 计。
//
// 注意：首尾危险不可见字符被**规范化删除**（非拒绝），仅内部出现才拒绝；返回规范值。
// 管理员逃生口（AdminConfigJSON 整份覆盖、monitors CRUD、直编 yaml/DB）刻意不经此校验。
package displayname

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// ProviderNameMaxRunes 服务商展示名长度上限（按 rune 计）。
	ProviderNameMaxRunes = 100
	// ChannelNameMaxRunes 通道展示名长度上限（按 rune 计）：状态页通道列空间有限。
	ChannelNameMaxRunes = 40
)

// isDisallowed 判定展示名**内部**禁止字符：控制符 / 格式符 / 行段分隔符。
func isDisallowed(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
}

// isEdgeStrippable 判定**首尾**可剥字符：Unicode 空白 ∪ 危险不可见字符。
// 二者并集使 Go 侧（IsSpace 含 NEL 不含 BOM）与 JS 侧（\s 含 BOM 不含 NEL）剥完口径一致。
func isEdgeStrippable(r rune) bool {
	return unicode.IsSpace(r) || isDisallowed(r)
}

// ValidateProviderName 校验服务商展示名（必填）。返回规范值。
func ValidateProviderName(value string) (string, error) {
	return validate(value, "服务商展示名（provider_name）", true, ProviderNameMaxRunes)
}

// ValidateChannelName 校验通道展示名（可选）。空值合法、返回空串。
func ValidateChannelName(value string) (string, error) {
	return validate(value, "channel_name", false, ChannelNameMaxRunes)
}

func validate(value, label string, required bool, maxRunes int) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%s 包含无效的 UTF-8 编码", label)
	}
	value = strings.TrimFunc(value, isEdgeStrippable)
	if value == "" {
		if required {
			return "", fmt.Errorf("%s 不能为空", label)
		}
		return "", nil
	}
	for _, r := range value {
		if isDisallowed(r) {
			return "", fmt.Errorf("%s 格式无效（%q），不能包含控制字符、双向文本控制符、零宽字符或行分隔符", label, value)
		}
	}
	if n := utf8.RuneCountInString(value); n > maxRunes {
		return "", fmt.Errorf("%s 过长（%d 个字符），应不超过 %d 个字符", label, n, maxRunes)
	}
	return value, nil
}
