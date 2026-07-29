# ZKL-74 单次会话上下文窗口管理 Implementation Checklist

> Checklist 版本：v1.0
> 对应已批准技术方案：`docs/design/session-context-window-management-technical-design.md` v0.1
> 批准记录：负责人 chenow，评论 `b1e6d8c8-7876-4eec-a840-cee3f3377972`，2026-07-29
> 生效决策：D1-A、D2-A、D3-A、D4-A、D5-A、D6-A、D7-A
> 事实基线：`b4b131899fd0c4dbe916ed2fc9c53662536f2310`
> 总项数：14
> 当前状态：READY_FOR_IMPLEMENTATION

## 1. 使用规则

1. Forge 必须严格按 IC-01 → IC-14 的顺序执行；当前项经独立验证 PASS 后直接开始下一项，不需要逐项等待 Knower 回复。
2. 每项实现完成并完成开发自测后，Forge 必须新建一个只负责该项的临时、只读 verification subagent。该 subagent：
   - 不是持久 Agent，不创建或承载 Issue / 子 Issue；
   - 不得复用前一项或前一次 FAIL 的 verifier；
   - 只读取方案、checklist、相关 diff/代码/测试证据并可执行非变更型验证命令；不得编辑源码、提交、推送、评论或改变 Issue 状态；
   - 按本项“独立验证标准”给出明确 `PASS` 或 `FAIL` 及证据摘要。
3. 只有 verifier 明确 `PASS`，本项状态才可改为 `VERIFIED` 并进入下一项。若 `FAIL`，记录摘要、修复并新建另一个 verification subagent 复验，禁止让原 verifier 复用上下文后改判。
4. 每项都要在本文的进度记录中填写：状态、实现 commit/PR 或 diff 证据、开发自测命令与结果、verification subagent 标识、PASS/FAIL 摘要。不得用子 Issue、Stage 或仅口头评论代替进度记录。
5. Forge 可在不改变已批准方案的前提下处理普通代码细节。出现以下任一情况必须暂停并回到 Knower：checklist 缺失/冲突/不可执行，或实现需要改变范围、架构、API、数据、权限、安全、迁移、兼容、发布或验收决策。
6. 不得提前启用生产默认 gate，不得把 `rolling_summary` 提前设为默认，不得删除/改写原始 `chat_messages`，不得把 checkpoint 用作会话上下文存储。

## 2. 已批准决策摘要

| 决策 | 已批准内容 |
| --- | --- |
| D1-A | 先交付 `token_window`；`rolling_summary` 在硬预算、存储、安全与质量门槛通过后显式 opt-in |
| D2-A | 模型使用不会透传上游、严格校验的 `runtimeCapabilities`，并写入不可变运行快照 |
| D3-A | workspace/agent 使用专用版本化 `context_policy`；系统硬约束 ＞ 内部 run override（v1 不公开）＞ agent ＞ workspace ＞ 平台默认 |
| D4-A | 摘要使用独立元数据表和加密 `CHAT_CONTEXT_SUMMARY` 永久对象，不写回 `chat_messages` |
| D5-A | 仅 `session-context.v1` 激活；已识别旧占位走 legacy，显式未知版本安全失败；新 run 由 fail-closed gate 控制 |
| D6-A | 保持 Console/AAP accepted 契约；预检失败后以稳定安全错误码转 `FAILED`，不截断当前输入 |
| D7-A | `run.v2` 固定 Agent prompt revision 与模型推理字段；仅实时保留 Agent/model 禁用 kill switch 和同一 secret 引用解析 |

## 3. 全局不可违背约束

- 只处理同一 ChatSession / AAP Conversation 内的上下文；不做跨会话 Memory、RAG、向量检索或用户画像。
- PostgreSQL `chat_messages` 及其永久对象是原始事实，内容、顺序、ownership 和永久保留语义不变。
- Console `/api/v1` 与 AAP `/api/agent-access/v1` 共用运行时策略；AAP `createRun` 请求、JSON 202 / SSE 200 成功 shape 和冻结事件类型不变。
- 初始运行才执行 assembly；HITL/工具确认恢复只调用 checkpoint `Resume`，不得重新读取历史、生成摘要或重复工具调用。
- mandatory context 始终包含精确 SYSTEM、工具契约和当前 USER；当前 USER 不可静默截断，历史只按连续完整轮次选取。
- manifest、日志、metrics、trace、协议错误不得包含 prompt、消息、摘要、provider body 或对象 URL 明文。
- 旧 `run.v1` / `{}` snapshot 保持 legacy；显式未知版本返回稳定错误，不静默降级为全量历史。
- 所有 rollout gate 默认关闭；回滚优先关闭 gate，保留 expand-only schema、manifest 和永久摘要对象。

## 4. 总体进度

| ID | 项目 | 状态 | 实现证据 | Verification |
| --- | --- | --- | --- | --- |
| IC-01 | Expand-only schema 与领域契约 | VERIFIED | feat/zkl-74-session-context-window (000002 + domain models) | subagent `019fab98-4085-75f1-b484-8b5bfa286f84` PASS |
| IC-02 | 严格配置、权限与管理 API | VERIFIED | model-runtime.v1 + session-context-policy.v1 config APIs | subagent `019faba4-29e2-7853-8e26-86b08ef348a6` PASS |
| IC-03 | `run.v2` 快照绑定与策略解析 | VERIFIED | run.v2 + session-context.v1 resolver + fail-closed gate | subagent `019fabab-cc43-7b91-b138-0da5b2203891` PASS |
| IC-04 | Tokenizer registry 与 token estimator | VERIFIED | contextwindow registry + estimator + tiktoken-go | subagent `019fabaf-e6ff-7560-898a-69406774b21b` PASS |
| IC-05 | Principal-safe 历史分页与轮次规范化 | VERIFIED | reverse page + NormalizeTurns | subagent `019fabb3-15b2-7c13-ae60-bfd9d7900736` PASS |
| IC-06 | 纯 `token_window` assembler | VERIFIED | AssembleTokenWindow pure | subagent `019fabb3-15b3-7d52-91e6-64e987698936` PASS |
| IC-07 | Assembly manifest 与稳定错误契约 | PENDING | — | — |
| IC-08 | Bridge / model adapter 初始运行接入 | PENDING | — | — |
| IC-09 | Usage、overflow、可观测与用户错误投影 | PENDING | — | — |
| IC-10 | Shadow 与 token-window 灰度就绪 | PENDING | — | — |
| IC-11 | 摘要存储、对象与 claim 状态机 | PENDING | — | — |
| IC-12 | 受限滚动摘要生成器 | PENDING | — | — |
| IC-13 | `rolling_summary` assembler/bridge 接入 | PENDING | — | — |
| IC-14 | 管理 UI、全链路验收与发布交付 | PENDING | — | — |

## 5. 顺序实施项

### IC-01 — Expand-only schema 与领域契约

**目的**

先建立旧二进制可容忍、默认不生效的数据基础，供后续配置、快照、manifest 使用。

**精确范围**

- 新增 `backend/internal/database/migrations/000002_session_context_contracts.up.sql` 与对应 down 文件。
- 扩展 `model_configs.runtime_capabilities`、`workspaces.context_policy`、`agents.context_policy`、`agent_runs.agent_snapshot`，默认均为 JSON object `{}`。
- 新建 `agent_run_context_assemblies`，以 `(workspace_id, run_id)` 唯一，保存预算、segment ID/hash、snapshot hash、summary 引用和 assembly digest，不保存正文。
- 更新 `backend/internal/modelconfig/models.go`、`backend/internal/agent/models.go`、`backend/internal/execution/run_models.go` 的领域形态；仅定义结构，不改变运行行为。
- 在现有 migration/repository 测试中覆盖 schema、复合 workspace 外键、JSON object 和不可变约束。

**不可违背约束**

- 不修改 `migrations_archive` 或已发布 `000001_init.*`；UUID 仍由应用生成；使用 `TIMESTAMPTZ`、直接 `workspace_id` 和明确 FK 行为。
- migration 必须 expand-only；down 文件用于迁移契约完整性，但生产回滚不得依赖 destructive down。
- 不创建摘要表/对象 kind（留给 IC-11），不接入 bridge，不启用 gate。

**完成定义**

- clean database 可从 `000001` 升至 `000002`；现有数据无需回填且仍可读取。
- 新旧 repository 路径对 `{}` 默认值、workspace 隔离和 manifest 唯一性有测试。
- 此项合入后，所有运行行为与基线完全一致。

**开发自测**

- 在 `backend/` 运行：`go test ./internal/database/... ./internal/modelconfig/... ./internal/agent/... ./internal/execution/...`。
- 运行迁移 up/down/up 测试并记录 schema 断言结果；运行 `go test ./...` 的 migration/architecture 相关失败筛查。

**独立验证标准**

完成后新建仅用于 IC-01 的临时只读 verification subagent（不是持久 Agent/Issue，不得复用任何既有 verifier）。它必须核对 migration 文件、默认值、FK/唯一/不可变约束和旧数据兼容，并独立运行目标测试；只有确认“无运行时启用、无正文列、无 archive 修改”才可 `PASS`。

**回滚 / 风险**

- 风险：约束过弱造成跨 workspace 引用，或默认值使旧 reader 失败。
- 回滚：代码回滚时保留新增列/表；gate 尚未存在生效路径，不删数据。

**进度记录**

- 状态：VERIFIED
- 实现证据：`000002_session_context_contracts.{up,down}.sql`；domain shapes in `modelconfig`/`agent`/`execution`/`workspace` models；`session_context_contracts_migration_test.go`；latest migration pin 1→2 in tests
- 开发自测：`cd backend && go test ./internal/database/... ./internal/modelconfig/... ./internal/agent/... ./internal/execution/... ./internal/workspace/... -count=1` → all ok
- Verification subagent / 结果：`019fab98-4085-75f1-b484-8b5bfa286f84`（execute, brand-new）VERDICT PASS；前次 `019fab93-b858-7930-884f-b4cd916a9949` 因无 shell 未能跑测 FAIL，已不复用

### IC-02 — 严格配置、权限与管理 API

**目的**

实现 D2-A/D3-A 的可校验配置源，但仍不改变运行时送模行为。

**精确范围**

- 在 `backend/internal/modelconfig` 增加 `model-runtime.v1` 严格解析、规范化和 repository CAS 支持：`contextWindowTokens`、`defaultOutputReserveTokens`、`outputTokenLimitMode`、`tokenizerProfile`、`tokenizerVersion`。
- 在 workspace 与 `backend/internal/agent` 增加 `session-context-policy.v1` patch 配置与 CAS/lock-version 更新。
- 扩展 `backend/internal/transport/http/configuration.go`、`workspace_member.go`、`agent_capability.go` 的 Console 管理 DTO/handler；未知字段/版本和非法预算返回稳定 4xx validation error。
- 覆盖 model 管理、workspace 管理、agent 编辑权限和审计事件；AAP data-plane 不增加写入口。

**不可违背约束**

- `runtimeCapabilities` 绝不能合并进或透传为 provider `Options`。
- Agent policy 不能放进弱类型 workspace settings；普通聊天用户和 AAP subject 不能写策略或 per-run override。
- 配置只允许收紧模型硬能力；保存时必须拒绝未知 tokenizer/output limit mode。

**完成定义**

- model/workspace/agent 管理 API 能读写规范化配置，权限、CAS 冲突和严格校验测试齐全。
- 老客户端省略新字段仍正常；AAP OpenAPI 请求 shape 无变化。
- 尚未由任何 run 消费新配置。

**开发自测**

- `backend/`：`go test ./internal/modelconfig/... ./internal/agent/... ./internal/workspace/... ./internal/transport/http/...`。
- 运行 HTTP 权限、validation、CAS 与“字段不进入 model options”专项测试。

**独立验证标准**

完成后新建仅用于 IC-02 的临时只读 verification subagent（不是持久 Agent/Issue，不得复用任何既有 verifier）。它必须以越权用户、AAP principal、未知字段/版本、预算放大和 options 透传为反例验证 fail-closed；全部配置/API 兼容断言通过才可 `PASS`。

**回滚 / 风险**

- 风险：替换式 DTO 丢失旧字段、权限扩大、内部字段发送给 provider。
- 回滚：保留 schema，回滚 API reader/writer；因运行时未消费，不改变聊天行为。

**进度记录**

- 状态：VERIFIED
- 实现证据：`modelconfig/runtime_capabilities.go` + repo CAS；`sessioncontext/policy.go`；HTTP DTO/handlers for model/workspace/agent；options leak rejection
- 开发自测：`go test ./internal/modelconfig/... ./internal/sessioncontext/... ./internal/agent/... ./internal/workspace/... ./internal/transport/http/...` → all ok
- Verification subagent / 结果：`019faba4-29e2-7853-8e26-86b08ef348a6` VERDICT PASS

### IC-03 — `run.v2` 快照绑定与策略解析

**目的**

在创建 run 时固定已批准的 Agent/model/capability/context 语义，消除排队和重试时配置漂移。

**精确范围**

- 更新 `backend/internal/application/adapters.go` 的 `SnapshotAgentRun`、`backend/internal/execution/run_models.go` 与 `run_repository.go`。
- `run.v2` 的 `agent_snapshot` 固定 agent ID、prompt revision ID/hash、model config ID/lock version；model snapshot 固定 provider/API base/model/options/runtime capability/credential secret ID 引用，不含 secret 值。
- 增加 policy resolver：系统硬约束 ＞ 内部 run override（仅内部测试接口）＞ agent ＞ workspace ＞平台默认，随后用模型能力 clamp，写自包含 `session-context.v1`。
- 在 `backend/internal/config/runtime.go`、`config.go`、`backend/config.yaml` 增加独立 fail-closed `runtime.sessionContext` rollout；只在创建 run 时求值，默认关闭。
- 提供 versioned snapshot parser；`run.v1`/`{}`/已识别旧占位保持 legacy，显式未知版本返回 `CONTEXT_SNAPSHOT_UNSUPPORTED`。

**不可违背约束**

- Bridge 以后只从 v2 快照读取推理字段；实时 repository 仅用于 Agent/model 禁用 kill switch 和按快照 secret ID 解析当前密钥值。
- gate 变化不能改变已创建 run；不回填或伪造旧 snapshot。
- 不开放 Console/AAP per-run override。

**完成定义**

- 新 run 在 allowlist + 完整配置下写确定性 v2/v1-context 快照；gate off 写 legacy。
- 创建后修改 current prompt/model 不改变快照；禁用状态仍可被后续 runtime 识别。
- Snapshot canonicalization、旧值、未知版本、层级优先级和并发 CAS 测试通过。

**开发自测**

- `backend/`：`go test ./internal/application/... ./internal/execution/... ./internal/config/... ./internal/chat/... ./internal/aap/...`。
- 运行排队期间配置变更、gate 变更、secret 引用轮换与旧 run 重读专项测试。

**独立验证标准**

完成后新建仅用于 IC-03 的临时只读 verification subagent（不是持久 Agent/Issue，不得复用任何既有 verifier）。它必须独立构造 v1、v2、`{}`、旧占位和未知版本样本，并验证 gate 只在创建时求值、snapshot 不含 secret 明文、D2/D3/D5/D7 优先级完全一致；满足才可 `PASS`。

**回滚 / 风险**

- 风险：快照缺字段导致无法复现，或 kill switch 被错误快照化。
- 回滚：关闭 `runtime.sessionContext` 只影响新 run；已创建 v2 run 仍按快照处理，schema 保留。

**进度记录**

- 状态：VERIFIED
- 实现证据：`sessioncontext/snapshot.go` Resolve/Parse；`config.SessionContextRollout` fail-closed；`SnapshotAgentRun` gate-on v2；`agent_snapshot` persist/scan；`TestAgentRunSnapshotsV2WhenGateAndCapabilitiesReady`
- 开发自测：`go test ./internal/sessioncontext/... ./internal/execution/... ./internal/application/... ./internal/config/... ./internal/chat/... ./internal/aap/...` → all ok
- Verification subagent / 结果：`019fabab-cc43-7b91-b138-0da5b2203891` VERDICT PASS

### IC-04 — Tokenizer registry 与 token estimator

**目的**

提供版本固定、可审计且保守的 token 估算基础，不依赖模型名猜测或在线试算。

**精确范围**

- 新增 `backend/internal/contextwindow` 的 tokenizer registry、message/tool framing estimator 和 estimate result/version 类型。
- 如引入依赖，更新 `backend/go.mod` / `go.sum` 并记录许可证与供应链评估。
- 实现受控精确 tokenizer profile；仅对经过 provider 兼容验证的 profile 允许 `byte_upper_bound`，否则拒绝启用。
- 估算 SYSTEM、USER/ASSISTANT framing、工具 schema、tool-choice envelope、固定 provider 开销和合成摘要。

**不可违背约束**

- 不根据任意模型名静默推断；profile 未知或不可验证时 fail closed。
- estimator/version 必须进入后续 manifest；不得把正文写入日志或错误。
- estimator 只计算，不选择历史、不调用 provider。

**完成定义**

- CJK、emoji、长 ASCII、空内容、消息边界和大工具 schema 有 golden tests。
- 相同输入/profile/version 产生确定结果；byte profile 不低估其声明兼容范围。
- Fuzz/property 测试证明无负数、溢出或非确定性。

**开发自测**

- `backend/`：`go test ./internal/contextwindow/...`，并运行该包 fuzz/property 测试的固定回归语料。
- 执行依赖许可/漏洞检查的仓库既有命令；记录 tokenizer 版本和 golden 更新原因。

**独立验证标准**

完成后新建仅用于 IC-04 的临时只读 verification subagent（不是持久 Agent/Issue，不得复用任何既有 verifier）。它必须复算 golden、审阅 framing/tool 开销、验证未知 profile 被拒绝并检查新增依赖；只有不存在模型名猜测和可见低估样例才可 `PASS`。

**回滚 / 风险**

- 风险：provider tokenizer 漂移或 framing 漏算造成上游 overflow。
- 回滚：profile 可从 rollout allowlist 冻结；此项尚未接入运行路径。

**进度记录**

- 状态：VERIFIED
- 实现证据：`contextwindow/{registry,estimator}.go`；profiles o200k_base/cl100k_base/byte_upper_bound；`github.com/pkoukk/tiktoken-go v0.1.7`
- 开发自测：`go test ./internal/contextwindow/...` → ok
- Verification subagent / 结果：`019fabaf-e6ff-7560-898a-69406774b21b` VERDICT PASS

### IC-05 — Principal-safe 历史分页与轮次规范化

**目的**

替代一次性 `ListMessages` 全量正文加载，为预算组装提供有界、授权且顺序稳定的历史流。

**精确范围**

- 在 `backend/internal/chat/repository.go` / `models.go` 增加带 workspace/session/principal ownership predicate 的 `(created_at,id)` 反向游标分页和边界/count 查询。
- 永久对象正文继续通过 `backend/internal/chat/permanent_content.go` 加载并校验 SHA/长度；达到预算候选边界后不再解密更老正文。
- 在 `backend/internal/contextwindow` 增加 turn normalizer：每个历史 USER 与下一个 USER 前的 ASSISTANT 为不可拆分单元。
- 通过 `job.UserMessageID` 唯一定位当前 USER；过滤历史 SYSTEM/TOOL，并排除与 FAILED run 关联的自动失败 assistant 文本，保留对应 user-only 单元。

**不可违背约束**

- 不允许只凭 message UUID 绕过 workspace/session/principal；AAP external subject/service principal 仍用现有 ownership 规则。
- 分页大小是资源参数，不得改变语义选择；排序必须稳定。
- 不修改、删除或重新保存原始消息/对象。

**完成定义**

- inline/object 混合、相同时间戳、失败 run、user-only、跨主体和 10k+ 消息场景有测试。
- 读取只覆盖 assembler 请求的页/增量，不再全量解密正文。
- 当前 USER 缺失、重复或 session 不一致返回数据完整性错误。

**开发自测**

- `backend/`：`go test ./internal/chat/... ./internal/contextwindow/...`。
- 运行 principal ownership、对象 hash、游标稳定性、查询计划与大历史基准测试。

**独立验证标准**

完成后新建仅用于 IC-05 的临时只读 verification subagent（不是持久 Agent/Issue，不得复用任何既有 verifier）。它必须尝试跨 workspace/session/AAP subject 读取，检查 SQL predicate/索引与对象校验，并确认轮次不拆分、排序稳定、没有全量正文回退；满足才可 `PASS`。

**回滚 / 风险**

- 风险：游标遗漏/重复消息，或分页接口造成新的 IDOR。
- 回滚：旧 `ListMessages` 保留给 legacy gate-off 路径；新 reader 尚未进入生产路径。

**进度记录**

- 状态：VERIFIED
- 实现证据：`ListMessagesForPrincipalReversePage`/`CountMessagesForPrincipal`；`contextwindow/turns.go` NormalizeTurns
- 开发自测：`go test ./internal/chat/... ./internal/contextwindow/...` → ok
- Verification subagent / 结果：`019fabb3-15b2-7c13-ae60-bfd9d7900736` VERDICT PASS

### IC-06 — 纯 `token_window` assembler

**目的**

实现不依赖 HTTP/bridge 的确定性硬预算选择算法，作为所有模式不可绕过的底座。

**精确范围**

- 在 `backend/internal/contextwindow` 实现 assembler 输入/输出、budget math、turn selection 和 assembly plan。
- 计算 `modelContextWindow - outputReserve - safetyMargin`，再扣 SYSTEM、工具 schema 与 framing；返回 messages 和 `effectiveOutputLimitTokens`。
- mandatory 为精确 SYSTEM + tools + 当前 USER；超限返回 `CONTEXT_REQUIRED_INPUT_TOO_LARGE`。
- 历史从新到旧按完整轮次装入，遇到第一个不适配轮次停止，输出按时间正序；不得部分截断。
- 本项只实现 `token_window`；summary 以接口/plan 占位，不生成、不注入。

**不可违背约束**

- SYSTEM 与当前 USER 各恰好一次；当前 USER 不可排除或裁剪。
- 历史必须是连续最近后缀，不能越过超大中间轮次捞更老内容。
- 算法是纯逻辑；不读数据库、不写 manifest、不调用模型。

**完成定义**

- 空历史、单轮、临界 token、mandatory 超限、超大历史轮、大工具 schema、maxRecentTurns 和 output clamp 全覆盖。
- Property/fuzz 不变量：确定性、顺序、完整轮次、预算不越界、无正文变异。
- plan 清楚列出 included/omitted 边界、估值和 estimator/version。

**开发自测**

- `backend/`：`go test ./internal/contextwindow/...`，运行 assembler fuzz/property 固定时间并保存 seed/失败语料。
- 对设计文档第 8 节伪代码建立 table-driven 对照测试。

**独立验证标准**

完成后新建仅用于 IC-06 的临时只读 verification subagent（不是持久 Agent/Issue，不得复用任何既有 verifier）。它必须以 adversarial 边界重新验证 mandatory、连续后缀、完整轮次和硬预算公式，并检查没有 DB/provider 依赖；所有不变量成立才可 `PASS`。

**回滚 / 风险**

- 风险：双重扣减/漏扣开销，或 current USER 被历史逻辑误处理。
- 回滚：该纯包尚未接入 bridge，可直接回滚代码而无数据影响。

**进度记录**

- 状态：VERIFIED
- 实现证据：`contextwindow/assembler.go` AssembleTokenWindow；budget/mandatory/suffix tests
- 开发自测：`go test ./internal/contextwindow/...` → ok
- Verification subagent / 结果：`019fabb3-15b3-7d52-91e6-64e987698936` VERDICT PASS

### IC-07 — Assembly manifest 与稳定错误契约

**目的**

让实际送模投影可审计、可幂等，并在接入 bridge 前固定安全错误语义。

**精确范围**

- 在 `backend/internal/execution` 实现 `agent_run_context_assemblies` model/repository 与 canonical digest。
- 每个初始 run 最多一条不可变 manifest：snapshot/system/tool/message/summary hash、ID/role、预算、estimator、included/omitted 边界；禁止正文。
- 同 run 同 digest 可复用，不同 digest 返回一致性错误并告警；实际 provider usage 使用独立 append-only observation，不修改 manifest。
- 在 `backend/internal/execution/errors.go` 及 runtime error mapper 定义：`CONTEXT_SNAPSHOT_UNSUPPORTED`、`CONTEXT_MODEL_LIMIT_UNKNOWN`、`CONTEXT_REQUIRED_INPUT_TOO_LARGE`、`CONTEXT_ASSEMBLY_FAILED`、`CONTEXT_WINDOW_EXCEEDED_UPSTREAM`。
- 定义面向用户的固定安全消息和 retryable 语义，不包含 cause/provider body。

**不可违背约束**

- manifest 只描述实际 applied assembly；shadow plan 不得写成权威 manifest。
- 任何 prompt/摘要/对象 URL/供应商响应都不得进入 manifest、普通日志或失败 assistant 文本。
- manifest 持久化失败时不能继续调用模型。

**完成定义**

- Repository 唯一性、不可变、跨 workspace 拒绝、digest 确定性和正文泄漏测试通过。
- 五个错误码在 execution/bridge/protocol 可稳定映射，未知内部错误仍收敛为安全通用错误。
- 审计能用 IDs/hashes/snapshots 重建“模型看到了哪些片段”。

**开发自测**

- `backend/`：`go test ./internal/execution/... ./internal/chatruntimebridge/... ./internal/protocolevent/...`。
- 对 manifest/log/error payload 运行敏感字符串 canary 测试。

**独立验证标准**

完成后新建仅用于 IC-07 的临时只读 verification subagent（不是持久 Agent/Issue，不得复用任何既有 verifier）。它必须注入 prompt/provider-secret canary、制造重复 run 不同 digest、跨 workspace 查询和 repository 失败；只有无泄漏、无覆盖、错误稳定才可 `PASS`。

**回滚 / 风险**

- 风险：manifest 被误当正文仓库，或错误链泄露 provider body。
- 回滚：保留表和既有记录；运行尚未接入时可回滚 repository/error 使用方。

**进度记录**

- 状态：PENDING
- 实现证据：—
- 开发自测：—
- Verification subagent / 结果：—

### IC-08 — Bridge / model adapter 初始运行接入

**目的**

把 v2 snapshot、history、assembler、manifest 和模型输出上限接到共享初始运行路径，同时保证 legacy 与 Resume 不变。

**精确范围**

- 更新 `backend/internal/chatruntimebridge/bridge.go`：v2 初始 run 从 snapshot 构建 Agent/model/tools，调用 assembler，成功写 manifest 后才打开 text sink 并调用 `Engine.Run`。
- `targets != nil` / Resume 路径明确跳过 history、assembler、manifest、summary，继续 `Engine.Resume(checkpoint)`。
- 更新 `backend/internal/modelapi/platform_chat_model.go` / `eino_openai_chat_model.go`，按 snapshot 的 `outputTokenLimitMode` 强制 `effectiveOutputLimitTokens`；旧 options 更大时 clamp，更小时保留。
- 实时读取只检查快照所指 Agent/model 是否禁用，并按快照 secret ID 解析密钥；不跟随 current prompt/model。
- gate off、旧 `run.v1` 和已识别 legacy snapshot 保持现有 `buildMessages` 行为。
- assembly 失败沿现有 `failRun` 顺序：持久化 FAILED + 安全 assistant 消息，再发布 `run.failed`；不得产生空 streaming item。

**不可违背约束**

- Console/AAP 必须共用这一接入点，不在 HTTP 入口复制裁剪。
- mandatory/manifest 失败后不得调用 provider；text sink 不得提前打开。
- Resume 不受 gate、会话新增消息或配置变化影响，不重复工具。

**完成定义**

- 同输入的 Console/AAP v2 run 产生同 assembly digest；gate off golden 行为不变。
- 排队后修改 prompt/model 不改变 v2 推理字段，禁用 kill switch 仍失败。
- `Engine.Run` 输入和 output cap 与 manifest 一致；Resume 相关 spy 证明 assembler/summarizer 调用次数为 0。

**开发自测**

- `backend/`：`go test ./internal/chatruntimebridge/... ./internal/einoruntime/... ./internal/modelapi/... ./internal/chat/... ./internal/aap/... ./internal/transport/http/...`。
- 运行现有 text/tool/approval-resume golden、fail_run、stream delta 与重复投递测试。

**独立验证标准**

完成后新建仅用于 IC-08 的临时只读 verification subagent（不是持久 Agent/Issue，不得复用任何既有 verifier）。它必须检查 bridge 分支和调用顺序，并用 spies 验证初始/Resume、sink、provider、manifest、kill switch、legacy 行为；任何 Resume 重组装或 provider-before-manifest 都必须 `FAIL`。

**回滚 / 风险**

- 风险：共享 bridge 改动影响所有入口、stream 时序或 HITL 工具幂等。
- 回滚：关闭 `runtime.sessionContext` 仅使新 run 回 legacy；已创建 v2 run 按不可变快照完成，保留 manifest。

**进度记录**

- 状态：PENDING
- 实现证据：—
- 开发自测：—
- Verification subagent / 结果：—

### IC-09 — Usage、overflow、可观测与用户错误投影

**目的**

校准估算器、识别边界失效，并让 Console/AAP 获得稳定而不泄密的可操作反馈。

**精确范围**

- 在 `backend/internal/modelapi` 解析非流式/流式 usage 和 provider context-overflow，转换为稳定 observation/error，不透传 body。
- 接入 `backend/internal/chatruntime/auxiliary_protocol.go` 的既有 `usage.updated`，以及 execution/protocol append-only usage 记录；不新增 AAP 冻结事件类型。
- 在 bridge/context 模块增加设计文档第 14 节的低基数 metrics、structured logs 和 spans；ID 只进日志字段，不进 metric labels。
- 更新 AAP Run/error 与 `run.failed` 投影，保持 `docs/openapi/agent-access-v1.yaml` 的请求/成功 shape；错误 code 是 additive string 值。
- 更新 `frontend/src/stores/chat.ts`、`ChatExecutionPageBody.vue` / 行为测试，对 context 错误展示缩短输入、减少工具/附件、新建会话或联系管理员动作；不显示原始 cause。

**不可违背约束**

- usage 不能回写不可变 manifest；summary usage 与主模型 usage 分开。
- metrics/log/trace/error 中不出现 prompt、工具参数、摘要、provider body 或高基数内容标签。
- `CONTEXT_WINDOW_EXCEEDED_UPSTREAM` 必须告警，不能作为普通 retryable 错误吞掉。

**完成定义**

- usage/overflow 在 streaming 与 non-streaming 均有 fixture/golden；未知 provider 格式安全降级。
- Console 与 AAP 对五个 context 错误码行为兼容，老客户端仍按 FAILED 终态工作。
- estimator actual/estimate ratio 和 TTFT 可按受控 mode/profile 观测。

**开发自测**

- `backend/`：`go test ./internal/modelapi/... ./internal/chatruntime/... ./internal/chatruntimebridge/... ./internal/protocolevent/... ./internal/protocolschema/... ./internal/transport/http/...`。
- `frontend/`：`npm test -- --run`、`npm run type-check`、`npm run lint`。
- 运行敏感 payload canary 与 protocol compatibility diff。

**独立验证标准**

完成后新建仅用于 IC-09 的临时只读 verification subagent（不是持久 Agent/Issue，不得复用任何既有 verifier）。它必须用 provider-body/prompt canary 检查所有输出面，验证 usage 不改 manifest、AAP 冻结事件集合不变、Console 动作正确；无泄漏且兼容测试通过才可 `PASS`。

**回滚 / 风险**

- 风险：流式 usage 丢失/重复，或高基数指标造成成本问题。
- 回滚：关闭 context gate；usage/观测的 additive 解析可单独关闭，不删除历史 observation。

**进度记录**

- 状态：PENDING
- 实现证据：—
- 开发自测：—
- Verification subagent / 结果：—

### IC-10 — Shadow 与 token-window 灰度就绪

**目的**

在不改变实际送模输入的 shadow 阶段验证估算，再把 `token_window` 安全开放给内部 allowlist。

**精确范围**

- 完成 `runtime.sessionContext` 的 `disabled` / `shadow` / `enforced` wiring 和 workspace allowlist；仓库默认仍为 disabled。
- shadow 只计算 plan，发出无正文 metrics 或明确 `applied=false` 的独立 observation；不得写权威 assembly manifest。
- 新增 `docs/runbooks/session-context-window-management.md`：能力配置、gate、shadow 比对、告警、profile 冻结、故障定位、回滚与数据保留。
- 增加 1k/10k/100k 消息、0/10/100 tools 的分页量、对象读取、内存、assembly P95、TTFT benchmark/容量测试。
- 建立 token-window 开放门槛：模型能力已验证、估算不系统性低于 actual usage、overflow/失败/TTFT/内存满足 runbook 阈值。

**不可违背约束**

- shadow 不能改变 `Engine.Run` messages/output cap 或用户可见结果。
- 本项只允许内部 workspace allowlist；不得全平台默认开启，不得开启 rolling summary。
- rollout 失败只关闭 gate；不删除 manifest/schema/消息。

**完成定义**

- gate 状态矩阵、创建时快照、shadow/applied 区分和 allowlist 测试通过。
- token-window 内部灰度有可执行 runbook、dashboard/alert 定义与一次演练证据。
- legacy rollback 在测试环境证明不重写旧 run/消息。

**开发自测**

- `backend/`：`go test ./internal/config/... ./internal/contextwindow/... ./internal/chatruntimebridge/...` 并运行 context benchmarks。
- 按 runbook 在本地/测试 fixture 演练 disabled → shadow → enforced → disabled，记录消息 digest 与状态差异。

**独立验证标准**

完成后新建仅用于 IC-10 的临时只读 verification subagent（不是持久 Agent/Issue，不得复用任何既有 verifier）。它必须验证 shadow 实际输入完全不变、manifest 只记录 applied、默认配置关闭、allowlist/freeze/rollback 可执行，并审阅性能证据；任何默认启用或 shadow 改输入都必须 `FAIL`。

**回滚 / 风险**

- 风险：shadow 被误当 applied 审计，或 gate 配置意外全开。
- 回滚：关闭独立 gate；profile 级冻结；永久数据保持不动。

**进度记录**

- 状态：PENDING
- 实现证据：—
- 开发自测：—
- Verification subagent / 结果：—

### IC-11 — 摘要存储、对象与 claim 状态机

**目的**

建立 D4-A 的加密永久摘要事实和多副本幂等 claim，不调用摘要模型。

**精确范围**

- 新增 `backend/internal/database/migrations/000003_chat_context_summaries.up.sql` 与对应 down 文件。
- 新建 `chat_context_summaries`：workspace/session、BUILDING/READY/FAILED、owner token/lease、coverage、source/parent digest、policy/summarizer/template fingerprint、对象引用、token 估值、attempt/backoff 与安全 failure code。
- 在 `backend/internal/storedobject/models.go`、access policy、secure store/bucket mapping 和 DB kind 约束增加 `CHAT_CONTEXT_SUMMARY`，强制 SENSITIVE + PERMANENT + encryption。
- 新增 `backend/internal/contextsummary` repository/claim API；以已批准幂等键唯一，READY 后不可变，FAILED 可按有界 backoff/attempt 重领。
- 使用现有 staged/finalize 或等价补偿，防止缺失对象引用和永久孤儿对象。

**不可违背约束**

- 摘要不写入 `chat_messages`，不存 Redis 作为事实，不降低来源敏感度/保留级别。
- 所有查询带 workspace/session/principal ownership；复合 FK 防跨租户引用。
- 外部模型调用期间不得持有 DB transaction/row lock；本项本身不调用模型。

**完成定义**

- clean migration、永久对象、READY 不可变、lease takeover、重试上限、赢家复用、对象补偿和跨主体拒绝测试齐全。
- 相同幂等键只有一个 READY；竞争 loser 可读 winner 或安全退化。
- 存储容量与备份恢复要求补入 runbook，rolling gate 仍关闭。

**开发自测**

- `backend/`：`go test ./internal/database/... ./internal/storedobject/... ./internal/contextsummary/... ./internal/chat/...`。
- 运行并发/race、lease expiry、对象上传失败与永久保留 acceptance tests。

**独立验证标准**

完成后新建仅用于 IC-11 的临时只读 verification subagent（不是持久 Agent/Issue，不得复用任何既有 verifier）。它必须并发争抢同幂等键、模拟 worker crash/object failure、尝试跨 principal 读取和 READY 修改；只有事实不丢失、不泄漏、不产生双 READY 才可 `PASS`。

**回滚 / 风险**

- 风险：孤儿对象、僵尸 lease、双摘要或跨租户引用。
- 回滚：rolling gate 保持关闭；保留表和永久对象，不执行 destructive down。

**进度记录**

- 状态：PENDING
- 实现证据：—
- 开发自测：—
- Verification subagent / 结果：—

### IC-12 — 受限滚动摘要生成器

**目的**

生成可验证、可复用的连续前缀摘要，同时将提示注入、成本和故障隔离在主运行之外。

**精确范围**

- 在 `backend/internal/contextsummary` 实现 generator、版本化 prompt/template、structured output validator、chunk/pass budget 和结果类型。
- 输入仅为上一 READY 摘要 + 从其高水位后到新 coverage boundary 的连续原始轮次；记录 source digest、parent chain、summarizer snapshot 和 template hash。
- summarizer 使用固定低随机性、无 tools 的模型调用；输入/输出均按自身 capability 预算，最多执行 policy `maxGenerationPasses`。
- 使用 IC-11 claim/object API 提交 READY；超时、限流、校验失败或达到 pass 限制返回可观测 fallback，不使 mandatory 可容纳的主 run 失败。
- 摘要结构仅记录稳定事实、已定决定、未决项、用户偏好/约束；不得声称或授予权限。

**不可违背约束**

- summarizer 无工具、无 SYSTEM 权限提升；摘要正文不进普通日志/manifest/protocol。
- coverage 必须连续且同 workspace/session/principal；摘要不能覆盖 current USER 或未来 raw suffix。
- claim 等待超过主运行时延预算时立即 fallback，不能无限等待。

**完成定义**

- 增量、分块、父链、digest、max passes、恶意历史指令、超时/限流/非法结构和赢家复用测试通过。
- 摘要对象可由 snapshot/template/source 重建审计，正文 hash 一致。
- summary usage 与主模型 usage 分离并进入低敏观测。

**开发自测**

- `backend/`：`go test ./internal/contextsummary/... ./internal/modelapi/... ./internal/storedobject/...`。
- 运行恶意 prompt corpus、结构 fuzz、超时/限流/并发 fixture 和敏感 canary。

**独立验证标准**

完成后新建仅用于 IC-12 的临时只读 verification subagent（不是持久 Agent/Issue，不得复用任何既有 verifier）。它必须验证 summarizer 没有 tools、摘要不能提升权限、coverage/digest 连续、所有失败均返回 fallback 且无正文泄漏；全部成立才可 `PASS`。

**回滚 / 风险**

- 风险：摘要漂移、提示注入、额外成本/延迟或生成风暴。
- 回滚：rolling mode 保持关闭；停止 generator worker/调用，READY 对象按永久审计数据保留。

**进度记录**

- 状态：PENDING
- 实现证据：—
- 开发自测：—
- Verification subagent / 结果：—

### IC-13 — `rolling_summary` assembler/bridge 接入

**目的**

在硬 token window 不变量之上加入可选滚动摘要，失败时始终安全退化而不恢复全量历史。

**精确范围**

- 扩展 `backend/internal/contextwindow` assembler：摘要只覆盖 omitted 连续前缀，不与 raw suffix/current USER 重叠。
- 注入顺序固定为 SYSTEM → 带固定不受信前缀的合成 ASSISTANT summary → 最近原始完整轮次 → 当前 USER。
- 当 summary + raw suffix 不适配时逐个淘汰最老完整轮次并更新 coverage；summary + mandatory 仍不适配则丢弃 summary，退化 token window。
- 更新 `backend/internal/chatruntimebridge` 初始运行 wiring，复用/生成摘要；Resume 仍完全旁路。
- 增加 summary reuse/build/fallback/latency/usage metrics；`rolling_summary` 仅在显式 policy + 独立 allowlist 下生效。

**不可违背约束**

- 硬预算 assembler 始终最终断言，不信任摘要估值或生成器结果。
- 摘要使用 ASSISTANT role，不得成为 SYSTEM、工具授权或审批依据。
- 任何 summary 错误都只能退化 token window；不得退回全量历史，不得让已可容纳 mandatory 的主 run 失败。

**完成定义**

- 覆盖/不重叠、摘要过大、raw 逐轮淘汰、生成失败、竞争、注入攻击和 Resume 旁路集成测试通过。
- manifest 正确记录 summary ID/hash/coverage 和最终 raw IDs，能重建实际输入。
- token-window 默认和 gate-off 行为无回归。

**开发自测**

- `backend/`：`go test ./internal/contextwindow/... ./internal/contextsummary/... ./internal/chatruntimebridge/... ./internal/einoruntime/...`。
- 运行 Console/AAP rolling golden、恶意摘要、安全 fallback、重复投递和 approval-resume 回归。

**独立验证标准**

完成后新建仅用于 IC-13 的临时只读 verification subagent（不是持久 Agent/Issue，不得复用任何既有 verifier）。它必须对照 manifest 重建实际 messages，验证无 coverage 重叠、摘要非 SYSTEM、所有 failure 路径退化 token window 且 Resume 零调用；满足才可 `PASS`。

**回滚 / 风险**

- 风险：摘要与 raw 重复、上下文污染、summary 故障拖垮主运行。
- 回滚：独立关闭 rolling allowlist，保留 token-window；READY 摘要和 manifest 不删除。

**进度记录**

- 状态：PENDING
- 实现证据：—
- 开发自测：—
- Verification subagent / 结果：—

### IC-14 — 管理 UI、全链路验收与发布交付

**目的**

完成管理员配置体验、终态用户体验、跨入口验收和可执行发布/回滚交付；不在代码仓库中默认开启生产 gate。

**精确范围**

- 更新 `frontend/src/types/domain.ts`、`services/api.ts`、`stores/modelConfigs.ts`、`stores/workspaces.ts`、`stores/agents.ts`。
- 更新 `ModelAPIConfigsView.vue`、`WorkspacesView.vue`、`AgentsDialogs.vue` / `AgentsStudioPanel.vue`：展示严格 runtime capability、workspace/agent policy、继承来源、有效上限和权限；rolling 选项受独立 gate/资格条件限制。
- 完成 `ChatExecutionPageBody.vue` 的 context 压缩非敏提示与错误动作；不展示摘要正文或伪造聊天消息。
- 补齐 `docs/openapi/agent-access-v1.yaml` 的 additive error code 说明（不改请求/成功 shape/冻结事件集合）和 `docs/runbooks/session-context-window-management.md` 的最终发布矩阵。
- 执行技术方案第 16/20 节的单元、集成、E2E、安全、性能、迁移、Console/AAP、legacy、Resume 与回滚验收。
- 形成 PR/CI/rollout 交付证据；实际生产 allowlist/默认切换仍由发布授权流程执行，不由此项擅自改变。

**不可违背约束**

- UI 不能允许扩大模型硬窗口、绕过权限或向 AAP 请求添加 per-run policy。
- rolling 不成为 workspace 平台默认；只有 token-window 指标稳定、摘要安全/合规门槛通过后才可显式 opt-in。
- 所有原始消息、摘要对象和 manifest 保留；回滚不依赖删表/删数据。

**完成定义**

- 14 项所有前置均为 VERIFIED；管理 UI 权限、继承、校验与错误行为测试通过。
- 技术方案第 20 节 9 条验收逐条有测试/运行证据；10k+ 会话和工具规模 benchmark 满足 runbook 门槛。
- Console/AAP 共用相同 digest/错误，AAP protocol compatibility diff 无破坏；Resume/工具零重复。
- runbook 完成 disabled → shadow → token-window allowlist → rolling opt-in 和反向回滚演练。
- PR 使用 ZKL-74 可路由 key，列出本 checklist 的逐项实现与 verification PASS 证据；CI 可按平台规则交接。

**开发自测**

- `backend/`：`go test ./...`，并运行 context benchmarks、migration/acceptance/security/protocol compatibility suites。
- `frontend/`：`npm test -- --run`、`npm run type-check`、`npm run lint`、`npm run build`、相关 Playwright E2E。
- 执行 runbook 的 gate/rollback 演练，记录无正文的指标与 manifest/digest 证据。

**独立验证标准**

完成后新建仅用于 IC-14 的临时只读 verification subagent（不是持久 Agent/Issue，不得复用 IC-01～IC-13 或任何既有 verifier）。它必须独立审计全部已填进度、技术方案第 20 节 9 条验收、完整测试输出、AAP compatibility、权限/泄漏、性能门槛和回滚演练。只有给出明确总体验收 `PASS` 才可视为实现完成。

**回滚 / 风险**

- 风险：UI 与后端继承语义漂移、协议意外破坏、性能门槛未达却扩大 rollout。
- 回滚：关闭 rolling，再关闭 session-context gate；回滚 UI/API reader，不删除 expand-only schema、manifest、原始消息或永久摘要。

**进度记录**

- 状态：PENDING
- 实现证据：—
- 开发自测：—
- Verification subagent / 结果：—

## 6. PR 边界映射

| PR | Checklist 项 | 合并条件 |
| --- | --- | --- |
| PR1 | IC-01～IC-03 | 三项均 VERIFIED；schema additive、配置/快照可用、gate 默认关闭、运行行为不变 |
| PR2 | IC-04～IC-06 | 三项均 VERIFIED；纯 estimator/history/assembler 完整，不接生产运行路径 |
| PR3 | IC-07～IC-08 | 两项均 VERIFIED；token-window 接入、manifest/error/Resume/legacy 门禁通过，仍不默认启用 |
| PR4 | IC-09～IC-10 | 两项均 VERIFIED；usage/观测/安全错误/shadow/runbook 就绪，可做内部 token-window allowlist |
| PR5 | IC-11～IC-13 | 三项均 VERIFIED；摘要永久存储、受限生成、rolling 接入，模式仍显式 opt-in |
| PR6 | IC-14 | 最终独立 verifier PASS；管理 UI、全链路验收与发布/回滚交付齐全 |

PR 可因仓库维护需要进一步缩小，但不得跨越未 VERIFIED 的依赖、合并未验证项或改变上述已批准边界。每个 PR 都要可通过 gate 回滚；任何需要改变 D1～D7 的实现提议必须回到 Knower 和负责人确认。

## 7. 实施完成判定

只有同时满足以下条件，Conductor 才可把实现视为完成：

1. IC-01～IC-14 均在本文标记 `VERIFIED`，每项有独立且未复用的 verification subagent `PASS` 摘要；
2. 技术方案第 20 节 9 条验收均有可追溯证据；
3. 全量 backend/frontend/E2E/安全/性能/迁移/protocol compatibility 测试满足 IC-14；
4. PR/CI/rollout/runbook/rollback 证据齐全，且没有未批准的设计偏移；
5. 生产默认 gate 未被本实现擅自开启，原始消息审计链、Resume/checkpoint 和 AAP 兼容性保持完整。
