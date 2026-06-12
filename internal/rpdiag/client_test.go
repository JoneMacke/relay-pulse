package rpdiag

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// testNow is a fixed reference clock so the staleness gate (scoreStaleWindow)
// is deterministic regardless of wall-clock time. Test clients pin nowFn to it
// via newTestClient/fixedClock; fixtures stamp latest_at relative to it.
var testNow = time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

func fixedClock() time.Time { return testNow }

// freshAt returns a latest_at within the current-signal window (1h old).
func freshAt() *string {
	s := testNow.Add(-time.Hour).Format(time.RFC3339Nano)
	return &s
}

// staleAt returns a latest_at older than scoreStaleWindow.
func staleAt() *string {
	s := testNow.Add(-(scoreStaleWindow + time.Hour)).Format(time.RFC3339Nano)
	return &s
}

func TestNormalizeChannelKey(t *testing.T) {
	cases := map[string]string{
		"O-Max":           "max",
		"R-MyChannel":     "mychannel",
		"M-Mixed":         "mixed",
		"U-DawAPI-86a39a": "dawapi-86a39a",
		"cc":              "cc",
		"":                "",
		"  O-Padded  ":    "padded",
		"o-lower":         "lower",
		"X-NotAPrefix":    "x-notaprefix",
	}
	for in, want := range cases {
		if got := NormalizeChannelKey(in); got != want {
			t.Errorf("NormalizeChannelKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeService(t *testing.T) {
	cases := map[string]string{
		"claude":  "cc",
		"codex":   "cx",
		"gemini":  "gm",
		"CLAUDE":  "cc",
		"unknown": "unknown",
		"":        "",
	}
	for in, want := range cases {
		if got := normalizeService(in); got != want {
			t.Errorf("normalizeService(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScoreKey(t *testing.T) {
	got := ScoreKey("SAIAi", "claude", "O-Max")
	want := "saiai|claude|o-max"
	if got != want {
		t.Errorf("ScoreKey = %q, want %q", got, want)
	}
}

func TestEnabledFromEnv(t *testing.T) {
	on := []string{"1", "true", "TRUE", "yes", "on", " On "}
	off := []string{"", "0", "false", "no", "off", "anything-else"}
	for _, raw := range on {
		if !enabledFromEnv(raw) {
			t.Errorf("enabledFromEnv(%q) = false, want true", raw)
		}
	}
	for _, raw := range off {
		if enabledFromEnv(raw) {
			t.Errorf("enabledFromEnv(%q) = true, want false", raw)
		}
	}
}

func TestBuildScoresAggregatesByTriple(t *testing.T) {
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{
		{ // baseline (high)
			ChannelName: "Anthropic", RelaypulseChannelKey: "anthropic",
			ProviderName: "Anthropic", ServiceCLICommand: "claude",
			Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			DetailURL:         "https://diag.relaypulse.top/channel/Anthropic?window=30d&provider=Anthropic&service=claude&model=claude-haiku-4-5",
			FinalQualityScore: mk(100),
			ScoreTrend:        ScoreTrend{Latest: mk(100), LatestAt: freshAt(), Avg7D: mk(100), Avg30D: mk(100), N7D: 3, N30D: 9},
		},
		{ // baseline same channel, different model — should merge
			ChannelName: "Anthropic", RelaypulseChannelKey: "anthropic",
			ProviderName: "Anthropic", ServiceCLICommand: "claude",
			Model: "claude-sonnet-4-6", ModelKey: "claude-sonnet-4-6",
			FinalQualityScore: mk(98),
			ScoreTrend:        ScoreTrend{Latest: mk(98), LatestAt: freshAt(), Avg7D: mk(98), Avg30D: mk(98), N7D: 3, N30D: 9},
		},
		{ // user-submitted — dropped
			ChannelName: "U-DawAPI-86a39a", RelaypulseChannelKey: "dawapi-86a39a",
			ProviderName: "DawAPI", ServiceCLICommand: "claude",
			SubmissionSource: "user",
			Model:            "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			ScoreTrend: ScoreTrend{Latest: mk(95)},
		},
		{ // missing trend representative — dropped (no recent_scores, no Latest)
			ChannelName: "Foo", RelaypulseChannelKey: "foo",
			ProviderName: "Bar", ServiceCLICommand: "claude",
		},
	}

	out := c.buildScores(rows)
	if len(out) != 1 {
		t.Fatalf("expected 1 aggregated entry (user + missing-score filtered), got %d (%v)", len(out), keysOf(out))
	}

	key := "anthropic|cc|anthropic"
	entry, ok := out[key]
	if !ok {
		t.Fatalf("expected key %q, got %v", key, keysOf(out))
	}
	if entry.MaxScore == nil || *entry.MaxScore != 100 {
		t.Errorf("MaxScore = %v, want 100", entry.MaxScore)
	}
	if len(entry.Models) != 2 {
		t.Errorf("Models len = %d, want 2", len(entry.Models))
	}
	// ChannelURL 必须从 max-score 那行的 detail_url 派生（去掉 ?model=），
	// 保留 rpdiag 给的原始 channel name 与 provider/service 限定。
	wantChannelURL := "https://diag.relaypulse.top/channel/Anthropic?provider=Anthropic&service=claude&window=30d"
	if entry.ChannelURL != wantChannelURL {
		t.Errorf("ChannelURL = %q, want %q", entry.ChannelURL, wantChannelURL)
	}
}

func TestBuildScoresFallsBackToChannelNameWhenJoinKeyMissing(t *testing.T) {
	// Older rpdiag deployments (< v5.1) don't ship `relaypulse_channel_key`.
	// Local strip must still produce the same join key.
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{
		{
			ChannelName:  "O-Max", // no RelaypulseChannelKey
			ProviderName: "SAIAi", ServiceCLICommand: "claude",
			Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			ScoreTrend: ScoreTrend{Latest: mk(96)},
		},
	}
	out := c.buildScores(rows)
	if _, ok := out["saiai|cc|max"]; !ok {
		t.Errorf("expected fallback join key saiai|cc|max, got %v", keysOf(out))
	}
}

func TestScoresUpstreamRoundTrip(t *testing.T) {
	mk := func(v float64) *float64 { return &v }
	// 关键：FinalQualityScore=95.2 但 trend.Latest=98，MaxScore 应跟 latest (98)
	// 而非 final（95.2）。验证从 composite quality 切到 fingerprint 表征分。
	payload := exportPayload{
		SchemaVersion: "ranking-export.v5.1",
		Items: []rankingRow{{
			ChannelName: "cc", RelaypulseChannelKey: "cc",
			ProviderName: "InfAI", ServiceCLICommand: "claude",
			Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			DetailURL:         "https://diag.relaypulse.top/channel/cc?window=30d&provider=InfAI&service=claude&model=claude-haiku-4-5",
			FinalQualityScore: mk(95.2),
			ScoreTrend:        ScoreTrend{Latest: mk(98), LatestAt: freshAt(), Avg7D: mk(98), Avg30D: mk(98)},
		}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := NewClient(nil, srv.URL, 0, true)
	client.nowFn = fixedClock
	scores, err := client.Scores(context.Background())
	if err != nil {
		t.Fatalf("Scores returned error: %v", err)
	}
	entry, ok := scores["infai|cc|cc"]
	if !ok {
		t.Fatalf("missing infai|cc|cc entry, got %v", keysOf(scores))
	}
	if entry.MaxScore == nil || *entry.MaxScore != 98 {
		t.Errorf("MaxScore = %v, want 98 (trend.latest, NOT final_quality_score 95.2)", entry.MaxScore)
	}
	if entry.Models[0].Score == nil || *entry.Models[0].Score != 98 {
		t.Errorf("Models[0].Score = %v, want 98 (per-model score must also be latest fingerprint sample)", entry.Models[0].Score)
	}
	wantChannelURL := "https://diag.relaypulse.top/channel/cc?provider=InfAI&service=claude&window=30d"
	if entry.ChannelURL != wantChannelURL {
		t.Errorf("ChannelURL = %q, want %q", entry.ChannelURL, wantChannelURL)
	}
}

func TestBuildScoresChannelURLEmptyWhenDetailURLMissing(t *testing.T) {
	// 若 rpdiag 没给 detail_url（理论上不该发生，但 schema 可选），ChannelURL
	// 必须留空，前端 nil-check 后不展示链接 — 避免回退到任何本地拼接的
	// "bare channel key" 死路。
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{{
		ChannelName: "O-Max", RelaypulseChannelKey: "max",
		ProviderName: "SAIAi", ServiceCLICommand: "claude",
		Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
		ScoreTrend: ScoreTrend{Latest: mk(90)},
		// DetailURL 缺省为空字符串
	}}

	entry, ok := c.buildScores(rows)["saiai|cc|max"]
	if !ok {
		t.Fatalf("expected entry saiai|cc|max, got %v", keysOf(c.buildScores(rows)))
	}
	if entry.ChannelURL != "" {
		t.Errorf("ChannelURL = %q, want empty", entry.ChannelURL)
	}
}

func TestLatestFingerprintSample(t *testing.T) {
	mk := func(v float64) *float64 { return &v }

	tests := []struct {
		name string
		in   ScoreTrend
		want *float64
	}{
		// recent_scores 优先：返回数组最末位（时间最新的 single sample）。
		{"recent_scores_wins_over_latest", ScoreTrend{RecentScores: []float64{82, 72, 76}, Latest: mk(99)}, mk(76)},
		{"recent_scores_single", ScoreTrend{RecentScores: []float64{88}}, mk(88)},
		// v5.1 wire 没 recent_scores 时 fallback latest。
		{"latest_fallback_when_recent_empty", ScoreTrend{Latest: mk(64)}, mk(64)},
		{"latest_fallback_when_recent_nil", ScoreTrend{RecentScores: nil, Latest: mk(70)}, mk(70)},
		// 两个都缺 → nil（调用方据此跳过 row）。
		{"both_missing_returns_nil", ScoreTrend{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := latestFingerprintSample(tt.in)
			switch {
			case got == nil && tt.want == nil:
				return
			case got == nil || tt.want == nil:
				t.Fatalf("got %v, want %v", got, tt.want)
			case *got != *tt.want:
				t.Fatalf("got %v, want %v", *got, *tt.want)
			}
		})
	}
}

func TestBuildScoresUsesRecentScoresTailWhenAvailable(t *testing.T) {
	// 验证 buildScores 端到端：v5.2 wire 的 recent_scores 末位（76）
	// 应被采纳为 ModelScore.Score 与通道 MaxScore，而不是 trend.latest（72）
	// 或 final_quality_score（85.9）。覆盖 FastCode opus 在 prod 实际看到的形态。
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{{
		ChannelName: "cc", RelaypulseChannelKey: "cc",
		ProviderName: "FastCode", ServiceCLICommand: "claude",
		Model: "claude-opus-4-7", ModelKey: "claude-opus-4-7",
		FinalQualityScore: mk(85.9),
		ScoreTrend: ScoreTrend{
			RecentScores: []float64{82, 72, 76},
			Latest:       mk(72),
			LatestAt:     freshAt(),
			Avg7D:        mk(76.7),
			Avg30D:       mk(76.7),
		},
	}}

	entry, ok := c.buildScores(rows)["fastcode|cc|cc"]
	if !ok {
		t.Fatalf("expected fastcode|cc|cc, got %v", keysOf(c.buildScores(rows)))
	}
	if entry.MaxScore == nil || *entry.MaxScore != 76 {
		t.Errorf("MaxScore = %v, want 76 (recent_scores[-1])", entry.MaxScore)
	}
	if entry.Models[0].Score == nil || *entry.Models[0].Score != 76 {
		t.Errorf("Models[0].Score = %v, want 76", entry.Models[0].Score)
	}
}

func TestBuildScoresKeepsHardFailRowAsZero(t *testing.T) {
	// rpdiag 标记 hard_fail_active 的行：即便没有任何 fingerprint sample，
	// 也不再被跳过，而是以代表分 0 入列（红点贴底），并把故障文案带给 tooltip。
	c := newTestClient()

	rows := []rankingRow{{
		ChannelName: "O-Max", RelaypulseChannelKey: "max",
		ProviderName: "SaiAI", ServiceCLICommand: "claude",
		Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
		HardFailActive:      true,
		AvailabilityWarning: "最近连续评测失败，当前不可用",
	}}

	entry, ok := c.buildScores(rows)["saiai|cc|max"]
	if !ok {
		t.Fatalf("expected hard-fail row to be kept, got %v", keysOf(c.buildScores(rows)))
	}
	if entry.MaxScore == nil || *entry.MaxScore != 0 {
		t.Fatalf("MaxScore = %v, want 0", entry.MaxScore)
	}
	if len(entry.Models) != 1 {
		t.Fatalf("Models len = %d, want 1", len(entry.Models))
	}
	m := entry.Models[0]
	if !m.Failed {
		t.Errorf("Model.Failed = false, want true")
	}
	if m.Score == nil || *m.Score != 0 {
		t.Errorf("Model.Score = %v, want 0", m.Score)
	}
	if m.Trend.Latest == nil || *m.Trend.Latest != 0 {
		t.Errorf("Trend.Latest = %v, want 0", m.Trend.Latest)
	}
	if m.Trend.LatestAt != nil {
		t.Errorf("Trend.LatestAt = %v, want nil (synthetic 0 has no sample time)", *m.Trend.LatestAt)
	}
	if !reflect.DeepEqual(m.Trend.RecentScores, []float64{0}) {
		t.Errorf("RecentScores = %v, want [0]", m.Trend.RecentScores)
	}
	if m.AvailabilityWarning != "最近连续评测失败，当前不可用" {
		t.Errorf("AvailabilityWarning = %q, not propagated", m.AvailabilityWarning)
	}
}

func TestBuildScoresHardFailAppendsZeroWithoutMutatingInput(t *testing.T) {
	// 有历史成功分时，hard-fail 行应保留窗口均值、在 recent 末尾补 0（取末 2 真值 + 0），
	// 让 sparkline 读作"从高跌到 0"；且绝不能原地改 decode 出来的共享 backing array。
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }
	latestAt := "2026-06-11T00:00:00Z"

	rows := []rankingRow{{
		ChannelName: "O-Max", RelaypulseChannelKey: "max",
		ProviderName: "SaiAI", ServiceCLICommand: "claude",
		Model: "claude-sonnet-4-6", ModelKey: "claude-sonnet-4-6",
		ScoreTrend: ScoreTrend{
			Latest:       mk(93),
			LatestAt:     &latestAt,
			Avg7D:        mk(90),
			Avg30D:       mk(89),
			RecentScores: []float64{88, 91, 93},
		},
		HardFailActive: true,
	}}

	m := c.buildScores(rows)["saiai|cc|max"].Models[0]
	if want := []float64{91, 93, 0}; !reflect.DeepEqual(m.Trend.RecentScores, want) {
		t.Fatalf("RecentScores = %v, want %v", m.Trend.RecentScores, want)
	}
	if m.Trend.Avg30D == nil || *m.Trend.Avg30D != 89 {
		t.Errorf("Avg30D = %v, want 89 (historical average kept)", m.Trend.Avg30D)
	}
	if !reflect.DeepEqual(rows[0].ScoreTrend.RecentScores, []float64{88, 91, 93}) {
		t.Fatalf("input RecentScores mutated: %v", rows[0].ScoreTrend.RecentScores)
	}
	// Writing through the normalized slice must not reach the decoded input.
	m.Trend.RecentScores[0] = 1
	if rows[0].ScoreTrend.RecentScores[1] != 91 {
		t.Fatalf("normalized trend reused input backing array")
	}
}

func TestBuildScoresPartialHardFailKeepsMaxFromHealthyModel(t *testing.T) {
	// 同通道一个 model 故障(0)、一个健康(92)：MaxScore 仍取健康 model 的分，
	// 不让"任一 model 失败"拖垮整通道排序。
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{
		{
			ChannelName: "O-Max", RelaypulseChannelKey: "max",
			ProviderName: "SaiAI", ServiceCLICommand: "claude",
			Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			HardFailActive: true,
		},
		{
			ChannelName: "O-Max", RelaypulseChannelKey: "max",
			ProviderName: "SaiAI", ServiceCLICommand: "claude",
			Model: "claude-sonnet-4-6", ModelKey: "claude-sonnet-4-6",
			ScoreTrend: ScoreTrend{Latest: mk(92), LatestAt: freshAt(), RecentScores: []float64{92}},
		},
	}

	entry := c.buildScores(rows)["saiai|cc|max"]
	if entry.MaxScore == nil || *entry.MaxScore != 92 {
		t.Fatalf("MaxScore = %v, want 92 (healthy model)", entry.MaxScore)
	}
	var failed int
	for _, m := range entry.Models {
		if m.Failed {
			failed++
			if m.Score == nil || *m.Score != 0 {
				t.Errorf("failed model score = %v, want 0", m.Score)
			}
		}
	}
	if failed != 1 {
		t.Fatalf("failed models = %d, want 1", failed)
	}
}

func TestIsStaleScoreTrend(t *testing.T) {
	at := func(s string) ScoreTrend { return ScoreTrend{LatestAt: &s} }
	fresh := testNow.Add(-time.Hour).Format(time.RFC3339Nano) // microsecond precision
	old := testNow.Add(-(scoreStaleWindow + time.Hour)).Format(time.RFC3339Nano)
	bareFresh := testNow.Add(-time.Hour).Format(time.RFC3339) // no fractional seconds
	cases := []struct {
		name string
		in   ScoreTrend
		want bool
	}{
		{"fresh_fractional", at(fresh), false},
		{"fresh_bare_rfc3339", at(bareFresh), false}, // RFC3339Nano parses non-fractional too
		{"stale", at(old), true},
		{"missing_fail_closed", ScoreTrend{}, true},
		{"empty_string_fail_closed", at("  "), true},
		{"unparseable_fail_closed", at("not-a-timestamp"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStaleScoreTrend(tc.in, testNow); got != tc.want {
				t.Errorf("isStaleScoreTrend(%+v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildScoresStaleRowRanksZeroButKeepsHistory(t *testing.T) {
	// A non-hard-fail row whose latest sample predates the 7d window (e.g. a
	// retired model whose per-channel score froze) contributes 0 to the channel
	// MaxScore ranking key — so it can't float to the top on a stale number.
	// But its per-model Score and Trend are displayed EXACTLY as exported: an
	// honest historical line, not flagged Failed, no synthetic point injected.
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }
	staleStamp := staleAt()

	rows := []rankingRow{{
		ChannelName: "O-Max", RelaypulseChannelKey: "max",
		ProviderName: "TopRouterCN", ServiceCLICommand: "claude",
		Model: "claude-opus-4-7", ModelKey: "claude-opus-4-7",
		ScoreTrend: ScoreTrend{
			Latest:         mk(88),
			LatestAt:       staleStamp,
			Avg7D:          mk(90),
			Avg30D:         mk(91),
			RecentScores:   []float64{85, 88},
			RecentAttempts: []*float64{}, // v5.5: no in-7d attempt
		},
	}}

	entry, ok := c.buildScores(rows)["toproutercn|cc|max"]
	if !ok {
		t.Fatalf("expected stale row kept, got %v", keysOf(c.buildScores(rows)))
	}
	// Ranking: zeroed.
	if entry.MaxScore == nil || *entry.MaxScore != 0 {
		t.Fatalf("MaxScore = %v, want 0 (stale → no current ranking signal)", entry.MaxScore)
	}
	// Display: untouched history.
	m := entry.Models[0]
	if m.Failed {
		t.Errorf("Model.Failed = true, want false (stale is not hard-fail)")
	}
	if m.Score == nil || *m.Score != 88 {
		t.Errorf("Model.Score = %v, want 88 (real history shown, not ranking 0)", m.Score)
	}
	if m.Trend.LatestAt == nil || *m.Trend.LatestAt != *staleStamp {
		t.Errorf("Trend.LatestAt = %v, want preserved %q", m.Trend.LatestAt, *staleStamp)
	}
	if m.Trend.Avg30D == nil || *m.Trend.Avg30D != 91 {
		t.Errorf("Avg30D = %v, want 91 (history kept)", m.Trend.Avg30D)
	}
	if len(m.Trend.RecentAttempts) != 0 {
		t.Errorf("RecentAttempts = %v, want [] untouched (no synthetic point)", m.Trend.RecentAttempts)
	}
}

func TestBuildScoresFreshModelDominatesStaleSibling(t *testing.T) {
	// Channel with one fresh model (90) and one stale sibling (frozen 95): the
	// fresh score must win MaxScore — max() means a stale model only drags the
	// channel down when *every* model is stale.
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{
		{ // stale, frozen high
			ChannelName: "O-Max", RelaypulseChannelKey: "max",
			ProviderName: "TopRouterCN", ServiceCLICommand: "claude",
			Model: "claude-opus-4-7", ModelKey: "claude-opus-4-7",
			ScoreTrend: ScoreTrend{Latest: mk(95), LatestAt: staleAt()},
		},
		{ // fresh current model
			ChannelName: "O-Max", RelaypulseChannelKey: "max",
			ProviderName: "TopRouterCN", ServiceCLICommand: "claude",
			Model: "claude-opus-4-8", ModelKey: "claude-opus-4-8",
			ScoreTrend: ScoreTrend{Latest: mk(90), LatestAt: freshAt()},
		},
	}

	entry := c.buildScores(rows)["toproutercn|cc|max"]
	if entry.MaxScore == nil || *entry.MaxScore != 90 {
		t.Fatalf("MaxScore = %v, want 90 (fresh model dominates stale sibling)", entry.MaxScore)
	}
}

func TestBuildScoresSkipsHardFailUserSubmission(t *testing.T) {
	// 公开提交(submission_source=user)通道即便 hard-fail 也不进 relaypulse 列表。
	c := newTestClient()
	rows := []rankingRow{{
		ChannelName: "U-foo-abc123", RelaypulseChannelKey: "foo-abc123",
		ProviderName: "Foo", ServiceCLICommand: "claude",
		Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
		SubmissionSource: "user",
		HardFailActive:   true,
	}}
	if got := c.buildScores(rows); len(got) != 0 {
		t.Fatalf("expected user hard-fail row skipped, got %v", keysOf(got))
	}
}

func TestCloneScoresDeepCopiesRecentScores(t *testing.T) {
	// cloneScores 返回独立快照：改克隆里的 RecentScores 不应回写到源 cache。
	mk := func(v float64) *float64 { return &v }
	src := map[string]Score{
		"k": {
			MaxScore: mk(6),
			Trend:    ScoreTrend{RecentScores: []float64{1, 2, 3}},
			Models:   []ModelScore{{Trend: ScoreTrend{RecentScores: []float64{4, 5, 6}}}},
		},
	}

	cloned := cloneScores(src)["k"]
	cloned.Trend.RecentScores[0] = 99
	cloned.Models[0].Trend.RecentScores[0] = 88

	if src["k"].Trend.RecentScores[0] != 1 {
		t.Fatalf("aggregate trend recent_scores shared with clone")
	}
	if src["k"].Models[0].Trend.RecentScores[0] != 4 {
		t.Fatalf("model trend recent_scores shared with clone")
	}
}

func TestScoresRejectsUnsupportedSchema(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, `{"schema_version":"ranking-export.v4","items":[]}`)
	}))
	defer srv.Close()

	client := NewClient(nil, srv.URL, 0, true)
	if _, err := client.Scores(context.Background()); err == nil {
		t.Errorf("expected error for unsupported schema_version, got nil")
	}
}

func TestScoresAcceptsV53UnavailableRow(t *testing.T) {
	// v5.3 export feed: an export-only "unavailable" row for a model that never
	// scored and is still hard-failing (e.g. a 403-only model). The schema gate
	// accepts v5.x by prefix and the unknown `quality_state` field is ignored;
	// the row rides the existing hard_fail_active path and renders as a kept
	// gray-zero model carrying rpdiag's "couldn't measure" warning.
	const payload = `{"schema_version":"ranking-export.v5.3","items":[` +
		`{"channel_name":"M-Max","relaypulse_channel_key":"max","provider_name":"AIMZ",` +
		`"service_cli_command":"claude","model":"claude-opus-4-8","model_key":"claude-opus-4-8",` +
		`"quality_state":"unavailable","hard_fail_active":true,` +
		`"availability_warning":"质量探测未取得可评分响应","final_quality_score":0,"score_trend":{}}` +
		`]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, payload)
	}))
	defer srv.Close()

	client := NewClient(nil, srv.URL, 0, true)
	scores, err := client.Scores(context.Background())
	if err != nil {
		t.Fatalf("v5.3 payload rejected: %v", err)
	}
	entry, ok := scores["aimz|cc|max"]
	if !ok {
		t.Fatalf("expected aimz|cc|max, got %v", keysOf(scores))
	}
	if len(entry.Models) != 1 {
		t.Fatalf("Models len = %d, want 1", len(entry.Models))
	}
	m := entry.Models[0]
	if !m.Failed {
		t.Errorf("Model.Failed = false, want true (unavailable row is hard-fail active)")
	}
	if m.Score == nil || *m.Score != 0 {
		t.Errorf("Model.Score = %v, want 0", m.Score)
	}
	if m.AvailabilityWarning != "质量探测未取得可评分响应" {
		t.Errorf("AvailabilityWarning = %q, want the unavailable-export wording", m.AvailabilityWarning)
	}
}

// TestRecentAttemptsEmptyVsAbsentRoundTrip locks the v5.5 contract: an upstream
// empty `recent_attempts:[]` ("no in-7d attempt") must survive decode → clone →
// re-encode as `[]`, while an absent/null field must survive as `null`. The
// front end keys off exactly this distinction — `[]` draws no recent dots,
// `null` falls back to recent_scores — so an `omitempty` regression that
// collapsed `[]` to absent would silently resurrect stale dots.
func TestRecentAttemptsEmptyVsAbsentRoundTrip(t *testing.T) {
	cases := []struct {
		name      string
		trendJSON string
		want      string
	}{
		// latest:83 keeps the row past buildScores' representative-score gate; the
		// recent_attempts field under test is independent of it.
		{"empty_stays_empty", `{"latest":83.0,"recent_attempts":[]}`, `"recent_attempts":[]`},
		{"absent_stays_null", `{"latest":83.0}`, `"recent_attempts":null`},
		{"values_preserved", `{"latest":83.0,"recent_attempts":[null,88.0]}`, `"recent_attempts":[null,88]`},
	}
	// Inject a fresh latest_at so the staleness gate leaves recent_attempts
	// untouched — this test isolates the empty-vs-absent round-trip, not staleness.
	freshStr := testNow.Add(-time.Hour).Format(time.RFC3339Nano)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trendJSON := strings.Replace(tc.trendJSON, "{", `{"latest_at":"`+freshStr+`",`, 1)
			payload := `{"schema_version":"ranking-export.v5.5","items":[` +
				`{"channel_name":"O-Max","relaypulse_channel_key":"max","provider_name":"TopRouterCN",` +
				`"service_cli_command":"claude","model":"claude-opus-4-7","model_key":"claude-opus-4-7",` +
				`"detail_url":"https://diag.relaypulse.top/channel/O-Max",` +
				`"score_trend":` + trendJSON + `}]}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintln(w, payload)
			}))
			defer srv.Close()

			client := NewClient(nil, srv.URL, 0, true)
			client.nowFn = fixedClock
			scores, err := client.Scores(context.Background())
			if err != nil {
				t.Fatalf("Scores returned error: %v", err)
			}
			entry, ok := scores["toproutercn|cc|max"]
			if !ok {
				t.Fatalf("missing toproutercn|cc|max entry, got %v", keysOf(scores))
			}
			// Marshal the cloned model trend the way the HTTP handler serves it
			// to the browser, then assert the recent_attempts fragment.
			out, err := json.Marshal(entry.Models[0].Trend)
			if err != nil {
				t.Fatalf("marshal trend: %v", err)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("trend JSON = %s, want substring %q", out, tc.want)
			}
		})
	}
}

// helpers ---------------------------------------------------------------

func newTestClient() *Client {
	c := NewClient(nil, DefaultExportURL, DefaultTTL, true)
	c.nowFn = fixedClock
	return c
}

func keysOf(m map[string]Score) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
