import { describe, it, expect } from 'vitest';
import { normalizeDisplayName, isProviderNameValid, isChannelNameValid } from './displayName';

// 不可见字符用显式 \u 转义：BOM=\ufeff  NEL=\u0085  ZWSP=\u200b  RLO=\u202e

describe('normalizeDisplayName', () => {
  it('strips edge whitespace', () => expect(normalizeDisplayName('  赛博AI  ')).toBe('赛博AI'));
  it('strips edge BOM', () => expect(normalizeDisplayName('\ufeff赛博AI\ufeff')).toBe('赛博AI'));
  it('strips edge NEL', () => expect(normalizeDisplayName('\u0085Sai\u0085')).toBe('Sai'));
  it('strips edge ZWSP', () => expect(normalizeDisplayName('\u200b赛博AI\u200b')).toBe('赛博AI'));
  it('keeps interior invisible (rejected later, not stripped)', () =>
    expect(normalizeDisplayName('Sai\u200bAI')).toBe('Sai\u200bAI'));
  it('pure invisible -> empty', () => expect(normalizeDisplayName('\u200b\ufeff')).toBe(''));
});

describe('isProviderNameValid', () => {
  it('accepts chinese', () => expect(isProviderNameValid('赛博AI')).toBe(true));
  it('accepts edge BOM (stripped)', () => expect(isProviderNameValid('\ufeff赛博AI')).toBe(true));
  it('accepts edge NEL (stripped)', () => expect(isProviderNameValid('\u0085Sai AI')).toBe(true));
  it('rejects interior zero width', () => expect(isProviderNameValid('Sai\u200bAI')).toBe(false));
  it('rejects interior bidi', () => expect(isProviderNameValid('Sai\u202eAI')).toBe(false));
  it('rejects empty', () => expect(isProviderNameValid('')).toBe(false));
  it('rejects pure invisible', () => expect(isProviderNameValid('\u200b\ufeff')).toBe(false));
  it('rejects over 100 runes', () => expect(isProviderNameValid('赛'.repeat(101))).toBe(false));
  it('accepts exactly 100 runes', () => expect(isProviderNameValid('赛'.repeat(100))).toBe(true));
});

describe('isChannelNameValid', () => {
  it('accepts empty (optional)', () => expect(isChannelNameValid('')).toBe(true));
  it('accepts pure invisible -> empty', () => expect(isChannelNameValid('\u200b')).toBe(true));
  it('accepts chinese', () => expect(isChannelNameValid('华东线路')).toBe(true));
  it('rejects interior bidi', () => expect(isChannelNameValid('线\u202e路')).toBe(false));
  it('rejects interior zero width', () => expect(isChannelNameValid('线\u200b路')).toBe(false));
  it('rejects over 40 runes', () => expect(isChannelNameValid('赛'.repeat(41))).toBe(false));
  it('accepts exactly 40 runes', () => expect(isChannelNameValid('赛'.repeat(40))).toBe(true));
});
