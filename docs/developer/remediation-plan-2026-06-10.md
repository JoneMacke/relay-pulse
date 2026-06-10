# Relay Pulse 问题修复与上线推进计划（2026-06-10）

本文档用于把 2026-06-10 的代码审阅结论交接给后续实现会话。目标是按低风险优先的方式修复自助收录/变更请求流程中的契约漂移、安全边界和工程保障问题。

## 实现状态（2026-06-10 更新）

**第一批已全部实现并验证**（go test ./... / go vet / 前端 tsc -b / vitest 175 全过 / lint 0 error / build 均通过），并经 codex 二轮 review 闭环：

- ① 用户自助变更白名单移除 `category`/`sponsor_level`（保留管理员白名单）+ 删除随之不可达的 sponsor_level 特判 + 回归测试。
- ② proof 有效期改后端下发：`apikey.IssueWithExpiry` / `onboarding.IssueProofWithExpiry` / `/api/onboarding/test` 返回 `proof_expires_at`（Unix 秒）；前端提交守卫、倒计时、警告/过期全改用该绝对时间，删除硬编码 15m/12m，警告阈值自适应服务端真实 `proof_ttl`（生命周期最后 20%，至少 30s）。
- ③ inline 429 `rate_limited`→`rate_limit`（对齐 scheduler/storage）；前端 sub_status 走 `t('subStatus.'+code, code)` 翻译并回退原始码，4 个 locale 补 `redirect_blocked`/`response_too_large`/`concurrency_limited`。**3xx 的 redirect_blocked 红/绿语义本批未动，留作单独产品决策。**
- ④ `/api/change/auth` 复用 `probeLimiter`（main.go 无条件初始化）做 IP 限流。
- ⑤ **额外修复 finding C**：`normalizeOnboardingConfig` 守卫改为 `!Onboarding.Enabled && !ChangeRequests.Enabled`，修复「仅启用 change_requests 时 ProofTTLDuration 留零值→proof 即刻过期」的潜伏 bug + 新增 `onboarding_normalize_test.go`。

**行为收紧（需写入 release note）**：finding C 修复后，「仅开 `change_requests` 但未配 `admin_token`/`encryption_key`/`proof_secret`」的部署将启动失败（fail-fast）。此前此类配置已因零 TTL 静默损坏，现改为显式报错。

**刻意 defer 的两点（均无正确性/安全缺口）**：
1. proof_expires_at 缺失时从 proof token 尾部解析的 fallback——前端嵌入 Go 二进制为原子部署，后端必同时下发该字段，且后端 `Verify` 才是过期真闸，故不引入对 token 格式的耦合。
2. 变更请求流程（`useChangeRequest`）的客户端 proof 过期预检——既存缺口非本次回归，`change.Submit` 后端 `Verify` 已强制校验过期，仅缺客户端 UX 预检，留作小 follow-up。

**第一批已发版 v2.41.1 并部署生产**（2026-06-10；commit 9d9a85c；生产 /api/version git_commit=9d9a85c，health/ready 均 200）。

### 第二批已全部实现（2026-06-11，4 个独立 commit）

经真实工具链实证后落地，scope 较原计划有一处重要修正：

- **② react-router-dom 6.30.2 → 6.30.4（commit 2176d43）**：`npm audit --omit=dev` 报的 ghsa-2w69-qvjg-hvjx（开放重定向 XSS）修复落在 v6 线内的 6.30.4（非破坏性 patch，无 API 变更），不必迁移 v7。升级后生产面 0 高危。
- **③ Go 版本字符串统一 1.25 线（commit d5ee69d）—— 非安全修复**：**实证推翻原计划的安全判断**。advisory 显示 GO-2026-5037 / GO-2026-5039 均 fixed in **go1.25.11**，而生产镜像（golang:1.25-alpine）构建出的二进制 `go_version=go1.25.11` 已含补丁，**生产不受影响**。本机 go1.26.3 的 govulncheck 告警只反映本机工具链，与生产无关（典型「本机 Go ≠ Docker 工具链」陷阱）。故本项仅做一致性清理：notifier 1.24→1.25（go.mod/Dockerfile/CI 三处）+ 文档 "Go 1.24+"→"Go 1.25+"；不强行升 1.26 线。
- **④ pull_request 触发 + govulncheck 闸 + Makefile 聚合命令（commit ffc3f3f）**：ci-release.yml 加 pull_request（release/docker/tag-version 仍 main-push-only）；ci job setup-go 改 `go-version: '1.25'`（与生产同源、确保 govulncheck 跑在打补丁的 1.25.x 上）；新增 govulncheck 步骤防回归（仅 CI，不入 Makefile——本机更新的 Go 小版本会误报）；Makefile 补 embed-frontend/test/lint/build/ci。
- **① 新增 /api/change/test 解耦 onboarding（commit 57f8468）**：抽 Handler 私有 helper runInlineTestProbe + inlineTestProbeResponse，两端共用探测编排；change.Service 加 IssueProofWithExpiry（复用其同源独立 proofIssuer）；前端 useChangeRequest 改调新端点；/api/onboarding/test 保留兼容。codex 二轮协作。

验证：go vet / go build / go test ./...（全 pass）/ notifier go build/vet/test / 前端 tsc -b / vitest / npm run build 全通过。`make ci` 因本机无 make 二进制未实跑（recipe tab 缩进 + 底层命令已逐条验证）；govulncheck 闸与 go-version 改动将在下次 push 的 CI 首次实跑。

**第二批待办**：push（触发 semantic-release，feat 会 bump minor → v2.42.0）+ 部署生产。

---

## 当前审阅结论

已验证通过：

- `go test ./...`
- `cd notifier && go test ./...`
- `go vet ./...`
- `cd notifier && go vet ./...`
- `cd frontend && npm run build`
- `cd frontend && npm run test`
- `cd frontend && npm run lint`：仅 3 个 Fast Refresh warning

未完成验证：

- `go test -race`：当前环境 `CGO_ENABLED=0` 且缺少 `gcc`，race test 未能运行。

额外安全扫描：

- `cd frontend && npm audit --omit=dev` 报 `react-router-dom@6.30.2` / `@remix-run/router@1.23.1` 高危告警：`GHSA-2w69-qvjg-hvjx`。
- `govulncheck` 在本机 Go `1.26.3` 下报告标准库调用路径漏洞 `GO-2026-5039`、`GO-2026-5037`，修复版本指向 Go `1.26.4`。需要结合 CI/Docker/runtime 实际工具链处理。

## 第一批：低风险热修

第一批不改数据库结构，不改主监控调度，不改公开 `/api/status`，适合作为快速修复小版本。

### 1. 收紧用户自助变更字段

问题：

- 前端明确说明 `category` 和 `sponsor_level` 需要人工对接，不属于用户自助变更范围。
- 后端 `internal/change/service.go` 的 `allowedFields` 仍允许用户直接提交这两个字段。
- 持有通道 API Key 的用户可以绕过 UI 直接向 `/api/change/submit` 提交 `category` / `sponsor_level`。

建议修改：

- 从用户侧 `allowedFields` 移除 `category`、`sponsor_level`。
- 保留 `adminUpdateableFields` 中的 `category`、`sponsor_level`，允许管理员在后台人工调整。
- 增加单测：用户提交 `category` 或 `sponsor_level` 应返回不允许自助变更。

重点文件：

- `internal/change/service.go`
- `internal/change/service_test.go`
- `frontend/src/pages/ChangeRequestPage.tsx`
- `frontend/src/types/change.ts`

### 2. proof 有效期改为后端下发

问题：

- 后端默认 `onboarding.proof_ttl` 是 `5m`，且可配置。
- 前端 `ConnectionTestStep`、`ConfirmStep`、`useOnboarding` 写死 15 分钟。
- 用户界面可能仍允许提交，但后端已经判定 proof 过期。

建议修改：

- 后端测试成功响应增加 `proof_expires_at`（Unix 秒）或 `proof_ttl_seconds`。优先建议返回 `proof_expires_at`，以 proof 的真实签发过期时间为准。
- `apikey.ProofIssuer.Issue` 可新增一个返回 proof 与 expiresAt 的方法，例如 `IssueWithExpiry`，保留原 `Issue` 兼容现有调用。
- 前端基于 `proof_expires_at` 计算倒计时和提交前校验，不再硬编码 15 分钟。
- change request 流程也复用同一字段，避免后续再漂移。

重点文件：

- `internal/apikey/proof.go`
- `internal/onboarding/service.go`
- `internal/api/onboarding_handler.go`
- `frontend/src/types/onboarding.ts`
- `frontend/src/hooks/useOnboarding.ts`
- `frontend/src/components/onboarding/ConnectionTestStep.tsx`
- `frontend/src/components/onboarding/ConfirmStep.tsx`
- `frontend/src/hooks/useChangeRequest.ts`
- `frontend/src/pages/ChangeRequestPage.tsx`

### 3. 统一内联探测 sub_status

问题：

- 定时探测/存储/翻译使用 `rate_limit`。
- 内联探测 429 返回 `rate_limited`。
- 前端测试页当前直接显示原始 `sub_status`，会出现口径和翻译漂移。

建议修改：

- `internal/probe/inline.go` 中 429 改为 `rate_limit`。
- 若保留内联专属状态（例如 `redirect_blocked`、`response_too_large`、`concurrency_limited`），给前端做统一展示映射或补 i18n。
- `ConnectionTestStep` 和变更请求测试页展示翻译后的状态，找不到翻译再回退原始码。
- 增加内联探测状态码单测。

重点文件：

- `internal/probe/inline.go`
- `internal/probe/inline_test.go`
- `frontend/src/i18n/locales/*.json`
- `frontend/src/components/onboarding/ConnectionTestStep.tsx`
- `frontend/src/pages/ChangeRequestPage.tsx`

### 4. 给 `/api/change/auth` 增加 IP 限流

问题：

- `/api/change/auth` 会按 API Key 指纹查找候选通道。
- 当前有统一失败文案，但没有速率限制，容易被高频枚举。

建议修改：

- 复用现有 `probe.IPLimiter`，或为 change auth 单独配置一个 limiter。
- 在 `AuthChange` 解析请求前或后增加 `c.ClientIP()` 限流。
- 返回 `429 RATE_LIMITED`。
- 增加 handler/service 级测试。

重点文件：

- `internal/api/change_handler.go`
- `internal/api/handler.go`
- `cmd/server/main.go`
- `internal/probe/limiter.go`

## 第二批：结构与供应链修复

第二批涉及依赖和接口结构，建议单独发版，并做 staging/生产配置副本冒烟。

### 1. 拆出变更请求专用测试端点

问题：

- `ChangeRequestConfig` 注释说独立于 Onboarding，但变更请求前端测试调用 `/api/onboarding/test`。
- 如果只启用 `change_requests`、不启用 `onboarding`，涉及 `base_url` 或 API Key 轮换的变更流程会卡在 503。

建议修改：

- 新增 `/api/change/test`。
- 复用 inline prober、安全 URL 校验、proof 签发逻辑。
- proof 签发服务不要依赖 onboarding service 是否启用。
- 旧 `/api/onboarding/test` 保持兼容。

重点文件：

- `internal/api/change_handler.go`
- `internal/api/onboarding_handler.go`
- `internal/change/service.go`
- `internal/onboarding/service.go`
- `cmd/server/main.go`
- `frontend/src/hooks/useChangeRequest.ts`

### 2. 处理前端 React Router 安全告警

问题：

- `npm audit --omit=dev` 报 `react-router-dom@6.30.2` 链路高危告警。

建议修改：

- 升级 `react-router-dom` 到包含修复的版本。
- 同步 `package-lock.json`。
- 跑 `npm run build && npm run lint && npm run test`。
- 冒烟检查多语言路由：`/`、`/en/`、`/ru/`、`/ja/`、`/contact/apply`、`/contact/change`、`/admin`。

重点文件：

- `frontend/package.json`
- `frontend/package-lock.json`
- `frontend/src/router.tsx`
- `frontend/src/hooks/useSyncLanguage.ts`

### 3. 处理 Go 工具链安全告警

问题：

- 本机 `govulncheck` 在 Go `1.26.3` 下报告标准库漏洞，修复版本为 Go `1.26.4`。
- 根模块 `go.mod` 是 `go 1.25.0`，Docker 使用 `golang:1.25-alpine`，文档仍写 Go 1.24+。

建议修改：

- 明确根项目实际支持的 Go 版本，并同步：
  - `go.mod`
  - `Dockerfile`
  - `.github/workflows/ci-release.yml`
  - `README.md`
  - `CONTRIBUTING.md`
  - `docs/user/*` 中相关版本说明
- 确认 Docker builder 镜像对应 patch 版本不受 `govulncheck` 告警影响。
- notifier 当前 `go.mod` 是 `1.24.0`，单独评估是否同步升级。

重点文件：

- `go.mod`
- `notifier/go.mod`
- `Dockerfile`
- `notifier/Dockerfile`
- `.github/workflows/*.yml`
- `README.md`
- `CONTRIBUTING.md`

### 4. 补齐 PR CI 与本地聚合命令

问题：

- 根项目 CI 只在 main push 和手动触发运行，缺少 `pull_request`。
- `Makefile` 只有 dev 目标，但内部文档提到 `make ci`。

建议修改：

- `.github/workflows/ci-release.yml` 增加 `pull_request` 触发，发布和 Docker push job 仍只在 main push 运行。
- `Makefile` 增加：
  - `test`: `go test ./...` + notifier tests + frontend tests
  - `lint`: `go vet ./...` + notifier vet + frontend lint
  - `build`: frontend build + Go build
  - `ci`: 聚合上述命令
- 注意 Go embed：Go build 前需要存在 `internal/api/frontend/dist`，可以复用 `./scripts/setup-dev.sh --rebuild-frontend` 或在 Makefile 中显式处理。

重点文件：

- `Makefile`
- `.github/workflows/ci-release.yml`
- `.pre-commit-config.yaml`
- `scripts/setup-dev.sh`

## 上线建议

第一批上线步骤：

1. 实现第一批四项修复。
2. 跑：
   - `go test ./...`
   - `cd frontend && npm run build && npm run lint && npm run test`
3. 用本地或 staging 配置冒烟：
   - `/api/onboarding/test`
   - `/api/onboarding/submit`
   - `/api/change/auth`
   - `/api/change/submit`
   - 管理后台查看/批准/应用变更请求
4. 发小版本。
5. 观察自助收录和变更请求错误率。

第一批风险：

- 不改数据库结构，风险低。
- 如果生产中已有 pending 的 `category` / `sponsor_level` 用户变更，建议保留管理员手动处理能力，但禁止新用户提交。

第二批上线步骤：

1. 先升级依赖和工具链，在 staging 完整跑前端路由、admin、onboarding、change request。
2. 构建生产 Docker 镜像并验证 `/health`、`/ready`。
3. 准备回滚镜像 tag。
4. 单独发版。

第二批风险：

- React Router 和 Go 工具链升级属于供应链变更，建议不要和第一批混发。
- 新增 `/api/change/test` 时需保留旧端点，避免前端/后端灰度期间不匹配。

## 建议给新实现会话的启动提示

新会话可以直接使用以下目标：

```text
请在 relay-pulse 仓库中按 docs/developer/remediation-plan-2026-06-10.md 推进第一批低风险热修：
1. 禁止用户自助提交 category/sponsor_level，但保留管理员编辑能力；
2. proof 有效期由后端下发，前端不再硬编码 15 分钟；
3. 统一内联探测 sub_status，至少把 rate_limited 改为 rate_limit，并补前端显示映射；
4. 给 /api/change/auth 加 IP 限流；
5. 补对应测试并运行 go test ./...、frontend build/lint/test。

不要改 internal/api/frontend 生成目录；前端源码只改 frontend/。
```
