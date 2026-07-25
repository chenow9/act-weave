# ZKL-56 PM E2E UX-01～07 修复：技术方案

| 字段 | 值 |
|---|---|
| Issue | ZKL-56 / `6563b563-60d1-4da7-9e90-eb293454187d` |
| 版本 | v0.2 |
| 状态 | Draft / Awaiting Approval |
| 日期 | 2026-07-26 |
| 工作分支 | `fix/zkl-56-pm-e2e-ux-fixes` |
| 产品输入 | `docs/design/zkl-56-pm-e2e-ux-fixes-product-design.md` v1.0 / Approved |
| UI 输入 | `docs/design/zkl-56-pm-e2e-ux-fixes-ui-design.md` UI v0.1 / Ready for Knower merge |
| 走查输入 | `docs/verification/pm-e2e-ux-report-2026-07-25.md` |
| 冻结范围 | UX-01～07；AC-01～AC-15 |

## 修订记录

| 版本 | 日期 | 状态 | 说明 |
|---|---|---|---|
| v0.1 | 2026-07-25 | Awaiting Approval | 基于已批准产品设计、真实代码/API/测试和走查证据形成首版技术方案 |
| v0.2 | 2026-07-26 | Awaiting Approval | 并入 Canvas UI v0.1 的页面态、关键文案、恢复动作、组件边界、可访问性与窄屏输入；不改变 T1～T10 推荐、冻结语义或 AC |

## 0. 结论摘要与批准边界

### 0.1 推荐结论

本方案推荐在现有 Console、Workflow、Smart DAG、OpenAPI Import 和 Tool 管理边界内修复，不新增数据库迁移，不修改 AAP 外部公共路径、鉴权、OpenAPI 或 SDK 签名，不自动回填历史 OpenAPI 数据，不改变 Tool/Workflow 发布生命周期。

核心方案：

1. Workflow 编辑入口继续复用现有 Draft 与 Readiness API，前端并行加载；加载完成前保留详情上下文，失败不再写入默认空图。
2. Agent capability snapshot 仍完整暴露给模型，但 Connection/identity resolution 从 Run 构建期延迟到模型实际选择 Tool 后；解析结果仍先过确认、权限、schema、限流、幂等与凭据注入边界。
3. Run 失败持久化后补齐幂等 `run.failed` protocol event；前端在错误 item、终态帧或流重试耗尽时，用有界 GET 校准收敛到持久终态。
4. Smart DAG 引入稳定失败阶段/错误码、现有 `lock_version` 乐观并发、跨实例 PostgreSQL advisory lock，以及短事务 Draft/Session/Turn Unit of Work；不新增表或列。
5. OpenAPI 详情由服务端返回实时完整性投影，生成服务端再次校验；前端按 endpoint 展示和显式选择，URL 使用单一规范化函数。
6. Tool 生命周期、历史测试和当前可调用性保持三个正交维度；后端以批量只读摘要返回真实历史测试，当前可调用性由 workspace-scoped Connection catalog 加载状态派生，实际调用仍以 Invocation Resolver 为安全权威。
7. 前端权限门禁扩展为与后端 `VIEW/EDIT/TEST/PUBLISH/EXECUTE/MANAGE/DELETE` 同构并 fail closed；后端 RBAC 不放宽。
8. Canvas UI v0.1 作为 §10 的呈现输入：复用现有 modal、status pill、Copilot 与管理页组件，采用 T10=A 的最小原位结构，不新增路由、wizard、恢复中心或设计系统。

### 0.2 本版不等于实施授权

本文件仍有 §14 的 T1～T10 待负责人逐项确认。负责人明确批准当前版本前：

- 不生成 implementation checklist；
- 不交 Forge；
- 不修改生产代码；
- 不创建 Issue、子 Issue 或 Stage。

若选择新增迁移、改变产品冻结 D1～D5、扩大 UX-08～10、修改 AAP 公共面，须先返回 Atlas 与负责人重新确认产品范围。

## 1. 现状证据

### 1.1 事实来源

事实优先级如下：

1. 当前分支真实实现、数据库迁移和测试；
2. 已批准产品设计 v1.0 与负责人确认评论；
3. Canvas UI v0.1（仅作为不改变冻结产品语义的交互与呈现输入）；
4. 2026-07-25 真实 Chrome + 本地全栈走查报告和截图；
5. `README.md`、相关 `docs/design`、`docs/runbooks` 与 `docs/openapi`。

进入设计时分支为 `fix/zkl-56-pm-e2e-ux-fixes`，仅存在未跟踪的运行上下文 `.agent_context/` 与 `AGENTS.md`；本方案不触碰它们。

### 1.2 缺陷根因与代码锚点

| UX | 已验证事实 | 根因代码 |
|---|---|---|
| UX-01 | 点击“编辑流程图”后详情立即关闭；Draft 请求失败时用户只看到列表，且失败分支写入默认空图 | `frontend/src/views/WorkflowView.vue` 的 `openWorkflowEditor`、`loadEditorDraft`；`frontend/src/stores/workflow.ts` 的 `loadWorkflowDraft` |
| UX-02 | Run 构建模型 Tool 列表时对每个 capability 调用 `ResolveInvocation`；一个无关 Tool 的 Connection 不可用会在模型回答前终止 Run | `backend/internal/chatruntimebridge/bridge.go` 的 `buildPipelineTools` |
| UX-03 | 成功路径记录 `run.completed`；失败路径持久化 FAILED 消息和 Run 后直接返回 cause，没有记录 `ProtocolRecordRunFailed` | `backend/internal/chatruntimebridge/bridge.go` 的 `completeRun`、`failRun` |
| UX-03 | SSE 的 `item.failed` 只更新消息，不更新 Run；404/401/网络重试耗尽后直接 return，没有 GET 校准或“实时连接中断”状态 | `frontend/src/services/run-event-stream.ts`、`frontend/src/stores/chat.ts` |
| UX-04 | Smart DAG 除 Guard 外把错误压成通用 `FAILED`；HTTP 通用 500 不返回 stage、sessionStatus、turnId/generationId；页面只有 toast | `backend/internal/smartdag/session.go`、`turn.go`、`platform_graph_model.go`、`backend/internal/transport/http/generate_session.go`、`frontend/src/stores/smartdag.ts`、`frontend/src/views/SmartDagView.vue` |
| UX-04 | `NextTurnIndex` 与 `CreateTurn` 分离；同一 Session 并发可竞争。Draft 写入与 Turn/Session 写入不在同一事务，存在 Draft 已变但 Turn 失败的窗口 | `backend/internal/smartdag/session_repository.go`、`session.go`、`turn.go`；迁移 `000059_workflow_generate_sessions.up.sql` |
| UX-05 | Connection DTO 已把完整 `serviceBaseUrl` 放进 `domain`，页面又追加派生的 `port` 和 `basePath`；无绑定时还回退到 catalog 第一条 Connection | `frontend/src/stores/integration.ts` 的 `connectionFromDTO`；`frontend/src/views/OpenAPIImportsView.vue` 的 `selectedConnection`、`connectionAddress` |
| UX-06 | 详情 DTO 包含每个 endpoint，但 store 又把第一个 endpoint 投影为顶层 request/response contract；页面同时展示首条总览和全部卡片，没有 active endpoint 与完整性状态 | `frontend/src/stores/integration.ts` 的 `importFromDTO`；`frontend/src/views/OpenAPIImportsView.vue` |
| UX-06 | Import 完成时 endpoint 与摘要计数同事务写入；但 GET 不返回实时完整性，Generate 只校验客户端提交的 endpoint 是否 ready，未校验摘要与实际列表一致 | `backend/internal/openapiimport/repository.go`、`generation.go`、`backend/internal/transport/http/tool_openapi.go` |
| UX-07 | 找不到 Connection 实体时立即显示“连接缺失”，无法区分 catalog 尚未加载、加载失败和真实缺失；Published 描述直接等同“可调用” | `frontend/src/utils/tool-governance.ts`、`frontend/src/stores/integration.ts`、`frontend/src/views/ToolsView.vue` |

### 1.3 必须保留的现有契约

- Workflow 真相仍是 PostgreSQL 中的 Draft、Compilation、CompiledExecutionPlan、Revision；保存 Draft 会使旧 Compilation 失效，但不改变 active Revision。
- Workflow Readiness 已在服务端读取 latest Compilation，并提供 `compilationId/current/valid` 与从 compilation issues 派生的 blockers；没有独立 GET Compilation 详情路由。
- `capability-snapshot.v1` 已固定 capability/release/callable/schema/risk/confirmation/connection ID，不含 Secret。
- `execution.InvocationPipeline` 已负责 authz、schema、confirmation、rate limit、idempotency、非敏感 invocation record、受保护凭据注入与执行；不得在 Eino adapter 平行实现。
- Protocol event 是实时唯一权威语义；Console 与 AAP 共用 `run.*` / `item.*` wire shape，禁止新增 Console 专用事件方言或 protocol/legacy 双 SoT。
- Smart Generate Session 独立于 ChatSession/AAP Conversation；Session 状态只有 `OPEN/CLOSED`，Turn 持久状态只有 `SUCCEEDED/GUARD_REJECTED/FAILED`。
- `workflow_generate_sessions.lock_version`、`workflow_generate_turns.error_code`、唯一 `(session_id, turn_index)` 和唯一 `generation_id` 已存在。
- OpenAPI Import 完成写入本身已是事务性的；D4 明确禁止自动重解析/回填历史记录。
- Connection 只有 `VERIFIED` 且不处于 `MIGRATION_REQUIRED` 等阻断状态时，Invocation Resolver 才允许执行；解析不返回 Token 明文。
- 后端 Workspace RBAC 为：OWNER 全部；ADMIN 除 DELETE 外全部；EDITOR 为 VIEW/EDIT/TEST/PUBLISH/EXECUTE；OPERATOR 为 VIEW/TEST/EXECUTE；VIEWER 仅 VIEW。

## 2. 目标、非目标与不可变约束

### 2.1 目标

1. 逐条满足 AC-01～AC-15。
2. 失败时保留可识别资源上下文、最后一版合法数据和可行动恢复入口。
3. 所有终态均可由持久事实恢复；SSE 丢帧不再让 UI 永久停在 RUNNING。
4. 延迟 Connection/identity resolution 时不降低权限、确认、幂等、Secret 和审计边界。
5. Smart DAG 的成功 Draft、Session 绑定和成功 Turn 要么一起提交，要么全部回滚。
6. OpenAPI UI 展示与实际 endpoint 行、ready 状态和生成门禁一致。
7. Published 生命周期不被 Connection 当前状态隐式改写。

### 2.2 非目标

- UX-08～10。
- Workflow 高级节点、自动 compile/trial/publish/production execute。
- Smart DAG in-flight cancel、真实阶段流式进度、自动 publish/bind。
- 新的 Tool 生命周期或 Connection 自动修复。
- 历史 OpenAPI 自动回填、读取时重解析或批量迁移。
- AAP 路径、鉴权、OpenAPI、SDK 公共签名或事件 schema 的 breaking change。
- Secret/Token 可见性、调试输出或日志范围扩大。
- 无关页面的视觉重构。

### 2.3 不可变约束

- 所有后端 mutation 继续强制服务端 RBAC；前端门禁只改善体验，不是授权边界。
- 不把 Token、Secret、credential locator、完整 prompt、业务响应体写入错误、日志、指标或截图。
- Trial 与 production execution 保持分离；本轮 Chrome 验收除非负责人另行授权，不触发 production side effect。
- 失败不写本地“假草稿”，不把 Tool resolution failure 标为调用成功。
- 关闭 Smart DAG Session 不删除 Draft、Turn 或审计。

## 3. 推荐架构与模块边界

### 3.1 总体数据流

```text
Workflow detail
  └─ parallel GET Draft + Readiness
       ├─ success + request still current → atomic UI handoff to editor
       └─ error → keep detail + retry metadata

Agent Run
  └─ build model Tool metadata from capability snapshot only
       └─ model actually selects Tool
            ├─ resolve pinned release + current Connection/identity
            ├─ confirmation if required
            └─ existing InvocationPipeline

Run terminal
  └─ persist Run + assistant message
       └─ append deterministic protocol terminal event
            └─ frontend protocol reducer + bounded GET calibration

Smart DAG turn
  └─ session lock + lock_version claim
       └─ model / parse / guard outside DB transaction
            ├─ failure → persist typed failed turn
            └─ success → short Draft + Session + Turn transaction

OpenAPI detail
  └─ import + endpoint rows + computed integrity
       └─ active endpoint contract / explicit selection
            └─ generation rechecks integrity inside transaction
```

### 3.2 改动边界

| 模块 | 责任 | 允许变化 | 禁止变化 |
|---|---|---|---|
| `frontend/src/views/WorkflowView.vue` | 编辑入口状态与 UI handoff | Loading/Error/Retry/stale/permission | 默认空图伪装失败 |
| `frontend/src/stores/workflow.ts` | Editor context loader | 并行 Draft + Readiness、request token | 新前端 Draft SoT |
| `backend/internal/chatruntimebridge` | capability Tool 构建、Run terminal | lazy resolution、failed protocol record | 绕过 InvocationPipeline |
| `backend/internal/einoruntime/tool_adapter.go` | 模型实际 Tool call adapter | resolve-at-call、HITL metadata | Secret 获取、resume 重复执行 |
| `backend/internal/chatruntime` / `protocolevent` | protocol terminal projection | deterministic terminal event id、幂等 conflict | 新事件类型 |
| `frontend/src/services/run-event-stream.ts`、`stores/chat.ts` | reducer、SSE、GET calibration | monotonic terminal、stream health | legacy 事件成为新 SoT |
| `backend/internal/smartdag` | session/turn orchestration | typed failure、session lock、UoW | 新 Session 状态、自动 publish |
| `backend/internal/transport/http/generate_session.go` | Console Smart DAG DTO | additive fields/error details | AAP 变化 |
| `backend/internal/openapiimport` | detail integrity、generation gate | 读取时投影、事务内复核 | 自动回填/重解析 |
| `frontend/src/stores/integration.ts`、OpenAPI/Tools views | detail state、URL、availability | scoped state/view model | 跨 workspace fallback |
| `frontend/src/stores/workspaces.ts` | UI permission projection | 与后端 actions 同构 | 放宽后端权限 |

## 4. 详细设计

### 4.1 UX-01：Workflow 详情到编辑器

#### 4.1.1 前端状态机

新增页面级 `editorLoad`：

```ts
type EditorLoadStatus = "IDLE" | "LOADING" | "FAILED" | "READY";

interface EditorLoadState {
  workflowId: string;
  requestToken: number;
  status: EditorLoadStatus;
  requestId?: string;
  traceId?: string;
  errorCode?: string;
  message?: string;
}
```

状态规则：

```text
DETAIL_READY
  → click edit
  → LOADING (detail remains visible, actions disabled)
  → READY (commit context, mount editor, then close detail)
  → FAILED (detail remains visible, retry / close)
```

- `openWorkflowEditor` 先校验当前用户 `EDIT`，再递增 `requestToken`。
- `loadWorkflowEditorContext(workflowId)` 用 `Promise.all` 并行请求现有 Draft 与 Readiness。
- Readiness 的 `compilationId/current/valid/blockers` 即 latest Compilation 的编辑器所需投影；本轮不新增 GET Compilation 路由。
- 只有 `requestToken` 与当前目标 Workflow 均匹配时，才能一次性提交 `activeDraft`、readiness、selected Workflow 和 editor visible。
- 成功时先挂载 editor，再在下一次 DOM flush 关闭详情；避免同层 modal z-index 竞争。
- 失败时不清空上一份合法 editor state，不生成默认 Start/End 空图，不切回列表；在详情内显示安全错误、requestId、重试。
- 关闭详情或切换 Workflow 可 best-effort abort 请求，但正确性只依赖 request token，不能依赖网络取消。

#### 4.1.2 API 与并发

- 复用：
  - `GET /api/v1/workspaces/{wid}/workflows/{id}/draft`
  - `GET /api/v1/workspaces/{wid}/workflows/{id}/readiness`
- 不修改响应契约，不新增聚合 API。
- Draft ETag 继续用于后续 PUT 的乐观并发。
- stale 请求即使成功也不得写 store 或画布。

#### 4.1.3 权限

- VIEWER/OPERATOR 可查看详情但不渲染“编辑流程图”入口。
- 后端 GET 仍为 VIEW；PUT Draft/compile 等继续按现有 EDIT/TEST/PUBLISH。
- 直接调用无权限 mutation 仍由后端返回 403。

### 4.2 UX-02：实际 Tool 调用时才解析 Connection/identity

#### 4.2.1 构建期

`Bridge.buildPipelineTools` 只做：

1. 解析 `capability-snapshot.v1`；
2. 校验 TOOL/WORKFLOW kind、callable name 和 input schema；
3. 生成 Eino `ToolInfo`；
4. 把固定 workspace/capability/release/connection/risk/confirmation/principal IDs 交给 lazy adapter。

禁止在这一阶段调用 `ResolveInvocation`。因此纯文本回答不会读取目标 Connection、解析身份或触发外部调用。

#### 4.2.2 实际 Tool call

`PipelineTool.InvokableRun` 顺序调整为：

1. 先读取 Eino interrupt/resume state；已 resume 且有平台结果时直接返回，不 resolve、不 invoke。
2. 首次实际 Tool call 生成/复用 invocation ID，规范化模型参数。
3. 调用现有 `ToolInvoker.ResolveInvocation` 一次，校验 pinned workspace/capability/release 与 Connection readiness。
4. 复用 Invocation Pipeline 的同一 schema validator，用 resolved snapshot 的 input schema 校验模型参数；非法参数在确认前以 `TOOL_ARGS_INVALID` 失败，禁止形成第二套校验语义。
5. `needsConfirmation = snapshot.requiresConfirmation || resolved.requiresConfirmation`。
6. 需要确认时，把参数和**非敏感 resolved snapshot**交给进程内 pending hook，随后 StatefulInterrupt；不执行外部调用。
7. 无需确认时调用现有 `InvokeResolved`。
8. resolution 失败时生成结构化 Tool error result，写失败 TOOL step/错误码，不创建成功 Invocation，不发业务 HTTP。

`ToolConfirmInterruptState` 继续保持 IDs-only；非敏感 resolved snapshot 只通过现有 confirmation resume snapshot 持久化，不写 gob、不含 Token/Secret。确认 dispatch 仍是唯一真实执行者，Eino resume 只读取 dispatch 结果，保证不重复调用。

#### 4.2.3 安全与错误

- Resolver 只生成非敏感 Connection/CredentialReference；真实 Token 获取/注入仍在 InvocationPipeline 的安全 callback 内。
- `OUTBOUND_IDENTITY_CONNECTION_NOT_READY`、`OUTBOUND_IDENTITY_MIGRATION_REQUIRED`、`OUTBOUND_CREDENTIAL_REQUIRED` 等稳定码继续复用。
- Tool step 对用户显示 Tool 名、Connection 名/修复入口和 requestId/traceId；不显示 Secret 名、Token、Broker body 或内部 locator。
- shared bridge 行为可同时改善 Console 与 AAP Run，但 AAP 公共 path/auth/frame/SDK 完全不变，并由 golden/contract test 锁定。

### 4.3 UX-03：Run 失败终态与前端校准

#### 4.3.1 后端 terminal projection

失败顺序：

```text
RecordAssistantResult(FAILED)
  → transaction commits Run FAILED + failed assistant message + session unlock/audit
  → reload finished Run
  → Record ProtocolRecordRunFailed
```

调整点：

- `failRun` 在 `RecordAssistantResult` 成功后不直接 return，必须读取 finished Run 和 result message，再调用 protocol recorder。
- chatruntime bridge 的 terminal event ID 使用确定性 namespace UUID，键为 `(runId, eventType)`；`run.completed/run.failed` 重入时 event conflict 视为已完成。既有 cancellation service 继续负责 `run.cancelled`。
- terminal item projection 对“item 已完成”也幂等，不重复创建消息或 ordinal。
- protocol append 失败不回滚已持久化 Run；记录结构化错误并让 GET 成为恢复路径。此次方案不引入 outbox/migration。
- `ensureRunNotLeftRunning` 仍只处理真正遗留 RUNNING，不制造第二个终态。

#### 4.3.2 前端 monotonic reducer

新增每 Run 的 `streamHealth`：

```ts
type RunStreamHealth =
  | "CONNECTING"
  | "HEALTHY"
  | "RECONNECTING"
  | "CALIBRATING"
  | "DEGRADED";
```

触发 GET calibration：

- `run.completed/run.failed/run.cancelled` 后校准持久 Run/steps；
- 收到 `item.failed` 或 failed assistant item，但尚无 terminal Run；
- 404/401/网络错误达到现有重试预算；
- stream EOF 且重连预算耗尽。

校准策略：

- 同一 Run singleflight，立即、约 1.5 秒、约 3.5 秒最多三次；总 deadline 5 秒，每次请求有短超时。
- GET 返回 terminal 后，terminal 成为吸收态，关闭 SSE，并加载 Session 消息以恢复漏掉的 failed assistant message。
- 旧 RUNNING/PENDING GET、迟到的 `run.started` 或低 sequence frame 不得覆盖 terminal。
- 重复 terminal frame 按 sequence/event/item ID 去重，不重复消息。
- `ChatExecutionView` 对 FAILED/CANCELLED 映射顶部终态和意图“未完成”，并恢复输入；不得继续显示“执行中/意图识别中”。
- 5 秒内 GET 仍非终态时不伪造 FAILED；保留真实 RUNNING/WAITING，标记 DEGRADED 并显示“实时状态中断，可刷新校准”，输入仍按服务端状态门禁。

### 4.4 UX-04：Smart DAG 失败恢复、并发与原子提交

#### 4.4.1 稳定失败模型

新增领域类型但不新增列：

```text
FailureStage:
  SESSION
  MODEL_CALL
  OUTPUT_PARSE
  GUARD
  DRAFT_PERSIST
  UNKNOWN
```

`TurnFailure` 包含：

- `Stage`
- stable `Code`
- `Retryable`
- safe public message
- wrapped internal cause（仅日志）

推荐映射见 §6.2。持久 Turn 继续使用现有 `status/error_code`；GET 时通过稳定 error code 派生 `failureStage/retryable`。历史通用 `FAILED` 映射为 `UNKNOWN/false`，不修改历史行。

#### 4.4.2 单 Turn 流程

```text
Authorize EDIT
  → acquire session-scoped pg_try_advisory_lock
  → load OPEN session
  → claim expected session lock_version
  → allocate turnId/generationId/turnIndex
  → gate + prompt + catalog + model + parse + guard (outside DB tx)
       ├─ typed failure → short tx insert failed Turn
       └─ guarded candidate
            → short Unit of Work tx:
                 reload Session FOR UPDATE
                 verify OPEN + claimed version
                 create/update Workflow Draft with CAS
                 bind first Workflow to Session if needed
                 insert SUCCEEDED Turn
                 commit
  → release advisory lock
```

要求：

- advisory lock 使用 workspace+session 派生的 64-bit key，`try` 失败立即返回 `SMART_DAG_TURN_IN_PROGRESS`，不排队占用 HTTP worker。
- lock 通过 dedicated DB connection 持有，context cancel/connection close 必须释放；模型请求仍有当前 210 秒上限。
- `expectedSessionLockVersion` 防止重复提交和丢响应后的盲重放。旧客户端暂可省略，服务端在 advisory lock 内读取当前版本以保持发布兼容；新前端始终发送。
- 请求开始时把客户端版本 `N` CAS claim 为 `N+1`；成功或可持久化失败的 terminal commit 再推进到 `N+2` 并把最终版本返回客户端。这样其他标签页在 in-flight 期间读取到的 `N+1`，也不能在锁释放后用过期上下文提交。
- 模型/parse/guard 不处于数据库事务内，避免长事务。
- success commit 是短事务；Draft、首次 Session workflow bind、成功 Turn 同成同败。
- existing Workflow Draft 的 `draftVersion/lockVersion/ETag` CAS 继续生效；若用户在模型生成期间手工修改 Draft，整个 Smart DAG commit 回滚并返回可重试 conflict，绝不覆盖新手工版本。
- failed turn 不改变 Draft。若连失败 Turn 都因数据库不可用而无法落库，HTTP 仍返回 stage/requestId/traceId，日志和指标记录 persist failure；不得谎称已写历史。
- Create Session 的模型门禁错误也使用同一公开分类（SESSION 或 MODEL_CALL），但未创建 Session 时不伪造 sessionId/turnId。

#### 4.4.3 显式重试与关闭

- “重试本轮”复用保留的 user message/feedback，但使用 GET 校准后的最新 session lock version，创建新的 turnId/generationId。
- 重试成功只创建下一 Draft version；不 compile、不 trial、不 publish。
- `close` 同样获取 session advisory lock并校验可选 `expectedSessionLockVersion`；in-flight 时返回 busy，不取消模型。
- CLOSED 后发送固定 409 `SESSION_CLOSED`，继续输入 Disabled；“新建会话”创建新 Session。
- 关闭不删除 Draft、Turn、promptHash 或审计。

#### 4.4.4 前端恢复卡

`smartdag` store 增加：

```ts
interface SmartDagFailureState {
  stage: FailureStage;
  code: string;
  retryable: boolean;
  requestId: string;
  traceId: string;
  sessionId?: string;
  sessionStatus?: "OPEN" | "CLOSED";
  sessionLockVersion?: number;
  turnId?: string;
  generationId?: string;
  message: string;
}
```

- 失败不再只有 toast；Copilot 面板内展示持久恢复卡。
- `OPEN + retryable`：显示“重试本轮”“关闭会话”。
- `OPEN + non-retryable`：按 code 显示修复配置/关闭/新建动作，不显示无效重试。
- `CLOSED`：输入和继续发送 Disabled，显示“新建会话”。
- 页面始终保留原输入、上一合法 Draft 和失败前画布。
- generating 时关闭按钮 Disabled，并明确“不支持执行中取消”。

### 4.5 UX-05/06：OpenAPI URL、endpoint 契约与完整性

#### 4.5.1 服务地址规范化

新增纯函数 `normalizeServiceBaseURL(connection)`：

1. 若 `protocolConfig.domain` 是合法 absolute HTTP(S) URL，则它是唯一来源；移除 query/fragment，规范 trailing slash，不再追加已派生的 port/basePath。
2. 仅对历史 host-only 值，才从 protocol/host/port/basePath 构造一次，并用 URL parser 合并 path segment。
3. 非 HTTP(S)、非法 URL 显示“配置异常”，不猜测。
4. Import 没有 `connectionId` 或 scoped catalog 已加载但找不到实体时显示“未配置”。
5. 禁止回退到 `serviceConnections[0]`，禁止跨 workspace catalog fallback。

#### 4.5.2 Detail API 完整性投影

扩展现有：

```http
GET /api/v1/workspaces/{wid}/openapi-imports/{id}
```

新增 additive 字段：

```json
{
  "import": {},
  "endpoints": [],
  "integrity": {
    "status": "COMPLETE",
    "expectedTotalEndpoints": 8,
    "actualTotalEndpoints": 8,
    "expectedReadyEndpoints": 8,
    "actualReadyEndpoints": 8,
    "issues": []
  },
  "requestId": "...",
  "traceId": "..."
}
```

完整性由当前数据库行实时计算，不写回：

- expected total/ready 与 actual count 一致；
- endpoint 必须有 id/method/path；
- input/output schema 必须是合法 JSON schema object；
- ready 行必须满足生成服务现有 schema/actionConfig 前置；
- 合法 `{type:"object", properties:{}}` 是“明确空契约”，不是缺失；
- 摘要大于 0 而实际列表为空为 `INCOMPLETE`；
- 现有历史通用异常不自动重解析、不修改 Import 或 endpoint。

#### 4.5.3 服务端生成门禁

`GenerationService.Generate` 在已有 Import/Provider `FOR UPDATE` 事务内再次调用同一完整性判定：

- `INCOMPLETE` 返回 409 `OPENAPI_IMPORT_INCOMPLETE`、`retryable=false`，零 Tool Draft 写入；
- 客户端 endpoint IDs 必须属于该 Import、ready、未生成、非认证基础设施；
- count、ready、schema 状态不信任前端；
- 现有 endpoint/Tool/link transaction 保持 all-or-nothing。

#### 4.5.4 前端详情模型

- 用户点击详情时先设置 selected import 和 `LOADING`，立即打开 modal skeleton；失败保持 modal 并显示 requestId/重试。
- 移除顶层“第一个 endpoint contract”投影。
- modal 使用 endpoint 列表 + active endpoint 契约区：
  - 列表显示 method/path/operationId/ready/issues/generated；
  - 点击 endpoint 切换 active contract；
  - request parameters 按 `x-actweave-location` 分 Path/Query/Header；
  - Body 与 response 分区展示；
  - 合法空 Body 显示“该接口未声明请求体”；
  - schema 缺失/非法显示“契约数据异常”，不能伪装为空。
- 每个 eligible endpoint 有显式 checkbox；默认选中全部 eligible，用户可取消或全选。active row 与生成选择互不混淆。
- `INCOMPLETE`、未加载、加载失败或零选择时禁用“生成 Tool 草稿”，展示恢复建议。

### 4.6 UX-07：Tool 三维状态与 Connection catalog

#### 4.6.1 Catalog 状态

`integration` store 新增 workspace-scoped：

```ts
type CatalogLoadStatus = "IDLE" | "LOADING" | "LOADED" | "ERROR";

toolConnectionCatalogStateByWorkspace: Record<
  string,
  { status: CatalogLoadStatus; errorCode?: string; requestId?: string }
>;
```

- `loadToolWorkspaceContext` 开始即设 LOADING，成功才写 LOADED，失败写 ERROR。
- `connectionForTool` 只查 `toolConnectionsByWorkspace[tool.workspaceId]`；不回退 active workspace/global catalog。
- force reload 保留旧实体用于稳定渲染，但 availability 在请求期间显示 LOADING。

#### 4.6.2 当前可调用性投影

```text
tool Disabled                                  → DISABLED
catalog IDLE/LOADING                           → LOADING
catalog ERROR                                  → UNKNOWN
catalog LOADED + binding entity absent         → MISSING
connection migrationState=MIGRATION_REQUIRED   → MIGRATION_REQUIRED
connection status=DISABLED                     → DISABLED
connection UNVERIFIED/ERROR/Needs attention    → NEEDS_ATTENTION
connection VERIFIED/Available                  → AVAILABLE
其他/无法解释                                  → UNKNOWN
```

`Expiring soon` 显示 NEEDS_ATTENTION 警告；最终能否执行仍由服务端 resolver 判断。

页面保持：

1. Lifecycle：Draft/Review/Tested/Published/Disabled；
2. 最近测试：通过/失败/未测试 + `testedAt`；
3. Availability：上述状态 + 修复入口。

#### 4.6.3 历史测试摘要

当前列表把 Published 在缺少 `lastTestResult` 时推断为“测试通过”，无法给出真实 `testedAt`。本方案取消该推断：

- 后端 Tool list/detail additive 返回当前相关 version 的 `latestTest` 安全摘要：
  - Published：active release 对应 version 的最新测试；
  - 未发布：当前 latest version 的最新测试；
  - 字段仅含 `status/testedAt/testedBy/errorCode`，不返回 request/response body。
- repository 使用 workspace 内批量查询，禁止列表 N+1。
- 有成功 TestRecord 才显示“测试通过”与时间；有失败记录显示“测试失败”；无记录显示“历史测试未知”。
- lifecycle `Published` 不能替代 TestRecord，也不因没有摘要而降级。

Published + 历史测试通过 + Connection ERROR 的主文案固定为：

> 已发布 · 当前不可调用（连接需处理）

不得显示“连接缺失”，不得自动 Disabled/撤销发布。MISSING 提供回到 Tool 编辑器重新绑定的入口。

### 4.7 共享前端权限投影

`WorkspaceAction` 扩展为与后端一致：

```text
VIEW | EDIT | TEST | PUBLISH | EXECUTE | MANAGE | DELETE
```

权限来源复用现有 Workspace owner + members API：

- owner 可由 Workspace DTO 直接确定；
- 其他成员先 `loadMembers`，加载中/失败时 mutation UI fail closed；
- `can(workspaceId, userId, action)` 使用与 `backend/internal/authz/workspace_policy.go` 相同矩阵，并用表驱动测试锁定。

本范围动作映射：

| 动作 | 前端 action | 后端仍为权威 |
|---|---|---|
| Workflow 进入编辑/保存 Draft、Smart DAG turn/close、OpenAPI import/generate | EDIT | 是 |
| Tool/Connection test/verify | TEST | 是 |
| Workflow/Tool publish | PUBLISH | 是 |
| Console send/Tool invoke/trial execute | EXECUTE 或现有 TEST 语义 | 是，按具体 route 现状 |
| 只读详情 | VIEW | 是 |
| 删除 OpenAPI Import/Workflow/Tool | DELETE | 是 |

按钮不可用时显示权限原因；不通过 CSS 隐藏来代替服务端授权。

## 5. 数据、迁移与持久化

### 5.1 数据库变化

推荐方案没有 schema migration。

复用：

- `workflow_generate_sessions.lock_version`
- `workflow_generate_turns.error_code`
- `workflow_generate_turns.status/guard_report/draft_version`
- `protocol_events.id` 唯一性
- OpenAPI Import/endpoint 与 Tool generation 现有事务和约束

### 5.2 Smart DAG 事务边界

新增代码级 `TurnCommitUnitOfWork`，在同一 `sql.Tx` 内调用 transaction-aware repository primitives：

- first turn：Workflow + Draft create、Session workflow bind、Turn insert；
- later turn：Draft CAS update、Turn insert；
- failure turn：Turn insert（不写 Draft）。

现有 repository 公共行为保持，抽取 `CreateInTransaction/UpdateDraftInTransaction/CreateTurnInTransaction/BindSessionInTransaction` 供 UoW 复用，避免复制 SQL 语义。

事务外只执行授权、读取上下文、模型调用、parse/guard；事务内禁止模型/网络调用。

### 5.3 历史兼容

- 历史 Smart DAG `error_code=FAILED` 只读映射 `UNKNOWN`，不 backfill。
- 历史 OpenAPI 行只计算 integrity，不重解析、不持久修复。
- 历史 Tool lifecycle/test 数据不改写。
- 已存在的随机 terminal events 不重写；新事件开始使用 deterministic ID。

## 6. API、错误与兼容

### 6.1 Console 内部 API additive 变化

| 路由 | 变化 | 兼容 |
|---|---|---|
| `POST .../workflow-generate-sessions/{sid}/turns` | request 可选 `expectedSessionLockVersion`; error details 新增 stage/session/turn fields | 旧请求暂可省略；新 FE 必传 |
| `GET .../workflow-generate-sessions/{sid}` | session 新增 `lockVersion`; turn 新增 `failureStage/retryable` | additive |
| `POST .../workflow-generate-sessions/{sid}:close` | request 可选 `expectedSessionLockVersion` | 旧 `{}` 继续接受 |
| `GET .../openapi-imports/{id}` | 新增 `integrity/requestId/traceId` | additive |
| `POST .../openapi-imports/{id}:generate-tools` | incomplete 时新增稳定 409 | intentional fail-closed |
| `GET .../tools`、`GET .../tools/{id}` | 每项新增安全 `latestTest` 摘要 | additive；批量读取 |
| Workflow Draft/Readiness | 无变化 | 完全兼容 |
| Console Run GET/SSE | 无 path/schema 变化；开始可靠提交既有 `run.failed` | 既有 protocol 契约 |

Smart DAG error detail 放进现有 `error.details`：

```json
{
  "error": {
    "code": "SMART_DAG_MODEL_TIMEOUT",
    "message": "模型生成超时，本轮未修改草稿。",
    "requestId": "...",
    "traceId": "...",
    "retryable": true,
    "details": [
      {
        "kind": "SMART_DAG_TURN_FAILURE",
        "stage": "MODEL_CALL",
        "sessionId": "...",
        "sessionStatus": "OPEN",
        "sessionLockVersion": 3,
        "turnId": "...",
        "generationId": "..."
      }
    ]
  }
}
```

现有 Guard 顶层 `guardReport/sessionId/turnId/generationId` 暂保留一版，同时提供标准 detail，避免现有前端/测试 break。

### 6.2 Smart DAG 稳定错误

| code | HTTP | stage | retryable | Draft 行为 |
|---|---:|---|---|---|
| `SESSION_CLOSED` | 409 | SESSION | false | 不变 |
| `SMART_DAG_TURN_IN_PROGRESS` | 409 | SESSION | false | 不变；加载当前会话 |
| `SMART_DAG_SESSION_VERSION_CONFLICT` | 409 | SESSION | true | 不变；先 GET 再显式重试 |
| `AGENT_MODEL_REQUIRED` | 422 | MODEL_CALL | false | 不变；先修 Agent model |
| `SMART_DAG_MODEL_TIMEOUT` | 504 | MODEL_CALL | true | 不变 |
| `SMART_DAG_MODEL_UNAVAILABLE` | 502/503 | MODEL_CALL | true | 不变 |
| `SMART_DAG_OUTPUT_INVALID` | 422 | OUTPUT_PARSE | true | 不变 |
| `GUARD_REJECTED` | 422 | GUARD | true | 保留上一合法 Draft |
| `SMART_DAG_DRAFT_CONFLICT` | 409 | DRAFT_PERSIST | true | transaction rollback |
| `SMART_DAG_DRAFT_PERSIST_FAILED` | 503 | DRAFT_PERSIST | true | transaction rollback |
| `SMART_DAG_UNKNOWN_FAILURE` | 500 | UNKNOWN | false | 不变 |

400/403/404 继续使用通用错误语义，不泄露资源存在性或内部 cause。

### 6.3 AAP 冻结

下列均不改：

- `/api/agent-access/v1` path/method；
- AAP token/CORS/visibility；
- `docs/openapi/agent-access-v1.yaml`；
- SDK `createRun/followRun/streamRunEvents` 公共签名和默认 base URL；
- protocol event type/data schema。

shared runtime 的 lazy resolution 与 reliable `run.failed` 属于内部执行语义修复；必须跑 AAP contract、protocol golden、confirmation resume 与 SDK regression。

## 7. 状态机、并发与幂等

### 7.1 Workflow editor

- request token 是 UI commit fence；
- AbortController 只是资源优化；
- Draft ETag 是持久写 fence；
- 任何 stale completion 均不产生 UI/数据副作用。

### 7.2 Tool invocation

- 每次实际 model Tool call 只 resolve 一次；
- confirmation pending 使用固定 invocation ID；
- platform dispatch 是唯一真实 invoke；
- Eino resume 返回已持久结果，不 resolve、不重放；
- 原有 Invocation Pipeline idempotency key、rate limit 和 side-effect retry policy不变。

### 7.3 Run terminal

- 持久 Run 状态是恢复 SoT；
- protocol terminal 是 at-least-once delivery，deterministic event ID 提供幂等；
- reducer 按 sequence + event/item ID 去重；
- terminal 为吸收态，旧非终态不能降级。

### 7.4 Smart DAG

- advisory lock：跨实例同 Session 最多一个 in-flight turn/close；
- session lock version：防 stale/重复客户端 mutation；
- draft version/lock version：防与普通 Workflow 编辑器冲突；
- success UoW：Draft/Session/Turn all-or-nothing；
- explicit retry：新的 generationId/turnId；永不自动重放模型或 publish；
- request lost：客户端先 GET Session/Turns；若已存在结果则采用，只有用户再次点击才创建下一尝试。

### 7.5 OpenAPI

- detail integrity 是读取时快照；
- generation transaction 再次锁 Import/Provider/endpoint 并复核；
- endpoint link 使用 `generated_capability_id IS NULL` CAS；
- 重复/竞争生成继续返回 conflict，不重复 Tool。

## 8. 权限、安全与审计

### 8.1 权限

- 前端与后端 action 表驱动一致，但后端始终最终授权。
- 错误恢复按钮也按对应 action 门禁；VIEWER 不能通过 retry/close/generate 绕过。
- OpenAPI/Tool/Connection 查找始终带 workspace scope。
- 资源不存在/不可见继续沿用现有 403/404 策略。

### 8.2 Secret 与数据最小化

- capability snapshot、pending confirmation 和错误 detail 不含 Token/Secret。
- lazy resolver 不提前获取真实用户凭据；凭据只在现有安全 callback 内短暂注入。
- 日志不记录 Tool args 全文、Smart DAG user message/feedback、模型原文、OpenAPI 文档内容或 schema 全文。
- requestId/traceId、资源 UUID、stage、stable code、计数和耗时可记录。
- UI 截图仅显示公开安全文案和不可逆 request/trace correlation，不显示 credential。

### 8.3 审计

- Run/assistant message/Tool invocation/confirmation 继续复用现有审计事实；protocol append 不新造第二份业务审计。
- Smart DAG 成功/失败 Turn 继续记录 sessionId/turnId/generationId/promptHash/agentId/traceId；失败记录 stage/code，不记录 prompt 全文。
- Session close 继续保留记录；不得 cascade delete。
- OpenAPI 生成和 Tool 创建继续使用现有事务/创建审计；integrity read 不写审计。
- 权限拒绝继续由 Workspace authorizer 记录。

## 9. 可观测性

### 9.1 指标

| 指标 | labels（低基数） | 目标 |
|---|---|---|
| `chatruntime_capability_resolution_total` | `kind,result` | 证明纯文本无 resolve，实际 Tool 才 resolve |
| `chatruntime_terminal_projection_total` | `status,result` | 监控 FAILED 持久化后 event append |
| `smartdag_generate_total` | 现有 `result` 扩展稳定 stage/code 分类 | 区分 model/parse/guard/persist |
| `smartdag_session_lock_total` | `result=acquired|busy|error` | 监控并发与连接池压力 |
| `smartdag_turn_commit_total` | `result=success|conflict|rollback|error` | 监控 UoW |
| `openapi_import_integrity_total` | `status,reason` | 发现历史/异常不完整 |
| `openapi_tool_generation_block_total` | `reason` | 证明 incomplete fail closed |

不把 workspace/tool/import ID 放进 metric label。

### 9.2 日志与告警

- 结构化日志字段：event、workspace_id、run/session/turn/generation/import ID、request_id、trace_id、stage、code、duration_ms。
- 告警建议：
  - terminal projection error 持续出现；
  - Smart DAG persist/lock error 比例异常；
  - incomplete OpenAPI 新增速率异常；
  - capability resolution failure 激增。
- UI calibration 没有现成前端 telemetry；本轮以 store 单测、浏览器 Network/Console 和 E2E 证据验证，不引入新的浏览器追踪 SDK。

## 10. 前端/UI 状态

Canvas UI v0.1 已并入本节。它是 T10=A 下的实现输入，不是新的产品契约；若与已批准产品设计、服务端状态机、安全门禁或本技术方案冲突，以后四者为准。唯一需要显式收敛的细节是：Canvas §4.5 提到 GET 校准后仍为 RUNNING 时可恢复输入；已批准产品 §5.2 明确 RUNNING 输入为 Disabled，因此本方案不采纳该点。此时允许刷新或离开页面，但不得发起同 Session 并发 Run。

### 10.1 状态矩阵

| 页面 | Loading | Empty | Error | Success | Disabled |
|---|---|---|---|---|---|
| Workflow 详情 | modal 保持打开，显示“正在加载最新草稿”，写操作全组 Disabled | 真实无 Draft 显示“草稿不可用”，不挂载空图 | `role=alert` + requestId +“重试加载”；保留详情 | 先挂载 editor，下一 DOM flush 再关闭详情 | 无 EDIT 不渲染入口；stale response 无副作用 |
| Console | Session skeleton；stream 为 CONNECTING/RECONNECTING/CALIBRATING | 无消息沿用现有引导 | FAILED 显示“运行失败/未完成”；断流显示校准 banner；Tool gate 用结构化气泡 | SUCCEEDED 显示“已完成/已完成”，输入 Enabled | PENDING/RUNNING/WAITING 输入 Disabled；服务端仍 RUNNING 时即使 DEGRADED 也不解锁 |
| Smart DAG | `GENERATING` busy，不伪造百分比；关闭 Disabled | 无成功 Draft 保留 preview 和输入 | 持久恢复卡展示 stage/code/retryable/sessionStatus 与安全诊断 | 新 Draft version，移除失败卡 | CLOSED 只允许新建；无 EDIT 隐藏写动作；busy conflict 不改画布 |
| OpenAPI 详情 | modal 的 endpoint/contract 双区 skeleton，禁止先闪“0 节点” | 合法 0 endpoint、合法空 schema 与 INCOMPLETE 分开 | 保留摘要；requestId + 刷新/重新导入引导 | active endpoint 契约可切换，多选 eligible endpoints | INCOMPLETE、load error、零选择或无 EDIT 时禁生成 |
| Tool | catalog LOADING 显示“连接状态加载中” | 仅 LOADED 后绑定实体真实不存在才 MISSING | catalog ERROR 为 UNKNOWN；连接异常为 NEEDS_ATTENTION 等稳定状态 | 生命周期、真实 latestTest、AVAILABLE 三维分开 | Tool lifecycle Disabled 与 Connection Disabled 分开呈现 |

### 10.2 关键文案

- Workflow Loading：`正在加载最新草稿`；失败：`无法打开流程图` / `加载 Workflow 草稿失败；原详情已保留。`
- Console terminal：FAILED 为 `运行失败` / `未完成`；Tool gate 为 `工具调用未执行 · 「{name}」当前不可调用（{reason}）`。
- Console degraded：`实时状态连接中断，正在以持久记录校准。`
- Smart 标题：`本轮生成未完成`；retryable：`本轮在「{stage}」失败，未修改上一版合法草稿。`
- Smart closed：`生成会话已关闭；历史与草稿已保留。`
- OpenAPI valid empty：`该接口未声明请求体。`
- OpenAPI incomplete：`导入详情不完整，已禁止生成 Tool；请重新导入或联系管理员。`
- Tool：`已发布 · 当前不可调用（连接需处理）`

所有错误面只展示 stable code、公开资源名、requestId/traceId 和安全文案；长错误可展开但不得透出 Secret、Token、credential locator、Broker body 或模型原文。

### 10.3 组件与交互边界

| 页面 | 实现形态 | 必须行为 |
|---|---|---|
| Workflow | 复用详情 modal；内联 `InlineLoadErrorBar` 可先局部实现 | detail 保持 → READY mount → close；失败/重试原位 |
| Console | 基于现有 summary/status DOM 收敛为 `RuntimeStatusStrip`；复用消息 error 样式 | badge、意图、composer 同 tick 收敛；terminal 单调；Tool gate 气泡可行动 |
| Smart DAG | Copilot 内新增 `SmartDagRecoveryCard` | 卡常驻至下一成功、新会话或 close；toast 不能作为唯一恢复面 |
| OpenAPI | 详情 modal 内 `OpenAPIEndpointPicker` + `OpenAPIEndpointContractPane` | desktop 双栏、窄屏单列；active row 与生成多选是两个独立状态 |
| Tool | 扩展 `tool-governance.ts` 的 `ToolAvailabilityMeta` | 不创建第三套状态源；列表可合成 pill，详情必须拆出三维 |

不新增路由、全屏 wizard、统一恢复中心、全局 Toast 改造或新设计系统。样式复用现有 `status-pill`、modal/drawer、glass panel 与 focus token。

### 10.4 可访问性与窄屏

- modal 保持 focus trap；关闭后焦点回触发控件。Workflow Loading 使用 `aria-busy`/`role=status`，Error 和 Smart 恢复卡使用 `role=alert`。
- Console 消息流继续 `aria-live=polite`，terminal 收敛不得重复播报整页。
- Tool/OpenAPI 状态不得只靠颜色；endpoint、恢复动作和诊断复制控件必须有可见 focus。
- 390×844 不新增另一套流程：modal 可全屏、OpenAPI 双栏改堆叠/横向 endpoint chip、三维 pill 可换行；重试、关闭会话和生成禁用原因必须可达。

## 11. 测试与验收

### 11.1 单元测试

| 范围 | 重点文件/新增测试 |
|---|---|
| Workflow | `frontend/src/stores/workflow.test.ts`、`frontend/src/views/WorkflowView.test.ts`、`frontend/src/components/workflow/workflow-editor.test.ts`：并行、失败保留、retry、stale、权限 |
| Lazy Tool | `backend/internal/chatruntimebridge/workflow_tools_test.go`、`continue_test.go`、`backend/internal/einoruntime/tool_adapter_test.go`：pure text resolver=0；actual call=1；HITL no invoke；resume no re-invoke |
| Terminal | `backend/internal/chatruntimebridge/result_test.go`、native recorder tests、`frontend/src/stores/chat.test.ts`、`run-event-stream` tests：failed event、duplicate、GET calibration、terminal monotonic |
| Smart DAG | `backend/internal/smartdag/session_test.go`、`turn_test.go`、repository/UoW tests、`backend/internal/transport/http/generate_session_test.go`、`frontend/src/stores/smartdag.test.ts`：typed error、rollback、lock conflict、retry/close |
| OpenAPI | `backend/internal/openapiimport/generation_test.go`、`acceptance_test.go`、`backend/internal/transport/http/tool_openapi_test.go`、`frontend/src/views/openapi-imports-view-behavior.test.ts`：integrity、empty schema、selection、URL |
| Tool/权限 | `backend/internal/tool/test_repository_test.go`、Tool transport tests、`frontend/src/utils/tool-governance.test.ts`、`tools-view-behavior.test.ts`、`workspaces.test.ts`、后端 `workspace_policy_test.go`：批量真实 test summary、load vs missing、Published degraded、role matrix |

### 11.2 集成/契约

- POST Console message with broken unrelated Tool + pure text fake model → Run SUCCEEDED，resolver/invoker 均未触发。
- 同一 Agent 模型实际选择 broken Tool → TOOL step failed，零外部 HTTP，stable code/request/trace 可见。
- Run backend failure → DB FAILED + failed message + exactly one effective `run.failed`; event missing模拟下 GET 仍在 5 秒内收敛。
- Smart DAG model/parse/guard/persist 每阶段 fault injection；previous Draft hash/version 不变。
- Smart DAG 并发两个 turn、turn vs close、turn vs普通 Draft save；busy/conflict 与 rollback 符合设计。
- OpenAPI reported 8/8 + actual 8/8 为 COMPLETE；8/8 + actual 0 为 INCOMPLETE 且 generate 零写入。
- 所有受影响 mutation 逐角色 403/成功矩阵。
- `backend/internal/transport/http/aap_openapi_contract_test.go`、protocolschema golden、confirmation resume、SDK tests 全绿；AAP OpenAPI 无 breaking diff。

### 11.3 AC-01～AC-15 映射

| AC | 技术落点 | 验证 |
|---|---|---|
| AC-01 | §4.1 并行 context + atomic handoff | Workflow view/store + Chrome |
| AC-02 | §4.1 failed context/retry/requestId | 4xx/5xx/network fault |
| AC-03 | request token + §4.7 EDIT gate | stale race + OPERATOR/VIEWER 403 |
| AC-04 | §4.2 build phase resolver=0 | pure text with broken Tool |
| AC-05 | actual call lazy resolve + existing Pipeline | zero external request + stable Tool error |
| AC-06 | §4.3 deterministic terminal + GET | dropped/repeated/stale frame |
| AC-07 | §4.4 typed retryable failure | model/parse/guard/persist + retry |
| AC-08 | close/busy/CLOSED state | close/new session + retention |
| AC-09 | §4.5.1 URL single SoT | full URL, basePath, no binding |
| AC-10 | active endpoint view + integrity count | 8 endpoint switch |
| AC-11 | schema presence semantics + generation gate | valid empty vs missing |
| AC-12 | §4.6 three dimensions | Published/Tested/Connection ERROR |
| AC-13 | catalog state machine | LOADING/ERROR/true missing |
| AC-14 | §4.7/§8 | role matrix + secret scan |
| AC-15 | 全链 Chrome | UX-01～07 evidence package |

### 11.4 Chrome 终验

Sentinel 使用真实 Chrome、可控本地服务和独立测试数据：

1. Workflow：详情 → loading → editor → 保存 Draft → compile → trial → publish；不执行 production。
2. Console：纯文本成功；broken Tool 实际调用安全失败；丢 terminal frame 后 GET 收敛。
3. Smart DAG：成功一轮、四阶段至少各一类失败、retry、close/new；确认无自动 publish。
4. OpenAPI：URL、8 endpoint 切换、合法空 Body、incomplete 禁生成、选择 endpoint 生成。
5. Tool：catalog loading、true missing、Published + historical test + Connection ERROR。
6. VIEWER/OPERATOR/EDITOR 权限抽样与 Network 403。

证据写入新的 verification 报告/截图目录；不覆盖 2026-07-25 原始走查证据。

## 12. 发布、回滚与运行手册

### 12.1 发布顺序

推荐不新增 feature flag，采用兼容的 backend-first：

1. Backend：lazy resolution、terminal projection、Smart additive DTO/UoW、OpenAPI integrity/gate；先跑 backend/AAP regression。
2. Frontend：新 request fields、状态机、UI/权限；前端对缺失 additive 字段保留安全 fallback。
3. 全量自动化与真实 Chrome。

Smart request 新字段因旧后端 `DisallowUnknownFields` 无法识别，所以回滚必须遵循 §12.2；正常发布必须 backend 先于 frontend。

### 12.2 回滚

1. 先回滚 frontend，恢复不发送 `expectedSessionLockVersion` 的版本。
2. 再回滚 backend。
3. 无 schema migration，无需数据 down migration。
4. 已写 terminal event、failed turn、integrity read 无需删除；均为合法既有数据/事件。
5. 若仅 UI 回滚，backend additive response 对旧客户端无害。
6. 若 lazy resolution 出现严重 runtime regression，可回滚对应 backend commit；不得临时绕过 InvocationPipeline 或放宽 Connection readiness。

### 12.3 运行检查

- 观察 terminal projection、Smart lock/commit、OpenAPI incomplete metrics。
- 抽查 AAP createRun/followRun、Console pure text、HITL approve/resume。
- Smart advisory lock busy 激增时先检查模型超时和 DB pool；不直接提高无限超时。
- OpenAPI incomplete 只指导重新导入/人工诊断，不运行自动修复 SQL。

## 13. 风险与缓解

| 风险 | 级别 | 缓解 |
|---|---|---|
| lazy resolution 改到 shared bridge，影响 AAP runtime 行为 | 高 | AAP public freeze + golden/confirmation/SDK regression；不改 wire |
| resolver 在实际 call 后发现 requiresConfirmation | 高 | 先 resolve 再决定 interrupt；dispatch 唯一 invoke；resolved snapshot 非敏感 |
| terminal status 已提交而 event append 失败 | 中 | deterministic idempotency + GET calibration；不伪造 exactly-once |
| advisory lock 长时间占一个 DB connection | 中 | try-lock、210s deadline、指标/连接池测试；不等待排队 |
| 模型期间普通编辑器修改 Draft | 中 | final transaction 使用 draft CAS，冲突全回滚 |
| Smart failure Turn 因 DB outage 也无法持久化 | 中 | HTTP/日志/指标明确 persist failure；不声称已有历史 |
| OpenAPI schema 合法空与缺失误判 | 中 | JSON schema presence 规则 + parser/golden fixtures |
| FE catalog/role 加载短暂抖动 | 低 | mutation fail closed、显式 LOADING，不误报 MISSING |
| backend-first/frontend-second 顺序错误 | 中 | 部署门禁和 rollback 顺序测试 |
| UI 文档与技术状态机后续漂移 | 中 | Canvas UI v0.1 已并入 §10；服务端状态/产品门禁优先，特别锁定 RUNNING composer Disabled，并用 AC/组件测试双向校验 |

## 14. 待负责人确认的技术决策

以下每项均给出事实、选项、推荐与影响。未明确确认时不得实施。

### T1 Workflow editor context API

**事实：** 现有 Readiness 已读取 latest Compilation 并返回当前性、合法性和 blockers；没有 GET Compilation detail。

- A（推荐）：并行复用 Draft + Readiness，不新增 API。
- B：新增 `GET .../editor-context` 聚合 Draft/Compilation/Readiness。
- C：只修详情关闭时机，不加载 Readiness。

**影响：** A 改动最小且满足 AC；B 增加内部 API/缓存一致性负担；C 不能完整恢复 editor context。

### T2 lazy resolution 的 shared runtime 范围

**事实：** Console/AAP 可共用 chatruntime bridge；AAP 公共契约冻结，但内部 runtime 行为可修复。

- A（推荐）：shared bridge 全部 Run 在实际 Tool call 才 resolve，并跑 AAP 全回归。
- B：仅 `TriggeredByType=USER` 的 Console Run lazy，AAP 保持 eager。
- C：模型前过滤不可用 Tool。

**影响：** A 避免两套 runtime，纯文本语义一致；B 降低 AAP行为变化但制造分叉；C 与已批准 D2=A 冲突。

### T3 Run terminal 一致性强度

**事实：** status/message 与 protocol event 不在同一事务；现有 schema 没有 outbox。

- A（推荐）：status/message 先提交，deterministic terminal event 幂等追加，前端 GET 兜底。
- B：新增 transactional outbox/migration，异步投影。
- C：只做前端 GET，不补 `run.failed`。

**影响：** A 无迁移并满足 AC；B 更强但须返回 Atlas/负责人批准迁移且扩大部署面；C 违反 protocol SoT 的完整性目标。

### T4 Smart DAG 并发与原子性

**事实：** 已有 session/draft lock version，但没有 in-flight/pending 列；Draft 与 Turn 目前非原子。

- A（推荐）：advisory lock + existing lock versions + 短事务 UoW；API additive optional expected lock。
- B：新增 PENDING/idempotency/lease 列与 migration。
- C：仅前端防双击。

**影响：** A 无迁移、跨实例安全，但模型期间占一个 DB connection；B 恢复能力更强但触发重新产品确认；C 无法处理多标签/多实例/丢响应。

### T5 Smart DAG failure wire contract

**事实：** 通用 ErrorDTO 已有 `code/requestId/traceId/retryable/details`，Guard 另有历史顶层字段。

- A（推荐）：扩展 `error.details`，GET Turn additive 派生字段，暂保留 Guard 旧顶层字段。
- B：业务失败统一返回 HTTP 200 terminal result。
- C：新增独立 failure/recovery endpoint。

**影响：** A 最符合现有错误契约且兼容；B 模糊 HTTP 失败语义；C 增加无必要 API 和状态同步。

### T6 OpenAPI integrity 与 endpoint 选择

**事实：** Import 完成本身事务化，但历史/异常行可能出现摘要与 detail 不一致；D4 禁止自动回填。

- A（推荐）：服务端 additive integrity + generation fail closed；UI 显式多选，默认选中 eligible endpoints。
- B：仅前端计数和禁用，后端仍信任请求。
- C：读取时重解析/持久化或批量迁移。

**影响：** A 安全闭环、无数据改写；B 可被旧/直接客户端绕过；C 与 D4=A 冲突。

### T7 Tool 状态投影与历史测试来源

**事实：** 实际 invocation 已由 resolver fail closed；列表当前已加载 Tool 与 Connection但缺加载状态，且把 Published 在没有 TestRecord 时推断成测试通过，无法提供真实测试时间。

- A（推荐）：后端批量返回当前相关 version 的安全 `latestTest` 摘要；前端从 workspace-scoped catalog 状态派生 availability，resolver 保持执行权威。
- B：后端新增 Tool+Connection+Test aggregate availability DTO，全部状态由服务端投影。
- C：Connection 退化时自动修改 Tool lifecycle。

**影响：** A 只有 additive test summary、无数据变化，并避免 N+1；B 可集中语义但扩大 join/API 与缓存一致性；C 与 D5=A 冲突。

### T8 前端权限来源

**事实：** 现有 Workspace DTO + members API 足以计算后端角色矩阵；当前前端 action 集不完整。

- A（推荐）：复用 members，扩展 action matrix，加载未知时 mutation fail closed。
- B：后端每个 Workspace 返回 `allowedActions`。
- C：各页面自行硬编码角色。

**影响：** A 无新 API但多一次成员加载；B 更集中但扩大 API/缓存；C 易漂移，不推荐。

### T9 发布策略

**事实：** Smart 新 request 字段会被旧后端的 `DisallowUnknownFields` 拒绝。

- A（推荐）：backend-first、frontend-second；无新 flag；rollback frontend-first。
- B：为 lazy resolution、Smart UoW、OpenAPI gate 分别新增 feature flags。
- C：前后端 big-bang。

**影响：** A 操作简单且兼容；B 增加配置/组合测试和保留坏路径的风险；C 回滚窗口最大。

### T10 UI 结构

**事实：** 已批准产品设计要求原上下文内的 Loading/Error/Retry；Canvas UI v0.1 已交付并落实最小原位结构、完整状态矩阵、关键文案、恢复动作、可访问性与 390×844 输入，且声明无新增负责人未决项。本技术 v0.2 已将其并入 §10，并按冻结产品状态机收敛 RUNNING 输入门禁。

- A（推荐）：最小原位方案——Workflow 详情内状态、Console 状态条、Smart Copilot 恢复卡、OpenAPI 双栏 endpoint/contract、Tool 三状态。
- B：为五处入口新增全屏 wizard/统一恢复中心。
- C：保持 toast-only。

**影响：** A 已有可实现 UI 输入，不改变冻结流程且实现面可控；B 属于视觉/流程扩张；C 不满足 AC-02/07/08/11/13。

## 15. 文件影响清单

预计改动但尚未实施：

- Backend：
  - `backend/internal/chatruntime/contracts.go`
  - `backend/internal/chatruntime/native_protocol_recorder.go`
  - `backend/internal/chatruntimebridge/bridge.go`
  - `backend/internal/chatruntimebridge/pause.go`
  - `backend/internal/einoruntime/tool_adapter.go`
  - `backend/internal/smartdag/session.go`
  - `backend/internal/smartdag/turn.go`
  - `backend/internal/smartdag/platform_graph_model.go`
  - `backend/internal/smartdag/session_repository.go`
  - 新的 Smart DAG transaction/lock helper（位于 `backend/internal/smartdag`）
  - `backend/internal/transport/http/generate_session.go`
  - `backend/internal/openapiimport/generation.go`
  - `backend/internal/openapiimport/repository.go` 或只读 integrity helper
  - `backend/internal/transport/http/tool_openapi.go`
  - `backend/internal/tool/test_repository.go`（批量安全摘要读取）
- Frontend：
  - `frontend/src/views/WorkflowView.vue`
  - `frontend/src/stores/workflow.ts`
  - `frontend/src/services/run-event-stream.ts`
  - `frontend/src/stores/chat.ts`
  - `frontend/src/views/ChatExecutionView.vue`
  - `frontend/src/stores/smartdag.ts`
  - `frontend/src/views/SmartDagView.vue`
  - `frontend/src/stores/integration.ts`
  - `frontend/src/views/OpenAPIImportsView.vue`
  - `frontend/src/utils/tool-governance.ts`
  - `frontend/src/views/ToolsView.vue`
  - `frontend/src/stores/workspaces.ts`
  - `frontend/src/types/domain.ts`
- Tests：§11 所列对应测试。
- 不改：
  - 数据库 migration；
  - `docs/openapi/agent-access-v1.yaml`；
  - AAP SDK 公共 API；
  - UX-08～10 生产代码。

## 16. 确认与变更控制

负责人可按以下格式回复：

```text
T1=A，T2=A，T3=A，T4=A，T5=A，T6=A，T7=A，T8=A，T9=A，T10=A，批准技术方案 v0.2
```

若任一项选择不同，请说明选项与附加约束。沉默、“看起来可以”或只批准部分项不视为当前技术方案批准。

获明确批准后，Knower 才会：

1. 将本文件升级为 Approved 版本并记录确认；
2. 亲自生成 `docs/design/zkl-56-pm-e2e-ux-fixes-implementation-checklist.md`；
3. 按依赖顺序、逐项独立 verification subagent、测试/回滚/证据要求完成实施交接；
4. 由 Conductor 再交 Forge。
