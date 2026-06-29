package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"monitor/internal/config"
	"monitor/internal/logger"
)

// PostgresStorage PostgreSQL 存储实现
type PostgresStorage struct {
	pool *pgxpool.Pool
	ctx  context.Context
}

// NewPostgresStorage 创建 PostgreSQL 存储
func NewPostgresStorage(cfg *config.PostgresConfig) (*PostgresStorage, error) {
	// 构建连接字符串
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host,
		cfg.Port,
		cfg.User,
		cfg.Password,
		cfg.Database,
		cfg.SSLMode,
	)

	// 解析连接池配置
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析 PostgreSQL 连接配置失败: %w", err)
	}

	// 设置连接池参数
	poolConfig.MaxConns = int32(cfg.MaxOpenConns)
	poolConfig.MinConns = int32(cfg.MaxIdleConns)

	// 解析连接最大生命周期
	if cfg.ConnMaxLifetime != "" {
		lifetime, err := time.ParseDuration(cfg.ConnMaxLifetime)
		if err != nil {
			logger.Warn("storage", "解析 conn_max_lifetime 失败，使用默认值 1h", "error", err)
			lifetime = time.Hour
		}
		poolConfig.MaxConnLifetime = lifetime
	} else {
		poolConfig.MaxConnLifetime = time.Hour
	}

	// 创建连接池
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("创建 PostgreSQL 连接池失败: %w", err)
	}

	// 测试连接
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接 PostgreSQL 失败: %w", err)
	}

	return &PostgresStorage{
		pool: pool,
		ctx:  ctx,
	}, nil
}

// WithContext 返回绑定指定 context 的存储实例
func (s *PostgresStorage) WithContext(ctx context.Context) Storage {
	if ctx == nil {
		return s
	}
	return &PostgresStorage{
		pool: s.pool,
		ctx:  ctx,
	}
}

// effectiveCtx 返回有效的 context
func (s *PostgresStorage) effectiveCtx() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// Init 初始化数据库表
func (s *PostgresStorage) Init() error {
	ctx := s.effectiveCtx()
	schema := `
	CREATE TABLE IF NOT EXISTS probe_history (
		id BIGSERIAL PRIMARY KEY,
		provider TEXT NOT NULL,
		service TEXT NOT NULL,
		channel TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		status INTEGER NOT NULL,
		sub_status TEXT NOT NULL DEFAULT '',
		http_code INTEGER NOT NULL DEFAULT 0,
		latency INTEGER NOT NULL,
		timestamp BIGINT NOT NULL,
		error_detail TEXT NOT NULL DEFAULT ''
	);
	`

	_, err := s.pool.Exec(ctx, schema)
	if err != nil {
		return fmt.Errorf("初始化 PostgreSQL 数据库失败: %w", err)
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
	// - 覆盖索引专为核心查询优化：GetLatest() 和 GetHistory()
	// - 所有业务查询都包含完整的 (provider, service, channel, model) 等值条件
	// - timestamp DESC 支持时间范围查询和排序，避免额外排序开销
	// - INCLUDE 子句包含查询所需的大部分字段，减少回表开销
	// - 列顺序遵循 B-Tree 最佳实践：等值列在前，范围/排序列在后
	//
	// 性能优化：
	// - 覆盖索引使查询从 "Index Scan + Heap Fetch" 优化为 "Index Only Scan"
	// - 减少 50% I/O（无需回表），查询性能提升 5-10x
	// - 缓存友好（索引页比数据页更紧凑，更容易全部加载到 shared_buffers）
	//
	// ⚠️ 维护注意事项：
	// - 如果未来新增"不带 channel 的高频查询"，需要重新评估索引策略
	//
	// 性能验证：EXPLAIN ANALYZE SELECT ... WHERE provider=? AND service=? AND channel=? AND timestamp>=?
	if _, err := s.pool.Exec(ctx, `DROP INDEX IF EXISTS idx_probe_history_psc_ts_cover`); err != nil {
		return fmt.Errorf("删除旧覆盖索引失败: %w", err)
	}
	indexSQL := `
	CREATE INDEX IF NOT EXISTS idx_probe_history_pscm_ts_cover
	ON probe_history (provider, service, channel, model, timestamp DESC)
	INCLUDE (status, sub_status, latency, id, http_code);
	`
	if _, err := s.pool.Exec(ctx, indexSQL); err != nil {
		return fmt.Errorf("创建覆盖索引失败: %w", err)
	}

	// 新增 timestamp 索引：用于历史数据清理任务（PurgeOldRecords）
	// 支持高效的 WHERE timestamp < ? ORDER BY timestamp LIMIT ? 查询
	// 注意：对于生产环境现有大表，建议使用 CREATE INDEX CONCURRENTLY 手动创建
	timestampIndexSQL := `
	CREATE INDEX IF NOT EXISTS idx_probe_history_timestamp
	ON probe_history (timestamp);
	`
	if _, err := s.pool.Exec(ctx, timestampIndexSQL); err != nil {
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

	return nil
}

// ensureSubStatusColumn 在旧表上添加 sub_status 列（向后兼容）
func (s *PostgresStorage) ensureSubStatusColumn() error {
	ctx := s.effectiveCtx()
	// PostgreSQL 使用 information_schema 查询列是否存在
	checkQuery := `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'probe_history' AND column_name = 'sub_status'
	`

	var count int
	err := s.pool.QueryRow(ctx, checkQuery).Scan(&count)
	if err != nil {
		return fmt.Errorf("查询 PostgreSQL 表结构失败: %w", err)
	}

	if count > 0 {
		return nil // 列已存在，无需添加
	}

	// 添加列
	alterQuery := `ALTER TABLE probe_history ADD COLUMN sub_status TEXT NOT NULL DEFAULT ''`
	if _, err := s.pool.Exec(ctx, alterQuery); err != nil {
		return fmt.Errorf("添加 sub_status 列失败: %w", err)
	}

	logger.Info("storage", "已为 probe_history 表添加 sub_status 列 (PostgreSQL)")
	return nil
}

// ensureChannelColumn 在旧表上添加 channel 列（向后兼容）
func (s *PostgresStorage) ensureChannelColumn() error {
	ctx := s.effectiveCtx()
	checkQuery := `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'probe_history' AND column_name = 'channel'
	`

	var count int
	err := s.pool.QueryRow(ctx, checkQuery).Scan(&count)
	if err != nil {
		return fmt.Errorf("查询 PostgreSQL 表结构失败: %w", err)
	}

	if count > 0 {
		return nil // 列已存在，无需添加
	}

	// 添加列
	alterQuery := `ALTER TABLE probe_history ADD COLUMN channel TEXT NOT NULL DEFAULT ''`
	if _, err := s.pool.Exec(ctx, alterQuery); err != nil {
		return fmt.Errorf("添加 channel 列失败: %w", err)
	}

	logger.Info("storage", "已为 probe_history 表添加 channel 列 (PostgreSQL)")
	return nil
}

// ensureHttpCodeColumn 在旧表上添加 http_code 列（向后兼容）
func (s *PostgresStorage) ensureHttpCodeColumn() error {
	ctx := s.effectiveCtx()
	checkQuery := `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'probe_history' AND column_name = 'http_code'
	`

	var count int
	err := s.pool.QueryRow(ctx, checkQuery).Scan(&count)
	if err != nil {
		return fmt.Errorf("查询 PostgreSQL 表结构失败: %w", err)
	}

	if count > 0 {
		return nil // 列已存在，无需添加
	}

	// 添加列
	alterQuery := `ALTER TABLE probe_history ADD COLUMN http_code INTEGER NOT NULL DEFAULT 0`
	if _, err := s.pool.Exec(ctx, alterQuery); err != nil {
		return fmt.Errorf("添加 http_code 列失败: %w", err)
	}

	logger.Info("storage", "已为 probe_history 表添加 http_code 列 (PostgreSQL)")
	return nil
}

func (s *PostgresStorage) ensureModelColumn() error {
	ctx := s.effectiveCtx()
	checkQuery := `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'probe_history' AND column_name = 'model'
	`

	var count int
	err := s.pool.QueryRow(ctx, checkQuery).Scan(&count)
	if err != nil {
		return fmt.Errorf("查询 PostgreSQL 表结构失败: %w", err)
	}

	if count > 0 {
		return nil // 列已存在，无需添加
	}

	alterQuery := `ALTER TABLE probe_history ADD COLUMN model TEXT NOT NULL DEFAULT ''`
	if _, err := s.pool.Exec(ctx, alterQuery); err != nil {
		return fmt.Errorf("添加 model 列失败: %w", err)
	}

	logger.Info("storage", "已为 probe_history 表添加 model 列 (PostgreSQL)")
	return nil
}

// ensureErrorDetailColumn 在旧表上添加 error_detail 列（向后兼容）
func (s *PostgresStorage) ensureErrorDetailColumn() error {
	ctx := s.effectiveCtx()
	checkQuery := `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'probe_history' AND column_name = 'error_detail'
	`

	var count int
	err := s.pool.QueryRow(ctx, checkQuery).Scan(&count)
	if err != nil {
		return fmt.Errorf("查询 PostgreSQL 表结构失败: %w", err)
	}

	if count > 0 {
		return nil // 列已存在，无需添加
	}

	// 添加列
	alterQuery := `ALTER TABLE probe_history ADD COLUMN error_detail TEXT NOT NULL DEFAULT ''`
	if _, err := s.pool.Exec(ctx, alterQuery); err != nil {
		return fmt.Errorf("添加 error_detail 列失败: %w", err)
	}

	logger.Info("storage", "已为 probe_history 表添加 error_detail 列 (PostgreSQL)")
	return nil
}

// ensureProbeHistoryModelIDColumn 在旧表上添加 model_id 列（向后兼容，可空，无默认值）
func (s *PostgresStorage) ensureProbeHistoryModelIDColumn() error {
	ctx := s.effectiveCtx()
	checkQuery := `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'probe_history' AND column_name = 'model_id'
	`

	var count int
	if err := s.pool.QueryRow(ctx, checkQuery).Scan(&count); err != nil {
		return fmt.Errorf("查询 PostgreSQL 表结构失败: %w", err)
	}

	if count > 0 {
		return nil // 列已存在，无需添加
	}

	if _, err := s.pool.Exec(ctx, `ALTER TABLE probe_history ADD COLUMN model_id TEXT`); err != nil {
		return fmt.Errorf("添加 model_id 列失败: %w", err)
	}

	logger.Info("storage", "已为 probe_history 表添加 model_id 列 (PostgreSQL)")
	return nil
}

// ensureProbeHistoryModelIDIndex 创建 model_id 的部分覆盖索引（处理 invalid 残留，幂等）。
// 使用 CREATE INDEX CONCURRENTLY，不能在事务内运行；Init() 的 ensure 段直接用 s.pool.Exec，符合此约束。
func (s *PostgresStorage) ensureProbeHistoryModelIDIndex() error {
	ctx := s.effectiveCtx()

	// 检查索引是否存在，以及是否有效（CONCURRENTLY 中断会留下 invalid 索引）。
	// 用 to_regclass 按当前 search_path 解析，并校验索引归属 probe_history，
	// 避免其他 schema 下同名索引导致误判跳过创建（索引不存在时 to_regclass 返回 NULL → 0 行 → 走 no-rows 分支创建）。
	var exists, valid bool
	err := s.pool.QueryRow(ctx, `
		SELECT true, i.indisvalid
		FROM pg_class c
		JOIN pg_index i ON i.indexrelid = c.oid
		WHERE c.oid = to_regclass('idx_probe_history_mid_ts')
		  AND i.indrelid = to_regclass('probe_history')`).Scan(&exists, &valid)
	if err != nil {
		// 没有行表示索引不存在，继续创建
		if errors.Is(err, pgx.ErrNoRows) {
			exists = false
		} else {
			return fmt.Errorf("检查 model_id 索引状态失败: %w", err)
		}
	}

	if exists && !valid {
		// 清理 CONCURRENTLY 中断后留下的 invalid 索引
		if _, derr := s.pool.Exec(ctx, `DROP INDEX CONCURRENTLY IF EXISTS idx_probe_history_mid_ts`); derr != nil {
			return fmt.Errorf("清理 invalid model_id 索引失败: %w", derr)
		}
		exists = false
	}

	if exists {
		return nil // 索引已存在且有效，无需创建
	}

	if _, err := s.pool.Exec(ctx, `
		CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_probe_history_mid_ts
		ON probe_history (model_id, timestamp DESC)
		INCLUDE (status, sub_status, latency, id, http_code)
		WHERE model_id IS NOT NULL`); err != nil {
		return fmt.Errorf("创建 model_id 索引失败: %w", err)
	}

	return nil
}

// MigrateChannelData 根据配置将 channel 为空的旧数据迁移到指定 channel
func (s *PostgresStorage) MigrateChannelData(mappings []ChannelMigrationMapping) error {
	ctx := s.effectiveCtx()
	var pending int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM probe_history WHERE channel = ''`).Scan(&pending); err != nil {
		return fmt.Errorf("检测 PostgreSQL channel 迁移需求失败: %w", err)
	}

	if pending == 0 {
		return nil
	}

	if len(mappings) == 0 {
		logger.Info("storage", "检测到 channel 为空的历史记录，但未提供迁移映射 (PostgreSQL)", "pending", pending)
		return nil
	}

	logger.Info("storage", "检测到 channel 为空的历史记录，开始迁移 (PostgreSQL)", "pending", pending)

	var totalUpdated int64
	for _, mapping := range mappings {
		if mapping.Channel == "" {
			continue
		}

		tag, err := s.pool.Exec(
			ctx,
			`UPDATE probe_history SET channel = $1 WHERE channel = '' AND provider = $2 AND service = $3`,
			mapping.Channel, mapping.Provider, mapping.Service,
		)
		if err != nil {
			return fmt.Errorf("迁移 PostgreSQL channel 数据失败 (provider=%s service=%s): %w", mapping.Provider, mapping.Service, err)
		}

		affected := tag.RowsAffected()
		if affected > 0 {
			totalUpdated += affected
			logger.Info("storage", "已迁移记录 (PostgreSQL)",
				"count", affected, "channel", mapping.Channel, "provider", mapping.Provider, "service", mapping.Service)
		}
	}

	if totalUpdated == 0 {
		logger.Info("storage", "PostgreSQL channel 迁移：没有匹配的记录需要更新（可能缺少配置或 channel 仍为空）")
		return nil
	}

	remaining := int64(pending) - totalUpdated
	if remaining > 0 {
		logger.Info("storage", "PostgreSQL channel 迁移完成", "updated", totalUpdated, "remaining", remaining)
	} else {
		logger.Info("storage", "PostgreSQL channel 迁移完成", "updated", totalUpdated)
	}

	return nil
}

// Ping 检查数据库连通性
func (s *PostgresStorage) Ping() error {
	return s.pool.Ping(s.effectiveCtx())
}

// Close 关闭数据库连接
// PgxPool 返回底层 *pgxpool.Pool 实例（用于 onboarding 等扩展模块共享连接池）
func (s *PostgresStorage) PgxPool() *pgxpool.Pool {
	return s.pool
}

func (s *PostgresStorage) Close() error {
	s.pool.Close()
	return nil
}

// SaveRecord 保存探测记录
func (s *PostgresStorage) SaveRecord(record *ProbeRecord) error {
	ctx := s.effectiveCtx()
	query := `
		INSERT INTO probe_history (provider, service, channel, model, status, sub_status, http_code, latency, timestamp, error_detail, model_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id
	`

	err := s.pool.QueryRow(ctx, query,
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
	).Scan(&record.ID)

	if err != nil {
		return fmt.Errorf("保存 PostgreSQL 记录失败: %w", err)
	}

	return nil
}

// GetLatestBatch 批量获取每个监测项的最新记录
//
// 实现说明：
// - 使用 CTE(keys) 承载入参列表，避免拼接 IN (...) 的多列比较复杂度
// - 使用 DISTINCT ON + ORDER BY timestamp DESC 取每个 (provider,service,channel) 的最新一条
func (s *PostgresStorage) GetLatestBatch(keys []MonitorKey) (map[MonitorKey]*ProbeRecord, error) {
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
		base := i*4 + 1
		fmt.Fprintf(&b, "($%d,$%d,$%d,$%d)", base, base+1, base+2, base+3)
		args = append(args, k.Provider, k.Service, k.Channel, k.Model)
	}
	b.WriteString(")\n")
	b.WriteString(`
SELECT DISTINCT ON (p.provider, p.service, p.channel, p.model)
	p.id, p.provider, p.service, p.channel, p.model, p.status, p.sub_status, p.http_code, p.latency, p.timestamp
FROM probe_history p
JOIN keys k
	ON p.provider = k.provider AND p.service = k.service AND p.channel = k.channel AND p.model = k.model
ORDER BY p.provider, p.service, p.channel, p.model, p.timestamp DESC, p.id DESC
`)

	rows, err := s.pool.Query(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("批量查询 PostgreSQL 最新记录失败: %w", err)
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
			return nil, fmt.Errorf("扫描 PostgreSQL 最新记录失败: %w", err)
		}
		rec.SubStatus = SubStatus(subStatusStr)
		result[MonitorKey{Provider: rec.Provider, Service: rec.Service, Channel: rec.Channel, Model: rec.Model}] = rec
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代 PostgreSQL 最新记录失败: %w", err)
	}

	return result, nil
}

// GetHistoryBatch 批量获取多个监测项的历史记录（时间范围）
//
// 实现说明：
// - 使用 CTE(keys) + JOIN 过滤目标监测项集合
// - ORDER BY 按 (provider,service,channel,timestamp DESC) 输出，便于按 key 聚合且尽量利用索引顺序
// - 最终对每个 key 的切片做 reverse，保证返回时间升序（与 GetHistory 一致）
func (s *PostgresStorage) GetHistoryBatch(keys []MonitorKey, since time.Time) (map[MonitorKey][]*ProbeRecord, error) {
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
		base := i*4 + 1
		fmt.Fprintf(&b, "($%d,$%d,$%d,$%d)", base, base+1, base+2, base+3)
		args = append(args, k.Provider, k.Service, k.Channel, k.Model)
	}

	sinceArgIndex := len(keys)*4 + 1
	args = append(args, since.Unix())

	b.WriteString(")\n")
	fmt.Fprintf(&b, `
SELECT
	p.id, p.provider, p.service, p.channel, p.model, p.status, p.sub_status, p.http_code, p.latency, p.timestamp
FROM probe_history p
JOIN keys k
	ON p.provider = k.provider AND p.service = k.service AND p.channel = k.channel AND p.model = k.model
WHERE p.timestamp >= $%d
ORDER BY p.provider, p.service, p.channel, p.model, p.timestamp DESC
`, sinceArgIndex)

	rows, err := s.pool.Query(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("批量查询 PostgreSQL 历史记录失败: %w", err)
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
			return nil, fmt.Errorf("扫描 PostgreSQL 历史记录失败: %w", err)
		}
		rec.SubStatus = SubStatus(subStatusStr)
		key := MonitorKey{Provider: rec.Provider, Service: rec.Service, Channel: rec.Channel, Model: rec.Model}
		result[key] = append(result[key], rec)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代 PostgreSQL 历史记录失败: %w", err)
	}

	// DESC 取数利用索引，返回前对每个 key 翻转为时间升序
	for k := range result {
		reverseRecords(result[k])
	}

	return result, nil
}

// GetTimelineAggBatch 批量获取多个监测项的时间轴 bucket 聚合结果（时间范围）
//
// 设计目标：
// - 将 7d/30d 场景的聚合下推到 PostgreSQL，避免应用层拉取百万级原始记录
// - 输出语义与 api.buildTimeline 完全一致（bucket 归属、边界排除、时段过滤、统计口径）
func (s *PostgresStorage) GetTimelineAggBatch(keys []MonitorKey, since, endTime time.Time, bucketCount int, bucketWindow time.Duration, timeFilter *DailyTimeFilter) (map[MonitorKey][]AggBucketRow, error) {
	ctx := s.effectiveCtx()
	result := make(map[MonitorKey][]AggBucketRow, len(keys))
	if len(keys) == 0 {
		return result, nil
	}
	if bucketCount <= 0 {
		return result, nil
	}

	windowSec := int64(bucketWindow / time.Second)
	if windowSec <= 0 {
		return nil, fmt.Errorf("无效的 bucketWindow: %s", bucketWindow)
	}

	sinceUnix := since.Unix()
	endUnix := endTime.Unix()

	var b strings.Builder
	args := make([]any, 0, len(keys)*4+8)

	// keys CTE
	b.WriteString("WITH keys(provider, service, channel, model) AS (VALUES ")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		base := i*4 + 1
		fmt.Fprintf(&b, "($%d,$%d,$%d,$%d)", base, base+1, base+2, base+3)
		args = append(args, k.Provider, k.Service, k.Channel, k.Model)
	}
	b.WriteString(")\n")

	// 额外参数（紧跟 keys 之后）
	sinceArg := len(args) + 1
	args = append(args, sinceUnix)
	endArg := len(args) + 1
	args = append(args, endUnix)
	windowSecArg := len(args) + 1
	args = append(args, windowSec)
	bucketCountArg := len(args) + 1
	args = append(args, bucketCount)

	// 时段过滤条件（UTC，左闭右开）
	timeFilterCond := ""
	if timeFilter != nil {
		startMinArg := len(args) + 1
		args = append(args, timeFilter.StartMinutes)
		endMinArg := len(args) + 1
		args = append(args, timeFilter.EndMinutes)

		minutesExpr := "(EXTRACT(HOUR FROM timezone('UTC', to_timestamp(p.timestamp)))::int * 60 + EXTRACT(MINUTE FROM timezone('UTC', to_timestamp(p.timestamp)))::int)"
		if timeFilter.CrossMidnight {
			// 跨午夜： [start, 24:00) ∪ [00:00, end)
			timeFilterCond = fmt.Sprintf(" AND (%s >= $%d OR %s < $%d)", minutesExpr, startMinArg, minutesExpr, endMinArg)
		} else {
			// 正常： [start, end)
			timeFilterCond = fmt.Sprintf(" AND (%s >= $%d AND %s < $%d)", minutesExpr, startMinArg, minutesExpr, endMinArg)
		}
	}

	// filtered：先计算 bucket_idx，再做聚合
	//
	// bucket_idx 的计算需与 api.buildTimeline 完全一致：
	//   timeDiffSec := endUnix - ts
	//   bucketIndexFromLast := timeDiffSec / windowSec
	//   bucket_idx := bucketCount - 1 - bucketIndexFromLast
	//
	// 边界一致性：
	// - 排除 timestamp<=since 的边界数据（buildTimeline 会跳过 timeDiff>=bucketCount*window）
	// - 排除 timestamp>endTime 的数据（对齐模式下未来窗口）
	fmt.Fprintf(&b, `
, filtered AS (
	SELECT
		p.id,
		p.provider,
		p.service,
		p.channel,
		p.model,
		p.status,
		p.sub_status,
		p.http_code,
		p.latency,
		p.timestamp,
		($%d::int - 1 - (($%d::bigint - p.timestamp) / $%d::bigint))::int AS bucket_idx
	FROM probe_history p
	JOIN keys k
		ON p.provider = k.provider AND p.service = k.service AND p.channel = k.channel AND p.model = k.model
	WHERE p.timestamp > $%d
	  AND p.timestamp <= $%d
	  AND (($%d::bigint - p.timestamp) / $%d::bigint) < $%d::bigint
%s
)
`, bucketCountArg, endArg, windowSecArg, sinceArg, endArg, endArg, windowSecArg, bucketCountArg, timeFilterCond)

	// http_code_breakdown：仅统计红色(status==0)且有有效 http_code 的记录
	// sub_status 范围需与 api.incrementStatusCount 完全一致
	b.WriteString(`
, http_code_counts AS (
	SELECT
		provider, service, channel, model, bucket_idx, sub_status, http_code, COUNT(*)::int AS cnt
	FROM filtered
	WHERE status = 0
	  AND http_code > 0
	  AND sub_status IN ('server_error','client_error','auth_error','invalid_request','rate_limit')
	GROUP BY provider, service, channel, model, bucket_idx, sub_status, http_code
)
, http_code_sub_agg AS (
	SELECT
		provider, service, channel, model, bucket_idx, sub_status,
		jsonb_object_agg(http_code::text, cnt) AS codes
	FROM http_code_counts
	GROUP BY provider, service, channel, model, bucket_idx, sub_status
)
, http_code_bucket_agg AS (
	SELECT
		provider, service, channel, model, bucket_idx,
		COALESCE(jsonb_object_agg(sub_status, codes), '{}'::jsonb) AS breakdown
	FROM http_code_sub_agg
	GROUP BY provider, service, channel, model, bucket_idx
)
SELECT
	f.provider,
	f.service,
	f.channel,
	f.model,
	f.bucket_idx,
	COUNT(*)::int AS total,
	(ARRAY_AGG(f.status ORDER BY f.timestamp DESC, f.id DESC))[1]::int AS last_status,
	COALESCE(SUM(CASE WHEN f.status > 0 THEN f.latency ELSE 0 END), 0)::bigint AS latency_sum,
	COALESCE(SUM(CASE WHEN f.status > 0 THEN 1 ELSE 0 END), 0)::int AS latency_count,
	COALESCE(SUM(CASE WHEN f.latency > 0 THEN f.latency ELSE 0 END), 0)::bigint AS all_latency_sum,
	COALESCE(SUM(CASE WHEN f.latency > 0 THEN 1 ELSE 0 END), 0)::int AS all_latency_count,

	COALESCE(SUM(CASE WHEN f.status = 1 THEN 1 ELSE 0 END), 0)::int AS available,
	COALESCE(SUM(CASE WHEN f.status = 2 THEN 1 ELSE 0 END), 0)::int AS degraded,
	COALESCE(SUM(CASE WHEN f.status = 0 THEN 1 ELSE 0 END), 0)::int AS unavailable,
	COALESCE(SUM(CASE WHEN f.status NOT IN (0,1,2) THEN 1 ELSE 0 END), 0)::int AS missing,

	COALESCE(SUM(CASE WHEN f.status = 2 AND f.sub_status = 'slow_latency' THEN 1 ELSE 0 END), 0)::int AS slow_latency,
	COALESCE(SUM(CASE WHEN f.sub_status = 'rate_limit' AND f.status IN (0,2) THEN 1 ELSE 0 END), 0)::int AS rate_limit,

	COALESCE(SUM(CASE WHEN f.status = 0 AND f.sub_status = 'server_error' THEN 1 ELSE 0 END), 0)::int AS server_error,
	COALESCE(SUM(CASE WHEN f.status = 0 AND f.sub_status = 'client_error' THEN 1 ELSE 0 END), 0)::int AS client_error,
	COALESCE(SUM(CASE WHEN f.status = 0 AND f.sub_status = 'auth_error' THEN 1 ELSE 0 END), 0)::int AS auth_error,
	COALESCE(SUM(CASE WHEN f.status = 0 AND f.sub_status = 'invalid_request' THEN 1 ELSE 0 END), 0)::int AS invalid_request,
	COALESCE(SUM(CASE WHEN f.status = 0 AND f.sub_status = 'network_error' THEN 1 ELSE 0 END), 0)::int AS network_error,
	COALESCE(SUM(CASE WHEN f.status = 0 AND f.sub_status = 'response_timeout' THEN 1 ELSE 0 END), 0)::int AS response_timeout,
	COALESCE(SUM(CASE WHEN f.status = 0 AND f.sub_status = 'content_mismatch' THEN 1 ELSE 0 END), 0)::int AS content_mismatch,

	COALESCE(h.breakdown, '{}'::jsonb) AS http_code_breakdown
FROM filtered f
LEFT JOIN http_code_bucket_agg h
	ON h.provider = f.provider AND h.service = f.service AND h.channel = f.channel AND h.model = f.model AND h.bucket_idx = f.bucket_idx
GROUP BY
	f.provider, f.service, f.channel, f.model, f.bucket_idx, h.breakdown
ORDER BY
	f.provider, f.service, f.channel, f.model, f.bucket_idx
`)

	rows, err := s.pool.Query(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("批量查询 PostgreSQL 时间轴聚合失败: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			provider, service, channel, model string
			bucketIdx                         int
			total                             int
			lastStatus                        int
			latencySum                        int64
			latencyCount                      int
			allLatencySum                     int64
			allLatencyCount                   int

			available       int
			degraded        int
			unavailable     int
			missing         int
			slowLatency     int
			rateLimit       int
			serverError     int
			clientError     int
			authError       int
			invalidRequest  int
			networkError    int
			responseTimeout int
			contentMismatch int

			breakdownRaw []byte
		)

		if err := rows.Scan(
			&provider,
			&service,
			&channel,
			&model,
			&bucketIdx,
			&total,
			&lastStatus,
			&latencySum,
			&latencyCount,
			&allLatencySum,
			&allLatencyCount,
			&available,
			&degraded,
			&unavailable,
			&missing,
			&slowLatency,
			&rateLimit,
			&serverError,
			&clientError,
			&authError,
			&invalidRequest,
			&networkError,
			&responseTimeout,
			&contentMismatch,
			&breakdownRaw,
		); err != nil {
			return nil, fmt.Errorf("扫描 PostgreSQL 时间轴聚合结果失败: %w", err)
		}

		var httpCodeBreakdown map[string]map[int]int
		// breakdownRaw 可能为 "{}" 或其他 jsonb
		if len(breakdownRaw) > 0 && string(breakdownRaw) != "null" {
			if err := json.Unmarshal(breakdownRaw, &httpCodeBreakdown); err != nil {
				return nil, fmt.Errorf("解析 PostgreSQL http_code_breakdown 失败: %w", err)
			}
			// 兼容：如果为空对象，保持空 map（omitempty 会省略）
			if len(httpCodeBreakdown) == 0 {
				httpCodeBreakdown = nil
			}
		}

		key := MonitorKey{Provider: provider, Service: service, Channel: channel, Model: model}
		result[key] = append(result[key], AggBucketRow{
			BucketIndex:     bucketIdx,
			Total:           total,
			LastStatus:      lastStatus,
			LatencySum:      latencySum,
			LatencyCount:    latencyCount,
			AllLatencySum:   allLatencySum,
			AllLatencyCount: allLatencyCount,
			StatusCounts: StatusCounts{
				Available:         available,
				Degraded:          degraded,
				Unavailable:       unavailable,
				Missing:           missing,
				SlowLatency:       slowLatency,
				RateLimit:         rateLimit,
				ServerError:       serverError,
				ClientError:       clientError,
				AuthError:         authError,
				InvalidRequest:    invalidRequest,
				NetworkError:      networkError,
				ResponseTimeout:   responseTimeout,
				ContentMismatch:   contentMismatch,
				HttpCodeBreakdown: httpCodeBreakdown,
			},
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代 PostgreSQL 时间轴聚合结果失败: %w", err)
	}

	return result, nil
}

// GetLatest 获取最新记录
func (s *PostgresStorage) GetLatest(provider, service, channel, model string) (*ProbeRecord, error) {
	ctx := s.effectiveCtx()
	query := `
		SELECT id, provider, service, channel, model, status, sub_status, http_code, latency, timestamp
		FROM probe_history
		WHERE provider = $1 AND service = $2 AND channel = $3 AND model = $4
		ORDER BY timestamp DESC, id DESC
		LIMIT 1
	`

	var record ProbeRecord
	var subStatusStr string
	err := s.pool.QueryRow(ctx, query, provider, service, channel, model).Scan(
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
		// pgx 使用 ErrNoRows 的方式不同，需要检查错误消息
		if err.Error() == "no rows in result set" {
			return nil, nil // 没有记录不算错误
		}
		return nil, fmt.Errorf("查询 PostgreSQL 最新记录失败: %w", err)
	}

	record.SubStatus = SubStatus(subStatusStr)
	return &record, nil
}

// GetHistory 获取历史记录
func (s *PostgresStorage) GetHistory(provider, service, channel, model string, since time.Time) ([]*ProbeRecord, error) {
	ctx := s.effectiveCtx()
	// 使用 ORDER BY timestamp DESC 以利用索引（索引是 timestamp DESC）
	// 返回前在 Go 代码中反转为时间升序
	query := `
		SELECT id, provider, service, channel, model, status, sub_status, http_code, latency, timestamp
		FROM probe_history
		WHERE provider = $1 AND service = $2 AND channel = $3 AND model = $4 AND timestamp >= $5
		ORDER BY timestamp DESC
	`

	rows, err := s.pool.Query(ctx, query, provider, service, channel, model, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("查询 PostgreSQL 历史记录失败: %w", err)
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
			return nil, fmt.Errorf("扫描 PostgreSQL 记录失败: %w", err)
		}
		record.SubStatus = SubStatus(subStatusStr)
		records = append(records, &record)
	}

	// 检查迭代过程中是否发生错误
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代 PostgreSQL 记录失败: %w", err)
	}

	// DESC 取数利用索引，返回前翻转为时间升序
	reverseRecords(records)

	return records, nil
}

// GetLatestByModelID 按 model_id 获取最新一条记录（跨展示名历史连续）。
func (s *PostgresStorage) GetLatestByModelID(modelID string) (*ProbeRecord, error) {
	ctx := s.effectiveCtx()
	query := `
		SELECT id, provider, service, channel, model, status, sub_status, http_code, latency, timestamp
		FROM probe_history
		WHERE model_id = $1
		ORDER BY timestamp DESC, id DESC
		LIMIT 1
	`

	var record ProbeRecord
	var subStatusStr string
	err := s.pool.QueryRow(ctx, query, modelID).Scan(
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
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("按 model_id 查询 PostgreSQL 最新记录失败: %w", err)
	}

	record.SubStatus = SubStatus(subStatusStr)
	record.ModelID = modelID
	return &record, nil
}

// GetHistoryByModelID 按 model_id 获取 since 之后的历史记录（时间升序，跨展示名历史连续）。
func (s *PostgresStorage) GetHistoryByModelID(modelID string, since time.Time) ([]*ProbeRecord, error) {
	ctx := s.effectiveCtx()
	query := `
		SELECT id, provider, service, channel, model, status, sub_status, http_code, latency, timestamp
		FROM probe_history
		WHERE model_id = $1 AND timestamp >= $2
		ORDER BY timestamp DESC
	`

	rows, err := s.pool.Query(ctx, query, modelID, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("按 model_id 查询 PostgreSQL 历史记录失败: %w", err)
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
			return nil, fmt.Errorf("扫描 model_id PostgreSQL 历史记录失败: %w", err)
		}
		record.SubStatus = SubStatus(subStatusStr)
		record.ModelID = modelID
		records = append(records, &record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代 model_id PostgreSQL 历史记录失败: %w", err)
	}

	reverseRecords(records)
	return records, nil
}

// GetHistoryWithLimitByModelID 按 model_id 获取 since 之后最近 limit 条记录（timestamp DESC + id DESC）。
// 返回值保留时间倒序（最新在前）；包含 ErrorDetail 字段。
func (s *PostgresStorage) GetHistoryWithLimitByModelID(modelID string, since time.Time, limit int) ([]*ProbeRecord, error) {
	ctx := s.effectiveCtx()
	if limit <= 0 {
		limit = 200
	}

	query := `
		SELECT id, provider, service, channel, model, status, sub_status, http_code, latency, timestamp, error_detail
		FROM probe_history
		WHERE model_id = $1 AND timestamp >= $2
		ORDER BY timestamp DESC, id DESC
		LIMIT $3
	`

	rows, err := s.pool.Query(ctx, query, modelID, since.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("按 model_id 查询 PostgreSQL 限量历史记录失败: %w", err)
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
			return nil, fmt.Errorf("扫描 model_id PostgreSQL 限量历史记录失败: %w", err)
		}
		record.SubStatus = SubStatus(subStatusStr)
		record.ModelID = modelID
		records = append(records, &record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代 model_id PostgreSQL 限量历史记录失败: %w", err)
	}

	return records, nil
}

// GetHistoryWithLimit 获取指定 PSCM 在 since 之后的最近 limit 条记录，按 timestamp DESC + id DESC 返回。
// 返回值保留时间倒序（最新在前），适合直接喂给管理后台日志面板；调用方需要升序时自行 reverseRecords。
// 与 GetHistory 不同，此方法返回 ErrorDetail 字段（error_detail 不在覆盖索引 INCLUDE 列内，limit ≤ 1000 时回表成本可接受）。
func (s *PostgresStorage) GetHistoryWithLimit(provider, service, channel, model string, since time.Time, limit int) ([]*ProbeRecord, error) {
	ctx := s.effectiveCtx()
	if limit <= 0 {
		limit = 200
	}

	// 复合排序键 (timestamp DESC, id DESC) 利用 idx_probe_history_pscm_ts_cover，
	// 同一秒内多条记录按 id 倒序，保证分页结果稳定。
	query := `
		SELECT id, provider, service, channel, model, status, sub_status, http_code, latency, timestamp, error_detail
		FROM probe_history
		WHERE provider = $1 AND service = $2 AND channel = $3 AND model = $4 AND timestamp >= $5
		ORDER BY timestamp DESC, id DESC
		LIMIT $6
	`

	rows, err := s.pool.Query(ctx, query, provider, service, channel, model, since.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("查询 PostgreSQL 历史记录失败: %w", err)
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
			return nil, fmt.Errorf("扫描 PostgreSQL 记录失败: %w", err)
		}
		record.SubStatus = SubStatus(subStatusStr)
		records = append(records, &record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代 PostgreSQL 记录失败: %w", err)
	}

	return records, nil
}

// ===== 状态订阅通知（事件）相关方法 =====

// initEventTables 初始化事件相关表
func (s *PostgresStorage) initEventTables(ctx context.Context) error {
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
		last_record_id BIGINT,
		last_timestamp BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (provider, service, channel, model)
	);
	`
	if _, err := s.pool.Exec(ctx, serviceStatesSchema); err != nil {
		return fmt.Errorf("创建 service_states 表失败 (PostgreSQL): %w", err)
	}

	// 状态事件表
	statusEventsSchema := `
	CREATE TABLE IF NOT EXISTS status_events (
		id BIGSERIAL PRIMARY KEY,
		provider TEXT NOT NULL,
		service TEXT NOT NULL,
		channel TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		event_type TEXT NOT NULL,
		from_status INTEGER NOT NULL,
		to_status INTEGER NOT NULL,
		trigger_record_id BIGINT NOT NULL,
		observed_at BIGINT NOT NULL,
		created_at BIGINT NOT NULL,
		meta JSONB
	);
	`
	if _, err := s.pool.Exec(ctx, statusEventsSchema); err != nil {
		return fmt.Errorf("创建 status_events 表失败 (PostgreSQL): %w", err)
	}

	// 兼容旧数据库：事件表补齐 model 列（旧数据默认 model=''）
	if err := s.ensureServiceStatesModelColumn(); err != nil {
		return err
	}
	if err := s.ensureStatusEventsModelColumn(); err != nil {
		return err
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
		last_record_id BIGINT,
		last_timestamp BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (provider, service, channel)
	);
	`
	if _, err := s.pool.Exec(ctx, channelStatesSchema); err != nil {
		return fmt.Errorf("创建 channel_states 表失败 (PostgreSQL): %w", err)
	}

	// 创建索引
	eventsIndexSQL := `
	CREATE INDEX IF NOT EXISTS idx_status_events_psc_id
	ON status_events(provider, service, channel, id);
	`
	if _, err := s.pool.Exec(ctx, eventsIndexSQL); err != nil {
		return fmt.Errorf("创建 status_events 索引失败 (PostgreSQL): %w", err)
	}

	// 创建唯一约束索引（幂等性保障）
	uniqueIndexSQL := `
	CREATE UNIQUE INDEX IF NOT EXISTS idx_status_events_unique
	ON status_events(provider, service, channel, event_type, trigger_record_id);
	`
	if _, err := s.pool.Exec(ctx, uniqueIndexSQL); err != nil {
		return fmt.Errorf("创建 status_events 唯一索引失败 (PostgreSQL): %w", err)
	}

	return nil
}

func (s *PostgresStorage) ensureServiceStatesModelColumn() error {
	ctx := s.effectiveCtx()

	// 检查 model 列是否存在
	checkColumnQuery := `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = current_schema()
			AND table_name = 'service_states'
			AND column_name = 'model'
	`
	var columnCount int
	if err := s.pool.QueryRow(ctx, checkColumnQuery).Scan(&columnCount); err != nil {
		return fmt.Errorf("查询 PostgreSQL 表结构失败: %w", err)
	}

	// 检查 model 是否在主键中
	checkPKQuery := `
		SELECT COUNT(*)
		FROM information_schema.key_column_usage kcu
		JOIN information_schema.table_constraints tc
			ON kcu.constraint_name = tc.constraint_name
			AND kcu.table_schema = tc.table_schema
		WHERE tc.constraint_type = 'PRIMARY KEY'
			AND tc.table_schema = current_schema()
			AND kcu.table_name = 'service_states'
			AND kcu.column_name = 'model'
	`
	var pkCount int
	if err := s.pool.QueryRow(ctx, checkPKQuery).Scan(&pkCount); err != nil {
		return fmt.Errorf("查询 PostgreSQL 主键结构失败: %w", err)
	}

	// 如果 model 列存在且在主键中，无需迁移
	if columnCount > 0 && pkCount > 0 {
		return nil
	}

	// 需要重建主键（PostgreSQL 支持 ALTER PRIMARY KEY）
	logger.Info("storage", "正在迁移 service_states 表以添加 model 到主键 (PostgreSQL)...")

	// PostgreSQL 可以直接修改主键，但需要先删除旧主键再添加新主键
	// 使用事务确保原子性
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开始事务失败: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. 如果 model 列不存在，先添加
	if columnCount == 0 {
		if _, err := tx.Exec(ctx, `ALTER TABLE service_states ADD COLUMN model TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("添加 service_states.model 列失败: %w", err)
		}
	} else {
		// model 列存在但可能有 NULL 值或不是 NOT NULL，需要清洗数据
		// 将 NULL 值转换为空字符串
		if _, err := tx.Exec(ctx, `UPDATE service_states SET model = '' WHERE model IS NULL`); err != nil {
			return fmt.Errorf("清洗 model 列 NULL 值失败: %w", err)
		}
		// 设置默认值和 NOT NULL 约束
		if _, err := tx.Exec(ctx, `ALTER TABLE service_states ALTER COLUMN model SET DEFAULT ''`); err != nil {
			return fmt.Errorf("设置 model 列默认值失败: %w", err)
		}
		if _, err := tx.Exec(ctx, `ALTER TABLE service_states ALTER COLUMN model SET NOT NULL`); err != nil {
			return fmt.Errorf("设置 model 列 NOT NULL 失败: %w", err)
		}
	}

	// 2. 查询旧主键约束名称
	var constraintName string
	pkNameQuery := `
		SELECT tc.constraint_name
		FROM information_schema.table_constraints tc
		WHERE tc.table_schema = current_schema()
			AND tc.table_name = 'service_states'
			AND tc.constraint_type = 'PRIMARY KEY'
	`
	if err := tx.QueryRow(ctx, pkNameQuery).Scan(&constraintName); err != nil {
		return fmt.Errorf("查询主键约束名称失败: %w", err)
	}

	// 3. 删除旧主键（使用引号保护约束名）
	dropPKSQL := fmt.Sprintf(`ALTER TABLE service_states DROP CONSTRAINT "%s"`, constraintName)
	if _, err := tx.Exec(ctx, dropPKSQL); err != nil {
		return fmt.Errorf("删除旧主键失败: %w", err)
	}

	// 4. 添加新主键（包含 model）
	addPKSQL := `ALTER TABLE service_states ADD PRIMARY KEY (provider, service, channel, model)`
	if _, err := tx.Exec(ctx, addPKSQL); err != nil {
		return fmt.Errorf("添加新主键失败: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}

	logger.Info("storage", "已完成 service_states 表迁移（model 列已加入主键）(PostgreSQL)")
	return nil
}

func (s *PostgresStorage) ensureStatusEventsModelColumn() error {
	ctx := s.effectiveCtx()
	checkQuery := `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = current_schema()
			AND table_name = 'status_events'
			AND column_name = 'model'
	`

	var count int
	err := s.pool.QueryRow(ctx, checkQuery).Scan(&count)
	if err != nil {
		return fmt.Errorf("查询 PostgreSQL 表结构失败: %w", err)
	}

	if count > 0 {
		return nil
	}

	alterQuery := `ALTER TABLE status_events ADD COLUMN model TEXT NOT NULL DEFAULT ''`
	if _, err := s.pool.Exec(ctx, alterQuery); err != nil {
		return fmt.Errorf("添加 status_events.model 列失败: %w", err)
	}

	logger.Info("storage", "已为 status_events 表添加 model 列 (PostgreSQL)")
	return nil
}

// GetServiceState 获取服务状态机持久化状态
func (s *PostgresStorage) GetServiceState(provider, service, channel, model string) (*ServiceState, error) {
	ctx := s.effectiveCtx()
	query := `
		SELECT provider, service, channel, model, stable_available, streak_count, streak_status, last_record_id, last_timestamp
		FROM service_states
		WHERE provider = $1 AND service = $2 AND channel = $3 AND model = $4
	`

	var state ServiceState
	var lastRecordID *int64

	err := s.pool.QueryRow(ctx, query, provider, service, channel, model).Scan(
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
		if err.Error() == "no rows in result set" {
			return nil, nil // 尚未初始化
		}
		return nil, fmt.Errorf("查询服务状态失败 (PostgreSQL): %w", err)
	}

	if lastRecordID != nil {
		state.LastRecordID = *lastRecordID
	}

	return &state, nil
}

// UpsertServiceState 写入或更新服务状态机持久化状态
func (s *PostgresStorage) UpsertServiceState(state *ServiceState) error {
	ctx := s.effectiveCtx()
	query := `
		INSERT INTO service_states (provider, service, channel, model, stable_available, streak_count, streak_status, last_record_id, last_timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT(provider, service, channel, model) DO UPDATE SET
			stable_available = EXCLUDED.stable_available,
			streak_count = EXCLUDED.streak_count,
			streak_status = EXCLUDED.streak_status,
			last_record_id = EXCLUDED.last_record_id,
			last_timestamp = EXCLUDED.last_timestamp
	`

	_, err := s.pool.Exec(ctx, query,
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
		return fmt.Errorf("更新服务状态失败 (PostgreSQL): %w", err)
	}

	return nil
}

// SaveStatusEvent 保存状态变更事件
func (s *PostgresStorage) SaveStatusEvent(event *StatusEvent) error {
	ctx := s.effectiveCtx()

	query := `
		INSERT INTO status_events (provider, service, channel, model, event_type, from_status, to_status, trigger_record_id, observed_at, created_at, meta)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (provider, service, channel, event_type, trigger_record_id) DO NOTHING
		RETURNING id
	`

	err := s.pool.QueryRow(ctx, query,
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
		event.Meta,
	).Scan(&event.ID)

	if err != nil {
		// ON CONFLICT DO NOTHING 不返回行，这是幂等处理
		if err.Error() == "no rows in result set" {
			return nil // 重复事件，视为成功
		}
		return fmt.Errorf("保存状态事件失败 (PostgreSQL): %w", err)
	}

	return nil
}

// GetStatusEvents 查询状态变更事件列表
func (s *PostgresStorage) GetStatusEvents(sinceID int64, limit int, filters *EventFilters) ([]*StatusEvent, error) {
	ctx := s.effectiveCtx()

	var conditions []string
	var args []any
	argIndex := 1

	// 游标条件
	conditions = append(conditions, fmt.Sprintf("id > $%d", argIndex))
	args = append(args, sinceID)
	argIndex++

	// 可选过滤条件
	if filters != nil {
		if filters.Provider != "" {
			conditions = append(conditions, fmt.Sprintf("provider = $%d", argIndex))
			args = append(args, filters.Provider)
			argIndex++
		}
		if filters.Service != "" {
			conditions = append(conditions, fmt.Sprintf("service = $%d", argIndex))
			args = append(args, filters.Service)
			argIndex++
		}
		if filters.Channel != "" {
			conditions = append(conditions, fmt.Sprintf("channel = $%d", argIndex))
			args = append(args, filters.Channel)
			argIndex++
		}
		if len(filters.Types) > 0 {
			placeholders := make([]string, len(filters.Types))
			for i, t := range filters.Types {
				placeholders[i] = fmt.Sprintf("$%d", argIndex)
				args = append(args, string(t))
				argIndex++
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
		LIMIT $%d
	`, strings.Join(conditions, " AND "), argIndex)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询状态事件失败 (PostgreSQL): %w", err)
	}
	defer rows.Close()

	var events []*StatusEvent
	for rows.Next() {
		var event StatusEvent
		var eventTypeStr string
		var meta map[string]any

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
			&meta,
		)
		if err != nil {
			return nil, fmt.Errorf("扫描状态事件失败 (PostgreSQL): %w", err)
		}

		event.EventType = EventType(eventTypeStr)
		event.Meta = meta

		events = append(events, &event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代状态事件失败 (PostgreSQL): %w", err)
	}

	return events, nil
}

// GetLatestEventID 获取最新事件 ID
func (s *PostgresStorage) GetLatestEventID() (int64, error) {
	ctx := s.effectiveCtx()
	query := `SELECT COALESCE(MAX(id), 0) FROM status_events`

	var latestID int64
	err := s.pool.QueryRow(ctx, query).Scan(&latestID)
	if err != nil {
		return 0, fmt.Errorf("查询最新事件 ID 失败 (PostgreSQL): %w", err)
	}

	return latestID, nil
}

// GetChannelState 获取通道级状态机持久化状态
func (s *PostgresStorage) GetChannelState(provider, service, channel string) (*ChannelState, error) {
	ctx := s.effectiveCtx()
	query := `
		SELECT provider, service, channel, stable_available, down_count, known_count, last_record_id, last_timestamp
		FROM channel_states
		WHERE provider = $1 AND service = $2 AND channel = $3
	`

	var state ChannelState
	var lastRecordID *int64

	err := s.pool.QueryRow(ctx, query, provider, service, channel).Scan(
		&state.Provider,
		&state.Service,
		&state.Channel,
		&state.StableAvailable,
		&state.DownCount,
		&state.KnownCount,
		&lastRecordID,
		&state.LastTimestamp,
	)

	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil // 尚未初始化
		}
		return nil, fmt.Errorf("查询通道状态失败 (PostgreSQL): %w", err)
	}

	if lastRecordID != nil {
		state.LastRecordID = *lastRecordID
	}

	return &state, nil
}

// UpsertChannelState 写入或更新通道级状态机持久化状态
func (s *PostgresStorage) UpsertChannelState(state *ChannelState) error {
	ctx := s.effectiveCtx()
	query := `
		INSERT INTO channel_states (provider, service, channel, stable_available, down_count, known_count, last_record_id, last_timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT(provider, service, channel) DO UPDATE SET
			stable_available = EXCLUDED.stable_available,
			down_count = EXCLUDED.down_count,
			known_count = EXCLUDED.known_count,
			last_record_id = EXCLUDED.last_record_id,
			last_timestamp = EXCLUDED.last_timestamp
	`

	_, err := s.pool.Exec(ctx, query,
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
		return fmt.Errorf("更新通道状态失败 (PostgreSQL): %w", err)
	}

	return nil
}

// GetModelStatesForChannel 获取通道下所有模型的状态
func (s *PostgresStorage) GetModelStatesForChannel(provider, service, channel string) ([]*ServiceState, error) {
	ctx := s.effectiveCtx()
	query := `
		SELECT provider, service, channel, model, stable_available, streak_count, streak_status, last_record_id, last_timestamp
		FROM service_states
		WHERE provider = $1 AND service = $2 AND channel = $3
		ORDER BY model
	`

	rows, err := s.pool.Query(ctx, query, provider, service, channel)
	if err != nil {
		return nil, fmt.Errorf("查询通道模型状态失败 (PostgreSQL): %w", err)
	}
	defer rows.Close()

	var states []*ServiceState
	for rows.Next() {
		var state ServiceState
		var lastRecordID *int64

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
			return nil, fmt.Errorf("扫描模型状态失败 (PostgreSQL): %w", err)
		}

		if lastRecordID != nil {
			state.LastRecordID = *lastRecordID
		}

		states = append(states, &state)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代模型状态失败 (PostgreSQL): %w", err)
	}

	return states, nil
}

// ===== 历史数据清理相关方法 =====

// cleanupLockID PostgreSQL advisory lock ID，用于多实例互斥
// 使用固定值确保所有实例共享同一把锁
const cleanupLockID = 12345

// archiveLockNamespace PostgreSQL advisory lock 命名空间，用于归档任务多实例互斥
// 使用两参数版本 pg_try_advisory_lock(namespace, key)，其中 key 为日期
// 注意：必须与清理任务使用的 lock key 区分，避免互相干扰
const archiveLockNamespace int32 = 23456

// ErrArchiveAdvisoryLockNotAcquired 表示未获取归档 advisory lock（其他实例正在归档）
var ErrArchiveAdvisoryLockNotAcquired = errors.New("未获取归档 advisory lock")

// PurgeOldRecords 清理指定时间之前的历史记录
// PostgreSQL 实现：使用 advisory lock 确保多实例互斥
func (s *PostgresStorage) PurgeOldRecords(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	cutoff := before.Unix()

	// 获取单独连接确保 lock 和 delete 在同一连接
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("获取连接失败: %w", err)
	}
	defer conn.Release()

	// 尝试获取 advisory lock（非阻塞）
	var acquired bool
	err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", cleanupLockID).Scan(&acquired)
	if err != nil {
		return 0, fmt.Errorf("获取 advisory lock 失败: %w", err)
	}

	if !acquired {
		// 其他实例正在清理，跳过本轮
		logger.Info("cleaner", "其他实例正在清理，跳过本轮")
		return 0, nil
	}

	// 确保释放锁（使用独立 ctx，避免传入 ctx 取消导致解锁失败）
	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer unlockCancel()
		_, unlockErr := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", cleanupLockID)
		if unlockErr != nil {
			logger.Warn("cleaner", "释放 advisory lock 失败", "error", unlockErr)
		}
	}()

	// 使用 CTE 批量删除
	query := `
		WITH d AS (
			SELECT id FROM probe_history
			WHERE timestamp < $1
			ORDER BY timestamp
			LIMIT $2
		)
		DELETE FROM probe_history
		WHERE id IN (SELECT id FROM d)
	`

	result, err := conn.Exec(ctx, query, cutoff, batchSize)
	if err != nil {
		return 0, fmt.Errorf("删除历史记录失败: %w", err)
	}

	return result.RowsAffected(), nil
}

// ExportDayToWriter 导出指定日期范围的历史记录到 writer
// PostgreSQL 实现：使用 COPY 协议高效导出 CSV
// 多实例互斥：按"日期(UTC)"加锁，避免多实例同时归档同一天数据
func (s *PostgresStorage) ExportDayToWriter(ctx context.Context, dayStart, dayEnd int64, w io.Writer) (int64, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("获取连接失败: %w", err)
	}
	defer conn.Release()

	// 归档多实例互斥：按"日期(UTC)"加锁，避免多实例同时归档同一天数据
	day := time.Unix(dayStart, 0).UTC()
	dayKey := int32(day.Year()*10000 + int(day.Month())*100 + day.Day())

	var acquired bool
	err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1, $2)", archiveLockNamespace, dayKey).Scan(&acquired)
	if err != nil {
		return 0, fmt.Errorf("获取归档 advisory lock 失败: %w", err)
	}
	if !acquired {
		return 0, ErrArchiveAdvisoryLockNotAcquired
	}
	// 确保释放锁（使用独立 ctx，避免传入 ctx 取消导致解锁失败）
	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer unlockCancel()
		_, unlockErr := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1, $2)", archiveLockNamespace, dayKey)
		if unlockErr != nil {
			logger.Warn("archiver", "释放 advisory lock 失败", "error", unlockErr, "date", day.Format("2006-01-02"))
		}
	}()

	// 使用 COPY TO STDOUT 导出 CSV
	query := fmt.Sprintf(`
		COPY (
			SELECT id, provider, service, channel, model, status, sub_status, http_code, latency, timestamp
			FROM probe_history
			WHERE timestamp >= %d AND timestamp < %d
			ORDER BY timestamp
		) TO STDOUT WITH (FORMAT CSV, HEADER)
	`, dayStart, dayEnd)

	// pgx 使用 CopyTo 方法
	tag, err := conn.Conn().PgConn().CopyTo(ctx, w, query)
	if err != nil {
		return 0, fmt.Errorf("导出数据失败: %w", err)
	}

	return tag.RowsAffected(), nil
}

// ===== 自动移板 override 持久化 =====

func (s *PostgresStorage) initOverrideTables(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS monitor_overrides (
		provider      TEXT NOT NULL,
		service       TEXT NOT NULL,
		channel       TEXT NOT NULL DEFAULT '',
		model         TEXT NOT NULL DEFAULT '',
		board         TEXT NOT NULL DEFAULT '',
		cold_reason   TEXT NOT NULL DEFAULT '',
		sponsor_level TEXT NOT NULL DEFAULT '',
		created_at    BIGINT NOT NULL,
		updated_at    BIGINT NOT NULL,
		PRIMARY KEY (provider, service, channel, model)
	);
	`
	if _, err := s.pool.Exec(ctx, schema); err != nil {
		return fmt.Errorf("创建 monitor_overrides 表失败 (PostgreSQL): %w", err)
	}
	return nil
}

// ListMonitorOverrides 加载全部 override 快照。
func (s *PostgresStorage) ListMonitorOverrides() ([]MonitorOverrideRecord, error) {
	ctx := s.effectiveCtx()
	rows, err := s.pool.Query(ctx, `
		SELECT provider, service, channel, model, board, cold_reason, sponsor_level, created_at, updated_at
		FROM monitor_overrides
		ORDER BY provider, service, channel, model
	`)
	if err != nil {
		return nil, fmt.Errorf("查询 monitor_overrides 失败 (PostgreSQL): %w", err)
	}
	defer rows.Close()

	var records []MonitorOverrideRecord
	for rows.Next() {
		var r MonitorOverrideRecord
		if err := rows.Scan(
			&r.Key.Provider, &r.Key.Service, &r.Key.Channel, &r.Key.Model,
			&r.Board, &r.ColdReason, &r.SponsorLevel,
			&r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("扫描 monitor_overrides 失败 (PostgreSQL): %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 monitor_overrides 失败 (PostgreSQL): %w", err)
	}
	return records, nil
}

// ReplaceMonitorOverrides 原子替换全部 override 快照。
func (s *PostgresStorage) ReplaceMonitorOverrides(records []MonitorOverrideRecord) error {
	ctx := s.effectiveCtx()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开始 monitor_overrides 事务失败 (PostgreSQL): %w", err)
	}
	defer tx.Rollback(ctx)

	// 保留已有记录的 created_at
	existingCreatedAt := make(map[MonitorKey]int64)
	eRows, err := tx.Query(ctx, `SELECT provider, service, channel, model, created_at FROM monitor_overrides`)
	if err != nil {
		return fmt.Errorf("查询现有 monitor_overrides 失败 (PostgreSQL): %w", err)
	}
	for eRows.Next() {
		var key MonitorKey
		var createdAt int64
		if err := eRows.Scan(&key.Provider, &key.Service, &key.Channel, &key.Model, &createdAt); err != nil {
			eRows.Close()
			return fmt.Errorf("扫描现有 monitor_overrides 失败 (PostgreSQL): %w", err)
		}
		existingCreatedAt[key] = createdAt
	}
	eRows.Close()
	if err := eRows.Err(); err != nil {
		return fmt.Errorf("遍历现有 monitor_overrides 失败 (PostgreSQL): %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM monitor_overrides`); err != nil {
		return fmt.Errorf("清空 monitor_overrides 失败 (PostgreSQL): %w", err)
	}

	if len(records) > 0 {
		const insertSQL = `
			INSERT INTO monitor_overrides (
				provider, service, channel, model, board, cold_reason, sponsor_level, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`
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

			if _, err := tx.Exec(ctx, insertSQL,
				r.Key.Provider, r.Key.Service, r.Key.Channel, r.Key.Model,
				r.Board, r.ColdReason, r.SponsorLevel, createdAt, updatedAt,
			); err != nil {
				return fmt.Errorf("写入 monitor_overrides 失败 (PostgreSQL): %w", err)
			}
		}
	}

	return tx.Commit(ctx)
}
