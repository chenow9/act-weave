# ZKL-81 Agent 上下文 LLM Compact 产品设计

> 版本：v0.2（已批准并冻结）
>
> 日期：2026-07-30
>
> 状态：负责人 chenow 已明确批准；产品需求确认完成，可交 Knower 进入技术方案

## 0. 版本记录

| 版本 | 输入 | 变更 |
| --- | --- | --- |
| v0.1 | Issue 原始需求与代码/API/测试事实核查 | 提供 D1～D7 选项、推荐项及 AC-01～AC-14 |
| v0.2 | 负责人评论 `3dec7a0e-5833-4fa3-9cdb-ca6463e56e40`、批准评论 `86516f5d-00b2-4292-a5d0-3f872d08f047` | D1～D7 全部采用推荐项；D6-A 增加“Agent 审计必须明确指出 Compact 失败并已退化为 `token_window`”；负责人原文“批准 v0.2”，本版本冻结 |

## 1. 背景与问题

当 Agent 会话持续增长时，需要在上下文窗口逼近上限前，把较早的连续对话前缀压缩成一份可复用摘要，再与最近原文轮次一起送给模型。

当前仓库虽然已有滚动摘要的领域、存储和组装基础，但摘要正文由本地确定性抽取逻辑拼接，不是由 LLM 总结；同时真实运行桥尚未调用该摘要生成器。因此当前生产路径既没有“达到 80% 自动触发 LLM Compact”，也没有可供 AAP 或 Agent 审计展示的 Compact 运行记录。

本需求希望做到：

1. 上下文占用达到 80% 时，由 LLM 生成摘要，而不是本地拼接。
2. AAP 与 Agent 审计都能看到本次 Compact 的记录。
3. AAP 可配置是否把摘要正文返回到协议。
4. Agent 审计仅在允许查看明文且前端关闭展示脱敏时显示摘要正文。

## 2. 事实基线

以下为基于当前 `release_v1` 代码、测试和 ZKL-74 文档核查得到的事实，不是本设计假设。

| 事实 | 当前证据 | 产品影响 |
| --- | --- | --- |
| 当前摘要生成器是本地抽取式拼接：逐轮截取用户/助手文本，不调用模型 | `backend/internal/contextsummary/generator.go` | 本需求需要替换生成语义 |
| 真实运行桥的 `rolling_summary` 路径没有向 assembler 传入 `OptionalSummary`，也没有调用 `contextsummary.Generator` | `backend/internal/chatruntimebridge/bridge.go` | 不能只替换生成器，还要补齐运行接入 |
| 当前 token-window 在硬预算放不下完整轮次时才停止加载并裁掉旧历史，没有 80% 水位触发 | `backend/internal/contextwindow/assembler.go`、`backend/internal/chatruntimebridge/bridge.go` | 需新增明确、可测试的触发水位 |
| 上下文策略已有 `rolling_summary`、`summary.maxTokens`、`minEvictedTurns`、`maxGenerationPasses` | `backend/internal/sessioncontext/policy.go` | 可扩展现有策略，不另造平行配置域 |
| Agent UI 已支持选择 `rolling_summary` 及摘要参数，默认最近原文 20 轮、摘要上限 2048 tokens | `frontend/src/components/AgentsStudioPanel.vue`、`frontend/src/utils/session-context-config.ts` | 新配置可放在现有“会话上下文策略”区域 |
| 原始 `chat_messages` 永久保留；摘要有独立元数据表和加密永久对象存储 | `backend/internal/contextsummary`、数据库迁移 `000003` | Compact 不应改写或删除原始消息 |
| 初始运行执行上下文组装；HITL/工具确认恢复直接使用 checkpoint，不重新组装 | `backend/internal/chatruntimebridge/bridge.go` | Compact 只在初始运行前触发 |
| AAP 已有 `notice` 和 `reasoning_summary` item，以及持久化的 `item.completed` 投影能力，但没有 Compact 专用记录 | `backend/internal/protocolevent`、`backend/internal/chatruntime/auxiliary_protocol.go` | 可复用现有协议能力或新增专用 item |
| Agent 审计仅平台管理员可访问；服务端 `agentAudit.debug` 默认关闭，关闭时固定脱敏；debug 开启后，前端才允许切换“数据脱敏” | `backend/internal/agentaudit`、`backend/internal/transport/http/agent_audit.go`、`frontend/src/views/AuditLogsView.vue` | “未开启脱敏可看摘要”必须同时受服务端 debug 与前端展示开关约束 |
| 当前 assembly manifest 记录预算、included segments、omitted count、summary 引用等无正文信息，但 Agent 审计时间轴没有 Compact 步骤 | `backend/internal/execution/context_assembly.go`、`backend/internal/agentaudit` | 需新增可审计的 Compact 生命周期事实 |
| Console 与 AAP 最终共用 Agent runtime 和 `chat_sessions` / `chat_messages` | ZKL-74 方案与当前 application/runtime 路径 | 触发与摘要质量必须一致，只有对外展示策略不同 |

## 3. 用户与目标

### 3.1 用户角色

| 角色 | 需求 |
| --- | --- |
| Agent 配置者 | 启用滚动摘要，决定 AAP 是否返回摘要正文 |
| AAP 调用方 | 知道本次运行发生过 Compact；仅在配置允许时收到摘要正文 |
| 平台管理员 / 审计人员 | 在 Agent 审计时间轴定位 Compact、查看结果与 token 变化；满足明文条件时查看摘要正文 |
| 最终对话用户 | 在长会话中保持主要上下文连续，不因摘要失败泄露内部错误或收到重复回答 |

### 3.2 产品目标

1. 在进入危险水位前稳定触发 LLM Compact，避免等到上游 context overflow 才处理。
2. Compact 后保留较早对话的主要事实、决策、约束和未决项，同时保留最近完整原文轮次。
3. 每次 Compact 都形成可关联到 workspace、session、run、summary 的持久事实。
4. AAP 与 Agent 审计遵循最小披露，记录可见与摘要正文可见分离。
5. Compact 失败不删除原始历史，不把摘要提升为系统指令，也不绕过 Agent、工具、审批或权限策略。

### 3.3 成功指标

- 达到已批准水位的 eligible 初始运行中，Compact 触发记录覆盖率为 100%。
- 成功 Compact 后，本次送模上下文占用降到已批准目标水位以内。
- 不出现因摘要正文进入普通日志、错误、默认 AAP 响应或脱敏审计视图造成的数据泄露。
- 摘要生成失败时，主运行按已批准降级策略处理，且审计/AAP 能区分 `completed`、`fallback`、`failed`。
- 同一 summary 幂等键在并发或重试下只产生一份 READY 摘要。

## 4. 范围

### 4.1 本期范围

- 仅对已启用 `rolling_summary` 且运行快照完整的 Agent 初始运行生效。
- 定义 80% 水位、LLM 摘要生成、Compact 后目标水位和滚动复用规则。
- 用受限 LLM 调用生成摘要：固定模板、禁用工具、不可请求人工审批、低随机性。
- 持久化 Compact 生命周期、token 统计、覆盖边界、摘要引用和结果。
- AAP 返回 Compact 记录，并支持按 Agent 配置决定是否返回摘要正文。
- Agent 审计增加“上下文压缩”时间轴步骤及脱敏展示。
- Console 与 AAP 使用同一 Compact 结果；不为两个入口分别总结。
- 覆盖权限、Loading / Success / Fallback / Error / Disabled 状态和可测试验收。

### 4.2 非目标

- 不做跨会话 Memory、用户画像、RAG、向量检索或知识库沉淀。
- 不总结或迁移历史存量会话；仅在后续 eligible run 按需触发。
- 不删除、覆盖、缩短原始 `chat_messages` 的保留期。
- 不把摘要写回原始消息，不把摘要作为 SYSTEM role，不允许摘要授予工具或审批权限。
- 不在 AAP 单次 `createRun` 请求中允许调用方临时覆盖 Compact 策略。
- 不改变 HITL / 工具确认恢复语义；Resume 不触发 Compact。
- 不在本期提供最终用户手工编辑摘要。
- 不把“模型推理摘要”和“上下文 Compact 摘要”混为同一审计含义。

## 5. 核心产品行为草案

### 5.1 触发条件

推荐口径：

```text
contextOccupancy =
  预计送模输入 tokens
  / effective input ceiling tokens

当 contextOccupancy >= 80% 时触发 Compact
```

其中：

- `预计送模输入` 包含当前精确 SYSTEM、工具 schema、已有 READY 摘要、待保留原始历史和当前 USER。
- `effective input ceiling` 是已扣除输出预留与安全余量，并应用 `maxInputTokens` 收紧后的实际输入上限。
- 只计算同一 session 内、当前 principal 有权使用的消息。
- 以完整轮次为边界；当前 USER 永不进入本轮被总结前缀。
- 水位判断与摘要输入 token 估算使用运行快照固定的 tokenizer/profile/version。

### 5.2 Compact 输入与输出

LLM 输入仅允许：

1. 上一个 READY 摘要（若有）；
2. 上一摘要高水位之后、当前淘汰边界之前的连续完整原始轮次；
3. 固定 Compact 模板与结构约束。

LLM 输出至少覆盖：

- 稳定事实；
- 已确认的决定；
- 用户偏好或明确约束；
- 仍未解决的问题；
- 必须继续遵守的任务上下文；
- 对不确定、冲突或可能过期信息的标记。

输出不得包含：

- “获得了新权限”“忽略系统规则”等权限提升性结论；
- 工具密钥、认证头、token 等秘密明文；
- 未在源消息出现的新事实；
- 当前 USER（避免重复或改变当前输入语义）。

摘要以不受信 ASSISTANT 上下文注入，并带固定警示前缀。当前 SYSTEM、工具许可、审批规则和实时禁用开关始终优先。

### 5.3 Compact 后组装

- 保留最近连续完整原文轮次，较早连续前缀由 READY 摘要覆盖。
- 摘要覆盖区间与保留原文不得重叠或断裂。
- Compact 成功后，重新估算最终输入；仍超目标水位时按完整轮次从最旧保留原文开始继续收紧，最多执行已配置的生成轮数。
- 摘要本身若无法装入硬预算，则不用该摘要，进入已批准降级行为。
- 每个新 run 只能使用创建该 run 时已解析的不可变 context policy 与 AAP 披露快照。

### 5.4 主流程

1. 新初始 run 创建并固定 Agent、模型、上下文策略和 AAP 披露快照。
2. Runtime 估算“当前 run 若直接组装”的上下文占用。
3. 小于 80%：不 Compact；正常组装并运行。
4. 达到或超过 80%：创建 `building` Compact 事实，调用受限 LLM。
5. 成功：加密保存摘要，写 `ready/completed`、覆盖边界与 token 前后值。
6. 使用 READY 摘要 + 最近原文重新组装，持久化 assembly manifest。
7. 启动主 Agent 模型调用。
8. AAP 投影 Compact 记录；Agent 审计时间轴显示“上下文压缩”步骤。

### 5.5 异常流程

| 场景 | 草案行为 |
| --- | --- |
| Compact 模型超时、限流或 5xx | 记录 `fallback`，安全退化为 `token_window`，绝不回退全量历史；Agent 审计明确显示“Compact 失败，已退化为 token_window” |
| Compact 输出为空、超长、结构非法或包含禁止字段 | 丢弃输出并记录稳定失败原因，不保存为 READY |
| 另一 worker 正在生成相同摘要 | 等待到延迟预算；超时后按 D6-A 退化为 `token_window`，不重复生成 |
| 已有 READY 摘要可复用 | 校验 workspace/session/coverage/digest/policy 后复用，不重复调用 LLM |
| 当前 USER + SYSTEM + tools 已超过硬预算 | 不尝试总结当前 USER；按现有 `CONTEXT_REQUIRED_INPUT_TOO_LARGE` 失败 |
| Resume / HITL 恢复 | 不重新估算、不 Compact、不新增记录 |
| Agent 或模型被禁用 | 按现有 kill switch 失败，不因 Compact 绕过 |
| Compact 成功但主 Agent 调用失败 | Compact 记录仍保留为 completed；主 run 独立失败 |

## 6. AAP 产品契约

### 6.1 可见性原则

- AAP 调用方始终能知道“本次 run 发生过 Compact”及结果。
- 摘要正文是否返回由 Agent 配置决定，默认不返回。
- 配置在 run 创建时快照化；之后修改配置不改变已持久化 run 的 replay 结果。
- AAP 只能读取其已获授权的 agent/conversation/run；不能通过 summary ID 跨 workspace 或跨主体读取摘要。
- `list/get/events/SSE replay` 对同一 run 返回一致结果。

### 6.2 无论是否返回正文都可见的字段

- Compact 状态：`completed` / `fallback` / `failed`；
- 触发阈值和实际触发占用率；
- Compact 前后估算 tokens；
- 被覆盖的消息/轮次数量；
- summary ID 或不可逆摘要标识（不得作为可猜测下载地址）；
- 是否包含摘要正文：`contentIncluded`；
- 稳定失败码（若有），不含 provider body。

### 6.3 摘要正文配置

建议在 Agent 的会话上下文策略中增加：

```text
aap.includeCompactionSummary: false  // 默认
```

- 仅有 Agent 编辑权限的用户可修改。
- 只控制 AAP 协议正文披露，不影响 Agent 运行是否使用摘要，也不影响平台管理员审计能力。
- 关闭时不能通过其他 AAP 字段、错误、日志、delta 或 artifact 间接取得摘要正文。
- 开启时返回的是实际注入主 Agent 上下文的 READY 摘要，不重新生成展示版。

## 7. Agent 审计产品契约

Agent 审计时间轴新增步骤：

```text
标题：上下文压缩
类型：compact
状态：进行中 / 已完成 / 已降级 / 失败
```

### 7.1 固定可见信息

平台管理员进入 Trace 详情后，即使开启脱敏，也能看到：

- 本次发生过 Compact；
- 触发占用率与阈值；
- Compact 前后 tokens；
- 覆盖消息数/轮次数；
- 摘要模型标识的非敏感快照；
- 状态、耗时、重用/新生成、稳定失败码；
- run ID、summary ID 等关联标识。

当 Compact 失败并执行 D6-A 时，时间轴必须使用“已降级”而不是“已完成”：

- 标题或状态文案明确显示“Compact 失败，已退化为 `token_window`”；
- 记录 `fallbackFrom=rolling_summary`、`fallbackTo=token_window`、稳定失败码和失败阶段；
- 可显示安全、非敏感的失败分类（如 timeout、rate_limited、invalid_summary），不得显示 provider body；
- 不产生 READY summary ID，不把 `token_window` 主运行成功误记为 Compact 成功；
- 无论审计是否脱敏，上述降级事实和目标模式均可见；脱敏只控制摘要正文及其他敏感字段。

### 7.2 摘要正文展示

仅当以下条件同时满足时展示明文：

1. 当前用户是 `PLATFORM_ADMIN`；
2. 服务端 `agentAudit.debug=true`，允许返回明文；
3. Agent 审计页面的“数据脱敏”开关被管理员主动关闭；
4. 摘要加密对象存在且完整性校验通过。

任一条件不满足时：

- Compact 步骤仍存在；
- 摘要正文显示为“已脱敏”或“密文不可读”，不显示前 80 字预览；
- 前端不能通过本地开关恢复服务端未返回的正文。

AAP 的 `includeCompactionSummary` 不影响 Agent 审计。两者是独立披露边界。

## 8. 权限、配置与状态

### 8.1 权限矩阵

| 动作 | 平台管理员 | 有 Agent 编辑权限的 workspace 用户 | AAP principal | 普通 workspace viewer |
| --- | --- | --- | --- | --- |
| 配置 `rolling_summary` | 按现有 Agent 编辑权限 | 允许 | 禁止 | 禁止 |
| 配置 AAP 返回摘要正文 | 按现有 Agent 编辑权限 | 允许 | 禁止 | 禁止 |
| 查看 Compact 元数据审计 | 允许 | 禁止（沿用当前 Agent 审计边界） | 仅协议内自己的 run | 禁止 |
| 查看审计摘要明文 | 仅满足 debug + 关闭脱敏时允许 | 禁止 | 不适用 | 禁止 |
| 通过 AAP 查看摘要正文 | 不按平台角色判断 | 不按 workspace UI 角色判断 | 仅 Agent 快照允许且 run scope 匹配时 | 不适用 |

### 8.2 UI 状态

| 状态 | Agent 配置页 | Agent 审计 | AAP |
| --- | --- | --- | --- |
| Loading | 保存控件禁用，保留原值 | Compact 卡片骨架/加载态 | 不产生伪事件 |
| Disabled / 非 rolling_summary | 隐藏 Compact 高级项与 AAP 正文开关，说明不会自动摘要 | 无 Compact 步骤 | 无 Compact 记录 |
| Ready | 显示阈值、目标水位、摘要参数与 AAP 开关 | 显示 completed | 显示 completed 记录 |
| Fallback | 不改变已保存配置 | 黄色“Compact 失败，已退化为 `token_window`”，展示稳定原因与 from/to | metadata-only fallback 记录 |
| Failed | 保存失败时回滚 UI 值 | 红色失败状态 | failed 记录或主 run 失败事件 |
| Empty | 没有足够旧轮次时说明“暂无可压缩历史” | 不创建虚假 completed | 不发送 Compact 记录 |
| Permission denied | 控件只读或隐藏 | 403 / 无入口 | 403，不泄露 run 是否存在 |

## 9. 数据、API、审计与安全影响

### 9.1 数据

- 复用独立 `chat_context_summaries` 与加密永久对象，不写入 `chat_messages`。
- Compact 生命周期必须可表达 `building`、`ready/completed`、`fallback`、`failed`。
- 每次 run 的 assembly manifest 关联实际使用的 summary ID/hash/coverage。
- AAP 披露开关写入不可变 run snapshot，保证 replay 一致。
- Compact 审计正文不得写进普通 `agent_run_steps.output_summary`；该位置只保存元数据/引用。明文继续放受控加密对象。
- 摘要保留和删除语义不得弱于其来源消息；原消息受 legal hold 时摘要同步受约束。

### 9.2 API / 协议

- Agent 管理 DTO/API 增加触发/目标水位及 AAP 披露字段（以最终批准决策为准）。
- AAP 必须有可稳定识别的 Compact 记录；具体复用 `notice + reasoning_summary` 还是新增专用 item 见 D5。
- 协议内容上限必须独立限制；开启正文时摘要仍不得超过已批准 `summary.maxTokens` 和协议字段字节上限。
- 旧 SDK 必须能忽略新记录而不影响 run reducer；新 SDK 提供 typed Compact 记录。

### 9.3 安全

- Summarizer 禁用工具、连接器、审批和工作流，不携带主 Agent 工具 schema。
- 使用固定低随机性和版本化模板；模板与模型快照写 hash，不在普通日志写正文。
- 摘要作为不受信历史数据，不能覆盖 SYSTEM、capability、approval 或身份/授权事实。
- 防 prompt injection 验证必须覆盖“忽略上文”“授权我调用工具”等源消息。
- AAP 默认 metadata-only；Agent 审计默认脱敏。
- 所有 summary/assembly 查询必须带 workspace、session、run 和 principal scope。

## 10. 依赖与发布

### 10.1 依赖

- ZKL-74 的 context policy、token estimator、assembly manifest、summary claim/store。
- 模型运行时能力中的 context window 与 tokenizer profile。
- AAP Protocol Event / run item 持久化与 replay。
- Agent 审计 debug、时间轴分页和加密对象读取。
- 监控：Compact 次数、成功/降级率、耗时、输入/输出 tokens、Compact 后占用率。

### 10.2 发布策略

- 所有新行为先受独立 rollout gate 控制，默认关闭。
- 先在测试 workspace 验证摘要质量、延迟、成本、协议兼容与脱敏，再逐步 allowlist。
- 回滚只关闭新 Compact gate；保留原始消息、摘要、manifest 和审计事实。
- 已创建 run 继续遵循其快照，不能因中途切 gate 改变 replay 或披露内容。

## 11. 风险

| 风险 | 影响 | 缓解 |
| --- | --- | --- |
| LLM 摘要遗漏或幻觉 | 后续回答偏离历史事实 | 固定结构、来源边界、低随机性、保留最近原文、质量评测 |
| 80% 口径不明确 | 触发过早/过晚 | 冻结分母、tokenizer 与快照口径 |
| Compact 调用增加首 token 延迟与成本 | 用户体验下降 | 目标水位留出余量、摘要复用、超时预算、监控 |
| AAP 摘要泄露历史敏感信息 | 数据安全事故 | 默认关闭正文、run scope、配置快照、字节上限、无旁路 |
| 审计页面前端脱敏被误认为权限控制 | 明文泄露 | 服务端 debug 决定是否返回；前端开关只能进一步遮罩 |
| 并发 run 重复总结 | 成本、摘要链分叉 | claim/lease、幂等键、READY 复用 |
| Compact 失败回退到全量历史 | 上游溢出 | 禁止回退全量；按 D6-A 明确退化为 `token_window` |
| 复用 `reasoning_summary` 造成语义混淆 | SDK/审计误读 | D5 优先采用可明确区分的 Compact 契约 |

## 12. 已收敛的产品决策

负责人在评论 `3dec7a0e-5833-4fa3-9cdb-ca6463e56e40` 中选择 D6-A 并要求补充审计降级说明，其余决策采用推荐项。因此 v0.2 冻结为 D1-A、D2-A、D3-A、D4-A、D5-A、D6-A（含审计降级增强）、D7-A。下表保留备选项仅用于决策追溯，不再表示仍有未决选择。

### D1：80% 的分母

| 选项 | 定义 | 影响 |
| --- | --- | --- |
| A（已选） | 预计送模输入 / effective input ceiling | 与实际可用输入预算一致，考虑输出预留、安全余量和 `maxInputTokens` |
| B | 预计送模输入 / 模型标称 context window | 表述直观，但可能在较小 `maxInputTokens` 下先裁剪再触发 |
| C | 预计“原始会话历史” / 模型标称 context window | 不受工具变化影响，但不能反映真实本次请求风险 |

### D2：触发后何时生成

| 选项 | 行为 | 影响 |
| --- | --- | --- |
| A（已选） | 当前 run 主模型调用前同步完成 Compact | 当前 run 立即受益；增加首次触发 run 的延迟 |
| B | 异步生成，仅下一个 run 使用 | 当前 run 延迟低；首次越过 80% 的 run 仍可能被裁剪或超限 |
| C | 同步等待短预算，超时转异步 | 延迟折中，但状态与幂等实现更复杂 |

### D3：Compact 后目标水位

| 选项 | 行为 | 影响 |
| --- | --- | --- |
| A（已选） | 降到 effective input ceiling 的 60% 以内 | 为后续多轮留出空间，减少每轮重复 Compact |
| B | 降到 70% 以内 | 保留更多原文，但更快再次触发 |
| C | 只遵守 `summary.maxTokens`，不设目标水位 | 配置简单，但无法承诺 Compact 后实际余量 |

### D4：Summarizer 模型来源

| 选项 | 行为 | 影响 |
| --- | --- | --- |
| A（已选） | 使用当前 run 已快照的 Agent 模型，禁用工具并使用专用模板 | 无新增模型配置，语种/能力一致；高价模型会提高 Compact 成本 |
| B | 平台统一配置专用 Compact 模型 | 成本和质量可统一控制；增加平台配置和模型可用性依赖 |
| C | workspace 单独选择 Compact 模型 | 最灵活；权限、快照、运维和回退复杂度最高 |

### D5：AAP Compact 记录形态与配置范围

| 选项 | 行为 | 影响 |
| --- | --- | --- |
| A（已选） | 新增语义明确的 `context_compaction` item；Agent 级开关控制 `summary` 字段，默认 false | 单一记录表达完整，SDK 语义清晰；需要协议/schema/SDK 演进 |
| B | 始终发 `notice(CONTEXT_COMPACTED)`；开关开启时再发 `reasoning_summary` | 可复用现有 item；开启时一件事产生两条记录，且容易与模型推理摘要混淆 |
| C | 始终发 `reasoning_summary`；关闭时 text 只写通用提示 | 变更最小；把上下文 Compact 错标为推理摘要，不推荐 |

配置范围建议为 Agent 级，因为 AAP 暴露的是 Agent 契约；不允许 AAP 请求级覆盖。若选择 workspace 级或 client credential 级，需要重新确认继承优先级。

### D6：LLM Compact 失败时主 run 行为

| 选项 | 行为 | 影响 |
| --- | --- | --- |
| A（已选，含审计增强） | 记录 fallback，退化为安全 `token_window`，绝不回退全量历史；Agent 审计明确显示“Compact 失败，已退化为 token_window” | 主服务可用性最好，但当前 run 可能丢失较早语义；审计可准确区分 Compact 成功与主运行降级成功 |
| B | Compact 失败则主 run 失败 | 语义最严格；summarizer 波动会直接影响 Agent 可用性 |
| C | 80%～硬上限间 fallback；达到硬上限则失败 | 风险分层，但阈值和用户错误语义更复杂 |

### D7：审计明文条件

| 选项 | 行为 | 影响 |
| --- | --- | --- |
| A（已选） | 沿用现有全局 `agentAudit.debug` + 平台管理员在页面关闭脱敏 | 与现状一致，改动小；需要重启才能改变服务端明文能力 |
| B | 增加 workspace 级审计明文策略，再叠加平台管理员关闭脱敏 | 租户控制更细；增加高风险权限和配置审计 |
| C | 只要平台管理员关闭前端脱敏就读取明文 | 操作简单，但把安全边界交给前端，不可接受 |

## 13. Given / When / Then 验收标准

### AC-01：未到水位

```gherkin
Given Agent 已启用 rolling_summary，预计输入占用为 79.99%
When 创建初始 run
Then 不调用 Compact LLM
And 不创建 Compact 协议记录或审计步骤
And 正常组装并执行主模型
```

### AC-02：达到 80% 触发

```gherkin
Given Agent 已启用 rolling_summary，且有足够的可压缩旧完整轮次
And 预计输入占用为 80.00%
When 创建初始 run
Then 在主模型调用前触发一次 LLM Compact
And 当前 USER 不进入摘要覆盖区间
And 成功后最终输入占用不超过已批准目标水位
```

### AC-03：LLM 而非本地拼接

```gherkin
Given Compact 被触发
When 生成摘要
Then 使用已批准来源的模型和版本化 Compact 模板
And 禁用工具、工作流和人工审批
And 不调用旧的本地抽取式正文拼接作为成功摘要
```

### AC-04：滚动复用与幂等

```gherkin
Given 同一 session 已有校验通过的 READY 摘要
When 新 run 再次达到 80%
Then 输入为上一 READY 摘要加其高水位后的连续旧轮次
And 并发相同幂等键最多产生一份 READY 摘要
And 摘要覆盖区间与保留原文无重叠、无断裂
```

### AC-05：AAP 默认不返回正文

```gherkin
Given Agent 的 aap.includeCompactionSummary 为 false 或未配置
When AAP run 发生 Compact 并通过 list/get/SSE replay 读取
Then 每个入口都能看到一致的 Compact 元数据与结果
And 任一入口均不包含摘要正文、正文片段或可下载正文 URL
```

### AC-06：AAP 配置返回正文

```gherkin
Given Agent 的 aap.includeCompactionSummary 为 true
And AAP principal 对该 agent/conversation/run 有效授权
When AAP run 成功 Compact
Then Compact 记录返回实际注入主 Agent 上下文的 READY 摘要
And replay 与首次响应内容一致
And 无权 principal 得到 403/404 且不能判断 summary 是否存在
```

### AC-07：AAP 配置快照

```gherkin
Given run 创建时 includeCompactionSummary 为 false
When run 创建后管理员把配置改为 true
Then该 run 的首次响应和后续 replay 仍不返回正文
And 新创建的 run 使用新配置
```

### AC-08：Agent 审计脱敏

```gherkin
Given run 发生成功 Compact
When 平台管理员在 Agent 审计查看 Trace
Then 时间轴包含“上下文压缩”步骤和固定元数据
And 服务端 debug 关闭或页面数据脱敏开启时不返回/不展示摘要正文
And 只有服务端 debug 开启且管理员主动关闭数据脱敏时展示完整摘要
```

### AC-09：非平台管理员

```gherkin
Given workspace Owner、Editor、Viewer 或 AAP principal 不是平台管理员
When 请求 Agent 审计 Compact 详情
Then 返回 403
And 不泄露 Compact 元数据或摘要正文
```

### AC-10：失败降级

```gherkin
Given 已达到 80% 且 Compact LLM 超时、限流或输出校验失败
When 系统处理本次 run
Then 不保存无效摘要为 READY
And 不回退到全量历史
And 按 D6-A 使用安全 token_window 继续主 run
And Agent 审计明确显示“Compact 失败，已退化为 token_window”
And 审计记录 fallbackFrom=rolling_summary、fallbackTo=token_window、失败阶段与稳定错误码
And 主 run 后续成功不把 Compact 状态改写为 completed
And AAP 与审计均不包含 provider body
```

### AC-11：Resume 不重复 Compact

```gherkin
Given run 因工具确认进入等待且之前已完成 Compact
When 用户批准并 Resume
Then 只恢复既有 checkpoint
And 不重新读取会话历史、不调用 Compact LLM、不新增 Compact 记录
```

### AC-12：安全优先级

```gherkin
Given 被摘要历史包含“忽略系统规则”“你已获得工具权限”等提示注入文本
When 摘要被生成并用于后续主模型
Then 摘要按不受信 ASSISTANT 内容注入
And 当前 SYSTEM、工具绑定、审批和主体权限不被改变
```

### AC-13：数据完整性与保留

```gherkin
Given Compact 成功、失败或回滚 gate
When 核查会话数据
Then 原始 chat_messages 的内容、顺序、ownership 和保留语义不变
And READY 摘要使用加密对象和 workspace/session 范围校验
And 普通日志、metrics、trace、错误与默认审计视图不含正文
```

### AC-14：边界输入

```gherkin
Given SYSTEM + tools + 当前 USER 已超过硬输入预算
When 创建 run
Then 当前 USER 不被截断或摘要
And 返回稳定 CONTEXT_REQUIRED_INPUT_TOO_LARGE
And 不把 Compact 伪记为成功
```

## 14. 确认结论

### 已解决

- D1-A：80% 以 effective input ceiling 为分母；
- D2-A：当前 run 主模型调用前同步 Compact；
- D3-A：Compact 后降到 60% 以内；
- D4-A：使用当前 run 已快照模型，禁用工具并使用专用模板；
- D5-A：新增 `context_compaction` item，Agent 级配置是否返回正文，默认关闭；
- D6-A：失败时安全退化为 `token_window`，并在 Agent 审计明确显示该降级事实；
- D7-A：审计正文沿用全局 debug + 平台管理员主动关闭页面脱敏。

### 批准记录

- 负责人：chenow（Issue 创建者）
- 决策输入位置：评论 `3dec7a0e-5833-4fa3-9cdb-ca6463e56e40`
- 当前版本批准位置：评论 `86516f5d-00b2-4292-a5d0-3f872d08f047`
- 批准原文：`批准 v0.2`
- 批准范围：本文全部范围、非目标、D1-A～D7-A（含 D6 审计增强）及 AC-01～AC-14
- 未决项：无

## 15. 冻结范围与 Knower 交接输入

### 15.1 冻结范围

1. 仅在 `rolling_summary` eligible 初始 run 中，以 effective input ceiling 为分母，预计输入占用达到 80% 时同步触发 LLM Compact。
2. 使用当前 run 已快照的 Agent 模型、专用模板、禁用工具与审批；成功后把上下文降到 60% 以内。
3. 新增 AAP `context_compaction` item；Agent 级配置是否返回实际摘要正文，默认关闭并在 run 创建时快照化。
4. Agent 审计始终显示 Compact 生命周期和非敏感元数据；摘要正文仅在全局 debug 开启且平台管理员关闭页面脱敏时显示。
5. Compact 失败按 D6-A 安全退化为 `token_window`，绝不回退全量历史；审计明确显示“Compact 失败，已退化为 `token_window`”及 from/to、失败阶段和稳定错误码。
6. 原始消息、摘要加密对象、assembly manifest、权限隔离、Resume 不重复 Compact 等约束按本文执行。

### 15.2 冻结非目标

- 不做跨会话 Memory、RAG、向量检索、用户画像或知识库沉淀。
- 不迁移或重写历史会话，不删除或缩短原始 `chat_messages` 保留期。
- 不把摘要作为 SYSTEM role，不允许摘要提升工具、审批或主体权限。
- 不开放 AAP request 级 Compact 策略覆盖，不为 Console/AAP 分别生成摘要。
- 不改变 HITL/checkpoint Resume 语义，不提供最终用户手工编辑摘要。
- 未经新的产品确认，不得把上下文 Compact 复用为模型推理摘要。

### 15.3 验收基线

技术方案、实现 checklist 与 Sentinel 验收必须完整映射本文 AC-01～AC-14；不得只覆盖成功路径。尤其必须覆盖 80% 边界、60% 目标、并发幂等、AAP 正文开关及 replay、审计脱敏、D6-A 降级标识、Resume、安全优先级和超大当前输入。

### 15.4 交给 Knower 的输入

Knower 的技术方案必须基于当前真实差距，而不是假设 rolling summary 已完整接入：

1. 替换 `contextsummary.Generator` 的本地抽取式拼接为受限 LLM 生成，并把生成/复用真正接入 `chatruntimebridge` 与 assembler。
2. 定义可复现的 80% 水位、60% 目标、完整轮次边界、token estimator/version、最大生成轮数和延迟预算。
3. 设计 Compact `building/completed/fallback/failed` 持久事实、READY 摘要对象、assembly manifest、claim/lease 和并发幂等。
4. 设计 AAP `context_compaction` schema、Protocol Event/run item、OpenAPI/SDK/reducer 兼容、Agent 级披露配置与 run snapshot/replay。
5. 设计 Agent 审计 `compact` 时间轴、受控摘要对象读取、全局 debug/前端脱敏边界，以及 D6-A 的 from/to 和稳定失败码。
6. 保证 Compact 失败只退化到安全 `token_window`，不回退全量历史；主 run 成功不得覆盖 Compact fallback 状态。
7. 给出迁移、权限、加密与保留、prompt injection、防泄露、rollout gate、指标、回滚和 AC-01～AC-14 测试映射。

任何技术设计或实现中出现新的范围、协议语义、权限、数据保留或验收变化，必须回到产品确认流程。
