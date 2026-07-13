package rpdiag

import (
	"reflect"
	"testing"
)

// qMk is a local *float64 helper (mirrors the mk closures in client_test.go)
// so quality fixtures can spell out RecentAttempts / scores inline.
func qMk(v float64) *float64 { return &v }

// TestBuildQualitySignals_TailThreeAllNull_Triggers: a hard-fail target whose
// last three attempts are all null (grey), for a model proven active by a fresh
// sibling channel, must flip the channel to QualityHardFail and name the model.
func TestBuildQualitySignals_TailThreeAllNull_Triggers(t *testing.T) {
	rows := []rankingRow{
		{ // target: hard-fail, tail-3 all null
			ChannelName: "Acme-CC", ProviderName: "Acme", ServiceCLICommand: "claude",
			Model: "claude-opus", ModelKey: "claude-opus",
			HardFailActive: true,
			ScoreTrend:     ScoreTrend{RecentAttempts: []*float64{qMk(80), nil, nil, nil}},
		},
		{ // fresh sibling keeps (claude, claude-opus) globally active
			ChannelName: "Healthy-CC", ProviderName: "Healthy", ServiceCLICommand: "claude",
			Model: "claude-opus", ModelKey: "claude-opus",
			ScoreTrend: ScoreTrend{Latest: qMk(97), LatestAt: freshAt()},
		},
	}

	snap := buildQualitySignalsAt(rows, fixedClock())

	key := ScoreKey("Acme", "cc", "Acme-CC")
	got, ok := snap[key]
	if !ok {
		t.Fatalf("target channel %q missing from snapshot %v", key, snap)
	}
	if got.State != QualityHardFail {
		t.Fatalf("State = %v, want QualityHardFail", got.State)
	}
	if want := []string{"claude-opus"}; !reflect.DeepEqual(got.TriggerModels, want) {
		t.Fatalf("TriggerModels = %v, want %v", got.TriggerModels, want)
	}
}

// TestBuildQualitySignals_HardFailButModelNotActiveElsewhere_Unknown: a hard-fail
// row whose model is proven active nowhere carries no current signal (aged-out /
// retired model), so it must NOT trigger — QualityUnknown, not QualityHardFail.
func TestBuildQualitySignals_HardFailButModelNotActiveElsewhere_Unknown(t *testing.T) {
	rows := []rankingRow{
		{
			ChannelName: "Acme-CC", ProviderName: "Acme", ServiceCLICommand: "claude",
			Model: "claude-opus", ModelKey: "claude-opus",
			HardFailActive: true,
			ScoreTrend:     ScoreTrend{RecentAttempts: []*float64{nil, nil, nil}},
		},
	}

	snap := buildQualitySignalsAt(rows, fixedClock())

	key := ScoreKey("Acme", "cc", "Acme-CC")
	got, ok := snap[key]
	if !ok {
		t.Fatalf("target channel %q missing from snapshot %v", key, snap)
	}
	if got.State != QualityUnknown {
		t.Fatalf("State = %v, want QualityUnknown (model not active anywhere)", got.State)
	}
	if len(got.TriggerModels) != 0 {
		t.Fatalf("TriggerModels = %v, want empty", got.TriggerModels)
	}
}

// TestBuildQualitySignals_TailShorterThanThree_Unknown: the model is active (fresh
// sibling) and the target is hard-fail, but fewer than three attempts are on the
// tail — cold-start / thin history is not enough evidence to auto-move, so
// QualityUnknown. Asserts the channel is genuinely present (real path, not a
// map-miss zero value).
func TestBuildQualitySignals_TailShorterThanThree_Unknown(t *testing.T) {
	rows := []rankingRow{
		{ // target: hard-fail but only two attempts on the tail
			ChannelName: "Acme-CC", ProviderName: "Acme", ServiceCLICommand: "claude",
			Model: "claude-opus", ModelKey: "claude-opus",
			HardFailActive: true,
			ScoreTrend:     ScoreTrend{RecentAttempts: []*float64{nil, nil}},
		},
		{ // fresh sibling keeps the model active
			ChannelName: "Healthy-CC", ProviderName: "Healthy", ServiceCLICommand: "claude",
			Model: "claude-opus", ModelKey: "claude-opus",
			ScoreTrend: ScoreTrend{Latest: qMk(96), LatestAt: freshAt()},
		},
	}

	snap := buildQualitySignalsAt(rows, fixedClock())

	key := ScoreKey("Acme", "cc", "Acme-CC")
	got, ok := snap[key]
	if !ok {
		t.Fatalf("target channel %q missing from snapshot %v", key, snap)
	}
	if got.State != QualityUnknown {
		t.Fatalf("State = %v, want QualityUnknown (tail shorter than three)", got.State)
	}
	if len(got.TriggerModels) != 0 {
		t.Fatalf("TriggerModels = %v, want empty", got.TriggerModels)
	}
}

// TestBuildQualitySignals_DelimiterCollisionDoesNotCrossActivate: two DIFFERENT
// (service, model) pairs that concatenate to the same "service|model" string
// must not cross-activate. Target (service="a", model="b|c") is hard-fail; the
// only fresh row is a genuinely different pair (service="a|b", model="c") that
// collides under naive concatenation ("a|b|c"). With a collision-free key the
// target's model is proven active nowhere -> QualityUnknown, not QualityHardFail.
func TestBuildQualitySignals_DelimiterCollisionDoesNotCrossActivate(t *testing.T) {
	rows := []rankingRow{
		{ // target: service "a", model "b|c" -> naive key "a|b|c"
			ChannelName: "Acme-CC", ProviderName: "Acme", ServiceCLICommand: "a",
			Model: "b|c", ModelKey: "b|c",
			HardFailActive: true,
			ScoreTrend:     ScoreTrend{RecentAttempts: []*float64{nil, nil, nil}},
		},
		{ // fresh but a DIFFERENT pair: service "a|b", model "c" -> naive key "a|b|c"
			ChannelName: "Healthy-CC", ProviderName: "Healthy", ServiceCLICommand: "a|b",
			Model: "c", ModelKey: "c",
			ScoreTrend: ScoreTrend{Latest: qMk(97), LatestAt: freshAt()},
		},
	}

	snap := buildQualitySignalsAt(rows, fixedClock())

	key := ScoreKey("Acme", "a", "Acme-CC")
	got, ok := snap[key]
	if !ok {
		t.Fatalf("target channel %q missing from snapshot %v", key, snap)
	}
	if got.State != QualityUnknown {
		t.Fatalf("State = %v, want QualityUnknown (delimiter collision must not cross-activate)", got.State)
	}
}

// TestBuildQualitySignals_UnusableTriggerName_HardFailNoEmptyEntry: a hard-fail
// trigger whose model name sanitizes to empty (ModelKey="\x00") must still flip
// the channel to QualityHardFail, and TriggerModels must never contain an empty
// string.
func TestBuildQualitySignals_UnusableTriggerName_HardFailNoEmptyEntry(t *testing.T) {
	rows := []rankingRow{
		{ // target: hard-fail, model name is all control chars
			ChannelName: "Acme-CC", ProviderName: "Acme", ServiceCLICommand: "claude",
			Model: "\x00", ModelKey: "\x00",
			HardFailActive: true,
			ScoreTrend:     ScoreTrend{RecentAttempts: []*float64{nil, nil, nil}},
		},
		{ // fresh sibling with the same (raw) model key keeps it active
			ChannelName: "Healthy-CC", ProviderName: "Healthy", ServiceCLICommand: "claude",
			Model: "\x00", ModelKey: "\x00",
			ScoreTrend: ScoreTrend{Latest: qMk(97), LatestAt: freshAt()},
		},
	}

	snap := buildQualitySignalsAt(rows, fixedClock())

	key := ScoreKey("Acme", "cc", "Acme-CC")
	got, ok := snap[key]
	if !ok {
		t.Fatalf("target channel %q missing from snapshot %v", key, snap)
	}
	if got.State != QualityHardFail {
		t.Fatalf("State = %v, want QualityHardFail (unusable name must not suppress verdict)", got.State)
	}
	for _, m := range got.TriggerModels {
		if m == "" {
			t.Fatalf("TriggerModels = %v, must not contain an empty string", got.TriggerModels)
		}
	}
}

// TestBuildQualitySignals_ControlPaddedNameDedupes: a trigger name padded with
// control chars ("\x00 opus \x00") must sanitize to "opus" and dedupe against a
// plain "opus" trigger on the same channel into a single entry.
func TestBuildQualitySignals_ControlPaddedNameDedupes(t *testing.T) {
	rows := []rankingRow{
		{ // target row A: plain "opus"
			ChannelName: "Acme-CC", ProviderName: "Acme", ServiceCLICommand: "claude",
			Model: "opus", ModelKey: "opus",
			HardFailActive: true,
			ScoreTrend:     ScoreTrend{RecentAttempts: []*float64{nil, nil, nil}},
		},
		{ // target row B: control-padded "opus" (distinct raw model key)
			ChannelName: "Acme-CC", ProviderName: "Acme", ServiceCLICommand: "claude",
			Model: "\x00 opus \x00", ModelKey: "\x00 opus \x00",
			HardFailActive: true,
			ScoreTrend:     ScoreTrend{RecentAttempts: []*float64{nil, nil, nil}},
		},
		{ // fresh sibling for plain opus
			ChannelName: "Healthy-CC", ProviderName: "Healthy", ServiceCLICommand: "claude",
			Model: "opus", ModelKey: "opus",
			ScoreTrend: ScoreTrend{Latest: qMk(97), LatestAt: freshAt()},
		},
		{ // fresh sibling for control-padded opus (same raw key keeps it active)
			ChannelName: "Healthy-CC", ProviderName: "Healthy", ServiceCLICommand: "claude",
			Model: "\x00 opus \x00", ModelKey: "\x00 opus \x00",
			ScoreTrend: ScoreTrend{Latest: qMk(96), LatestAt: freshAt()},
		},
	}

	snap := buildQualitySignalsAt(rows, fixedClock())

	key := ScoreKey("Acme", "cc", "Acme-CC")
	got, ok := snap[key]
	if !ok {
		t.Fatalf("target channel %q missing from snapshot %v", key, snap)
	}
	if got.State != QualityHardFail {
		t.Fatalf("State = %v, want QualityHardFail", got.State)
	}
	if want := []string{"opus"}; !reflect.DeepEqual(got.TriggerModels, want) {
		t.Fatalf("TriggerModels = %v, want %v (control-padded name must dedupe)", got.TriggerModels, want)
	}
}

// TestBuildQualitySignals_HealthyModel_Recovered: a single fresh healthy row is
// genuine recovery evidence (a fresh row can only exist with a recent successful
// eval), so QualityRecovered — distinct from QualityUnknown.
func TestBuildQualitySignals_HealthyModel_Recovered(t *testing.T) {
	rows := []rankingRow{
		{
			ChannelName: "Acme-CC", ProviderName: "Acme", ServiceCLICommand: "claude",
			Model: "claude-opus", ModelKey: "claude-opus",
			ScoreTrend: ScoreTrend{Latest: qMk(98), LatestAt: freshAt()},
		},
	}

	snap := buildQualitySignalsAt(rows, fixedClock())

	key := ScoreKey("Acme", "cc", "Acme-CC")
	got, ok := snap[key]
	if !ok {
		t.Fatalf("target channel %q missing from snapshot %v", key, snap)
	}
	if got.State != QualityRecovered {
		t.Fatalf("State = %v, want QualityRecovered", got.State)
	}
	if len(got.TriggerModels) != 0 {
		t.Fatalf("TriggerModels = %v, want empty", got.TriggerModels)
	}
}

func TestQualitySnapshot_Lookup_CidPriorityThenTriple(t *testing.T) {
	snap := QualitySnapshot{
		Fresh: true,
		ByBucket: map[string]ChannelQualitySignal{
			cidBucketKey("O-123"):                    {State: QualityHardFail, TriggerModels: []string{"m"}},
			ScoreKey("acme", "anthropic", "acme-cc"): {State: QualityRecovered},
		},
	}
	// cid hit takes priority (trim only, case-sensitive)
	if got := snap.Lookup([]string{"whatever"}, "anthropic", "ignored", " O-123 "); got.State != QualityHardFail {
		t.Fatalf("cid lookup state = %v, want HardFail", got.State)
	}
	// empty cid -> triple (canonical trim+lower); provider candidates tried in order
	if got := snap.Lookup([]string{"Acme"}, "Anthropic", "ACME-CC", ""); got.State != QualityRecovered {
		t.Fatalf("triple lookup state = %v, want Recovered", got.State)
	}
	// second candidate wins when first is blank/miss
	if got := snap.Lookup([]string{"", "Acme"}, "anthropic", "acme-cc", ""); got.State != QualityRecovered {
		t.Fatalf("candidate-order lookup state = %v, want Recovered", got.State)
	}
	// cid provided but absent -> falls through to the triple match
	if got := snap.Lookup([]string{"Acme"}, "anthropic", "acme-cc", "O-DOES-NOT-EXIST"); got.State != QualityRecovered {
		t.Fatalf("cid-miss fallback state = %v, want Recovered", got.State)
	}
	// total miss -> Unknown
	if got := snap.Lookup([]string{"nobody"}, "anthropic", "nope", ""); got.State != QualityUnknown {
		t.Fatalf("miss state = %v, want Unknown", got.State)
	}
	// nil ByBucket -> Unknown (no panic)
	var empty QualitySnapshot
	if got := empty.Lookup([]string{"a"}, "b", "c", ""); got.State != QualityUnknown {
		t.Fatalf("nil-map lookup state = %v, want Unknown", got.State)
	}
}
