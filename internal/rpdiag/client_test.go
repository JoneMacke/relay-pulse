package rpdiag

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
			ScoreTrend:        ScoreTrend{Latest: mk(100), Avg7D: mk(100), Avg30D: mk(100), N7D: 3, N30D: 9},
		},
		{ // baseline same channel, different model — should merge
			ChannelName: "Anthropic", RelaypulseChannelKey: "anthropic",
			ProviderName: "Anthropic", ServiceCLICommand: "claude",
			Model: "claude-sonnet-4-6", ModelKey: "claude-sonnet-4-6",
			FinalQualityScore: mk(98),
			ScoreTrend:        ScoreTrend{Latest: mk(98), Avg7D: mk(98), Avg30D: mk(98), N7D: 3, N30D: 9},
		},
		{ // user-submitted — dropped
			ChannelName: "U-DawAPI-86a39a", RelaypulseChannelKey: "dawapi-86a39a",
			ProviderName: "DawAPI", ServiceCLICommand: "claude",
			SubmissionSource: "user",
			Model:            "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			FinalQualityScore: mk(95),
		},
		{ // missing score — dropped
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
			FinalQualityScore: mk(96),
		},
	}
	out := c.buildScores(rows)
	if _, ok := out["saiai|cc|max"]; !ok {
		t.Errorf("expected fallback join key saiai|cc|max, got %v", keysOf(out))
	}
}

func TestScoresUpstreamRoundTrip(t *testing.T) {
	mk := func(v float64) *float64 { return &v }
	payload := exportPayload{
		SchemaVersion: "ranking-export.v5.1",
		Items: []rankingRow{{
			ChannelName: "cc", RelaypulseChannelKey: "cc",
			ProviderName: "InfAI", ServiceCLICommand: "claude",
			Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			DetailURL:         "https://diag.relaypulse.top/channel/cc?window=30d&provider=InfAI&service=claude&model=claude-haiku-4-5",
			FinalQualityScore: mk(95.2),
			ScoreTrend:        ScoreTrend{Latest: mk(98), Avg7D: mk(98), Avg30D: mk(98)},
		}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := NewClient(nil, srv.URL, 0, true)
	scores, err := client.Scores(context.Background())
	if err != nil {
		t.Fatalf("Scores returned error: %v", err)
	}
	entry, ok := scores["infai|cc|cc"]
	if !ok {
		t.Fatalf("missing infai|cc|cc entry, got %v", keysOf(scores))
	}
	if entry.MaxScore == nil || *entry.MaxScore != 95.2 {
		t.Errorf("MaxScore = %v, want 95.2", entry.MaxScore)
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
		FinalQualityScore: mk(90),
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

// helpers ---------------------------------------------------------------

func newTestClient() *Client {
	return NewClient(nil, DefaultExportURL, DefaultTTL, true)
}

func keysOf(m map[string]Score) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
