package rpdiag

import (
	"sort"
	"strings"
	"time"
	"unicode"
)

// QualityState is a tri-state classification of a channel's current quality
// signal, used by the automove feature. The order matters: it is a STRICT
// priority ladder HardFail > Recovered > Unknown, and QualityUnknown MUST NOT
// collapse into QualityRecovered — an aged-out channel with no fresh evaluation
// is "we can't tell" (Unknown), not "it recovered" (Recovered). Faking a
// recovery from stale data would silently pull a channel back off the backup
// board with no successful eval to justify it.
type QualityState int

const (
	QualityUnknown QualityState = iota
	QualityRecovered
	QualityHardFail
)

// ChannelQualitySignal is one channel's classified quality signal. When State
// is QualityHardFail, TriggerModels names the active models whose last three
// quality attempts were all hard-fail (dedup'd, stably sorted, sanitized);
// otherwise it is empty.
type ChannelQualitySignal struct {
	State         QualityState
	TriggerModels []string
}

// triggerModelNameMaxRunes bounds a trigger model name's length. The name
// originates from rpdiag wire data and flows onward into persisted overrides,
// API responses, and logs, so it is capped to keep those surfaces bounded.
const triggerModelNameMaxRunes = 64

// buildQualitySignalsAt classifies each channel's current quality signal from a
// set of rpdiag ranking rows. It mirrors buildScoresAt's two-pass structure so
// the two stay aligned on the hard-fail / staleness / bucketing rules:
//
//	Pass 1 reduces every consumable row to a scoreRowView and learns which
//	models are still alive somewhere, keyed by (service, modelKey) from FRESH
//	views only. A hard-fail row is never fresh, so a currently-failing model is
//	recognized as "active" only when a sibling channel reports it fresh — the
//	same cross-channel activeness rule buildScoresAt uses.
//
//	Buckets are then consolidated to stable-id (cid) keys where available, exactly
//	as buildScoresAt does, by rewriting each view's key.
//
//	Pass 2 aggregates per bucket: a channel triggers HardFail when an active model
//	has its tail-three attempts all null; hasHealthy records whether the channel
//	has any fresh row (genuine recovery evidence, since aged-out data is never
//	fresh). HardFail wins over Recovered wins over Unknown.
func buildQualitySignalsAt(rows []rankingRow, now time.Time) map[string]ChannelQualitySignal {
	views := make([]scoreRowView, 0, len(rows))
	active := make(map[[2]string]bool)
	for _, row := range rows {
		view, ok := buildScoreRowView(row, now)
		if !ok {
			continue
		}
		views = append(views, view)
		if view.fresh {
			active[activeModelKey(view.service, view.modelKey)] = true
		}
	}

	bucketKeys := resolveBucketKeys(views)
	for i := range views {
		views[i].key = bucketKeys[views[i].key]
	}

	// hasTrigger (the HardFail verdict) is tracked separately from the trigger
	// name-set: a trigger whose model name sanitizes to empty must still flip the
	// channel to HardFail, without inserting an unusable "" into TriggerModels.
	type bucketAgg struct {
		hasHealthy bool
		hasTrigger bool
		triggers   map[string]bool
	}
	byBucket := make(map[string]*bucketAgg, len(views))
	for _, view := range views {
		agg := byBucket[view.key]
		if agg == nil {
			agg = &bucketAgg{triggers: make(map[string]bool)}
			byBucket[view.key] = agg
		}
		if view.fresh {
			agg.hasHealthy = true
		}
		if active[activeModelKey(view.service, view.modelKey)] && isTailThreeHardFail(view.row) {
			agg.hasTrigger = true
			if name := triggerModelName(view.row); name != "" {
				agg.triggers[name] = true
			}
		}
	}

	out := make(map[string]ChannelQualitySignal, len(byBucket))
	for key, agg := range byBucket {
		switch {
		case agg.hasTrigger:
			models := make([]string, 0, len(agg.triggers))
			for m := range agg.triggers {
				models = append(models, m)
			}
			sort.Strings(models)
			out[key] = ChannelQualitySignal{State: QualityHardFail, TriggerModels: models}
		case agg.hasHealthy:
			out[key] = ChannelQualitySignal{State: QualityRecovered}
		default:
			out[key] = ChannelQualitySignal{State: QualityUnknown}
		}
	}
	return out
}

// QualitySnapshot is the quality projection of one cached refresh. Generation
// identifies which cache produced it; Fresh means the local cache is unexpired
// AND the last refresh succeeded. ByBucket keys match buildQualitySignalsAt's
// bucketing ("cid:"+id or a legacy provider|service|channel triple).
type QualitySnapshot struct {
	Generation uint64
	Fresh      bool
	ByBucket   map[string]ChannelQualitySignal
}

// Lookup mirrors the frontend lookupRpdiagScore join: a non-empty channelID tries
// the cid bucket first (trim only, case-sensitive — cid is opaque); otherwise/on
// miss it tries each provider candidate against the canonical (trim+lower)
// provider|service|channel triple, skipping blank candidates. Total miss returns
// QualityUnknown; a nil ByBucket is safe and also returns QualityUnknown.
func (s QualitySnapshot) Lookup(providerCandidates []string, service, channel, channelID string) ChannelQualitySignal {
	if s.ByBucket == nil {
		return ChannelQualitySignal{State: QualityUnknown}
	}
	if cid := strings.TrimSpace(channelID); cid != "" {
		if sig, ok := s.ByBucket[cidBucketKey(cid)]; ok {
			return sig
		}
	}
	// No empty-segment early-out is needed: buildScoreRowView drops rows whose
	// provider/service/channel is blank, so no ByBucket key has an empty segment;
	// a blank-segment lookup simply misses -> Unknown (matches frontend behavior).
	for _, p := range providerCandidates {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if sig, ok := s.ByBucket[ScoreKey(p, service, channel)]; ok {
			return sig
		}
	}
	return ChannelQualitySignal{State: QualityUnknown}
}

// isTailThreeHardFail reports whether the row's three most-recent attempts are
// all hard-fail (nil) AND rpdiag currently marks the row hard-fail-active. Fewer
// than three attempts (cold start / thin history) is never enough evidence.
func isTailThreeHardFail(row rankingRow) bool {
	attempts := row.ScoreTrend.RecentAttempts
	if len(attempts) < 3 {
		return false
	}
	for _, a := range attempts[len(attempts)-3:] {
		if a != nil {
			return false
		}
	}
	return row.HardFailActive
}

// activeModelKey is the collision-free (service, model) identity used for the
// active-model set. A two-element array (not a concatenated string) is used so
// distinct pairs whose parts merely straddle a delimiter — ("a|b","c") vs
// ("a","b|c") — can never collapse into the same key. Inputs are already
// canonical on a scoreRowView; canonicalizing again is idempotent and keeps the
// key correct for any other caller.
func activeModelKey(service, model string) [2]string {
	return [2]string{canonical(service), canonical(model)}
}

// triggerModelName derives the human-readable model name for a trigger, favoring
// rpdiag's ModelKey and falling back to Model. For EACH candidate it strips
// control characters BEFORE trimming (so a control-only value collapses to empty
// and a control-padded value trims cleanly), then caps the length because the
// value crosses trust boundaries (it is persisted in overrides and echoed to
// API/logs). Returns "" when both candidates sanitize away.
func triggerModelName(row rankingRow) string {
	name := sanitizeModelName(row.ModelKey)
	if name == "" {
		name = sanitizeModelName(row.Model)
	}
	if runes := []rune(name); len(runes) > triggerModelNameMaxRunes {
		name = string(runes[:triggerModelNameMaxRunes])
	}
	return name
}

// sanitizeModelName strips control characters first, then trims surrounding
// whitespace. Order matters: trimming first would leave interior control chars
// that, once removed, expose untrimmed whitespace (e.g. "\x00 opus \x00").
func sanitizeModelName(raw string) string {
	stripped := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, raw)
	return strings.TrimSpace(stripped)
}
