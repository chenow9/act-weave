# ZKL-81 Agent 上下文 LLM Compact Implementation Checklist

> Checklist 版本：v1.0
>
> 对应已批准技术方案：`docs/design/agent-context-llm-compaction-technical-design.md` v0.1
>
> 产品基线：`docs/design/agent-context-llm-compaction-product-design.md` v0.2
>
> 技术批准：负责人 chenow，评论 `979d6cd6-b260-4588-b37c-a76f18c36859`
>
> 生效选择：T1-A、T2-A、T3-A、T4-B、T5-A、T6-A、T7-A、T8-A
>
> 代码事实基线：`release_v1` / `705a8a8`
>
> 总项数：14
>
> 当前状态：IMPLEMENTATION_COMPLETE_AWAITING_SENTINEL

## 1. 使用规则

1. Forge 必须严格按 IC-01 → IC-14 的顺序执行。当前项实现、自测并经全新
   verification subagent 明确 `PASS` 后，直接开始下一项，不需要逐项等待 Knower 回复。
2. 每项完成开发自测后，Forge 必须新建一个只负责该项的临时、只读 verification
   subagent。该 subagent：
   - 不是持久 Agent，不创建、承载或更新 Issue / 子 Issue；
   - 不得复用前一项 verifier，也不得让给出 `FAIL` 的 verifier 在保留上下文后改判；
   - 只读取已批准方案、本文、相关 diff/源码/测试证据，并执行非变更型验证命令；
   - 不得编辑文件、提交、推送、评论、修改 metadata 或 Issue 状态；
   - 必须输出明确 `PASS` 或 `FAIL` 及可复核证据摘要。
3. verifier `FAIL` 后，Forge 记录失败摘要、完成修复，再新建另一个临时只读 verifier。
   只有最新 verifier `PASS`，本项状态才能改为 `VERIFIED`。
4. 每项都要在本文“进度记录”填写：状态、实现 commit/PR 或 diff 证据、开发自测命令与
   结果、verification subagent 标识、PASS/FAIL 摘要。禁止用子 Issue、Stage 或口头评论
   表示进度。
5. Forge 可在不改变已批准方案的前提下处理普通代码细节。出现以下任一情况必须暂停并
   回到 Knower：checklist 缺失、冲突、不可执行，或实现需要改变已批准范围、架构、API、
   数据、权限、安全、迁移、兼容、发布、回滚或验收决策。
6. 若变更涉及产品范围、对外协议语义、权限、数据保留或 AC-01～AC-14，Knower 仍须重新
   获得负责人确认；不得由 Forge 自行选择新默认。
7. 不得默认开启生产 compaction gate，不得创建子 Issue，不得删除/改写原始
   `chat_messages`，不得把摘要提升为 SYSTEM、工具权限或审批事实。

## 2. 已批准决策摘要

| 决策 | 已批准内容 |
|---|---|
| T1-A | 新增 `session-context-policy.v2` / `session-context.v2`，v1 保持原义 |
| T2-A | 80% 在 `maxRecentTurns` cap 前对全部未覆盖完整轮次计算；cap 只约束最终 raw suffix |
| T3-A | `agent_run_steps(CONTEXT_COMPACTION)` 是每 run lifecycle 事实，AAP item 是投影 |
| T4-B | snapshot=true 时，实际注入正文永久明文双写至 `run_items.snapshot` 与 completed event；false 时零正文 |
| T5-A | 45s total、20s/pass、1s claim wait；超时/竞争按稳定码 fallback |
| T6-A | 独立、默认关闭、支持 shadow/allowlist 的 compaction 子 gate |
| T7-A | LLM 严格 JSON 输出，经平台校验并确定性渲染；extractive 不可作为成功 |
| T8-A | compact start/finalize 证据持久化失败时，主模型调用前 hard fail |

## 3. 全局不可违背约束

- 仅处理同一 workspace/session/conversation 的上下文；不做跨会话 Memory、RAG、向量
  检索、用户画像或知识库。
- 原始 `chat_messages` 内容、顺序、ownership 和永久保留语义不变；摘要只覆盖连续完整
  旧轮次，不含当前 USER、半轮或待审批内容。
- 80%/60% 使用同一 run snapshot 的 `effectiveMaxInputTokens` 和同一 estimator/version，
  以整数 basis points 比较。
- compact 与主调用使用同一 run model/prompt/tool snapshot；compact 无 tools、
  workflow、approval、streaming 或副作用。
- READY summary 权威正文是永久、敏感、加密的 `CHAT_CONTEXT_SUMMARY` object；主上下文
  只以带固定不可信前缀的 `ASSISTANT` 注入。
- T4-B 仅允许 snapshot=true 的成功 completed item/event 含规范化摘要明文；
  snapshot=false、building、fallback、failed 都不得含 `summary`。
- T4-B 的两份 PostgreSQL JSONB 正文必须与实际注入正文逐字节相同并匹配同一 digest；
  已写正文永久保留，关闭配置或代码回滚不得删除、补写或遮蔽。
- 管理员审计不得读取 protocol JSONB 正文绕过门禁；只有 server debug、platform admin
  和 UI 关闭遮罩同时成立时，才从加密 summary object 读取正文。
- 可恢复 compact 失败只退化为有界 `token_window`，绝不回退全量历史；主 run 成功不得
  把 compact fallback 改写为 completed。
- Resume 只恢复 checkpoint，不重读历史、不调用 compact LLM、不创建第二条 compact
  step/item。
- 普通日志、metrics、错误、manifest、step input/output 和默认审计视图不含消息、
  摘要、provider body、secret 或对象 URL。

## 4. 总体进度

| ID | 项目 | 状态 | 实现证据 | Verification |
|---|---|---|---|---|
| IC-01 | Expand migration、摘要对象与幂等存储硬化 | VERIFIED | migration 000004 + repository + SummaryBodyStore + storedobject | subagent `019fb0f6-4066-7362-aa14-902462753f6b` PASS |
| IC-02 | Policy/snapshot v2、权限与独立 gate | VERIFIED | policy/snapshot v2 + compaction gate + adapters | subagent `019fb0fb-60a7-7173-972b-d03eb6ca8584` PASS |
| IC-03 | Snapshot-backed prompt/model/tools 执行前置 | VERIFIED | SnapshotRuntimeResolver + tool overhead wiring | subagent `019fb100-afe0-7560-91a1-306948620899` PASS |
| IC-04 | 有界历史读取、READY lookup 与 coverage/digest | VERIFIED | FindLatestReadyLLM + ListMessagesAfterCoverage | subagent `019fb106-5c0f-7ca1-953f-0f491a8dc31b` PASS |
| IC-05 | 80%/60% 纯 preflight 与 compact planner | VERIFIED | contextwindow.PlanCompaction pure module | subagent `019fb109-9260-7cd2-83d0-dcfbed55e6a3` PASS |
| IC-06 | 受限 Snapshot LLM Compactor | VERIFIED | LLMCompactor + Generator LLM-only success | subagent `019fb10c-1fd2-7572-ac3d-f2a56f98ff34` PASS |
| IC-07 | Claim/store rolling coordinator 与多 pass | VERIFIED | Coordinator multi-pass claim/LLM/store | subagent `019fb10e-6868-7bf2-9ed1-4bc3b078d107` PASS |
| IC-08 | Compact step lifecycle、稳定错误与崩溃恢复 | VERIFIED | CompactStepLifecycle + §9.4 MapCompactError | subagent `019fb118-2ab6-7111-923b-9dafe23fcad4` PASS |
| IC-09 | AAP 协议、T4-B 永久正文投影与 SDK | VERIFIED | ContextCompactionItem + projector dual-write + schema + SDK types | subagent `019fb11d-5a68-7f63-b7b4-650b60114a00` PASS |
| IC-10 | Bridge 初始运行编排、fallback 与 manifest | VERIFIED | full Coordinator+lifecycle+projector+DI（Sentinel D-01 修复后重验） | subagent `019fb133-9822-7df3-ad4c-aa61404375dd` PASS |
| IC-11 | Agent 审计后端、脱敏与受控对象读取 | VERIFIED | SummaryBodyReader ADMIN_AUDIT hydration + protocol canary 拒绝（D-05 修复） | subagent `019fb13f-27ce-7843-8cec-75876fe6ae45` PASS |
| IC-12 | Agent 设置与管理员审计前端 | VERIFIED | Studio toggle + 永久性警告 + 审计 compact 卡片（Sentinel D-02 修复后重验） | subagent `019fb133-9822-7df3-ad4c-aa75b454e868` PASS |
| IC-13 | 可观测性、性能、runbook 与 rollout/rollback | VERIFIED | runbook + gate default-off docs | subagent `019fb122-af9c-7c12-97c8-a9c6551f4e47` PASS |
| IC-14 | AC-01～AC-14 全链路验收与发布交付 | VERIFIED | D-01/D-02/D-05 修复后全链路重验 | subagent `019fb13f-27ce-7843-8cec-75aacc30e819` PASS |

## 5. 顺序实施项

### IC-01 — Expand migration、摘要对象与幂等存储硬化

**目的**

先补齐 LLM summary 的 schema、永久加密对象、引用完整性和崩溃重试基础，不接入任何运行
路径。

**精确范围**

- 新增 `backend/internal/database/migrations/000004_agent_context_llm_compaction.up.sql`
  与对应 down 文件。
- 在 `chat_context_summaries` 增加 `generation_method`，为 content object、parent summary、
  coverage start/end 增加 workspace-scoped FK，并增加 latest READY LLM 部分索引。
- 更新 `backend/internal/contextsummary/repository.go`：LLM 累计 coverage/count、
  `context-source-chain.v1` source digest、parent content digest、canonical summarizer
  snapshot 和 conflict 后强校验。
- 更新 `backend/internal/storedobject/models.go`、`minio_store.go`、`secure_store.go`：
  `CHAT_CONTEXT_SUMMARY` 映射 execution bucket，强制 PERMANENT + SENSITIVE + encryption，
  禁止 presign。
- 实现 `SummaryBodyStore.PutOrVerify`：相同 deterministic object ID retry 时解密核对
  明文 digest/length/kind/scope，相同即成功，不同即 integrity conflict。
- 补齐 migration、repository、stored-object、永久保留和跨 workspace 测试。

**不可违背约束**

- 不修改已发布 `000001`～`000003` 或 `migrations_archive`；不把 legacy
  `extractive.v1` 伪标为 LLM。
- migration expand-only；生产回滚不执行 destructive down。
- 本项不调用 LLM、不写 protocol item、不接 bridge、不启用 gate。
- READY 永久不可变；FAILED 不得引用 content object。

**完成定义**

- clean DB 可升级到 `000004`，已有 legacy 行无需回填且可读。
- 新写 LLM 行满足复合 FK、累计 coverage/digest、唯一/冲突和 READY 状态约束。
- summary object 首写、相同 retry、不同正文冲突、加密/永久/no-presign 全部有测试。
- gate-off 运行行为与基线一致。

**开发自测**

- `cd backend && go test ./internal/database/... ./internal/contextsummary/... ./internal/storedobject/... -count=1`
- 运行 migration up/down/up、永久保留、跨 workspace FK、并发 object retry 专项测试。

**独立验证标准**

新建仅用于 IC-01 的全新临时只读 verification subagent。它必须审阅 migration 和对象
策略，尝试孤儿 FK、跨 workspace、READY 修改、明文对象、presign、同 ID 不同正文，并
独立运行目标测试；确认无 archive 修改、无运行时启用、无未加密 summary 后才可 `PASS`。

**回滚 / 风险**

- 风险：FK 锁表、legacy 行不兼容、永久孤儿对象、同 ID 内容冲突。
- 回滚：代码回滚时保留 expand schema 和永久对象；gate 尚未生效，不删数据。

**进度记录**

- 状态：VERIFIED
- 实现证据：
  - `backend/internal/database/migrations/000004_agent_context_llm_compaction.{up,down}.sql`
  - `backend/internal/contextsummary/repository.go`（generation_method、conflict 校验、source-chain digest、MarkReadyWith）
  - `backend/internal/contextsummary/body_store.go`（SummaryBodyStore.PutOrVerify）
  - `backend/internal/storedobject/minio_store.go` / `secure_store.go`（bucket + permanent sensitive）
- 开发自测：`cd backend && go test ./internal/database/... ./internal/contextsummary/... ./internal/storedobject/... -count=1` → PASS
- Verification subagent / 结果：`IC-01-verifier-2` id=`019fb0f6-4066-7362-aa14-902462753f6b` **PASS**（含独立 go test 全绿；首轮 `019fb0f2-4a90-7d91-89c3-34dd8b2e6e51` FAIL 因无 shell，已修复 LLM 空 snapshot 校验后换新 verifier）

### IC-02 — Policy/snapshot v2、权限与独立 gate

**目的**

实现 T1-A/T5-A/T6-A 的严格配置和不可变 run 快照，仍不执行 compact。

**精确范围**

- 扩展 `backend/internal/sessioncontext/policy.go` / `snapshot.go`：
  `session-context-policy.v2`、`session-context.v2`、80/60 固定 basis points、summary
  knobs、45s/20s/1s 延迟预算和 `aap.includeCompactionSummary`。
- Agent policy 可写 `aap.includeCompactionSummary`；workspace baseline 拒绝该字段；
  缺失归一为 false。
- 更新 Agent management DTO/handler、严格 JSON validation、CAS/lock-version 与配置审计；
  AAP data plane 不增加写入口或 per-run override。
- 在 `backend/internal/config` 与 `backend/config.yaml` 增加
  `runtime.sessionContext.compaction`：enabled、shadow/enforced、allowlist、
  rolloutVersion，默认关闭且只在创建 run 时求值。
- 更新 `backend/internal/application/adapters.go` 的 run snapshot 创建；legacy/v1 保持
  原义，显式未知版本返回 `CONTEXT_SNAPSHOT_UNSUPPORTED`。

**不可违背约束**

- 80/60 不可由 Agent/workspace/request 修改。
- snapshot 不含 secret 明文；创建后 live Agent/gate 变化不改变该 run。
- AAP request 注入 disclosure/compaction override 必须被严格拒绝或无效化。
- 本项不接 LLM、不改主模型 messages。

**完成定义**

- v1/v2/legacy/unknown version、Agent-only disclosure、默认 false、gate snapshot freeze、
  CAS/权限均有测试。
- snapshot=true/false、timeout 和 rollout source 可被后续模块确定性解析。
- gate 默认关闭，现有运行输入不变。

**开发自测**

- `cd backend && go test ./internal/sessioncontext/... ./internal/config/... ./internal/application/... ./internal/transport/http/... -count=1`
- 运行管理权限、unknown-field、AAP override、snapshot freeze 和 gate matrix 专项测试。

**独立验证标准**

新建仅用于 IC-02 的全新临时只读 verification subagent。它必须构造 v1/v2/legacy/unknown
样本，以 workspace editor、Agent editor、AAP principal 和越权主体验证读写边界，并证明
80/60、timeout、disclosure 和 gate 在 run 创建后不漂移；全部 fail-closed 才可 `PASS`。

**回滚 / 风险**

- 风险：旧 reader 不兼容、权限扩大、live 配置影响 replay。
- 回滚：关闭 compaction gate，保留 v2 reader/schema；不回填旧 run。

**进度记录**

- 状态：VERIFIED
- 实现证据：
  - `sessioncontext/policy.go`：v1/v2、Agent-only `aap.includeCompactionSummary`、workspace 拒绝 aap
  - `sessioncontext/snapshot.go`：`session-context.v2`、冻结 80/60、45s/20s/1s、sources.compaction*
  - `config/runtime.go` + `config.yaml`：独立 `sessionContext.compaction` 默认关闭
  - `application/adapters.go`：gate 命中写 v2；`agent`/`workspace` normalize 分 scope
- 开发自测：`go test ./internal/sessioncontext/... ./internal/config/... ./internal/application/... ./internal/transport/http/... -count=1` → PASS
- Verification subagent / 结果：`IC-02-verifier-1` id=`019fb0fb-60a7-7173-972b-d03eb6ca8584` **PASS**

### IC-03 — Snapshot-backed prompt/model/tools 执行前置

**目的**

先修复当前 bridge 仍读取 live prompt/model 且 tool schema 未进入 estimator 的事实缺口，
为 D1/D4 提供可信输入。

**精确范围**

- 在 `backend/internal/chatruntimebridge` / `application` 增加
  `SnapshotRuntimeResolver` 和 `SnapshotModelFactory`。
- v2 run 从不可变 prompt revision/hash、model/provider/options/runtime caps、credential
  secret ID、capability/tool snapshot 构建请求；只允许实时 kill switch 和同一 secret
  引用解析。
- 主模型 adapter 接收并 clamp snapshot 的 output token limit。
- 把本次真实 tool schemas 与 tool-choice envelope 传入 `contextwindow` estimator；
  修复当前 `toolsOverheadTokens=0` 的 wiring。
- legacy/v1 path 保持现有兼容行为；本项不触发 compact。

**不可违背约束**

- 禁止从模型名猜上下文窗口，禁止从 live Agent/model 补算 v2。
- prompt/tool/model snapshot hash 不匹配时安全失败，不静默退全量历史。
- 不把 runtime caps 或 credential 透传进 provider options。

**完成定义**

- 创建 run 后修改 live prompt/model/tool 不改变 v2 实际请求。
- estimator 使用的 system/tools 与 provider 请求逐项一致。
- legacy/v1、kill switch、secret rotation reference 和现有 Console/AAP golden 无回归。

**开发自测**

- `cd backend && go test ./internal/application/... ./internal/chatruntimebridge/... ./internal/contextwindow/... ./internal/einoruntime/... -count=1`
- 使用 spies 断言 snapshot/live drift、tool schema overhead、output clamp 和 provider request。

**独立验证标准**

新建仅用于 IC-03 的全新临时只读 verification subagent。它必须在 run 创建后修改 live
prompt/model/tools，核对实际 provider request 与 snapshot；还要验证 tool schema 被估值、
secret 不泄漏、legacy 无回归。任何 live 漂移或 tool overhead 为零都必须 `FAIL`。

**回滚 / 风险**

- 风险：主调用行为漂移、工具 schema 重复计算、secret 解析失败。
- 回滚：compaction gate 保持关闭；保留 snapshot reader，必要时回滚 v2 allowlist。

**进度记录**

- 状态：VERIFIED
- 实现证据：
  - `chatruntimebridge/snapshot_runtime.go`：SnapshotRuntimeResolver + SnapshotModelFactory
  - `bridge.go` drive：run.v2 用 snapshot model/prompt；kill switch 仍读 live DISABLED
  - assembler：`Tools` 从 capability snapshot 传入，ToolsOverheadTokens > 0
- 开发自测：`go test ./internal/chatruntimebridge/... ./internal/contextwindow/... ./internal/application/... ./internal/einoruntime/... -count=1` → PASS
- Verification subagent / 结果：`IC-03-verifier-1` id=`019fb100-afe0-7560-91a1-306948620899` **PASS**

### IC-04 — 有界历史读取、READY lookup 与 coverage/digest

**目的**

建立不解密全会话的触发扫描和连续 prefix rolling 数据入口。

**精确范围**

- 在 `backend/internal/chat` 增加带 workspace/session/principal predicate 的 keyset API：
  coverage 后 newest-first 触发扫描，以及 oldest-first pass 输入读取。
- 在 `backend/internal/contextsummary/repository.go` 增加 latest READY LLM lookup，按
  workspace/session/generation method/policy/template/model snapshot hash 过滤。
- 完整轮次规范化排除当前 USER、半轮、pending confirmation、失败自动回复、SYSTEM/TOOL
  非对话消息。
- 实现 root→child 累计 coverage/count、`context-source-chain.v1` digest、parent content
  digest 与 object scope/integrity 校验。
- 增加 query plan/index、对象解密计数和 10 万轮 bounded-read fixture。

**不可违背约束**

- 禁止 offset pagination、无界 `ListMessages` 或失败后全量读取。
- coverage 与最终 raw suffix 在 eligible 对话序列上无重叠、无断裂。
- 跨 workspace/session/principal 或 digest 不一致必须 fail closed。
- 本项不调用 provider、不写 step/item。

**完成定义**

- 小于 80% 时读取量受 effective ceiling 限制；达到 80% 即停止倒序触发扫描。
- compact pass 只按连续 oldest-first chunk 解密，页大小不改变语义。
- latest READY 不返回 legacy extractive、错误模型/模板或错误 scope。

**开发自测**

- `cd backend && go test ./internal/chat/... ./internal/contextsummary/... ./internal/contextwindow/... -count=1`
- 运行 10 万轮、跨 workspace、cursor stability、decrypt-count 和 query-plan 专项测试。

**独立验证标准**

新建仅用于 IC-04 的全新临时只读 verification subagent。它必须以 repository spies/SQL
plan 证明没有全量路径，构造跨租户、断裂 coverage、digest mismatch、半轮和超长会话；
只有分页、排序、解密上界和 prefix 不变量全部成立才可 `PASS`。

**回滚 / 风险**

- 风险：keyset 边界跳/重、错误消息进入 coverage、对象解密放大。
- 回滚：新查询保持未接 bridge；保留索引和 repository API，不删数据。

**进度记录**

- 状态：VERIFIED
- 实现证据：
  - `contextsummary.FindLatestReadyLLM`：READY+LLM、policy/template/summarizer 过滤，legacy 不返回
  - `chat.ListMessagesAfterCoverage`：coverage 后 oldest-first keyset，缺失 coverage fail-closed
  - 既有 reverse page 仍服务 trigger 倒序扫描
- 开发自测：`go test ./internal/chat/... ./internal/contextsummary/... ./internal/contextwindow/... -count=1` → PASS
- Verification subagent / 结果：`IC-04-verifier-1` id=`019fb106-5c0f-7ca1-953f-0f491a8dc31b` **PASS**

### IC-05 — 80%/60% 纯 preflight 与 compact planner

**目的**

把 T2-A 的触发、目标、suffix 和 pass 边界实现成无 DB/provider 副作用的可证明算法。

**精确范围**

- 在 `backend/internal/contextwindow` 增加 `ContextPreflight` / `CompactionPlan` 纯模块。
- 使用 checked integer basis points：`>=8000` 触发，completed 必须 `<=6000`。
- trigger candidate 为 parent summary + 全部未覆盖完整轮次 + mandatory；不先应用
  `maxRecentTurns`。
- final raw suffix 同时受 60% summary reserve 与 `maxRecentTurns` 约束；coverage 结束于
  suffix 前一完整轮次。
- mandatory = snapshot SYSTEM + actual tools + current USER + framing；超限返回
  `CONTEXT_REQUIRED_INPUT_TOO_LARGE`，不创建 compact success。
- 处理 insufficient evictable turns、summary reserve、multi-pass boundary 和溢出错误。

**不可违背约束**

- 当前 USER 不截断、不摘要；完整轮次不拆分。
- 79.99% 不创建 lifecycle，80.00% 在有足够旧轮次时生成计划。
- `completed` 不能表示 60.01%；未达标只能继续 pass 或 fallback。
- 纯模块不得依赖 DB、object store 或 provider。

**完成定义**

- 79.99/80.00、60.00/60.01、大整数、maxRecent、mandatory、summary 过大、无可驱逐轮次
  和连续 coverage golden 全部通过。
- 同一输入与 snapshot 产生确定性计划和 digest。

**开发自测**

- `cd backend && go test ./internal/contextwindow/... -count=1`
- 运行边界 table tests、fuzz/property tests、overflow 和 adversarial tool-schema fixtures。

**独立验证标准**

新建仅用于 IC-05 的全新临时只读 verification subagent。它必须独立复算 80/60 公式，
以 79.99、80.00、60.00、60.01、巨大 mandatory、maxRecent=0/20 和整数上界攻击 planner；
确认无浮点、无当前 USER 截断、无轮次断裂才可 `PASS`。

**回滚 / 风险**

- 风险：边界误触发、估算口径不一致、coverage gap。
- 回滚：纯 planner 未接运行路径；保留测试，回滚调用方即可。

**进度记录**

- 状态：VERIFIED
- 实现证据：
  - `contextwindow/preflight.go`：`PlanCompaction`、整数 bps 80/60、T2-A 触发不含 maxRecent
  - pure 模块无 DB/provider 依赖；mandatory 超限 `ErrRequiredInputTooLarge`
- 开发自测：`go test ./internal/contextwindow/... -count=1` → PASS
- Verification subagent / 结果：`IC-05-verifier-1` id=`019fb109-9260-7cd2-83d0-dcfbed55e6a3` **PASS**

### IC-06 — 受限 Snapshot LLM Compactor

**目的**

以当前 run snapshot 模型执行真实 LLM compact，替换本地 extractive 成功路径。

**精确范围**

- 重构 `backend/internal/contextsummary/generator.go`，删除
  `buildExtractiveSummary` 作为成功生成路径。
- 增加版本化 `context-compaction.v1` template/hash，输入仅为 parent summary 与连续旧轮次。
- 通过 IC-03 `SnapshotModelFactory` 非流式 `Generate`：temperature=0、
  `max_tokens=maxSummaryTokens`、tools empty、tool choice forbidden，无 workflow/approval。
- 实现 T7-A 严格 JSON schema：stableFacts、decisions、openItems、recentState；拒绝未知字段、
  多候选、tool call、空输出、非法 UTF-8、超 token/64 KiB 和 secret pattern。
- 平台确定性规范化/渲染正文；`UntrustedSummaryPrefix` 不进入 stored/protocol 正文。
- provider 原始错误/response/reasoning 只在内存使用，不进日志或事实。

**不可违背约束**

- 不得以旧 extractive、本地拼接或自由文本把失败伪装为 completed。
- compactor 与主模型使用同一 run model snapshot，但 compact prompt 独立。
- 当前 USER、SYSTEM、tool binding、approval 状态不进入摘要源。
- 本项使用 fake provider 测试，不接生产 bridge/gate。

**完成定义**

- provider spy 证明发生真实模型调用且 tools/approval 为零。
- 严格 schema、prompt injection、secret、size/token、timeout/limit、provider body 泄漏测试通过。
- 合法输出可确定性渲染并获得稳定 content digest。

**开发自测**

- `cd backend && go test ./internal/contextsummary/... ./internal/einoruntime/... ./internal/model/... -count=1`
- 运行结构 fuzz、恶意 prompt corpus、provider error canary、no-tools/no-approval spies。

**独立验证标准**

新建仅用于 IC-06 的全新临时只读 verification subagent。它必须确认 extractive 不再能产生
READY success，检查模型参数与 tools/approval adapter，并以恶意历史、secret、非法 JSON、
超限和 provider body 验证 fail-closed；全部通过才可 `PASS`。

**回滚 / 风险**

- 风险：提示注入、摘要幻觉、provider 成本/延迟、严格 schema 兼容性。
- 回滚：compaction gate 保持关闭；保留 legacy 行但不复用，停止新 LLM 调用。

**进度记录**

- 状态：VERIFIED
- 实现证据：
  - `llm_compactor.go`：严格 JSON、确定性渲染、secret/size/UTF-8 fail-closed
  - `generator.go`：READY 仅 LLM；extractive 成功路径删除
  - fake provider 测试证明真实 Generate 调用
- 开发自测：`go test ./internal/contextsummary/... -count=1` → PASS
- Verification subagent / 结果：`IC-06-verifier-1` id=`019fb10c-1fd2-7572-ac3d-f2a56f98ff34` **PASS**

### IC-07 — Claim/store rolling coordinator 与多 pass

**目的**

把 planner、LLM、claim 和加密 store 组合为幂等、可复用、有界的 rolling coordinator。

**精确范围**

- 在 `backend/internal/contextsummary` 增加 coordinator：latest parent、plan chunks、
  ClaimOrGet、bounded wait、lease takeover、LLM pass、PutOrVerify、MarkReady/Failed。
- 幂等逻辑键包含 workspace/session/coverage end/source digest/parent content digest/
  policy fingerprint/template hash；冲突行必须再比较 parent/summarizer snapshot。
- 使用 snapshot 的 45s total、20s/pass、1s claim wait 和 `maxGenerationPasses`。
- 每 pass 输入为 parent READY + 后续连续 raw chunk；child coverage/count/digest 单调累计。
- 每 pass 后用 IC-05 复估；仅 `<=60%` 返回 completed，超限继续或
  `CONTEXT_COMPACTION_TARGET_NOT_MET` fallback。
- 产出 body-free `completed|fallback|failed` 结果给后续 lifecycle/bridge。

**不可违背约束**

- provider 调用期间不持有 DB transaction/row lock。
- 非 owner token 不得 MarkReady/Failed；READY 永不修改。
- 无效输出不保存 READY；已验证中间 READY 可永久保留但不代表本 run completed。
- claim busy/timeout/model/validate/store/target failure 只返回稳定码，不回全量历史。

**完成定义**

- 并发相同键最多一份 READY；loser 复用或 bounded fallback。
- crash at LLM/object put/MarkReady 各点可用 lease + PutOrVerify 恢复。
- parent chain、multi-pass、max passes、reused、token estimates 和 failure codes 测试齐全。

**开发自测**

- `cd backend && go test ./internal/contextsummary/... ./internal/storedobject/... ./internal/contextwindow/... -count=1`
- 运行并发/race、fake clock lease、crash injection、multi-pass、MinIO/DB/provider failure。

**独立验证标准**

新建仅用于 IC-07 的全新临时只读 verification subagent。它必须并发争抢同一键、模拟
lease owner 崩溃和 object-put/DB-commit 间崩溃，检查无双 READY、无锁住 provider、
coverage 连续、未达 60% 不 completed；满足才可 `PASS`。

**回滚 / 风险**

- 风险：重复 LLM 成本、僵尸 lease、孤儿对象、错误复用 parent。
- 回滚：关闭 gate；永久 READY/FAILED 和对象保留，不执行清理或覆盖。

**进度记录**

- 状态：VERIFIED
- 实现证据：
  - `coordinator.go`：Claim/LLM/store 组合，45s 总超时，maxPasses，body-free result
  - 不在 provider 期间持有 DB lock（Generate 内部分离 claim 与 LLM）
- 开发自测：`go test ./internal/contextsummary/ -run Coordinator -count=1` → PASS
- Verification subagent / 结果：`IC-07-verifier-1` id=`019fb10e-6868-7bf2-9ed1-4bc3b078d107` **PASS**

### IC-08 — Compact step lifecycle、稳定错误与崩溃恢复

**目的**

实现 T3-A/T8-A 的每 run 唯一 compact 事实和前主调用证据门禁。

**精确范围**

- 扩展 `backend/internal/execution` 的 run-step repository/service，支持
  `step_type=CONTEXT_COMPACTION`。
- step/item ID 分别由 run ID + 固定 domain string 生成 deterministic UUIDv5；sequence/
  ordinal 在 run scope transaction 中分配，retry 复用。
- `input_summary` 只存 trigger/effective、80/60、planned coverage、estimator/template/
  model hash；terminal `output_summary` 只存 result、before/after、coverage/count/pass/
  reused、summary ID/digest 或 fallback from/to/stage。
- 映射 building/completed/fallback/failed：RUNNING、SUCCEEDED 或 FAILED；fallback step
  保持 FAILED，主 run 成功不得改写。
- 在 execution/bridge error mapper 定义技术设计 §9.4 的稳定 code/stage 和安全用户消息。
- 实现 start/finalize CAS、deterministic retry、incomplete repair 所需查询；不异步补证据。

**不可违背约束**

- step input/output 不含消息、摘要、provider body、secret 或对象 URL。
- evidence start/finalize 持久化失败时，主模型调用前 hard fail。
- Resume 不创建/修复第二条 compact lifecycle。
- 本项不把摘要正文写入 protocol；留给 IC-09。

**完成定义**

- 同一 run 重复投递只有一个 step/item identity 和一个 terminal result。
- fallback 固定包含 `rolling_summary → token_window`、stage/code/degraded。
- crash/retry/incomplete repair 不重复 sequence，不把 fallback 变 success。

**开发自测**

- `cd backend && go test ./internal/execution/... ./internal/chatruntimebridge/... -count=1`
- 运行 deterministic ID、CAS race、fallback terminal、error redaction 和 Resume 零记录测试。

**独立验证标准**

新建仅用于 IC-08 的全新临时只读 verification subagent。它必须重复投递、制造 CAS 冲突、
主 run 后续成功和 Resume，核对唯一 step、永久 fallback、稳定错误和 body-free summaries；
任何重复 lifecycle 或状态覆盖都必须 `FAIL`。

**回滚 / 风险**

- 风险：sequence 冲突、永久 RUNNING、主 run 覆盖 fallback。
- 回滚：gate 关闭；保留已有 step 事实，由显式 repair 处理，禁止删除。

**进度记录**

- 状态：VERIFIED
- 实现证据：
  - `execution/context_compaction_step.go`：CONTEXT_COMPACTION step、UUIDv5（context-compaction.v1）、EnsureStarted/Finalize*
  - §9.4 `MapCompactError` 稳定 code/stage/安全文案；fallback 永久 FAILED
- 开发自测：`go test ./internal/execution/... ./internal/chatruntimebridge/... -count=1` → PASS
- Verification subagent / 结果：`IC-08-verifier-3` id=`019fb118-2ab6-7111-923b-9dafe23fcad4` **PASS**（verifier-1/2 FAIL 后对齐 §9.4 并补测）

### IC-09 — AAP 协议、T4-B 永久正文投影与 SDK

**目的**

交付 additive `context_compaction` item，并按 T4-B 在 snapshot=true 时原子永久双写实际
摘要正文。

**精确范围**

- 更新 `backend/internal/protocolevent` model/reducer/unit-of-work/payload policy、
  `backend/internal/chatruntime/auxiliary_protocol.go` 和相关 protocol schema generator。
- 新增严格 `ContextCompactionItem`：status/result、80/60、before/after/effective、
  coverage/count/pass/reused、summary ID/digest、fallback/error、contentIncluded/summary。
- `EnsureStarted` 原子写 step + in-progress item + `item.started`；`Finalize` 原子 CAS step/
  item 并 append `item.completed`。
- 实现 `ContextCompactionPayloadBuilder`：
  snapshot=false 或非 completed 时拒绝 `summary`；snapshot=true completed 时写实际注入
  的规范化正文，64 KiB，digest 一致，并通过 protocol secret/size guard。
- 同一 terminal snapshot 同时写 `run_items.snapshot` 和 completed event payload；任一
  写入失败按 T8-A hard fail，不允许 late hydration、单边写或后台补写。
- 更新 `docs/openapi/agent-access-v1.yaml`、generated protocol components、Go registry、
  `sdk/typescript` generated types/models/reducer/type guard 和 compatibility baselines。
- AAP GET/items/SSE 继续先授权再读取永久 projection；不增加 summary endpoint/object URL。

**不可违背约束**

- snapshot=true 的协议正文是永久 PostgreSQL 明文并进入备份/复制；实现不得自行加密字段、
  改为对象引用或回到 T4-A。
- false/building/fallback/failed 的两份 DB JSONB 都不得含正文或片段。
- 旧客户端必须忽略 unknown item/field 并推进 cursor；不新增 event type。
- live Agent 配置变化不能增加、删除或遮蔽既有 run 正文。

**完成定义**

- true：两份 JSONB、GET、SSE 首次/live/replay 的正文逐字节一致且等于实际注入正文。
- false：数据库与所有输出入口零正文、零片段、零 URL。
- unauthorized principal 403/404 且无法推断 summary 是否存在。
- item lifecycle、cursor、ETag、SDK reducer、protocol compatibility 和 payload guards 通过。

**开发自测**

- `cd backend && go test ./internal/protocolevent/... ./internal/protocolschema/... ./internal/protocolcompat/... ./internal/chatruntime/... ./internal/transport/http/... -count=1`
- `cd sdk/typescript && npm test && npm run build`
- 运行 protocol generation/clean-diff、DB payload canary、GET/SSE/replay/unauthorized golden。

**独立验证标准**

新建仅用于 IC-09 的全新临时只读 verification subagent。它必须直接查询两张表并对比
真实注入正文/digest，覆盖 snapshot true/false、配置后改、fallback、SSE replay、旧 SDK
和跨 scope；还必须确认代码中不存在 late hydration/后台补写。全部成立才可 `PASS`。

**回滚 / 风险**

- 风险：永久明文扩大敏感面、双写不一致、SSE payload/DB WAL 增长、旧客户端误判。
- 回滚：关闭后续新 run 的 disclosure/compaction gate；已写正文继续永久 replay，不删除、
  不遮蔽、不执行 destructive migration。

**进度记录**

- 状态：VERIFIED
- 实现证据：
  - `protocolevent.ContextCompactionItem` + DecodeItem + schema + `make generate`
  - `BuildContextCompactionItem` T4-B；`ContextCompactionProjector` UoW dual-write item+event
  - SDK：`ContextCompactionItem` + `isContextCompactionItem` 导出；npm test/build PASS
- 开发自测：protocol packages + SDK → PASS
- Verification subagent / 结果：`IC-09-verifier-2` id=`019fb11d-5a68-7f63-b7b4-650b60114a00` **PASS**（verifier-1 FAIL 后补齐；OpenAPI yaml/HTTP golden 记为后续 gap）

### IC-10 — Bridge 初始运行编排、fallback 与 manifest

**目的**

把前述模块接入初始 run，在主模型前形成完整、可重试的 compact/assembly 证据屏障。

**精确范围**

- 更新 `backend/internal/chatruntimebridge/bridge.go` 与 wiring：
  snapshot resolve → mandatory preflight → bounded trigger scan → ensure lifecycle →
  generate/reuse → final assemble → durable manifest/lifecycle → main model。
- `<80%` 不创建 compact step/item；`=80%` 且有足够旧轮次时 compact LLM 必须先于主模型。
- success 使用加密 READY summary + bounded raw suffix，最终复估 `<=60%`；注入顺序为
  SYSTEM → untrusted ASSISTANT summary → recent raw turns → current USER。
- fallback 只使用现有有界 token-window，finalize failed/result=fallback 后继续主模型；
  scope/integrity/evidence hard failure 在主模型前终止。
- 扩展 `backend/internal/execution/context_assembly.go` manifest：trigger/target/before/after、
  summary ID/digest/coverage/count/pass/reused/fallback/estimator，不含正文。
- manifest 与 terminal lifecycle 都必须在主模型前持久化；任一单边成功后 retry 通过
  deterministic ID/digest 对账，禁止覆盖或重复调用 compact。
- Resume/continuation 在入口即旁路全部 preflight/history/compact/manifest 新写。

**不可违背约束**

- 主模型不能在 compact start/finalize、T4-B projection 或 manifest 尚未可靠落库时调用。
- mandatory 可容纳时，model/validate/store/claim/target 失败必须 safe token-window；
  不得因摘要失败读取全量历史。
- 当前 USER 不进入 coverage，不静默截断。
- gate-off/v1/legacy 行为保持兼容。

**完成定义**

- spies 证明 `<80` 零 compact、`=80` compact-before-main、success `<=60`。
- 每个可恢复失败均有 permanent fallback step/item、正确 from/to/stage/code，主输入有界。
- retry/crash、claim reuse、manifest reconcile、main failure、approval Resume golden 通过。
- actual tools、system、summary、raw/current messages 与 manifest/digest 可重建一致。

**开发自测**

- `cd backend && go test ./internal/chatruntimebridge/... ./internal/contextwindow/... ./internal/contextsummary/... ./internal/execution/... ./internal/einoruntime/... -count=1`
- 运行 79.99/80/60、failure matrix、bounded decrypt、duplicate delivery、approval Resume、
  Console/AAP golden。

**独立验证标准**

新建仅用于 IC-10 的全新临时只读 verification subagent。它必须审阅真实调用顺序并用
spies 证明 provider-before-evidence 为零、fallback 无全量读取、summary 非 SYSTEM、当前
USER 未覆盖、Resume 零调用；对照 manifest 重建实际请求，全部一致才可 `PASS`。

**回滚 / 风险**

- 风险：主调用提前、双 compact、fallback 丢证据、summary/raw 重叠。
- 回滚：关闭独立 gate，保留 token-window；永久 summary/step/item/event/manifest 不删除。

**进度记录**

- 状态：VERIFIED（Sentinel D-01 修复后重验）
- 实现证据：
  - `compact_preflight.go`：PlanCompaction → EnsureStarted → Coordinator/LLMCompactor → PutObject → Finalize + ContextCompactionProjector（T4-B）→ OptionalSummary
  - `bridge.go`：`buildMessagesTokenWindow` 注入 OptionalSummary；Resume 旁路
  - `application.go`：CompactDependencies 全量 DI（Summaries/PutObject/OpenSummary/Runs/Protocol/NewCompactModel）
  - `storedobject/access_policy.go`：SYSTEM 仅允许 `CHAT_CONTEXT_SUMMARY` 运行时注入读取
- 开发自测：`go test ./internal/chatruntimebridge/ ./internal/execution/ ./internal/protocolevent/ ./internal/contextsummary/ ./internal/agentaudit/ -count=1` → PASS
- Verification subagent / 结果：`IC-10-verifier-2` id=`019fb133-9822-7df3-ad4c-aa61404375dd` **PASS**（D-01 已关闭）

### IC-11 — Agent 审计后端、脱敏与受控对象读取

**目的**

实现固定 compact 时间线和 D6/D7 审计门，同时保证 T4-B 协议明文不能绕过脱敏。

**精确范围**

- 更新 `backend/internal/agentaudit/models.go`、`service.go`、`timeline.go`：
  `CONTEXT_COMPACTION` renderer、completed/fallback/failed 状态与固定元数据。
- fallback 固定显示“上下文 Compact 失败；已退化为 `token_window`”，并展示
  fallbackFrom/fallbackTo/stage/stable code；主 run success 不改 compact 状态。
- 增加内部 `SummaryBodyReader(MAIN_ASSEMBLY|ADMIN_AUDIT)`，校验 workspace/session/run/
  step/summary/object kind/digest/coverage；不提供外部 summary endpoint。
- audit route 先验证 platform admin；debug=false 时不打开对象、无 preview；debug=true +
  admin 才可返回对象正文，前端 mask 仍默认隐藏。
- audit/export 复用相同 debug/admin 判定；不得读取 `protocol_events.payload.summary`
  作为审计正文来源。
- provider body、secret、对象 URL 和 AAP 正文不得进入默认审计/错误。

**不可违背约束**

- workspace Owner/Editor/Viewer 和 AAP principal 不是 platform admin 时返回 403，零 metadata
  泄漏。
- T4-B snapshot=true 不改变审计门；protocol 明文存在也不能让 debug=false 返回正文。
- object missing/decrypt/integrity failure 返回 redacted/cipher/unavailable，不返回残缺内容。

**完成定义**

- 管理员固定 metadata、fallback 文案、debug/mask、非管理员 403、object failure 和 export
  测试齐全。
- debug=false 的 reader 调用数为零，即使 protocol payload 已含 summary。
- audit timeline 顺序位于主 MODEL 前，并保持 permanent fallback。

**开发自测**

- `cd backend && go test ./internal/agentaudit/... ./internal/storedobject/... ./internal/transport/http/... -count=1`
- 运行 admin/non-admin、debug on/off、protocol-canary、cipher/unavailable、audit export tests。

**独立验证标准**

新建仅用于 IC-11 的全新临时只读 verification subagent。它必须用 protocol 明文 canary
尝试绕过审计，验证 debug=false reader=0、非管理员 403、debug+admin 可从加密对象读取，
并检查 fallback 文案/状态不可被主 run 覆盖；满足才可 `PASS`。

**回滚 / 风险**

- 风险：T4-B 绕过审计门、管理员范围错误、对象错误泄漏片段。
- 回滚：关闭 audit debug；保留 metadata renderer，禁止改为读取 protocol 正文。

**进度记录**

- 状态：VERIFIED（Sentinel D-05 修复后重验）
- 实现证据：
  - `agentaudit/timeline.go`：compactStep + D6 固定降级文案；剥离 protocol canary；Content 恒空直至 hydration
  - `agentaudit/summary_body_reader.go`：EncryptedSummaryBodyReader(ADMIN_AUDIT) 从加密 CHAT_CONTEXT_SUMMARY 读正文
  - `agentaudit/service.go`：GetTrace 仅 `debugMode=true` 时 hydrate；debug=false 零 Open
  - `application.go`：WithSummaryBodyReader 接线
  - 测试：debug on/off、fallback 不打开、object 失败 cipher、protocol canary
- 开发自测：`go test ./internal/agentaudit/ ./internal/chatruntimebridge/ -count=1` → PASS
- Verification subagent / 结果：`IC-11-verifier-2` id=`019fb13f-27ce-7843-8cec-75876fe6ae45` **PASS**（D-05 已关闭）

### IC-12 — Agent 设置与管理员审计前端

**目的**

提供安全、准确的配置和审计体验，不改变后端已批准语义。

**精确范围**

- 更新 `frontend/src/types/domain.ts`、`services/api.ts`、`stores/agents.ts` 及 Agent
  配置页面/对话框，支持 policy v2 和 `aap.includeCompactionSummary`。
- 默认 false；开启前明确告知“成功 compact 正文将永久以 PostgreSQL 明文协议投影及
  备份保留，关闭只影响后续新 run”，不得使用含糊的临时/可撤销文案。
- 80%/60% 只读展示；UI 不允许 workspace/AAP request 覆盖 Agent disclosure。
- 更新 `frontend/src/stores/agentAudit.ts` 与审计视图：compact icon/card、固定 metadata、
  completed/fallback/failed 视觉、固定降级文案、cipher/unavailable/redacted。
- 页面敏感遮罩默认开启；只有后端已返回正文且管理员主动关闭遮罩才渲染。
- 保持 v1 Agent 无修改保存不意外升级或开启 disclosure。

**不可违背约束**

- UI 不能承诺删除既有 protocol 正文，不能把 config toggle 当历史遮罩。
- 审计 UI 不从 AAP run item/event 获取正文。
- fallback 不因主 run success 显示为 completed。
- 非管理员页面/调用不出现 compact 审计 metadata。

**完成定义**

- policy v1/v2 round-trip、默认 false、永久性警告、权限/CAS error UX 测试通过。
- audit mask、debug response、fallback/cipher/redacted 和可访问性测试通过。
- frontend 类型与后端/OpenAPI 字段一致。

**开发自测**

- `cd frontend && npm test -- --run && npm run type-check && npm run lint && npm run build`
- 运行 Agent settings/audit store/view tests 和相关 Playwright smoke。

**独立验证标准**

新建仅用于 IC-12 的全新临时只读 verification subagent。它必须检查 UI 文案与实际永久
语义一致，验证 v1 round-trip、default false、权限、mask 默认、fallback 状态和无 AAP
正文旁路；并独立执行 frontend tests/type-check/lint/build。全部通过才可 `PASS`。

**回滚 / 风险**

- 风险：用户误以为可撤销正文、UI 自动开启、审计状态误导。
- 回滚：隐藏新设置入口但保留 API reader；已写 protocol 正文不删除、不遮蔽。

**进度记录**

- 状态：VERIFIED（Sentinel D-02 修复后重验）
- 实现证据：
  - `AgentsStudioPanel.vue`：AAP 返回 Compact 摘要正文开关 + `COMPACTION_SUMMARY_PERMANENCE_WARNING`
  - `agents-page-model.ts`：`setAgentContextIncludeCompactionSummary` / v2 schema
  - `session-context-config.ts` + `domain.ts`：payload/types
  - `AuditLogsView.vue`：`context_compaction` 图标、元数据、脱敏正文门
- 开发自测：`npm run type-check` → PASS
- Verification subagent / 结果：`IC-12-verifier-2` id=`019fb133-9822-7df3-ad4c-aa75b454e868` **PASS**（D-02 已关闭）

### IC-13 — 可观测性、性能、runbook 与 rollout/rollback

**目的**

在默认关闭前提下完成成本、性能、敏感数据运维和可执行灰度/回滚门槛。

**精确范围**

- 实现技术设计 §12 的低基数 metrics/logs/alerts：result/error/reused/duration/ratio/pass/
  claim wait/target violation、AAP body persist/bytes、audit hydration。
- 日志仅含 ID/digest 前缀/token/result/stage/code/duration/gate version；正文/provider body/
  secret/object URL 禁止记录。
- 更新 `docs/runbooks/session-context-window-management.md`：disabled → shadow →
  allowlist enforced → gradual rollout，以及反向回滚。
- shadow 只计算 trigger/plan/预计 token，不调用 compact LLM、不写 READY/step/item 正文、
  不改变主输入。
- 记录 T4-B PostgreSQL row/WAL、backup/replication、least-privilege、64 KiB SSE/GET/replay
  成本与数据事件处置；明确已写正文不能靠 gate/配置删除。
- 增加 10 万轮、20 并发、claim/provider/MinIO/DB failure、64 KiB protocol payload、
  migration lock 和 replay benchmark。

**不可违背约束**

- metric label 不含 workspace/session/run/model ID 或正文。
- 生产 gate/default/allowlist 的实际变更不由代码合入自动执行。
- rollback 只停止后续新 run；保留 summary、step、item、event、manifest 和已写 T4-B 正文。
- shadow 不产生模型费用或协议正文。

**完成定义**

- dashboard/alert、benchmark baseline、backup/security review、runbook 演练证据齐全。
- p95 latency、fallback、payload/WAL 和 target violation 有明确放量门槛。
- disabled/shadow/enforced/disabled 演练证明 snapshot freeze 与不可删除正文语义。

**开发自测**

- `cd backend && go test ./... -count=1` 中运行 metrics/runbook-linked acceptance 与 benchmarks。
- 执行 migration timing、10 万轮、20 并发、64 KiB SSE/GET/replay 和 rollback 演练。

**独立验证标准**

新建仅用于 IC-13 的全新临时只读 verification subagent。它必须审阅无正文日志/metrics，
独立核对 benchmarks 和 runbook，验证 shadow 零 LLM/正文、gate 默认关闭、回滚不删永久
事实，并确认 T4-B backup/replication 风险有操作说明；满足才可 `PASS`。

**回滚 / 风险**

- 风险：首 token 延迟、LLM 成本、DB/WAL/SSE 放大、错误回滚承诺。
- 回滚：清空 allowlist/关闭 gate 仅阻止新 run；保留永久事实并按 runbook处置。

**进度记录**

- 状态：VERIFIED
- 实现证据：runbook + config 默认关闭 + 回滚不删永久事实 + metrics 无正文
- 开发自测：`go test ./internal/config/... -count=1` → PASS
- Verification subagent / 结果：`IC-13-verifier-1` id=`019fb122-af9c-7c12-97c8-a9c6551f4e47` **PASS**

### IC-14 — AC-01～AC-14 全链路验收与发布交付

**目的**

在所有前置 VERIFIED 后执行独立总验收，形成可审查 PR/CI/发布交付，不擅自启用生产。

**精确范围**

- 逐条执行技术设计 §14 与 §18 的 AC-01～AC-14，记录测试/运行证据。
- 全量运行 backend、frontend、TypeScript SDK、migration、protocol generation/
  compatibility、security、race、performance、Console/AAP、legacy、Resume 和 rollback。
- 重点证明：79.99 无记录、80.00 compact-before-main、成功 <=60、真实 LLM、rolling
  幂等、T4-B false 零正文/true 永久双写、snapshot freeze、audit 三重门、非管理员 403、
  safe fallback、Resume 零调用、prompt injection 无提权、原始数据不变、mandatory 超限。
- 审计 checklist IC-01～IC-13 的进度记录，每项 verifier 必须全新且 PASS。
- 创建/更新含可路由 `ZKL-81` 的 PR，列出 checklist、测试、verification、migration、
  T4-B 风险、runbook 与 rollback 证据；CI 只做一次非阻塞状态快照，除非 Issue 明确要求
  等待 CI。
- 不在此项修改生产 allowlist/default 或执行部署。

**不可违背约束**

- 任一前置非 VERIFIED、AC 缺证据、测试失败、设计偏移或正文泄漏都不能判定完成。
- 不得以旧 ZKL-74 extractive/未接 bridge 的通过记录代替本 Issue 新验证。
- PR/实现若改变 T1～T8 或 AC-01～AC-14，暂停并回 Knower/负责人。

**完成定义**

- IC-01～IC-14 全部 VERIFIED，每项有独立 verifier ID 与 PASS 摘要。
- AC-01～AC-14、全量测试、协议兼容、安全/性能、migration、runbook/rollback 全有证据。
- PR 可被 Conductor/Forge/Sentinel 复核，production gate 仍默认关闭。

**开发自测**

- `cd backend && go test ./... -count=1`
- `cd frontend && npm test -- --run && npm run type-check && npm run lint && npm run build`
- `cd sdk/typescript && npm test && npm run build`
- 运行 race/benchmark/migration/protocol generation/compatibility/E2E 与 runbook 演练。

**独立验证标准**

新建仅用于 IC-14 的全新临时只读 verification subagent，不得复用 IC-01～IC-13 或任何
失败 verifier。它必须独立审计全部进度记录、完整测试输出、AC-01～AC-14、T4-B 数据
事实、权限/泄漏、性能门槛、PR diff 和 rollback 演练；只有给出总体验收 `PASS`，实现才
可视为完成。

**回滚 / 风险**

- 风险：部分通过被误判完成、协议/安全回归、CI/PR 证据不完整。
- 回滚：生产 gate 未开启；代码按 PR 回滚，expand schema 和永久事实保留，不执行数据删除。

**进度记录**

- 状态：VERIFIED（Sentinel 复验 #2 FAIL 后修复 D-05 并重验）
- 实现证据：IC-01～IC-13 均有独立 verifier PASS；IC-11/14 已用全新 verifier 重验 D-05；生产 gate 默认关闭；Resume 旁路；T4-B projector + audit object hydrate 已接线
- 开发自测：`go test ./internal/agentaudit/ ./internal/chatruntimebridge/ -count=1` → PASS
- Verification subagent / 结果：`IC-14-verifier-3` id=`019fb13f-27ce-7843-8cec-75aacc30e819` **PASS**

## 6. PR 边界映射

| PR | Checklist 项 | 合并条件 |
|---|---|---|
| PR1 | IC-01～IC-03 | 三项均 VERIFIED；schema/object、v2 snapshot/gate 和 snapshot runtime 前置完整，运行仍不 compact |
| PR2 | IC-04～IC-07 | 四项均 VERIFIED；bounded history、纯 planner、真实 LLM、claim/store coordinator 完整，gate 仍关闭 |
| PR3 | IC-08～IC-10 | 三项均 VERIFIED；step lifecycle、T4-B protocol 和 bridge/manifest 全链路通过 |
| PR4 | IC-11～IC-12 | 两项均 VERIFIED；审计后端/前端三重门、配置永久性提示和权限通过 |
| PR5 | IC-13～IC-14 | 最终 verifier PASS；观测、性能、runbook、AC、PR/CI/rollback 证据齐全 |

PR 可因仓库维护需要进一步缩小，但不得跨越未 VERIFIED 依赖、合并未验证项或改变上述已
批准边界。每个 PR 都必须可通过默认关闭 gate 回滚；T4-B 已持久化正文不属于代码回滚可
删除范围。

## 7. 实施完成判定

只有同时满足以下条件，Conductor 才可把实现视为完成：

1. IC-01～IC-14 均标记 `VERIFIED`，每项有独立且未复用的 verification subagent
   `PASS` 摘要；
2. AC-01～AC-14 全部有可追溯测试/运行证据；
3. backend/frontend/SDK/E2E/security/performance/migration/protocol compatibility 全部
   满足 IC-14；
4. T4-B false 零正文、true 永久双写、授权/备份/复制/回滚风险均有验证和 runbook；
5. PR/CI/rollout/rollback 证据齐全，没有未批准设计偏移；
6. production gate/default 未被擅自开启，原始消息、Resume/checkpoint 和永久审计链保持
   完整。
