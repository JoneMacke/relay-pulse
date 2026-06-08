# CLAUDE.md

⚠️ 本文档为 AI 助手（如 Claude / ChatGPT）在此代码库中工作的内部指南，**优先由 AI 维护，人类贡献者通常不需要修改本文件**。
如果你是人类开发者，请优先阅读 `README.md` 和 `CONTRIBUTING.md`，只在需要了解更多技术细节时再参考这里的内容。

### 同步检查点
- **最后同步**: 2026-05-13（HEAD=c626642，探测链路统一：InlineProber.ProbeConfig + config.ResolveSingleMonitor + onboarding.BuildServiceConfigFromSubmission 三个 helper 让 onboarding/test、admin/submissions/:id/test、admin/monitors/:key/probe 三处与 scheduler 走字段级一致的 ServiceConfig；新增 GET /api/admin/monitors/:key/logs；InlineProber 每次探测打 probe_id 结构化日志；前端 MonitorDetail 加详情/探测历史 tabs；上一同步 8435c17 cc-haiku-arith-20260506 模板 anthropic-beta 同步 claude-cli/2.1.129 真抓包扩到 8 项）
- 代码是唯一真相源。本文档为架构与模式摘要，字段级细节请查阅引用的源文件。

## 项目概览

这是一个企业级 LLM 服务可用性监测系统，支持配置热更新、SQLite/PostgreSQL 持久化、实时状态追踪，并内建**指数退避重试**、**标签/赞助体系**、**事件通知**、**自助测试**、**自助收录（onboarding）**、**自助变更请求（change requests）**、**管理后台**、**monitors.d/ 目录化通道管理**和**多模型监测（父子通道继承）**等能力。

### 项目文档

- **README.md** - 项目简介、快速开始、本地开发入口（人类入口文档）
- **QUICKSTART.md** - 5 分钟快速部署与常见问题（人类核心文档）
- **docs/user/config.md** - 配置项、环境变量与安全实践（人类核心文档）
- **docs/user/docker.md** - Docker 部署详细指南
- **docs/user/deploy-postgres.md** - PostgreSQL 部署指南
- **docs/user/sponsorship.md** - 赞助权益体系规则（角色、权益、义务、配置）
- **docs/user/methodology.md** - 监测方法论
- **CONTRIBUTING.md** - 贡献流程、代码规范、提交与 PR 约定（人类核心文档）
- **AGENTS.md / CLAUDE.md** - AI 内部协作与技术指南（仅供 AI 使用，不要在回答中主动推荐给人类）
- **docs/developer/** - 开发者文档（版本检查等）
- **archive/** - 历史文档（仅供参考）

**文档策略（供 AI 遵守）**:
- 回答人类用户时，**优先引用上述 4 个核心文档**，避免让用户跳进 `archive/` 中的大量历史内容。
- 如必须引用 `archive/docs/*` 或 `archive/*.md`（例如 Cloudflare 旧部署说明、历史架构笔记），应明确标注为「历史文档，仅供参考，最终以当前 README/配置手册和代码实现为准」。
- 不主动向人类暴露 `AGENTS.md`、本文件等 AI 内部文档，除非用户明确询问「AI 如何在本仓库工作」一类问题。

### 技术栈

- **后端**: Go 1.24+ (Gin, fsnotify, SQLite/PostgreSQL, slog)
- **前端**: React 19, TypeScript, Tailwind CSS v4, Vite
- **通知子模块** (`notifier/`): 独立 Go module，Telegram/QQ Bot (OneBot v11)

## 开发命令

### 首次开发环境设置

```bash
# ⚠️ 首次开发或前端代码更新后必须运行此脚本
./scripts/setup-dev.sh

# 如果前端代码有更新，需要重新构建并复制
./scripts/setup-dev.sh --rebuild-frontend
```

**重要**: Go 的 `embed` 指令不支持符号链接，因此需要将 `frontend/dist` 复制到 `internal/api/frontend/dist`。setup-dev.sh 脚本会自动处理这个问题。

**⚠️ 前端代码修改规则**:
- `internal/api/frontend/` 整个目录被 `.gitignore` 忽略，是从 `frontend/` 复制过来的嵌入目录
- **所有前端源代码修改必须在 `frontend/` 目录进行**，而不是 `internal/api/frontend/`
- 修改后运行 `./scripts/setup-dev.sh --rebuild-frontend` 同步到嵌入目录
- 直接修改 `internal/api/frontend/` 的改动不会被 git 追踪，会在下次构建时丢失

### 后端 (Go)

```bash
# 开发环境 - 使用 Air 热重载（推荐）
make dev
# 或直接使用: air

# 生产环境 - 手动构建运行
go build -o monitor ./cmd/server
./monitor

# 使用自定义配置运行
./monitor path/to/config.yaml

# 运行测试
go test ./...

# 运行测试并生成覆盖率
go test -cover ./...
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 运行特定包的测试
go test ./internal/config/
go test -v ./internal/storage/

# 代码格式化和检查
go fmt ./...
go vet ./...

# 整理依赖
go mod tidy

# 验证单个检测项（调试配置问题）
go run ./cmd/verify/main.go -provider <name> -service <name> [-v]
# 示例: go run ./cmd/verify/main.go -provider AICodeMirror -service cc -v

```

### 前端 (React)

```bash
cd frontend

# 开发服务器
npm run dev

# 生产构建
npm run build

# 代码检查
npm run lint

# 预览生产构建
npm run preview

# 运行测试
npm run test

# 测试监听模式
npm run test:watch
```

### Pre-commit Hooks

```bash
# 安装 pre-commit (一次性设置)
pip install pre-commit
pre-commit install

# 手动运行所有检查
pre-commit run --all-files
```

### CI/CD

```bash
# 本地模拟 CI 检查（提交前运行）
make ci

# CI 流程包含：
# - Go 格式检查 (gofmt)
# - Go 静态分析 (go vet)
# - Go 单元测试 (go test)
# - 前端 lint (npm run lint)
```

**GitHub Actions 工作流**：
- `ci-release.yml` - CI 测试 + semantic-release 自动发版
- `notifier-docker.yml` - Notifier Docker 镜像构建

## 架构与设计模式

### 后端架构

Go 后端遵循**分层架构**，核心包 16 个 + 独立通知子模块：

```
cmd/
├── server/main.go         → 应用入口，依赖注入
├── verify/main.go         → 单项验证 CLI
└── migrate/main.go        → config.yaml → monitors.d/ 迁移工具

internal/
├── config/                → 配置管理（21 源文件 + 7 测试，按职责拆分）
│   ├── app_config.go     → AppConfig 全局设置
│   ├── monitor.go        → ServiceConfig 监测项字段
│   ├── storage_config.go → StorageConfig / RetentionConfig / ArchiveConfig
│   ├── features.go       → EventsConfig / SponsorPinConfig / BoardsConfig / OnboardingConfig / ChangeRequestConfig
│   ├── external.go       → GitHubConfig / AnnouncementsConfig / CacheTTLConfig
│   ├── badges.go         → RiskBadge（旧版兼容）
│   ├── annotation.go     → Annotation / AnnotationFamily / AnnotationRule / AnnotationMatch
│   ├── enums.go          → SponsorLevel
│   ├── parent_inheritance.go → 父子通道配置继承
│   ├── template.go       → 模板加载（templates/*.json → ServiceConfig）
│   ├── monitor_store.go  → monitors.d/ 目录 CRUD（MonitorStore）
│   ├── normalize*.go     → 归一化与默认值填充
│   ├── validate.go       → 校验规则
│   ├── loader.go         → YAML 解析 + .env 加载 + monitors.d/ 合并
│   ├── dotenv.go         → .env 文件支持
│   ├── watcher.go        → fsnotify 热更新（监听 config.yaml + monitors.d/）
│   ├── lifecycle.go      → Clone / ApplyEnvOverrides / ResolveTemplates
│   └── helpers.go        → 工具函数
├── logger/                → 结构化日志（slog）
│   └── logger.go
├── buildinfo/             → 版本/commit/构建元数据注入
│   └── buildinfo.go
├── storage/               → 存储抽象层（7 源文件 + 测试）
│   ├── storage.go        → Storage / TimelineAggStorage / ArchiveStorage 接口
│   ├── factory.go        → Factory: SQLite/PostgreSQL 选择
│   ├── sqlite.go         → SQLite 实现 (modernc.org/sqlite)
│   ├── postgres.go       → PostgreSQL 实现
│   ├── common.go         → 共享工具函数
│   ├── cleaner.go        → Retention 数据清理
│   └── archiver.go       → 每日 CSV/CSV.GZ 归档导出
├── monitor/               → 探测执行
│   ├── client.go         → HTTP 客户端池（含 proxy 支持）
│   └── probe.go          → 健康检查 + 指数退避重试
├── scheduler/             → 任务调度
│   └── scheduler.go      → 周期探测、并发控制、错峰分散
├── events/                → 状态变更检测（4 源文件 + 测试）
│   ├── types.go          → 事件类型定义
│   ├── detector.go       → 模型级状态机（DOWN/UP 阈值）
│   ├── channel_detector.go → 通道级聚合检测
│   └── service.go        → 事件服务编排
├── probe/                 → 内联探测基础设施（5 文件）
│   ├── registry.go       → 模板注册表（cc/cx/gm 测试类型）
│   ├── inline.go         → InlineProber（同步探测 + 并发控制）
│   ├── ssrf.go           → SSRF 防护
│   ├── safe_client.go    → 沙箱化 HTTP 客户端
│   └── limiter.go        → IP 限流
├── automove/              → 自动移板（基于 7 天可用率在 hot/secondary/cold 间切换）
│   ├── availability.go   → 可用率计算
│   └── service.go        → 自动移板服务编排
├── announcements/         → GitHub Discussions 公告（3 文件）
│   ├── fetcher.go        → GraphQL 拉取
│   ├── service.go        → 轮询 + 缓存
│   └── handler.go        → API 处理器
├── onboarding/            → 自助收录（8 文件）
│   ├── service.go        → 收录业务逻辑（提交/审核/发布）
│   ├── store.go          → Store 接口定义
│   ├── store_sql.go      → SQLite 实现
│   ├── store_pgx.go      → PostgreSQL 实现
│   ├── crypto.go         → AES-GCM API Key 加密（内部调用 apikey 包）
│   └── proof.go          → 测试证明签发与验证（内部调用 apikey 包）
├── apikey/                → 共享 API Key 加密/指纹/掩码工具（5 文件）
│   ├── cipher.go         → KeyCipher（AES-256-GCM 加密/解密 + HMAC-SHA256 指纹）
│   ├── proof.go          → ProofIssuer（HMAC-SHA256 签发/验证）
│   ├── mask.go           → Last4() 掩码工具
│   └── *_test.go         → 加密/证明测试
├── change/                → 变更请求（5 文件）
│   ├── types.go          → ChangeRequest 结构体、状态枚举、AuthCandidate
│   ├── index.go          → 运行时 API Key 指纹索引（内存，热更新重建）
│   ├── service.go        → 业务逻辑：Auth / Submit / GetStatus / Admin*
│   ├── store_sql.go      → SQLite 实现
│   └── store_pgx.go      → PostgreSQL 实现
├── identity/              → 用户标识生成（{{USER_ID}} 占位符，从 config/ 迁出）
│   └── userid.go
├── verifier/              → 单项验证 CLI 逻辑
│   └── verifier.go
└── api/                   → HTTP API 层（14 源文件 + 测试）
    ├── server.go         → Gin 服务器、中间件、CORS、安全头
    ├── handler.go        → /api/status 主处理器、缓存、singleflight
    ├── status_query_handler.go → /api/status/query + POST /api/status/batch
    ├── events_handler.go → /api/events 与 /api/events/latest
    ├── onboarding_handler.go → /api/onboarding/* 端点
    ├── change_handler.go → /api/change/* + /api/admin/changes/* 端点
    ├── admin_handler.go  → /api/admin/submissions/* 端点
    ├── monitor_handler.go → /api/admin/monitors/* 端点（monitors.d/ CRUD）
    ├── monitor_groups.go → 多模型分组构建（parent/child 层级）
    ├── meta.go           → SSR meta 标签注入（SEO）
    └── *_test.go         → 多个测试文件

notifier/                  → 独立通知子模块（独立 go.mod）
├── cmd/notifier/main.go  → 通知服务入口
└── internal/
    ├── config/           → 通知专属配置
    ├── poller/           → 事件轮询
    ├── notifier/         → 消息分发编排（含 sender.go 发送器抽象）
    ├── telegram/         → Telegram Bot
    ├── qq/               → QQ Bot (OneBot v11)
    ├── screenshot/       → 截图服务
    ├── validator/        → 订阅验证
    ├── storage/          → 订阅持久化
    └── api/              → Webhook/回调服务
```

**核心设计原则：**
1. **接口 + Factory 模式**: `storage.Storage` 接口 + `storage.Factory` 支持 SQLite/PostgreSQL 切换
2. **并发安全**: 所有共享状态使用 `sync.RWMutex` 或 `sync.Mutex`
3. **热更新**: 配置变更触发回调，无需重启即可更新运行时状态
4. **优雅关闭**: Context 传播确保资源清理
5. **HTTP 客户端池**: `monitor.ClientPool` 复用连接、管理 proxy
6. **结构化日志**: 统一 `logger` 包，支持 request_id 追踪
7. **Parent-child 继承**: 多模型监测通过 `parent` 字段继承公共配置
8. **事件驱动通知**: `events.Detector` 基于阈值状态机生成 UP/DOWN 事件
9. **指数退避重试**: `retry_*` + jitter 统一控制失败重试节奏
10. **功能开关分层**: boards/annotations/events/announcements 可按需启用
11. **自动移板**: `automove.Service` 基于 7 天可用率自动在 hot/secondary/cold 间切换通道（cold 为 sticky，需 `auto_cold_exempt` 手动解除）
12. **探测链路统一**: 三处 inline 测试端点（用户自助 `/api/onboarding/test`、管理员审核 `/api/admin/submissions/:id/test`、监测项管理 `/api/admin/monitors/:key/probe`）都走 `onboarding.BuildServiceConfigFromSubmission`（或 runtime resolved root） + `config.ResolveSingleMonitor`（模板填充 + Duration 派生） + `probe.InlineProber.ProbeConfig`，确保与 `scheduler` 调用的 `monitor.Prober` **字段级一致**（headers/body/method/success_contains/timeout/retry 全覆盖）。模板覆盖编辑不允许在 inline 测试时即时生效（返回 422 `TEMPLATE_CHANGE_REQUIRES_SAVE`），需先保存。每次 inline 探测打 `probe_id` 结构化日志便于跨端追踪。

### 日志系统

项目使用 Go 标准库 `log/slog` 实现统一的结构化日志：

```go
// 基础用法
logger.Info("component", "消息", "key1", value1, "key2", value2)
logger.Warn("component", "警告消息", "error", err)
logger.Error("component", "错误消息", "error", err)

// 带 request_id 的日志（用于 API 请求追踪）
logger.FromContext(ctx, "api").Info("请求处理完成", "status", 200)
```

**日志格式**：
```
time=2024-01-15T10:30:00.000Z level=INFO msg=消息 app=relay-pulse component=api request_id=abc123
```

**Request ID 中间件**：
- API 层自动为每个请求生成 8 位短 UUID
- 支持通过 `X-Request-ID` 请求头传入自定义 ID
- 响应头返回 `X-Request-ID` 便于客户端关联

### 配置热更新模式

系统采用**基于回调的热更新**机制：
1. `config.Watcher` 使用 `fsnotify` 监听 `config.yaml`
2. 文件变更时，先验证新配置再应用
3. 调用注册的回调函数（调度器、API 服务器）传入新配置
4. 各组件使用锁原子性地更新状态
5. 调度器立即使用新配置触发探测周期

**环境变量覆盖**: API 密钥可通过 `MONITOR_<PROVIDER>_<SERVICE>_<CHANNEL>_API_KEY`（优先）或 `MONITOR_<PROVIDER>_<SERVICE>_API_KEY` 设置（大写，`-` → `_`）。也可通过 `env_var_name` 自定义变量名。

### 前端架构

React SPA，采用嵌套路由布局（`LanguageLayout` + `Outlet`），42 组件、15 hooks、10+ utils：

```
frontend/src/
├── pages/                     → 路由级页面
│   ├── ProviderPage.tsx      → 服务商详情页 (/p/:provider)
│   ├── OnboardingPage.tsx    → 自助收录页 (/contact/apply)
│   ├── ContactPage.tsx       → 联系我们落地页 (/contact)
│   ├── ChangeRequestPage.tsx → 变更申请页 (/contact/change)
│   └── AdminPage.tsx         → 管理后台页 (/admin)
├── components/                → UI 组件（42 文件）
│   ├── Header / Footer / Controls → 布局与导航
│   ├── StatusTable / StatusCard   → 数据展示（桌面表格/移动卡片）
│   ├── HeatmapBlock / LayeredHeatmapBlock → 热力图（单层/多模型）
│   ├── Tooltip / StatusDot        → 状态详情与指示器
│   ├── BoardSwitcher              → 热板/备板/冷板切换
│   ├── AnnouncementsBanner        → 公告横幅
│   ├── FavoriteButton / EmptyFavorites → 收藏功能
│   ├── MultiSelect / TimeFilterPicker / RefreshButton → 交互控件
│   ├── MultiModelIndicator        → 多模型状态指示
│   ├── ThemeSwitcher / FlagIcon / ServiceIcon / ChannelTypeIcon → 主题与图标
│   ├── ExternalLink / ExternalLinkModal → 外链安全
│   ├── CommunityMenu / SubscribeButton / Toast → 社区与通知
│   ├── icons/TelegramIcon.tsx     → 图标
│   ├── annotations/               → 标签子系统（4 文件）
│   │   ├── AnnotationChip / AnnotationCell
│   │   ├── AnnotationTooltip
│   │   └── index.ts
│   ├── admin/                     → 管理后台（7 文件）
│   │   ├── AdminAuth.tsx          → 管理员认证
│   │   ├── SubmissionList/Detail  → 收录申请管理
│   │   ├── ChangeRequestList.tsx  → 变更请求管理
│   │   ├── MonitorList/Detail     → monitors.d/ 通道管理
│   │   └── MonitorForm.tsx        → 通道创建/编辑表单
│   └── onboarding/                → 自助收录（3 文件）
│       ├── ProviderInfoStep.tsx   → 服务商信息
│       ├── ConnectionTestStep.tsx → 连接测试
│       └── ConfirmStep.tsx        → 确认提交
├── hooks/                     → 自定义 Hooks（15 文件）
│   ├── useMonitorData.ts     → API 轮询与数据管理
│   ├── useFavorites.ts       → 收藏持久化 (localStorage)
│   ├── useAnnouncements.ts   → 公告轮询
│   ├── useVersionInfo.ts     → 版本检测
│   ├── useSyncLanguage.ts    → URL ↔ i18n 语言同步
│   ├── useUrlState.ts        → URL 查询参数状态
│   ├── useSeoMeta.ts         → 动态 SEO meta
│   ├── useAnnotationTooltip.ts → 标签 tooltip 逻辑
│   ├── useTheme.ts           → 主题状态管理
│   ├── useAdmin.ts           → 管理员认证与会话
│   ├── useMonitorAdmin.ts    → monitors.d/ CRUD 操作
│   ├── useOnboarding.ts      → 自助收录流程管理
│   ├── useChangeRequest.ts   → 变更申请流程（API Key 认证 + 多步表单）
│   └── useChangeAdmin.ts     → 变更请求管理（管理后台 CRUD）
├── utils/                     → 工具函数（10+ 文件）
│   ├── sortMonitors.ts       → 监测项排序逻辑
│   ├── heatmapAggregator.ts  → 热力图数据聚合
│   ├── color.ts              → 颜色工具（渐变、HSL）
│   ├── mediaQuery.ts         → 响应式断点管理
│   ├── badgeUtils.ts         → 标签渲染工具
│   ├── format.ts             → 数字/日期格式化
│   ├── analytics.ts          → 分析追踪
│   ├── share.ts              → 分享功能
│   └── mockMonitor.ts        → 开发用 mock 数据
├── i18n/                      → 国际化（配置 + 翻译资源）
├── types/                     → TypeScript 类型定义（index.ts, monitor.ts, onboarding.ts, change.ts）
├── constants/                 → 应用常量
├── styles/themes/             → 主题 CSS 文件
├── App.tsx                    → 主仪表盘页面
├── router.tsx                 → 路由配置（嵌套布局）
└── main.tsx                   → 应用入口
```

**关键模式：**
- **嵌套路由**: `LanguageLayout` 负责语言同步，`Outlet` 渲染子页面（App / ProviderPage / OnboardingPage / AdminPage）
- **自定义 Hooks**: `useMonitorData` / `useOnboarding` / `useAdmin` / `useMonitorAdmin` 等分离数据逻辑
- **标签/赞助子系统**: `annotations/` 组件 + `badgeUtils` + `useBadgeTooltip`
- **多模型展示**: `LayeredHeatmapBlock` + `MultiModelIndicator` 处理父子通道
- **TypeScript**: `types/` 中的接口实现完整类型安全
- **Tailwind CSS**: v4 实用优先的样式
- **响应式设计**: 统一断点管理 + matchMedia API
- **国际化**: react-i18next + react-router-dom URL 路径多语言
- **主题系统**: 4 套主题 + 语义化 CSS 变量

### 主题系统

**支持的主题**:
- `default-dark`: 默认暗色（青色强调）
- `night-dark`: 护眼暖暗（琥珀色强调）
- `light-cool`: 冷灰亮色（青色强调）
- `light-warm`: 暖灰亮色（琥珀色强调）

**技术实现**:
```
frontend/src/
├── styles/themes/           → 主题 CSS 文件
│   ├── index.css           → 入口 + 语义化工具类
│   ├── default-dark.css    → 默认暗色主题变量
│   ├── night-dark.css      → 护眼暖暗主题变量
│   ├── light-cool.css      → 冷灰亮色主题变量
│   └── light-warm.css      → 暖灰亮色主题变量
├── hooks/useTheme.ts        → 主题状态管理 Hook
└── components/ThemeSwitcher.tsx → 主题切换器组件
```

**语义化颜色变量** (`themes/*.css`):
```css
:root[data-theme="default-dark"] {
  /* 背景层级 */
  --bg-page: 222 47% 3%;       /* 最底层页面背景 */
  --bg-surface: 217 33% 8%;    /* 卡片/面板背景 */
  --bg-elevated: 215 28% 12%;  /* 悬浮/弹出层背景 */
  --bg-muted: 215 25% 18%;     /* 禁用/次要背景 */

  /* 文字层级 */
  --text-primary: 210 40% 98%;   /* 主要文字 */
  --text-secondary: 215 20% 65%; /* 次要文字 */
  --text-muted: 215 15% 45%;     /* 禁用文字 */

  /* 强调色 */
  --accent: 187 85% 53%;         /* 主强调色 */
  --accent-strong: 187 90% 60%;  /* 强调色悬停态 */

  /* 状态色 */
  --success: 142 71% 45%;
  --warning: 38 92% 50%;
  --danger: 0 84% 60%;
}
```

**语义化工具类** (`themes/index.css`):
```css
@layer utilities {
  .bg-page { background-color: hsl(var(--bg-page)); }
  .bg-surface { background-color: hsl(var(--bg-surface)); }
  .text-primary { color: hsl(var(--text-primary)); }
  .text-accent { color: hsl(var(--accent)); }
  /* ... 更多工具类 */
}
```

**FOUC 防护** (`index.html`):
```html
<script>
  (function() {
    var theme = 'default-dark';
    try {
      var stored = localStorage.getItem('relay-pulse-theme');
      if (stored && ['default-dark','night-dark','light-cool','light-warm'].indexOf(stored) !== -1) {
        theme = stored;
      }
    } catch (e) {}
    document.documentElement.setAttribute('data-theme', theme);
    // 设置初始背景色防止白屏...
  })();
</script>
```

**使用规范**:
- ❌ 避免硬编码颜色：`text-slate-500`、`bg-zinc-800`
- ✅ 使用语义化类：`text-muted`、`bg-elevated`
- 透明度变体：`bg-surface/60`、`text-accent/50`

### 国际化架构 (i18n)

**支持的语言**:
- 🇨🇳 **中文** (zh-CN) - 默认语言，路径 `/`
- 🇺🇸 **English** (en-US) - 路径 `/en/`
- 🇷🇺 **Русский** (ru-RU) - 路径 `/ru/`
- 🇯🇵 **日本語** (ja-JP) - 路径 `/ja/`

**技术实现**:
1. **react-i18next** + **i18next-browser-languagedetector**: 翻译框架与语言检测
2. **react-router-dom v6**: 嵌套路由布局（`LanguageLayout` + `Outlet`）
3. **react-helmet-async**: 动态 `<title>` / `<meta>` SEO
4. **useSyncLanguage**: URL 前缀 ↔ i18n 状态同步

**设计原则**:
- **URL 简洁性**: 使用简化语言码（`/en/` 而非 `/en-US/`）
- **内部完整性**: 内部使用完整 locale（`en-US`）兼容 i18next
- **类型安全**: `isSupportedLanguage` 类型守卫确保正确性
- **路由分层**: `/api/*`、`/health` 等技术路径不参与 i18n

**核心映射** (`i18n/index.ts`):

| URL 前缀 | Locale | 说明 |
|----------|--------|------|
| (空) | zh-CN | 中文默认，根路径 |
| en | en-US | `/en/` → en-US |
| ru | ru-RU | `/ru/` → ru-RU |
| ja | ja-JP | `/ja/` → ja-JP |

**路由策略** (`router.tsx`):
- 根路径 `/` 使用检测语言（localStorage > 浏览器语言，默认 zh-CN）
- 语言前缀路径 `/en`、`/ru`、`/ja` 进入 `LanguageLayout`，通过 `Outlet` 渲染子页面
- 每个语言布局下包含子路由：`index`（App）、`p/:provider`（ProviderPage）
- 语言归一化：`normalizeLanguage()` 将浏览器语言码（如 `en`）映射到完整 locale（`en-US`）

**翻译文件** (`i18n/locales/*.json`): 嵌套 JSON 结构，覆盖 `meta/common/header/controls/table/status/subStatus/tooltip/footer/accessibility` 等命名空间。

**工厂模式 - 动态注入翻译到常量** (`constants/index.ts`):
```typescript
// 静态版本（向后兼容）
export const TIME_RANGES: TimeRange[] = [
  { id: '24h', label: '近24小时', points: 24, unit: 'hour' },
  // ...
];

// i18n 版本：工厂函数
export const getTimeRanges = (t: TFunction): TimeRange[] => [
  { id: '24h', label: t('controls.timeRanges.24h'), points: 24, unit: 'hour' },
  // ...
];
```

**i18n 规范**: 所有用户可见文本使用 `t()` 函数。新增组件时确保所有字符串走 i18n。

### 响应式断点系统

前端采用**统一的媒体查询管理系统**（`utils/mediaQuery.ts`），确保断点检测的一致性和浏览器兼容性：

**断点定义** (`BREAKPOINTS`):
- **mobile**: `< 768px`（`max-width: 767px`） - Tooltip 底部 Sheet vs 悬浮提示
- **tablet**: `< 1024px`（`max-width: 1023px`，与 Tailwind `lg` 断点一致） - StatusTable 卡片视图 vs 表格 + 热力图聚合

**设计原则：**
1. **使用 matchMedia API**：替代 `resize` 事件监听，避免高频触发
2. **Safari ≤13 兼容**：自动回退到 `addListener/removeListener` API
3. **HMR 安全**：在 Vite 热重载时自动清理监听器，防止内存泄漏
4. **缓存优化**：模块级缓存断点状态，避免重复计算
5. **事件隔离**：移动端禁用鼠标悬停事件，避免闪烁

**使用示例：**
```typescript
import { createMediaQueryEffect } from '../utils/mediaQuery';

useEffect(() => {
  const cleanup = createMediaQueryEffect('mobile', (isMobile) => {
    setIsMobile(isMobile);
  });
  return cleanup;
}, []);
```

**响应式行为：**
| 组件 | < 768px (mobile) | < 1024px (tablet) | ≥ 1024px (desktop) |
|------|------------------|-------------------|---------------------|
| Tooltip | 底部 Sheet | 底部 Sheet | 悬浮提示 |
| StatusTable | 卡片列表 | 卡片列表 | 完整表格 |
| HeatmapBlock | 点击触发，禁用悬停 | 点击触发 | 悬停显示 |
| 热力图数据 | 聚合显示 | 聚合显示 | 完整显示 |

### 数据流

1. **配置加载**: `config.Loader` 读取 YAML + .env + 环境变量覆盖，执行规范化、父子继承与校验
2. **调度计划**: `scheduler.Scheduler` 根据 `interval` / `max_concurrency` / `stagger_probes` 构建周期任务
3. **探测执行**: `monitor.Probe` 组装 headers/body/proxy，发起 HTTP 探测
4. **重试退避**: 失败时按 `retry_*` 参数执行指数退避 + jitter 重试
5. **存储写入**: `storage.Factory` 选择 SQLite/Postgres，写入探测结果
6. **归档与清理**: `storage.Archiver` 每日导出 CSV/CSV.GZ；`storage.Cleaner` 按 retention 清理过期数据
7. **事件检测**: `events.Detector` 基于连续计数阈值生成 UP/DOWN 事件
8. **API 聚合**: `api.Handler` 执行批量/并发查询，组装 `data + groups + meta` 并通过 singleflight 缓存
9. **前端渲染**: `useMonitorData` 轮询 `/api/status`，展示 boards/annotations/多模型热力图
10. **通知派发**: `notifier` 独立进程轮询 `/api/events`，推送 Telegram/QQ 通知

### 状态码系统

**主状态（status）**：
- `1` = 🟢 绿色（成功、HTTP 2xx、延迟正常）
- `2` = 🟡 黄色（降级：慢响应等）
- `0` = 🔴 红色（不可用：各类错误，包括限流）
- `-1` = ⚪ 灰色（仅用于时间块无数据，不是探测结果）

**HTTP 状态码映射**：
```
HTTP 响应
├── 2xx + 快速 + 内容匹配 → 🟢 绿色
├── 2xx + 慢速 + 内容匹配 → 🟡 波动 (slow_latency)
├── 2xx + 内容不匹配 → 🔴 不可用 (content_mismatch)  ← 无论快慢
├── 3xx → 🟢 绿色（重定向）
├── 400 → 🔴 不可用 (invalid_request)
├── 401/403 → 🔴 不可用 (auth_error)
├── 429 → 🔴 不可用 (rate_limit)  ← 不做内容校验
├── 其他 4xx → 🔴 不可用 (client_error)
├── 5xx → 🔴 不可用 (server_error)
└── 网络错误 → 🔴 不可用 (network_error)
```

**内容校验（`success_contains`）**：
- 仅对 **2xx 响应**（绿色和慢速黄色）执行内容校验
- **429 限流**：响应体是错误信息，不做内容校验
- **红色状态**：已是最差状态，不需要再校验
- 若 2xx 响应但内容不匹配 → 降级为 🔴 红色（语义失败）

**细分状态（SubStatus）**：

| 主状态 | SubStatus | 标签 | 触发条件 |
|--------|-----------|------|---------|
| 🟡 黄色 | `slow_latency` | 响应慢 | HTTP 2xx 但延迟超过阈值 |
| 🔴 红色 | `rate_limit` | 限流 | HTTP 429 |
| 🔴 红色 | `server_error` | 服务器错误 | HTTP 5xx |
| 🔴 红色 | `client_error` | 客户端错误 | HTTP 4xx（除 400/401/403/429） |
| 🔴 红色 | `auth_error` | 认证失败 | HTTP 401/403 |
| 🔴 红色 | `invalid_request` | 请求参数错误 | HTTP 400 |
| 🔴 红色 | `network_error` | 连接失败 | 网络错误、连接超时 |
| 🔴 红色 | `response_timeout` | 响应超时 | HTTP 连接成功但读取响应体超时 |
| 🔴 红色 | `content_mismatch` | 内容校验失败 | HTTP 2xx 但响应体不含预期内容 |

**可用率计算**：
- 采用**加权平均法**：每个状态按不同权重计入可用率
  - 绿色（status=1）→ **100% 权重**
  - 黄色（status=2）→ **degraded_weight 权重**（默认 70%，可配置）
  - 红色（status=0）→ **0% 权重**
- 每个时间块可用率 = `(累积权重 / 总探测次数) * 100`
- 总可用率 = `平均(所有时间块的可用率)`
- 无数据的时间块（availability=-1）不参与可用率计算，全无数据时显示 "--"
- 所有可用率显示（列表、Tooltip、热力图）统一使用渐变色：
  - 0-60% → 红到黄渐变
  - 60-100% → 黄到绿渐变

**延迟统计**：
- **仅统计可用状态**：只有 status > 0（绿色/黄色）的记录才纳入延迟统计，红色状态不计入
- 每个时间块延迟 = `sum(可用记录延迟) / 可用记录数`
- 延迟显示使用渐变色（基于 `slow_latency` 配置）：
  - < 30% slow_latency → 绿色（优秀）
  - 30%-100% → 绿到黄渐变（良好）
  - 100%-200% → 黄到红渐变（较慢）
  - ≥ 200% → 红色（很慢）
- API 响应 `meta.slow_latency_ms` 返回阈值（毫秒），供前端计算颜色

## 配置管理

配置分为两层：`config.yaml`（全局设置）+ `monitors.d/`（通道配置，按 PSC 一文件一通道）。结构定义于 `internal/config/*.go`。完整字段文档见 `docs/user/config.md`。

**monitors.d/ 目录化管理**：
- 每个 YAML 文件包含 `metadata`（source/revision/timestamps）+ `monitors`（ServiceConfig 数组）
- 文件名格式：`{provider}--{service}--{channel}.yaml`（parent-child 在同一文件）
- config.yaml 和 monitors.d/ 不能有同 PSC，否则启动报冲突
- 管理后台（`/api/admin/monitors/*`）可通过 API 进行 CRUD 操作
- 删除为软删除（归档到 `monitors.d/.archive/`）
- 热更新同时监听 config.yaml 和 monitors.d/ 目录变化

### AppConfig 全局设置

来源：`internal/config/app_config.go`

| 分组 | 关键字段 | 说明 |
|------|----------|------|
| 探测节奏 | `interval`、`slow_latency`、`timeout` | 全局巡检频率与阈值（兜底），优先级：monitor > template > global |
| 重试退避 | `retry`、`retry_base_delay`（默认 200ms）、`retry_max_delay`（默认 2s）、`retry_jitter`（默认 0.2） | 指数退避重试，`retry` 表示额外重试次数 |
| 运行时 | `degraded_weight`（默认 0.7）、`max_concurrency`（默认 10，-1 无限）、`stagger_probes`（默认 true） | 可用率权重与并发控制 |
| 查询优化 | `enable_concurrent_query`、`concurrent_query_limit`、`enable_batch_query`、`enable_db_timeline_agg`、`batch_query_max_keys` | API 层数据库查询优化 |
| 缓存 | `cache_ttl`（按 period 区分，90m/24h=10s，7d/30d=60s） | API 响应缓存 |
| Provider 策略 | `disabled_providers`、`hidden_providers`、`risk_providers` | 批量禁用/隐藏/风险标记 |
| 板块系统 | `boards`（`enabled`，三层：hot/secondary/cold）、`boards.auto_move`（`enabled`、`threshold_cold/down/up`、`min_probes`、`check_interval`） | 热板/备板/冷板 + 自动移板 |
| 展示控制 | `expose_channel_details`、`channel_details_providers`、`public_base_url` | 通道技术细节暴露 |
| 赞助/标签 | `sponsor_pin`、`enable_annotations`、`annotation_rules` | 置顶与标签体系 |
| 功能模块 | `events`、`onboarding`、`announcements`、`github` | 事件/收录/公告/GitHub 配置 |
| 存储 | `storage`（含 type/sqlite/postgres/retention/archive） | 数据库与数据生命周期 |

### ServiceConfig 监测项设置

来源：`internal/config/monitor.go`

| 分组 | 关键字段 | 说明 |
|------|----------|------|
| 身份标识 | `provider`、`service`、`channel`、`provider_slug`、`provider_url` | PSC 三元组 + URL slug |
| 显示名称 | `provider_name`、`service_name`、`channel_name` | UI 显示名称（可选，未配置时回退到标识字段） |
| 业务属性 | `category`（commercial/public）、`sponsor`、`sponsor_url`、`sponsor_level`、`price_min`、`price_max`、`listed_since`、`expires_at` | 分类、赞助与倍率 |
| 多模型 | `model`（模型名称）、`parent`（格式 `provider/service/channel`） | 父子通道继承体系 |
| 生命周期 | `disabled`/`disabled_reason`、`hidden`/`hidden_reason`、`board`（hot/secondary/cold）、`cold_reason`、`auto_cold_exempt` | 停用/隐藏/板块控制 |
| 模板配置 | `template`、`base_url`、`url_pattern` | 模板引用 + 基础地址（新格式，推荐） |
| 探测配置 | `url`、`method`、`headers`、`body`、`success_contains`、`api_key`、`proxy`、`env_var_name` | HTTP 探测参数（传统格式或模板自动填充） |
| 覆盖配置 | `interval`、`slow_latency`、`timeout`、`retry`、`retry_base_delay`、`retry_max_delay`、`retry_jitter` | 监测项级覆盖全局设置 |
| 标签 | `annotations`（运行时由 annotation_rules + 系统派生填充） | 标签与风险标记 |

**配置优先级**: `monitor` > `template` > `global`（适用于 slow_latency、timeout、retry 等所有分级配置；同名字段以更高优先级覆盖，未指定则继承。模板值在 resolveTemplates 阶段填入 monitor 级别作为默认值）

**⚠️ `model` 字段的双重身份（换模板/改名前必读）**: `model` 既是**热力图展示名**，又是**历史数据的 DB 业务键**。
- 各历史表按 `(provider, service, channel, model)` 区分序列：`probe_history`/`status_events` 的真实 PK 是 `id`，但业务键是该四元组（覆盖索引 `idx_probe_history_pscm_ts_cover`）；`service_states`/`monitor_overrides` 的 **PK 直接含 model**；`channel_states` PK 不含 model。
- **probe 写库 `result.Model = cfg.Model`（展示名），且没有 `request_model` 列**——库里只靠展示 `model` 串区分序列，某历史点当时实际请求哪个版本无法回溯。
- **后果**：换探测模板或改 `model` 显示名 = 业务键变 = 历史序列断裂（旧名成孤儿序列）+ automove 的 sticky cold override（按旧键存）失效、通道回 hot。
- **取舍（无免费午餐）**：`model` 带版本号 → 能并排比多版本但每次升版断历史；`model` 不带版本（version-less，把版本放 `request_model`）→ 历史跨版本连续，但同通道不能并存两版本（撞业务键），且无法回溯历史版本。
- 因 `{{MODEL}}`=`request_model`回退`model`，只要模板/monitor 显式设了 `request_model`，改 `model` 展示名不影响 body 发出的真实模型——这是“给 monitor 加 `model: X` 覆盖展示名而不打红”的前提。
- **换模板想保历史**：保持 `model` 串不变、版本只改 `request_model`；若必须改名，需配套 SQL 把旧 model 的历史行 relabel 到新名（`service_states` 因 PK 含 model 要先 dedup）。详见 `/rpmigrate` skill。

**模板占位符**: URL/headers/body 中的占位符在探测时由 `internal/monitor/probe.go` 的 `InjectVariables` 统一替换。支持：`{{BASE_URL}}`、`{{API_KEY}}`、`{{MODEL}}`（=`request_model`，为空回退 `model`）、`{{REQUEST_MODEL}}`、`{{USER_ID}}`、`{{USER_ID_HASH}}`、`{{USER_ACCOUNT_UUID}}`、`{{RAND_UUID}}`、`{{RAND_UUID2}}`、`{{PROMPT}}`、`{{EXPECTED_ANSWER}}`、`{{ARITH_A}}`、`{{ARITH_B}}`（同一次注入中两个 `{{RAND_UUID}}` 取同一值）。注意：`body` 按模板文件中的**原始字节**发送（仅 `TrimSpace`，不 re-marshal/不 compact），占位符按字符串替换；需与抓包字节一致时 body 要写成压缩单行、且不放占位符。

**引用文件**: 对于大型请求体，使用 `body: "!include templates/filename.json"`（必须在 `templates/` 目录下）。

### 存储配置

来源：`internal/config/storage_config.go`

- **类型选择**: `storage.type`（`sqlite` 默认 / `postgres`），由 `storage.Factory` 自动选择实现
- **SQLite**: `storage.sqlite.path`（默认 `monitor.db`）
- **PostgreSQL**: `storage.postgres.{host,port,user,password,database,sslmode,max_open_conns,max_idle_conns,conn_max_lifetime}`
- **数据保留** (`storage.retention`): `enabled`、`days`（默认 36）、`cleanup_interval`（默认 1h）、`batch_size`（默认 10000）、`max_batches_per_run`（默认 100）、`startup_delay`（默认 1m）、`jitter`（默认 0.2）
- **数据归档** (`storage.archive`): `enabled`、`schedule_hour`（UTC，默认 3）、`output_dir`（默认 ./archive）、`format`（csv/csv.gz，默认 csv.gz）、`archive_days`（默认 35）、`backfill_days`（默认 7）、`keep_days`（默认 365，0=永久）

详见 `docs/user/deploy-postgres.md`。

### 功能模块配置

来源：`internal/config/features.go`、`internal/config/external.go`

| 模块 | 关键字段 | 说明 |
|------|----------|------|
| Events | `enabled`、`mode`（model/channel）、`down_threshold`、`up_threshold`、`channel_down_threshold`、`channel_count_mode`、`api_token` | 状态变更事件 |
| SponsorPin | `enabled`、`max_pinned`、`min_uptime`、`min_level` | 赞助通道置顶（详见 `docs/user/sponsorship.md`） |
| Boards | `enabled` | 热板/备板/冷板三层系统 |
| Onboarding | `enabled`、`admin_token`、`encryption_key`、`proof_secret`、`proof_ttl`（默认 5m）、`max_per_ip_per_day`（默认 5）、`contact_info`、`change_requests`（子配置：`enabled`、`max_per_ip_per_day`） | 自助收录 + 变更请求（启用需重启容器）。启用 onboarding 时允许零 monitors 启动 |
| Announcements | `enabled`、`owner`、`repo`、`category_name`、`poll_interval`、`window_hours`、`max_items`、`api_max_age` | GitHub Discussions 公告 |
| GitHub | `token`、`proxy`、`timeout` | GitHub API 通用配置（公告功能依赖） |

### 热更新测试

```bash
# 启动监测服务
./monitor

# 在另一个终端编辑配置
vim config.yaml

# 观察日志：
# [Config] 检测到配置文件变更，正在重载...
# [Config] 热更新成功！已加载 3 个监测任务
# [Scheduler] 配置已更新，下次巡检将使用新配置
```

## API 端点

来源：`internal/api/server.go:156-248`

| 方法 | 路径 | 说明 |
|------|------|------|
| GET/HEAD | `/health` | 健康检查 |
| GET | `/api/status` | 主监测数据（含时间线） |
| GET | `/api/status/query` | 轻量状态查询 |
| POST | `/api/status/batch` | 批量状态查询 |
| GET | `/api/events` | 状态变更事件（游标分页，强制鉴权，未配置 token 返回 503） |
| GET | `/api/events/latest` | 最新事件 ID（强制鉴权） |
| GET | `/api/announcements` | GitHub 公告列表 |
| GET | `/api/version` | 构建版本信息 |
| GET | `/api/onboarding/meta` | 收录表单元数据（服务类型、赞助等级等） |
| POST | `/api/onboarding/submit` | 提交收录申请（IP 限流） |
| GET | `/api/onboarding/:id` | 查询收录状态 |
| POST | `/api/onboarding/test` | 收录内联探测测试（IP 限流） |
| POST | `/api/change/auth` | 变更：API Key 认证（返回通道列表） |
| POST | `/api/change/submit` | 变更：提交变更请求（含测试证明） |
| GET | `/api/change/:id` | 变更：查询请求状态 |
| GET | `/api/admin/changes` | 管理：变更请求列表（Bearer 鉴权，支持 status 过滤） |
| GET | `/api/admin/changes/:id` | 管理：变更请求详情 |
| POST | `/api/admin/changes/:id/approve` | 管理：批准变更 |
| POST | `/api/admin/changes/:id/reject` | 管理：拒绝变更 |
| POST | `/api/admin/changes/:id/apply` | 管理：应用到 monitors.d/（仅 auto 模式） |
| DELETE | `/api/admin/changes/:id` | 管理：删除变更请求 |
| GET | `/api/admin/submissions` | 管理：收录申请列表（Bearer 鉴权） |
| GET/PUT/DELETE | `/api/admin/submissions/:id` | 管理：申请详情/更新/删除 |
| POST | `/api/admin/submissions/:id/reject` | 管理：拒绝申请 |
| POST | `/api/admin/submissions/:id/test` | 管理：测试申请连通性 |
| POST | `/api/admin/submissions/:id/publish` | 管理：发布到 monitors.d/ |
| GET | `/api/admin/monitors` | 管理：monitors.d/ 通道列表 |
| GET/PUT/DELETE | `/api/admin/monitors/:key` | 管理：通道详情/更新/归档 |
| POST | `/api/admin/monitors` | 管理：创建通道 |
| POST | `/api/admin/monitors/:key/toggle` | 管理：切换 disabled/hidden |
| POST | `/api/admin/monitors/:key/probe` | 管理：手动探测（走完整 ServiceConfig，与 scheduler 字段级一致） |
| GET | `/api/admin/monitors/:key/logs` | 管理：探测历史日志（since/limit/model 查询，含 error_detail） |
| GET/HEAD | `/ready` | 就绪检查（含存储连通性） |
| GET | `/sitemap.xml` | 动态站点地图 |
| GET | `/robots.txt` | 爬虫规则 |

**/api/status 查询参数**:
- `period`: `90m` / `24h`（默认，`1d` 为别名）/ `7d` / `30d`
- `align`: `hour`（整点对齐，可选）
- `time_filter`: `HH:MM-HH:MM`（UTC 时段过滤，仅 7d/30d 可用，支持跨午夜）
- `provider` / `service`: 按名称过滤
- `board`: `hot` / `secondary` / `cold` / `all`（板块过滤）
- `include_hidden`: 调试用，包含隐藏项

**/api/status 响应结构**:
```json
{
  "meta": {
    "period": "24h",
    "timeline_mode": "aggregated",
    "count": 3,
    "slow_latency_ms": 5000,
    "enable_annotations": true,
    "sponsor_pin": { "enabled": true, "max_pinned": 3, "..." : "..." },
    "boards": { "enabled": true },
    "all_monitor_ids": ["provider-service-channel"]
  },
  "data": [
    {
      "provider": "88code",
      "service": "cc",
      "channel": "vip3",
      "current_status": { "status": 1, "latency": 234, "timestamp": 1735559123 },
      "timeline": [{ "time": "14:30", "status": 1, "latency": 234 }]
    }
  ],
  "groups": [
    {
      "provider": "88code",
      "service": "cc",
      "channel": "vip3",
      "layers": [{ "model": "claude-4-opus", "timeline": [...] }]
    }
  ]
}
```

## 测试

### 后端测试

- 测试文件与源文件放在一起（`*_test.go`）
- 关键测试文件：
  - `internal/config/config_test.go` - 配置解析与规范化
  - `internal/config/parent_inheritance_test.go` - 父子继承
  - `internal/config/concurrency_test.go` - 并发安全
  - `internal/config/disabled_test.go` - 禁用逻辑
  - `internal/config/proxy_test.go` - 代理配置
  - `internal/config/url_security_test.go` - URL 安全校验
  - `internal/config/monitor_store_test.go` - monitors.d/ CRUD
  - `internal/monitor/probe_test.go` - 探测逻辑
  - `internal/events/detector_test.go` - 事件检测
  - `internal/events/channel_detector_test.go` - 通道级事件检测
  - `internal/events/service_test.go` - 事件服务
  - `internal/storage/sqlite_test.go` - SQLite 存储
  - `internal/storage/postgres_test.go` - PostgreSQL 存储（`//go:build postgres`）
  - `internal/api/handler_test.go` - API 处理器
  - `internal/api/time_filter_test.go` - 时段过滤
  - `internal/api/disabled_filter_test.go` - 禁用过滤
  - `internal/api/meta_test.go` - Meta 注入
  - `internal/scheduler/scheduler_test.go` - 调度器核心
  - `internal/scheduler/stagger_test.go` - 错峰分散
  - `internal/scheduler/grouping_test.go` - 分组逻辑
  - `internal/scheduler/disabled_test.go` - 禁用逻辑
  - `internal/automove/availability_test.go` - 自动移板可用率计算
  - `internal/automove/service_test.go` - 自动移板服务
  - `internal/announcements/*_test.go` - 公告拉取与服务
  - `internal/onboarding/crypto_test.go` - API Key 加密
  - `internal/onboarding/proof_test.go` - 测试证明签发
  - `internal/apikey/cipher_test.go` - 共享 API Key 加密/指纹
  - `internal/apikey/proof_test.go` - 共享测试证明签发/验证
- 使用 `go test -v` 查看详细输出

### 前端测试

- 测试框架：Vitest
- 测试文件：`frontend/src/utils/*.test.ts`
- 关键测试：
  - `sortMonitors.test.ts` - 排序逻辑
  - `heatmapAggregator.test.ts` - 热力图聚合
  - `monitorDataProcessor.test.ts` - 数据处理（canonicalize/uptime/转换）
  - `apiClient.test.ts` - API 客户端
  - `color.test.ts` - 颜色工具
  - `modelName.test.ts` - 模型名称处理
  - `annotationUtils.test.ts` - 标签工具
  - `color.test.ts` - 颜色工具

```bash
cd frontend

# 运行测试
npm run test

# 监听模式（开发时使用）
npm run test:watch
```

### 手动集成测试

```bash
# 终端 1：启动后端
./monitor

# 终端 2：启动前端
cd frontend && npm run dev

# 终端 3：测试 API
curl http://localhost:8080/api/status

# 测试热更新
vim config.yaml  # 修改 interval 为 "30s"
# 观察调度器日志中的配置重载信息
```

## 提交信息规范

遵循 conventional commits：

```
<type>: <subject>

<body>

<footer>
```

**类型**: `feat`、`fix`、`docs`、`refactor`、`test`、`chore`

**示例**:
```
feat: add response content validation with success_contains

- Add success_contains field to ServiceConfig
- Implement keyword matching in probe.go
- Update config.yaml.example with usage

Closes #42
```

## 常见模式与陷阱

### Scheduler 中的并发

调度器使用两个锁：
- `cfgMu` (RWMutex): 保护配置访问
- `mu` (Mutex): 保护调度器状态（运行标志、定时器）

对于只读配置访问，始终使用 `RLock()/RUnlock()`。

### Storage Factory 与驱动选择

`storage.Factory` 根据 `storage.type` 选择 SQLite 或 PostgreSQL 实现。新增存储驱动时先实现 `storage.Storage` 接口，再在 Factory 中注册。

### Parent-child 继承

父通道定义公共配置（url/headers/body 等），子通道通过 `model` + `parent`（格式 `provider/service/channel`）继承。继承逻辑集中在 `internal/config/parent_inheritance.go`，校验确保父通道存在。

### 指数退避重试

`retry` 表示**额外重试次数**（不含首次尝试）。退避公式：`min(base_delay * 2^attempt, max_delay) + random_jitter`。配置见 `internal/config/app_config.go`，实现见 `internal/monitor/probe.go`。

### 事件状态机与鉴权

`events.Detector` 使用连续计数阈值防止状态抖动（flapping）：连续 N 次不可用才触发 DOWN，连续 M 次恢复才触发 UP。`/api/events*` 端点**强制鉴权**：未配置 `api_token` 时返回 503 拒绝所有请求；已配置时需要 `Authorization: Bearer <token>`。

### 批量查询优化

7d/30d 等长周期查询可通过 `enable_batch_query` 将 N 个监测项的 2N 次数据库往返降为 2 次。配合 `enable_db_timeline_agg`（仅 PostgreSQL）可将聚合计算下推到数据库层。回退链路：batch → concurrent → serial。

### SQLite 并发

使用 WAL 模式（`_journal_mode=WAL`）允许写入时并发读取。连接 DSN：`file:monitor.db?_journal_mode=WAL`

### Probe 中的错误处理

- 网络错误 → 状态 0（红色）
- HTTP 4xx/5xx → 状态 0（红色）
- HTTP 2xx + 慢延迟 → 状态 2（黄色）
- HTTP 2xx + 快速 + 内容匹配 → 状态 1（绿色）

### Onboarding 通道标识派生

收录申请提交时，channel code 由 `deriveChannelCode(channelType, channelSource, channelGroup)` 派生为三段 `{type}-{source}-{group}`（全小写；group 为空时回退两段，仅用于兼容旧数据）。例如 type="O" + source="max" + group="us" → `o-max-us`。提交即强制校验（见 `internal/onboarding/service.go`）：
- **provider_name** 仅允许 ASCII 可打印字符（`^[\x20-\x7E]+$`，禁中文）；
- **channel_source** 必须是 `ChannelSourceCatalog`（per-service 受控词表，单一真相源，同时供 `/api/onboarding/meta` 下发前端）中的 2-5 位小写代码；如需新增来源改这一处 map；
- **channel_group** 为 1-8 位小写字母/数字（中转商自定义分组），留空默认 `main`。

PSC 各段仍只允许小写字母、数字、短横线（`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`）。`AdminUpdate` 仅当 service/type/source/group 四元组真正变化时才重派生 channel_code（保护 legacy 两段记录），并对 channel_type(O/R/M)、service_type(cc/cx/gm) 做枚举校验。管理员可在发布前通过 `target_channel` 覆盖派生值（**故意保留的逃生口，不受三段约束**，用于 legacy 与特殊命名）。前端 `ChannelTypeIcon` 通过首字母（大小写不敏感）识别通道类型图标（o→官方、r→逆向、m→混合）。

### 零 monitors 启动

当 `onboarding.enabled = true` 时，`validate()` 允许 `monitors` 数组为空。这支持 "onboarding-first" 部署场景：先启动空系统，再通过收录流程添加通道。

### 前端数据获取

`useMonitorData` Hook 每 30 秒轮询 `/api/status`。组件卸载时需禁用轮询以防止内存泄漏。

## 生产部署

### 环境变量（推荐）

```bash
export MONITOR_88CODE_CC_API_KEY="sk-real-key"
export MONITOR_DUCKCODING_CC_API_KEY="sk-duck-key"
./monitor
```

### Systemd 服务

参见 README.md 中的 systemd unit 文件模板。

### Docker

参见 README.md 中的多阶段 Dockerfile。

## 相关文档

- 完整开发指南：`CONTRIBUTING.md`
- API 设计细节：`archive/prds.md`（历史参考）
- 实现笔记：`archive/IMPLEMENTATION.md`（历史参考）
- 每次提交代码前记得检测, 是否有变动需要同步到文档
- 在commit前应先进行代码格式检查

## 同步检查清单

更新本文档时，核对以下关键同步点：

- [ ] 更新顶部"同步检查点"的日期和 commit
- [ ] 后端架构树 vs `internal/` + `cmd/` 实际目录：`find internal/ -type f -name "*.go" | sort`
- [ ] AppConfig 字段 vs `internal/config/app_config.go` struct tags
- [ ] ServiceConfig 字段 vs `internal/config/monitor.go` struct tags
- [ ] API 路由表 vs `internal/api/server.go` 中 `router.GET/POST` 注册
- [ ] API 响应结构 vs `internal/api/handler.go` JSON 序列化
- [ ] 前端组件列表 vs `frontend/src/components/` 目录
- [ ] 前端 hooks 列表 vs `frontend/src/hooks/` 目录
- [ ] 前端 utils 列表 vs `frontend/src/utils/` 目录
- [ ] 前端 pages 列表 vs `frontend/src/pages/` 目录
- [ ] 断点值 vs `frontend/src/utils/mediaQuery.ts` BREAKPOINTS 常量
- [ ] 测试文件列表 vs 实际 `*_test.go` 和 `*.test.ts` 文件
- [ ] Notifier 子模块结构 vs `notifier/` 目录
- [ ] Onboarding 配置字段 vs `internal/config/features.go` OnboardingConfig
- [ ] Admin/Onboarding/Change API 路由 vs `internal/api/server.go` 注册
- [ ] monitors.d/ 相关描述 vs `internal/config/monitor_store.go` + `loader.go`
