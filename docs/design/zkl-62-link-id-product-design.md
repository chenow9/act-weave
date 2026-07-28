# ZKL-62 链路 ID 现状核查说明

- 版本：v0.2
- 日期：2026-07-27
- 状态：范围已确认；待负责人确认说明内容（Blocked）
- 对应 Issue：ZKL-62「链路问题」

## 1. 结论摘要

**不是所有链路 ID 都携带在请求体里，也不是都与业务参数混放。**

当前实现分为三个层次：

1. **ACTWEAVE HTTP 入站链路**
   - `requestId` 从 `X-Request-ID` 读取；缺失或非法时由服务端生成。
   - `traceId` 优先从 `X-Trace-ID` 读取，其次从 W3C `traceparent` 提取；都没有时回退为本次 `requestId`。
   - 两者写入服务端 Request Context，并通过响应 Header `X-Request-ID` / `X-Trace-ID` 返回。
   - 普通命令的业务 JSON 不要求携带 `requestId` / `traceId`。

2. **ACTWEAVE 运行与响应链路**
   - `traceId` 会从 Request Context 进入 Run、Workflow Execution、Tool Invocation、审计、日志及 AAP 事件。
   - `requestId` / `traceId` 会出现在通用错误响应 JSON 中；部分执行成功响应和协议事件也会在响应体/事件体内返回 `traceId`。
   - `runId`、`executionId`、`invocationId`、`conversationId` 等是业务/运行资源标识：通常位于 URL Path、响应体或事件体，不等同于 HTTP 请求链路 ID。

3. **Tool 出站链路**
   - Tool 测试/直接调用 ACTWEAVE API 使用一个 `input` JSON 包装 Tool 的 Path、Query、Header、Body 全部参数；这会让“调用 ACTWEAVE 的请求体”看起来包含所有 Tool 参数。
   - HTTP Executor 会依据 Tool `actionConfig` 把 `input` 再拆分到真实下游请求的 Path、Query、Header、Body，因此声明为 Header 的字段最终不会落入下游业务 Body。
   - **系统自身的 `traceId` 当前不会被 HTTP Executor 自动注入下游 `traceparent`、`X-Trace-ID` 或请求体。** 若导入的 OpenAPI 声明了 `X-Trace-Id` Header，它会成为普通 Tool input，需要调用方/Agent 手工提供。
   - 模型 API 出站当前也只设置协议、Accept、Authorization 等 Header，没有自动透传 ACTWEAVE trace。

因此，当前不是“链路 ID 与业务参数统一混放”，而是“入站链路 ID 与业务 JSON 已分离；Tool 参数在 ACTWEAVE 内部统一封装后按位置拆分；跨下游服务的系统 trace 自动传播尚未建立”。

### 1.1 `X-Request-ID` 与 `X-Trace-ID` 的区别

一句话理解：

- **`X-Request-ID` 标识“一次 HTTP 请求”。**
- **`X-Trace-ID` 标识“一条可能包含多个请求、运行步骤或事件的处理链路”。**

| 维度 | `X-Request-ID` | `X-Trace-ID` |
| --- | --- | --- |
| 主要用途 | 精确定位某一次 HTTP 请求、响应和对应日志 | 把同一业务动作/Run/Execution 下的多个处理步骤关联起来 |
| 典型粒度 | 每次请求一个；重试、轮询、SSE 重连通常应有新的 requestId | 相关请求/内部步骤可复用同一 traceId |
| 当前来源 | 请求 Header `X-Request-ID`；缺失/非法时服务端生成 UUID | 优先 `X-Trace-ID`，其次 W3C `traceparent`；都没有时回退为本次 requestId |
| 当前去向 | Request Context、响应 Header、HTTP 日志、审计、错误体 | Request Context、响应 Header、日志/审计，以及 Run、Execution、Invocation、AAP Event 等持久运行对象 |
| 是否必然不同 | 否 | 否；当前缺省规则会使二者相同 |
| 是否是权限/幂等依据 | 不是 | 不是 |

示例：

```text
用户发起一次 Run：
  X-Request-ID = req-001
  X-Trace-ID   = trace-order-42

客户端随后查询 Run 状态：
  X-Request-ID = req-002        # 新的一次 HTTP 请求
  X-Trace-ID   = trace-order-42 # 若客户端希望把查询也归入同一链路，可继续携带
```

当前 Console 前端不会主动设置/延续这两个 Header，所以常见实际情况是服务端为每个页面请求生成 requestId，并令 traceId 回退为同一个值；此时二者**值相同但语义仍不同**。Run/Execution/Event 内保存的 traceId 用于运行轨迹关联，但浏览器后续请求和下游 Tool/模型不会自动继承它。

另一个实现细节：直接传入的 `X-Trace-ID` 当前接受与 requestId 相同的宽松字符格式；从 W3C `traceparent` 提取时才要求 32 位十六进制且非全零。因此当前 `traceId` 不一定是标准 W3C trace-id。

## 2. 目标、用户与范围

### 2.1 本版本目标

- 基于当前主线代码、契约和测试，回答链路 ID 的真实携带方式。
- 区分 HTTP 请求关联 ID、运行资源 ID、Tool 业务入参，避免把不同概念合并为一个“链路 ID”。
- 解释 `X-Request-ID` 与 `X-Trace-ID` 的语义、生成规则及当前传播边界。
- 仅提供现状说明，不产生改造需求或开发交接。

### 2.2 用户角色

- Console 用户：通过浏览器发起管理、测试、执行请求，并在错误面看到可用于排障的 ID。
- AAP Client / SDK 调用方：创建 Conversation/Run、订阅事件、执行交互决策。
- Workspace 管理员与审计员：按 `requestId` / `traceId` 查询审计记录或运行轨迹。
- Tool/Workflow 设计者：定义 Tool 的 Path、Query、Header、Body 契约。
- 平台运维/研发：使用日志、审计、Run/Execution/Event 关联定位问题。

### 2.3 本版本范围

- Console `/api/v1` 与 AAP `/api/agent-access/v1` HTTP 入站关联。
- Run、Workflow Execution、Tool Invocation、审计、日志、错误响应、AAP 事件中的 ID 使用。
- Tool 测试/直接调用的统一 `input` 与 HTTP Executor 出站映射。
- 前端与 TypeScript SDK 对 ID 的当前处理。

### 2.4 非目标

- 不修改生产代码、数据库、OpenAPI、SDK 或 UI。
- 不设计新的链路传播标准、兼容期或迁移计划。
- 不把 `workspaceId`、`agentId`、`runId` 等资源 ID 改成 Header。
- 不引入 OpenTelemetry Collector、第三方 APM 或跨系统 baggage。
- 不调整日志/审计数据保留期限。

## 3. 事实、假设与未决项

### 3.1 已核实事实

| 编号 | 事实 | 代码/契约依据 |
| --- | --- | --- |
| F-01 | 入站 `requestId` 来自 `X-Request-ID`，非法或缺失时服务端生成 UUID。 | `backend/internal/transport/http/context.go:60` |
| F-02 | 入站 `traceId` 来自 `X-Trace-ID` 或 W3C `traceparent`；缺失时回退为 `requestId`。 | `backend/internal/transport/http/context.go:68`、`:95` |
| F-03 | 响应 Header 始终写出 `X-Request-ID` / `X-Trace-ID`。 | `backend/internal/transport/http/context.go:77` |
| F-04 | 通用错误 JSON 含 `requestId` / `traceId`。 | `backend/internal/transport/http/errors.go:49`、`:93` |
| F-05 | Workflow 生产执行、Chat/AAP Run 等从 Request Context 取 trace，而非从业务 JSON 取 trace。 | `backend/internal/transport/http/workflow.go:625`、`chat_execution.go:379`、`aap_create_run.go:184` |
| F-06 | Tool 测试/直接调用的管理 API Body 含 `connectionId`、`input` 等业务包装，系统 trace 单独从 Request Context 注入内部 InvokeRequest。 | `backend/internal/transport/http/tool_openapi.go:439`、`:491`、`:542`、`:570` |
| F-07 | HTTP Executor 根据 Tool 声明把统一 input 拆到 Path、Query、Header、Body。 | `backend/internal/toolruntime/http_executor.go:292` |
| F-08 | HTTP Executor 不会自动把内部 `TraceID` 注入下游 HTTP Header/Body；只写 Tool 输入 Header、Connection Header、Content-Type、Accept，以及管线按条件写 Idempotency-Key。 | `backend/internal/toolruntime/http_executor.go:337`、`backend/internal/execution/invocation_pipeline.go:361` |
| F-09 | OpenAPI 中声明的 `X-Trace-Id` 会保留为位置为 Header 的普通 Tool 输入字段。 | `backend/internal/openapiimport/generation.go:285`、`parser_test.go:100` |
| F-10 | 前端 API Client 读取错误体或响应 Header 中的 requestId，但未设置请求侧 `X-Request-ID` / `X-Trace-ID` / `traceparent`。 | `frontend/src/services/api.ts:163` |
| F-11 | AAP 的 Idempotency-Key、If-Match、Last-Event-ID 等控制标识位于 Header；`traceId` 是持久化 Event Envelope 字段。 | `docs/guides/agent-access-api-reference.md:10`、`docs/openapi/agent-access-v1.yaml:421` |
| F-12 | 审计查询可把 requestId/traceId 作为 Query 参数；创建审计导出时它们位于 Body，但语义是“过滤条件”，不是当前请求的链路传播字段。 | `backend/internal/transport/http/audit.go:150`、`:184` |

### 3.2 已确认范围

| 编号 | 已确认事项 | 确认依据 | 影响 |
| --- | --- | --- |
| D-01 | 本 Issue 仅用于了解当前行为，不修改携带方式或契约。 | 负责人评论 `21e7fa06-5d3b-4927-b257-8b08e962d76d`：“我不是要修改什么，我是想了解。” | 关闭 v0.1 中全部改造选项；不交技术方案或开发。 |
| D-02 | 本轮重点补充 `X-Request-ID` 与 `X-Trace-ID` 的区别。 | 同一负责人评论。 | v0.2 新增 §1.1 与对应验收标准。 |

### 3.3 当前假设与限制

| 编号 | 假设/限制 | 影响 |
| --- | --- | --- |
| A-01 | 本说明以当前本地主线代码为事实基础，不代表未指定部署环境的精确版本。 | 若需核查生产/测试环境，还需指定环境与版本并做只读抓包/日志验证。 |
| A-02 | “区别”按当前实现与产品语义解释，不承诺当前已实现完整分布式追踪。 | 避免把 Run 内部 trace 持久化误解为浏览器与第三方服务的自动端到端传播。 |

### 3.4 仍未解决事项

- 无范围、流程、权限、业务规则、数据保留或改造方案未决项。
- 仅待负责人明确确认 v0.2 是否已完整回答现状问题；如仍有疑问，请指出具体场景继续补充。

## 4. 当前流程与状态

### 4.1 Console/API 入站主流程

1. 客户端发送业务请求；链路 ID 可选放在 `X-Request-ID`、`X-Trace-ID` 或 `traceparent`。
2. Request Context Middleware 校验/生成 ID，并写入 Context 与响应 Header。
3. Handler 只解码业务 DTO；需要 trace 的应用服务从 Context 获取。
4. 日志、审计、Run/Execution/Invocation 使用该 trace。
5. 失败时错误体带 requestId/traceId；成功时是否在 Body 中返回取决于业务 DTO，但 Header 始终存在。

### 4.2 Tool 出站主流程

1. Console/Agent/Workflow 构造 Tool `input` 对象。
2. Tool Invocation 内部同时持有 `TraceID` 与 `Input`，两者逻辑分离。
3. HTTP Executor 读取 `actionConfig`，将 input 字段映射至 Path、Query、Header、Body。
4. 只有 Tool 声明的 Header 输入和 Connection 静态/凭据 Header 被写入下游请求。
5. 系统 TraceID 留在 ACTWEAVE 的 Invocation 记录中，不自动传播到下游。

### 4.3 UI 状态

- Loading：尚未收到响应时没有可展示的服务端 requestId。
- Empty：无错误/无执行记录时不生成额外 UI 链路项。
- Error：通用前端错误对象读取 `requestId`；Smart DAG 等特定错误面可同时展示 traceId。
- Success：Execution、Audit、AAP Event 等业务面展示/返回 traceId；普通 CRUD 成功不统一在 Body 展示，仍可从响应 Header 获取。
- Disabled/权限不足：链路 ID 不参与授权；无权限请求仍按安全错误契约返回关联 ID，不因持有某个 traceId 获得资源访问权。

## 5. 权限、安全、数据与审计影响

- `requestId` / `traceId` 是可观测标识，不是身份凭证、幂等键或授权依据。
- 当前允许调用方提供符合格式的 `X-Request-ID` / `X-Trace-ID`，不保证全局唯一；日志与审计使用时必须视为不可信输入。
- `traceparent` 的 trace-id 做 32 位十六进制和非全零校验；`X-Trace-ID` 使用更宽松的 request-id 字符规则，两种入口格式不完全一致。
- ID 会进入日志、审计、错误响应和运行记录；不得把 Token、Secret、个人数据、业务原文编码进 ID。
- 将 trace 自动传播到第三方 Tool 会扩大数据出域面，必须受 Header 白名单、最大长度、重定向剥离和敏感 Header 规则保护。
- 审计导出 Body 中的 requestId/traceId 是筛选条件；只有具备相应 Workspace 审计管理权限的用户可创建导出。
- 本核查不执行危险操作，不改变数据保留、删除、导出或权限策略。

## 6. 风险与依赖

### 6.1 当前风险

- 前端不主动设置或延续 trace，多个页面请求通常由服务端各自生成独立 trace，无法天然还原一次用户操作触发的多请求链路。
- Tool 与模型出站不自动注入 trace，ACTWEAVE 内部轨迹与第三方服务日志之间缺少稳定关联。
- OpenAPI 声明的追踪 Header 被当作普通 Tool input，可能让 Agent/用户承担系统上下文生成责任。
- `X-Trace-ID` 与 `traceparent` 格式规则不同，后续接入标准追踪系统时可能需要兼容与归一化。
- 同一个 `traceId` 可由调用方复用或伪造；不能将其视为唯一事实或安全边界。

### 6.2 本次说明的依赖与边界

- 本次没有实现依赖，不需要 Knower、Canvas 或 Forge。
- 结论依赖当前主线代码和测试；具体部署环境的代理/Header 保留策略不在本轮证据内。
- 若未来提出改造要求，视为新范围，必须重新进入产品确认流程。

## 7. Given / When / Then 验收标准

以下为“现状核查说明”的验收口径；不包含目标态改造验收。

### AC-01 入站 Header 与业务 Body 分离

**Given** 客户端发送含业务 JSON 且带合法 `X-Request-ID` / `X-Trace-ID` 的请求  
**When** ACTWEAVE 接收并处理请求  
**Then** Handler 从业务 JSON 解码业务字段，并从 Request Context 获取两类链路 ID；响应 Header 回显相同 ID。

### AC-02 traceparent 兼容与缺省生成

**Given** 请求不带 `X-Trace-ID` 但带合法 W3C `traceparent`  
**When** Middleware 建立 Request Context  
**Then** 使用 `traceparent` 的 trace-id；若所有 trace 输入均缺失/非法，则生成 requestId 并令 traceId 回退为该 requestId。

### AC-03 错误关联

**Given** 任意已进入 HTTP Middleware 的请求发生 4xx/5xx  
**When** 返回通用错误  
**Then** 响应 Header 与错误 JSON 都提供可关联的 requestId/traceId，且不包含 Secret/Token。

### AC-04 Tool 统一 input 的位置拆分

**Given** Tool input 同时包含 Path、Query、Header、Body 字段，且 actionConfig 声明各自位置  
**When** HTTP Executor 构造下游请求  
**Then** 每个字段只进入声明的位置；Header 字段不因 ACTWEAVE 管理 API 使用统一 `input` 包装而落入下游业务 Body。

### AC-05 当前出站 trace 缺口被准确陈述

**Given** Invocation 内部存在系统 TraceID，但 Tool schema 未声明追踪 Header  
**When** HTTP Executor 构造下游请求  
**Then** 当前实现不会自动新增 `traceparent`、`X-Trace-ID` 或 trace Body 字段；报告不得宣称已实现端到端传播。

### AC-06 资源 ID 不误判为 HTTP 链路 ID

**Given** AAP/Workflow 请求包含 URL Path 中的 runId/executionId/interactionId 或响应/事件体中的相关 ID  
**When** 评估其携带方式  
**Then** 将其归类为业务/运行资源标识，不归类为本次 HTTP requestId。

### AC-07 权限不足

**Given** 调用方携带已知 requestId/traceId 但无目标资源权限  
**When** 访问资源  
**Then** 仍按授权规则拒绝/隐藏资源；链路 ID 不改变权限结果。

### AC-08 测试证据

**Given** 使用 Go 1.25 工具链  
**When** 运行 `go test ./internal/transport/http ./internal/toolruntime ./internal/openapiimport`  
**Then** 相关入口、Tool HTTP 映射和 OpenAPI Header 参数测试通过。

### AC-09 request 与 trace 语义区分

**Given** 一条业务链路包含发起 Run 与随后查询 Run 两次 HTTP 请求  
**When** 解释两类 ID  
**Then** 两次请求分别使用不同 requestId；若调用方主动延续链路，可使用相同 traceId；同时说明当前 Console/下游不会自动完成该延续。

## 8. 本轮验证记录

- 代码核查：README、HTTP Middleware/错误契约、Workflow/Chat/AAP Handler、Tool API、HTTP Executor、OpenAPI Import、前端 API Client、AAP OpenAPI/指南。
- 测试命令：`go test ./internal/transport/http ./internal/toolruntime ./internal/openapiimport`（Go 1.25.11）
- 结果：通过。
- 未验证：具体部署环境的代理/Header 保留策略、生产 APM/日志平台、真实第三方 Tool 接收行为。

## 9. 负责人确认请求

负责人已确认本 Issue 仅用于了解现状，不做修改。请明确回复是否批准 v0.2，尤其确认以下解释是否已回答问题：

1. requestId = 单次 HTTP 请求；traceId = 可跨多个请求/步骤复用的链路关联 ID；
2. 当前缺省时二者值相同，但语义不同；
3. 当前 Console、Tool 与模型调用并未自动延续完整端到端 trace。

在负责人明确批准 v0.2 前，本 Issue 保持 Blocked；批准后由 Conductor 按流程完成收口，不交技术实现。

## 10. 版本记录

- v0.1：完成请求体/Header/其它位置及 Tool 出站现状核查，提出改造范围选项。
- v0.2：根据负责人评论 `21e7fa06-5d3b-4927-b257-8b08e962d76d`，冻结为“仅了解、不改造”，关闭改造未决项，补充 `X-Request-ID` 与 `X-Trace-ID` 区别。
