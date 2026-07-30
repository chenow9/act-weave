# Agent 上下文 LLM Compact 技术设计

> Issue：ZKL-81 / `786fefed-9e1b-481b-9c55-50f97069f320`
>
> 文档版本：v0.1
>
> 状态：已批准，等待 Conductor 安排实施
>
> 技术批准：负责人 chenow，评论 `979d6cd6-b260-4588-b37c-a76f18c36859`
>
> 生效选择：T1-A、T2-A、T3-A、T4-B、T5-A、T6-A、T7-A、T8-A
>
> 产品输入：`docs/design/agent-context-llm-compaction-product-design.md` v0.2（已批准）
>
> 继承基线：`docs/design/session-context-window-management-technical-design.md` v0.1（ZKL-74）
>
> 代码证据基线：`release_v1` / `705a8a8`

## 1. 结论摘要

推荐在现有 ZKL-74 的 `session-context` 快照、token window assembler、永久摘要表和协议事件投影之上，增加一条默认关闭、按新 run 快照冻结的 **LLM rolling compact** 路径，而不是替换原始消息或另建一套 Console/AAP 运行时。

关键结论如下：

1. 80% 触发与 60% 收敛均使用当前 run 已冻结的 `effectiveMaxInputTokens`，以整数 basis points 比较，避免边界浮点误差。
2. compact 只发生在初始运行的主模型调用前；resume 沿用 checkpoint，绝不再次 compact。
3. compact 模型必须由当前 run 的 model snapshot 构建；禁用 tools、tool choice、审批和流式输出，使用版本化专用模板与受限结构化输出。
4. 原始 `chat_messages` 永久保留且不改写；摘要权威正文进入永久、敏感、加密的
   `CHAT_CONTEXT_SUMMARY` stored object，并始终以不可信 `ASSISTANT` 消息注入主模型；
   T4-B 允许对 snapshot=true 的成功 run 另存永久协议明文副本。
5. 每个发生 compact 尝试的 run 复用 `agent_run_steps` 保存唯一生命周期事实，复用 `chat_context_summaries` 保存可跨 run 复用的摘要事实；不新增第三张重复事实表。
6. AAP 新增 additive 的 `context_compaction` item。按已批准 T4-B，run 快照中
   `aap.includeCompactionSummary=true` 时，最终 `run_items.snapshot` 与
   `item.completed` event 会永久保存实际注入的摘要明文；false 时只保存元数据。
   replay 直接读取同一永久事实，不做 late hydration。
7. 任一可恢复 compact 失败都记录 `failed + fallback`，随后仅使用现有有界 `token_window`；严禁回退全量历史。scope/integrity 异常时不使用可疑摘要：能够持久化完整降级证据才继续 token window，否则在主模型调用前终止 run。
8. 审计默认只返回 compact 元数据且不读取摘要对象；只有全局 `agentAudit.debug=true`、平台管理员身份和 UI 主动关闭遮罩三项同时成立时才显示正文。
9. 独立 compaction gate 默认关闭。必须先修复“tool schema 未计入 assembler”和“bridge 未真正从 run model/prompt snapshot 构建请求”两个现状缺口，才允许进入 allowlist。

对应 implementation checklist：
`docs/design/agent-context-llm-compaction-implementation-checklist.md`。

## 2. 已冻结产品决策与追踪

产品设计 v0.2 已明确批准以下决策，本技术设计不重新打开它们：

| 产品决策 | 冻结结论 | 技术落点 |
|---|---|---|
| D1-A | 80% 分母为运行时有效输入上限 | `session-context.v2.effectiveMaxInputTokens` |
| D2-A | 当前初始 run 主模型调用前同步 compact | Bridge preflight；resume bypass |
| D3-A | compact 成功后占用率不高于 60% | `targetBps=6000`，完成前二次估算 |
| D4-A | 当前 run 快照模型、专用模板、tools/approval 禁用 | `SnapshotModelFactory` + `LLMCompactor` |
| D5-A | AAP 新 item；Agent 配置控制正文，默认 false | `context_compaction` + policy/snapshot v2 |
| D6-A | 失败明确审计并降级 token window | step/item `result=fallback` |
| D7-A | 仅 debug + 平台管理员 + UI 解除遮罩显示正文 | Audit service/server/UI 三重门 |

产品 AC-01～AC-14 在第 18 节逐项映射到实现与验证标准。

### 2.1 技术批准记录

负责人在评论 `979d6cd6-b260-4588-b37c-a76f18c36859` 明确批准技术设计 v0.1：

```text
T1-A、T2-A、T3-A、T4-B、T5-A、T6-A、T7-A、T8-A
```

其中 T4-B 明确接受以下取舍：仅当 run 创建时快照的
`aap.includeCompactionSummary=true`，把实际注入的规范化摘要正文直接、永久写入
PostgreSQL `protocol_events.payload` 与 `run_items.snapshot`。该正文是敏感数据的明文
JSONB 投影，会进入数据库备份、复制与永久协议保留；默认 false，不提供事后删除或
request 级覆盖。审计正文仍只从加密 summary object 读取并遵守 debug/admin/UI 三重门，
不得因协议中存在正文而绕过审计脱敏。

## 3. 现状证据

### 3.1 已有可复用能力

| 证据 | 当前事实 | 本方案处理 |
|---|---|---|
| `backend/internal/sessioncontext/policy.go` | 已有 `token_window` / `rolling_summary`、预算、`maxRecentTurns` 和 summary knobs；v1 严格拒绝未知字段 | 保留 v1，新增可兼容读取的 policy v2 |
| `backend/internal/sessioncontext/snapshot.go` | 已解析并冻结模型窗口、有效输入上限、输出预留、估算器和 summary 配置；快照为严格 `session-context.v1` | 新 run 在独立 gate 命中时写 `session-context.v2` |
| `backend/internal/contextwindow/assembler.go` | 支持 `OptionalSummary`，摘要以带警示前缀的 `ASSISTANT` 注入；mandatory input 超限返回类型化错误 | 继续作为最终组装器；增加 compact preflight/plan 层 |
| `backend/internal/contextsummary/repository.go` | 已有 claim lease、READY/FAILED 状态、摘要覆盖边界、父摘要和幂等键 | 扩充查询、内容存取和 LLM 元数据，不重建 repository |
| `backend/internal/database/migrations/000003_chat_context_summaries.up.sql` | 已有永久 `chat_context_summaries` 和 `CHAT_CONTEXT_SUMMARY` kind | 用后续 expand migration 补 FK/生成方式，不改写原始消息 |
| `backend/internal/protocolevent`、`run_items` | 已有 item lifecycle、事务投影和 unknown item 容错 | 新增 additive item，不新增 event type |
| `backend/internal/agentaudit`、`agent_run_steps` | 已有永久 step 事实和管理员审计时间线 | 新增 compact renderer 与正文读取门 |
| `docs/runbooks/session-context-window-management.md` | 已有 gate、shadow/enforced、bounded history、回滚与永久保留原则 | compaction 作为独立子 gate 延续相同发布纪律 |

### 3.2 必须先补齐的真实缺口

以下不是未来优化，而是启用本功能前的硬依赖：

1. `backend/internal/contextsummary/generator.go` 当前调用 `buildExtractiveSummary`，没有模型调用，默认模板为 `extractive.v1`；这不满足“LLM compact”，且该 generator 未接入 bridge。
2. `backend/internal/chatruntimebridge/bridge.go` 的 token-window 路径没有把 `OptionalSummary` 传给 assembler，因此 rolling summary 当前没有实际生效。
3. 同一 bridge 路径在组装上下文时没有传入已构建的 tool schemas，导致 `toolsOverheadTokens` 实际为零；在修复前无法证明 80% 分母和 mandatory input 估算正确。
4. 当前 bridge 仍读取 live Agent/model/prompt 配置构建调用；已有 run snapshot 尚未完整成为执行事实。D4 要求 compact 和主调用使用同一 run snapshot，因此必须先建立共享 `SnapshotModelFactory` 和不可变 prompt/tool 解析路径。
5. 当前 bounded history 只服务 recent suffix，没有“查找最新可复用 READY 摘要”“按 coverage 后向前读取”“从 session 起点向 coverage boundary 分块读取”的 repository contract。
6. `CHAT_CONTEXT_SUMMARY` 虽已进入数据库 kind check，但 `backend/internal/storedobject/minio_store.go::bucketForKind` 未路由该 kind，`secure_store.go::requiresPermanentSensitiveContent` 也未强制其永久敏感策略；当前真实 secure put 会失败或缺少强约束。
7. `chat_context_summaries.content_object_id`、父摘要与 coverage message 当前缺少相应 FK；`summarizer_snapshot` 和 token estimates 尚未由现有 claim/ready 路径完整写入。
8. AAP 已能安全保留 unknown item，但 Go schema、辅助投影器、OpenAPI 和 TypeScript SDK 尚无 `context_compaction` 类型。
9. Audit builder 目前只专门识别 MODEL/TOOL/WORKFLOW；没有 compact 的状态、降级措辞或受控正文读取。
10. resume 当前绕过 initial assembly。这是正确的兼容行为，应被测试锁定而不是改造。

## 4. 目标与非目标

### 4.1 目标

1. 在逻辑上下文占用达到或超过有效输入上限 80% 时，于初始主模型调用前同步执行真实 LLM compact。
2. compact 成功后，以同一估算器验证最终实际请求不超过 60%。
3. 通过 rolling parent summary + 连续原始消息区间，使摘要可复用、可滚动、可证明覆盖范围。
4. 保证并发 claim、worker retry、run retry 下至多生成一个等价 READY 摘要，并且每个 run 只有一个 compact lifecycle。
5. AAP 和管理员审计都能看到发生时间、结果、压缩前后 token、覆盖量、降级原因和稳定错误码。
6. Agent 可配置 AAP 是否包含摘要正文，默认关闭；请求方不能临时覆盖。
7. 任一失败保持 prompt 边界安全、workspace/session 隔离、永久证据和明确回滚路径。

### 4.2 非目标

1. 不删除、覆盖、重排或修改原始 chat message。
2. 不把摘要变成 SYSTEM、memory、RAG、权限声明、工具授权或审批结果。
3. 不让 AAP 单次请求选择是否返回正文。
4. 不改变 resume/checkpoint 的输入恢复语义。
5. 不为 Console 和 AAP 建立两套 compact 生成链路。
6. 不提供摘要编辑、删除、下载 URL 或公开读取 endpoint。
7. 不使用本地抽取式摘要作为 LLM 失败时的“成功结果”。
8. 不在本 Issue 中重新设计 tokenizer、模型上下文窗口、run snapshot 或全局审计权限模型；仅补足本功能必需的连接点。

## 5. 术语、计算与不可变约束

### 5.1 预算定义

继承 ZKL-74 的预算定义：

```text
hardInputCeiling
  = modelContextWindowTokens
    - effectiveOutputReserveTokens
    - safetyMarginTokens

effectiveInputCeiling
  = min(hardInputCeiling, configuredMaxInputTokens when non-zero)
```

所有值来自当前 run 的不可变 snapshot。compact 不重新读取 live Agent policy、模型窗口、rollout gate 或模板选择。

### 5.2 占用率

为避免 79.99% / 80.00% 边界因浮点误差漂移，内部只使用整数：

```text
triggered =
  triggerInputTokens * 10000
  >= effectiveInputCeiling * 8000

targetMet =
  finalInputTokens * 10000
  <= effectiveInputCeiling * 6000
```

乘法使用 checked `int64` 或先约分，溢出视为 `CONTEXT_BUDGET_INVALID`，不得继续模型调用。

`triggerInputTokens` 是以下逻辑候选的估值：

- run snapshot 对应的 SYSTEM prompt；
- 本次实际 tool schemas 和 tool-choice envelope；
- 最新合法 READY parent summary（若有）；
- parent coverage 之后的所有完整原始对话轮次；
- 当前 USER message；
- provider/chat framing 固定开销。

`maxRecentTurns` 不参与“是否达到 80%”的判断；它只约束 compact 后或 token-window fallback 的 raw recent suffix。否则默认 20 轮的小上限可能让长会话永远无法触发 compact。该语义已按 T2-A 批准。

### 5.3 硬约束

1. 当前 USER、SYSTEM、tool schemas 都是 mandatory input，永不进入摘要。
2. 只有完整且已终态的 USER→ASSISTANT 对话轮次可进入 coverage；pending confirmation、半轮、失败中的当前轮不得进入。
3. 摘要 coverage 必须是 session 中“可进入上下文的完整对话消息”连续前缀；新摘要只可在父摘要 coverage 之后单调前进。它与最终保留的 raw suffix 在该序列上无重叠、无断裂。
4. 摘要正文存储时不带权限；注入时统一添加 `UntrustedSummaryPrefix` 并使用 `ASSISTANT` role。
5. 摘要不能触发 tools、审批、workflow 或其他副作用。
6. `completed` 必须意味着最终主请求估值已验证 `<=60%`；未达目标只能继续 pass 或进入 fallback。
7. fallback 只能调用同一有界 token-window assembler，绝不能 `ListAllMessages`。
8. 原始消息、READY 摘要、run snapshot、step 起始证据和 protocol events 均永久保留。

## 6. 推荐架构

```text
Run claim
   |
   v
SnapshotRuntimeResolver
   |-- immutable prompt revision
   |-- immutable model/runtime caps
   |-- immutable tool schemas
   `-- session-context.v2
   |
   v
ContextPreflight
   |-- mandatory input check
   |-- latest valid READY summary
   |-- bounded logical occupancy scan
   `-- trigger/target plan
          |
          +-- < 80% --------------------------+
          |                                    |
          v                                    |
ContextCompactionCoordinator                   |
   |-- ensure run step + item.started          |
   |-- claim summary pass                      |
   |-- SnapshotModelFactory                    |
   |-- LLMCompactor (no tools/approval)        |
   |-- validate + encrypted permanent store    |
   |-- rolling passes until target             |
   `-- finalize step + item.completed/failed   |
          |                                    |
          +---------- success/fallback --------+
                       |
                       v
ContextWindowAssembler
   |-- summary + bounded recent raw, or
   `-- bounded token_window fallback
                       |
                       v
                 Main model call

Permanent facts:
  chat_context_summaries -> encrypted stored object
  agent_run_steps        -> per-run compact lifecycle
  run_items/events       -> AAP projection; snapshot true 时含永久明文摘要
  assembly manifest      -> exact request plan, no bodies
```

### 6.1 模块边界

| 模块 | 单一职责 | 不允许承担 |
|---|---|---|
| `SnapshotRuntimeResolver` | 从 run snapshot 解析 prompt/model/tools/context policy | 读取 live 配置补算旧 run |
| `ContextPreflight` | 预算、parent 校验、有界扫描、触发与 pass plan | 调模型、写协议事件 |
| `ContextCompactionCoordinator` | 生命周期、claim、pass 编排、fallback 决策 | 直接拼 prompt 或绕过 repository |
| `LLMCompactor` | 用 snapshot model 和专用模板生成候选结构 | tools、审批、主任务推理 |
| `SummaryValidator` | schema、大小、token、角色、敏感模式与 coverage 校验 | 修改原始消息 |
| `SummaryBodyStore` | 永久敏感加密 put/open/完整性验证 | 对外提供对象 URL |
| `ContextWindowAssembler` | 组装最终主模型请求并二次估算 | 读取 DB 或调用 provider |
| `ContextCompactionProjector` | 原子保存 step/item/event；按 run snapshot 决定是否写永久摘要明文 | 读取 live Agent 配置或写 provider body |
| `ContextCompactionPayloadBuilder` | 从已验证的最终正文构造严格、大小受限的 item/event snapshot | 从 stored object 事后补水或改写永久 event |
| `AuditCompactRenderer` | 管理员 compact 时间线与三重遮罩 | 从 AAP 协议明文绕过审计门读取正文 |

## 7. 初始运行详细流程

### 7.1 Snapshot 解析

run 被创建时解析并冻结 `session-context.v2`：

```json
{
  "schemaVersion": "session-context.v2",
  "mode": "rolling_summary",
  "modelContextWindowTokens": 128000,
  "effectiveMaxInputTokens": 121856,
  "outputReserveTokens": 4096,
  "safetyMarginTokens": 2048,
  "maxRecentTurns": 20,
  "tokenizerProfile": "o200k_base",
  "tokenizerVersion": "2026-01",
  "outputTokenLimitMode": "max_tokens",
  "compaction": {
    "triggerBps": 8000,
    "targetBps": 6000,
    "maxSummaryTokens": 2048,
    "minEvictedTurns": 4,
    "maxGenerationPasses": 2,
    "templateVersion": "context-compaction.v1",
    "templateHash": "<sha256>",
    "totalTimeoutMs": 45000,
    "perPassTimeoutMs": 20000,
    "claimWaitMs": 1000
  },
  "aap": {
    "includeCompactionSummary": false
  },
  "sources": {
    "workspacePolicyVersion": 3,
    "agentPolicyVersion": 9,
    "rolloutVersion": "context-compaction-2026-07",
    "compactionGateEnabled": true
  }
}
```

约束：

- 80/60 是平台冻结常量，只读显示，不允许 Agent 或 workspace 配置改写。
- `aap.includeCompactionSummary` 只允许来自 Agent policy，缺省 false；workspace policy 和 AAP request 都不能把它临时打开。
- timeout 写入 snapshot 是为了 retry 一致性；部署默认值为 45s total、20s/pass、1s claim wait。
- 显式未知 snapshot version 返回 `CONTEXT_SNAPSHOT_UNSUPPORTED`；不从 live policy 猜测。
- legacy `{}`、`session-context.v1` 和 gate 未命中的新 run 保持原有行为，不暗中执行 compact。

### 7.2 Mandatory input 检查

Bridge 必须先构建与主调用完全相同的 SYSTEM、tool schemas、当前 USER 和模型 framing，再由同一 estimator 估算。

若 mandatory input 已超过 `effectiveInputCeiling`：

- 返回现有 `CONTEXT_REQUIRED_INPUT_TOO_LARGE`；
- 不调用 compact 模型，因为任何历史摘要都无法缩小 mandatory input；
- 不创建“成功/降级” compact item；可创建普通运行失败审计，但不能伪称发生 compact；
- 主模型不得被调用。

### 7.3 选择 parent summary

新增 repository 查询：

```text
FindLatestReady(
  workspace_id,
  session_id,
  generation_method = LLM,
  policy_fingerprint,
  prompt_template_hash,
  summarizer_snapshot_hash
)
```

候选必须全部满足：

1. workspace/session 与当前 run 完全一致；
2. status=READY，generation_method=LLM；
3. coverage 起止消息存在、属于同一 session、顺序正确；
4. source digest、parent digest、content SHA-256 和 stored-object kind 全部可验证；
5. policy fingerprint 包含 resolved budget、estimator、摘要 knobs、snapshot model hash 与 template hash；
6. coverage 后没有被修改或删除的消息；当前数据模型禁止删除，但仍做完整性验证；
7. 摘要正文解密后 token/byte 上限仍满足当前模板契约。

候选不合法时：

- scope/integrity 可疑：拒绝使用并记录稳定错误；
- 对象暂时不可用：本次可进入 token-window fallback；
- 旧 `extractive.v1` 或 generation_method=LEGACY_EXTRACTIVE：只保留审计，不得作为 LLM READY success 复用。

### 7.4 有界触发扫描

新增两种 keyset page contract：

1. `ListCompleteTurnsAfterCoverageNewestFirst`：从当前 USER 之前向过去扫描，直到已证明达到 80%，或到达 parent coverage/session 起点。
2. `ListCompleteTurnsAfterCoverageOldestFirst`：compact pass 从 parent coverage 之后按连续顺序取内容，单页和累计 token 都受限。

算法不会一次加载整段历史：

- 有 parent 时，先计入 parent summary token，再倒序累加新完整轮次。
- 无 parent 时，从最近轮次倒序累加；达到 80% 即停止触发扫描。
- 若扫描到起点仍未达到 80%，逻辑候选本身小于 80%，因此已解密内容天然受有效窗口上界约束。
- 命中 80% 后，另行按 oldest-first 分块读取需要覆盖的前缀；每个 pass 都受 snapshot model 输入预算和 `maxGenerationPasses` 限制。

任何 query 必须带 `workspace_id + session_id + keyset anchor + limit`，禁止 offset pagination 和无界 `ListMessages`。

### 7.5 选择 coverage 与 recent suffix

触发后，planner 从最新完整轮次向过去选择 raw suffix，并为以下内容预留预算：

- mandatory input；
- `UntrustedSummaryPrefix`；
- `maxSummaryTokens`；
- provider framing。

raw suffix 同时满足：

1. summary reserve + suffix + mandatory `<=60%`；
2. `maxRecentTurns=0` 表示无轮数额外限制，否则 suffix 不超过该值；
3. coverage end 位于 suffix 前一个完整轮次；摘要必须连续覆盖到该边界；
4. 当前 USER 永远在 coverage 外。

若没有任何可摘要完整轮次，或可驱逐轮次少于 `minEvictedTurns`，本次记录
`CONTEXT_COMPACTION_INSUFFICIENT_EVICTABLE_TURNS` 并进入 token-window fallback。该路径仍算“在 80% 时发起过 compact 决策”，但不调用没有有效输入的模型。

### 7.6 LLM pass

每个 pass 的模型输入由以下内容构成：

1. 固定的 `context-compaction.v1` system template；
2. parent summary 的受限正文（首轮可为空）；
3. 紧接 parent coverage 的连续完整原始轮次；
4. coverage message IDs、内容 digest 等只作为数据边界，不作为权限指令。

模型调用约束：

- 由 `SnapshotModelFactory` 使用当前 run 的 model snapshot 构建；
- 与主调用同一 provider/model/runtime capability snapshot，但使用专用 prompt；
- `Generate` 非流式；
- temperature=0；
- `max_tokens=maxSummaryTokens`，且受 provider hard cap 再 clamp；
- tools 为空，tool choice 为 forbidden/none；
- 不创建 capability、workflow、confirmation 或子 run；
- 总时间、每 pass 时间和 claim 等待均受 snapshot 限制；
- provider 原始错误和原始响应不写日志、step、item 或 audit。

专用输出推荐为受限 JSON：

```json
{
  "stableFacts": ["..."],
  "decisions": ["..."],
  "openItems": ["..."],
  "recentState": ["..."]
}
```

`SummaryValidator` 仅接受：

- 单一 assistant 文本结果；
- 无 tool calls、无多候选、无空正文；
- 严格 UTF-8 和严格 schema，拒绝未知字段；
- 每个数组和字符串有数量/字节上限；
- 标准化后正文不超过 64 KiB 且估值不超过 `maxSummaryTokens`；
- 不包含平台 secret、credential、Authorization header、内部对象 URL 等已知高风险模式。

通过后由平台做确定性排序保持、Unicode/换行标准化和模板化渲染。`UntrustedSummaryPrefix` 只在主 prompt 组装时添加，不纳入摘要正文。

### 7.7 Rolling pass 与目标验证

一个 pass 成功后产生新的 READY summary，并把 coverage 单调推进：

```text
parent summary + next continuous raw chunk
  -> new summary covering [session start, new coverage end]
```

随后使用新摘要、planned raw suffix、mandatory input 重新调用同一 estimator：

- `<=60%`：compact completed；
- `>60%` 且仍有 pass：下一 pass；
- `>60%` 且达到 `maxGenerationPasses`：`CONTEXT_COMPACTION_TARGET_NOT_MET`，fallback；
- 任一 pass 不得跳过中间消息或让 child coverage 倒退。

只有最后实际注入主调用的 summary 才写入 run compact step 的 `summaryId` 和 assembly manifest。中间 READY summary 仍作为永久 rolling 链事实保留，可被后续 pass/run 复用。

### 7.8 最终组装

成功：

```text
SYSTEM
untrusted ASSISTANT summary
continuous recent complete-turn suffix
current USER
```

fallback：

```text
SYSTEM
bounded token-window complete-turn suffix
current USER
```

两条路径都必须：

- 传入真实 tool schemas；
- 使用同一 estimator/version；
- 输出 `effectiveOutputLimitTokens` 并 clamp 主模型调用；
- 最终再验证一次不超过 effective ceiling；
- 写 body-free assembly manifest；
- 绝不在失败后装载全量历史。

## 8. 数据模型与迁移

### 8.1 复用的事实模型

| 事实 | 存储 | 生命周期 |
|---|---|---|
| 可跨 run 复用的 rolling summary | `chat_context_summaries` | BUILDING → READY/FAILED；READY 永久不可变 |
| 摘要正文 | `stored_objects` + MinIO | `CHAT_CONTEXT_SUMMARY`、PERMANENT、SENSITIVE、加密 |
| 单个 run 的 compact 尝试 | `agent_run_steps` | RUNNING → SUCCEEDED/FAILED |
| AAP 可见投影 | `run_items` + `protocol_events` | item.started → item.completed；T4-B 下 snapshot=true 时 completed 永久含正文 |
| 主模型实际上下文计划 | assembly manifest stored object | 永久、body-free |

不新增 `agent_context_compactions` 表，避免 step/item/新表三套 lifecycle 相互漂移。
completed step 的 `raw_object_id` 指向最终实际注入的 summary content object，
`output_summary.summaryId` 指向 summary metadata；fallback/failed 的 `raw_object_id` 为空。

### 8.2 `chat_context_summaries` expand migration

在 `backend/internal/database/migrations/000004_agent_context_llm_compaction.{up,down}.sql`
中做 expand；生产回滚不执行 destructive down：

1. 新增 `generation_method TEXT NOT NULL DEFAULT 'LEGACY_EXTRACTIVE'`，约束值为
   `LEGACY_EXTRACTIVE | LLM`。所有新 compact READY 行必须显式写 `LLM`。
2. 新增或补齐以下 `NOT VALID` FK，再在 gate 开启前完成校验：
   - `(workspace_id, content_object_id)` → `stored_objects(workspace_id, id)`；
   - `(workspace_id, parent_summary_id)` → `chat_context_summaries(workspace_id, id)`；
   - `(workspace_id, session_id, coverage_start_message_id)` → `chat_messages(...)`；
   - `(workspace_id, session_id, coverage_end_message_id)` → `chat_messages(...)`。
3. 为 latest READY lookup 增加部分索引：

   ```text
   (workspace_id, session_id, coverage_end_message_id, ready_at DESC, id)
   WHERE status='READY' AND generation_method='LLM'
   ```

4. READY LLM 行通过 repository 强制 coverage 起止、summarizer snapshot、token estimates、estimator version 非空；若要下沉为 DB check，先对历史行按 `generation_method` 分支，不破坏 legacy 数据。
5. `policy_fingerprint` 的规范输入扩充为：

   ```text
   resolved context policy
   + tokenizer profile/version
   + run model snapshot canonical hash
   + prompt template hash
   + normalization schema version
   ```

   现有唯一键因此已经包含模型与模板身份，不需要破坏性替换 unique constraint。
6. LLM 行的字段语义固定为：
   - `coverage_start_message_id` 始终继承 root summary 的首条 eligible message；
   - `coverage_end_message_id` 是本摘要覆盖的最后一条 eligible message；
   - `source_message_count` 是累计覆盖数，不是本 pass 增量数；
   - `source_digest` 使用 domain-separated `context-source-chain.v1`，由 parent 的累计 source digest 与本 pass 连续原始消息 canonical tuple 递推；
   - `parent_summary_digest` 必须等于 parent 的 `content_sha256`，不得继续使用 parent `source_digest` 代替。
7. 现有唯一约束比新版 claim key 更粗。`ClaimOrGet` 在命中冲突行后还必须比较
   `parent_summary_id + parent_summary_digest + summarizer_snapshot canonical hash`；任一不一致均 fail closed，绝不复用该行。

迁移前检查：

- 是否已有 READY legacy 行；
- content object、parent、coverage 是否存在孤儿；
- FK validate 的锁时间；
- 索引并发创建是否符合当前 migration runner 能力。

不自动把 `extractive.v1` 标记为 LLM，也不为其伪造正文。

### 8.3 Stored object 修复

必须把 `KindChatContextSummary`：

- 映射到 execution bucket；
- 加入 `requiresPermanentSensitiveContent`；
- 强制 `RetentionMode=PERMANENT`；
- 强制 `Classification=SENSITIVE` 或更高；
- 强制通过 `SecureStore` 加密；
- 禁止 presigned download；
- 限制明文最大 64 KiB。

对象 ID 使用 summary ID，保证稳定关联。新增 `SummaryBodyStore.PutOrVerify`：

1. 首次 put 正常加密写入；
2. 若同 ID 已存在，内部 open、解密并核对明文 SHA-256/长度/kind/scope；
3. 完全相同则视为幂等成功；
4. 任一不一致返回 integrity conflict，不覆盖已有对象。

这样可覆盖“对象已写入、DB MarkReady 前进程退出”的 retry，不需要删除永久对象。

### 8.4 Assembly manifest 扩展

manifest 只写元数据：

```json
{
  "mode": "rolling_summary",
  "triggerBps": 8000,
  "targetBps": 6000,
  "triggerInputTokens": 98500,
  "effectiveInputTokens": 121856,
  "finalInputTokens": 70120,
  "summaryId": "<uuid>",
  "summaryDigest": "<sha256>",
  "summaryCoverageStartMessageId": "<uuid>",
  "summaryCoverageEndMessageId": "<uuid>",
  "summarySourceMessageCount": 42,
  "summaryPasses": 1,
  "summaryReused": false,
  "fallback": null,
  "estimatorProfile": "o200k_base",
  "estimatorVersion": "2026-01"
}
```

manifest 不写 SYSTEM、原始消息、摘要正文、provider 响应或 secret。

### 8.5 保留与删除

- 原始消息永久不变；
- READY summary、run step、protocol event、run item 和 manifest 永久保留；
- FAILED BUILDING claim 也作为失败元数据保留，正文为空；
- `aap.includeCompactionSummary=true` 的成功 run 会把同一规范化正文永久复制到
  `run_items.snapshot` 与 `item.completed` 的 `protocol_events.payload`；两份 JSONB
  都是 PostgreSQL 可查询明文，并随数据库备份、复制和协议保留长期存在；
- `aap.includeCompactionSummary=false`、building、fallback 与 failed 事实不得出现
  `summary` 字段；run 创建后的 live 配置变更不得增加、删除或遮蔽既有协议正文；
- 协议正文必须与实际注入正文逐字节相同并匹配 `summaryDigest`；加密
  `CHAT_CONTEXT_SUMMARY` 对象仍是主组装与管理员审计的正文事实源；
- 不提供用户删除摘要的单独路径；
- workspace 级法定删除若未来存在，必须由统一 retention/erasure 方案处理，不在本 Issue 私自增加例外。

## 9. API、AAP 与兼容性

### 9.1 Agent policy API

新增 `session-context-policy.v2`。v1 继续可读写；只有需要 AAP disclosure 配置时才写 v2：

```json
{
  "schemaVersion": "session-context-policy.v2",
  "mode": "rolling_summary",
  "maxInputTokens": 0,
  "outputReserveTokens": 4096,
  "safetyMarginTokens": 2048,
  "maxRecentTurns": 20,
  "summary": {
    "maxTokens": 2048,
    "minEvictedTurns": 4,
    "maxGenerationPasses": 2
  },
  "aap": {
    "includeCompactionSummary": false
  }
}
```

写入规则：

- Agent management endpoint 可写 `aap.includeCompactionSummary`；
- workspace baseline schema 不接受该字段；
- 缺失严格归一为 false；
- AAP create/run request 不新增 override 字段；
- 后端 response 回显解析后的 Agent 值；
- 前端在开启前必须明确提示：成功 compact 的摘要正文会作为永久明文 JSONB 写入
  `run_items` / `protocol_events` 及其备份，关闭配置只影响后续新 run。

### 9.2 AAP `context_compaction` item

不新增协议 event type，继续使用 `item.started` / `item.completed`：

```json
{
  "id": "<item-id>",
  "type": "context_compaction",
  "status": "completed",
  "result": "completed",
  "triggerThresholdBps": 8000,
  "targetThresholdBps": 6000,
  "triggerInputTokens": 98500,
  "effectiveInputTokens": 121856,
  "beforeTokens": 98500,
  "afterTokens": 70120,
  "coveredMessageCount": 42,
  "coveredTurnCount": 21,
  "summaryId": "<opaque-summary-id>",
  "summaryDigest": "<sha256>",
  "passes": 1,
  "reused": false,
  "contentIncluded": false
}
```

若该 run 的 snapshot 为 `aap.includeCompactionSummary=true`，completed item 改为：

```json
{
  "contentIncluded": true,
  "summary": "<exact normalized body injected into the main Agent context>"
}
```

fallback 例：

```json
{
  "type": "context_compaction",
  "status": "failed",
  "result": "fallback",
  "fallback": {
    "from": "rolling_summary",
    "to": "token_window"
  },
  "error": {
    "code": "CONTEXT_COMPACTION_MODEL_TIMEOUT",
    "stage": "model"
  },
  "contentIncluded": false
}
```

硬失败使用 `status=failed, result=failed`。可恢复 fallback 与导致 run 终止的 failed 通过 `result` 区分。

兼容要求：

- 旧客户端按既有要求忽略未知 item type/fields 并推进 cursor；
- Go 协议 schema 新增严格 `ContextCompactionItem`；
- TypeScript SDK 新增 typed interface 与 type guard，同时保留 generic unknown 分支；
- OpenAPI item union additive 更新；
- event envelope、cursor、sequence 和 replay contract 不变；
- `source_type=RUNTIME`，`source_id` 绑定 compact step ID；不暴露 stored object ID。

### 9.3 T4-B：正文永久协议投影

`ContextCompactionPayloadBuilder` 在 compact 完成、主模型调用前，使用当前 run 的不可变
snapshot 构造最终 item/event：

1. snapshot=false：`contentIncluded=false`，删除/拒绝 `summary` 字段；
2. snapshot=true：`contentIncluded=true`，`summary` 等于最终实际注入主 Agent 上下文的
   READY 规范化正文，不包含 `UntrustedSummaryPrefix`；
3. `summaryDigest` 必须等于该正文 SHA-256，并与 READY summary metadata、step 和
   assembly manifest 一致；
4. 正文必须在 LLM 输出阶段已通过严格 schema、UTF-8、secret pattern、64 KiB 和 token
   上限校验，并再次通过 protocol payload size/secret guard；
5. building、fallback、failed item 永远不含 `summary`。

`Finalize` 在同一 transaction 内把完全相同的 completed snapshot：

- 写入 `run_items.snapshot`；
- 附在 `item.completed` 的 `protocol_events.payload`。

任一 guard、序列化或数据库写入失败均按 T8-A 返回
`CONTEXT_COMPACTION_EVIDENCE_PERSIST_FAILED`，在主模型调用前 hard fail；不得只写一份、
异步补写或退回 late hydration。

AAP run items list/get、SSE catch-up 和 SSE live follow 直接读取永久 projection，不再打开
`CHAT_CONTEXT_SUMMARY` 对象或读取 live Agent 配置。配置在 run 创建后从 false 改 true
不会给旧 run 补正文，从 true 改 false 也不会从旧 run 删除/遮蔽正文。GET run 的 ETag
直接覆盖已持久化 snapshot。

所有 AAP 授权必须在 repository list/get/stream 之前完成，并继续使用现有
workspace/agent/conversation/run scope predicate；无权 principal 沿用 403/404
不可见语义，不能据响应差异判断 summary 是否存在。

禁止增加下载 URL、summary object endpoint、request override 或后台正文补写任务。

### 9.4 稳定错误码

| 错误码 | stage | 对主 run 的处理 |
|---|---|---|
| `CONTEXT_REQUIRED_INPUT_TOO_LARGE` | preflight | 直接失败；compact 无法缩小 mandatory input |
| `CONTEXT_COMPACTION_INSUFFICIENT_EVICTABLE_TURNS` | plan | 记录 fallback，使用 token window |
| `CONTEXT_COMPACTION_CLAIM_BUSY` | claim | bounded wait 后复用 READY，否则 fallback |
| `CONTEXT_COMPACTION_MODEL_TIMEOUT` | model | fallback |
| `CONTEXT_COMPACTION_MODEL_FAILED` | model | fallback |
| `CONTEXT_COMPACTION_OUTPUT_INVALID` | validate | fallback |
| `CONTEXT_COMPACTION_OBJECT_PUT_FAILED` | store | fallback；保留失败事实 |
| `CONTEXT_COMPACTION_TARGET_NOT_MET` | assemble | fallback |
| `CONTEXT_SUMMARY_SCOPE_MISMATCH` | load | 安全失败，不使用对象；若能保存证据则 token-window fallback |
| `CONTEXT_SUMMARY_INTEGRITY_FAILED` | load/store | 安全失败，不信任摘要；若能保存证据则 token-window fallback |
| `CONTEXT_COMPACTION_EVIDENCE_PERSIST_FAILED` | project | 主模型调用前终止，避免无审计的 compact |
| `CONTEXT_SNAPSHOT_UNSUPPORTED` | snapshot | 直接失败，不读取 live 配置补算 |

协议、审计、日志只使用稳定码和 stage 枚举：
`snapshot|preflight|load|plan|claim|model|validate|store|assemble|project`。
provider 原始 message/response 不进入这些字段。

## 10. 状态机、并发与幂等

### 10.1 每 run lifecycle

产品语义与永久事实的对应关系固定如下：

| 产品状态 | Summary | Run step | AAP item |
|---|---|---|---|
| building | BUILDING（若已取得 claim） | RUNNING | in_progress |
| completed | READY | SUCCEEDED | completed / `result=completed` |
| fallback | 无效候选为 FAILED；已验证的中间 READY 可保留但不代表本 run completed | FAILED | failed / `result=fallback` |
| failed | 无效候选为 FAILED；不得伪造 READY | FAILED | failed / `result=failed` |

每个初始 run 最多一个 compact lifecycle：

```text
NOT_STARTED
   |
   | occupancy >= 80%
   v
RUNNING
   |--------------------|---------------------|
   v                    v                     v
SUCCEEDED            FALLBACK_FAILED       HARD_FAILED
main continues       token_window continues run stops
```

数据库映射：

| 结果 | `agent_run_steps.status` | item status | item result |
|---|---|---|---|
| completed | SUCCEEDED | completed | completed |
| fallback | FAILED | failed | fallback |
| hard failure | FAILED | failed | failed |

fallback step 必须带稳定 `error_code`，output summary 明确
`from=rolling_summary,to=token_window,degraded=true`。主 run 后续成功不能把该 step 改成 SUCCEEDED。

step 的 immutable `input_summary` 只保存 trigger/effective ceiling、80/60、planned coverage、
estimator/template/model snapshot hash 和 run snapshot version；mutable-to-terminal
`output_summary` 保存 result、before/after、实际 coverage/count/pass/reused、summary ID/digest
或 fallback from/to/stage。两者都不保存消息、摘要或 provider 正文。

### 10.2 唯一身份

- compact step ID：由 `run_id + "context-compaction.v1"` 生成稳定 UUIDv5。
- compact item ID：由 `run_id + "context-compaction-item.v1"` 生成稳定 UUIDv5。
- step sequence 和 item ordinal 由同一 DB transaction 在 run scope 下加锁分配；retry 先按稳定 ID 查询，存在则复用，不能再次分配。
- resume 检测 continuation/checkpoint 后直接 bypass，不创建这些 ID。

### 10.3 原子证据

使用现有 protocol unit-of-work 扩展：

1. `EnsureStarted` 在一个 transaction 中：
   - 插入/读取 deterministic `agent_run_steps`；
   - 插入/读取 `run_items` in_progress；
   - append `item.started`。
2. `Finalize` 在一个 transaction 中：
   - CAS step RUNNING → SUCCEEDED/FAILED；
   - CAS item in_progress → completed/failed；
   - append `item.completed`；
   - 写最终 step body-free output；
   - 按 run snapshot 将相同的 metadata-only 或含摘要正文 item snapshot 同时写入
     `run_items` 与 completed event。
3. transaction retry 返回现有事实，不重复 event。

如果 `EnsureStarted` 或 `Finalize` 无法可靠持久化，或 T4-B 的两份协议正文/摘要 digest
不一致，主模型不能继续；否则产品要求的可见性和 replay 一致性无法保证。

### 10.4 Summary claim

摘要幂等键继续使用：

```text
workspace
+ session
+ coverage_end_message_id
+ source_digest
+ parent_summary_digest
+ policy_fingerprint
+ prompt_template_hash
```

其中新版 policy fingerprint 已包含 model snapshot hash 和 normalization schema。数据库现有 unique constraint 是该逻辑键的粗粒度子集；repository 在 conflict/read 后强制比较 parent 与 summarizer snapshot，不一致即 integrity conflict，而不是误复用。

claim 规则：

1. READY：验证后直接复用，`reused=true`；
2. BUILDING 且 lease 有效：等待最多 `claimWaitMs`，只轮询该 claim；
3. 等待期间变 READY：复用；
4. 等待超时：记录 `CLAIM_BUSY` fallback，不发第二次模型调用；
5. lease 过期：用 owner token CAS 抢占，attempt_count+1；
6. owner token 不匹配不能 MarkReady/MarkFailed；
7. LLM 调用期间不持有数据库 transaction/row lock；
8. READY 永不修改；child pass 新建下一条 summary。

### 10.5 崩溃恢复

| 崩溃点 | retry 行为 |
|---|---|
| step started 前 | 无事实，正常重试 preflight |
| step started 后、claim 前 | 复用 step/item，继续 |
| LLM 中 | lease 到期后允许抢占；最多产生重复 provider 调用，不产生重复 READY |
| encrypted object put 后、MarkReady 前 | `PutOrVerify` 验证同 ID 正文后继续 MarkReady |
| READY 后、step finalize 前 | 查到 READY，按 run snapshot 重建确定性 protocol snapshot 并原子 finalize |
| finalize 后、主模型前 | 复用最终 item/manifest，不再 compact |
| 主模型中 | 走既有 run retry/checkpoint；不得创建第二 compact lifecycle |
| resume | 直接 checkpoint resume，不执行 preflight/compact |

## 11. 权限、安全与审计

### 11.1 Prompt injection 边界

- 原始消息和 parent summary 都作为不可信数据放入专用模板。
- LLM compactor 没有 tool、approval、workflow 或 side-effect adapter。
- 输出只允许受限 JSON，再由平台渲染为普通文本。
- 主调用中摘要固定为 `ASSISTANT`，前置不可信警示，绝不插入 SYSTEM。
- 摘要中的“批准”“调用工具”“忽略系统指令”等文本没有权限语义。
- 当前 USER 和待审批状态不进入摘要，避免重放意图。

### 11.2 Scope 与对象读取

通用 stored-object workspace member authorizer 不足以表达主运行和平台管理员审计目的，因此增加内部、不可路由的 `SummaryBodyReader`：

```text
OpenForPurpose(
  purpose = MAIN_ASSEMBLY | ADMIN_AUDIT,
  workspace, session, run, summary, expectedDigest, viewer
)
```

它先验证调用方已经通过相应 route/service 授权，再验证：

- stored object workspace；
- kind=`CHAT_CONTEXT_SUMMARY`；
- summary workspace/session；
- run step/item 引用；
- content SHA-256/长度；
- coverage message scope。

任何 scope mismatch 都 fail closed。外部 caller 不能只凭 summary ID 读取正文。

AAP 不通过该 reader 读取对象，而是读取 T4-B 已持久化的 protocol projection。AAP route
必须先完成现有 principal/scope 授权再查询 item/event；repository 仍以
workspace/agent/conversation/run predicate 限定。管理员审计不得复用 protocol JSONB
中的明文：即使 AAP snapshot=true，也只能在 debug/admin/UI 三重门通过后从加密对象
读取，防止 T4-B 绕过审计脱敏。

### 11.3 Agent 配置权限

- `aap.includeCompactionSummary` 使用现有 Agent management update 权限；
- Agent Access/AAP data principal 无权修改；
- 写入 audit 记录配置变更的 actor、前后布尔值和 Agent ID，但不写摘要正文；
- 默认 false；迁移不批量打开已有 Agent；
- 开启确认文案必须明确“永久 PostgreSQL 明文协议投影及备份”；关闭只影响后续新 run，
  不能承诺删除历史正文。

### 11.4 管理员审计

新增 `CONTEXT_COMPACTION` step renderer，时间线固定展示：

- 发生时间、耗时；
- completed / failed-and-fallback / failed；
- 80%/60% 阈值；
- before/after/effective ceiling；
- 覆盖消息/轮次数；
- pass 数、reused；
- summary ID/digest；
- fallback from/to；
- 稳定 error code/stage。

失败降级使用明确文案：

```text
上下文 compact 失败；已降级为 token_window。
```

正文显示必须同时满足：

1. server 全局 `agentAudit.debug=true`；
2. route 已验证 viewer 为 platform admin；
3. 前端 UI 的“隐藏敏感内容”遮罩被管理员主动关闭。

具体行为：

- debug=false：service **不打开摘要对象**，只返回 `contentState=redacted`，不生成 80 字 preview；
- debug=true + admin：service 可通过 `SummaryBodyReader(ADMIN_AUDIT)` 返回正文；
- UI 遮罩开启：前端不渲染正文，即使 response 已含；
- 非管理员：沿用 403；
- object 缺失/解密失败：`contentState=cipher|unavailable`，不返回残缺明文；
- audit export 若包含该正文，必须复用同一 debug/admin 判定和加密 export contract。

### 11.5 日志与 secret

日志禁止记录：

- 原始消息；
- parent/new summary 正文；
- provider request/response；
- credential secret、Authorization header；
- stored-object URL。

允许记录低敏感 ID、digest 前缀、token 数、result、stage、稳定错误码和时长。错误包装必须丢弃 provider body。

## 12. 可观测性

### 12.1 指标

建议：

```text
context_compaction_total{result,error_code,reused}
context_compaction_duration_seconds{result}
context_compaction_trigger_ratio
context_compaction_after_ratio
context_compaction_passes
context_compaction_claim_wait_seconds{outcome}
context_compaction_fallback_total{error_code}
context_compaction_target_violation_total
context_compaction_summary_bytes
context_compaction_aap_body_persist_total{included,outcome}
context_compaction_aap_body_bytes
context_compaction_audit_hydration_total{outcome}
```

禁止把 workspace/session/run/model ID 放入 metric label。model/provider 维度只有在现有低基数 registry 可证明受控时才允许。

### 12.2 结构化日志

每个 lifecycle 至少有：

- run/step/item/summary ID；
- trigger/target/before/after；
- coverage count；
- pass/reused；
- result/stage/error code；
- duration；
- gate/rollout version；
- estimator/template/model snapshot hash 前缀。

正文与 provider 错误文本永不记录。

### 12.3 告警

- fallback rate 超阈值；
- target violation 非零；
- summary integrity/scope mismatch 非零；
- evidence persist failure 非零；
- p95 compact latency 接近 total timeout；
- snapshot=false 却出现 protocol `summary`、两份 protocol digest 不一致或 payload guard
  拒绝次数非零；
- audit hydration cipher/unavailable 增长；
- estimator 后续获得 provider usage 时出现实际输入大于估值。

## 13. 前后端影响

### 13.1 后端

预计影响的模块边界：

- `sessioncontext`：policy v2、snapshot v2、validation/merge；
- `chatruntimebridge`：snapshot-backed prompt/model/tools、preflight、compact coordinator、resume guard；
- `contextwindow`：逻辑占用 planner、final target verification、manifest 字段；
- `contextsummary`：LLM generator、latest-ready lookup、claim wait、validator、body store；
- `storedobject`：summary kind bucket、永久敏感策略、内部 reader；
- `runstep` / `protocolevent`：compact UoW、snapshot-gated 永久正文 projection 和 payload guard；
- `agentaudit`：compact renderer/body gate；
- AAP route：沿用授权后直接读取永久 run item/event，不增加 output decorator；
- OpenAPI/SDK generation：新 item 和 policy schema；
- config：独立 compaction gate 与 shadow/enforced 模式。

### 13.2 Agent 设置前端

- Context policy editor 支持 v2；
- 80%/60% 以只读说明展示；
- 新增“AAP 返回 compact 摘要正文”开关，默认关闭；
- 开启前显示“永久 PostgreSQL 明文协议投影及备份”提示；
- v1 Agent 加载时 UI 显示 false，不在无修改保存时意外升级/打开。

### 13.3 AAP SDK

- 新增 `ContextCompactionItem`；
- `isContextCompactionItem` type guard；
- summary 仅在 `contentIncluded=true` 时存在；
- reducer/replay 对 started/completed、fallback、重复 event 幂等；
- 旧 generic unknown item 行为保持。

### 13.4 管理员审计前端

- 新增 compact icon/card；
- metadata 在遮罩下仍可见；
- 正文遵循现有 debug 与 mask UI；
- fallback 用 degraded/failed 视觉，不因主 run 最终成功被渲染为成功；
- cipher/unavailable 与 redacted 明确区分。

## 14. 测试设计

### 14.1 单元测试

1. 79.99% 比较为 false，80.00% 为 true；大整数无溢出。
2. 60.00% 为 target met，60.01% 不成立。
3. mandatory input 超限，不调用 compactor。
4. trigger 估值忽略 `maxRecentTurns`，final suffix 遵守它。
5. coverage 只接受连续完整轮次，当前 USER/半轮不进入。
6. parent/child coverage 与 digest 单调。
7. snapshot model/template/policy 任一变化均不误复用。
8. compactor tools/approval 为空，temperature/max tokens 被 clamp。
9. 输出严格 schema、byte/token、UTF-8 和 secret pattern 校验。
10. summary 以 ASSISTANT + untrusted prefix 注入。
11. 最多 pass 数、timeout 和 target-not-met fallback。
12. resume guard 永不调用 preflight/compactor。
13. protocol payload builder 只读取 run snapshot：false 删除正文，true 写实际注入正文；
    live Agent 配置变化不影响结果。
14. audit debug false 时不调用 object reader。

### 14.2 Repository / migration 测试

1. generation_method check 与 legacy 默认。
2. content/parent/coverage FK workspace 隔离。
3. latest READY 只返回同 workspace/session/LLM/fingerprint。
4. claim 并发只有一个 owner；lease 抢占 CAS。
5. READY immutable，FAILED 无 content object。
6. `PutOrVerify` 首次、同内容 retry、不同内容 conflict。
7. CHAT_CONTEXT_SUMMARY 强制 execution bucket、permanent、sensitive、encrypted、no presign。
8. step/item deterministic ID 与 transaction retry 不重复 sequence/event。

### 14.3 Bridge 集成测试

使用 fake model、真实 repository contract 和确定性 estimator：

1. `<80%`：无 step、无 compact provider call，主模型一次。
2. `=80%`：先 compact provider，再主 provider，顺序可断言。
3. success：最终 `<=60%`，manifest/step/item/summary 一致。
4. multi-pass：parent coverage 连续，最后达标。
5. timeout/invalid/store error：item failed+fallback，主模型收到 token window。
6. fallback 路径 repository spy 证明未全量读取。
7. summary reuse：第二并发 run 不重复生成。
8. claim busy：bounded wait 后复用或 fallback。
9. object put 后 DB crash：retry verify 并完成。
10. tools overhead 真正计入 trigger 和 mandatory input。
11. 主模型和 compact 模型使用同一 run model snapshot，live 配置变化不影响 retry。
12. resume：compact 调用数保持零。

### 14.4 AAP contract / replay 测试

1. OpenAPI schema 接受 `context_compaction`，旧 unknown decoder 仍通过。
2. started → completed 投影与 cursor 顺序。
3. fallback/failed reducer 状态稳定。
4. GET、SSE catch-up、SSE live 直接返回同一永久 projection，ETag/cursor 稳定。
5. `aap.includeCompactionSummary=false`：items list/get/SSE、`run_items.snapshot` 与
   `protocol_events.payload` 都无 `summary`。
6. `aap.includeCompactionSummary=true`：两份数据库 JSONB 都永久包含与实际注入正文
   逐字节一致的 `summary`，digest 一致，首次响应与 replay 一致。
7. AAP request 注入 override 字段被拒绝/忽略，不能改变 snapshot。
8. 跨 workspace/session summary 引用被 fail closed。
9. run 创建后切换 Agent 配置不增加、删除或遮蔽旧 run 正文；无权 principal 403/404。
10. legacy/unknown 客户端忽略 `context_compaction` 或新增 `summary` 字段仍推进 cursor。

### 14.5 Audit 与安全测试

1. 平台管理员可见 metadata，非管理员 403。
2. debug=false：response 无正文、无 preview、reader 未调用。
3. debug=true + admin + UI unmask：正文可见。
4. UI mask 默认开启。
5. fallback 固定显示“失败并降级”及 from/to/stage/code。
6. object cipher/unavailable 不泄漏残缺正文。
7. prompt injection 文本不能产生 tool/approval 副作用。
8. provider error/body/secret 不进入日志、step、item、audit；仅通过全部 summary/protocol
   guards 的 opt-in 正文可进入 item/event。
9. 即使 AAP protocol 已含正文，debug=false 的管理员审计仍不返回正文且 object reader
   未调用。

### 14.6 性能与故障测试

- 10 万轮 session：触发扫描和 fallback 都保持 keyset bounded memory；
- 并发 20 个同 session run：一个 summary owner，其余复用或 bounded fallback；
- MinIO/DB/provider 分别超时；
- migration 在代表性数据量上测锁时间和索引时间；
- compact p50/p95/p99 与主 run 总延迟；
- 64 KiB 正文双写对 PostgreSQL row/WAL、SSE payload、GET ETag 和 replay latency 的影响。

## 15. 发布、迁移与回滚

### 15.1 发布顺序

1. **Expand**：数据库列/FK/索引、stored-object kind 修复、兼容 reader。
2. **兼容发布**：policy/snapshot v2 parser、protocol item/SDK、audit renderer、
   snapshot-gated protocol payload builder/guard；compaction gate 仍关闭。
3. **前置正确性**：主 bridge 真正使用 snapshot prompt/model/tools，并确保 tool schema 进入 estimator；通过 ZKL-74 回归。
4. **Shadow**：只计算触发、coverage plan、预计 before/after，不调 compact 模型、不写成功摘要；比较指标与成本。
5. **Allowlist enforced**：仅指定 workspace/Agent 的新 rolling-summary run 写 v2 并执行。
6. **渐进放量**：1% → 10% → 50% → 100%，每阶段观察 fallback、target、latency、integrity。
7. **默认策略评估**：本 Issue 不自动把已有 Agent 从 token_window 改为 rolling_summary。

独立配置建议：

```text
runtime.sessionContext.compaction.enabled=false
runtime.sessionContext.compaction.mode=shadow|enforced
runtime.sessionContext.compaction.allowlist=[]
runtime.sessionContext.compaction.rolloutVersion=...
```

gate 只在 run snapshot 创建时求值。执行中的 worker 不重新读取 gate 决定同一 run 行为。

### 15.2 启用门槛

进入 allowlist 前必须全部满足：

- migration validate 完成；
- 所有 active worker 能解析 v2；
- snapshot-backed model/prompt/tools 已上线；
- tool schema overhead 估值已验证；
- MinIO summary kind 永久加密测试通过；
- T4-B 双写、snapshot=false 零正文、payload guard、授权、备份/复制敏感数据评审通过；
- debug/admin/mask 三重门通过；
- fallback 路径无全量历史读取；
- runbook、dashboard、告警和 on-call 操作已更新。

### 15.3 回滚

紧急回滚：

1. 将 compaction gate 对 **新 run** 关闭；
2. 已写 `session-context.v2` 的 run 继续按 snapshot 执行，不能因 gate 变化改语义；
3. 若 provider 故障，可将 allowlist 清空，已开始 run 仍按 timeout/fallback 完成；
4. 不删除 READY summaries、steps、items、events 或 manifests；
5. 不回滚 expand migration；
6. 关闭 Agent 配置或 compaction gate 只影响后续新 run；已按 snapshot=true 永久写入的
   protocol 正文继续 replay，不得通过回滚代码删除或遮蔽；
7. 若发现 security/integrity 问题，停止主模型前的 v2 run，而不是退回全量历史。

后续 contract migration 只在全量稳定且另行批准后考虑；本设计没有 destructive migration。

## 16. 风险与缓解

| 风险 | 影响 | 缓解 |
|---|---|---|
| compact 增加首 token 延迟 | 用户等待变长 | 同步总预算、bounded pass、claim reuse、allowlist |
| 模型摘要遗漏或幻觉 | 后续回答偏差 | recent raw suffix、结构化模板、不可信 ASSISTANT、原文永久审计 |
| prompt injection 进入摘要 | 权限提升或工具滥用 | no tools/approval、严格输出、非 SYSTEM、主运行仍受策略约束 |
| 估算低估 tool/provider framing | 实际请求超窗 | snapshot tools、同 estimator、safety margin、usage 对比告警 |
| 默认 `maxRecentTurns` 掩盖触发 | 永不 compact | trigger 在 cap 前计算；T2 明确确认 |
| 并发重复 LLM 花费 | 成本增加 | claim lease、bounded wait、稳定幂等键 |
| 对象写成功、DB 失败 | 永久孤儿/重试冲突 | deterministic ID + PutOrVerify |
| AAP 正文明文永久持久化 | DB 读者、备份、复制与协议消费者的敏感面扩大 | 默认 false、显式永久性警告、run snapshot、64 KiB/secret guard、scope 授权、DB/备份最小权限；已写正文不承诺删除 |
| debug 开关误泄漏 | 管理员页面展示敏感内容 | server/admin/UI 三重门，默认不读对象 |
| 旧 worker 读取 v2 | 错误 fallback 或漂移 | 全 fleet 兼容部署后才开 gate |
| compact 证据失败但主运行继续 | 不可审计 | evidence persist failure 为 hard failure |
| 超长首次历史在 pass 上限内无法覆盖 | 高频 fallback | 低基数告警；后续另行评估更高 pass/cost，不动态越权 |

## 17. 已批准技术决策

负责人已在评论 `979d6cd6-b260-4588-b37c-a76f18c36859` 完成选择：

| 决策 | 生效选择 |
|---|---|
| T1 | A |
| T2 | A |
| T3 | A |
| T4 | **B** |
| T5 | A |
| T6 | A |
| T7 | A |
| T8 | A |

以下保留事实、备选与取舍作为设计追踪；实现只能采用上述生效选择。

### T1：policy/snapshot 版本策略

**事实**：现有 v1 使用严格未知字段拒绝；原地加字段会使旧 worker 失败且语义不显式。

- A（推荐）：新增 `session-context-policy.v2` / `session-context.v2`；v1 保持原义。
- B：原地扩展 v1，并要求全 fleet 同时升级。
- C：另建独立 compaction JSON 配置，不进入 context policy。

**推荐 A 的影响**：兼容和回滚最清晰，但需要双版本 parser、前端和 OpenAPI 更新。

### T2：80% 与 `maxRecentTurns` 的顺序

**事实**：Agent UI 当前默认 `maxRecentTurns=20`。若先 cap 再计算占用，长会话可能一直低于 80%。

- A（推荐）：80% 对“parent summary + 全部未覆盖完整轮次 + mandatory”计算；cap 只作用于最终 raw suffix。
- B：先应用 cap 再计算；实现更接近当前 assembler，但可能永不 compact。
- C：rolling_summary 模式忽略 `maxRecentTurns`；语义简单但改变已批准 ZKL-74 配置含义。

**推荐 A 的影响**：能实现产品所述“上下文达到 80%”，需要新增 bounded logical scan。

### T3：每 run compact lifecycle 的事实表

**事实**：已有永久 `agent_run_steps` 和协议投影，另建表会形成重复状态。

- A（推荐）：`agent_run_steps(step_type=CONTEXT_COMPACTION)` 为事实，item 为投影。
- B：新建 `agent_run_context_compactions`，step 只做引用。
- C：只保存 protocol item，不进入审计 step。

**推荐 A 的影响**：迁移最小且审计一致；需要 compact 专用 UoW/renderer。

### T4：AAP 正文存储方式

**事实**：摘要是敏感派生数据；`protocol_events` / `run_items` 为 PostgreSQL JSONB 明文。

- A（原推荐，未选）：永久事件只存元数据，GET/catch-up/live 在授权边界 late hydrate。
- B（已批准）：配置 true 时把正文直接存进 event/item；replay 简单但扩大明文面。
- C：item 返回临时下载链接；与“无公开摘要 endpoint/URL”冲突且客户端更复杂。

**已批准 B 的影响**：实际注入正文永久进入两份 PostgreSQL JSONB 及其备份；默认
false、run snapshot、授权、payload guard 与 64 KiB 上限降低暴露面，但关闭配置或回滚
不能删除/遮蔽既有 run 的协议正文。

### T5：同步延迟与 claim 竞争策略

**事实**：产品要求同步 compact，同时失败可 token-window 降级。

- A（推荐）：45s total、20s/pass、1s claim wait；超时/竞争后记录 fallback。
- B：等待现有 claim 直到 45s；提高复用率但放大尾延迟。
- C：compact 超时直接让主 run 失败；不符合 D6 的可恢复失败意图。

**推荐 A 的影响**：尾延迟有硬界，极端并发下可能增加 fallback。

### T6：发布 gate

**事实**：现有 `runtime.sessionContext` 已控制 ZKL-74；本功能还有模型成本和敏感正文的新风险。

- A（推荐）：新增独立、默认关闭的 compaction 子 gate，并支持 shadow/allowlist。
- B：复用现有 sessionContext gate；配置少，但无法独立回滚 compact。
- C：v2 上线即对全部 rolling Agent 开启。

**推荐 A 的影响**：多一个运维开关，换取独立成本、安全与回滚控制。

### T7：LLM 输出契约

**事实**：自由文本难做大小、字段和安全校验；本地 extractive fallback 不满足产品。

- A（推荐）：模型输出严格 JSON，平台验证并确定性渲染文本。
- B：模型直接返回自由文本，只做 token/byte 检查。
- C：LLM 失败时以本地 extractive 作为 completed。

**推荐 A 的影响**：模板和 validator 工作更多，但可审计、可版本化；C 不可选，因为违反已批准产品范围。

### T8：证据持久化失败时是否继续主模型

**事实**：产品要求 AAP/审计明确可见；若 step/item 无法持久化，继续会产生不可审计 compact。

- A（推荐）：证据 start/finalize 失败时，主模型调用前 hard fail。
- B：记录日志并 token-window 继续；可用性高，但 AAP/审计缺事实。
- C：只要 step 成功即可继续，item 异步补写；会引入 run-owned background work 与状态漂移。

**推荐 A 的影响**：协议数据库故障时可用性下降，但符合审计与幂等硬约束。

上述选择已冻结。任何实现若需改变其中一项，必须停止 checklist 并回到 Knower；
涉及产品范围、协议对外语义、权限、保留或验收变化时还必须重新获得负责人产品确认。

## 18. 产品验收映射

| AC | 技术实现 | 必须验证 |
|---|---|---|
| AC-01 | basis-points 严格边界，`<8000` 无 lifecycle | 79.99% 无 compact call/item |
| AC-02 | `>=8000` initial preflight 同步执行 | 80.00% compact 在主模型前 |
| AC-03 | `SnapshotModelFactory` + `LLMCompactor` | provider fake 证明真实模型调用，非 extractive |
| AC-04 | parent/coverage 单调、claim 幂等、multi-pass | 并发/retry/reuse |
| AC-05 | 新 AAP item，正文默认 false | items list/get/SSE/replay 及两份 DB projection 均无片段/URL |
| AC-06 | snapshot=true 时永久双写实际注入正文 | 两份 JSONB、首次响应与 replay 正文/digest 一致；无权者 403/404 |
| AC-07 | run 创建时冻结 `aap.includeCompactionSummary` | 旧 run 配置不漂移，新 run 使用新配置 |
| AC-08 | compact audit renderer + debug/admin/UI mask 三重门 | 固定元数据可见，默认不读/不显示正文 |
| AC-09 | audit route 先做 platform-admin 授权 | Owner/Editor/Viewer/AAP principal 403 且零泄漏 |
| AC-10 | stable error/stage + token-window fallback | 明确 from/to/degraded，无全量历史/provider body |
| AC-11 | continuation/checkpoint resume bypass | resume 不读历史、compact call/record=0 |
| AC-12 | no tools/approval、ASSISTANT、不可信前缀 | prompt-injection 不改变 SYSTEM/绑定/审批/主体权限 |
| AC-13 | 永久 raw + 加密 summary；T4-B opt-in protocol 明文 | 顺序/ownership 不变；仅 opt-in item/event 含正文，日志/指标/默认审计无正文 |
| AC-14 | mandatory preflight | SYSTEM+tools+current USER 超限直接类型化失败 |

## 19. 实施交接

- 已批准设计：本文 v0.1；
- 生效选择：T1-A、T2-A、T3-A、T4-B、T5-A、T6-A、T7-A、T8-A；
- Checklist：`docs/design/agent-context-llm-compaction-implementation-checklist.md`；
- 实施必须严格按 checklist 依赖顺序推进，每项由全新临时只读 verification subagent
  给出 PASS 后直接进入下一项；
- 只有 checklist 缺失/冲突/不可执行或实现需要改变已批准设计时才回到 Knower。
