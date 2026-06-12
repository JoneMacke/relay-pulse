// Package rpdiag fetches and caches the public quality-score export from
// rpdiag (diag.relaypulse.top), indexing it by the (provider, service,
// channel) triple that relaypulse listings expose.
//
// Cache TTL is intentionally generous (10 min) — the upstream score moves
// on a sampler-cadence (~hourly) so refreshing per request would only
// burn rate quota on rpdiag. singleflight collapses concurrent refreshes;
// a refresh failure falls back to the last good snapshot so a transient
// upstream blip doesn't strip the column from the listing.
//
// The package is opt-in: NewClientFromEnv returns nil when
// MONITOR_RPDIAG_ENABLED is unset or false, so callers can skip wiring
// without conditionals.
package rpdiag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	defaultExportURL = "https://diag.relaypulse.top/api/v1/ranking/export?scoring_version=all"
	defaultTTL       = 10 * time.Minute
	requestTimeout   = 10 * time.Second
	maxResponseBytes = 10 << 20 // 10 MiB; export payload is < 1 MiB today
	// scoreStaleWindow bounds how old a channel's latest fingerprint sample may
	// be and still count as a *current* quality signal. A row whose newest
	// sample predates this window (and is not already hard-fail active) carries
	// no current signal — its representative score is forced to 0 so a channel
	// that was measured well once and then went dark (e.g. a model retired from
	// the sampler pool) cannot float to the top of the quality sort on a frozen
	// score. Kept equal to rpdiag's own 7-day hard-fail stale window so the two
	// "no current signal" notions stay aligned; bump both together.
	scoreStaleWindow = 7 * 24 * time.Hour
)

// ScoreTrend mirrors rpdiag's per-row sparkline payload. Consumers can render
// either the original 3-point form (avg_30d → avg_7d → latest) or the
// ranking-export.v5.2+ 5-point form (avg_30d → avg_7d → up to 3 most-recent
// single samples in time-ascending order). All fields are optional; nil
// counts mean "no in-scope samples".
type ScoreTrend struct {
	Latest   *float64 `json:"latest,omitempty"`
	LatestAt *string  `json:"latest_at,omitempty"`
	Avg7D    *float64 `json:"avg_7d,omitempty"`
	Avg30D   *float64 `json:"avg_30d,omitempty"`
	// RecentScores holds up to 3 most-recent single fingerprint samples in
	// time-ascending order (oldest → newest). nil on pre-v5.2 wire or when
	// no in-scope samples exist; len may be 1 or 2 during cold start.
	RecentScores []float64 `json:"recent_scores,omitempty"`
	// RecentAttempts holds up to 3 most-recent quality-relevant terminal
	// attempts in time-ascending order (ranking-export.v5.4+; bounded to the
	// last 7 days in v5.5). A non-nil element is a scored fingerprint sample; a
	// nil element is a hard-fail attempt (rendered as a neutral grey marker).
	// A non-nil empty slice means v5.5 found no in-window attempt — the front
	// end then draws no recent dots (NOT a fallback). A nil slice means the
	// upstream field was absent/null (pre-v5.5 wire); only then does the front
	// end fall back to RecentScores. Hence NO `omitempty`: the empty-vs-absent
	// distinction must survive re-serialization to the browser. Unlike
	// RecentScores this is not used for the representative score.
	RecentAttempts []*float64 `json:"recent_attempts"`
	N7D            int        `json:"n_7d"`
	N30D           int        `json:"n_30d"`
}

// ModelScore captures one (channel, model) row from rpdiag.
//
// Failed marks a row rpdiag currently considers hard-fail active (its most
// recent evaluations died before scoring); for such rows Score and Trend are
// normalized to 0/grey so the column shows "scored, currently out" instead of a
// stale value or nothing. A stale (but not hard-fail) row keeps its real
// historical Score and Trend for display — only its contribution to the channel
// MaxScore ranking key is zeroed (see buildScoresAt). AvailabilityWarning
// carries rpdiag's user-facing reason string, surfaced in the cell tooltip.
type ModelScore struct {
	Model               string     `json:"model,omitempty"`
	ModelKey            string     `json:"model_key,omitempty"`
	Score               *float64   `json:"score,omitempty"`
	Trend               ScoreTrend `json:"trend"`
	DetailURL           string     `json:"detail_url,omitempty"`
	Failed              bool       `json:"failed,omitempty"`
	AvailabilityWarning string     `json:"availability_warning,omitempty"`
}

// Score is the aggregated quality view for one (provider, service, channel)
// triple. MaxScore picks the strongest *current-signal* model: fresh models use
// their latest fingerprint sample, hard-fail/stale models contribute 0. Listing
// users want to know "what is this channel currently capable of"; averaging
// across models would punish channels that also host weaker fallbacks.
type Score struct {
	MaxScore   *float64     `json:"max_score,omitempty"`
	Models     []ModelScore `json:"models"`
	Trend      ScoreTrend   `json:"trend"`
	ChannelURL string       `json:"channel_url"`
}

// Client is safe for concurrent use.
type Client struct {
	httpClient *http.Client
	exportURL  string
	ttl        time.Duration
	// nowFn is the clock; defaults to time.Now. Tests override it to drive the
	// staleness gate (scoreStaleWindow) and cache expiry deterministically.
	nowFn func() time.Time

	mu        sync.RWMutex
	cache     map[string]Score
	expiresAt time.Time

	sf singleflight.Group
}

// NewClient constructs a Client with explicit dependencies. Used by tests
// and rare callers that want to bypass env-based wiring; production code
// should call NewClientFromEnv. Passing enabled=false returns a Client that
// always serves an empty snapshot (useful for tests that need a non-nil
// reference).
func NewClient(httpClient *http.Client, exportURL string, ttl time.Duration, enabled bool) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}
	if ttl <= 0 {
		ttl = defaultTTL
	}
	if strings.TrimSpace(exportURL) == "" {
		exportURL = defaultExportURL
	}
	c := &Client{
		httpClient: httpClient,
		exportURL:  strings.TrimSpace(exportURL),
		ttl:        ttl,
		nowFn:      time.Now,
	}
	if !enabled {
		// Disabled clients still need to honour the Scores() contract; tag
		// them so external code can branch if desired.
		c.cache = map[string]Score{}
		c.expiresAt = c.now().Add(time.Hour) // freeze empty snapshot
	}
	return c
}

// Exported constants for tests.
const (
	DefaultExportURL = defaultExportURL
	DefaultTTL       = defaultTTL
)

// NewClientFromEnv returns a Client when MONITOR_RPDIAG_ENABLED is truthy,
// otherwise nil. Recognized env vars:
//
//	MONITOR_RPDIAG_ENABLED      "1"/"true"/"yes" → enable, default disabled
//	MONITOR_RPDIAG_EXPORT_URL   override the rpdiag export endpoint
//	MONITOR_RPDIAG_CACHE_TTL    Go duration string (e.g. "5m"), defaults 10m
func NewClientFromEnv() *Client {
	if !enabledFromEnv(os.Getenv("MONITOR_RPDIAG_ENABLED")) {
		return nil
	}

	exportURL := strings.TrimSpace(os.Getenv("MONITOR_RPDIAG_EXPORT_URL"))
	if exportURL == "" {
		exportURL = defaultExportURL
	}
	ttl := defaultTTL
	if raw := strings.TrimSpace(os.Getenv("MONITOR_RPDIAG_CACHE_TTL")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			ttl = parsed
		}
	}

	return &Client{
		httpClient: &http.Client{Timeout: requestTimeout},
		exportURL:  exportURL,
		ttl:        ttl,
		nowFn:      time.Now,
	}
}

// now returns the client clock, tolerating a zero-value Client (nowFn unset)
// constructed by older callers or struct literals.
func (c *Client) now() time.Time {
	if c.nowFn != nil {
		return c.nowFn()
	}
	return time.Now()
}

// enabledFromEnv defaults to *disabled*; only explicit truthy strings flip
// it on. This is deliberate — operators must opt-in to surface a third-
// party signal on the listing.
func enabledFromEnv(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// Scores returns the cached score index, refreshing from upstream when the
// cache has expired. A refresh failure with a previous snapshot still
// available returns the stale snapshot rather than an error.
func (c *Client) Scores(ctx context.Context) (map[string]Score, error) {
	if c == nil {
		return map[string]Score{}, nil
	}
	if snap, ok := c.freshSnapshot(c.now()); ok {
		return snap, nil
	}

	v, err, _ := c.sf.Do("scores", func() (interface{}, error) {
		if snap, ok := c.freshSnapshot(c.now()); ok {
			return snap, nil
		}
		fresh, refreshErr := c.refresh(ctx)
		if refreshErr != nil {
			if stale, ok := c.staleSnapshot(); ok {
				return stale, nil
			}
			return nil, refreshErr
		}
		return fresh, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(map[string]Score), nil
}

func (c *Client) freshSnapshot(now time.Time) (map[string]Score, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cache == nil || now.After(c.expiresAt) {
		return nil, false
	}
	return cloneScores(c.cache), true
}

func (c *Client) staleSnapshot() (map[string]Score, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cache == nil {
		return nil, false
	}
	return cloneScores(c.cache), true
}

// exportPayload mirrors the rpdiag ranking-export wire schema we consume
// (v5.1 baseline + v5.2 recent_scores). Only the fields the client needs
// are bound; unknown fields are dropped.
type exportPayload struct {
	SchemaVersion string       `json:"schema_version"`
	Items         []rankingRow `json:"items"`
}

type rankingRow struct {
	ChannelName          string     `json:"channel_name"`
	RelaypulseChannelKey string     `json:"relaypulse_channel_key"`
	ProviderName         string     `json:"provider_name"`
	ServiceCLICommand    string     `json:"service_cli_command"`
	SubmissionSource     string     `json:"submission_source"`
	Model                string     `json:"model"`
	ModelKey             string     `json:"model_key"`
	DetailURL            string     `json:"detail_url"`
	FinalQualityScore    *float64   `json:"final_quality_score"`
	ScoreTrend           ScoreTrend `json:"score_trend"`
	// HardFailActive is rpdiag's current-availability gate: the newest ≥3
	// consecutive terminal attempts were hard-fails (FAILED with no
	// fingerprint score) and the latest fail is within rpdiag's 7-day stale
	// window. rpdiag forces its own `final_quality_score` to 0 under the same
	// condition; we mirror that as a representative score of 0.
	HardFailActive      bool   `json:"hard_fail_active"`
	AvailabilityWarning string `json:"availability_warning"`
}

func (c *Client) refresh(ctx context.Context) (map[string]Score, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.exportURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "relaypulse/rpdiag-client")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rpdiag fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rpdiag export HTTP %d", resp.StatusCode)
	}

	var payload exportPayload
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("rpdiag decode: %w", err)
	}
	if payload.SchemaVersion != "" && !strings.HasPrefix(payload.SchemaVersion, "ranking-export.v5") {
		return nil, fmt.Errorf("rpdiag unsupported schema_version %q", payload.SchemaVersion)
	}

	now := c.now()
	scores := c.buildScoresAt(payload.Items, now)

	c.mu.Lock()
	c.cache = scores
	c.expiresAt = now.Add(c.ttl)
	c.mu.Unlock()

	return cloneScores(scores), nil
}

// latestFingerprintSample returns the most recent single fingerprint sample
// from a trend — the value the sparkline's rightmost dot already renders
// (front-end uses recent_scores[-1] when present, falling back to trend.latest;
// rpdiag fills both with the same value, so the tooltip's "latest=" row stays
// aligned in practice). Using it as the channel representative score keeps
// list ordering and per-row visualisation in lockstep.
//
// Prefers recent_scores[-1] when v5.2 wire carries it (one true sample,
// strictly time-ascending), falling back to trend.latest on v5.1.
func latestFingerprintSample(t ScoreTrend) *float64 {
	if n := len(t.RecentScores); n > 0 {
		v := t.RecentScores[n-1]
		return &v
	}
	return t.Latest
}

// normalizeHardFailTrend returns a display-only ScoreTrend for a row rpdiag has
// flagged as currently hard-fail active. The representative point is forced to
// 0 and tagged via ModelScore.Failed; the front end renders that endpoint in a
// neutral unavailable grey at the floor (grey = couldn't measure, distinct from
// the red qualityScoreColor uses for a genuinely poor measured response). The
// historical window averages are kept so the sparkline reads as "dropped from
// high to unavailable". The synthetic 0 has no real sample timestamp, so
// LatestAt is cleared.
//
// RecentScores is rebuilt into a fresh slice (last up-to-2 real samples, then
// the synthetic 0; just [0] when there is no history). It never aliases the
// decoded row's backing array, which stays cached and is handed to concurrent
// readers.
func normalizeHardFailTrend(t ScoreTrend) ScoreTrend {
	out := t
	zero := 0.0
	out.Latest = &zero
	out.LatestAt = nil

	recent := make([]float64, 0, 3)
	if n := len(t.RecentScores); n > 0 {
		start := 0
		if n > 2 {
			start = n - 2
		}
		recent = append(recent, t.RecentScores[start:]...)
	}
	recent = append(recent, 0)
	out.RecentScores = recent
	// RecentAttempts (v5.4) carries real per-attempt grey markers and is left
	// untouched — it is the preferred source for the sparkline's recent slots,
	// so it must not be overwritten by this v5.3-era synthetic RecentScores tail.
	return out
}

// isStaleScoreTrend reports whether a trend's latest fingerprint sample is
// older than scoreStaleWindow, i.e. carries no *current* quality signal.
//
// Fail-closed: a missing or unparseable latest_at counts as stale. v5.5 export
// always stamps latest_at on a scored row, so an absent value means the upstream
// regressed or the wire is untrustworthy — surfacing that loudly (the channel
// drops to a red 0) beats silently trusting a value we cannot date. The window
// is the same 7 days rpdiag uses for its hard-fail gate.
//
// latest_at is RFC3339 with sub-second precision (e.g. "...01.813413Z").
// RFC3339Nano is used to make that shape explicit; Go's time.Parse accepts a
// fractional second whether or not the layout names it, so both fractional and
// bare timestamps parse fine.
func isStaleScoreTrend(t ScoreTrend, now time.Time) bool {
	if t.LatestAt == nil || strings.TrimSpace(*t.LatestAt) == "" {
		return true
	}
	latestAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*t.LatestAt))
	if err != nil {
		return true
	}
	return latestAt.Before(now.Add(-scoreStaleWindow))
}

// buildScores collapses many rpdiag rows into one entry per (provider,
// service, channel) triple. Rows that lack a representative fingerprint
// sample, or that come from the public /submit pipeline
// (`submission_source=user` / `U-` channel prefix), are skipped — those
// entries don't exist in relaypulse listings and would never join.
//
// Display vs ranking are deliberately decoupled. The per-model Trend (the
// sparkline) and Score are shown exactly as rpdiag exported them — the line is
// an honest history coloured by real quality, the only grey being a hard-fail
// row that rpdiag couldn't score. The representative score is the latest single
// fingerprint sample (not rpdiag's composite `final_quality_score`, which folds
// in latency/availability — that belongs to rpdiag's own ranking page).
//
// The channel ranking key (MaxScore = strongest model) instead uses a current-
// signal score: a model that can't be measured right now contributes 0 so it
// can't keep a channel ranked high on a stale number. Two cases score 0:
//   - hard-fail active: rpdiag flags the channel as currently failing (also
//     normalized to a grey 0 trend for display, the only grey).
//   - stale: a non-hard-fail row whose latest sample predates scoreStaleWindow.
//     Its history is displayed untouched; only its ranking contribution is 0.
//
// max() across a channel's models means a 0 only sinks the channel when *every*
// model is hard-fail/stale (a genuinely dark channel) — a fresh model still
// wins. The hard-fail check MUST come first: normalizeHardFailTrend clears
// LatestAt, so testing staleness afterwards would misread its 0 trend as stale.
func (c *Client) buildScores(rows []rankingRow) map[string]Score {
	return c.buildScoresAt(rows, c.now())
}

func (c *Client) buildScoresAt(rows []rankingRow, now time.Time) map[string]Score {
	out := make(map[string]Score, len(rows))

	for _, row := range rows {
		// displayLatest drives the per-model Score/Trend (honest history);
		// rankLatest drives MaxScore (current-signal, 0 when unmeasurable now).
		displayLatest := latestFingerprintSample(row.ScoreTrend)
		rankLatest := displayLatest
		trend := row.ScoreTrend
		if row.HardFailActive {
			trend = normalizeHardFailTrend(row.ScoreTrend)
			displayLatest = trend.Latest // grey 0, shown + ranked
			rankLatest = trend.Latest
		} else if displayLatest != nil && isStaleScoreTrend(row.ScoreTrend, now) {
			// Keep the historical trend exactly as exported; just don't let a
			// frozen old sample rank the channel as currently good.
			zero := 0.0
			rankLatest = &zero
		}
		if displayLatest == nil {
			continue
		}
		if strings.EqualFold(row.SubmissionSource, "user") {
			continue
		}

		provider := canonical(row.ProviderName)
		service := normalizeService(row.ServiceCLICommand)
		channel := canonical(row.RelaypulseChannelKey)
		if channel == "" {
			// Older rpdiag deployments (<v5.1) don't ship the join key —
			// strip the prefix locally so we still work during rollout.
			channel = NormalizeChannelKey(row.ChannelName)
		}
		if provider == "" || service == "" || channel == "" {
			continue
		}

		key := ScoreKey(provider, service, channel)
		entry := out[key]
		entry.Models = append(entry.Models, ModelScore{
			Model:               row.Model,
			ModelKey:            row.ModelKey,
			Score:               copyFloat(displayLatest),
			Trend:               trend,
			DetailURL:           row.DetailURL,
			Failed:              row.HardFailActive,
			AvailabilityWarning: row.AvailabilityWarning,
		})

		if entry.MaxScore == nil || *rankLatest > *entry.MaxScore {
			entry.MaxScore = copyFloat(rankLatest)
			entry.Trend = trend
			// 通道整体跳转 = max-score 那行的 detail_url 去掉 model 参数 →
			// 落到 rpdiag 的"服务商+通道"概览页（channel name 与大小写、前缀都来自
			// rpdiag，本地不再猜测路由规则）。
			entry.ChannelURL = channelURLFromDetailURL(row.DetailURL)
		}
		out[key] = entry
	}

	for key, score := range out {
		sort.SliceStable(score.Models, func(i, j int) bool {
			return modelOrderScore(score.Models[i]) > modelOrderScore(score.Models[j])
		})
		out[key] = score
	}
	return out
}

// ScoreKey is the join key shape: lower-case "provider|service|channel".
// `service` should already be the relaypulse short code (cc/cx/gm), and
// `channel` should already be the bare key (no rpdiag prefix). Helpers
// below normalize callers' inputs.
func ScoreKey(provider, service, channel string) string {
	return canonical(provider) + "|" + canonical(service) + "|" + canonical(channel)
}

// NormalizeChannelKey strips a single-letter rpdiag source prefix (O-/R-/
// M-/U-, case-insensitive) and lower-cases the rest. Channels without a
// prefix pass through lower-cased.
func NormalizeChannelKey(name string) string {
	normalized := canonical(name)
	if len(normalized) > 2 && normalized[1] == '-' {
		switch normalized[0] {
		case 'o', 'r', 'm', 'u':
			return normalized[2:]
		}
	}
	return normalized
}

// normalizeService maps rpdiag's CLI command name onto relaypulse's
// service code. Unknown services pass through unchanged so future tools
// integrate without code edits.
func normalizeService(cliCommand string) string {
	switch canonical(cliCommand) {
	case "claude":
		return "cc"
	case "codex":
		return "cx"
	case "gemini":
		return "gm"
	default:
		return canonical(cliCommand)
	}
}

func canonical(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

// channelURLFromDetailURL 从 model 级别的 detail_url 派生出 channel 级别链接：
// 解析 URL，丢弃 ?model= 查询参数后重新序列化。
//
// rpdiag 已经在 detail_url 里给了正确的 channel name（带前缀、大小写敏感）和
// 必要的 provider/service 限定符，去掉 model 后就是"服务商+通道"概览。这样
// relaypulse 不需要硬编码 rpdiag 路由规则，路由变化只需要 rpdiag 调整 detail_url
// 即可。detail_url 为空或不可解析时返回空，前端 nil-check 后不展示链接。
func channelURLFromDetailURL(detailURL string) string {
	trimmed := strings.TrimSpace(detailURL)
	if trimmed == "" {
		return ""
	}
	u, err := url.Parse(trimmed)
	if err != nil || !u.IsAbs() {
		return ""
	}
	q := u.Query()
	q.Del("model")
	u.RawQuery = q.Encode()
	return u.String()
}

func modelOrderScore(m ModelScore) float64 {
	if m.Score == nil {
		return -1
	}
	return *m.Score
}

func copyFloat(v *float64) *float64 {
	if v == nil {
		return nil
	}
	x := *v
	return &x
}

// cloneScoreTrend returns a copy whose slice fields are independent of the
// source. The other fields are value types or never-mutated pointers, so a
// shallow struct copy is enough for them.
func cloneScoreTrend(t ScoreTrend) ScoreTrend {
	if t.RecentScores != nil {
		t.RecentScores = append([]float64(nil), t.RecentScores...)
	}
	if t.RecentAttempts != nil {
		// Deep-copy: each element is a pointer, so a shallow slice copy would
		// still alias the underlying float64s with the cached snapshot. The nil
		// guard preserves the nil-vs-empty distinction (a non-nil empty slice
		// clones to a non-nil empty slice via make(.., 0)), which the front end
		// relies on to tell "no in-window attempt" from "old wire".
		attempts := make([]*float64, len(t.RecentAttempts))
		for i, v := range t.RecentAttempts {
			attempts[i] = copyFloat(v)
		}
		t.RecentAttempts = attempts
	}
	return t
}

func cloneScores(src map[string]Score) map[string]Score {
	dst := make(map[string]Score, len(src))
	for k, v := range src {
		models := make([]ModelScore, len(v.Models))
		copy(models, v.Models)
		for i := range models {
			models[i].Trend = cloneScoreTrend(models[i].Trend)
		}
		v.Models = models
		v.Trend = cloneScoreTrend(v.Trend)
		dst[k] = v
	}
	return dst
}

// ErrDisabled is returned by callers that want to distinguish "client not
// configured" from real upstream errors. The Client itself never returns
// it — callers should check NewClientFromEnv()==nil instead. Exposed
// so external tests can lean on the sentinel without copying the string.
var ErrDisabled = errors.New("rpdiag client disabled")
