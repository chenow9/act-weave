# 智能编排 — Console API 契约草案（Phase 0）

| 字段 | 值 |
|------|-----|
| **文档标题** | Intelligent Orchestration — Console API Draft |
| **状态** | Draft（PR-CL0 / T-P0；无运行时 handler 承诺） |
| **日期** | 2026-07-23 |
| **设计全文** | [`intelligent-orchestration-closed-loop.md`](./intelligent-orchestration-closed-loop.md)（Rev 1.1 §6） |
| **实施清单** | [`intelligent-orchestration-closed-loop-checklist.md`](./intelligent-orchestration-closed-loop-checklist.md) |
| **单测策略** | [`intelligent-orchestration-test-strategy.md`](./intelligent-orchestration-test-strategy.md) |
| **迁移草案** | `backend/internal/database/migrations/000059_workflow_generate_sessions.*` |
| **Mock 约定** | `examples/mock-aftersales/`（默认端口 **18080**） |

> 本文件是 **Console additive** 路由与错误码的书面契约锚点。实现落在 WP1–WP3；**不得**改动 AAP 外部公共面（F1–F7）。

---

## 0. 冻结与禁止

| 规则 | 说明 |
|------|------|
| **AAP 外部面冻结** | `docs/openapi/agent-access-v1.yaml`、`/api/agent-access/v1`、`sdk/typescript` 公共 API：**本 goal 不 breaking 变更**（checklist F1–F7） |
| **无 request-body `modelConfigId` 绕过** | 模型 **只** 取自会话绑定 Agent 的 `modelConfig`（经 `PlatformChatModel`）。请求体若携带 `modelConfigId` → **400/422 拒绝**，不得用于生成 |
| **无 rules 降级主路径** | Agent 无可用 LLM → **422 `AGENT_MODEL_REQUIRED`**；禁止静默 `smart-dag.v1` rules 201 |
| **生成会话 ≠ 生产会话** | `SmartGenerateSession` 独立于 `ChatSession` / AAP Conversation（D15） |
| **System Prompt 非用户编辑** | 管理员场景固化（D16）；Console 生成 UI **无** System Prompt 编辑器 |

Base path（Console）：`/api/v1/workspaces/{wid}/...`  
鉴权：既有 Console workspace RBAC（与现有 workflow 写操作同级；具体 role 以 transport 惯例为准）。

---

## 1. Generate sessions（WP1 / D2 / D15 / D16）

### 1.1 创建会话

```http
POST /api/v1/workspaces/{wid}/workflow-generate-sessions
Content-Type: application/json
```

**Request body**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agentId` | uuid | **是** | 同 Workspace 的 Agent（D2） |
| `workflowId` | uuid | 否 | 在已有 Draft 上继续多轮 |
| `constraints` | object | 否 | maxNodes、allowedNodeTypes 等；服务端仍强制 D8 白名单 |

```json
{
  "agentId": "uuid",
  "workflowId": "uuid?",
  "constraints": {}
}
```

**201 Created**

```json
{
  "sessionId": "uuid",
  "agentId": "uuid",
  "modelConfigId": "uuid",
  "workflowId": "uuid?",
  "status": "OPEN"
}
```

| 条件 | HTTP | code | 说明 |
|------|------|------|------|
| Agent 无可用 LLM / modelConfig 不可解析 | **422** | `AGENT_MODEL_REQUIRED` | **不创建 session** |
| `agentId` 缺失/非法 | 400 | （bad request） | |
| Agent 跨 workspace / 不存在 | 4xx | | 建议 404 或 422 |
| 授权失败 | **403** | | |

创建时 **快照** Agent 当前可用的 `modelConfigId` 写入 session（后续 turn 仍须校验该 config 仍可用）。

---

### 1.2 发送一轮（主路径）

```http
POST /api/v1/workspaces/{wid}/workflow-generate-sessions/{sid}/turns
Content-Type: application/json
```

**Request body**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `message` | string | **是** | 1..2000 runes，本轮自然语言意图 |
| `feedback` | object | 否 | `FailureFeedback`（编译/试跑/生产失败回流，D14） |

```json
{
  "message": "string",
  "feedback": null
}
```

**200 / 201 成功**

```json
{
  "sessionId": "uuid",
  "turnId": "uuid",
  "generationId": "uuid",
  "workflow": { },
  "draft": { },
  "assistantMessage": "string?",
  "reasoningSteps": [],
  "missingCapabilities": [],
  "nodeExplanations": [],
  "availableToolIds": [],
  "selectedToolIds": [],
  "confidence": 0,
  "guardReport": null,
  "draftVersion": 1
}
```

说明：

- 首轮成功：`workflow.Create`；后续：`UpdateDraft`，`draftVersion` 递增。
- Draft `ui`（至少）：`generatedBy=smart-dag.v2`、`sessionId`、`agentId`、`modelConfigId`、`promptHash`（D16）、`businessGoal`（可由首轮/最近轮摘要）。
- 审计：`promptId` + `promptHash` + `generationId` + `sessionId` + `agentId`。

**失败（turn）**

| 条件 | HTTP | code | 行为 |
|------|------|------|------|
| message 非法（空/超长） | **400** | | 不写 Draft |
| session 不存在 | **404** | | |
| session 已 `CLOSED` | **409** | | close 后禁止 turn |
| Agent 模型中途失效 | **422** | `AGENT_MODEL_REQUIRED` | 不写本轮 Draft |
| Guard 拒绝（幻觉 toolId / 非法图等） | **422** | （guard / validation） | **`guardReport`**；**保留上一轮合法 Draft** |
| LLM 上游错误 | 502/504 或 422 | | **不**用本轮结果覆盖 Draft |
| 授权失败 | **403** | | |

**禁止：** 失败时前端本地假草稿；无 LLM 时静默 rules 201。

---

### 1.3 读会话

```http
GET /api/v1/workspaces/{wid}/workflow-generate-sessions/{sid}
```

**200** — 会话元数据 + turns 历史摘要 + 当前 draft 摘要（含 `draftVersion`、`workflowId`、status）。

---

### 1.4 关闭会话

```http
POST /api/v1/workspaces/{wid}/workflow-generate-sessions/{sid}:close
```

**200** — `{ "sessionId", "status": "CLOSED", "closedAt" }`  

之后任何 `.../turns` → **409**。用户进入既有 compile → trial → publish → bind 旅程。

---

### 1.5 兼容（过渡，可选）

`POST .../workflows:generate` 可实现为「隐式 session + 单 turn」；**FE 主路径必须多轮 UI**，产品路径 `generatedBy=smart-dag.v2`。

---

## 2. Production execute（WP2 / D4 / D11 / D13）

### 2.1 启动生产执行

```http
POST /api/v1/workspaces/{wid}/workflows/{id}/revisions/{rid}:execute
Idempotency-Key: optional
Content-Type: application/json
```

```json
{
  "input": {},
  "trigger": "console"
}
```

`trigger`: `"console" | "api"`（可扩展，须文档化）。

**202 Accepted**

```json
{
  "executionId": "uuid",
  "workflowId": "uuid",
  "revisionId": "uuid",
  "status": "PENDING",
  "traceId": "string"
}
```

`status` 初始：`PENDING` | `RUNNING`。

**约束**

- MVP：`rid` 建议 **仅 active** 已发布 revision；未编译 graph **禁止**执行。
- 必须走 CompiledExecutionPlan + `workflowruntime` / Eino；Invocation Pipeline 不变。
- Trial 仍用 `compilations/:cid:trial`，**不与本 API 合并**（D11）。
- 重复 `Idempotency-Key` 不双跑。

### 2.2 执行事件（E1，D13）

```http
GET /api/v1/workspaces/{wid}/executions/{eid}/events
```

- SSE 帧语义 **对齐 protocolevent**（类型集合可子集）。
- **E2**（仅 agent-run events）**非**本方案生产默认路径。

列表/详情：既有 `GET .../executions`、`GET .../executions/:id` 可扩展 `revisionId`、`trigger`、`traceId`、`generationId?`。

---

## 3. Bind（WP3 / D12）— 参考

优先 **复用** 既有 agent capability binding API；若产品化不足可 additive：

```http
POST /api/v1/workspaces/{wid}/workflows/{id}/revisions/{rid}:bind-agent
```

```json
{
  "agentId": "uuid",
  "versionPolicy": "PINNED",
  "enabled": true
}
```

| 规则 | 说明 |
|------|------|
| 仅 **已发布** revision 可 bind | 未发布 / Draft → **4xx** |
| 底层 | `agent_capability_bindings`，`kind=WORKFLOW` |
| 向导默认 | 绑生成会话的 `agentId`（D12）；更换 Agent 须显式且目标仍满足 D2 |

`versionPolicy`：`"PINNED" | "LATEST_ACTIVE"`（与现网 binding 语义对齐；若现网枚举为 `FOLLOW_ACTIVE`，实现时统一映射并在 OpenAPI/DTO 注明）。

---

## 4. Error codes（最低集）

| code | HTTP | when |
|------|------|------|
| `AGENT_MODEL_REQUIRED` | **422** | Agent 缺少/不可用 modelConfig；**不建 session / 不写本轮 Draft** |
| （guard failure） | **422** | 幻觉 `toolId`、非法节点类型、缺 Start/End、超 maxNodes、非 `workflow.graph.v1` 等；body 含 `guardReport`；**不覆盖**上一轮合法 Draft |
| （session closed） | **409** | close 之后再 `POST .../turns` |
| （bad message / bad request） | **400** | message 非法、未知字段滥用（含请求体 `modelConfigId` 绕过）等 |
| （authz） | **403** | 无 workspace / 资源写权限 |
| （not found） | **404** | session / workflow / revision 不存在（或不可见） |

实现侧可将 guard 细分为稳定 code（如 `GUARD_REJECTED`、`HALLUCINATED_TOOL`）；**契约最低要求**是 HTTP + 可机读报告 + 不 clobber Draft。

---

## 5. 与设计章节映射

| 本草案 | 设计 |
|--------|------|
| §1 Generate sessions | §6.1.2 |
| §2 Production execute + E1 | §6.2.1 / §6.2.2 / D13 |
| §3 Bind | §6.3.1 / D12 |
| Error codes | §6.1.2 失败表 + D2 |

---

## 6. 变更记录

| 版本 | 日期 | 说明 |
|------|------|------|
| 0 | 2026-07-23 | PR-CL0 / T-P0：书面契约锚点，无 handler 实现 |
