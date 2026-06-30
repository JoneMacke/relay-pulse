# CLAUDE.md

⚠️ 本文档为 AI 助手（如 Claude / ChatGPT）在此代码库中工作的内部指南，**优先由 AI 维护，人类贡献者通常不需要修改本文件**。
如果你是人类开发者，请优先阅读 `README.md` 和 `CONTRIBUTING.md`，只在需要了解更多技术细节时再参考这里的内容。

### 同步检查点
- **最后同步**: 2026-06-30（HEAD=fecbca6，已发版 **v2.55.2** + **已部署生产**[prod git_commit=fecbca6，go1.26.4，health=200，monitors=239；回滚锚点 `rollback-20260630-boardanchor-pre`=部署前 c09bc2d]）。本轮**一个修复（`internal/automove/service.go`，relaypulse-only，自动移板语义）**：**board 字段从"初始板位"改为"锚点/天花板"——配置板位决定自动移板上限、绝不向上越板**。configBoard=secondary 的通道任何可用率都不再自动升 hot（旧逻辑 avail≥threshold_up 即 promote），稳定留备板、仅 avail<threshold_cold 时冷板；configBoard=hot 的 hot↔secondary 双向迟滞不变；`purgeStaleOverrides` 同步清理备板遗留 hot override（热更新即时生效、不等下轮评估）。**用户报告**：配置为备板、未开 `auto_move_exempt` 的通道被自动移到主板（prod auto_move 启用，阈值 cold10/down50/up55）。**实现**=evaluate 的 switch 按 configBoard 分流（codex 挑出"仅删 promote 会留 secondary 孤儿 override"→改按 configBoard 分流根治，SESSION 019f1631）。**部署实证**：部署前 prod `monitor_overrides` board=hot 仅 1 行（modelflare/cx/o-pro-us，config board=secondary 被旧逻辑升板）→ 部署后首次评估 promoted=0、该 hot override 清除落回 secondary、secondary(4)/cold(63) 不变。验证：新增 5 锚点测试（含 20% 可用率存量回落用例）+ automove 全包 54 绿 + gofmt/vet/`go test ./...`/build 全绿 + codex review 无阻塞 + prod 端到端实证。文档：features.go/monitor.go 注释、docs/user/config.md、CLAUDE.md 设计原则 11 同步锚点语义。**⚠️检查点对齐**：此前 stale 在 v2.53.1/a64f8a2，中间 v2.54.0~v2.55.1 多版本（探针模板 cc-opus-ping/opus timing、admin loading 等）已陆续部署未逐条回填，prod 当前以 fecbca6/v2.55.2 为准（详见 `git log a64f8a2..fecbca6`）。**上一同步**: 2026-06-29（HEAD=a64f8a2，已发版 **v2.53.1** + **已部署生产**[prod git_commit=a64f8a2，go1.26.4，health=200，ready=200，monitors=239；回滚锚点 `rollback-20260629-updatemintid-pre`=部署前 v2.53.0/38cd89a]）。本轮**一个修复（`internal/config/monitor_store.go`+`cmd/migrate/main.go`，relaypulse-only，monitors.d 写路径稳定 id）**：**`MonitorStore.Update` 写盘前补 `BackfillFileIDs`**（与 `Create` 对称、幂等，已有 id 绝不覆盖），一处覆盖三条 Update 路径（admin 编辑 / toggle / change-request apply）。**修复事故**：admin「编辑→保存」给既有通道**新增子通道行时该行无 `model_id`**，触发 v2.53.0 的 `CheckRuntimeModelIDs` fail-closed **跳过整份配置热更新**（admin 保存返回 200、运行态却静默不变，且**连带卡死全站 monitors.d 热更新**）——owlai/cx/O-web 实锤（子通道无探测按钮 + 未被监测）。`cmd/migrate` 写盘前同补 `BackfillFileIDs` 封堵旁路（一次性迁移工具产物缺 id 会启动 `os.Exit`/热更新跳过）。**事故数据先用 `backfillids` 解封**（给 owl 子通道补 id → 热更新恢复、子通道进运行配置开始被探测）。**已确认非写路径**：手改磁盘 yaml 仍由 fail-closed 闸拦（启动 crash / 热更新跳过），符合预期不在修复内。codex 全程（根因确认 + 写路径枚举 + diff review APPROVE，SESSION 019f1351）。**观测性 follow-up 未做**：admin 保存 200 但热更新静默跳过、无回馈——留待单独定夺（翻 `/ready` 状态有 LB/编排重启抖动风险，倾向只在 `/ready` body 信息化、不翻 200/503）。验证：新增红→绿回归测试 `TestUpdate_MintsModelIDForNewChild`（复刻 owl 事故）+ gofmt/vet/`go test ./...`/build 全绿 + codex APPROVE + **prod 实测**（version=a64f8a2、health/ready=200、monitors=239、scheduler 起、owl 子通道 `layers=['GPT','gpt-5.4']` 已被探测）。**上一同步**: 2026-06-29（HEAD=38cd89a，已发版 **v2.53.0** + **已部署生产**[prod git_commit=38cd89a，go1.26.4，health=200，ready=200，monitors=238；回滚锚点 `rollback-20260629-plandid-pre`=部署前 v2.52.0/804690f]）。本轮 **通道稳定 id Plan D 增量1——relay-pulse 内部 `probe_history` 按稳定 `model_id` 重键**（补齐「改 model 展示名后内部历史不断档」那一半；跨产品 join 已在 A/B/C 交付）。**纯 additive 零 PK 改动**：`probe_history`(PK=id) 加 nullable `model_id` 列 + partial 索引 `idx_probe_history_mid_ts(model_id,timestamp DESC) WHERE model_id IS NOT NULL`（PG `CONCURRENTLY`+`to_regclass`+`INCLUDE`/SQLite 阻塞），**保留**旧 PSCM 覆盖索引作迁移窗口。**(1) 写路径**(`monitor/probe.go`)从 `cfg.ModelID` 写 model_id；**(2) 启动期幂等回填**(`cmd/server/main.go` 接 `MigrateChannelData` 后，`storage.BackfillProbeHistoryModelIDs` 按 config 映射补 legacy `model_id IS NULL` 行、歧义 fail-fast、出错 `os.Exit`)；**(3) 展示读全切 model_id**(`api/query.go` batch/serial/concurrent + DB timeline-agg、`monitor_handler.injectLatestProbe`、`status_query_handler` channelInfo 带 model_id；新增 `storage.ProbeHistoryKey` 独立键**不动**共享 `MonitorKey`；批查返回 map 按**输入 key 回填**不靠 DB 行 model 重建；**admin logs 故意保留 PSCM** 作孤儿/legacy 查询面)；**(4) fail-closed**(`config.CheckRuntimeModelIDs` 启动+热更新校验所有监测行有 model_id，与允许空的 `validateModelIDs` 分开避免 config 测试涟漪)；**(5) 顺带修 bug** `monitor_store.childMatchKey` 改两遍匹配(parent+model_id 优先/parent+model 兜底)——改子通道展示名经 admin 编辑不再丢 EnvVarName/RequestModel。**service_states/monitor_overrides 重键=Plan D-2 后置**（实测 prod `events.mode=model`，model 模式下改名仅短暂自愈无误报；channel 模式才需同批，故安全后置）。**部署实录**：8 task subagent-driven(每 task 子代理 TDD + 我 spec 检查 + codex per-task review + 整支集成评审 APPROVE，SESSION 019f10df)→**PG dry-run**(restore-local 灌 prod 1.24M 行真数据,harness 验 Init+CONCURRENTLY 索引+回填+按 id 单/批/timeline-agg 读全过)→merge main(8 commit eee60d1..38cd89a)→push→GHA 自动发 **v2.53.0**→/ops backup(rp-backups/20260629-110537)→/ops update→**prod 实测**:启动期回填 **1,134,635/1,149,583(98.7%,~15k NULL 孤儿=部署前改名/删通道历史,符合非目标)**+`idx_probe_history_mid_ts` valid+`/api/status` 79 层全出 24h 历史(model_id 读生效)+`/api/status/query` 正常+health/ready 200。**坑**:(a)启动期全量回填~60-90s 致 health 短暂 000 别误判崩;(b)relaypulse `/api/status` 强制 gzip,curl 验证用 `--compressed` 否则 406。验证:gofmt/vet/`go test ./...`/build(±postgres) 全绿。计划见 meta 仓 `docs/superpowers/plans/2026-06-29-channel-stable-id-plan-d-relaypulse-internal-model-id.md`。**上一同步**: 2026-06-29（HEAD=804690f，已发版 **v2.52.0** + **已部署生产**[prod git_commit=804690f，go1.26.4，build 2026-06-29T02:25:03+08:00，health=200，ready=200，monitors=237；回滚锚点 `rollback-20260629-channelid-pre`=部署前 v2.51.3/645f3e5]）。本轮 **通道稳定 id 跨产品 join（Plan A+C，relay-pulse 侧）+ 配套 rpdiag Plan B 同日部署**——给每通道不可变 `channel_id`（`ch_<uuidv4>`），质量列跨产品 join 从会漂移的展示名三元组切到 **`channel_id` 优先、三元组永久兜底**，根除展示名脆性。**Plan A**（`internal/config`+`internal/api`）：通道/行加 `channel_id`/`model_id` + 校验查重 + `/api/status`/admin 暴露 channel_id + `cmd/backfillids` 幂等回填 CLI。**Plan C**（`internal/rpdiag/client.go`+前端）：`buildScoresAt` 把 export 行按 legacy 三元组做**共识合并**——单一 cid 整组归 `"cid:"+id` 桶（含 cid 滞后 model 行）、0 cid 留三元组、**多 cid 或同 cid 跨 service fail-closed 退三元组+`logger.Error` 绝不静默挑 id**、改名 drift 多名半区按同 cid 合并；同 modelKey 去重 first-wins→**max-wins**（防 cid 合并让 export 顺序挑 stale 0，均分语义不变、按 modelKey 排序求和保确定性）；`MonitorGroup`+`ProcessedMonitorData` 透传 channel_id；前端 `lookupRpdiagScore` cid 优先 miss 退三元组。**部署实录**：push main→GHA→prod `update`(v2.52.0)→build `backfillids`(linux/amd64) scp 到 prod 跑、给 **211 个 prod monitors.d 文件**写 channel_id/model_id（hot-reload 自动吸，`AtomicWriteYAML` 同 admin CRUD 写法）→配套 **rpdiag B 同日上线**（migration 022 additive + export **v5.9** + sampler 捕获 cid，rpdiag 仓 a340697）→sampler `--once` 捕获→relay-pulse ≤10min cache 刷新后 `/api/rpdiag-scores` 出 **8 个 `cid:` 桶**（multi-model 通道 null-cid straggler 正确合并成 3-4 model/桶），余随 sampler jitter 周期逐步迁；**实锤修真实漂移**（ByteCatCode 通道 relay-pulse 名 `O-Web-Max`、rpdiag 名 `O-Max`，展示名 join 本 miss、cid 同 `ch_0814..` 成功 join）。codex 全程（设计裁 Design A+三元组共识合并、否决 alias-map、自己抓出 effective_model 致同 modelKey 跨名重复→促成 max-dedup）。验证：gofmt/vet/`go test ./...`/tsc -b/lint/vitest **197** 全绿（client.go 9 新测含 cid 桶/共识/anomaly/drift/straggler、前端 cid 优先查表、group 透传）+ 部署后端到端实证（export v5.9 出 cid、relay-pulse 8 cid 桶、id 对称、两站 200）。spec/计划见 meta 仓 `docs/superpowers/plans/2026-06-29-channel-stable-id-plan-c-relaypulse-join.md`。Plan D（内部 model_id 重键）未做。**上一同步**: 2026-06-26（HEAD=645f3e5，已发版 **v2.51.3** + **已部署生产**[prod git_commit=645f3e5，go1.26.4，build 2026-06-26T11:51:25+08:00，health=200，ready=200，monitors=237；回滚锚点 `rollback-20260626-qualitytip-pre`=部署前 v2.51.2/dd8eb70]）。本轮两个用户报告修复（纯前端，relaypulse-only，**质量列 tip**）：**(1) 表头浮层采样来源域名修正 + 做成外链**——错误的 `rpdiag.relaypulse.top` 改为正确的 `diag.relaypulse.top`，并把域名做成新标签外链（`<diag>` 占位 + 组件内 `Trans` 渲染、`target=_blank`+`noopener` 指向 rpdiag 站点本身；底部指向本站 `/detect` 的内链保留不动、二者并存）；4 locale 的 `qualityTooltip` 串同步把域名包成 `<diag>` 占位。**(2) 质量列单元格 tip 从浏览器原生 `title` 改为自定义浮层**——此前质量 sparkline 用原生 `title`，与通道列的自定义浮层视觉风格不一致（用户报告）。新增组件 `frontend/src/components/HoverTooltip.tsx`（从 `ChannelCell` 抽出 portal 到 `document.body`+定位+120/100ms hover 桥+scroll/resize 跟随+`bg-elevated`/边框/圆角/阴影 的单一真相源），通道列与质量列**共用**；质量列改结构化 per-model 行（模型名 + 30d/7d/近3次，硬失败追加高亮可用性提示），`formatModelTooltipRow`（拼串）重构为返回 `{key,detail,warning}` 的 `buildModelTooltipRow`，清理因抽组件而空出的死 import（`createPortal`/`useCallback`/`useRef`）。**纯 UI/presentation，rpdiag 零改动、Go 零改动；跳过 codex**（视觉 codex 盲，靠 playwright 实测验收）。验证：tsc -b/eslint/vitest **190** 全绿 + playwright 自包含 harness（build dist + 进程内 server 注入线上真 payload，绕开网络沙箱）实证：质量 tip 现为与通道列同款样式浮层（`fixed … bg-elevated border border-default rounded-lg shadow-lg`）+ 结构化两行、单元格无残留 `title`；表头链接 `href=https://diag.relaypulse.top target=_blank` 且无字面 `<diag>` 标签；通道列 tip 无回归；console 0 error + 部署后生产 **live bundle 实证**（`index-P2FO2GNT.js` 含 `diag.relaypulse.top`、零 `rpdiag.relaypulse.top`）。**上一同步**: 2026-06-26（HEAD=dd8eb70，已发版 **v2.51.2** + **已部署生产**[prod git_commit=dd8eb70，go1.26.4，build 2026-06-26T10:59:27+08:00，health=200，monitors=237；回滚锚点 `rollback-20260626-embed-pre`=部署前 v2.50.4/d29093a]）。本轮一批（纯前端，relaypulse-only，**服务商嵌入页 `?embed=1` 精简为纯数据视图**，三版顺序部署 v2.51.0→2.51.2）：给中转商外嵌的 `/p/<slug>?embed=1` 页去掉 relaypulse 平台外壳，只留可用性数据本体。**(1) 隐藏平台级控件**（`Controls.tsx` 加 `embed` 开关 + `ProviderPage` 传 `embed={isEmbedMode}`）：桌面与移动端的「收藏/订阅/板块切换」整组在嵌入模式隐藏（均为 relaypulse 全站功能、对单中转商嵌入无意义且把人导回本站），保留刷新 + 列表/网格切换。**(2) 隐藏「标注」列**（嵌入模式传 `enableAnnotations=false`；服务商列早有 `showProvider={!isEmbedMode}`）——赞助等级/公益/监测频率等编辑性徽章挂在中转商自己站上既尴尬又挤占数据。**(3) 修单行嵌入表凭空冒出的竖直滚动条**——根因=质量/价格列表头的 ⓘ 解释浮层（`absolute top-full`）是 `overflow-x:auto` 滚动容器（其 `overflow-y` 计算为 auto）的 DOM 后代，可见且向表下方探出约 50px 就贡献 scrollHeight、单行表上凭空生成滚动条。**修了三次才到位（教训：布局/溢出 bug 先量 DOM 实际成因再修，别假设亚像素，见 memory `feedback_measure_dom_cause_before_layout_fix`）**：v2.51.0 `overflow-y:hidden`（治标且 hover 裁浮层）→ v2.51.1 浮层隐藏态 `display:none`（只解决不悬停、hover 仍出滚动条）→ **v2.51.2 根治**=新增组件 `frontend/src/components/HeaderInfoPopover.tsx`，两处表头 ⓘ 浮层用 `createPortal` 渲染到 `document.body`+`position:fixed`（坐标由触发器 `getBoundingClientRect` 算），**脱离滚动容器**故 hover 与否都不撑 scrollHeight、也不被裁，mouseleave 留 120ms hover 桥保「中转站质量检测→」内链可点、触发器吞 click/keydown 不连带触发列排序。**(4) 开发支持**：`vite.config.ts` 加 `DEV_PROXY_TARGET` env 整体覆盖 dev 代理目标（指向线上只读数据做 UI 联调，不设时行为完全不变）。**纯 UI/presentation，rpdiag 零改动、Go 零改动；全程跳过 codex**（视觉/runtime 几何 codex 盲，靠 playwright 实测定位与验收）。验证：tsc -b / eslint / vitest **190** 全绿 + playwright 自包含 harness（build dist + 进程内 http server + route mock 线上真 payload，绕开网络沙箱，见 memory `reference_local_ui_verify_inprocess_server`）+ 生产真站 stellarfer 单行嵌入页双态实测（不悬停 deltaY=0；hover 质量 ⓘ deltaYWhileHover=0 无滚动条、浮层 portal 到 body 完整 111px 在视口内含内链）。**上一同步**: 2026-06-26（HEAD=d29093a，已发版 **v2.50.4** + **已部署生产**[prod git_commit=d29093a，go1.26.4，build 2026-06-26T09:53:51+08:00，health=200，monitors=237；回滚锚点 `rollback-20260626-providerjoin-pre`=部署前 v2.50.3/d6b1582]）。本轮一个修复（纯前端 `frontend/src/hooks/useRpdiagScores.ts` + 3 调用站，relaypulse-only，**跨产品质量列 join 的 provider 段**）：**质量列 provider 段从「relaypulse slug（`item.providerId`）」改为「展示名优先、slug 兜底（`[providerName, providerId]`）」**——生产方索引（`buildScoreRowView`）按 `canonical(provider_name)` 展示名建 key，前端却用 slug 查表；88/90 家 slug==展示名小写故对得上，但 slug≠展示名的服务商落空：**WorldBase.ai**（slug `worldbase` vs 名 `worldbase.ai`）、**YunWu**（slug `yunwui` vs 名 `yunwu`）质量列空白，尽管 rpdiag 有分（71.7/19.5）。这是 v2.50.3 给 channel 段修过的 slug 漂移在 provider 段的同类问题。`lookupRpdiagScore` 的 provider 参数改收候选数组按序查表（展示名修好漂移服务商；slug 兜底保证展示名缺失/为空白/与 rpdiag 不同步时历史能 join 的通道**零回归**，防 codex 提的空白 `provider_name`/desync 反例）；4 调用站（StatusTable×2/useMonitorData/DetectPage）传 `[providerName, providerId]`。**rpdiag 零改动、后端零改动**（后端索引一直按 provider_name 建 key，对的；bug 只在前端查表用错标识）。生产数据实测：当前 slug 命中 88→改后 90、回归 0、修复 2。codex 两轮（原型对抗 + diff review）无阻断，纠正一处注释（`providerSlug`→`providerId`）。验证：tsc -b/eslint/vitest **190** 全绿（新增 8 例覆盖 worldbase/yunwu/空白兜底/单串 back-compat）+ 部署后 **生产 live DOM 实证**（relaypulse.top/p/worldbase 三 model 质量分 haiku/sonnet/opus 全出、/p/yunwui sonnet=39 + opus 不可测；此前两站均空）。同步更新 `docs/contract-ranking-export.md` 不变量 1（顺手修其 channel 段「剥前缀」的 stale 描述）。**上一同步**: 2026-06-26（HEAD=d6b1582，已发版 **v2.50.3** + **已部署生产**[prod git_commit=d6b1582，go1.26.4，build 2026-06-26T09:16:54+08:00，health=200，monitors=237；回滚锚点 `rollback-20260626-rpdiagjoin-pre`=部署前 v2.50.2/8d64b0a]）。本轮一个修复（`internal/rpdiag/client.go` + `frontend/src/hooks/useRpdiagScores.ts`，relaypulse-only，**跨产品质量列 join**）：**质量列 join 的 channel 段从「剥前缀的 relaypulse_channel_key」改为「原始 channel_name（仅 trim+lower）」**——剥 O-/R-/M-/U- 前缀会把仅靠前缀区分的通道折叠：某商 `o-cx`(付费档)/`u-cx`(free档) 两 codex 档剥完都塌成 `cx`、挤进 `provider|cx|cx` 一格、4 model 挤一格分不开（item「right 服务商」用户报告）。全量实证（线上 157cc+98cx export × 57 监测）：剥前缀对 **0** 通道有用、54 裸名直配、3 个 `M-` 空名裸名更干净。改 `buildScoreRowView` channel 段用 `canonical(row.ChannelName)`（删死函数 `NormalizeChannelKey`）+ 前端 `buildRpdiagKey` 去 `stripChannelPrefix`；`relaypulse_channel_key` wire 字段保留 decode 但不再 join 用、**rpdiag 零改动**。`buildScoresAt` 活跃模型均分/stale/hard-fail/`(service,modelKey)` 分桶逻辑不变。codex 两轮 review（方案+diff，仅指出未跟踪测试已 add + 一处 stale 注释已修，无运行时风险）。验证：gofmt/vet/`go test ./...`/tsc -b/vitest 186 全绿（删 TestNormalizeChannelKey、~16 处 key 断言改 raw、新增 `TestBuildScoresKeepsRawPrefixedCodexChannelsSeparate` + 前端 `useRpdiagScores.test.ts`）+ 部署后 `/api/rpdiag-scores` 实测 `right|cx|o-cx`(93.5/2model)/`right|cx|u-cx`(50/2model) 两格分立、折叠键 `right|cx|cx` 消失、94 key 无回归（其它 cx 通道现带原始前缀名如 `0-0|cx|o-team/plus`）。**上一同步**: 2026-06-24（HEAD=8d64b0a，已发版 **v2.50.2** + **已部署生产**[prod git_commit=8d64b0a，go1.26.4，build 2026-06-24T08:15:30+08:00，health=200，rpdiag_enabled=true、88 质量通道；回滚锚点 `rollback-20260624-optout-revert-pre`=部署前 v2.50.1/7e6259e]）。本轮两个用户报告修复（睡前批 + 次日澄清回退）：**(1) 质量列默认语义=opt-in（默认关）——v2.50.2 已回退**：v2.50.1(7e6259e) 一度把 `enabledFromEnv` 误翻成 opt-out（默认开），**经用户澄清『质量列默认关闭、relaypulse.top 是经 `/opt/relaypulse_pg/.env` `MONITOR_RPDIAG_ENABLED=1`（2026-05-23 起）配置才开』后，v2.50.2(8d64b0a) 已 `git revert` 回退为 opt-in**（默认关、仅 `1/true/yes/on` 开），恢复原始设计。`enabledFromEnv`/`NewClientFromEnv`/handler 注释/`docs/user/config.md`/`TestEnabledFromEnv` 全部回到 opt-in。**prod 始终经 .env 显式开启，v2.50.1↔v2.50.2 来回对现网行为零变化**（列一直在），仅来回切代码默认语义。**教训**：用户「默认关但 prod 开」是**陈述期望态/求确认机制**，不是「让你把默认翻过来」——动默认/语义前先 pin 方向再做（见 [[feedback_confirm_state_vs_fix_intent]]）。**(2) 全失败通道质量列灰格可点击（跨产品，改在 rpdiag 侧）**——rpdiag `_build_unavailable_export_rows` 的 `detail_url=None`（never-scored 的 `/channel` 页因 history descriptor 要求 `ranked_task_count>0` 会 dead-end）改为指向 scoped `/ranking` 看板深链；relaypulse `channelURLFromDetailURL` 自动派生出非空 ChannelURL → 灰格可点击。**relaypulse 端零改动**（route-agnostic 自动跟随，≤10min cache TTL 生效）。生产实测：6 个全灰通道（aiberm/anyrouter/dbai cc + dbai/jucodex/aitokensflux cx）detail_url 从 null→`/ranking?provider=..&service=..&channel_name=..[&test_case=codex板]`，relaypulse `/api/rpdiag-scores` 这 6 个 channel_url 全非空（混合通道 aitokensflux/cc 仍走 scored 行 `/channel`，符合预期）。rpdiag 侧 commit ce7f12c（additive，不 bump schema，仍 v5.6）。**第三项「diag.relaypulse.top 首页慢」本轮未修**：实测**非下载问题**（站在 Cloudflare 后、zstd 压缩，首页 wire 仅 87KB），瓶颈是 **TTFB ~1.5s**（Astro SSR 阻塞在 `/api/v1/task-groups` ~1s + `cf-cache-status: DYNAMIC` 无边缘缓存）+ **1.6MB DOM 客户端 hydration**（首页 task-groups island `initialGroups` 含 488KB members，607 组喂全量供客户端筛选）——属服务端/hydration 性能取舍。**用户已明确『要从根本解决』**→ 取根因修法（SSR 让 island 不喂全量 members 减 hydration + 加速 task-groups TTFB），**不取 CF 边缘缓存 band-aid**；待 /compact 后推进（见 memory `project_pending_followups` item 16）。**上一同步**: 2026-06-24（HEAD=432a3af，已发版 **v2.50.0** + **已部署生产**[prod git_commit=432a3af，go1.26.4，build 2026-06-24T01:12+08:00，health=200，monitors=231；回滚锚点 `rollback-20260624-detect-pre`=部署前 v2.49.0/dde7d92、`rollback-20260624-cxquality-pre`=更前 v2.48.14/261c8cd]）。本轮两个独立分支顺序部署：**(1) v2.49.0(dde7d92) 质量列合并 codex 板**（`internal/rpdiag/client.go`，relaypulse-only，**跨产品质量列**）——原只拉 claude 板（`test_case=quick-probe-v1`），改 `boardURLs=[claude板, codex板(同URL+test_case=quick-probe-codex-v1)]` 多板拉取 merge；join key=`provider|service|channel`（claude→cc/codex→cx，**service 入 key 故同名通道跨服务不撞**），**all-or-nothing**：任一板 fetch 失败整体 return err 走既有 stale 快照回退（**刻意不用 partial-success**——只拉到 codex 板写缓存会把 claude 列抹掉续满一个 TTL，见 `feedback_codex_graceful_degrade_blind_to_shared_state`）。两板串行（慢板拖该次 refresh，但各板 10s 超时上限 + singleflight 收敛 + 小时级 + fail-open 回 stale，不阻塞主表）。**零前端改动**（`lookupRpdiagScore` 早按 service 分桶）、**零 rpdiag 改动**。验证：gofmt/vet/build/`go test ./internal/rpdiag` 全绿（新增 URL 派生/双板 merge/同通道跨 service 不撞键/board 失败回退 stale 四测）+ 部署后 `/api/rpdiag-scores` 实测 **42 cc + 44 cx = 86**（cx 通道质量分上线）。**(2) v2.50.0(432a3af) 中转站检测专题页 `/detect` + rpdiag 运行时门控**（`internal/api/{meta,handler,server,query}.go` + `frontend/{pages/DetectPage,components/Footer,components/StatusTable,router,App,...}` + 4 locale 各 +123 键 + `docs/user/config.md`）——新增 `/detect` SEO 落地页（实时质量榜 + 检测能力介绍）；新增 `Handler.rpdiagEnabled()`=`rpdiagClient!=nil` 作**单一信号源**，门控「质量列 / `/detect` SSR 可索引性 / sitemap 收录 / Footer 入口 / 前端 `rpdiag_enabled` flag」——私有部署未接 rpdiag 时全部一致消失（`/detect` 退化 noindex、不进 sitemap、Footer 不渲染入口；路由仍客户端可解析=fail-open）。生产 rpdiag 启用态：`/detect` 注入专属 title/canonical(`https://relaypulse.top/detect`)/hreflang/JSON-LD(@graph WebPage+Breadcrumb)、sitemap 四语收录、**不发 lastmod**（静态正文避免假新鲜度，与 contact 同口径）。**约束**：detect title/description 是 raw 插入 index.html，改文案须避裸 `"`/`<`。codex 评审两分支各 5 对抗问全过、判可部署（detect 门控分支可达 / 启用态 SEO 自洽 / 启动时序 `SetRpdiagClient` 在路由前完成故无锁假设成立）。验证：gofmt/`go test ./...`/tsc -b/vitest 182 全绿 + i18n 四语言 709 叶子键齐 + 部署后生产实测（`/detect` 200+canonical 非 noindex、sitemap `/detect /en /ru /ja`、公网 relaypulse.top/detect 200）。**与 parked `feat/free-listing-pricing`（改价/收录）无耦合，不触发其 go-live hold。** **上一同步**: 2026-06-22（HEAD=261c8cd，已发版 **v2.48.14** + **已部署生产**[prod git_commit=261c8cd，go1.26.4，build 2026-06-22T16:21:56+08:00，health=200；回滚锚点 `rollback-20260622-seo-pre`=部署前]）。本轮主项 SEO（`internal/api/meta.go` + `handler.go::buildSitemapXML`，**跨产品 SEO 第一阶段 relaypulse 侧**，v2.48.14/261c8cd）：**`/contact` 独立 meta/canonical + 实时页 sitemap lastmod**——此前 `/contact` 套首页 title/canonical（SSR meta 注入 bug，爬虫/社媒卡读到错的页面标识）。修法=`meta.go` 加 `trimLanguagePrefix`+`parseStaticPath`+`MetaData.StaticPath`+`getMetaContent` 第 5 参 `staticPath` 的 contact 分支（4 语言文案**复用前端 `contact.meta.*` i18n 串**保爬虫/CSR 一致）+ `generatePageMeta` static canonical/hreflang/OG + ContactPage JSON-LD；`buildSitemapXML` 只给**实时刷新页**（首页+服务商页）发 `lastmod`=今日 UTC（诚实新鲜度），**静态 contact 页不发 lastmod**（不每天变、避免假新鲜度被贬权）。配套 rpdiag 侧独立部署（78a3c57+193bbd6：data-driven sitemap 补全通道页 + canonical 四元组 + 社交分享大图），实现细节见 memory `reference_rp_seo_implementation`。Search Console 两站 sitemap 已提交（relaypulse.top 网域资源覆盖两站，2026-06-22）。**v2.48.3→v2.48.13 同批已部署 delta（补记 changelog 漂移，非本轮新写）**：① UI——**v2.48.13**(f316144) 质量列 sparkline 随列宽自适应铺满（去 SVG viewBox、节点 cx 改列宽百分比、polyline 拆逐段 `line`，圆点保圆；4-locale playwright 实测）、**v2.48.12**(e5fb7b0) 压缩状态表列宽（长 locale 下趋势热力图不再被挤瘦截断）；② (2a2a1d4) 删除自助申请「状态查询」页；③ i18n——**v2.48.3**(275034c) 等回填 admin.detail/change-request/status-query/onboarding 各 locale 缺键 + locale parity guard；④ deps（**v2.48.4–v2.48.11**，构建工具链 currency，均不改运行时行为语义）——golang 1.25→1.26、alpine 3.23→3.24、前端构建 node 20→24-alpine、vite 7→8(rolldown)、eslint 9→10+react-hooks 7.1、@types/node 25+jsdom 29、react 19.2.7/vitest 4.1.8（npm-frontend group）、pgx 5.10/x/net 0.56/sqlite 1.52（gomod-root group）。**上一同步**: 2026-06-13（HEAD=5a63b71，已发版 **v2.48.2** + **已部署生产**[prod git_commit=5a63b71，build 2026-06-13T12:51+08:00/go1.25.11，health=200；回滚锚点 `rollback-20260613-anyrouter-opus`=部署前 v2.48.1/449344c]）。本轮一个改动（`templates/cc-opus-arith-anyrouter.json`，**探针模板**）：**补全 anyrouter opus 客户端指纹以通过上游 WAF**——anyrouter 对 opus 系列做 Claude Code 客户端指纹校验，缺指纹一律 503/520 "Service Unavailable"（haiku **不**校验、裸 4 头即通），原模板只发 4 头 + opus-4-7、长期假红。2026-06-13 **抓真实 claude-cli 包逐项 bisect 实证**放行三闸：① header——`anthropic-beta` 含 `context-1m-2025-08-07`（缺→400「请启用 1m」）；② body——`system` 含 SDK 身份串 `You are a Claude agent, built on Anthropic's Claude Agent SDK.` 且 `metadata.user_id` 非平凡（relay-pulse `{{USER_ID}}` 格式即可、device_id 真假不校验只看形状）；③ edge——完整 SDK 头集（UA `claude-cli/...` + `x-app: cli` + `anthropic-dangerous-direct-browser-access` + `X-Stainless-*` 家族），缺则 ESA 边缘 520 `http_response_incomplete`。三闸缺一即拒；`[1m]` 模型名后缀无效。修复：request_model `opus-4-7`→`opus-4-8` + 补齐上述指纹头 + `_comment` 注释防后人误删（**不动 `model="Opus"` 展示名以保历史**）。**纯探针模板改动，无前端/Go/rpdiag 改动**；templates 是 COPY 进镜像（非挂载非 go:embed）→ 改模板必须 push→GHA build→prod pull、无 scp 捷径。本轮属 runtime/上游行为取证，**codex 静态分析够不到、全程跳过 codex**（判断正确，见 `feedback_codex_static_blind_to_runtime`）。验证：跑 relay-pulse 自身 `ResolveSingleMonitor`+`InjectVariables` 组装**真实请求字节**打 anyrouter→200 + 算术答案命中（金标准=验组装字节非脑补）+ 部署后 admin probe（target_model=Opus、via_proxy=true→http 200→通道 status=1 绿）。配套沉淀 `.claude/skills/relay-client-gate` skill（抓包→bisect→修模板→harness 验证→部署全链路 + capture/bisect 两脚本）。〔中间版本（均已随更早部署上线、正文已记，此处补记 checkpoint 连续性）：**v2.47.4**(1a6d33c) 质量探测缺口前端文案「不可用」→「不可测」；**v2.48.0**(747cd08) admin 逐子通道探测 + sub-status 细节 + 配代理自动走代理（`via_proxy`，SSRF 硬边界=**仅** admin 探测传 `WithProxy`）；**v2.48.1**(449344c) body-read 超时归 `response_timeout` 非 `response_too_large`。〕**上一同步**: 2026-06-12（HEAD=cf02852，已发版 **v2.47.3** + **已部署生产**[prod git_commit=cf02852，health=200；回滚锚点 `rollback-20260612-v2472`=部署前 v2.47.2/6a180db]）。本轮一个改动（client.go，relaypulse-only，**质量列排名语义**）：**通道排名键从"模型最高分 max"改为"活跃模型均分"**——上一轮把陈旧/故障 model 的 rankLatest 归 0 后仍用 `max()` 聚合，导致"四缺三、只剩 haiku 能测"的通道（如 **TopRouterCN**）靠单个幸存模型顶在 97，`max` 把"可用面"信息吃掉。改 `buildScoresAt` 为两遍扫描：**pass1** 在可消费视图（抽出 `buildScoreRowView`，复用 hard-fail/stale/fresh 判定）上构建全局**活跃模型集** `activeModels[service][modelKey]`（fresh=非 hard-fail && 有样本 && 未超 7d）；**pass2** 照旧 append 展示行（`ModelScore.Score/Trend` **一字不动**），同时对"命中活跃集且该通道该 modelKey 未计过"的行累加 `rankLatest` → `MaxScore=sum/count`。**退役模型**（全站 0 fresh，如 opus-4-7）从所有通道**分子分母整体剔除**（不偏袒不连坐）；仍活跃但本通道 hard-fail/stale 的模型计 0；**全退役通道 → MaxScore=nil 沉最底**（`*float64` 零值，无需显式置）。`ChannelURL/Trend` 改取**首条可解析 detail_url 行**（均分后无单一"最高分行"；Trend 取首行、前端不消费）。`max_score` wire 字段名保留（前端/排序都消费），仅更新 doc 注释。**零前端改动 + 零 rpdiag 改动**。codex 三轮（需求/原型/review）——补强 `(service,modelKey)` 分桶防跨 service 串扰 + modelKey 去重防分母放大 + ChannelURL 不再依赖最高分行；review 无阻断。验证：gofmt/vet/`go build ./...`/test 全绿（重写 5 个旧 max 假设测试加 fresh sibling 反映生产 + 新增 TopRouterCN 头条 `(97+0+0)/3≈32.3`/退役剔除/全退役 nil/去重/model_key fallback/ChannelURL 跳空首行 等 8 个）+ **生产原始 152 行 export 跑新逻辑实测**（TopRouterCN 97→**32.3**、saiai 100、hongmacc 89→78、ddshub/tokaify/94lover 0 沉底）+ **部署后生产 `/api/rpdiag-scores` 复核**（toproutercn max_score=32.3、saiai=100 一致）。**上一同步**: 2026-06-12（HEAD=6a180db，已发版 **v2.47.2** + **已部署生产**[prod git_commit=6a180db，health=200；回滚锚点 `rollback-20260612-v2471`=部署前 v2.47.1/7c43f0e]）。本轮一个改动（client.go，relaypulse-only，**质量列排名语义**）：**陈旧信号 model 排名归 0、展示保真**——某 (channel,model) 行最新指纹样本超 7 天（如 sampler 退役的 opus-4-7，分被冻结）此前仍以冻结高分参与通道 MaxScore，把"测过几次后再没测到"的通道顶到前面。改 `buildScores`：拆 `displayLatest`（喂 `ModelScore.Score/Trend`，**原样如实**——折线按真实历史分着色、tooltip 真实，唯一的灰仍只给 hard-fail）与 `rankLatest`（喂 `MaxScore`——hard-fail/stale>7d→0）。`max()` across models 意味单个 stale model 只在**通道全 model stale/hardfail** 时才沉底，有 fresh model 仍主导（hongmacc 由冻结 opus-4-7=95 回落真实 sonnet 89；ddshub/tokaify/94lover 全 stale→0 沉底）。删掉中途试过的 stale-红0 合成点方案（用户嫌概念多："如实画趋势、灰只留特例不可测、排名不可测判0"）。`scoreStaleWindow=7d`（对齐 rpdiag hard-fail 窗）、`nowFn` 可注入测时钟、`isStaleScoreTrend` 用 `RFC3339Nano`（latest_at 带微秒）+ 缺失 fail-closed。**零前端改动 + 零 rpdiag 改动**（折线/tooltip 用 `m.trend`、排序用 `max_score`，全未动）。codex 三轮（需求/原型/review）——其原型用 `RFC3339` 我实测两种都解析故订正注释、并在 caught 其 stale-红0 与"如实展示"冲突后简化为 display/rank 解耦。验证：gofmt/vet/test 全绿（新增确定性时钟 + stale-rank-0/fresh-sibling-dominates 测试）+ **生产 `/api/rpdiag-scores` 实测**（ddshub/tokaify/94lover max_score=0、退役 opus-4-7 各处展示真实 88/93/95/100 且 `recent_attempts=[]`、全站合成红0=0）+ React-fiber 读排序数组确认 0 分沉到有分段最底、null 更后。**上一同步**: 2026-06-12（HEAD=7c43f0e，已发版 **v2.47.1** + **已部署生产**[prod git_commit=7c43f0e，health=200；回滚锚点 `rollback-20260612-recent7d`=部署前 v2.47.0/c73c4dd]）。本轮一个修正（client.go，**跨产品**）：**recent_attempts 空数组保真**——配合 rpdiag v5.5 把 `recent_attempts` 收窄到近 7 天，`ScoreTrend.RecentAttempts` 去掉 `omitempty`，让上游空 `[]`（"近 7 天无探测"）re-serialize 到前端仍是 `[]`（前端 `Array.isArray([])`→不画近况点）、而 absent/null（老 wire）才回退 recent_scores；`cloneScoreTrend` nil 守卫早保 nil-vs-empty 之分，补注释 + nil/empty/有值 JSON 往返单测。前端**零改动**（现有逐元素 null 渲染对 `[]` 已正确）。**配套 rpdiag baab387**（v5.5：recent_attempts 7d 界 + 合成行排除判据 `channel_type != official`→`official_api_key_ciphertext IS NULL`，让中转商 O- 通道从未打分模型的 403 显示成灰）。codex 三轮（完善/原型/review）无阻塞、2 建议采纳（SQL shape 断言收紧 + Go 往返测试）。验证：rpdiag pytest 69 / prod DB read-only 实跑（O-Max opus-4-8 现返回 unavailable、opus-4-7 7d 窗空）；relaypulse gofmt/vet/test 全绿 + 生产 **live DOM 实证**（TopRouterCN O-Max 15 圆点=6 彩 9 灰：opus-4-8 整条 5 灰线、opus-4-7 仅 1 个 30d 彩点；tooltip opus-4-8 `近3次=不可用×3`、opus-4-7 `近3次=—`）。**上一同步**: 2026-06-12（HEAD=c73c4dd，已发版 **v2.47.0** + **已部署生产**[prod git_commit=c73c4dd，health=200；回滚锚点 `rollback-20260612-recentattempts-pre`=部署前 v2.46.0/2af3bb9]）。本轮一个功能（前端渲染 + client.go 新字段，**跨产品**）：**质量列 sparkline 近 3 槽改画"最近 3 次 terminal 尝试结局"**——消费 rpdiag **ranking-export.v5.4** 新增的 `score_trend.recent_attempts`（最近 ≤3 次质量相关尝试，`float`=打分、`null`=hard-fail）。`StatusTable.tsx::QualityScoreCell` 从"整行 `failed` 特判"改成**逐元素判 null**：slot0/1 仍是 30d/7d 均值（有数着色、无数留空、**绝不涂灰**），slot2/3/4 右对齐 recent_attempts，number→`qualityScoreColor`、null→中性灰贴底；连接线在彩↔灰逐段渐变，把"刚崩/已恢复"如实画出。**保留 Request A 的"纯不可用整条 5 灰点"**（无任何彩色节点时走该分支）。旧 wire（无 recent_attempts）完全回退既有 recent_scores 路径。`client.go::ScoreTrend` 加 `RecentAttempts []*float64`（normalizeHardFailTrend 透传不清空、cloneScoreTrend 深拷），**代表分仍用 recent_scores[-1]/latest 不动**；tooltip 近3次同源 recent_attempts。**配套 rpdiag 768db99**（v5.4，单产品独立部署，3 容器全 recreate，readyz=200，同名回滚锚点）。**部署序：relay-pulse 先（已就绪、向后兼容空 wire 无视觉变化）→ rpdiag 后**。codex 三轮协作 LGTM，我 override 它 3 点（never-scored 不改 nil 否则 DBAI 整格消失 / 不加 hard_fail_streak fallback / 代表分语义不动）。验证：rpdiag pytest 64 / SQL pg-dialect 编译 + **prod DB read-only 真实值核对**（O-Max sonnet `[null,null,null]`+仅历史 90 / haiku `[null,null,97]` / opus `[null,78,88]`）；relaypulse gofmt/vet/build/tsc -b 全绿 + 生产 live DOM 实证（O-Max 13 圆点=7 彩 6 灰，sonnet=1 彩 90+3 灰；DBAI 15 全灰 5 点线）。**上一同步**: 2026-06-12（HEAD=2af3bb9，已发版 **v2.46.0** + **已部署生产**[prod git_commit=2af3bb9，health=200；回滚锚点 `rollback-20260612-recent3-pre`=部署前 v2.45.3]）。本轮一个改动（纯前端）：**质量列 tooltip 把 `latest=` 单点换成 `近3次=a, b, c`**——`StatusTable.tsx::formatModelTooltipRow` 改用 `recent_scores.slice(-3)`（升序，与 sparkline slot 2/3/4 同源），让 tooltip 读全 5 槽位（30d / 7d / 近3次）；故障态行最后一个合成 0 显示"不可用"；v5.1 无 recent_scores 时回退单 latest。验证：tsc -b/eslint/vitest 177/build + 生产 relaypulse.top live DOM（24 cell title 含"近3次"，纯不可用行 `近3次=不可用 ⚠ …`）。**配套 rpdiag 改动**（c3d1be1，单产品独立部署，3 容器全 recreate）：hard-fail `availability_warning` 从 verdict 式 `"最近一次评测崩在评分前；当前不可用"` 改成纯事实 `"最近一次评测未取得可评分响应"`——理由：触发门是单次最新失败(streak 阈值 1) + 最长 7 天旧证据，"当前不可用"的现时可用性判定 overreach，且 rpdiag 是质量仪表非可用性裁判（见 [[feedback_no_verdict_only_weighted]]）；relaypulse 原样显示该串、零改动，cache TTL 10min 后刷新。**上一同步**: 2026-06-12（HEAD=782eb02，已发版 **v2.45.3** + **已部署生产**[prod git_commit=782eb02，health=200；回滚锚点 `rollback-20260612-greyline-pre`=部署前 v2.45.2]）。本轮一个改动（纯前端 + 一句 Go 注释订正）：**质量列"不可用 model"从孤立灰点改为整条 5 槽位灰线**——`StatusTable.tsx::QualityScoreCell` 重写为统一节点模型：每个节点自带颜色（真实分走 `qualityScoreColor`，不可用终点走中性灰 `UNAVAILABLE_COLOR=hsl(0 0% 55%)`）并各贡献一个 gradient stop，于是连接线在每个顶点=该点色、每段是两端点渐变（含"彩→灰"末段）。① **纯不可用**（无任何质量历史，wire `avg30/avg7=null, recent=[0], failed`）：不再画孤零零一个空心灰 marker，改画贯穿 5 槽位、贴底的整条灰实心点线，读成"测不到分"而非看不懂的角落点；② **曾测到分→现失败**：真实彩色点保持各自高度，末尾失败点贴底画灰，最后一段从彩色渐变落到灰。删掉旧的灰虚线 connector + 空心 marker 特例分支（净减 8 行）。配套订正 `internal/rpdiag/client.go::normalizeHardFailTrend` 一句过时注释（还写着失败点"rendered as a red bottom dot"，Part 1 起早是灰）。**纯 presentation，rpdiag 零改动、无跨产品部署序**（不可用行早在 v5.3 wire 上）。验证：tsc -b / eslint / vitest 177 / vite build 全绿 + playwright SVG harness ×8 放大 4 case（健康/纯不可用/多点彩→灰/单点彩→灰）DOM 结构逐一核对 + 生产 relaypulse.top live DOM 实证（127 质量 svg，2 条纯灰线 + 10 条彩灰混合，grey 圆点 75 个）。**上一同步**: 2026-06-12（HEAD=ddae891，已发版 **v2.45.1** + **已部署生产**[prod git_commit=ddae891，health=200；回滚锚点 `rollback-20260612-gradient2-pre`=部署前 c04c468]）。本轮一个修正（纯前端）：**质量列趋势连接线渐变由"整条首→末两色"改为"相邻两点逐段渐变"**——`StatusTable.tsx::QualityScoreCell` 每条 series 的 `<linearGradient>`（仍 `userSpaceOnUse` 横向 x1=首点x x2=末点x）由原来的 2 个 stop（startColor/endColor）改为 **N 个 stop**：每个点一个，`offset=(p.x−x0)/span`、`color=qualityScoreColor(p.value)`。因点沿 x 单调递增，相邻 stop 之间正好覆盖该段，于是线在每个顶点处=该顶点圆点色、每段是其两端点的两点渐变（线完全贴合圆点，不再让中间点真实色被首尾两色糊过去）。保留单条 polyline（linejoin 平滑）+ `useId` 命名空间化 gradient id。验证：tsc -b / eslint 0err / vitest 177 / vite build 全绿 + 独立 SVG harness playwright 实证绿→橙→绿→红逐段渐变 + 生产 relaypulse.top DOM 实证（81 gradient，stop 数分布 {2:7,3:6,4:18,5:50}=每点一 stop，0 console err）。**上一同步**: 2026-06-12（HEAD=c04c468，已发版 **v2.45.0**[此版渐变为"整条首→末两色"，几分钟后即被 v2.45.1 的逐段渐变取代]，回滚锚点 `rollback-20260612-gradient-pre`=部署前 04d0a2d）。质量列趋势连接线由单色(按最新分着色)首次改为渐变；`useId` 命名空间化 + 圆点仍各自按自身分着色 + 单点 series 不画线。**上一同步**: 2026-06-12（HEAD=04d0a2d，已发版 **v2.44.0** + **已部署生产**[prod git_commit=04d0a2d，health=200，monitors=221；回滚锚点 `rollback-20260612-quality-pre`=部署前 175dc13]）。本轮一个功能：**质量列把 rpdiag 测试失败通道记为 0 分（红点贴底）**——relaypulse 消费 rpdiag ranking-export 早已带的 `hard_fail_active`/`availability_warning`（**rpdiag 零改动**），在 `internal/rpdiag/client.go` 把硬失败 (channel,model) 行归一化为代表分 **0**（新增 `normalizeHardFailTrend`：latest=0、latest_at 置空、recent_scores 取末 ≤2 真值再 append 0、**fresh slice 不碰 decode 共享 backing array**；`cloneScores` 顺带深拷 RecentScores），`buildScores` 不再因 `latest==nil` 跳过该行（修"故障通道从列表消失/残留过期绿线"误导），仍受 submission_source=user / 空值过滤；`ModelScore` 加 `Failed`/`AvailabilityWarning`，`rankingRow` 绑 `hard_fail_active`/`availability_warning`（**没绑没人用的 hard_fail_streak**）。**MaxScore 仍取通道内各 model 代表分 max**（partial fail 不拖垮健康 model）。前端**零渲染改动**——0 经现有 `qualityScoreColor`(0=红)+`qualityScoreYNorm`(0=贴底)自然画成红点贴底，只在 `formatModelTooltipRow` 末尾追加 `⚠ availability_warning`（verbatim 中文，i18n 债）+ cell 注释；排序 `compareQualityScore` 早已视 0 为有数据→排 null 之上，`?.max_score ?? null` 保 0，二者无需改。实测唯一命中 **TopRouterCN O-Max**（haiku `[96,97,0]`+sonnet `[90,0]` 红 0、opus 仍 88、channel max_score=88）。codex 三轮协作（plan/原型/review）LGTM，我收敛了它过度包含的 hard_fail_streak。验证：gofmt -l 空 / go vet / go test ./internal/rpdiag / tsc -b / vitest 44 / eslint 全绿。**上一同步**: 2026-06-11（HEAD=175dc13，已发版 **v2.43.1** + **已部署生产**[prod git_commit=175dc13，health/ready=200，monitors=221；回滚锚点 `rollback-20260611-uipolish-pre`=部署前 90c7a55]）。本轮一批 **收录/后台/变更三块 UI 一致性打磨**（5 commit，纯前端，无后端/行为语义变化）：① 申请向导抽 `frontend/src/components/onboarding/controls.ts` 单一样式源（input/select/label/hint/主次按钮 className 常量，roomy 规格 px-4/rounded-lg/ring-2），ProviderInfoStep 服务商名校验改失焦后触发（touched 态）+ aria-invalid/aria-describedby，ConfirmStep proof 过期/预警文案补 i18n（`confirm.proofExpiredBanner`/`proofExpiringSoon` ×4 locale）+ 错误容器 role=alert；② 后台抽 `frontend/src/components/admin/fieldStyles.ts`（fieldInputClass/fieldSelectClass/fieldShapeClass，dense 形参——统一设计语言但保留各上下文密度），FormControls/MonitorDetail/MonitorForm/SubmissionDetail 对齐 + 子通道删除 `&times;`→lucide X + aria-label，SubmissionDetail 驳回框 ring-accent→ring-danger（修边框/ring 异色）；③ 公开变更向导 ChangeRequestPage 全量切 controls 共享源并对齐申请向导（字段 label→labelClass、多通道分支返回链接 inline-flex 恢复图标间距、ConfirmStep 提交按钮→primaryButtonClass）。验证：tsc -b/eslint/vitest 175/build 全绿 + 重构段 md5 截图证零视觉回归 + 生产 playwright 实证 change 页 label 已 text-primary 渲染。codex 2 并行 session 评审：后台块五项全 LGTM，公开向导块 a11y/i18n/校验 LGTM、3 处对齐遗漏修为末 commit 175dc13。**上一同步 90c7a55**，已发版 **v2.43.0** + 已部署生产。该轮一个功能：**admin 测试输出可复制脱敏 curl**——admin 后台跑连通性探测时返回「本次实际请求」对应的 curl 命令（`internal/probe/curl.go` 新增 `buildCurlCommand`），测试失败可复制给通道方复现。密钥脱敏：`secretVariants(apiKey)` 生成 {raw,QueryEscape,PathEscape} 三形态匹配（防 URL path/query 内嵌 key 被百分号编码漏网），命中处换成 `"$RP_API_KEY"` shell 变量——真实 key 只作 `strings.Split` 分隔符、绝不写进输出；错误文案经 `redactSecrets` 脱敏（`*url.Error` 带完整 URL 会泄 key）。作用域闸：四个 inline 入口全走 `ProbeConfig`，仅两个 admin handler（`admin_handler.go` submission、`monitor_handler.go` monitor）传 `WithCurlCapture()` functional option 并在响应加 `curl` 字段；公开 onboarding 路径不传→无 curl，调度器走 `monitor.Prober.Probe` 另一内核、热路径零影响；curl 不写日志不入库。前端 `CurlCommandBlock.tsx` 默认复制脱敏版、「复制(含密钥)」仅前端持有明文 key 时出现且只在点击那刻拼 `export RP_API_KEY=...`。已知边界（非回归）：仅脱敏 cfg.APIKey，`response_snippet` 仍原样回显上游响应体（将来可对 snippet 也跑 redactSecrets）。**本次部署同时把上一批（已发版未部署的 v2.42.2 deps + v2.42.3 alpine 3.19→3.23 + CI 改动）一并上线**（prod 此前停在更早的 b56ab0a；回滚锚点：`rollback-20260611`=本次新镜像、`rollback-20260611-pre`=部署前 b56ab0a 镜像）。〔上一轮（已随本次上线）两类基础设施改动〕**(A) dependabot 19→5 清理**——关 4 个 stale（actions/notifier 镜像代码已超越）+ 9 个 patch/minor 稳妥批本地合并升级（主仓 Go×5 gzip/pgx/klauspost-compress/x-time/sqlite + notifier Go×2 sqlite/playwright-go + 前端×2 country-flag-icons/vitest，commit b0a950b+22d4f10）聚合成单个 v2.42.2；6 个大版本只做必要的：**#109 alpine 3.19→3.23**（3.19 于 2025-11-01 EOL、runtime 基底带未修 OS CVE，commit 5877a3e，待发版 v2.42.3，本地 smoke apk 全过）已升，**#144 ubuntu 24→26 已关**（24.04 LTS 支持到 2029，26.04 刚出风险高收益零）；剩 4 个纯构建期 currency 大版本（vite7→8 / node20→26 / @types/node / @eslint/js，均不进 ship 出的 Go 二进制镜像、非必要）暂留。`.github/dependabot.yml` 同步加 `groups:`（每 ecosystem 把 minor/patch 合并、major 单独）防再堆到 19。**(B) 两个 CI 改动**——① `paths-ignore: ['**.md','docs/**']`（commit 630ff98）：纯文档 push 跳过整条 CI+docker+release（md 不进 //go:embed 的 frontend/dist，docs/test/ci/style 本就 release:false），混合改动（含任一非文档文件）照常跑；② setup-go `check-latest: true`（commit 2b415e1）：**修正下方上一同步对 GO-2026-5037/5039 的乐观判断**——那两个 stdlib CVE（crypto/x509 + net/textproto，fixed in go1.25.11）对**生产二进制**确已含补丁（Dockerfile `golang:1.25-alpine` 浮动 tag，下次部署重建即吃 1.25.11），但 **CI runner 经 setup-go 缓存滞后在 1.25.10**，本轮 govulncheck 硬闸两次判红、阻断合法发版——是 toolchain-lag race（非代码回归），旧配置「go-version:'1.25'」不保证取到含补丁的补丁号，check-latest 强制从 go.dev manifest 取最新匹配补丁根治。上一同步 b56ab0a，已发版 v2.42.1 + 部署生产，两处修正：① 调度器 3xx 由判绿改判红——HTTP client 默认自动跟随合规重定向，漏到 `determineStatus` 的裸 3xx 必是畸形重定向，对 LLM API 非可用响应，归 `client_error` 桶，与 inline `redirect_blocked` 口径统一（生产 30+ 天 0 条 3xx，零数据影响）② 变更流程 `useChangeRequest` 提交前 proof 过期预检（gate 在 requiresTest+testProof）+ base_url/新 key 改动清测试状态 + `changeRequest.test.proofExpired`×4 locale。上一同步 4a1bab4，收录/变更 remediation 第二批已发版 v2.42.0 + 部署生产：① `/api/change/test` 内联探测端点——抽 Handler helper `runInlineTestProbe`/`inlineTestProbeResponse` 共享探测编排，`change.Service.IssueProofWithExpiry` 解耦 onboarding 服务依赖，前端 `useChangeRequest` 改调新端点，旧 `/api/onboarding/test` 保留；变更流程未启用 onboarding 时也能测通 ② react-router-dom 6.30.2→6.30.4 修开放重定向高危 ③ Go 版本字符串统一 1.25 线（notifier go.mod/Dockerfile + 文档）——**实证推翻原计划「Go 工具链漏洞」误判**：GO-2026-5037/5039 均 fixed in go1.25.11，生产二进制已含补丁 ④ CI 加 `pull_request` 触发 + setup-go `go-version:'1.25'` + govulncheck 漏洞扫描硬闸 + Makefile test/lint/build/ci 聚合命令。首次 push 即 CI 4 job 全绿、govulncheck 闸过。上一同步 847fe37，收录/变更请求/admin 后台 UX polish：① 变更请求详情字段中文化——`admin.changes.fields` i18n 映射（4 locale）+ `fieldLabel` helper 回退原 key，可编辑网格从 3 列改 4 列加「字段·当前·改为」列头 + ArrowRight 方向箭头 ② onboarding 连通性测试探测状态 emoji🟢🟡🔴→Lucide CheckCircle2/AlertTriangle/XCircle ③ onboarding 步骤指示器 div→ol/li 加文字标签 `onboarding.steps.*` + ✓→Lucide Check + aria-current ④ 测试已出结果后运行按钮文案切 `rerunTest`「重新测试」⑤ SubmissionDetail 详情 header flex-wrap + 标题 whitespace-nowrap 修 375px 断词 ⑥ 修 `onboardingDisplay.test.tsx` 渲染 ConfirmStep 缺 3 个上一轮提升为必传的 prop（`checkedClauses`/`onToggleClause`/`testPassedAt`）——**test 文件不在 `tsc -b` build scope，仅 vitest 暴露**。已部署生产。上一同步 0d8384e 自助收录第二/三批改进：类型↔来源自洽 `channelTypeAllowedCategories` + 协议 5 条逐条勾选落库审计 + step2 渲染 `response_snippet` + step3 标签 testType→testVariant 修正）
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

- **后端**: Go 1.25+ (Gin, fsnotify, SQLite/PostgreSQL, slog)
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
├── automove/              → 自动移板（7 天可用率驱动，配置板位=锚点上限，不向上越板）
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
11. **自动移板**: `automove.Service` 基于 7 天可用率移板，配置板位（board）是"锚点/天花板"——只在配置板位及以下浮动、绝不向上越板（board=secondary 不会被自动升 hot；board=hot 可降 secondary 再恢复）。cold 为 sticky，需 `auto_cold_exempt` 手动解除
12. **探测链路统一**: 三处 inline 测试端点（用户自助 `/api/onboarding/test`、管理员审核 `/api/admin/submissions/:id/test`、监测项管理 `/api/admin/monitors/:key/probe`）都走 `onboarding.BuildServiceConfigFromSubmission`（或 runtime resolved root） + `config.ResolveSingleMonitor`（模板填充 + Duration 派生） + `probe.InlineProber.ProbeConfig`，确保与 `scheduler` 调用的 `monitor.Prober` **字段级一致**（headers/body/method/success_contains/timeout/retry 全覆盖）。模板覆盖编辑不允许在 inline 测试时即时生效（返回 422 `TEMPLATE_CHANGE_REQUIRES_SAVE`），需先保存。每次 inline 探测打 `probe_id` 结构化日志便于跨端追踪。**管理员通道管理探测（v2.48.0+）扩展**：① 可逐个探测子通道——`AdminGetMonitor` 附带 `probe_targets`（runtime resolved 的父+子，`model` 为选择器，PSCM 唯一），探测请求带 `target_model` 即按 `(provider,service,channel,model)` 命中 runtime 已解析子通道直接探测、不套草稿覆盖（未生效则报错，不做 raw 半解析）；② **配了代理就自动走代理**（无开关，`AdminProbeMonitor` 显式传 `probe.WithProxy(cfg.Proxy)`，复用 `monitor.NewExplicitProxyTransport` 的 http/socks5 语义，结果带 `via_proxy`）——这是显式钉在调用方的 SSRF 硬边界：**只有** admin 通道管理探测传 `WithProxy`，公开 `onboarding`/`submission` 自测**永不传**、绝不走代理（即使其 cfg 将来出现 proxy 字段）。注意 inline 走代理后上游 IP 的 SSRF 校验天然失效（由代理解析连接），与 scheduler 一致、不额外加严。读响应体失败按真实原因分流（超大→`response_too_large`、读超时→`response_timeout`、其余→`network_error`，v2.48.1）。

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

React SPA，采用嵌套路由布局（`LanguageLayout` + `Outlet`），45 组件/模块、15 hooks、12 utils：

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
│   ├── HeaderInfoPopover          → 表头 ⓘ 解释浮层（portal 到 body+fixed，脱离 overflow 滚动容器；质量/价格列表头复用）
│   ├── HoverTooltip               → 表格单元格悬浮提示单一真相源（portal 到 body+定位+hover 桥+滚动跟随；通道列/质量列共用，替代原生 title）
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
│   ├── admin/                     → 管理后台（11 文件）
│   │   ├── AdminAuth.tsx          → 管理员认证
│   │   ├── SubmissionList/Detail  → 收录申请管理
│   │   ├── ChangeRequestList.tsx  → 变更请求管理
│   │   ├── MonitorList/Detail     → monitors.d/ 通道管理
│   │   ├── MonitorForm.tsx        → 通道创建/编辑表单
│   │   ├── MonitorLogsTab.tsx     → 探测历史日志页
│   │   ├── CurlCommandBlock.tsx   → 可复制脱敏 curl 展示
│   │   ├── FormControls.tsx       → 表单控件（引 fieldStyles）
│   │   └── fieldStyles.ts         → 后台密集字段设计语言单一源（fieldInputClass/fieldSelectClass/fieldShapeClass，dense 形参）
│   └── onboarding/                → 自助收录（4 文件）
│       ├── ProviderInfoStep.tsx   → 服务商信息
│       ├── ConnectionTestStep.tsx → 连接测试
│       ├── ConfirmStep.tsx        → 确认提交
│       └── controls.ts            → 公开提交向导共享样式单一源（input/select/label/hint/主次按钮 className 常量，申请+变更共用）
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
├── 3xx → 🔴 不可用 (client_error)  ← client 默认已自动跟随合规重定向，漏到这里的裸 3xx 是畸形重定向，非可用响应
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
- 各历史表按 `(provider, service, channel, model)` 区分序列：`probe_history`/`status_events` 的真实 PK 是 `id`，但业务键是该四元组（覆盖索引 `idx_probe_history_pscm_ts_cover`）；`service_states`/`monitor_overrides` 的 **PK 直接含 model**；`channel_states` PK 不含 model。**（⚠️2026-06-29 Plan D-1 起：`probe_history` 增稳定 `model_id` 内部键，`/api/status`/`/api/status/query` 等展示读已切按 `model_id` 查 → 改 `model` 展示名不再断 probe_history 历史/时间线；但 `service_states`/`monitor_overrides` 仍 PK 含 model = Plan D-2 后置，admin logs 仍按 PSCM 查孤儿。故下文「改 model 名断历史」对 probe_history 展示读已不成立，对这两张派生表与「回溯历史版本」仍成立。）**
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
| POST | `/api/onboarding/test` | 收录内联探测测试（IP 限流） |
| POST | `/api/change/auth` | 变更：API Key 认证（返回通道列表） |
| POST | `/api/change/test` | 变更：内联探测测试（IP 限流，与 onboarding 解耦，签发同源 proof） |
| POST | `/api/change/submit` | 变更：提交变更请求（含测试证明） |
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
- **channel_type ↔ channel_source 须自洽**：`channelTypeAllowedCategories`（service.go 另一单一真相源，同样经 `/api/onboarding/meta` 下发）规定 O→{subscription,official,cloud}、R→{reverse}、M→{mixed}；`validateChannelTypeSource` 在 Submit 与 AdminUpdate 四元组重派生前校验所选来源的 Category 落在该类型允许集合内，否则拒绝（官方通道不可选 kiro 等逆向来源）。前端来源下拉据此 map 同步过滤；
- **channel_group** 为 1-8 位小写字母/数字（中转商自定义分组），留空默认 `main`。

PSC 各段仍只允许小写字母、数字、短横线（`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`）。`AdminUpdate` 仅当 service/type/source/group 四元组真正变化时才重派生 channel_code（保护 legacy 两段记录），并对 channel_type(O/R/M)、service_type(cc/cx/gm) 做枚举校验。管理员可在发布前通过 `target_channel` 覆盖派生值（**故意保留的逃生口，不受三段约束**，用于 legacy 与特殊命名）。前端 `ChannelTypeIcon` 通过首字母（大小写不敏感）识别通道类型图标（o→官方、r→逆向、m→混合）。

**入驻须知逐条确认**：`SubmitRequest.AgreementAccepted` 必须为 true（前端 `ConfirmStep` 据《入驻须知与确认》拆 5 条独立勾选，全勾才放行），否则 Submit 在前置环节即拒。落库时后端盖戳 `agreement_accepted/agreement_accepted_at/agreement_version`（`const AgreementVersion`，不信客户端），store 三列沿用 `channel_group` 幂等迁移模式（sqlite PRAGMA 预检 / pgx `ADD COLUMN IF NOT EXISTS`）。

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
