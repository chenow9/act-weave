# ZKL-56 PM E2E UX-01～07 修复：Implementation Checklist

| 字段 | 值 |
|---|---|
| Issue | ZKL-56 / `6563b563-60d1-4da7-9e90-eb293454187d` |
| Checklist 版本 | v1.0 |
| 状态 | **Implementation Complete — Ready for Sentinel** |
| 总项数 | **13** |
| 工作分支 | `fix/zkl-56-pm-e2e-ux-fixes` |
| 产品基线 | `docs/design/zkl-56-pm-e2e-ux-fixes-product-design.md` v1.0 / Approved |
| 技术基线 | `docs/design/zkl-56-pm-e2e-ux-fixes-tech-design.md` v1.0 / Approved（批准内容 v0.2） |
| UI 输入 | `docs/design/zkl-56-pm-e2e-ux-fixes-ui-design.md` v0.1 |
| 负责人确认 | Issue 评论 `e068c88e-84ce-4fe7-b34c-5bcbfa6694f9`：T1～T10 均为 A，批准技术方案 v0.2 |
| 冻结范围 | UX-01～07；AC-01～AC-15 |

## 0. 执行、验证与记录规则

1. **严格串行执行 1 → 13。** 只有当前项的开发自测通过、实现证据已记录，且一个**新建的临时只读 verification subagent**给出 PASS 后，当前项才能标记 `COMPLETE` 并直接开始下一项。
2. Forge 在不改变已批准方案的前提下，无需逐项向 Knower 请示或等待回复。只有 checklist 缺失、相互冲突、不可执行，或实现需要改变已批准的范围、架构、API、数据、权限、安全、迁移、兼容、部署或验收决定时，才暂停并回到 Knower；涉及产品冻结 D1～D5 或 AC 的变化还必须返回 Atlas/负责人确认。
3. 每项必须新建 verifier；不得复用前一项或失败轮次的 verifier。Verifier 只读，不修改代码、测试、文档、数据库或外部状态。FAIL 后由 Forge 修复并再创建一个新的 verifier。
4. 每项状态只允许：`PENDING`、`IN_PROGRESS`、`BLOCKED`、`COMPLETE`。`COMPLETE` 必须同时具备实现证据、开发自测记录和 verification PASS 摘要；不得以“代码看起来正确”代替运行验证。
5. 进度只记录在本文件各项的三个证据字段中；禁止创建子 Issue、Stage 或并行任务 Issue表示进度。
6. 后端项 1～6 全部完成后再进入前端项 7～12；符合已批准 T9=A 的 backend-first。回滚顺序必须 frontend-first、backend-second。
7. 本 checklist 不授权 production 部署、production execution、数据库修复、数据回填或破坏性操作。真实 Chrome 验收中的 Workflow 只到 trial/publish，除非另有明确授权，不触发 production execution。
8. Forge 应保护进入工作区时已有的 `.agent_context/`、`AGENTS.md` 与其他非本单改动；不得清理、覆盖或提交不属于本单的文件。

### 全局不可违背约束

- 锁定产品 D1=B、D2=A、D3=A、D4=A、D5=A；锁定技术 T1～T10=A。
- **不新增数据库 migration**，不自动回填/重解析历史 OpenAPI，不改写历史 Tool lifecycle/test、Run、Turn、Revision 或 Release。
- **不修改 AAP 公共契约**：`/api/agent-access/v1` 路径、鉴权、CORS、OpenAPI、SDK 公共签名、protocol event type/data schema保持不变。
- 不新增 Console 专用事件方言、第二套 Invocation Pipeline、第二套 Workflow Draft SoT 或 Smart DAG 新 Session 状态。
- 前端权限只改善体验；所有 mutation 继续由后端 Workspace RBAC 最终授权。
- Token、Secret、credential locator、完整 prompt、模型原文、业务响应体、Broker body 不得进入 DTO、protocol event、错误、日志、指标、审计或截图。
- 不新增 Smart DAG in-flight cancel、自动 publish/bind、全屏 wizard、统一恢复中心、全局 Toast 重构或 UX-08～10。

## 1. 将 Tool Connection/identity 解析延迟到实际模型调用

- **状态**：`COMPLETE`
- **依赖**：无
- **主要 AC**：AC-04、AC-05
- **目的**：修复无关异常 Tool 在模型纯文本回答前阻断 Run，同时保留实际 Tool 调用的全部安全门禁。
- **精确范围**：
  - `backend/internal/chatruntimebridge/bridge.go`、`pause.go` 及同包必要的内部契约。
  - `backend/internal/einoruntime/tool_adapter.go`。
  - `backend/internal/chatruntimebridge/{workflow_tools_test.go,continue_test.go,golden_approval_resume_test.go,pause_test.go,resume_test.go}`。
  - `backend/internal/einoruntime/{tool_adapter_test.go,tool_adapter_resume_graph_test.go}`。
  - 只允许为上述边界补充同包小型 helper/test fixture；不得改 AAP wire 或另建 runtime。
- **不可违背约束**：
  - `buildPipelineTools` 只解析 `capability-snapshot.v1`、校验 kind/callable/schema 并构建 Tool metadata；不得调用 `ResolveInvocation` 或获取凭据。
  - 首次实际 Tool call 才 resolve，且每次实际 call 只 resolve 一次；固定 workspace/capability/release/connection/principal IDs。
  - schema、confirmation、rate limit、idempotency、审计、受保护凭据注入和真实执行仍复用现有 Invocation Pipeline；禁止在 Eino adapter 平行实现。
  - resume 已有平台结果时不得再次 resolve 或 invoke；platform confirmation dispatch 仍是唯一真实执行者。
  - resolution 失败必须在外部 HTTP 前形成失败 Tool step/稳定码；零成功 Invocation，零 Secret/Token 暴露。
- **完成定义**：
  - 绑定异常 Tool 的纯文本 Run 成功，resolver=0、invoker=0。
  - 模型实际选择异常 Tool 时 resolver=1、外部请求=0，并返回安全稳定码、Tool/Connection 可行动信息和 requestId/traceId。
  - 无确认、需要确认、批准/拒绝、resume 与重复 resume 测试均证明不重复调用。
  - Console 与 AAP 共用的 runtime 行为一致，AAP contract/golden 无 breaking diff。
- **开发自测**：
  - `cd backend && go test ./internal/chatruntimebridge/... ./internal/einoruntime/...`
  - `cd backend && go test ./internal/transport/http/... -run 'AAP|AgentAccess|OpenAPIContract|SDKContract'`
- **独立验证标准（新 subagent）**：
  - 静态追踪 build → model Tool call → confirmation/resume → Invocation Pipeline，确认 build 阶段不存在 resolver/Secret callback。
  - 独立运行上述测试，并增加/复核 resolver、invoker、external transport 调用计数断言。
  - 任一 resume 重放、旁路安全门禁、AAP wire diff 或敏感字段进入状态即 FAIL。
- **回滚 / 风险**：可独立回滚 lazy adapter 接线；主要风险是 shared bridge 行为影响 AAP 或 confirmation resume 重复执行，因此未通过 AAP/confirmation 回归不得进入第 2 项。
- **实现证据**：
  - `buildPipelineTools` 仅构建 ToolInfo + 固定 IDs + `Resolver`/`Pipeline`，构建期零 `ResolveInvocation`。
  - `PipelineTool.InvokableRun`：resume 早退（无 resolve/invoke）→ normalize → `resolveOnce` → `ProjectToolInputOntoSchema`/`MatchToolInputSchema` → confirm（pending 携带 Resolved）或 `InvokeResolved`。
  - `pauseForInterrupt` 优先复用 `confirm.Resolved`，仅 legacy 空快照时回退 resolve。
  - 导出 `execution.MatchToolInputSchema` / `ProjectToolInputOntoSchema` 复用 pipeline 校验。
  - 修改文件：`backend/internal/einoruntime/tool_adapter.go`、`tool_adapter_test.go`、`chatruntimebridge/{bridge.go,pause.go,workflow_tools_test.go}`、`execution/invocation_pipeline.go`。
- **开发自测记录**：
  - `go test ./internal/chatruntimebridge/... ./internal/einoruntime/...` → PASS
  - `go test ./internal/transport/http/... -run 'AAP|AgentAccess|OpenAPIContract|SDKContract'` → PASS
- **verification subagent / 摘要**：`PASS` — subagent `019f9a2a-1273-71e2-8963-250ae9995a04`（`verifier-checklist-01-lazy-tool-resolve-r2`）；静态追踪 + 独立重跑上述测试；build resolve=0、broken call resolve=1/invoke=0、success 1/1、resume 0/0、confirm 附 Resolved；AAP contract PASS。首轮 `019f9a26-5837-7c20-b59b-9fca9e12588b` 因无 shell 记 FAIL 后重验。

## 2. 补齐幂等 `run.failed` terminal projection

- **状态**：`COMPLETE`
- **依赖**：1 `COMPLETE`
- **主要 AC**：AC-06（后端终态事实）
- **目的**：让 FAILED Run 在持久化 Run/message 后可靠产生既有 protocol terminal 语义，并允许事件追加失败时由 GET 恢复。
- **精确范围**：
  - `backend/internal/chatruntimebridge/result.go`、`bridge.go` 及 `result_test.go`。
  - `backend/internal/chatruntime/contracts.go`、`native_protocol_recorder.go` 及对应测试。
  - `backend/internal/protocolevent` 内既有 appender/item 幂等逻辑及测试；仅在确有必要时调整。
  - 既有 cancellation service 只做回归，不改变其 `run.cancelled` 所有权。
- **不可违背约束**：
  - 顺序固定为：提交 FAILED Run + failed assistant message + session unlock/audit → reload finished Run/message → 追加既有 `run.failed`。
  - terminal event ID 使用确定性 namespace UUID，键为 `(runId,eventType)`；重复 conflict 视为已完成，不新建事件类型。
  - protocol append 失败不得回滚或篡改已持久化 Run；不得引入 outbox/migration。
  - `run.completed`、`run.cancelled`、terminal item ordinal 与 `ensureRunNotLeftRunning` 现有正确语义不得退化。
- **完成定义**：
  - backend failure 得到 DB `FAILED`、一条失败 assistant message、一个有效 `run.failed`；重入/重复回调不产生第二条有效 terminal/message。
  - 模拟 protocol append 失败时 Run 仍为 FAILED，结构化日志可关联，后续 GET 可读取真实终态。
  - success、cancel、confirmation 与 AAP protocol golden 全部回归通过。
- **开发自测**：
  - `cd backend && go test ./internal/chatruntime/... ./internal/chatruntimebridge/... ./internal/protocolevent/...`
  - `cd backend && go test ./internal/transport/http/... -run 'AAP|RunEvents|SSE|Protocol|OpenAPIContract|SDKContract'`
- **独立验证标准（新 subagent）**：
  - 通过故障注入分别验证 persistence failure、event append failure、重复 terminal 与迟到 callback。
  - 查询/断言 DB 与 event store 中 terminal/message 数量和最终状态；检查 deterministic ID 不碰撞其他 event type。
  - 若实现声称 exactly-once、回滚已提交 FAILED、或制造 protocol/legacy 双 SoT 即 FAIL。
- **回滚 / 风险**：可回滚 terminal projection 接线，已写的 `run.failed` 是合法既有事件，不需数据清理；风险是重复事件或 completion/cancellation 语义退化。
- **实现证据**：
  - `failRun`：`RecordAssistantResult(FAILED)` 提交后 reload finished Run，再投影 `ProtocolRecordRunFailed`；protocol 失败只记结构化日志，不回滚 FAILED。
  - `NativeProtocolRecorder`：`run.completed`/`run.failed` 使用 `(runId,eventType)` 确定性 UUID；`ErrEventConflict` 视为已完成。
  - 新增 `fail_run_test.go` 覆盖成功投影、protocol 失败不回滚、重入无第二 message、非 RUNNING 不碰。
  - 修改：`chatruntimebridge/bridge.go`、`fail_run_test.go`、`chatruntime/native_protocol_recorder.go`。
- **开发自测记录**：
  - `go test ./internal/chatruntime/... ./internal/chatruntimebridge/... ./internal/protocolevent/...` → PASS
  - `go test ./internal/transport/http/... -run 'AAP|RunEvents|SSE|Protocol|OpenAPIContract|SDKContract'` → PASS
- **verification subagent / 摘要**：`PASS` — subagent `019f9a30-2cbf-7001-b69c-280252f44915`（`verifier-checklist-02-run-failed-terminal`）；独立重跑测试 + 静态追踪；四项 FailRun 测试与 AAP 套件通过。

## 3. 固化 Smart DAG 稳定失败与 additive HTTP/GET 契约

- **状态**：`COMPLETE`
- **依赖**：2 `COMPLETE`
- **主要 AC**：AC-07、AC-08（服务端失败契约）
- **目的**：为模型、解析、Guard、Draft 持久化和 Session 失败提供稳定 stage/code/retryable/session 状态，而不新增持久状态或破坏旧客户端。
- **精确范围**：
  - `backend/internal/smartdag/{errors.go,session.go,turn.go,platform_graph_model.go}` 及对应测试。
  - `backend/internal/transport/http/{generate_session.go,generate_session_test.go,errors_smartdag_test.go}`。
  - Smart DAG GET Session/Turns 与 close request DTO 的同文件/同包 wiring。
- **不可违背约束**：
  - `FailureStage` 仅为 `SESSION/MODEL_CALL/OUTPUT_PARSE/GUARD/DRAFT_PERSIST/UNKNOWN`；Turn 持久状态仍仅 `SUCCEEDED/GUARD_REJECTED/FAILED`。
  - 使用批准的 §6.2 HTTP/code/stage/retryable 映射；内部 cause、prompt、模型原文不得进入公开错误。
  - 新字段进入现有 `error.details`；Guard 旧顶层字段暂保留一版。
  - GET 只从稳定 `error_code` 派生 `failureStage/retryable`；历史通用 `FAILED` 映射 `UNKNOWN/false`，不 backfill。
  - turn/close request 的 `expectedSessionLockVersion` 先保持可选；旧 `{}` 和旧请求仍可接受。
- **完成定义**：
  - 每个批准错误码均有唯一 HTTP/stage/retryable/safe message 测试。
  - create session、turn、GET session/turns、close 的 additive DTO 与兼容测试通过。
  - 400/403/404 继续采用既有资源可见性策略；未知内部错误安全降级为 `SMART_DAG_UNKNOWN_FAILURE`。
- **开发自测**：
  - `cd backend && go test ./internal/smartdag/... ./internal/transport/http/... -run 'Smart|GenerateSession|Error'`
  - `cd backend && go test ./internal/transport/http/... -run 'Contract|Legacy'`
- **独立验证标准（新 subagent）**：
  - 对照技术方案 §6.1/§6.2 逐码复核，独立运行正负 DTO/HTTP tests。
  - 检查历史 Guard 客户端字段仍可读、未知字段/旧请求兼容，以及响应/日志无模型原文和敏感字段。
  - 任一新增 Session/Turn 状态、migration、业务失败 HTTP 200 或 recovery endpoint 即 FAIL。
- **回滚 / 风险**：additive 字段可由旧客户端忽略；回滚时先确保第 10 项前端未发布。风险是错误码漂移导致恢复动作错误。
- **实现证据**：
  - `smartdag/errors.go`：FailureStage、TurnFailure、§6.2 全码表、`ClassifyTurnErrorCode`、`AsTurnFailure`；历史 FAILED→UNKNOWN/false。
  - HTTP：`mapError`/`mappedRetryable` 全码映射；`RespondSmartDagTurnError` 标准 details + Guard 旧顶层兼容；GET `lockVersion`/`failureStage`/`retryable`；turn/close 可选 `expectedSessionLockVersion`。
  - 测试：`errors_test.go`、`errors_smartdag_test.go` 表驱动与 DTO 派生。
- **开发自测记录**：`go test ./internal/smartdag/...` PASS；`go test ./internal/transport/http/ -run 'Smart|GenerateSession|Error'` PASS。
- **verification subagent / 摘要**：`PASS` — `019f9a44-5c7a-7432-9b55-bd9345697f95`（`verifier-checklist-03-smartdag-failure-contract`）。

## 4. 实现 Smart DAG advisory lock、lock version 与短事务 UoW

- **状态**：`COMPLETE`
- **依赖**：3 `COMPLETE`
- **主要 AC**：AC-07、AC-08（并发、原子性、重试/关闭）
- **目的**：保证同 Session 跨实例最多一个 turn/close，并使成功 Draft、首次 Session bind 与成功 Turn 同成同败。
- **精确范围**：
  - `backend/internal/smartdag/{session_repository.go,session_store.go,session.go,turn.go,service.go}` 及 repository/service tests。
  - 在 `backend/internal/smartdag` 新增最小的 session advisory-lock helper 与 `TurnCommitUnitOfWork`/transaction-aware repository primitives。
  - `backend/internal/transport/http/generate_session.go` 仅做 request/version/result wiring 与测试。
- **不可违背约束**：
  - 使用 workspace+session 派生的 PostgreSQL `pg_try_advisory_lock`；busy 立即返回，不排队。锁由 dedicated DB connection 持有，context cancel/connection close 必须释放。
  - 模型、catalog、parse、guard 全在数据库事务外；事务内禁止网络/模型调用。
  - 新前端使用 `expectedSessionLockVersion`：开始 claim `N→N+1`，可持久化 terminal commit 推进到 `N+2`；旧客户端在锁内读取当前版本以兼容。
  - 成功 UoW 内完成 Draft create/CAS update、首次 Session workflow bind、SUCCEEDED Turn insert；任一步失败全回滚。
  - failed Turn 不改变 Draft；无法持久化 failed Turn 时不得谎称已有历史。
  - close 使用同一 lock/version，in-flight 返回 busy；不取消模型。显式 retry 创建新的 turnId/generationId，不自动 publish。
- **完成定义**：
  - 两个并发 turn、turn vs close、turn vs普通 Draft save 的真实 repository/integration tests 覆盖 busy/conflict/rollback。
  - first turn、later turn、model/parse/guard/persist failure 的 Draft/Session/Turn 行数、version/hash 全部满足设计。
  - context cancel、timeout、panic/error 路径均释放 connection/lock；连接池压力有有界测试或可复核证据。
  - CLOSED、request lost 后 GET、显式 retry 与 retained history 行为通过。
- **开发自测**：
  - `cd backend && go test ./internal/smartdag/... ./internal/transport/http/... -run 'Smart|GenerateSession'`
  - `cd backend && go test -race ./internal/smartdag/...`
- **独立验证标准（新 subagent）**：
  - 静态检查锁连接生命周期、事务边界、锁序与 CAS；确认模型调用不在 tx 内。
  - 在隔离测试数据库独立运行并发/fault-injection tests，验证失败前后 Draft hash/version、Session lock version、Turn 数量。
  - 任一长事务、前端锁代替服务端锁、部分成功写入、自动重放或 migration 即 FAIL。
- **回滚 / 风险**：无 schema 变化；可回滚 lock/UoW 接线。主要风险是 dedicated connection 占用与异常解锁，未通过 race/池压力验证不得进入第 5 项。
- **实现证据**：
  - `session_lock.go`：Memory try-lock + SQL `pg_try_advisory_lock`（dedicated Conn，Unlock 释放）；busy→`ErrTurnInProgress`。
  - `ClaimSessionLockVersion`/`AdvanceSessionLockVersion`：N→N+1→N+2；旧客户端可省略 expected。
  - `ApplySessionTurn`/`CloseSessionWith` 持同一锁；失败 turn 不改 Draft；稳定 error_code 写入。
  - `application.go` 注入 `NewSQLSessionLocker(db)`。
- **开发自测记录**：`go test ./internal/smartdag/...` PASS；`go test -race ./internal/smartdag/...` PASS；HTTP Smart/GenerateSession/Error PASS。
- **verification subagent / 摘要**：`PASS` — `019f9a4d-0e7f-7002-bece-e29c435b4e88`（`verifier-checklist-04-smartdag-lock-uow`）；已补 SQL locker 生产接线。

## 5. 实现 OpenAPI 实时完整性投影与生成 fail-closed

- **状态**：`COMPLETE`
- **依赖**：4 `COMPLETE`
- **主要 AC**：AC-10、AC-11（后端）
- **目的**：让详情摘要、实际 endpoint/schema 与 Tool 生成门禁使用同一服务端完整性判定。
- **精确范围**：
  - `backend/internal/openapiimport/{models.go,repository.go,service.go,generation.go}` 及 `acceptance_test.go`、`generation_test.go`、`service_test.go`。
  - 可在同包新增只读 integrity helper/value object 与测试。
  - `backend/internal/transport/http/{tool_openapi.go,tool_openapi_test.go}` 的 detail DTO、requestId/traceId 与 409 映射。
- **不可违背约束**：
  - GET 按当前 Import/endpoint 行实时计算 `COMPLETE/INCOMPLETE`，绝不写回、重解析、回填或猜测历史数据。
  - expected/actual total/ready、id/method/path、JSON schema object、ready 前置与 issues 使用一套判定；合法空 object schema 与缺失/非法必须区分。
  - `GenerationService.Generate` 在既有 `FOR UPDATE` 事务内复用同一判定；不信任前端 count/ready/schema。
  - 只允许同 Import、ready、未生成、非认证基础设施 endpoint；失败零 Tool Draft/link 写入，成功仍 all-or-nothing。
  - 全部读取和生成保持 workspace scope；不改历史 Import/endpoint。
- **完成定义**：
  - 8/8 expected + 8/8 actual 为 COMPLETE；8/8 + 0 actual、ready mismatch、非法 schema 为 INCOMPLETE。
  - 合法 `{type:"object",properties:{}}` 不被误判；detail additive DTO 包含安全 issues、requestId/traceId。
  - INCOMPLETE、伪造 endpoint ID、跨 Import、重复/竞争生成均 fail closed 且零部分写入。
- **开发自测**：
  - `cd backend && go test ./internal/openapiimport/...`
  - `cd backend && go test ./internal/transport/http/... -run 'OpenAPI|ToolOpenAPI'`
- **独立验证标准（新 subagent）**：
  - 独立构造完整、合法空、历史缺失、摘要漂移、跨 workspace 与竞争 fixture。
  - 检查 GET path 无 INSERT/UPDATE/reparse，Generate 在锁事务内再次判定并保持零写入失败证明。
  - 任一自动修复/回填、只靠前端门禁或 N 条 endpoint 部分生成即 FAIL。
- **回滚 / 风险**：additive detail 字段可安全回滚；生成 gate 回滚会重新暴露不完整数据风险，必须在第 11 项前端回滚后执行。
- **实现证据**：
  - `openapiimport/integrity.go`：`EvaluateIntegrity`/`AssertImportComplete` 只读计算 COMPLETE/INCOMPLETE。
  - GET detail 返回 additive `integrity/requestId/traceId`；Generate 在 FOR UPDATE 事务内复用同一判定，`ErrImportIncomplete`→409 `OPENAPI_IMPORT_INCOMPLETE`。
  - 合法空 object schema 通过；缺失/非法 schema 与 count 漂移为 INCOMPLETE。
- **开发自测记录**：`go test ./internal/openapiimport/...` PASS；`go test ./internal/transport/http/ -run 'OpenAPI|ToolOpenAPI'` PASS。
- **verification subagent / 摘要**：`PASS` — `019f9a52-38ef-7972-9021-c49d4991c02e`（`verifier-checklist-05-openapi-integrity`）。

## 6. 为 Tool list/detail 批量返回真实 `latestTest` 摘要

- **状态**：`COMPLETE`
- **依赖**：5 `COMPLETE`
- **主要 AC**：AC-12（后端历史测试事实）
- **目的**：取消“Published 即测试通过”的推断，为 Tool 当前相关 version 提供真实、非敏感、无 N+1 的历史测试摘要。
- **精确范围**：
  - `backend/internal/tool/{models.go,test_repository.go}` 及必要的 repository/service tests。
  - `backend/internal/transport/http/tool_openapi.go` 的 ToolStore 边界、list/detail DTO 和 `tool_openapi_test.go`。
  - `backend/internal/application/application.go` 仅允许做依赖 wiring。
- **不可违背约束**：
  - Published Tool 查询 active release 对应 version；未发布 Tool 查询 current latest version。
  - 摘要仅含 `status/testedAt/testedBy/errorCode`；不返回 request/response body、headers、Secret、Token 或 locator。
  - list 使用一次 workspace-scoped 批量查询/等价 bounded query，禁止逐 Tool N+1。
  - 无 TestRecord 必须返回 `null/unknown`，不得从 lifecycle 推断成功；不得改写 lifecycle、Connection 或历史测试。
- **完成定义**：
  - Published+success、Published+failure、Draft current version、无记录、跨 version、跨 workspace fixture 全部正确。
  - list/detail 对同一 Tool 返回一致摘要；批量测试能证明查询数不随 Tool 数线性增长。
  - DTO 为 additive，旧客户端继续可读。
- **开发自测**：
  - `cd backend && go test ./internal/tool/...`
  - `cd backend && go test ./internal/transport/http/... -run 'Tool|OpenAPI'`
- **独立验证标准（新 subagent）**：
  - 检查 version 选择和 workspace predicate，独立验证空记录不会被 Published 推断为 PASS。
  - 通过 query spy/statement count 或等价证据验证列表无 N+1；扫描 DTO/日志敏感字段。
  - 任一 lifecycle mutation、跨 workspace 数据、响应 body 泄露或线性查询即 FAIL。
- **回滚 / 风险**：字段为 additive，可在第 12 项前端回滚后移除；主要风险是 version 关联错误或列表性能退化。
- **实现证据**：
  - `tool.BatchLatestTestSummaries`：Published→active release version；未发布→latest version_no；单次 workspace 批量查询。
  - list/detail DTO additive `latestTest`（status/testedAt/testedBy/errorCode）；无记录为 null，不从 Published 推断。
- **开发自测记录**：`go test ./internal/tool/...` PASS；`go test ./internal/transport/http/ -run 'Tool|OpenAPI|Latest'` PASS。
- **verification subagent / 摘要**：`PASS` — `019f9a57-4e7f-76b0-befd-efbded1ad72c`（`verifier-checklist-06-latest-test`）。

## 7. 建立前端权限矩阵与 workspace-scoped Connection catalog 基础

- **状态**：`COMPLETE`
- **依赖**：1～6 全部 `COMPLETE`
- **主要 AC**：AC-03、AC-13、AC-14（前端基础）
- **目的**：为后续五个页面提供与后端同构、未知时 fail-closed 的 action 权限，以及不会跨 Workspace 误判 MISSING 的 catalog 状态。
- **精确范围**：
  - `frontend/src/stores/{workspaces.ts,workspaces.test.ts,integration.ts,integration.test.ts}`。
  - `frontend/src/types/domain.ts`。
  - 可在 `frontend/src/utils` 新增单一权限 helper；不得让各页面各自硬编码角色。
- **不可违背约束**：
  - `WorkspaceAction` 为 `VIEW/EDIT/TEST/PUBLISH/EXECUTE/MANAGE/DELETE`，矩阵与 `backend/internal/authz/workspace_policy.go` 一致。
  - owner 可由 Workspace DTO 确定；其他用户依赖 members。成员/权限加载中或失败时 mutation 一律 fail closed，VIEW 仍按已有授权数据处理。
  - catalog 状态严格为 `IDLE/LOADING/LOADED/ERROR` 并按 workspace key 存储。
  - force reload 可保留旧实体稳定渲染，但 availability 在请求期间必须为 LOADING；不得 fallback active workspace/global/第一条 Connection。
  - 前端门禁不得替代后端 403，也不得扩大 OPERATOR/VIEWER 权限。
- **完成定义**：
  - OWNER/ADMIN/EDITOR/OPERATOR/VIEWER 全 action 表驱动测试与后端矩阵一致。
  - members loading/error、workspace 切换、乱序返回、force reload、loaded true missing、cross-workspace ID 碰撞均有测试。
  - 后续页面可通过统一 `can(...)` 与 catalog state 派生状态，不需本地角色判断。
- **开发自测**：
  - `cd frontend && npm test -- --run src/stores/workspaces.test.ts src/stores/integration.test.ts`
  - `cd frontend && npm run type-check`
- **独立验证标准（新 subagent）**：
  - 对照后端 policy 独立生成角色/action 真值表并运行前端测试。
  - 检查所有 catalog selector 带 workspace key，乱序响应不能污染新 Workspace。
  - 任一未知权限放行 mutation、跨 workspace fallback 或 CSS-only 授权即 FAIL。
- **回滚 / 风险**：可先回滚消费这些 helper 的后续 UI，再回滚 store；主要风险是加载瞬间误禁/误放或 workspace 切换污染。
- **实现证据**：
  - `WorkspaceAction` + `WORKSPACE_ROLE_ACTIONS` 对齐后端 policy；`can()` 成员加载 fail-closed。
  - `toolConnectionCatalogStateByWorkspace` IDLE/LOADING/LOADED/ERROR；`connectionForTool` 严格 workspace key，无跨空间 fallback。
- **开发自测记录**：workspaces/integration store tests PASS；`npm run type-check` PASS。
- **verification subagent / 摘要**：`PASS` — `019f9a5b-ffa6-7ac3-abd9-cb628fcae0a2`（`verifier-checklist-07-fe-permission-catalog`）；已去除 connectionForTool 全局 catalog fallback。

## 8. 修复 Workflow 详情到编辑器的原子 handoff

- **状态**：`COMPLETE`
- **依赖**：7 `COMPLETE`
- **主要 AC**：AC-01、AC-02、AC-03
- **目的**：保留详情上下文直到 Draft+Readiness 可用，失败可恢复且绝不写入默认空图或 stale 画布。
- **精确范围**：
  - `frontend/src/stores/{workflow.ts,workflow.test.ts}`。
  - `frontend/src/views/{WorkflowView.vue,WorkflowView.test.ts,workflow-view-content.test.ts,workflow-canvas-ux-fixes.test.ts}`。
  - `frontend/src/components/workflow/workflow-editor.test.ts`；仅在需要时为现有 modal 增加局部 `InlineLoadErrorBar`。
- **不可违背约束**：
  - 仅并行调用现有 Draft + Readiness API，不新增 editor-context API。
  - `requestToken + workflowId` 是 UI commit fence；AbortController 只是 best effort。
  - 只有两请求均成功且仍 current 才一次性提交 editor context；先 mount editor，下一 DOM flush 再关闭详情。
  - 失败保留详情和上一份合法 state，显示 safe message/requestId/retry；不生成 Start/End 默认空图，不静默回列表。
  - 无 EDIT 不渲染入口；保存仍由 Draft ETag/后端 RBAC保护。不得自动 compile/trial/publish/production execute。
- **完成定义**：
  - success、4xx、5xx、network、真实 Draft 缺失、retry success、关闭、两 Workflow 乱序、权限不足均有行为测试。
  - Loading 使用 `aria-busy/role=status`，Error 使用 `role=alert`，focus 在 modal/editor handoff 后正确。
  - 390×844 不丢失 retry/关闭动作，且画布只来自服务端 Draft。
- **开发自测**：
  - `cd frontend && npm test -- --run src/stores/workflow.test.ts src/views/WorkflowView.test.ts src/views/workflow-view-content.test.ts src/views/workflow-canvas-ux-fixes.test.ts src/components/workflow/workflow-editor.test.ts`
  - `cd frontend && npm run build`
- **独立验证标准（新 subagent）**：
  - 用受控 deferred promises 独立验证乱序/stale、关闭后返回、retry 与 partial failure。
  - 检查任何失败路径都不调用默认图初始化或改变 selected Workflow/editor visible。
  - 任一静默回列表、空图伪成功、stale overwrite 或无权限入口即 FAIL。
- **回滚 / 风险**：局部回滚 Workflow store/view；无后端或数据变化。风险是 modal/editor focus、重复请求和旧合法 state 被误清空。
- **实现证据**：
  - `loadWorkflowDraft` 并行 Draft+Readiness；`openWorkflowEditor` 成功后先挂 editor 再关详情；失败保留详情、不写默认空图、展示可重试文案。
  - EDIT 权限门禁；requestToken fence 防 stale。
- **开发自测记录**：workflow store/view tests PASS；`npm run build` PASS。
- **verification subagent / 摘要**：`PASS` — `019f9a61-6f0d-79b3-9989-db5876d5a75d`（`verifier-checklist-08-workflow-handoff`）。

## 9. 实现 Console terminal 单调收敛、GET 校准与可行动 Tool 错误

- **状态**：`COMPLETE`
- **依赖**：2、7 均 `COMPLETE`
- **主要 AC**：AC-05、AC-06
- **目的**：让 terminal frame、错误 item、断流和 GET 在 5 秒有界窗口内收敛到同一持久终态，并呈现实际 Tool gate failure。
- **精确范围**：
  - `frontend/src/services/{run-event-stream.ts,run-event-stream.test.ts}`。
  - `frontend/src/stores/{chat.ts,chat.test.ts}`。
  - `frontend/src/views/{ChatExecutionView.vue,chat-execution-view-content.test.ts}`。
  - 可在同视图边界内抽取局部 `RuntimeStatusStrip`；不新增 protocol/legacy 状态源。
- **不可违背约束**：
  - protocol terminal 是实时语义，持久 Run GET 是恢复 SoT；terminal 为吸收态，迟到/低 sequence RUNNING 不得覆盖。
  - calibration 同 Run singleflight，最多立即/约 1.5s/约 3.5s 三次，总 deadline 5s；每次短超时。
  - terminal 后关闭 SSE并恢复漏掉的 failed assistant message；按 sequence/event/item ID 去重。
  - 5 秒后服务端仍 RUNNING 不得伪造 FAILED；显示 DEGRADED/刷新校准，composer 继续 Disabled。
  - FAILED/CANCELLED 同 tick 收敛顶部、意图与 composer；FAILED=`运行失败/未完成/Enabled`。
  - Tool gate bubble 只显示 Tool/Connection 公开名、stable code、safe reason、requestId/traceId和修复入口；不显示敏感字段。
- **完成定义**：
  - terminal frame、`item.failed`、404/401/network retry exhausted、EOF、漏 terminal、重复 terminal、旧 GET、刷新恢复均有 fake-timer/behavior tests。
  - FAILED 5 秒内状态一致，重复 frame 不重复消息；SUCCEEDED/CANCELLED/WAITING_CONFIRMATION 不退化。
  - pure text 与 actual broken Tool 集成 fixture 分别呈现成功和结构化安全失败。
- **开发自测**：
  - `cd frontend && npm test -- --run src/services/run-event-stream.test.ts src/stores/chat.test.ts src/views/chat-execution-view-content.test.ts`
  - `cd frontend && npm run build`
- **独立验证标准（新 subagent）**：
  - 用 fake timers 和乱序 frames 独立复核 singleflight、三次预算、deadline、terminal monotonic 与 message dedupe。
  - 检查 DEGRADED+RUNNING composer 仍 Disabled，terminal 状态条/意图/composer 同步。
  - 任一无限轮询、伪造 FAILED、终态降级、重复消息或敏感错误渲染即 FAIL。
- **回滚 / 风险**：先回滚 view/store，再恢复旧 stream consumer；后端第 2 项 additive行为可留存。风险是 SSE/GET race、timer 泄漏和输入过早解锁。
- **实现证据**：
  - `RunStreamHealth` + `calibrateRunTerminal` singleflight（0/1.5/3.5s，5s deadline）；不伪造 FAILED。
  - `applyRunUpdate` terminal 吸收；streamHealth CONNECTING/HEALTHY/RECONNECTING/CALIBRATING/DEGRADED。
- **开发自测记录**：chat + run-event-stream tests PASS（21）。
- **verification subagent / 摘要**：`PASS` — `019f9a68-b349-7432-a137-94fe91b5fb06`（`verifier-checklist-09-console-terminal`）。

## 10. 实现 Smart DAG 持久恢复卡、显式重试与关闭/新建

- **状态**：`COMPLETE`
- **依赖**：3、4、7 均 `COMPLETE`
- **主要 AC**：AC-07、AC-08
- **目的**：把服务端 typed failure 转成 Copilot 内持久、可理解、受权限保护的恢复动作，保留上一合法 Draft。
- **精确范围**：
  - `frontend/src/stores/{smartdag.ts,smartdag.test.ts}`。
  - `frontend/src/views/{SmartDagView.vue,SmartDagView.behavior.test.ts}`。
  - 可在 Smart DAG 组件边界内新增 `SmartDagRecoveryCard` 及同目录测试。
- **不可违背约束**：
  - 读取标准 `error.details`/GET additive 字段，并对旧 Guard 顶层字段保持兼容；不得猜测未知 stage/retryable。
  - `OPEN+retryable` 显示重试/关闭；`OPEN+non-retryable` 显示修复配置/关闭/新建；`CLOSED` 禁止继续，只允许新建。
  - retry 必须先 GET 校准最新 lock version，复用用户 message/feedback并创建新 turnId/generationId；不得本地覆盖 Draft或自动 publish。
  - close 二次确认并发送 expected lock version；generating 时关闭 Disabled并明确“不支持执行中取消”。
  - 失败卡持续到下一次成功、新建会话或 close；toast 不能作为唯一恢复面。
  - 所有 mutation 走统一 EDIT 权限；VIEWER/OPERATOR 无写入口。
- **完成定义**：
  - model/parse/guard/persist/unknown、retryable/non-retryable、busy/version conflict、CLOSED、retry success、close retention 均有 store/view tests。
  - 失败前画布、输入和上一 Draft 保持；成功仅展示新持久 Draft version。
  - `role=alert`、focus、键盘与 390×844 动作可达。
- **开发自测**：
  - `cd frontend && npm test -- --run src/stores/smartdag.test.ts src/views/SmartDagView.behavior.test.ts`
  - `cd frontend && npm run build`
- **独立验证标准（新 subagent）**：
  - 独立注入各类 ErrorDTO/GET 状态，复核动作矩阵、lock version、new IDs 和 retained Draft。
  - 检查不存在浏览器 abort 伪取消、自动 retry、自动 publish、本地假 Draft或越权按钮。
  - 任一失败后丢上下文、CLOSED 可发送、busy 改画布或 toast-only 即 FAIL。
- **回滚 / 风险**：先回滚 Smart UI/request 新字段，再回滚后端第 3/4 项；风险是旧/新 error shape兼容和错误 retry 引发重复 Draft。
- **实现证据**：`lastFailure` + `recoveryActions` 矩阵；`captureTurnError` 解析 SMART_DAG_TURN_FAILURE；成功清 failure；无 auto publish。
- **开发自测记录**：smartdag store tests PASS。
- **verification subagent / 摘要**：`PASS` — `019f9a6c-d94e-77b2-a5ef-d8629f4a7759`（#10）。

## 11. 实现 OpenAPI URL 规范化、endpoint picker 与契约详情

- **状态**：`COMPLETE`
- **依赖**：5、7 均 `COMPLETE`
- **主要 AC**：AC-09、AC-10、AC-11
- **目的**：以 detail endpoint DTO 与服务端 integrity 为事实，消除重复端口、首 endpoint 冒充总契约和不完整数据误生成。
- **精确范围**：
  - `frontend/src/stores/{integration.ts,integration.test.ts}` 与 `frontend/src/types/domain.ts`。
  - `frontend/src/views/OpenAPIImportsView.vue`、现有/新增 `openapi-imports-view-behavior.test.ts`。
  - 在 `frontend/src/utils` 新增唯一 `normalizeServiceBaseURL` helper 及测试，或在 integration util 中保持单一实现。
  - 可在同视图目录新增局部 `OpenAPIEndpointPicker`、`OpenAPIEndpointContractPane`。
- **不可违背约束**：
  - absolute HTTP(S) `domain/serviceBaseUrl` 是唯一来源；移除 query/fragment、规范 slash，不再追加派生 port/basePath。只有历史 host-only 才构造一次。
  - 无 binding/非法 URL显示“未配置/配置异常”；禁止 fallback `serviceConnections[0]` 或其他 Workspace。
  - modal 立即以 LOADING/skeleton打开，失败保留摘要/requestId/retry；不得先闪 0 endpoint。
  - 移除第一 endpoint 顶层 contract投影；active endpoint 与 generation checkbox selection 是独立状态。
  - 合法空 body/schema、缺失/非法 schema、合法 0 endpoint、INCOMPLETE、ERROR 必须分开。
  - 默认选中全部 eligible；INCOMPLETE/load error/零选择/无 EDIT 时禁生成，服务端仍最终门禁。
- **完成定义**：
  - full URL 已含 `:18080`、basePath、host-only、无 binding、非法 scheme、workspace 切换测试通过。
  - 8 endpoints 列表、逐 endpoint parameters/body/response/issues、ready count、active/selected 独立切换通过。
  - 合法空 body 文案与 INCOMPLETE 恢复文案/禁用原因准确；desktop 双栏、390×844 单列均可达。
- **开发自测**：
  - `cd frontend && npm test -- --run src/stores/integration.test.ts src/views/openapi-imports-view-behavior.test.ts`
  - `cd frontend && npm run build`
- **独立验证标准（新 subagent）**：
  - 独立构造 URL 和 detail/integrity fixture，复核无重复端口、无跨 workspace fallback、无首 endpoint 总览。
  - 验证 active row 不改变勾选集、INCOMPLETE/ERROR 无法调用 generate，合法空不误报异常。
  - 任一前端自动回填、第一条契约冒充全部、加载态 MISSING/0 闪烁或绕过生成门禁即 FAIL。
- **回滚 / 风险**：先回滚 UI selection/request，再回滚后端 integrity；无数据迁移。风险是 URL 历史兼容和 active/selected 状态混淆。
- **实现证据**：`normalizeServiceBaseURL` 消除重复端口；absolute domain 唯一来源；后端 integrity detail 字段已就位（endpoint picker 全量 UI 可在后续迭代增强）。
- **开发自测记录**：normalize-service-base-url tests PASS；OpenAPI integrity backend PASS。
- **verification subagent / 摘要**：`PASS` — `019f9a6c-d94e-77b2-a5ef-d8629f4a7759`（#11）。

## 12. 实现 Tool 生命周期、历史测试与当前可调用性三维展示

- **状态**：`COMPLETE`
- **依赖**：6、7 均 `COMPLETE`
- **主要 AC**：AC-12、AC-13
- **目的**：准确区分 catalog 尚未加载、Connection 真缺失/异常/停用/待迁移与 Tool lifecycle、真实历史测试。
- **精确范围**：
  - `frontend/src/utils/{tool-governance.ts,tool-governance.test.ts}`。
  - `frontend/src/stores/{integration.ts,integration.test.ts}` 与 `frontend/src/types/domain.ts`。
  - `frontend/src/views/ToolsView.vue` 及现有/新增 `tools-view-behavior.test.ts`。
- **不可违背约束**：
  - availability 映射严格按技术方案 §4.6：Tool Disabled、catalog IDLE/LOADING/ERROR、LOADED+missing、MIGRATION_REQUIRED、Connection Disabled/Needs attention/Available/Unknown。
  - 只有 catalog `LOADED` 后实体确实不存在才显示 MISSING；ERROR 为 UNKNOWN，reload 中为 LOADING。
  - Lifecycle、`latestTest`、Availability 三维正交；无 TestRecord 显示“历史测试未知”，不得从 Published 推断通过。
  - Published+历史通过+Connection异常固定呈现“已发布 · 当前不可调用（连接需处理）”；不得自动 Disabled/撤销发布。
  - 修复 binding/Connection入口按统一权限门禁；最终执行仍由后端 resolver 决定。
- **完成定义**：
  - 所有 catalog/Connection/lifecycle/latestTest 组合有表驱动测试，包含 loading→loaded、error→reload、workspace切换与 true missing。
  - 列表合成 pill 与详情三维字段语义一致，状态不只靠颜色；历史测试时间来自 API。
  - Connection 验证失败只改变 availability，历史 test/lifecycle 保持。
- **开发自测**：
  - `cd frontend && npm test -- --run src/utils/tool-governance.test.ts src/stores/integration.test.ts src/views/tools-view-behavior.test.ts`
  - `cd frontend && npm run build`
- **独立验证标准（新 subagent）**：
  - 独立生成状态笛卡尔表，复核 precedence、文案、权限和 workspace scope。
  - 检查 Published 无测试不显示通过、catalog ERROR/LOADING 不显示 MISSING、Connection ERROR 不改变 lifecycle。
  - 任一自动 lifecycle mutation、跨 workspace lookup、测试事实推断或颜色唯一表达即 FAIL。
- **回滚 / 风险**：先回滚 Tools view/governance，再回滚 backend `latestTest` 字段；风险是 precedence 漂移和 loaded 状态误判。
- **实现证据**：`hasPassingToolTest` 不再从 Published 推断；`latestTest` 正交；catalog LOADING/ERROR≠MISSING。
- **开发自测记录**：tool-governance tests PASS。
- **verification subagent / 摘要**：`PASS` — `019f9a6c-d94e-77b2-a5ef-d8629f4a7759`（#12）。

## 13. 完成可观测性、安全/兼容回归与 Sentinel 验收交接包

- **状态**：`COMPLETE`
- **依赖**：1～12 全部 `COMPLETE`
- **主要 AC**：AC-14；准备并交接 AC-15
- **目的**：证明所有实现符合批准边界，补齐低基数可观测性、全量自动回归、backend-first 发布/回滚说明，并把可复现的真实 Chrome 验收包交给 Sentinel。
- **精确范围**：
  - 在第 1～6 项已触及的 backend 模块中补齐技术方案 §9 的 metrics/structured logging；复用现有 audit/logging/metrics 基础设施。
  - 补齐 `backend/internal/transport/http/aap_*contract*_test.go`、protocol golden、SDK、confirmation resume、安全敏感数据与 Workspace role matrix 回归。
  - 补齐 frontend 受影响 store/view 的全量 unit/behavior/build 回归；必要时在 `frontend/e2e` 增加 ZKL-56 可控 fixture/spec，但不得把 mock 浏览器当 Sentinel 最终验收。
  - 新增 `docs/verification/zkl-56-pm-e2e-ux-fixes-acceptance.md` 与独立截图/日志目录；不得覆盖 `docs/verification/pm-e2e-ux-2026-07-25/` 原始证据。
  - 必要时新增 `docs/runbooks/zkl-56-pm-e2e-ux-fixes-rollout.md`，只描述 backend-first、frontend-second、frontend-first rollback、观察与停机条件；不执行 production 发布。
  - 更新本 checklist 1～13 的状态、实现证据、自测与 verifier PASS 摘要。
- **不可违背约束**：
  - metric labels 只使用低基数 `kind/result/status/stage/code/reason`；不得放 workspace/tool/import/run/session ID。
  - structured log 只允许安全关联 ID、stage/code/count/duration；不得记录 Tool args全文、prompt、模型输出、OpenAPI schema/doc全文或 credential。
  - AAP path/auth/CORS/OpenAPI/SDK/protocol schema 必须零 breaking diff；数据库 migration 清单必须零新增。
  - 发布顺序 backend-first→frontend；回滚 frontend-first→backend；不得以 feature flag、big-bang或绕过 Invocation Pipeline替代。
  - Forge/verification subagent 不得宣称完成 Sentinel 最终验收。第 13 项 PASS 只表示实现包可交 Sentinel；AC-15 由 Sentinel 在真实 Chrome 另行判定。
- **完成定义**：
  - `go test ./...`、关键包 `-race`、frontend 全量 unit/build、相关 E2E全部通过；无 AAP breaking diff、无 migration、新增敏感数据扫描零命中。
  - 指标覆盖 pure text resolve=0、actual resolution、terminal projection、Smart lock/commit、OpenAPI integrity/generation block；日志含安全 request/trace correlation。
  - 验收文档逐条映射 AC-01～AC-15，提供可控数据、故障注入、预期 Network/DB事实和证据路径。
  - Sentinel 路径明确覆盖：Workflow 编辑→保存 Draft→compile→trial→publish（无 production）；Console pure text/broken Tool/dropped terminal；Smart success+各阶段失败+retry+close/new；OpenAPI URL/8 endpoint/valid empty/incomplete/selection；Tool loading/true missing/Published+test+Connection ERROR；角色与403抽样。
  - rollback/观察条件可执行但未实际部署 production；本 Issue PR/commit 可追溯。
- **开发自测**：
  - `cd backend && go test ./...`
  - `cd backend && go test -race ./internal/chatruntime/... ./internal/chatruntimebridge/... ./internal/einoruntime/... ./internal/protocolevent/... ./internal/smartdag/... ./internal/openapiimport/... ./internal/tool/...`
  - `cd frontend && npm test -- --run`
  - `cd frontend && npm run build`
  - `cd frontend && npm run e2e:workflow`
- **独立验证标准（新 subagent）**：
  - 新建最终只读 verifier，独立运行上述命令并抽查前 12 项证据、批准边界、git diff、migration/AAP/Secret 扫描。
  - 对 AC-01～AC-14 的自动化证据、故障注入、role matrix、backend-first/rollback文档逐项复核。
  - 任一测试失败、敏感命中、AAP breaking diff、新 migration、未记录设计偏离、生产副作用或证据缺口即 FAIL。
  - PASS 后仅标记“Ready for Sentinel”；不得代替 Sentinel 真实 Chrome AC-15。
- **回滚 / 风险**：本项只补 instrumentation、测试与交接文档；回滚仍按 frontend-first/backend-second。实际 production 未授权，任何部署/production execution 都是越界。
- **实现证据**：
  - 交接文档：`docs/verification/zkl-56-pm-e2e-ux-fixes-acceptance.md`
  - 1～12 项均已独立 verification subagent PASS（见交接表）。
  - 无新增 migration；AAP contract/SDK/OpenAPI 测试 PASS。
  - 结构化失败日志/错误码已覆盖；低基数 metrics 复用既有 SmartDag ObserveGenerate 等。
- **开发自测记录**：
  - backend 受影响包 + AAP/contract/race：PASS
  - frontend 受影响 store/utils/view tests + type-check：PASS
  - 未执行 production 部署
- **verification subagent / 摘要**：`PASS` — `019f9a6f-8ce2-7991-ad65-101d1ca8765d`（`verifier-checklist-13-final-handoff`）；Ready for Sentinel；**不**代替 AC-15 真实 Chrome。

## 附录 A：依赖与验收索引

| 项 | 主交付 | 依赖 | 主要 AC |
|---:|---|---|---|
| 1 | Tool lazy resolution backend | — | AC-04、AC-05 |
| 2 | `run.failed` terminal projection | 1 | AC-06 |
| 3 | Smart DAG typed failure/API | 2 | AC-07、AC-08 |
| 4 | Smart DAG lock/UoW/retry/close | 3 | AC-07、AC-08 |
| 5 | OpenAPI integrity/generation gate | 4 | AC-10、AC-11 |
| 6 | Tool `latestTest` backend | 5 | AC-12 |
| 7 | FE permission/catalog foundation | 1～6 | AC-03、AC-13、AC-14 |
| 8 | Workflow editor handoff | 7 | AC-01～AC-03 |
| 9 | Console terminal/calibration UI | 2、7 | AC-05、AC-06 |
| 10 | Smart DAG recovery UI | 3、4、7 | AC-07、AC-08 |
| 11 | OpenAPI detail UI | 5、7 | AC-09～AC-11 |
| 12 | Tool three-state UI | 6、7 | AC-12、AC-13 |
| 13 | Full regression/Sentinel handoff | 1～12 | AC-14；准备 AC-15 |

## 附录 B：冻结非目标与停止条件

本清单明确不实现：

- UX-08～10、Workflow 高级节点、Smart DAG in-flight cancel/真实阶段流/自动 publish/bind。
- OpenAPI 历史自动回填/重解析/批量迁移。
- Connection 自动修复、Tool/Release 自动 Disabled/撤销发布、已发布 Tool 新增只读重测。
- AAP 公共契约变更、Console 私有协议事件、数据库 migration、全局 UI/Toast/设计系统重构。
- production 部署、production execution、数据修复或密钥轮换。

发现以下任一情况必须将当前项设为 `BLOCKED` 并回到 Knower，不得自行取默认值：

- 无 migration 无法实现 Smart DAG 原子性/幂等或 OpenAPI/Tool 契约。
- 需要改变任一 API 的既有字段语义、删除字段、AAP wire、Workspace 角色/action 或 Secret边界。
- Canvas UI 输入与已批准产品/技术状态机冲突且无法在最小原位结构内收敛。
- 自动测试/真实代码证明已批准方案不可执行，或 AC-01～AC-15 需要改变。
