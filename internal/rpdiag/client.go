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
	N7D          int       `json:"n_7d"`
	N30D         int       `json:"n_30d"`
}

// ModelScore captures one (channel, model) row from rpdiag.
type ModelScore struct {
	Model     string     `json:"model,omitempty"`
	ModelKey  string     `json:"model_key,omitempty"`
	Score     *float64   `json:"score,omitempty"`
	Trend     ScoreTrend `json:"trend"`
	DetailURL string     `json:"detail_url,omitempty"`
}

// Score is the aggregated quality view for one (provider, service, channel)
// triple. MaxScore picks the strongest model — listing users want to know
// "what is this channel capable of", and averaging across models would
// punish channels that also host weaker fallbacks.
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
	}
	if !enabled {
		// Disabled clients still need to honour the Scores() contract; tag
		// them so external code can branch if desired.
		c.cache = map[string]Score{}
		c.expiresAt = time.Now().Add(time.Hour) // freeze empty snapshot
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
	}
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
	if snap, ok := c.freshSnapshot(time.Now()); ok {
		return snap, nil
	}

	v, err, _ := c.sf.Do("scores", func() (interface{}, error) {
		if snap, ok := c.freshSnapshot(time.Now()); ok {
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

	scores := c.buildScores(payload.Items)
	now := time.Now()

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

// buildScores collapses many rpdiag rows into one entry per (provider,
// service, channel) triple. Rows that lack a representative fingerprint
// sample, or that come from the public /submit pipeline
// (`submission_source=user` / `U-` channel prefix), are skipped — those
// entries don't exist in relaypulse listings and would never join.
//
// The representative score is the latest single fingerprint sample (not
// rpdiag's composite `final_quality_score`, which folds in latency and
// availability multipliers). Composite scoring belongs to rpdiag's own
// ranking page; relaypulse's "Quality" column shows pure response-quality
// per its tooltip, so the sort key must match that visual semantics.
func (c *Client) buildScores(rows []rankingRow) map[string]Score {
	out := make(map[string]Score, len(rows))

	for _, row := range rows {
		latest := latestFingerprintSample(row.ScoreTrend)
		if latest == nil {
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
			Model:     row.Model,
			ModelKey:  row.ModelKey,
			Score:     copyFloat(latest),
			Trend:     row.ScoreTrend,
			DetailURL: row.DetailURL,
		})

		if entry.MaxScore == nil || *latest > *entry.MaxScore {
			entry.MaxScore = copyFloat(latest)
			entry.Trend = row.ScoreTrend
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

func cloneScores(src map[string]Score) map[string]Score {
	dst := make(map[string]Score, len(src))
	for k, v := range src {
		models := make([]ModelScore, len(v.Models))
		copy(models, v.Models)
		v.Models = models
		dst[k] = v
	}
	return dst
}

// ErrDisabled is returned by callers that want to distinguish "client not
// configured" from real upstream errors. The Client itself never returns
// it — callers should check NewClientFromEnv()==nil instead. Exposed
// so external tests can lean on the sentinel without copying the string.
var ErrDisabled = errors.New("rpdiag client disabled")
