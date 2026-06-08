package onboarding

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SQLStore 基于 database/sql 的 Store 实现（适用于 SQLite）。
type SQLStore struct {
	db *sql.DB
}

// NewSQLStore 创建基于 database/sql 的 Store。
func NewSQLStore(db *sql.DB) *SQLStore {
	return &SQLStore{db: db}
}

// InitTable 创建 onboarding_submissions 表和索引。
// 应在应用启动时由 storage Init() 调用。
func (s *SQLStore) InitTable(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS onboarding_submissions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		public_id TEXT NOT NULL UNIQUE,
		status TEXT NOT NULL DEFAULT 'pending',

		provider_name TEXT NOT NULL,
		website_url TEXT NOT NULL,
		category TEXT NOT NULL,

		service_type TEXT NOT NULL,
		template_name TEXT NOT NULL,

		sponsor_level TEXT NOT NULL,

		channel_type TEXT NOT NULL,
		channel_source TEXT NOT NULL,
		channel_group TEXT NOT NULL DEFAULT '',
		channel_code TEXT NOT NULL,
		target_provider TEXT NOT NULL DEFAULT '',
		target_service TEXT NOT NULL DEFAULT '',
		target_channel TEXT NOT NULL DEFAULT '',
		channel_name TEXT NOT NULL DEFAULT '',
		listed_since TEXT NOT NULL DEFAULT '',
		expires_at TEXT NOT NULL DEFAULT '',
		price_min REAL NOT NULL DEFAULT 0,
		price_max REAL NOT NULL DEFAULT 0,

		base_url TEXT NOT NULL,
		api_key_encrypted TEXT NOT NULL,
		api_key_fingerprint TEXT NOT NULL,
		api_key_last4 TEXT NOT NULL,

		test_job_id TEXT NOT NULL,
		test_passed_at INTEGER NOT NULL,
		test_latency_ms INTEGER NOT NULL DEFAULT 0,
		test_http_code INTEGER NOT NULL DEFAULT 0,

		contact_info TEXT,
		submitter_ip_hash TEXT,
		locale TEXT,

		admin_note TEXT,
		admin_config_json TEXT,
		reviewed_at INTEGER,

		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_onboarding_status ON onboarding_submissions(status, created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_onboarding_fingerprint ON onboarding_submissions(api_key_fingerprint);
	`
	_, err := s.db.ExecContext(ctx, schema)
	if err != nil {
		return err
	}
	return s.ensureColumns(ctx)
}

// ensureColumns 为旧数据库补齐新列（兼容热升级）
func (s *SQLStore) ensureColumns(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(onboarding_submissions)`)
	if err != nil {
		return fmt.Errorf("查询表结构失败: %w", err)
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var defaultVal any
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultVal, &pk); err != nil {
			return fmt.Errorf("读取表结构失败: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	migrations := []struct {
		name string
		ddl  string
	}{
		{"channel_name", `ALTER TABLE onboarding_submissions ADD COLUMN channel_name TEXT NOT NULL DEFAULT ''`},
		{"listed_since", `ALTER TABLE onboarding_submissions ADD COLUMN listed_since TEXT NOT NULL DEFAULT ''`},
		{"price_min", `ALTER TABLE onboarding_submissions ADD COLUMN price_min REAL NOT NULL DEFAULT 0`},
		{"price_max", `ALTER TABLE onboarding_submissions ADD COLUMN price_max REAL NOT NULL DEFAULT 0`},
		{"expires_at", `ALTER TABLE onboarding_submissions ADD COLUMN expires_at TEXT NOT NULL DEFAULT ''`},
		{"target_provider", `ALTER TABLE onboarding_submissions ADD COLUMN target_provider TEXT NOT NULL DEFAULT ''`},
		{"target_service", `ALTER TABLE onboarding_submissions ADD COLUMN target_service TEXT NOT NULL DEFAULT ''`},
		{"target_channel", `ALTER TABLE onboarding_submissions ADD COLUMN target_channel TEXT NOT NULL DEFAULT ''`},
		{"channel_group", `ALTER TABLE onboarding_submissions ADD COLUMN channel_group TEXT NOT NULL DEFAULT ''`},
	}
	for _, m := range migrations {
		if existing[m.name] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, m.ddl); err != nil {
			return fmt.Errorf("迁移列 %s 失败: %w", m.name, err)
		}
	}
	return nil
}

// Save 保存新申请
func (s *SQLStore) Save(ctx context.Context, sub *Submission) error {
	query := `
	INSERT INTO onboarding_submissions (
		public_id, status, provider_name, website_url, category,
		service_type, template_name, sponsor_level,
		channel_type, channel_source, channel_group, channel_code,
		target_provider, target_service, target_channel,
		channel_name, listed_since, expires_at, price_min, price_max,
		base_url, api_key_encrypted, api_key_fingerprint, api_key_last4,
		test_job_id, test_passed_at, test_latency_ms, test_http_code,
		contact_info, submitter_ip_hash, locale,
		admin_note, admin_config_json, reviewed_at,
		created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	result, err := s.db.ExecContext(ctx, query,
		sub.PublicID, sub.Status, sub.ProviderName, sub.WebsiteURL, sub.Category,
		sub.ServiceType, sub.TemplateName, sub.SponsorLevel,
		sub.ChannelType, sub.ChannelSource, sub.ChannelGroup, sub.ChannelCode,
		sub.TargetProvider, sub.TargetService, sub.TargetChannel,
		sub.ChannelName, sub.ListedSince, sub.ExpiresAt, sub.PriceMin, sub.PriceMax,
		sub.BaseURL, sub.APIKeyEncrypted, sub.APIKeyFingerprint, sub.APIKeyLast4,
		sub.TestJobID, sub.TestPassedAt, sub.TestLatency, sub.TestHTTPCode,
		nullStr(sub.ContactInfo), nullStr(sub.SubmitterIPHash), nullStr(sub.Locale),
		nullStr(sub.AdminNote), nullStr(sub.AdminConfigJSON), sub.ReviewedAt,
		sub.CreatedAt, sub.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("保存申请失败: %w", err)
	}

	id, err := result.LastInsertId()
	if err == nil {
		sub.ID = id
	}
	return nil
}

// GetByPublicID 按公开 ID 查询
func (s *SQLStore) GetByPublicID(ctx context.Context, publicID string) (*Submission, error) {
	return s.scanOne(ctx, "SELECT "+allColumns+" FROM onboarding_submissions WHERE public_id = ?", publicID)
}

// GetByID 按内部 ID 查询
func (s *SQLStore) GetByID(ctx context.Context, id int64) (*Submission, error) {
	return s.scanOne(ctx, "SELECT "+allColumns+" FROM onboarding_submissions WHERE id = ?", id)
}

// List 列表查询
func (s *SQLStore) List(ctx context.Context, status, search string, limit, offset int) ([]*Submission, int, error) {
	// count 与 list 共用同一组 where 条件与过滤参数，list 再追加分页参数，
	// 保证两次查询命中的行集合一致。
	var conditions []string
	var filterArgs []any

	if status != "" && status != "all" {
		conditions = append(conditions, "status = ?")
		filterArgs = append(filterArgs, status)
	}
	if search != "" {
		conditions = append(conditions, "public_id LIKE ? ESCAPE '!'")
		filterArgs = append(filterArgs, search)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = " WHERE " + strings.Join(conditions, " AND ")
	}

	countQuery := "SELECT COUNT(*) FROM onboarding_submissions" + whereClause
	listQuery := "SELECT " + allColumns + " FROM onboarding_submissions" + whereClause +
		" ORDER BY created_at DESC LIMIT ? OFFSET ?"

	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, filterArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计申请数失败: %w", err)
	}

	listArgs := append(append([]any{}, filterArgs...), limit, offset)
	rows, err := s.db.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询申请列表失败: %w", err)
	}
	defer rows.Close()

	var results []*Submission
	for rows.Next() {
		sub, err := scanSubmission(rows)
		if err != nil {
			return nil, 0, err
		}
		results = append(results, sub)
	}
	return results, total, rows.Err()
}

// Update 更新申请
func (s *SQLStore) Update(ctx context.Context, sub *Submission) error {
	query := `
	UPDATE onboarding_submissions SET
		status = ?, provider_name = ?, website_url = ?, category = ?,
		service_type = ?, template_name = ?, sponsor_level = ?,
		channel_type = ?, channel_source = ?, channel_group = ?, channel_code = ?,
		target_provider = ?, target_service = ?, target_channel = ?,
		channel_name = ?, listed_since = ?, expires_at = ?, price_min = ?, price_max = ?,
		base_url = ?,
		contact_info = ?,
		admin_note = ?, admin_config_json = ?, reviewed_at = ?,
		updated_at = ?
	WHERE id = ?`

	_, err := s.db.ExecContext(ctx, query,
		sub.Status, sub.ProviderName, sub.WebsiteURL, sub.Category,
		sub.ServiceType, sub.TemplateName, sub.SponsorLevel,
		sub.ChannelType, sub.ChannelSource, sub.ChannelGroup, sub.ChannelCode,
		sub.TargetProvider, sub.TargetService, sub.TargetChannel,
		sub.ChannelName, sub.ListedSince, sub.ExpiresAt, sub.PriceMin, sub.PriceMax,
		sub.BaseURL,
		nullStr(sub.ContactInfo),
		nullStr(sub.AdminNote), nullStr(sub.AdminConfigJSON), sub.ReviewedAt,
		sub.UpdatedAt,
		sub.ID,
	)
	if err != nil {
		return fmt.Errorf("更新申请失败: %w", err)
	}
	return nil
}

// CountByIPToday 统计今天的提交数
func (s *SQLStore) CountByIPToday(ctx context.Context, ipHash string) (int, error) {
	start, end := todayRange()
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM onboarding_submissions WHERE submitter_ip_hash = ? AND created_at >= ? AND created_at < ?",
		ipHash, start, end,
	).Scan(&count)
	return count, err
}

// CountByFingerprint 统计未驳回的同指纹申请数
func (s *SQLStore) CountByFingerprint(ctx context.Context, fingerprint string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM onboarding_submissions WHERE api_key_fingerprint = ? AND status != 'rejected'",
		fingerprint,
	).Scan(&count)
	return count, err
}

// DeleteByPublicID 按公开 ID 删除申请
func (s *SQLStore) DeleteByPublicID(ctx context.Context, publicID string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM onboarding_submissions WHERE public_id = ?", publicID)
	if err != nil {
		return fmt.Errorf("删除申请失败: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("申请不存在")
	}
	return nil
}

// === 内部辅助 ===

const allColumns = `id, public_id, status,
	provider_name, website_url, category,
	service_type, template_name, sponsor_level,
	channel_type, channel_source, channel_group, channel_code,
	target_provider, target_service, target_channel,
	channel_name, listed_since, expires_at, price_min, price_max,
	base_url, api_key_encrypted, api_key_fingerprint, api_key_last4,
	test_job_id, test_passed_at, test_latency_ms, test_http_code,
	contact_info, submitter_ip_hash, locale,
	admin_note, admin_config_json, reviewed_at,
	created_at, updated_at`

// scanner 是 *sql.Row 和 *sql.Rows 的共同扫描接口
type scanner interface {
	Scan(dest ...any) error
}

func scanSubmission(s scanner) (*Submission, error) {
	var sub Submission
	var contactInfo, ipHash, locale, adminNote, adminConfigJSON sql.NullString
	var reviewedAt sql.NullInt64

	err := s.Scan(
		&sub.ID, &sub.PublicID, &sub.Status,
		&sub.ProviderName, &sub.WebsiteURL, &sub.Category,
		&sub.ServiceType, &sub.TemplateName, &sub.SponsorLevel,
		&sub.ChannelType, &sub.ChannelSource, &sub.ChannelGroup, &sub.ChannelCode,
		&sub.TargetProvider, &sub.TargetService, &sub.TargetChannel,
		&sub.ChannelName, &sub.ListedSince, &sub.ExpiresAt, &sub.PriceMin, &sub.PriceMax,
		&sub.BaseURL, &sub.APIKeyEncrypted, &sub.APIKeyFingerprint, &sub.APIKeyLast4,
		&sub.TestJobID, &sub.TestPassedAt, &sub.TestLatency, &sub.TestHTTPCode,
		&contactInfo, &ipHash, &locale,
		&adminNote, &adminConfigJSON, &reviewedAt,
		&sub.CreatedAt, &sub.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	sub.ContactInfo = contactInfo.String
	sub.SubmitterIPHash = ipHash.String
	sub.Locale = locale.String
	sub.AdminNote = adminNote.String
	sub.AdminConfigJSON = adminConfigJSON.String
	if reviewedAt.Valid {
		v := reviewedAt.Int64
		sub.ReviewedAt = &v
	}

	return &sub, nil
}

func (s *SQLStore) scanOne(ctx context.Context, query string, args ...any) (*Submission, error) {
	row := s.db.QueryRowContext(ctx, query, args...)
	sub, err := scanSubmission(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询申请失败: %w", err)
	}
	return sub, nil
}

// nullStr 将空字符串转为 sql.NullString
func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
