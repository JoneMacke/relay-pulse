package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"monitor/internal/logger"

	_ "modernc.org/sqlite" // 纯Go实现的SQLite驱动
)

// SQLiteStorage SQLite存储实现
type SQLiteStorage struct {
	db  *sql.DB
	ctx context.Context
}

// NewSQLiteStorage 创建SQLite存储
func NewSQLiteStorage(dbPath string) (*SQLiteStorage, error) {
	// 使用WAL模式和其他参数解决并发锁问题
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_timeout=5000&_busy_timeout=5000", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// 设置连接池参数（WAL模式支持更好的并发）
	db.SetMaxOpenConns(1) // SQLite建议单个写连接
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	return &SQLiteStorage{db: db, ctx: context.Background()}, nil
}

// WithContext 返回绑定指定 context 的存储实例
func (s *SQLiteStorage) WithContext(ctx context.Context) Storage {
	if ctx == nil {
		return s
	}
	return &SQLiteStorage{
		db:  s.db,
		ctx: ctx,
	}
}

// effectiveCtx 返回有效的 context
func (s *SQLiteStorage) effectiveCtx() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// Init 初始化数据库表
func (s *SQLiteStorage) Init() error {
	ctx := s.effectiveCtx()
	schema := `
	CREATE TABLE IF NOT EXISTS probe_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		provider TEXT NOT NULL,
		service TEXT NOT NULL,
		channel TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		status INTEGER NOT NULL,
		sub_status TEXT NOT NULL DEFAULT '',
		latency INTEGER NOT NULL,
		timestamp INTEGER NOT NULL,
		error_detail TEXT NOT NULL DEFAULT ''
	);
	`

	_, err := s.db.ExecContext(ctx, schema)
	if err != nil {
		return fmt.Errorf("初始化数据库失败: %w", err)
	}

	// 兼容旧数据库：添加缺失的列
	if err := s.ensureSubStatusColumn(); err != nil {
		return err
	}
	if err := s.ensureChannelColumn(); err != nil {
		return err
	}
	if err := s.ensureHttpCodeColumn(); err != nil {
		return err
	}
	if err := s.ensureModelColumn(); err != nil {
		return err
	}
	if err := s.ensureErrorDetailColumn(); err != nil {
		return err
	}
	if err := s.ensureProbeHistoryModelIDColumn(); err != nil {
		return err
	}
	if err := s.ensureProbeHistoryModelIDIndex(); err != nil {
		return err
	}

	// 在列迁移完成后创建索引
	//
	// 索引设计说明：
	// - 复合索引专为核心查询优化：GetLatest() 和 GetHistory()
	// - 所有业务查询都包含完整的 (provider, service, channel, model) 等值条件
	// - timestamp DESC 支持时间范围查询和排序，避免额外排序开销
	// - 包含查询所需的大部分字段（status, sub_status, latency），尽量减少回表
	// - 列顺序遵循 B-Tree 最佳实践：等值列在前，范围/排序列在后
	//
	// 性能优化：
	// - SQLite 不支持 INCLUDE 子句，使用复合索引模拟覆盖索引效果
	// - 虽然索引会变大，但 SQLite 查询优化器可以利用索引减少数据页访问
	// - 对于小型数据集（<1GB），性能提升明显
	//
	// ⚠️ 维护注意事项：
	// - 如果未来新增"不带 channel/model 的高频查询"，需要重新评估索引策略
	// - SQLite 对大数据量（>1GB）性能有限，建议迁移到 PostgreSQL
	//
	// 性能验证：EXPLAIN QUERY PLAN SELECT ... WHERE provider=? AND service=? AND channel=? AND model=? AND timestamp>=?
	if _, err := s.db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_probe_history_psc_ts_cover`); err != nil {
		return fmt.Errorf("删除旧覆盖索引失败: %w", err)
	}
	indexSQL := `
	CREATE INDEX IF NOT EXISTS idx_probe_history_pscm_ts_cover
	ON probe_history(provider, service, channel, model, timestamp DESC, status, sub_status, latency, http_code);
	`
	if _, err := s.db.ExecContext(ctx, indexSQL); err != nil {
		return fmt.Errorf("创建覆盖索引失败: %w", err)
	}

	// 新增 timestamp 索引：用于历史数据清理任务（PurgeOldRecords）
	// 支持高效的 WHERE timestamp < ? ORDER BY timestamp LIMIT ? 查询
	timestampIndexSQL := `
	CREATE INDEX IF NOT EXISTS idx_probe_history_timestamp
	ON probe_history(timestamp);
	`
	if _, err := s.db.ExecContext(ctx, timestampIndexSQL); err != nil {
		return fmt.Errorf("创建 timestamp 索引失败: %w", err)
	}

	// 事件功能相关表
	if err := s.initEventTables(ctx); err != nil {
		return err
	}

	// 自动移板 override 持久化表
	if err := s.initOverrideTables(ctx); err != nil {
		return err
	}
	// 质量移板机器字段迁移（幂等 + 一次性 secondary 回填）
	if err := s.ensureOverrideQualityColumns(); err != nil {
		return err
	}

	return nil
}

// ensureSubStatusColumn 在旧表上添加 sub_status 列（向后兼容）
func (s *SQLiteStorage) ensureSubStatusColumn() error {
	ctx := s.effectiveCtx()
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(probe_history)`)
	if err != nil {
		return fmt.Errorf("查询表结构失败: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var (
			cid          int
			name         string
			colType      string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("扫描表结构失败: %w", err)
		}
		if name == "sub_status" {
			hasColumn = true
			break
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历表结构失败: %w", err)
	}

	if hasColumn {
		return nil // 列已存在，无需添加
	}

	// 添加列
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE probe_history ADD COLUMN sub_status TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("添加 sub_status 列失败: %w", err)
	}

	logger.Info("storage", "已为 probe_history 表添加 sub_status 列")
	return nil
}

// ensureChannelColumn 在旧表上添加 channel 列（向后兼容）
func (s *SQLiteStorage) ensureChannelColumn() error {
	ctx := s.effectiveCtx()
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(probe_history)`)
	if err != nil {
		return fmt.Errorf("查询表结构失败: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var (
			cid          int
			name         string
			colType      string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("扫描表结构失败: %w", err)
		}
		if name == "channel" {
			hasColumn = true
			break
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历表结构失败: %w", err)
	}

	if hasColumn {
		return nil // 列已存在，无需添加
	}

	// 添加列
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE probe_history ADD COLUMN channel TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("添加 channel 列失败: %w", err)
	}

	logger.Info("storage", "已为 probe_history 表添加 channel 列")
	return nil
}

// ensureHttpCodeColumn 在旧表上添加 http_code 列（向后兼容）
func (s *SQLiteStorage) ensureHttpCodeColumn() error {
	ctx := s.effectiveCtx()
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(probe_history)`)
	if err != nil {
		return fmt.Errorf("查询表结构失败: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var (
			cid          int
			name         string
			colType      string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("扫描表结构失败: %w", err)
		}
		if name == "http_code" {
			hasColumn = true
			break
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历表结构失败: %w", err)
	}

	if hasColumn {
		return nil // 列已存在，无需添加
	}

	// 添加列
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE probe_history ADD COLUMN http_code INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("添加 http_code 列失败: %w", err)
	}

	logger.Info("storage", "已为 probe_history 表添加 http_code 列")
	return nil
}

// ensureModelColumn 在旧表上添加 model 列（向后兼容）
func (s *SQLiteStorage) ensureModelColumn() error {
	ctx := s.effectiveCtx()
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(probe_history)`)
	if err != nil {
		return fmt.Errorf("查询表结构失败: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var (
			cid          int
			name         string
			colType      string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("扫描表结构失败: %w", err)
		}
		if name == "model" {
			hasColumn = true
			break
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历表结构失败: %w", err)
	}

	if hasColumn {
		return nil // 列已存在，无需添加
	}

	// 添加列
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE probe_history ADD COLUMN model TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("添加 model 列失败: %w", err)
	}

	logger.Info("storage", "已为 probe_history 表添加 model 列")
	return nil
}

// ensureErrorDetailColumn 在旧表上添加 error_detail 列（向后兼容）
func (s *SQLiteStorage) ensureErrorDetailColumn() error {
	ctx := s.effectiveCtx()
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(probe_history)`)
	if err != nil {
		return fmt.Errorf("查询表结构失败: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var (
			cid          int
			name         string
			colType      string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("扫描表结构失败: %w", err)
		}
		if name == "error_detail" {
			hasColumn = true
			break
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历表结构失败: %w", err)
	}

	if hasColumn {
		return nil // 列已存在，无需添加
	}

	// 添加列
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE probe_history ADD COLUMN error_detail TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("添加 error_detail 列失败: %w", err)
	}

	logger.Info("storage", "已为 probe_history 表添加 error_detail 列")
	return nil
}

// ensureProbeHistoryModelIDColumn 在旧表上添加 model_id 列（向后兼容，可空，无默认值）
func (s *SQLiteStorage) ensureProbeHistoryModelIDColumn() error {
	ctx := s.effectiveCtx()
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(probe_history)`)
	if err != nil {
		return fmt.Errorf("检查 model_id 列失败: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid          int
			name         string
			colType      string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("扫描表结构失败: %w", err)
		}
		if name == "model_id" {
			return nil // 列已存在，无需添加
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历表结构失败: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, `ALTER TABLE probe_history ADD COLUMN model_id TEXT`); err != nil {
		return fmt.Errorf("添加 model_id 列失败: %w", err)
	}

	logger.Info("storage", "已为 probe_history 表添加 model_id 列")
	return nil
}

// ensureProbeHistoryModelIDIndex 创建 model_id 的部分索引（幂等）
func (s *SQLiteStorage) ensureProbeHistoryModelIDIndex() error {
	ctx := s.effectiveCtx()
	if _, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_probe_history_mid_ts
		ON probe_history(model_id, timestamp DESC)
		WHERE model_id IS NOT NULL`); err != nil {
		return fmt.Errorf("创建 model_id 索引失败: %w", err)
	}
	return nil
}

// MigrateChannelData 根据配置将 channel 为空的旧数据迁移到指定 channel
func (s *SQLiteStorage) MigrateChannelData(mappings []ChannelMigrationMapping) error {
	ctx := s.effectiveCtx()
	var pending int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe_history WHERE channel = ''`).Scan(&pending); err != nil {
		return fmt.Errorf("检测 channel 迁移需求失败: %w", err)
	}

	if pending == 0 {
		return nil
	}

	if len(mappings) == 0 {
		logger.Info("storage", "检测到 channel 为空的历史记录，但未提供迁移映射", "pending", pending)
		return nil
	}

	logger.Info("storage", "检测到 channel 为空的历史记录，开始迁移", "pending", pending)

	var totalUpdated int64
	for _, mapping := range mappings {
		if mapping.Channel == "" {
			continue
		}

		result, err := s.db.ExecContext(
			ctx,
			`UPDATE probe_history SET channel = ? WHERE channel = '' AND provider = ? AND service = ?`,
			mapping.Channel, mapping.Provider, mapping.Service,
		)
		if err != nil {
			return fmt.Errorf("迁移 channel 数据失败 (provider=%s service=%s): %w", mapping.Provider, mapping.Service, err)
		}

		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("获取迁移影响行数失败 (provider=%s service=%s): %w", mapping.Provider, mapping.Service, err)
		}

		if affected > 0 {
			totalUpdated += affected
			logger.Info("storage", "已迁移记录",
				"count", affected, "channel", mapping.Channel, "provider", mapping.Provider, "service", mapping.Service)
		}
	}

	if totalUpdated == 0 {
		logger.Info("storage", "channel 迁移：没有匹配的记录需要更新（可能缺少配置或 channel 仍为空）")
		return nil
	}

	remaining := int64(pending) - totalUpdated
	if remaining > 0 {
		logger.Info("storage", "channel 迁移完成", "updated", totalUpdated, "remaining", remaining)
	} else {
		logger.Info("storage", "channel 迁移完成", "updated", totalUpdated)
	}

	return nil
}

// BackfillProbeHistoryModelIDs 按映射把 model_id IS NULL 的历史行分批回填为对应 model_id（幂等）。
// 歧义检查（同一 psc4 key 映射到多个不同 model_id）在任何写库操作前完成，发现歧义立即返回错误。
func (s *SQLiteStorage) BackfillProbeHistoryModelIDs(mappings []ModelIDMigrationMapping) error {
	const backfillBatch = 5000

	// 步骤一：歧义检查（Go 侧，写库前）
	// 构建 psc4 -> model_id 的唯一映射，跳过 ModelID 为空的条目。
	type psc4 struct{ provider, service, channel, model string }
	seen := make(map[psc4]string, len(mappings))
	for _, m := range mappings {
		if m.ModelID == "" {
			continue
		}
		k := psc4{m.Provider, m.Service, m.Channel, m.Model}
		if existing, ok := seen[k]; ok && existing != m.ModelID {
			return fmt.Errorf(
				"model_id 回填歧义：(%s/%s/%s, model=%s) 映射到多个 model_id (%s vs %s)",
				m.Provider, m.Service, m.Channel, m.Model, existing, m.ModelID,
			)
		}
		seen[k] = m.ModelID
	}

	// 步骤二：对每个唯一映射执行分批 UPDATE（仅更新 model_id IS NULL 的行）
	ctx := s.effectiveCtx()
	var total int64
	for k, modelID := range seen {
		for {
			res, err := s.db.ExecContext(ctx, `
				UPDATE probe_history SET model_id = ?
				WHERE id IN (
					SELECT id FROM probe_history
					WHERE model_id IS NULL AND provider = ? AND service = ? AND channel = ? AND model = ?
					LIMIT ?)`,
				modelID, k.provider, k.service, k.channel, k.model, backfillBatch)
			if err != nil {
				return fmt.Errorf("回填 model_id 失败: %w", err)
			}
			n, _ := res.RowsAffected()
			total += n
			if n == 0 {
				break
			}
		}
	}
	if total > 0 {
		logger.Info("storage", "probe_history model_id 回填完成", "rows", total)
	}
	return nil
}

// Ping 检查数据库连通性
func (s *SQLiteStorage) Ping() error {
	return s.db.PingContext(s.effectiveCtx())
}

// Close 关闭数据库
// SqlDB 返回底层 *sql.DB 实例（用于 onboarding 等扩展模块共享连接）
func (s *SQLiteStorage) SqlDB() *sql.DB {
	return s.db
}

func (s *SQLiteStorage) Close() error {
	return s.db.Close()
}

// SaveRecord 保存探测记录
func (s *SQLiteStorage) SaveRecord(record *ProbeRecord) error {
	ctx := s.effectiveCtx()
	query := `
		INSERT INTO probe_history (provider, service, channel, model, status, sub_status, http_code, latency, timestamp, error_detail, model_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := s.db.ExecContext(ctx, query,
		record.Provider,
		record.Service,
		record.Channel,
		record.Model,
		record.Status,
		string(record.SubStatus),
		record.HttpCode,
		record.Latency,
		record.Timestamp,
		record.ErrorDetail,
		record.ModelID,
	)

	if err != nil {
		return fmt.Errorf("保存记录失败: %w", err)
	}

	id, _ := result.LastInsertId()
	record.ID = id
	return nil
}

// GetLatestBatch 批量获取每个监测项的最新记录
//
// 实现说明：
// - 使用 CTE(keys) 承载入参列表
// - 使用窗口函数 ROW_NUMBER() 分组取最新一条（rn=1）
func (s *SQLiteStorage) GetLatestBatch(keys []MonitorKey) (map[MonitorKey]*ProbeRecord, error) {
	ctx := s.effectiveCtx()
	result := make(map[MonitorKey]*ProbeRecord, len(keys))
	if len(keys) == 0 {
		return result, nil
	}

	var b strings.Builder
	args := make([]any, 0, len(keys)*4)

	b.WriteString("WITH keys(provider, service, channel, model) AS (VALUES ")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("(?, ?, ?, ?)")
		args = append(args, k.Provider, k.Service, k.Channel, k.Model)
	}
	b.WriteString(`),
ranked AS (
	SELECT
		p.id, p.provider, p.service, p.channel, p.model, p.status, p.sub_status, p.http_code, p.latency, p.timestamp,
		ROW_NUMBER() OVER (PARTITION BY p.provider, p.service, p.channel, p.model ORDER BY p.timestamp DESC, p.id DESC) AS rn
	FROM probe_history p
	JOIN keys k
		ON p.provider = k.provider AND p.service = k.service AND p.channel = k.channel AND p.model = k.model
)
SELECT id, provider, service, channel, model, status, sub_status, http_code, latency, timestamp
FROM ranked
WHERE rn = 1
`)

	rows, err := s.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("批量查询最新记录失败: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		rec := &ProbeRecord{}
		var subStatusStr string
		if err := rows.Scan(
			&rec.ID,
			&rec.Provider,
			&rec.Service,
			&rec.Channel,
			&rec.Model,
			&rec.Status,
			&subStatusStr,
			&rec.HttpCode,
			&rec.Latency,
			&rec.Timestamp,
		); err != nil {
			return nil, fmt.Errorf("扫描最新记录失败: %w", err)
		}
		rec.SubStatus = SubStatus(subStatusStr)
		result[MonitorKey{Provider: rec.Provider, Service: rec.Service, Channel: rec.Channel, Model: rec.Model}] = rec
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代最新记录失败: %w", err)
	}

	return result, nil
}

// GetHistoryBatch 批量获取多个监测项的历史记录（时间范围）
//
// 实现说明：
// - 使用 CTE(keys) + JOIN 过滤目标监测项集合
// - ORDER BY 按 (provider,service,channel,timestamp DESC) 输出
// - 返回前对每个 key 的切片做 reverse，保证时间升序（与 GetHistory 一致）
func (s *SQLiteStorage) GetHistoryBatch(keys []MonitorKey, since time.Time) (map[MonitorKey][]*ProbeRecord, error) {
	ctx := s.effectiveCtx()
	result := make(map[MonitorKey][]*ProbeRecord, len(keys))
	if len(keys) == 0 {
		return result, nil
	}

	var b strings.Builder
	args := make([]any, 0, len(keys)*4+1)

	b.WriteString("WITH keys(provider, service, channel, model) AS (VALUES ")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("(?, ?, ?, ?)")
		args = append(args, k.Provider, k.Service, k.Channel, k.Model)
	}
	b.WriteString(")\n")
	b.WriteString(`
SELECT
	p.id, p.provider, p.service, p.channel, p.model, p.status, p.sub_status, p.http_code, p.latency, p.timestamp
FROM probe_history p
JOIN keys k
	ON p.provider = k.provider AND p.service = k.service AND p.channel = k.channel AND p.model = k.model
WHERE p.timestamp >= ?
ORDER BY p.provider, p.service, p.channel, p.model, p.timestamp DESC
`)
	args = append(args, since.Unix())

	rows, err := s.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("批量查询历史记录失败: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		rec := &ProbeRecord{}
		var subStatusStr string
		if err := rows.Scan(
			&rec.ID,
			&rec.Provider,
			&rec.Service,
			&rec.Channel,
			&rec.Model,
			&rec.Status,
			&subStatusStr,
			&rec.HttpCode,
			&rec.Latency,
			&rec.Timestamp,
		); err != nil {
			return nil, fmt.Errorf("扫描历史记录失败: %w", err)
		}
		rec.SubStatus = SubStatus(subStatusStr)
		key := MonitorKey{Provider: rec.Provider, Service: rec.Service, Channel: rec.Channel, Model: rec.Model}
		result[key] = append(result[key], rec)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代历史记录失败: %w", err)
	}

	// DESC 取数利用索引，返回前对每个 key 翻转为时间升序
	for k := range result {
		reverseRecords(result[k])
	}

	return result, nil
}

// dedupModelIDKeys 把入参 ProbeHistoryKey 列表按 model_id 去重，
// 返回 model_id→入参 key 的反查表（first-wins）与去重后的 model_id 顺序列表。
//
// 返回 map 用于把 DB 行（其展示名列在改名后已是新名）映射回 caller 的 key——
// 必须按 model_id 反查，绝不能用 DB 行的 provider/service/channel/model 重建 key。
func dedupModelIDKeys(keys []ProbeHistoryKey) (map[string]ProbeHistoryKey, []string) {
	keyByModelID := make(map[string]ProbeHistoryKey, len(keys))
	modelIDs := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, dup := keyByModelID[k.ModelID]; dup {
			continue
		}
		keyByModelID[k.ModelID] = k
		modelIDs = append(modelIDs, k.ModelID)
	}
	return keyByModelID, modelIDs
}

// GetLatestBatchByModelID 按 model_id 批量获取每个监测项的最新记录（跨展示名历史连续）。
//
// 实现说明：
// - CTE(keys) 仅承载 model_id 列，JOIN probe_history 命中部分索引 idx_probe_history_mid_ts
// - 窗口函数 ROW_NUMBER() 按 model_id 分组取最新一条（rn=1）
// - 结果 map 以入参 ProbeHistoryKey 为键，按 SELECT 出的 model_id 反查
func (s *SQLiteStorage) GetLatestBatchByModelID(keys []ProbeHistoryKey) (map[ProbeHistoryKey]*ProbeRecord, error) {
	ctx := s.effectiveCtx()
	result := make(map[ProbeHistoryKey]*ProbeRecord, len(keys))
	if len(keys) == 0 {
		return result, nil
	}

	keyByModelID, modelIDs := dedupModelIDKeys(keys)

	var b strings.Builder
	args := make([]any, 0, len(modelIDs))

	b.WriteString("WITH keys(model_id) AS (VALUES ")
	for i, modelID := range modelIDs {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("(?)")
		args = append(args, modelID)
	}
	b.WriteString(`),
ranked AS (
	SELECT
		p.model_id, p.id, p.provider, p.service, p.channel, p.model, p.status, p.sub_status, p.http_code, p.latency, p.timestamp,
		ROW_NUMBER() OVER (PARTITION BY p.model_id ORDER BY p.timestamp DESC, p.id DESC) AS rn
	FROM probe_history p
	JOIN keys k ON p.model_id = k.model_id
)
SELECT model_id, id, provider, service, channel, model, status, sub_status, http_code, latency, timestamp
FROM ranked
WHERE rn = 1
`)

	rows, err := s.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("按 model_id 批量查询最新记录失败: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		rec := &ProbeRecord{}
		var modelID string
		var subStatusStr string
		if err := rows.Scan(
			&modelID,
			&rec.ID,
			&rec.Provider,
			&rec.Service,
			&rec.Channel,
			&rec.Model,
			&rec.Status,
			&subStatusStr,
			&rec.HttpCode,
			&rec.Latency,
			&rec.Timestamp,
		); err != nil {
			return nil, fmt.Errorf("扫描 model_id 最新记录失败: %w", err)
		}
		rec.SubStatus = SubStatus(subStatusStr)
		rec.ModelID = modelID
		if key, ok := keyByModelID[modelID]; ok {
			result[key] = rec
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代 model_id 最新记录失败: %w", err)
	}

	return result, nil
}

// GetHistoryBatchByModelID 按 model_id 批量获取多个监测项的历史记录（时间范围，跨展示名历史连续）。
//
// 实现说明：
// - CTE(keys) 仅承载 model_id，JOIN 命中部分索引；ORDER BY model_id, timestamp DESC 便于分桶
// - 返回前对每个 key 的切片 reverse，保证时间升序（与 GetHistoryBatch 一致）
// - 结果 map 以入参 ProbeHistoryKey 为键，按 SELECT 出的 model_id 反查
func (s *SQLiteStorage) GetHistoryBatchByModelID(keys []ProbeHistoryKey, since time.Time) (map[ProbeHistoryKey][]*ProbeRecord, error) {
	ctx := s.effectiveCtx()
	result := make(map[ProbeHistoryKey][]*ProbeRecord, len(keys))
	if len(keys) == 0 {
		return result, nil
	}

	keyByModelID, modelIDs := dedupModelIDKeys(keys)

	var b strings.Builder
	args := make([]any, 0, len(modelIDs)+1)

	b.WriteString("WITH keys(model_id) AS (VALUES ")
	for i, modelID := range modelIDs {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("(?)")
		args = append(args, modelID)
	}
	b.WriteString(")\n")
	b.WriteString(`
SELECT
	p.model_id, p.id, p.provider, p.service, p.channel, p.model, p.status, p.sub_status, p.http_code, p.latency, p.timestamp
FROM probe_history p
JOIN keys k ON p.model_id = k.model_id
WHERE p.timestamp >= ?
ORDER BY p.model_id, p.timestamp DESC
`)
	args = append(args, since.Unix())

	rows, err := s.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("按 model_id 批量查询历史记录失败: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		rec := &ProbeRecord{}
		var modelID string
		var subStatusStr string
		if err := rows.Scan(
			&modelID,
			&rec.ID,
			&rec.Provider,
			&rec.Service,
			&rec.Channel,
			&rec.Model,
			&rec.Status,
			&subStatusStr,
			&rec.HttpCode,
			&rec.Latency,
			&rec.Timestamp,
		); err != nil {
			return nil, fmt.Errorf("扫描 model_id 历史记录失败: %w", err)
		}
		rec.SubStatus = SubStatus(subStatusStr)
		rec.ModelID = modelID
		if key, ok := keyByModelID[modelID]; ok {
			result[key] = append(result[key], rec)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代 model_id 历史记录失败: %w", err)
	}

	// DESC 取数利用索引，返回前对每个 key 翻转为时间升序
	for k := range result {
		reverseRecords(result[k])
	}

	return result, nil
}

// GetLatest 获取最新记录
func (s *SQLiteStorage) GetLatest(provider, service, channel, model string) (*ProbeRecord, error) {
	ctx := s.effectiveCtx()
	query := `
		SELECT id, provider, service, channel, model, status, sub_status, http_code, latency, timestamp
		FROM probe_history
		WHERE provider = ? AND service = ? AND channel = ? AND model = ?
		ORDER BY timestamp DESC, id DESC
		LIMIT 1
	`

	var record ProbeRecord
	var subStatusStr string
	err := s.db.QueryRowContext(ctx, query, provider, service, channel, model).Scan(
		&record.ID,
		&record.Provider,
		&record.Service,
		&record.Channel,
		&record.Model,
		&record.Status,
		&subStatusStr,
		&record.HttpCode,
		&record.Latency,
		&record.Timestamp,
	)

	if err == sql.ErrNoRows {
		return nil, nil // 没有记录不算错误
	}

	if err != nil {
		return nil, fmt.Errorf("查询最新记录失败: %w", err)
	}

	record.SubStatus = SubStatus(subStatusStr)
	return &record, nil
}

// GetHistory 获取历史记录
func (s *SQLiteStorage) GetHistory(provider, service, channel, model string, since time.Time) ([]*ProbeRecord, error) {
	ctx := s.effectiveCtx()
	// 使用 ORDER BY timestamp DESC 以利用索引（索引是 timestamp DESC）
	// 返回前在 Go 代码中反转为时间升序
	query := `
		SELECT id, provider, service, channel, model, status, sub_status, http_code, latency, timestamp
		FROM probe_history
		WHERE provider = ? AND service = ? AND channel = ? AND model = ? AND timestamp >= ?
		ORDER BY timestamp DESC
	`

	rows, err := s.db.QueryContext(ctx, query, provider, service, channel, model, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("查询历史记录失败: %w", err)
	}
	defer rows.Close()

	var records []*ProbeRecord
	for rows.Next() {
		var record ProbeRecord
		var subStatusStr string
		err := rows.Scan(
			&record.ID,
			&record.Provider,
			&record.Service,
			&record.Channel,
			&record.Model,
			&record.Status,
			&subStatusStr,
			&record.HttpCode,
			&record.Latency,
			&record.Timestamp,
		)
		if err != nil {
			return nil, fmt.Errorf("扫描记录失败: %w", err)
		}
		record.SubStatus = SubStatus(subStatusStr)
		records = append(records, &record)
	}

	// 检查迭代过程中是否发生错误
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代记录失败: %w", err)
	}

	// DESC 取数利用索引，返回前翻转为时间升序
	reverseRecords(records)

	return records, nil
}

// GetLatestByModelID 按 model_id 获取最新一条记录（跨展示名历史连续）。
func (s *SQLiteStorage) GetLatestByModelID(modelID string) (*ProbeRecord, error) {
	ctx := s.effectiveCtx()
	query := `
		SELECT id, provider, service, channel, model, status, sub_status, http_code, latency, timestamp
		FROM probe_history
		WHERE model_id = ?
		ORDER BY timestamp DESC, id DESC
		LIMIT 1
	`

	var record ProbeRecord
	var subStatusStr string
	err := s.db.QueryRowContext(ctx, query, modelID).Scan(
		&record.ID,
		&record.Provider,
		&record.Service,
		&record.Channel,
		&record.Model,
		&record.Status,
		&subStatusStr,
		&record.HttpCode,
		&record.Latency,
		&record.Timestamp,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("按 model_id 查询最新记录失败: %w", err)
	}

	record.SubStatus = SubStatus(subStatusStr)
	record.ModelID = modelID
	return &record, nil
}

// GetHistoryByModelID 按 model_id 获取 since 之后的历史记录（时间升序，跨展示名历史连续）。
func (s *SQLiteStorage) GetHistoryByModelID(modelID string, since time.Time) ([]*ProbeRecord, error) {
	ctx := s.effectiveCtx()
	query := `
		SELECT id, provider, service, channel, model, status, sub_status, http_code, latency, timestamp
		FROM probe_history
		WHERE model_id = ? AND timestamp >= ?
		ORDER BY timestamp DESC
	`

	rows, err := s.db.QueryContext(ctx, query, modelID, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("按 model_id 查询历史记录失败: %w", err)
	}
	defer rows.Close()

	var records []*ProbeRecord
	for rows.Next() {
		var record ProbeRecord
		var subStatusStr string
		if err := rows.Scan(
			&record.ID,
			&record.Provider,
			&record.Service,
			&record.Channel,
			&record.Model,
			&record.Status,
			&subStatusStr,
			&record.HttpCode,
			&record.Latency,
			&record.Timestamp,
		); err != nil {
			return nil, fmt.Errorf("扫描 model_id 历史记录失败: %w", err)
		}
		record.SubStatus = SubStatus(subStatusStr)
		record.ModelID = modelID
		records = append(records, &record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代 model_id 历史记录失败: %w", err)
	}

	reverseRecords(records)
	return records, nil
}

// GetHistoryWithLimitByModelID 按 model_id 获取 since 之后最近 limit 条记录（timestamp DESC + id DESC）。
// 返回值保留时间倒序（最新在前）；包含 ErrorDetail 字段。
func (s *SQLiteStorage) GetHistoryWithLimitByModelID(modelID string, since time.Time, limit int) ([]*ProbeRecord, error) {
	ctx := s.effectiveCtx()
	if limit <= 0 {
		limit = 200
	}

	query := `
		SELECT id, provider, service, channel, model, status, sub_status, http_code, latency, timestamp, error_detail
		FROM probe_history
		WHERE model_id = ? AND timestamp >= ?
		ORDER BY timestamp DESC, id DESC
		LIMIT ?
	`

	rows, err := s.db.QueryContext(ctx, query, modelID, since.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("按 model_id 查询限量历史记录失败: %w", err)
	}
	defer rows.Close()

	records := make([]*ProbeRecord, 0, limit)
	for rows.Next() {
		var record ProbeRecord
		var subStatusStr string
		if err := rows.Scan(
			&record.ID,
			&record.Provider,
			&record.Service,
			&record.Channel,
			&record.Model,
			&record.Status,
			&subStatusStr,
			&record.HttpCode,
			&record.Latency,
			&record.Timestamp,
			&record.ErrorDetail,
		); err != nil {
			return nil, fmt.Errorf("扫描 model_id 限量历史记录失败: %w", err)
		}
		record.SubStatus = SubStatus(subStatusStr)
		record.ModelID = modelID
		records = append(records, &record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代 model_id 限量历史记录失败: %w", err)
	}

	return records, nil
}

// GetHistoryWithLimit 获取指定 PSCM 在 since 之后的最近 limit 条记录，按 timestamp DESC + id DESC 返回。
// 返回值保留时间倒序（最新在前），适合直接喂给管理后台日志面板；调用方需要升序时自行 reverseRecords。
// 与 GetHistory 不同，此方法返回 ErrorDetail 字段。
func (s *SQLiteStorage) GetHistoryWithLimit(provider, service, channel, model string, since time.Time, limit int) ([]*ProbeRecord, error) {
	ctx := s.effectiveCtx()
	if limit <= 0 {
		limit = 200
	}

	// 复合排序键 (timestamp DESC, id DESC) 与 idx_probe_history_pscm_ts_cover 配合，
	// 同一秒内多条记录按 id 倒序，保证分页结果稳定。
	query := `
		SELECT id, provider, service, channel, model, status, sub_status, http_code, latency, timestamp, error_detail
		FROM probe_history
		WHERE provider = ? AND service = ? AND channel = ? AND model = ? AND timestamp >= ?
		ORDER BY timestamp DESC, id DESC
		LIMIT ?
	`

	rows, err := s.db.QueryContext(ctx, query, provider, service, channel, model, since.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("查询历史记录失败: %w", err)
	}
	defer rows.Close()

	records := make([]*ProbeRecord, 0, limit)
	for rows.Next() {
		var record ProbeRecord
		var subStatusStr string
		if err := rows.Scan(
			&record.ID,
			&record.Provider,
			&record.Service,
			&record.Channel,
			&record.Model,
			&record.Status,
			&subStatusStr,
			&record.HttpCode,
			&record.Latency,
			&record.Timestamp,
			&record.ErrorDetail,
		); err != nil {
			return nil, fmt.Errorf("扫描记录失败: %w", err)
		}
		record.SubStatus = SubStatus(subStatusStr)
		records = append(records, &record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代记录失败: %w", err)
	}

	return records, nil
}

// ===== 状态订阅通知（事件）相关方法 =====

// initEventTables 初始化事件相关表
func (s *SQLiteStorage) initEventTables(ctx context.Context) error {
	// 服务状态表（状态机持久化）
	serviceStatesSchema := `
	CREATE TABLE IF NOT EXISTS service_states (
		provider TEXT NOT NULL,
		service TEXT NOT NULL,
		channel TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		stable_available INTEGER NOT NULL DEFAULT -1,
		streak_count INTEGER NOT NULL DEFAULT 0,
		streak_status INTEGER NOT NULL DEFAULT -1,
		last_record_id INTEGER,
		last_timestamp INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (provider, service, channel, model)
	);
	`
	if _, err := s.db.ExecContext(ctx, serviceStatesSchema); err != nil {
		return fmt.Errorf("创建 service_states 表失败: %w", err)
	}

	// 状态事件表
	statusEventsSchema := `
	CREATE TABLE IF NOT EXISTS status_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		provider TEXT NOT NULL,
		service TEXT NOT NULL,
		channel TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		event_type TEXT NOT NULL,
		from_status INTEGER NOT NULL,
		to_status INTEGER NOT NULL,
		trigger_record_id INTEGER NOT NULL,
		observed_at INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		meta TEXT
	);
	`
	if _, err := s.db.ExecContext(ctx, statusEventsSchema); err != nil {
		return fmt.Errorf("创建 status_events 表失败: %w", err)
	}

	// 通道状态表（通道级状态机持久化，用于 events.mode=channel）
	channelStatesSchema := `
	CREATE TABLE IF NOT EXISTS channel_states (
		provider TEXT NOT NULL,
		service TEXT NOT NULL,
		channel TEXT NOT NULL DEFAULT '',
		stable_available INTEGER NOT NULL DEFAULT -1,
		down_count INTEGER NOT NULL DEFAULT 0,
		known_count INTEGER NOT NULL DEFAULT 0,
		last_record_id INTEGER,
		last_timestamp INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (provider, service, channel)
	);
	`
	if _, err := s.db.ExecContext(ctx, channelStatesSchema); err != nil {
		return fmt.Errorf("创建 channel_states 表失败: %w", err)
	}

	// 兼容旧数据库：事件表补齐 model 列（旧数据默认 model=''）
	if err := s.ensureServiceStatesModelColumn(); err != nil {
		return err
	}
	if err := s.ensureStatusEventsModelColumn(); err != nil {
		return err
	}

	// 创建索引
	eventsIndexSQL := `
	CREATE INDEX IF NOT EXISTS idx_status_events_psc_id
	ON status_events(provider, service, channel, id);
	`
	if _, err := s.db.ExecContext(ctx, eventsIndexSQL); err != nil {
		return fmt.Errorf("创建 status_events 索引失败: %w", err)
	}

	// 创建唯一约束索引（幂等性保障）
	uniqueIndexSQL := `
	CREATE UNIQUE INDEX IF NOT EXISTS idx_status_events_unique
	ON status_events(provider, service, channel, event_type, trigger_record_id);
	`
	if _, err := s.db.ExecContext(ctx, uniqueIndexSQL); err != nil {
		return fmt.Errorf("创建 status_events 唯一索引失败: %w", err)
	}

	return nil
}

func (s *SQLiteStorage) ensureServiceStatesModelColumn() error {
	ctx := s.effectiveCtx()

	// 检查表结构：是否有 model 列，以及 model 是否在主键中
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(service_states)`)
	if err != nil {
		return fmt.Errorf("查询 service_states 表结构失败: %w", err)
	}
	defer rows.Close()

	hasModelColumn := false
	modelInPK := false
	for rows.Next() {
		var (
			cid          int
			name         string
			colType      string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("扫描 service_states 表结构失败: %w", err)
		}
		if name == "model" {
			hasModelColumn = true
			modelInPK = pk > 0 // pk > 0 表示该列是主键的一部分
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历 service_states 表结构失败: %w", err)
	}

	// 如果 model 列存在且在主键中，无需迁移
	if hasModelColumn && modelInPK {
		return nil
	}

	// 需要重建表以更新主键（SQLite 不支持 ALTER PRIMARY KEY）
	logger.Info("storage", "正在迁移 service_states 表以添加 model 到主键...")

	// 使用事务确保原子性
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始事务失败: %w", err)
	}
	defer tx.Rollback()

	// 1. 清理可能残留的临时表
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS service_states_new`); err != nil {
		return fmt.Errorf("清理临时表失败: %w", err)
	}

	// 2. 创建新表（包含 model 在主键中）
	createSQL := `
		CREATE TABLE service_states_new (
			provider TEXT NOT NULL,
			service TEXT NOT NULL,
			channel TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			stable_available INTEGER NOT NULL DEFAULT -1,
			streak_count INTEGER NOT NULL DEFAULT 0,
			streak_status INTEGER NOT NULL DEFAULT -1,
			last_record_id INTEGER,
			last_timestamp INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (provider, service, channel, model)
		)
	`
	if _, err := tx.ExecContext(ctx, createSQL); err != nil {
		return fmt.Errorf("创建临时表失败: %w", err)
	}

	// 3. 复制数据（根据是否有 model 列选择不同的 SQL）
	var copySQL string
	if hasModelColumn {
		// 旧表有 model 列，需要 COALESCE 处理可能的 NULL 值
		copySQL = `
			INSERT INTO service_states_new (provider, service, channel, model, stable_available, streak_count, streak_status, last_record_id, last_timestamp)
			SELECT provider, service, channel, COALESCE(model, ''), stable_available, streak_count, streak_status, last_record_id, last_timestamp
			FROM service_states
		`
	} else {
		// 旧表无 model 列，使用空字符串作为默认值
		copySQL = `
			INSERT INTO service_states_new (provider, service, channel, model, stable_available, streak_count, streak_status, last_record_id, last_timestamp)
			SELECT provider, service, channel, '', stable_available, streak_count, streak_status, last_record_id, last_timestamp
			FROM service_states
		`
	}
	if _, err := tx.ExecContext(ctx, copySQL); err != nil {
		return fmt.Errorf("复制数据失败: %w", err)
	}

	// 4. 删除旧表
	if _, err := tx.ExecContext(ctx, `DROP TABLE service_states`); err != nil {
		return fmt.Errorf("删除旧表失败: %w", err)
	}

	// 5. 重命名新表
	if _, err := tx.ExecContext(ctx, `ALTER TABLE service_states_new RENAME TO service_states`); err != nil {
		return fmt.Errorf("重命名表失败: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}

	logger.Info("storage", "已完成 service_states 表迁移（model 列已加入主键）")
	return nil
}

func (s *SQLiteStorage) ensureStatusEventsModelColumn() error {
	ctx := s.effectiveCtx()
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(status_events)`)
	if err != nil {
		return fmt.Errorf("查询 status_events 表结构失败: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var (
			cid          int
			name         string
			colType      string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("扫描 status_events 表结构失败: %w", err)
		}
		if name == "model" {
			hasColumn = true
			break
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历 status_events 表结构失败: %w", err)
	}

	if hasColumn {
		return nil
	}

	if _, err := s.db.ExecContext(ctx, `ALTER TABLE status_events ADD COLUMN model TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("添加 status_events.model 列失败: %w", err)
	}

	logger.Info("storage", "已为 status_events 表添加 model 列")
	return nil
}

// GetServiceState 获取服务状态机持久化状态
func (s *SQLiteStorage) GetServiceState(provider, service, channel, model string) (*ServiceState, error) {
	ctx := s.effectiveCtx()
	query := `
		SELECT provider, service, channel, model, stable_available, streak_count, streak_status, last_record_id, last_timestamp
		FROM service_states
		WHERE provider = ? AND service = ? AND channel = ? AND model = ?
	`

	var state ServiceState
	var lastRecordID sql.NullInt64

	err := s.db.QueryRowContext(ctx, query, provider, service, channel, model).Scan(
		&state.Provider,
		&state.Service,
		&state.Channel,
		&state.Model,
		&state.StableAvailable,
		&state.StreakCount,
		&state.StreakStatus,
		&lastRecordID,
		&state.LastTimestamp,
	)

	if err == sql.ErrNoRows {
		return nil, nil // 尚未初始化
	}
	if err != nil {
		return nil, fmt.Errorf("查询服务状态失败: %w", err)
	}

	if lastRecordID.Valid {
		state.LastRecordID = lastRecordID.Int64
	}

	return &state, nil
}

// UpsertServiceState 写入或更新服务状态机持久化状态
func (s *SQLiteStorage) UpsertServiceState(state *ServiceState) error {
	ctx := s.effectiveCtx()
	query := `
		INSERT INTO service_states (provider, service, channel, model, stable_available, streak_count, streak_status, last_record_id, last_timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, service, channel, model) DO UPDATE SET
			stable_available = excluded.stable_available,
			streak_count = excluded.streak_count,
			streak_status = excluded.streak_status,
			last_record_id = excluded.last_record_id,
			last_timestamp = excluded.last_timestamp
	`

	_, err := s.db.ExecContext(ctx, query,
		state.Provider,
		state.Service,
		state.Channel,
		state.Model,
		state.StableAvailable,
		state.StreakCount,
		state.StreakStatus,
		state.LastRecordID,
		state.LastTimestamp,
	)

	if err != nil {
		return fmt.Errorf("更新服务状态失败: %w", err)
	}

	return nil
}

// GetChannelState 获取通道级状态机持久化状态
func (s *SQLiteStorage) GetChannelState(provider, service, channel string) (*ChannelState, error) {
	ctx := s.effectiveCtx()
	query := `
		SELECT provider, service, channel, stable_available, down_count, known_count, last_record_id, last_timestamp
		FROM channel_states
		WHERE provider = ? AND service = ? AND channel = ?
	`

	var state ChannelState
	var lastRecordID sql.NullInt64

	err := s.db.QueryRowContext(ctx, query, provider, service, channel).Scan(
		&state.Provider,
		&state.Service,
		&state.Channel,
		&state.StableAvailable,
		&state.DownCount,
		&state.KnownCount,
		&lastRecordID,
		&state.LastTimestamp,
	)

	if err == sql.ErrNoRows {
		return nil, nil // 尚未初始化
	}
	if err != nil {
		return nil, fmt.Errorf("查询通道状态失败: %w", err)
	}

	if lastRecordID.Valid {
		state.LastRecordID = lastRecordID.Int64
	}

	return &state, nil
}

// UpsertChannelState 写入或更新通道级状态机持久化状态
func (s *SQLiteStorage) UpsertChannelState(state *ChannelState) error {
	ctx := s.effectiveCtx()
	query := `
		INSERT INTO channel_states (provider, service, channel, stable_available, down_count, known_count, last_record_id, last_timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, service, channel) DO UPDATE SET
			stable_available = excluded.stable_available,
			down_count = excluded.down_count,
			known_count = excluded.known_count,
			last_record_id = excluded.last_record_id,
			last_timestamp = excluded.last_timestamp
	`

	_, err := s.db.ExecContext(ctx, query,
		state.Provider,
		state.Service,
		state.Channel,
		state.StableAvailable,
		state.DownCount,
		state.KnownCount,
		state.LastRecordID,
		state.LastTimestamp,
	)

	if err != nil {
		return fmt.Errorf("更新通道状态失败: %w", err)
	}

	return nil
}

// GetModelStatesForChannel 获取通道下所有模型的状态
func (s *SQLiteStorage) GetModelStatesForChannel(provider, service, channel string) ([]*ServiceState, error) {
	ctx := s.effectiveCtx()
	query := `
		SELECT provider, service, channel, model, stable_available, streak_count, streak_status, last_record_id, last_timestamp
		FROM service_states
		WHERE provider = ? AND service = ? AND channel = ?
		ORDER BY model
	`

	rows, err := s.db.QueryContext(ctx, query, provider, service, channel)
	if err != nil {
		return nil, fmt.Errorf("查询通道模型状态失败: %w", err)
	}
	defer rows.Close()

	var states []*ServiceState
	for rows.Next() {
		var state ServiceState
		var lastRecordID sql.NullInt64

		err := rows.Scan(
			&state.Provider,
			&state.Service,
			&state.Channel,
			&state.Model,
			&state.StableAvailable,
			&state.StreakCount,
			&state.StreakStatus,
			&lastRecordID,
			&state.LastTimestamp,
		)
		if err != nil {
			return nil, fmt.Errorf("扫描模型状态失败: %w", err)
		}

		if lastRecordID.Valid {
			state.LastRecordID = lastRecordID.Int64
		}

		states = append(states, &state)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代模型状态失败: %w", err)
	}

	return states, nil
}

// SaveStatusEvent 保存状态变更事件
func (s *SQLiteStorage) SaveStatusEvent(event *StatusEvent) error {
	ctx := s.effectiveCtx()

	// 序列化 meta 为 JSON
	var metaJSON sql.NullString
	if event.Meta != nil && len(event.Meta) > 0 {
		metaBytes, err := json.Marshal(event.Meta)
		if err != nil {
			return fmt.Errorf("序列化事件 meta 失败: %w", err)
		}
		metaJSON = sql.NullString{String: string(metaBytes), Valid: true}
	}

	query := `
		INSERT INTO status_events (provider, service, channel, model, event_type, from_status, to_status, trigger_record_id, observed_at, created_at, meta)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := s.db.ExecContext(ctx, query,
		event.Provider,
		event.Service,
		event.Channel,
		event.Model,
		string(event.EventType),
		event.FromStatus,
		event.ToStatus,
		event.TriggerRecordID,
		event.ObservedAt,
		event.CreatedAt,
		metaJSON,
	)

	if err != nil {
		// 检查是否是唯一约束冲突（幂等处理）
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil // 重复事件，视为成功
		}
		return fmt.Errorf("保存状态事件失败: %w", err)
	}

	id, _ := result.LastInsertId()
	event.ID = id
	return nil
}

// GetStatusEvents 查询状态变更事件列表
func (s *SQLiteStorage) GetStatusEvents(sinceID int64, limit int, filters *EventFilters) ([]*StatusEvent, error) {
	ctx := s.effectiveCtx()

	var conditions []string
	var args []any

	// 游标条件
	conditions = append(conditions, "id > ?")
	args = append(args, sinceID)

	// 可选过滤条件
	if filters != nil {
		if filters.Provider != "" {
			conditions = append(conditions, "provider = ?")
			args = append(args, filters.Provider)
		}
		if filters.Service != "" {
			conditions = append(conditions, "service = ?")
			args = append(args, filters.Service)
		}
		if filters.Channel != "" {
			conditions = append(conditions, "channel = ?")
			args = append(args, filters.Channel)
		}
		if len(filters.Types) > 0 {
			placeholders := make([]string, len(filters.Types))
			for i, t := range filters.Types {
				placeholders[i] = "?"
				args = append(args, string(t))
			}
			conditions = append(conditions, "event_type IN ("+strings.Join(placeholders, ",")+")")
		}
	}

	// 限制条数
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	query := fmt.Sprintf(`
		SELECT id, provider, service, channel, model, event_type, from_status, to_status, trigger_record_id, observed_at, created_at, meta
		FROM status_events
		WHERE %s
		ORDER BY id ASC
		LIMIT ?
	`, strings.Join(conditions, " AND "))
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询状态事件失败: %w", err)
	}
	defer rows.Close()

	var events []*StatusEvent
	for rows.Next() {
		var event StatusEvent
		var eventTypeStr string
		var metaJSON sql.NullString

		err := rows.Scan(
			&event.ID,
			&event.Provider,
			&event.Service,
			&event.Channel,
			&event.Model,
			&eventTypeStr,
			&event.FromStatus,
			&event.ToStatus,
			&event.TriggerRecordID,
			&event.ObservedAt,
			&event.CreatedAt,
			&metaJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("扫描状态事件失败: %w", err)
		}

		event.EventType = EventType(eventTypeStr)

		// 反序列化 meta
		if metaJSON.Valid && metaJSON.String != "" {
			var meta map[string]any
			if err := json.Unmarshal([]byte(metaJSON.String), &meta); err == nil {
				event.Meta = meta
			}
		}

		events = append(events, &event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代状态事件失败: %w", err)
	}

	return events, nil
}

// GetLatestEventID 获取最新事件 ID
func (s *SQLiteStorage) GetLatestEventID() (int64, error) {
	ctx := s.effectiveCtx()
	query := `SELECT COALESCE(MAX(id), 0) FROM status_events`

	var latestID int64
	err := s.db.QueryRowContext(ctx, query).Scan(&latestID)
	if err != nil {
		return 0, fmt.Errorf("查询最新事件 ID 失败: %w", err)
	}

	return latestID, nil
}

// ===== 历史数据清理相关方法 =====

// PurgeOldRecords 清理指定时间之前的历史记录
// SQLite 实现：使用子查询批量删除，带锁冲突重试
func (s *SQLiteStorage) PurgeOldRecords(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	cutoff := before.Unix()

	// 使用主键 id 批量删除，确保走 timestamp 索引
	// 注意：SQLite 不支持 LIMIT 在 DELETE 中直接使用，使用子查询
	query := `
		DELETE FROM probe_history
		WHERE id IN (
			SELECT id FROM probe_history
			WHERE timestamp < ?
			ORDER BY timestamp
			LIMIT ?
		)
	`

	result, err := s.db.ExecContext(ctx, query, cutoff, batchSize)
	if err != nil {
		// SQLite 锁冲突由调用方（Cleaner）处理重试逻辑
		return 0, fmt.Errorf("删除历史记录失败: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("获取删除行数失败: %w", err)
	}

	return affected, nil
}

// ===== 自动移板 override 持久化 =====

func (s *SQLiteStorage) initOverrideTables(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS monitor_overrides (
		provider      TEXT NOT NULL,
		service       TEXT NOT NULL,
		channel       TEXT NOT NULL DEFAULT '',
		model         TEXT NOT NULL DEFAULT '',
		board         TEXT NOT NULL DEFAULT '',
		cold_reason   TEXT NOT NULL DEFAULT '',
		sponsor_level TEXT NOT NULL DEFAULT '',
		created_at    INTEGER NOT NULL,
		updated_at    INTEGER NOT NULL,
		PRIMARY KEY (provider, service, channel, model)
	);
	`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("创建 monitor_overrides 表失败: %w", err)
	}
	return nil
}

// ensureOverrideQualityColumns 在既有 monitor_overrides 表上补齐质量移板列
// （幂等，参照 ensureSubStatusColumn）。当 availability_latched 列本次新增时，
// 一次性把既有 secondary 行回填为可用率闩锁：本功能上线前，任何 secondary
// 都必然是可用率驱动降板；不回填的话 Restore 会误判为「从未降板」并在临界可用率
// 时错误地把它升回主板。回填以「本次是否新增该列」为守卫，绝不无条件每次 Init
// 都跑（否则将来质量驱动的 secondary 会被误标成可用率闩锁）。
func (s *SQLiteStorage) ensureOverrideQualityColumns() error {
	ctx := s.effectiveCtx()
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(monitor_overrides)`)
	if err != nil {
		return fmt.Errorf("查询 monitor_overrides 表结构失败: %w", err)
	}
	existing := map[string]bool{}
	for rows.Next() {
		var (
			cid     int
			name    string
			colType string
			notNull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("扫描 monitor_overrides 表结构失败: %w", err)
		}
		existing[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历 monitor_overrides 表结构失败: %w", err)
	}

	addCols := []struct{ name, ddl string }{
		{"board_reason", `ALTER TABLE monitor_overrides ADD COLUMN board_reason TEXT NOT NULL DEFAULT ''`},
		{"quality_latched", `ALTER TABLE monitor_overrides ADD COLUMN quality_latched INTEGER NOT NULL DEFAULT 0`},
		{"quality_recovery_count", `ALTER TABLE monitor_overrides ADD COLUMN quality_recovery_count INTEGER NOT NULL DEFAULT 0`},
		{"quality_trigger_models", `ALTER TABLE monitor_overrides ADD COLUMN quality_trigger_models TEXT NOT NULL DEFAULT ''`},
		{"quality_last_generation", `ALTER TABLE monitor_overrides ADD COLUMN quality_last_generation INTEGER NOT NULL DEFAULT 0`},
		{"availability_latched", `ALTER TABLE monitor_overrides ADD COLUMN availability_latched INTEGER NOT NULL DEFAULT 0`},
	}
	availabilityLatchedExisted := existing["availability_latched"]
	for _, c := range addCols {
		if existing[c.name] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, c.ddl); err != nil {
			return fmt.Errorf("添加 monitor_overrides 列 %s 失败: %w", c.name, err)
		}
	}
	if !availabilityLatchedExisted {
		if _, err := s.db.ExecContext(ctx, `UPDATE monitor_overrides SET availability_latched = 1 WHERE board = 'secondary'`); err != nil {
			return fmt.Errorf("回填 availability_latched 失败: %w", err)
		}
		logger.Info("storage", "已为 monitor_overrides 表补齐质量移板列并回填 secondary 可用率闩锁")
	}
	return nil
}

// ListMonitorOverrides 加载全部 override 快照。
func (s *SQLiteStorage) ListMonitorOverrides() ([]MonitorOverrideRecord, error) {
	ctx := s.effectiveCtx()
	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, service, channel, model, board, cold_reason, sponsor_level,
		       board_reason, quality_latched, quality_recovery_count,
		       quality_trigger_models, quality_last_generation, availability_latched,
		       created_at, updated_at
		FROM monitor_overrides
		ORDER BY provider, service, channel, model
	`)
	if err != nil {
		return nil, fmt.Errorf("查询 monitor_overrides 失败: %w", err)
	}
	defer rows.Close()

	var records []MonitorOverrideRecord
	for rows.Next() {
		var r MonitorOverrideRecord
		// SQLite 无 bool 类型：闩锁列以 INTEGER 0/1 存，代次以 int64 存
		var qLatched, availLatched int
		var qGen int64
		if err := rows.Scan(
			&r.Key.Provider, &r.Key.Service, &r.Key.Channel, &r.Key.Model,
			&r.Board, &r.ColdReason, &r.SponsorLevel,
			&r.BoardReason, &qLatched, &r.QualityRecoveryCount,
			&r.QualityTriggerModels, &qGen, &availLatched,
			&r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("扫描 monitor_overrides 失败: %w", err)
		}
		r.QualityLatched = qLatched != 0
		r.AvailabilityLatched = availLatched != 0
		r.QualityLastGeneration = uint64(qGen)
		records = append(records, r)
	}
	return records, rows.Err()
}

// ReplaceMonitorOverrides 原子替换全部 override 快照。
// 在事务中先查询现有 created_at（用于保留首次创建时间），再 DELETE ALL + INSERT ALL。
func (s *SQLiteStorage) ReplaceMonitorOverrides(records []MonitorOverrideRecord) error {
	ctx := s.effectiveCtx()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始 monitor_overrides 事务失败: %w", err)
	}
	defer tx.Rollback()

	// 保留已有记录的 created_at
	existingCreatedAt := make(map[MonitorKey]int64)
	rows, err := tx.QueryContext(ctx, `SELECT provider, service, channel, model, created_at FROM monitor_overrides`)
	if err != nil {
		return fmt.Errorf("查询现有 monitor_overrides 失败: %w", err)
	}
	for rows.Next() {
		var key MonitorKey
		var createdAt int64
		if err := rows.Scan(&key.Provider, &key.Service, &key.Channel, &key.Model, &createdAt); err != nil {
			rows.Close()
			return fmt.Errorf("扫描现有 monitor_overrides 失败: %w", err)
		}
		existingCreatedAt[key] = createdAt
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历现有 monitor_overrides 失败: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_overrides`); err != nil {
		return fmt.Errorf("清空 monitor_overrides 失败: %w", err)
	}

	if len(records) > 0 {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO monitor_overrides (
				provider, service, channel, model, board, cold_reason, sponsor_level,
				board_reason, quality_latched, quality_recovery_count,
				quality_trigger_models, quality_last_generation, availability_latched,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return fmt.Errorf("预编译 monitor_overrides 插入语句失败: %w", err)
		}
		defer stmt.Close()

		now := time.Now().Unix()
		for _, r := range records {
			createdAt := r.CreatedAt
			if createdAt == 0 {
				if existing, ok := existingCreatedAt[r.Key]; ok {
					createdAt = existing
				} else {
					createdAt = now
				}
			}
			updatedAt := now

			if _, err := stmt.ExecContext(ctx,
				r.Key.Provider, r.Key.Service, r.Key.Channel, r.Key.Model,
				r.Board, r.ColdReason, r.SponsorLevel,
				r.BoardReason, b2i(r.QualityLatched), r.QualityRecoveryCount,
				r.QualityTriggerModels, int64(r.QualityLastGeneration), b2i(r.AvailabilityLatched),
				createdAt, updatedAt,
			); err != nil {
				return fmt.Errorf("写入 monitor_overrides 失败: %w", err)
			}
		}
	}

	return tx.Commit()
}
