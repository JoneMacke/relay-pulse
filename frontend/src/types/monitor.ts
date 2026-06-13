/** monitors.d/ 文件元数据 */
export interface MonitorFileMeta {
  source: string;
  revision: number;
  created_at: string;
  updated_at: string;
}

/** 列表页活化用的最新探测快照 */
export interface LatestProbeSnapshot {
  status: number; // 1=绿 2=黄 0=红
  sub_status?: string;
  http_code?: number;
  latency: number; // ms
  timestamp: number; // Unix 秒
  model?: string;
}

/** 监测项摘要（列表用） */
export interface MonitorSummary {
  key: string;
  provider: string;
  service: string;
  channel: string;
  channel_name?: string;
  model_count: number;
  disabled: boolean;
  hidden: boolean;
  board: string;
  category: string;
  template: string;
  source: string;
  revision: number;
  updated_at: string;
  /** 最近探测快照，nil 表示该通道还没探测记录（新建或长期归档） */
  latest_probe?: LatestProbeSnapshot;
}

/** ServiceConfig 的前端子集（详情/编辑用） */
export interface MonitorConfig {
  provider: string;
  provider_name?: string;
  provider_slug?: string;
  provider_url?: string;
  service: string;
  service_name?: string;
  channel: string;
  channel_name?: string;
  model?: string;
  parent?: string;
  template?: string;
  base_url?: string;
  api_key?: string;
  proxy?: string;
  method?: string;
  headers?: Record<string, string>;
  body?: string;
  success_contains?: string;
  category?: string;
  sponsor?: string;
  sponsor_url?: string;
  sponsor_level?: string;
  key_type?: string;
  auto_cold_exempt?: boolean;
  auto_move_exempt?: boolean;
  board?: string;
  cold_reason?: string;
  retry?: number | null;
  retry_base_delay?: string;
  retry_max_delay?: string;
  retry_jitter?: number | null;
  user_id_refresh_minutes?: number;
  disabled?: boolean;
  disabled_reason?: string;
  hidden?: boolean;
  hidden_reason?: string;
  interval?: string;
  slow_latency?: string;
  timeout?: string;
  listed_since?: string;
  expires_at?: string;
  price_min?: number | null;
  price_max?: number | null;
}

/** monitors.d/ 文件完整结构 */
export interface MonitorFile {
  metadata: MonitorFileMeta;
  monitors: MonitorConfig[];
}

/** 某通道文件下的可探测目标（父或子通道）。
 *  model 取自后端 runtime 已解析配置，是探测请求 target_model 的稳定标识；
 *  未热重载时可能为空（前端据此禁用该行测试按钮）。 */
export interface ProbeTarget {
  role: 'parent' | 'child';
  model: string;
  template: string;
  disabled: boolean;
}

/** Admin Monitor API 响应 */
export interface AdminMonitorListResponse {
  monitors: MonitorSummary[];
  total: number;
}

export interface AdminMonitorDetailResponse {
  monitor: MonitorFile;
  probe_targets?: ProbeTarget[];
}

/** 单条探测历史记录（管理后台 logs tab 用） */
export interface ProbeHistoryEntry {
  id: number;
  provider: string;
  service: string;
  channel: string;
  model?: string;
  status: number; // 1=绿 2=黄 0=红
  sub_status: string;
  http_code: number;
  latency: number; // ms
  timestamp: number; // Unix 秒
  error_detail?: string;
}

/** 管理后台日志查询响应 */
export interface AdminMonitorLogsResponse {
  logs: ProbeHistoryEntry[];
  total: number;
}

/** rpdiag 质量分 sparkline 数据。
 *  ranking-export.v5.2 起额外暴露 recent_scores（最近 ≤3 次单 sample 升序），
 *  消费方可叠在 30d / 7d 均值之后构造 5 点 sparkline；旧版 wire 仍走 3 点
 *  (avg_30d → avg_7d → latest) fallback。 */
export interface RpdiagScoreTrend {
  latest?: number | null;
  latest_at?: string | null;
  avg_7d?: number | null;
  avg_30d?: number | null;
  /** v5.2+: 最近 ≤3 个 sample 升序（旧→新）。null/缺失时走 latest fallback。 */
  recent_scores?: number[] | null;
  /** v5.4+: 最近 ≤3 次质量相关 terminal attempt 升序（旧→新）。number=打分样本，
   *  null=hard-fail（画灰点）。存在时 sparkline slot 2/3/4 改用它；缺失走 recent_scores。 */
  recent_attempts?: (number | null)[] | null;
  n_7d: number;
  n_30d: number;
}

/** rpdiag (provider, service, channel) 三元组下某个 model 的质量分。 */
export interface RpdiagModelScore {
  model?: string;
  model_key?: string;
  score?: number | null;
  trend: RpdiagScoreTrend;
  detail_url?: string;
  /** rpdiag 判定该 model 当前处于硬失败故障态：score/trend 已被后端归一化为 0
   *  （sparkline 最右点红、贴底）。仅作信息标记，渲染由归一化后的 trend 驱动。 */
  failed?: boolean;
  /** 故障原因文案（rpdiag 后端动态生成），在质量列 tooltip 内 verbatim 展示。 */
  availability_warning?: string;
}

/** rpdiag 一个 (provider, service, channel) 三元组的聚合质量分。
 *  max_score 取该通道下所有 model 的最高分；列表用户更关心
 *  「该通道能达到的最佳状态」而不是平均，避免被低频/旧模型拖低。
 */
export interface RpdiagScore {
  max_score?: number | null;
  models: RpdiagModelScore[];
  trend: RpdiagScoreTrend;
  channel_url: string;
}

/** /api/rpdiag-scores 响应。
 *  键格式 "provider|service|channel"，三段均为小写并已剥 rpdiag 前缀。 */
export type RpdiagScoresResponse = Record<string, RpdiagScore>;
