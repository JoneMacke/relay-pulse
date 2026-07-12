// 展示名（provider_name / channel_name）前端规范化与校验——镜像后端 internal/displayname。
// 统一 trim 口径：剥首尾「空白 ∪ Cc/Cf/Zl/Zp」，消除 BOM(U+FEFF：JS .trim() 剥 / Go TrimSpace
// 不剥) 与 NEL(U+0085：反向) 的前后端漂移；内部危险不可见字符仍判非法。

/** 服务商展示名长度上限（rune/code point），与后端 displayname.ProviderNameMaxRunes 一致。 */
export const PROVIDER_NAME_MAX = 100;
/** 通道展示名长度上限（rune/code point），与后端 displayname.ChannelNameMaxRunes 一致。 */
export const CHANNEL_NAME_MAX = 40;

// 内部危险不可见字符：控制符(Cc) / 格式符(Cf，含 bidi 方向控制符、零宽、BOM) / 行段分隔符(Zl/Zp)。
const DISALLOWED = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u;

/**
 * 规范化展示名：剥首尾「空白 ∪ 危险不可见字符」。内部非法字符不在此处理（由 isValid 判定）。
 * 与后端 strings.TrimFunc(isSpace || Cc/Cf/Zl/Zp) 等价，保证前后端对首尾字符剥除口径一致。
 */
export function normalizeDisplayName(raw: string): string {
  return raw
    .replace(/^[\s\p{Cc}\p{Cf}\p{Zl}\p{Zp}]+/u, '')
    .replace(/[\s\p{Cc}\p{Cf}\p{Zl}\p{Zp}]+$/u, '');
}

/** 按 code point 计长度（一个汉字/emoji 记 1，避免 UTF-16 code unit 高估）。 */
function runeLength(s: string): number {
  return Array.from(s).length;
}

/** 服务商展示名是否合法（必填）：规范化后非空、≤PROVIDER_NAME_MAX rune、内部无危险字符。 */
export function isProviderNameValid(raw: string): boolean {
  const v = normalizeDisplayName(raw);
  return v.length > 0 && runeLength(v) <= PROVIDER_NAME_MAX && !DISALLOWED.test(v);
}

/** 通道展示名是否合法（可选）：规范化后空视为合法；非空则 ≤CHANNEL_NAME_MAX rune、内部无危险字符。 */
export function isChannelNameValid(raw: string): boolean {
  const v = normalizeDisplayName(raw);
  if (v.length === 0) return true;
  return runeLength(v) <= CHANNEL_NAME_MAX && !DISALLOWED.test(v);
}
