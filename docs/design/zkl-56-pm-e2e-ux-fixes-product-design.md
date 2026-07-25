# ZKL-56 PM E2E 缺陷修复产品设计

| 字段 | 值 |
|---|---|
| 文档版本 | v1.0 |
| 日期 | 2026-07-25 |
| 作者 | Atlas · 产品经理 |
| 状态 | **Approved / Frozen** |
| 关联 Issue | ZKL-56 |
| 工作分支 | `fix/zkl-56-pm-e2e-ux-fixes` |
| 输入报告 | `docs/verification/pm-e2e-ux-report-2026-07-25.md` |
| 负责人确认 | chenow 于 Issue 评论 `6c78f26c-da72-4115-9a31-c7d3575322ea` 明确回复“批准 v0.1” |

## 1. 目标

修复 2026-07-25 PM E2E 走查中阻塞核心闭环或造成状态误判的问题，使 Workspace 内有权限的用户能够：

1. 从 Workflow 详情可靠进入画布，并继续 Draft → Compilation → trial → publish。
2. 在 Console 中执行纯文本任务，不被本轮未调用的异常 Tool 连接提前阻断；运行失败后得到一致终态。
3. 在 Smart DAG 生成失败后理解会话是否可继续，并可重试或关闭，不产生本地假成功。
4. 在 OpenAPI 详情看到可信的服务地址、端点数量和逐端点契约。
5. 在 Connection 退化后准确区分 Tool 的生命周期、历史测试结果和当前可调用性。

本设计不改变 ACTWEAVE 的核心安全边界：不绕过 Workspace 授权，不自动发布，不把 trial 当作 production execution，不回显 Secret，不用前端本地结果冒充持久化成功。

## 2. 目标用户与场景

| 用户 | 主要任务 | 与本单相关权限 |
|---|---|---|
| Workspace OWNER / ADMIN / EDITOR | 编辑 Workflow、发起 Smart DAG、管理 OpenAPI 与 Tool | VIEW、EDIT、TEST、PUBLISH、EXECUTE；OWNER 另有 DELETE，OWNER/ADMIN 另有 MANAGE |
| Workspace OPERATOR | Console 执行、连接/Tool 测试与运行诊断 | VIEW、TEST、EXECUTE；不能编辑 Workflow、Smart DAG 或 OpenAPI 导入 |
| Workspace VIEWER | 查看资产、状态和诊断 | 仅 VIEW；所有写操作保持隐藏或 Disabled，并由后端拒绝越权请求 |
| 平台支持/审计人员 | 通过 requestId / traceId 关联失败 | 只读取获授权 Workspace 或平台审计面，不获得额外业务写权限 |

## 3. 事实、假设与约束

### 3.1 已验证事实

1. 走查基线为 `main` commit `b85e2452e5431e9aa2f90910e85ca6bcf373dcb8`；修复分支当前基线为 `a40b2cc`，已包含报告与截图。
2. Workflow 当前事实链为 `WorkflowGraphDraft → WorkflowCompilation → CompiledExecutionPlan → WorkflowRevision`；trial 与 production execution 是两条不同路径。
3. Workflow 详情按钮调用 `loadEditorDraft`；当前实现存在 `loading / loaded / failed / stale` 内部状态，但真实 Chrome 中失败后详情关闭、编辑器未出现，用户只看到列表。
4. Console 使用 Agent Run + protocol event SSE；`run.failed`、`run.completed`、`run.cancelled` 是终态事实，前端另有 GET Run 校准路径。
5. Console 当前在模型运行前遍历 capability snapshot，并对每个 Tool 调用 `ResolveInvocation`；任一连接未就绪即可使整个 Agent 初始化失败。
6. Smart DAG 主路径是 `SmartGenerateSession + turns`，会话有 OPEN/CLOSED 状态，已有 GET 会话与关闭 API；turn 超时配置为 210 秒，失败时不会创建本地替代 Draft。
7. OpenAPI 后端按 endpoint 持久化并返回 `inputSchema / outputSchema / issues / ready`；前端详情目前把第一条 endpoint 契约作为顶部汇总。
8. OpenAPI 服务地址由 Connection 的 `domain + ":" + port + basePath` 拼接；当 domain 已含端口时会重复端口。
9. Tool 已有三类独立信号：生命周期、最近测试结果、Connection/运行可用性；真实页面曾把存在但异常的 Connection 显示为“连接缺失”。
10. Workspace 角色和后端 Action 授权已存在，本单不新增角色或越权例外。

### 3.2 当前假设

1. UX-01～07 均可在现有数据模型上修复；除非技术方案证明必要，默认不做破坏性数据库迁移。
2. “最近测试通过”是版本在某个历史时点的测试事实，不等于当前 Connection 仍健康。
3. Connection 验证失败不会自动撤销已发布 Release；是否保持此语义列为未决项 D5。
4. 历史 OpenAPI 记录可能同时存在“schema 合法为空”和“旧数据未持久化/加载失败”，产品必须区分，不能统一显示为合法空契约。
5. 本轮所有错误文案继续使用通用 requestId / traceId 关联机制，不展示凭据、请求头或原始 Secret。

### 3.3 硬约束

- 未获负责人明确确认前，不交 Knower、Canvas 或 Forge。
- 不创建子 Issue、Stage 或并行修复任务。
- 不自动 publish、activate、bind Agent 或触发 production execution。
- 不把不可用 Tool 静默当作成功调用。
- 不自动删除或覆盖历史 OpenAPI、Workflow、Run、Session、Release、测试记录。

## 4. 已冻结范围

### 已批准：方案 B，P1 全修 + 高价值 P2（UX-01～07）

| ID | 本轮产品结果 |
|---|---|
| UX-01 | Workflow 详情进入编辑器具备可见 Loading、成功进入、失败保留上下文与重试 |
| UX-02 | Console capability 解析改为不因未调用 Tool 的连接异常阻断纯文本任务 |
| UX-03 | Console 消息、Run、意图、输入区在 terminal event 后一致收敛 |
| UX-04 | Smart DAG 失败呈现阶段/可重试性/会话状态，并提供重试本轮与关闭会话 |
| UX-05 | OpenAPI 服务地址按规范化 URL 展示，不重复端口 |
| UX-06 | OpenAPI 详情按 endpoint 展示契约，并识别摘要/详情不一致 |
| UX-07 | Tool 准确区分 Connection 缺失、异常、停用、待迁移与当前可调用性 |

### 默认非目标

- UX-08：Provider / Connection Toast 的全局可行动性改造。
- UX-09：已发布 Tool 新增只读重测能力。
- UX-10：匿名登录页 refresh 401 Console 噪声。
- Workflow 编辑器高级节点能力扩展。
- Smart DAG 模型质量、Prompt 重写、自动 publish 或自动 bind。
- Tool 生命周期重构、连接自动修复、自动撤销 Release。
- 批量历史数据修复或破坏性迁移。
- 与报告无关的新功能和视觉重构。

## 5. 产品流程与状态

### 5.1 UX-01：Workflow 详情 → 编辑器

主流程：

1. 用户在 Workflow 列表打开详情。
2. 点击“编辑流程图”后，详情上下文不立即丢失；界面进入“正在加载最新 Draft 与 Compilation”状态。
3. 后端返回当前 Draft 与最新 Compilation：
   - Draft 是唯一可编辑事实；
   - Compilation 仅表示某个 Draft version 的编译结果；
   - 若 Compilation 对应旧 Draft version，标记“编译已过期”，不得用于 trial/publish。
4. 编辑器成功挂载后才关闭详情层，展示画布。
5. 用户保存 Draft 后，旧 Compilation/CompiledExecutionPlan 失效；必须重新 compile。
6. 只有当前 Draft 的 VALID Compilation 且 trial 成功后，才允许 publish 生成不可变 Revision。
7. production execution 只使用 active published Revision，不因打开或保存编辑器自动触发。

异常流程：

- Loading：按钮 Disabled，显示 Workflow 名称和“加载最新草稿”。
- Failed：详情保持或可恢复，显示用户语言、requestId 和“重试加载”；不得显示空白画布。
- Stale：用户切换 Workflow 或关闭界面后，旧响应不得覆盖新选择，也不弹误导错误。
- Empty：Workflow 存在但 Draft 缺失时显示“草稿不可用”，禁止 compile/trial/publish，并给刷新/返回入口。
- Permission denied：VIEWER/OPERATOR 不显示编辑入口；直接调用 EDIT API 返回 403，现有页面数据不丢失。

### 5.2 UX-02/03：Console capability 降级与终态收敛

主流程：

1. 用户选择 Workspace 与 Agent，发送消息。
2. 系统建立 Agent Run；capability snapshot 仍冻结本轮所见能力与版本。
3. 未被模型实际调用的 Tool 不做连接凭据预取，不阻止模型完成纯文本回答。
4. 当模型实际选择 Tool 时，系统再执行该 Tool 的 Connection / outbound identity /权限门禁。
5. Tool 可用则进入现有 Invocation Pipeline；不可用则该次 Tool 调用失败，返回稳定错误码、Tool 名称和可行动说明，不调用外部服务。
6. 任一 `run.completed / run.failed / run.cancelled` 到达后，Run 状态、意图状态、输入区和最后消息在同一收敛周期内一致。

终态映射：

| Run 事实 | 顶部状态 | 意图状态 | 输入区 | 允许动作 |
|---|---|---|---|---|
| PENDING | 排队中 | 识别中 | Disabled | 等待 |
| RUNNING | 执行中 | 识别中或执行中 | Disabled | 等待 |
| WAITING_CONFIRMATION | 待确认 | 待确认 | Disabled | 确认/拒绝 |
| SUCCEEDED | 已完成 | 已完成 | Enabled | 继续对话 |
| FAILED | 运行失败 | 未完成 | Enabled | 重试或继续输入 |
| CANCELLED | 已取消 | 未完成 | Enabled | 重新发起 |

异常与恢复：

- SSE 断开但 Run 未终态：按现有 Last-Event-ID 重连；重连预算耗尽后 GET Run 校准并显示“实时连接中断”。
- 错误消息已经持久化但 terminal frame 丢失：立即 GET Run；若仍未终态，再进行有界校准，不无限显示“执行中”。
- 重复 terminal frame：幂等处理，不重复消息，不把终态降级回 RUNNING。
- Tool 失败：保留用户消息、失败 Tool、错误码、requestId/traceId；Secret 与一次性 Token 不进入消息或审计载荷。

### 5.3 UX-04：Smart DAG 失败恢复

状态：

`IDLE → CREATING_SESSION → OPEN → GENERATING → SUCCEEDED | FAILED_RETRYABLE | FAILED_FINAL → CLOSED`

`GUARD_REJECTED` 是 OPEN 会话内的可修订结果：保留上一版合法 Draft，不视为本地成功，也不关闭会话。

终态失败要求：

- 显示失败发生在哪个可识别阶段：会话创建、模型调用、输出解析、Guard、Draft 持久化；无法识别时显示“未知阶段”而非猜测。
- 显示 requestId/traceId、错误码、`retryable` 和服务器认定的 sessionStatus。
- 保留本轮输入与上一版合法 Draft。
- OPEN + retryable：提供“重试本轮”和“关闭会话”。
- OPEN + non-retryable：提供“关闭会话”和“新建会话”。
- CLOSED：禁用继续发送，提供“新建会话”。
- 重试不得自动 publish；成功仅更新正式持久化 Draft version。
- “关闭会话”需要确认，但不删除 Draft、Turn 或审计事实。

执行中取消是否纳入本轮见未决项 D3。

### 5.4 UX-05/06：OpenAPI 地址与契约详情

地址：

- 页面展示一个规范化 HTTP(S) URL。
- 若 `domain/serviceBaseUrl` 已含端口，不重复拼接。
- 默认端口不强制隐藏；路径只拼一次，Query/Fragment 不进入运行 Base URL。
- 地址数据缺失时显示“未配置”，不得用其他 Workspace 的第一条 Connection 兜底。

详情：

1. 先显示导入摘要：总端点、可生成、问题端点。
2. 显示 endpoint 列表；默认选中第一条，用户切换 endpoint 后查看该 endpoint 的 request parameters、Body、response 和 issues。
3. 合法无参数/无 Body/无响应 schema 分别显示“该接口未声明……”，不显示成加载失败。
4. 摘要 `totalEndpoints > 0` 但 endpoint 列表为空时，显示“导入详情不完整”，附 requestId 与刷新/重新导入建议；禁用“生成 Tool 草稿”。
5. `readyEndpoints` 必须等于 endpoint 列表中 `ready=true` 的数量；不一致时标为数据异常，不把记录显示为完整 Ready。
6. 生成 Tool 时只提交用户选中且 ready、未生成、非认证基础设施的 endpoint；不得因为顶部第一条契约为空而误判全部 endpoint。

Loading/Empty/Error：

- Loading：详情骨架和“正在加载端点契约”，不先渲染 0 节点。
- Empty：仅在后端明确返回合法空数组且摘要同为 0 时显示“未解析到接口”。
- Error：保留详情框与记录摘要，提供重试；不得回退到列表对象伪造完整详情。

历史数据处理按已冻结决策 D4=A 执行。

### 5.5 UX-07：Connection 与 Tool 状态语义

Tool 详情固定分三层展示：

1. 生命周期：草稿 / 待配置 / 待发布 / 已发布 / 已停用。
2. 最近测试：未测试 / 测试通过 / 测试失败，显示时间；这是历史版本事实。
3. 当前可调用性：
   - AVAILABLE：连接存在且当前可用；
   - NEEDS_ATTENTION：连接存在，但未验证、验证失败或运行异常；
   - DISABLED：连接存在但被停用；
   - MIGRATION_REQUIRED：连接存在但身份模式待迁移；
   - MISSING：绑定 ID 为空，或当前 Workspace 连接目录加载成功后确实找不到实体；
   - LOADING/UNKNOWN：连接目录尚未完成或诊断不足，不得提前显示 MISSING。

推荐展示示例：

- `已发布 · 当前可调用`
- `已发布 · 当前不可调用（连接需处理）`
- `已发布 · 当前不可调用（连接已停用）`
- `已发布 · 当前不可调用（连接缺失）`
- `测试通过于 2026-07-25；当前连接状态已变化`

连接验证失败只更新 Connection 当前健康与 Tool availability 投影，不改写历史测试结果。按已冻结决策 D5=A，已发布 Release 保持 Published，但当前 availability 转为不可调用并由 Invocation Pipeline 阻断。

## 6. 权限、危险操作与安全

| 行为 | Action | 允许角色 | 不足权限表现 |
|---|---|---|---|
| 查看 Workflow/OpenAPI/Tool/Connection | VIEW | OWNER/ADMIN/EDITOR/OPERATOR/VIEWER | 页面不可见或 403 |
| 编辑 Workflow、Smart DAG turn、OpenAPI 导入 | EDIT | OWNER/ADMIN/EDITOR | 写按钮隐藏或 Disabled；API 403 |
| Connection 验证、Workflow trial | TEST | OWNER/ADMIN/EDITOR/OPERATOR | 测试入口隐藏或 Disabled；API 403 |
| Console 发送与 Tool/Workflow 执行 | EXECUTE | OWNER/ADMIN/EDITOR/OPERATOR | 输入区 Disabled；API 403 |
| Workflow publish | PUBLISH | OWNER/ADMIN/EDITOR | 发布入口隐藏或 Disabled；API 403 |

危险操作边界：

- Smart DAG 关闭会话只关闭继续 turn 的能力，不删除会话、Draft 或审计记录。
- 本轮不新增删除、自动发布、自动停用、凭据轮换或数据回填按钮。
- 请求失败的诊断只展示经过脱敏的 endpoint、阶段、稳定码、requestId/traceId。
- Connection 当前异常不得诱导用户在 Tool 页面粘贴 Secret；修复凭据仍走既有受控编辑流程。

## 7. 数据、API、审计与兼容影响

### 7.1 数据与 API 结果要求

- Workflow：复用现有 Draft、Compilation、Revision 和 ETag/lockVersion；不创建旁路草稿。
- Console capability：capability snapshot 需能表达“目录可见”和“调用时可用性”差异；具体字段由技术方案定义，但不得预取 Secret。
- Console terminal：protocol terminal event 仍为首要事实；GET Run 是丢帧/断流校准，不创建第二套状态。
- Smart DAG 错误响应需提供或可推导：`stage`、`retryable`、`sessionStatus`、`requestId/traceId`；保持现有 `guardReport`。
- OpenAPI：详情以 endpoint DTO 为事实；前端不得用第一条 endpoint 冒充全部契约。
- Tool availability：可由 Tool + 当前 Workspace Connection 投影获得；若后端已有权威 availability DTO，前端优先使用权威值。

### 7.2 审计

必须可关联：

- Workflow Draft load failure 与 retry：workspaceId、workflowId、requestId，不记录图全文。
- Console Tool invocation gate failure：runId、capabilityId/releaseId、connectionId、错误码、traceId，不记录 Secret。
- Smart DAG turn failure/retry/close：sessionId、turnId、stage、result、traceId、durationMs，不记录 Prompt 全文。
- Connection verify 与 Tool availability 变化：保留现有验证审计，并能识别受影响 Tool 数量。
- OpenAPI 详情一致性错误是读取诊断，不应修改导入记录；若未来执行修复/重解析，必须单独审计。

### 7.3 兼容性与数据保留

- 不改变 AAP 外部公共契约。
- 不删除历史 Run、SmartGenerateSession、Turn、OpenAPI Import、endpoint schema、Tool test、Release 或 Revision。
- 新增错误字段必须保持旧客户端仍可读取原有 `error.code/message`。
- 若需要 schema migration，必须可回滚且不得把未知历史空 schema 推断成合法数据。

## 8. 依赖与风险

### 8.1 依赖

- Workflow store/API 能返回当前 Draft 与 latest Compilation。
- Console protocol event、Run GET 与 capability invocation pipeline。
- Smart DAG session/turn/close 服务及标准错误映射。
- OpenAPI import endpoint repository 和 detail API。
- Connection catalog、Tool binding 与 Workspace 授权。
- 真实 Chrome E2E 环境；Connection/Console 工具调用场景需要一个可控的可用服务和一个明确不可用服务。

### 8.2 风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| 将 Tool 解析延迟到 invocation 后改变模型可见工具行为 | 文本任务恢复，但工具选择错误可能后移 | 固定 snapshot，结构化 unavailable 原因，增加纯文本与工具调用两类测试 |
| terminal SSE 与 GET Run 竞态 | 终态被旧 RUNNING 覆盖 | 终态单调，不允许 GET 降级 terminal |
| Smart DAG “重试本轮”产生重复 Draft | 多次点击或超时重试重复写 | 按 session/turn/idempotency 设计；按钮防重 |
| 历史 OpenAPI 数据不完整 | 无法仅靠前端恢复契约 | 明确区分加载错误、合法空 schema、历史缺失；不自动猜测 |
| Connection 异常与 Published 生命周期混淆 | 用户误以为已下线或仍可调用 | 生命周期、测试、availability 三层固定展示 |
| 一次纳入 UX-01～10 | 回归面和交付周期显著扩大 | 推荐 UX-01～07，UX-08～10 后续独立确认 |

## 9. Given / When / Then 验收标准

### AC-01 Workflow 成功进入编辑器

- Given 用户有 EDIT 权限，Workflow 存在可读 Draft
- When 从详情点击“编辑流程图”
- Then 立即显示 Loading，并在成功后进入对应 Workflow 的编辑器
- And 画布来自后端 Draft，不是默认空图
- And 未自动 compile、trial、publish 或 production execute

### AC-02 Workflow 加载失败可恢复

- Given Draft GET 返回 4xx/5xx 或网络错误
- When 用户点击“编辑流程图”
- Then 保留可识别的 Workflow 上下文，显示 requestId 和“重试加载”
- And 不显示空白画布，不静默回列表
- And 重试成功后进入编辑器

### AC-03 Workflow stale 与权限

- Given 用户连续切换两个 Workflow，前一请求较晚返回
- When 后一 Workflow 已成为当前选择
- Then 前一响应不覆盖当前画布
- And OPERATOR/VIEWER 看不到编辑入口，直接调用 EDIT API 得到 403

### AC-04 Console 纯文本不被无关 Tool 阻断

- Given Agent 绑定至少一个 Connection 不可用的 Tool，模型服务可用
- When 用户发送明确不需要工具的纯文本请求
- Then Agent Run 可完成文本响应
- And 未对该 Tool 发起外部调用或凭据解析

### AC-05 Console 实际 Tool 调用仍受门禁

- Given 同一 Agent 的目标 Tool Connection 不可用
- When 模型实际选择该 Tool
- Then 该 Tool invocation 在外部请求前失败
- And 返回稳定错误码、Tool/Connection 可行动说明和 requestId/traceId
- And 不泄露 Secret，不把调用标为成功

### AC-06 Console 终态一致

- Given Run 已提交 `run.failed`
- When 前端收到 terminal frame，或发现错误消息后通过 GET 校准到 FAILED
- Then 5 秒内顶部显示“运行失败”、意图显示“未完成”、输入恢复 Enabled
- And 刷新页面后仍为同一终态
- And 重复 terminal frame 不重复消息，旧 RUNNING GET 不得降级终态

### AC-07 Smart DAG 可重试失败

- Given OPEN 会话中的 turn 在模型/解析/Guard/持久化阶段失败且 `retryable=true`
- When 失败返回
- Then 页面保留输入和上一版合法 Draft
- And 显示阶段、错误码、requestId/traceId、会话仍 OPEN
- And 提供“重试本轮”和“关闭会话”
- And 重试成功只生成新 Draft version，不自动 publish

### AC-08 Smart DAG 不可继续与关闭

- Given 会话已 CLOSED 或服务端返回不可重试失败
- When 用户查看失败结果
- Then 继续发送 Disabled
- And 页面提供新建/关闭会话的正确动作
- And 关闭会话不删除 Draft、Turn 或审计记录

### AC-09 OpenAPI 地址规范化

- Given Connection domain/service URL 已为 `http://127.0.0.1:18080`
- When 打开导入详情
- Then 服务地址只出现一个 `:18080`
- And basePath 只拼接一次
- And 无绑定 Connection 时显示“未配置”，不兜底到其他连接

### AC-10 OpenAPI endpoint 与契约一致

- Given 导入摘要为 8 个 endpoint、8 个 ready，detail API 返回 8 条 endpoint
- When 打开详情
- Then 显示 8 条接口明细
- And 切换每条接口可查看其 request parameters、Body、response、issues
- And ready 数与列表计算一致

### AC-11 OpenAPI 合法空契约与异常缺失

- Given 某 endpoint 明确没有 request Body
- When 查看该 endpoint
- Then 显示“该接口未声明请求体”
- But Given 摘要 endpoint 数大于 0 而 detail 列表为空
- When 查看详情
- Then 显示“导入详情不完整”、requestId 与恢复建议，并禁用生成 Tool

### AC-12 Tool 状态语义

- Given Tool 为 Published、最近测试通过，绑定 Connection 存在但状态为 ERROR/Needs attention
- When 查看 Tool 列表和详情
- Then 显示“已发布 · 当前不可调用（连接需处理）”
- And 显示历史“测试通过”及时间
- And 不显示“连接缺失”

### AC-13 Connection 加载与真正缺失

- Given Connection catalog 仍在 Loading
- When Tool 先渲染
- Then 当前可调用性为 Loading/Unknown，不显示 MISSING
- But Given目录加载成功且绑定 ID 对应实体确实不存在
- Then 显示“连接缺失”，并给出修复绑定入口

### AC-14 权限与安全回归

- Given VIEWER
- When 浏览受影响详情页
- Then 可读取获授权信息，但所有 EDIT/TEST/PUBLISH/EXECUTE 动作不可用
- And 直接调用相应 API 返回 403
- And 所有错误、审计、截图与日志均不包含 Secret 或一次性 Token

### AC-15 真实 Chrome 闭环回归

- Given Sentinel 使用真实 Chrome 和可控测试数据
- When 回归本轮确认范围
- Then UX-01～范围上限的每条 AC 均有截图/视频或可复现日志证据
- And Workflow 至少完成“编辑 → 保存 Draft → compile → trial → publish”，production execution 仅在验收明确授权时执行
- And Console 同时覆盖纯文本成功、不可用 Tool 调用失败、terminal 丢帧校准
- And Smart DAG 覆盖成功、可重试失败、关闭会话

## 10. 已冻结决策

负责人于 Issue 评论 `6c78f26c-da72-4115-9a31-c7d3575322ea` 明确批准 v0.1。v0.1 对 D1～D5 均给出唯一推荐项，因此本版将对应推荐项冻结为批准结论；当前无未决项。

### D1：本轮修复范围

| 选项 | 内容 | 影响 |
|---|---|---|
| A | 仅 UX-01～04（P1） | 周期最短；OpenAPI 与 Tool 状态矛盾继续存在 |
| **B（已批准）** | UX-01～07（P1 + 高价值 P2） | 覆盖核心闭环和跨资产一致性；回归面可控 |
| C | UX-01～10 全量 | 一次清完报告；需新增 Toast、只读重测、登录噪声范围，周期与风险最高 |

### D2：不可用 Tool 在 Console 的运行时策略

| 选项 | 内容 | 影响 |
|---|---|---|
| **A（已批准）** | Tool 仍在冻结 snapshot；连接/身份解析延迟到实际 invocation，未调用不阻断 | 最符合报告预期；需调整 runtime Tool wrapper 与测试 |
| B | 运行前把不可用 Tool 从模型可调用集合排除，UI 提示能力降级 | 实现可能更简单；用户要求该 Tool 时模型无法发起结构化调用 |
| C | 保持当前全量预检，任一不可用即阻断 Run | 安全最保守，但纯文本问题不解决，不接受 |

### D3：Smart DAG 是否纳入“执行中取消”

| 选项 | 内容 | 影响 |
|---|---|---|
| **A（已批准）** | 本轮做终态失败阶段、重试本轮、关闭/新建会话；不新增 in-flight cancel | 最小修复 UX-04，复用现有 API；最长仍可能等待既有超时 |
| B | 同时新增服务端 turn cancel 与真实阶段进度 | 体验最佳；新增 API、并发/幂等/模型取消与审计范围 |
| C | 仅浏览器 abort，不取消服务端任务 | UI 看似取消但后端仍可能写 Draft，语义危险，不推荐 |

### D4：历史 OpenAPI 详情缺失的处理

| 选项 | 内容 | 影响 |
|---|---|---|
| **A（已批准）** | 不自动回填；先正确展示现存 endpoint，历史确实缺失时标记异常并引导重新导入 | 无隐式数据写入，风险最低；旧记录可能需人工重新导入 |
| B | 读取详情时从保留原文自动重解析并持久化修复 | 用户无感；读取产生写副作用，需幂等、版本与审计设计 |
| C | 发布一次批量迁移/修复任务 | 可统一修复；交付与回滚风险最高，超出最小前端缺陷修复 |

### D5：Connection 退化后 Published Tool 的业务语义

| 选项 | 内容 | 影响 |
|---|---|---|
| **A（已批准）** | Release 保持 Published，但 availability 变为“当前不可调用”，Invocation Pipeline 强制阻断 | 保留版本事实和绑定；状态最清晰，无自动生命周期副作用 |
| B | 自动把 Tool/Release 设为 Disabled 或撤销发布 | 强一致但破坏性强，影响 Agent Binding、回滚和审计 |
| C | 仅警告，仍允许调用 | 可能反复失败或绕过身份门禁，不推荐 |

## 11. 确认记录

- v0.1：2026-07-25，Atlas 基于走查报告、README、现有页面、API、领域对象与测试形成。
- v1.0：2026-07-25，负责人批准 v0.1 后冻结为正式版；无范围扩张。
- 负责人确认原文位置：Issue 评论 `6c78f26c-da72-4115-9a31-c7d3575322ea`，原文“批准 v0.1”。
- 已解决：D1=B、D2=A、D3=A、D4=A、D5=A；当前无未决项。
- 已冻结范围：UX-01～07。
- 已冻结非目标：UX-08～10、Workflow 高级节点扩展、Smart DAG 自动 publish/bind、Tool 生命周期重构、连接自动修复/Release 自动撤销、批量历史数据修复、无关新功能与视觉重构。

## 12. 交给 Knower 的输入

1. 以本 v1.0、走查报告及截图证据为唯一产品输入，技术方案不得扩大至 UX-08～10。
2. 按 D2=A 设计 Console runtime：capability snapshot 继续冻结，Connection/outbound identity 解析延迟到实际 invocation；必须证明未调用 Tool 不触发 Secret/连接解析，实际调用仍在外部请求前受门禁。
3. 按 D3=A 设计 Smart DAG：复用现有 session/turn/close API，补终态阶段、retryable、sessionStatus、重试本轮与关闭/新建；本轮不新增 in-flight cancel。
4. 按 D4=A 设计 OpenAPI：不自动回填历史数据；正确展示现存 endpoint，确实缺失时标异常、禁用生成并引导重新导入。
5. 按 D5=A 设计 Tool availability：Published 生命周期不变，Connection 异常时显示当前不可调用并由 Invocation Pipeline 阻断。
6. 技术方案须覆盖前后端契约、竞态/幂等、错误映射、审计脱敏、权限矩阵、兼容性与测试；若发现必须新增数据迁移、修改 AAP 公共契约或改变已冻结语义，立即退回 Atlas 重新确认。
7. implementation checklist 必须逐条映射 AC-01～AC-15，并保留真实 Chrome 整体回归输入。
