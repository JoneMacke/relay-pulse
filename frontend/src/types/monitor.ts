/** monitors.d/ 文件元数据 */
export interface MonitorFileMeta {
  source: string;
  revision: number;
  created_at: string;
  updated_at: string;
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

/** Admin Monitor API 响应 */
export interface AdminMonitorListResponse {
  monitors: MonitorSummary[];
  total: number;
}

export interface AdminMonitorDetailResponse {
  monitor: MonitorFile;
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
