import { describe, it, expect } from 'vitest';

import { buildRpdiagKey, buildRpdiagCidKey, lookupRpdiagScore } from './useRpdiagScores';
import type { RpdiagScore, RpdiagScoresResponse } from '../types/monitor';

describe('buildRpdiagKey', () => {
  it('joins by raw channel_name without stripping the source prefix', () => {
    // "O-Max" keeps its prefix → "o-max" (no strip to "max").
    expect(buildRpdiagKey('SAIAi', 'cc', 'O-Max')).toBe('saiai|cc|o-max');
    // The two codex tiers of one provider stay distinct instead of both
    // collapsing to "...|cx".
    expect(buildRpdiagKey('right.codes', 'cx', 'o-cx')).toBe('right.codes|cx|o-cx');
    expect(buildRpdiagKey('right.codes', 'cx', 'u-cx')).toBe('right.codes|cx|u-cx');
  });

  it('trims and lower-cases each segment', () => {
    expect(buildRpdiagKey('  SaiAI ', ' CC ', '  O-Max ')).toBe('saiai|cc|o-max');
  });
});

describe('buildRpdiagCidKey', () => {
  it('namespaces a trimmed channel id, undefined when empty', () => {
    // Opaque id: trimmed but NOT lower-cased (symmetric with the Go cidBucketKey).
    expect(buildRpdiagCidKey('ch_a')).toBe('cid:ch_a');
    expect(buildRpdiagCidKey('CH_A')).toBe('cid:CH_A'); // case preserved, not lowered
    expect(buildRpdiagCidKey('  ch_a ')).toBe('cid:ch_a');
    expect(buildRpdiagCidKey('')).toBeUndefined();
    expect(buildRpdiagCidKey('   ')).toBeUndefined();
    expect(buildRpdiagCidKey(undefined)).toBeUndefined();
  });
});

describe('lookupRpdiagScore', () => {
  const score = (max: number): RpdiagScore => ({ max_score: max }) as RpdiagScore;
  const scores: RpdiagScoresResponse = {
    'right.codes|cx|o-cx': score(95),
    'right.codes|cx|u-cx': score(90),
  };

  it('resolves o-cx and u-cx codex tiers to separate scores', () => {
    expect(lookupRpdiagScore(scores, 'right.codes', 'cx', 'o-cx')?.max_score).toBe(95);
    expect(lookupRpdiagScore(scores, 'right.codes', 'cx', 'u-cx')?.max_score).toBe(90);
  });

  it('returns undefined for a stripped key, missing args, or missing map', () => {
    // The old prefix-stripped key must no longer resolve.
    expect(lookupRpdiagScore(scores, 'right.codes', 'cx', 'cx')).toBeUndefined();
    expect(lookupRpdiagScore(scores, undefined, 'cx', 'o-cx')).toBeUndefined();
    expect(lookupRpdiagScore(undefined, 'right.codes', 'cx', 'o-cx')).toBeUndefined();
  });

  describe('stable channel_id priority (cid bucket)', () => {
    const cidScores: RpdiagScoresResponse = {
      'cid:ch_a': score(91),
      // Same logical channel still exposed under a legacy triple (transition window).
      'p|cc|o-max': score(1),
    };

    it('prefers the cid bucket when channelId is present', () => {
      expect(lookupRpdiagScore(cidScores, ['P'], 'cc', 'O-Max', 'ch_a')?.max_score).toBe(91);
    });

    it('falls back to the triple when the cid bucket misses', () => {
      expect(lookupRpdiagScore(cidScores, ['P'], 'cc', 'O-Max', 'ch_missing')?.max_score).toBe(1);
    });

    it('uses the triple when channelId is absent (back-compat)', () => {
      expect(lookupRpdiagScore(cidScores, ['P'], 'cc', 'O-Max')?.max_score).toBe(1);
    });

    it('resolves a cid hit even without service/channel (cid key is self-sufficient)', () => {
      expect(lookupRpdiagScore(cidScores, undefined, undefined, undefined, 'ch_a')?.max_score).toBe(91);
    });
  });

  describe('provider candidate fallback (display name first, slug fallback)', () => {
    // Index is keyed by rpdiag's display name (provider_name); relaypulse may
    // carry a slug that differs. Lookup tries [providerName, providerSlug].
    const driftScores: RpdiagScoresResponse = {
      // Display name ≠ slug: dot dropped / extra letter in the slug.
      'worldbase.ai|cc|m-': score(72),
      'yunwu|cc|o-api': score(19),
      // A normal provider whose display name == slug.
      'aimz|cc|m-max': score(80),
    };

    it('joins slug≠display-name providers via the display-name candidate', () => {
      // WorldBase.ai: slug "worldbase", display "WorldBase.ai".
      expect(
        lookupRpdiagScore(driftScores, ['WorldBase.ai', 'worldbase'], 'cc', 'M-')?.max_score,
      ).toBe(72);
      // YunWu: slug "yunwui", display "YunWu".
      expect(
        lookupRpdiagScore(driftScores, ['YunWu', 'yunwui'], 'cc', 'O-Api')?.max_score,
      ).toBe(19);
    });

    it('falls back to the slug candidate when the display name misses', () => {
      // Display name absent (whitespace) / desynced → slug still joins, no regression.
      expect(lookupRpdiagScore(driftScores, ['   ', 'aimz'], 'cc', 'M-Max')?.max_score).toBe(80);
      expect(lookupRpdiagScore(driftScores, ['Totally Different', 'aimz'], 'cc', 'M-Max')?.max_score).toBe(80);
    });

    it('still accepts a single string provider (back-compat)', () => {
      expect(lookupRpdiagScore(driftScores, 'aimz', 'cc', 'M-Max')?.max_score).toBe(80);
    });

    it('returns undefined when no candidate matches', () => {
      expect(lookupRpdiagScore(driftScores, ['nope', 'nada'], 'cc', 'M-Max')).toBeUndefined();
      expect(lookupRpdiagScore(driftScores, ['   ', undefined], 'cc', 'M-Max')).toBeUndefined();
    });
  });
});
