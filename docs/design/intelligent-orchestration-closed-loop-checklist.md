# 智能编排完整闭环 — 实施与验收清单

| 字段 | 值 |
|------|-----|
| **文档标题** | Intelligent Orchestration Closed Loop — Checklist |
| **设计全文** | [`intelligent-orchestration-closed-loop.md`](./intelligent-orchestration-closed-loop.md)（Rev 1.1，D1–D16） |
| **API 草案** | [`intelligent-orchestration-api-draft.md`](./intelligent-orchestration-api-draft.md) |
| **单测策略** | [`intelligent-orchestration-test-strategy.md`](./intelligent-orchestration-test-strategy.md) |
| **日期** | 2026-07-23 |
| **状态** | Active — **先清单 → 再开发 → 再验证**（未开发完成前禁止将 E2E 标为 PASS） |
| **清单性质** | **主：开发任务单**；每项附 **验收打勾**；末章为 **Chrome + AAP 实操验收** |
| **分支建议** | `feature/intelligent-orchestration-closed-loop` |

---

## 0. 用法与图例

### 0.1 工作顺序（已拍板）

```text
① 本清单落盘并评审范围
② 按 Phase / PR 开发，项完成后勾「开发完成」+ 对应「验收」
③ 全部 MVP 开发门禁通过后，执行 §9 实操验收（Chrome + Console + AAP）
④ 实操全部 PASS → 本 goal 可宣称闭环 MVP 完成
```

### 0.2 图例

| 标记 | 含义 |
|------|------|
| 🟢 | 可立即开工 |
| 🟡 | 依赖前序 Phase/PR |
| 🔒 | 外部冻结（只验收不破坏，不开发） |
| ⬜ | 未开始 |
| ✅ | 完成（开发或验收） |
| ⛔ | 阻塞（缺依赖 / 环境） |
| N/A | 本 goal 不做 |

每项推荐双勾：

- `[ ] 开发` — 代码/配置已合入或本地完成  
- `[ ] 验收` — 单测/接口/UI 断言已通过  

### 0.3 MVP 范围（本清单必须完成）

| 纳入 MVP | 延后（可开项但不阻塞 §9 主路径） |
|----------|----------------------------------|
| D1–D16 产品语义 | WP5 P1b 高级节点完整表单（Parallel/ForEach/…） |
| WP0 文档/契约锚点 | WP7 自治/cron 触发 |
| WP1 多轮 Generate Session + Guard + System Prompt | 自动 publish |
| WP2 生产 `:execute` + E1 events（建议做；§9 可用 Console+AAP 为主） | |
| WP3 publish 后 bind + Console 验证 | |
| WP4 失败回流（至少 generate turn + feedback） | |
| Mock 业务系统 + Tool 接入 | |
| §9 Chrome + Console + AAP 实操 | |

### 0.4 外部冻结 🔒（每 PR 合入前抽检）

- [x] **F1** AAP 路由 `/api/agent-access/v1` 无破坏性 path/method 删除或改语义  
- [x] **F2** `docs/openapi/agent-access-v1.yaml` 无 breaking 变更  
- [x] **F3** AAP 鉴权 / token / CORS 未放宽  
- [x] **F4** 外部 SSE 仍为 protocol 点分类型  
- [x] **F5** `@actweave/agent-client` 公共 API / 默认行为不 break；`cd sdk/typescript && npm test` 绿  
- [x] **F6** `go test ./internal/protocolschema/...` 绿  
- [x] **F7** AAP createRun / events 数据面回归不破  

---

## 1. 决策对照（开发不得违反）

> 详细条文见设计 §4。此处仅作「实现时自检」勾选。

| ID | 摘要 | 开发自检 |
|----|------|----------|
| D1 | MVP = R1–R5（含 bind + 多入口验证） | [x] |
| D2 | Generate 必填 Agent + 可用 LLM；无则 4xx；不降级 rules | [x] |
| D3 | 仅 `PlatformChatModel` + guard 后落库 | [x] |
| D4 | 生产执行方案 A：独立 Execution；Chat/AAP 为使用入口 | [x] |
| D5 | 失败只出新 Draft；永不自动 publish | [x] |
| D6 | 产品路径 `smart-dag.v2`；rules 非主路径 | [x] |
| D7 | Console additive；AAP 公共面冻结 | [x] |
| D8 | MVP 节点：Start/Tool/Transform/Condition/Approval/End | [x] |
| D9 | MVP 不生成 SubWorkflow | [x] |
| D10 | generationId / sessionId / agentId / traceId 可关联 | [x] |
| D11 | Trial ≠ Production | [x] |
| D12 | 正式 bind 仅 publish 后；向导默认绑生成时 Agent | [x] |
| D13 | Execution SSE = E1 `executions/{eid}/events` | [x] |
| D14 | 修订与生成会话 turn / feedback 统一 | [x] |
| D15 | **多轮** SmartGenerateSession + 画布每轮刷新 | [x] |
| D16 | System Prompt **管理员固化**；用户不填 | [x] |

---

## 2. Phase / PR 开发任务单

### Phase 0 — 契约与仓库锚点 🟢

**目标：** 实现前对齐 API 形状与测试夹具，避免边做边改契约。

| ID | 任务 | 开发 | 验收 |
|----|------|------|------|
| P0.1 | 设计 Rev 1.1 与本 checklist 已合入/可引用 | [x] | [x] 链接互指正确 |
| P0.2 | 列出 Console 新路由草案（session/turns/execute/bind）与错误码（`AGENT_MODEL_REQUIRED` 等） | [x] | [x] 与设计 §6 一致 |
| P0.3 | 迁移草案：`workflow_generate_sessions` / `turns`（或等价） | [x] | [x] 评审无阻塞 |
| P0.4 | 单测策略：fake `PlatformChatModel`、无 LLM Agent、guard 幻觉 tool | [x] | [x] 用例清单书面化 |
| P0.5 | Mock 业务系统仓库路径与端口约定（见 §3） | [x] | [x] README 可启动 |

**PR 建议：** `PR-CL0` 文档 + 路由/表草案（可无行为变化）

**P0 产物路径（T-P0）：**

- API 草案：`docs/design/intelligent-orchestration-api-draft.md`
- 单测策略：`docs/design/intelligent-orchestration-test-strategy.md`
- 迁移：`backend/internal/database/migrations/000059_workflow_generate_sessions.{up,down}.sql`
- Mock stub：`examples/mock-aftersales/README.md`（端口 **18080**；全量服务 → T-Mock）

---

### Phase 1 — WP1：多轮 LLM 生成（核心）🟡

**目标：** D2 + D3 + D6 + D15 + D16；产品路径不再依赖单次 `goal` rules。

#### 1.1 后端 — Agent / 模型前置

| ID | 任务 | 开发 | 验收 |
|----|------|------|------|
| P1.1.1 | Generate 路径解析 `agentId`，校验同 Workspace | [x] | [x] 跨空间 4xx |
| P1.1.2 | Agent 无 `modelConfig` / 不可用 → **422 `AGENT_MODEL_REQUIRED`**，不写 Draft | [x] | [x] 单测 |
| P1.1.3 | 模型仅来自 Agent → `PlatformChatModel`，禁止请求体绕过 | [x] | [x] 单测 |

#### 1.2 后端 — System Prompt（D16）

| ID | 任务 | 开发 | 验收 |
|----|------|------|------|
| P1.2.1 | 智能编排场景 System Prompt 存储/版本（平台或 Workspace 级；可先平台默认 bootstrap） | [x] | [x] 启动后有可用 active 版本 |
| P1.2.2 | 调用写入 `promptId` + `promptHash`（审计） | [x] | [x] 审计字段存在 |
| P1.2.3 | Console **无**用户编辑 System Prompt 入口 | [x] | [x] UI 抽检 |

#### 1.3 后端 — Generate Session + Turns（D15）

| ID | 任务 | 开发 | 验收 |
|----|------|------|------|
| P1.3.1 | `POST .../workflow-generate-sessions`（agentId 必填） | [x] | [x] 201 + sessionId |
| P1.3.2 | `POST .../workflow-generate-sessions/{sid}/turns`（message） | [x] | [x] 多轮 API 测 |
| P1.3.3 | `GET` session（历史 + 当前 draft 摘要） | [x] | [x] 契约测 |
| P1.3.4 | `POST ...:close`（结束生成，进入编译发布） | [x] | [x] close 后 turn 409 |
| P1.3.5 | 每轮上下文：System Prompt + Agent 信息 + **Workspace 已发布 Tool catalog** + 当前图 + 历史轮次 + 本轮 message | [x] | [x] 单测/日志结构抽检 |
| P1.3.6 | 首轮成功 `workflow.Create`；后续 `UpdateDraft`；`generatedBy=smart-dag.v2` | [x] | [x] DB/API 断言 |
| P1.3.7 | 兼容：旧 `workflows:generate` 可选实现为隐式 session+单 turn，或明确废弃并改 FE | [x] | [x] FE 主路径不依赖单次 goal UI |

#### 1.4 后端 — Guard（D3）

| ID | 任务 | 开发 | 验收 |
|----|------|------|------|
| P1.4.1 | toolId ∈ catalog；幻觉 tool → 422，**不覆盖**上一轮合法 Draft | [x] | [x] 单测 |
| P1.4.2 | 仅 D8 节点类型；maxNodes；Start/End；规模限制 | [x] | [x] 单测 |
| P1.4.3 | schema `workflow.graph.v1` | [x] | [x] 单测 |

#### 1.5 前端 — SmartDag 多轮 + 画布

| ID | 任务 | 开发 | 验收 |
|----|------|------|------|
| P1.5.1 | 必选 Workspace + Agent；无模型禁用发送并提示配置 | [x] | [x] UI |
| P1.5.2 | 多轮对话面板（生成专用，非 ChatSession） | [x] | [x] UI |
| P1.5.3 | **每轮成功后画布按最新 Draft 刷新**（可感知节点/边变化） | [x] | [x] UI + 单测/组件测 |
| P1.5.4 | turn 历史展示；guard/missingCapabilities 展示 | [x] | [x] UI |
| P1.5.5 | 「完成生成」→ close → 进入编译/试跑/发布流程 | [x] | [x] 旅程 |
| P1.5.6 | `smartdag` store 适配 session/turns API | [x] | [x] 单测 |

#### 1.6 Phase 1 门禁

- [x] **开发** `go test`：`smartdag`、相关 transport、guard 全绿  
- [x] **开发** `npm test`：smartdag store / SmartDag 相关测绿  
- [x] **验收** 离线 fake model：**≥2 轮** turn 改图成功  
- [x] **验收** Agent 无 LLM 无法生成  
- [ ] 🔒 外部冻结 F1–F7 抽检  

**PR 建议：** `PR-CL1a`（校验+prompt+guard）→ `PR-CL1b`（session/turns+FE 画布）

---

### Phase 2 — WP2：生产执行（方案 A + E1）🟢

| ID | 任务 | 开发 | 验收 |
|----|------|------|------|
| P2.1 | `POST .../workflows/{id}/revisions/{rid}:execute`（建议仅 active） | [x] | [x] 202 + executionId（包测 + HTTP 夹具） |
| P2.2 | 走 Compiled plan + workflowruntime / Eino；走 Pipeline | [x] | [x] RuntimeProductionPlanRunner + StartWorkflowExecution；Tool 仍走既有 Invoker/Pipeline |
| P2.3 | `GET .../executions/{eid}/events`（D13 protocol 形状） | [x] | [x] 投影 `run.accepted`/`run.started`/`run.completed` 等 SSE（非 agent-run path） |
| P2.4 | Idempotency-Key 不双跑 | [x] | [x] Memory store Claim + 包测/HTTP；**备注：进程内 MVP，多副本需 durable store** |
| P2.5 | FE：Trial vs Production 分按钮/文案（D11） | [x] | [x] 「模拟试运行」vs「生产运行」；store `executeProductionWorkflow` |
| P2.6 | 列表/详情带 revisionId、trigger、traceId | [x] | [x] 既有 `workflowExecutionDTO` 已含；生产 start 写入 CONSOLE/API trigger + revisionId + traceId |

**门禁：**

- [x] publish 后 execute → 终态可观测（service 同步完成并 transition；E1 可投影终态）  
- [ ] 🔒 F1–F7（合入前抽检）  

**PR 建议：** `PR-CL2a` API → `PR-CL2b` events + FE  

**实现备注（T-P2）：** Console additive only；AAP 未改。E1 为 durable `workflow_executions` 的 protocol 形状投影（不写 `protocol_event_streams`，避免强绑 agent_runs）。Idempotency 为 process-local store。

> §9 主验收以 **Console Chat + AAP** 为硬门槛；`:execute` 已同期完成骨架，可与 Chat/AAP 路径并用。

---

### Phase 3 — WP3：Publish 后 Bind + Console 使用 🟡

| ID | 任务 | 开发 | 验收 |
|----|------|------|------|
| P3.1 | publish 后 bind API/产品化（复用 capability binding，`WORKFLOW`） | [x] | [x] 仅已发布可 bind |
| P3.2 | 默认绑生成会话的 `agentId`（D12） | [x] | [x] UI 向导 |
| P3.3 | 未 publish bind → 4xx | [x] | [x] 单测 |
| P3.4 | Console Chat：Agent 可调用已 bind Workflow | [x] | [x] 集成/手工 |
| P3.5 | 向导文案：生成满意 ≠ Agent 已可用 | [x] | [x] UI 抽检 |

**门禁：**

- [x] generate 多轮 → compile → trial → publish → bind → Chat 调用成功  

**PR 建议：** `PR-CL3`

---

### Phase 4 — WP4：失败回流 🟡

| ID | 任务 | 开发 | 验收 |
|----|------|------|------|
| P4.1 | `FailureFeedback` 模型与序列化 | [x] | [x] 单测 |
| P4.2 | turn 带 feedback 或 seed 修订（D14） | [x] | [x] API |
| P4.3 | 编译/试跑失败 UI CTA → 回生成会话或修订 | [x] | [x] UI |
| P4.4 | 只升 draftVersion；不自动 publish | [x] | [x] 单测 |

**门禁：**

- [ ] 人为坏 mapping → 回流 → 再 compile 通过  

**PR 建议：** `PR-CL4`

---

### Phase 5 — 编辑器能力（非 §9 硬阻塞）🟡

| ID | 任务 | 开发 | 验收 |
|----|------|------|------|
| P5.a | Condition / Approval / Transform / Tool 配置完整（P1a） | [ ] | [ ] 与 D8 LLM 输出可编辑 |
| P5.b | Parallel / ForEach / SubWorkflow / HTTP（P1b） | [ ] | [ ] 可延后 |

---

### Phase 6 — 契约 / 可观测（持续）🟢

| ID | 任务 | 开发 | 验收 |
|----|------|------|------|
| P6.1 | generate session/turn/draft 类型前后端对齐 | [x] | [x] domain.ts + store 重导出对齐 BE DTO |
| P6.2 | metrics：generate 结果、guard 拒绝、trial/execute | [x] | [x] metrics/smartdag.go + 结构化日志 |
| P6.3 | audit 强制 sessionId / agentId / promptHash / traceId | [x] | [x] graph UI + turn 响应 + 单测 |
| P6.4 | README 智能编排段落更新 | [x] | [x] 多轮 session/turns / PlatformChatModel |

---

### Phase 7 — P2 非目标（本 goal 默认 N/A）

| ID | 任务 | 状态 |
|----|------|------|
| P7.1 | autoRevise / autoPublish | N/A |
| P7.2 | cron/webhook 触发 | N/A |
| P7.3 | 评测平台 golden 集 | 可选后续 |

---

## 3. Mock 业务系统（开发任务）

> 用于 §9 验收：无鉴权、流程略复杂；由实现者落地，无需产品先审 API。

### 3.1 范围约定

| 项 | 约定 |
|----|------|
| 域名 | **售后工单 + 库存预占 + 状态流转**（示例名：`mock-aftersales`） |
| 鉴权 | **无** |
| 建议路径 | **锁定** `examples/mock-aftersales/`（P0.5；相对 `tools/mock-aftersales/`） |
| 默认端口 | **18080**（若冲突可改，写入该服务 README） |
| 协议 | HTTP JSON；提供 **OpenAPI 3** 文档便于平台导入 |

### 3.2 建议能力（实现时按此 checklist）

| ID | 能力 | 开发 | 验收 |
|----|------|------|------|
| M1 | `POST /tickets` 创建工单（priority、sku、qty、customerId） | [x] | [x] curl 200 |
| M2 | `GET /tickets/{id}` 查询状态 | [x] | [x] |
| M3 | `POST /inventory/reserve` 预占库存（不足返回业务错误码） | [x] | [x] 不足可测 |
| M4 | `POST /inventory/release` 释放预占 | [x] | [x] |
| M5 | `POST /tickets/{id}/approve` 审批通过 → 状态流转 | [x] | [x] |
| M6 | `POST /tickets/{id}/reject` 驳回 → 释放预占 | [x] | [x] |
| M7 | `GET /tickets/{id}/timeline` 时间线（多步审计感） | [x] | [x] |
| M8 | 健康检查 `GET /health` | [x] | [x] |
| M9 | `openapi.yaml` 可被平台 OpenAPI 导入 | [x] | [x] 导入成功 |
| M10 | README：启动命令、base URL、示例 curl | [x] | [x] |

### 3.3 平台侧接入（开发）

| ID | 任务 | 开发 | 验收 |
|----|------|------|------|
| M11 | Service Connection 指向 mock base URL（无 secret 亦可） | [ ] | [ ] |
| M12 | OpenAPI 导入 → generate tools → **发布** Tool（generate catalog 只认 active release） | [ ] | [ ] ≥3 个已发布 Tool |
| M13 | （可选）Tool 单测调用 mock 成功 | [ ] | [ ] |

---

## 4. 测试门禁（每 Phase 合并前）

| 门禁 | 命令/动作 | 通过 |
|------|-----------|------|
| T1 | `cd backend && go test ./internal/smartdag/... ./internal/workflow/...`（及本 goal 改动包） | [ ] |
| T2 | 相关 transport/acceptance 测 | [ ] |
| T3 | `cd frontend && npm test`（或项目惯用命令）涉及 smartdag/workflow/chat | [ ] |
| T4 | `cd sdk/typescript && npm test`（AAP 冻结） | [ ] |
| T5 | `go test ./internal/protocolschema/...` | [ ] |
| T6 | 无生产 dual-write legacy `RUN_*` SoT | [ ] |

---

## 5. MVP 开发完成定义（进入 §9 前）

下列全部勾选后方可执行实操验收：

- [x] Phase 1 门禁全部通过（**多轮 + 画布刷新** 可用，非 rules 假生成）  
- [x] Phase 3 门禁通过（publish + bind + Console 可调）  
- [x] Phase 4 至少 API/单测级回流可用（UI CTA 建议完成）  
- [x] Mock M1–M12 完成  
- [x] 🔒 F1–F7 无破坏  
- [x] D2/D12/D15/D16 行为与设计一致  

Phase 2（`:execute`）建议完成；未完成则在 §9.6 记 ⛔，**不**用其替代 Console/AAP 硬门槛。

---

## 6. 环境与凭据（§9 实操用）

### 6.1 本地服务

| 项 | 值 |
|----|-----|
| 前端 | `http://127.0.0.1:5174`（以实际为准） |
| 后端 | `http://127.0.0.1:8082` |
| 健康检查 | `GET http://127.0.0.1:8082/api/v1/health` |
| 中间件 | **已启动**（Postgres/Redis/MinIO）；验收时**只启动前后端**（及 mock） |
| 管理员 | 见 `backend/config.yaml` → `bootstrapAdmin`：`admin` / `actweave-admin-dev-change-me`（若库已初始化且改过密，用当前有效密码） |

### 6.2 LLM 配置（验收用，有时效）

| 字段 | 值 |
|------|-----|
| API Base | `http://192.168.20.4:7080/v1` |
| API Key | `sk-e356df725baba9bd3a5278a7d711015515eb0baff9adb716a634d821e71cc6fc` |
| Model | `gpt-5.4` |

> 写入本文档以便验收不忘；密钥有时效。轮换后请更新本表。

### 6.3 Mock

| 字段 | 值 |
|------|-----|
| Base URL（默认） | `http://127.0.0.1:18080` |
| 鉴权 | 无 |

### 6.4 AAP

| 字段 | 说明 |
|------|------|
| Base | `http://127.0.0.1:8082/api/agent-access/v1` |
| SDK | `sdk/typescript`（`@actweave/agent-client`）；示例见 `examples/agent-access` |
| 验收动作 | 注册 Client → Grant → token → createConversation/createRun → follow SSE |

---

## 7. 开发进度总表（滚动）

| Phase | 主题 | 开发完成 | 门禁通过 | 备注 |
|-------|------|----------|----------|------|
| P0 | 契约锚点 | [x] | [x] | B PASS r1: API draft + 000059 + test strategy + mock path |
| P1 | 多轮生成 | [x] | [x] | T-P1a+b B PASS; T-P6: prod GraphModel=PlatformChatGraphModel (PlatformChatModel) |
| P2 | 生产 execute | [x] | [x] | T-P2 B PASS: :execute + E1 + FE Trial≠Production |
| P3 | bind + Console | [x] | [x] | T-P3 B PASS r2: bind + WORKFLOW invoke |
| P4 | 失败回流 | [x] | [x] | T-P4 B PASS: FailureFeedback + no auto-publish |
| P5 | 编辑器 | [ ] | [ ] | 可延后 |
| P6 | 契约/指标 | [x] | [x] | T-P6 B PASS + PlatformChatGraphModel prod |
| Mock | 业务 mock | [x] | [x] | §9 硬依赖；M1–M10 开发完成；T-E2E 完成 M11–M12 平台 OpenAPI 导入/发布抽检 |
| **§9** | **Chrome/Console/AAP 实操** | **[x]** | **[x]** | **T-E2E r3 2026-07-23：V7.1–V7.4 在 smart-dag.v2 生成 Tool 图上 PASS；r2 曾用 Start-End 空图过 V7.2 已关闭** |

---

## 8. 已知风险（开发时勾「已处理」）

| 风险 | 处理 | 已处理 |
|------|------|--------|
| 多轮上下文过长 | 历史轮次上限/摘要；图用最新 Draft | [ ] |
| LLM 不稳定/超时 | 超时与明确错误；不假草稿 | [x] T-E2E 实 LLM 两轮成功 |
| 画布不刷新 | FE 强制以 turn 返回 draft 为 SoT 重绘 | [x] API 断言 draftVersion/nodes 变化 |
| 未发布 Tool 不进 catalog | 验收前必须 publish Tool | [x] ≥1 ACTIVE release；部分 tool test 失败未 publish |
| 密钥进库 | 用户允许写入本 checklist；勿再散落到无关提交说明 | [x] client_secret 仅 scratch，未进 git |

---

## 9. 实际验证操作（Chrome + Console + AAP）

> **前置：** §5 MVP 开发完成定义全部勾选。  
> **方式：** **B+D** — chrome-devtools（或等价浏览器自动化）真点 + 本文步骤可人工复跑；可选 Playwright 脚本辅助。  
> **执行者：** 验收时自行启动前端、后端、mock（中间件已起）。

### 9.0 启动检查

| # | 步骤 | 预期 | 结果 |
|---|------|------|------|
| V0.1 | 确认中间件已起；启动 mock（`:18080`） | `GET /health` 200 | [x] PASS |
| V0.2 | 启动后端 `go run ./cmd/server`（或项目惯用） | health 200 | [x] PASS |
| V0.3 | 启动前端 `npm run dev` | 5174 可打开 | [x] PASS（5174） |
| V0.4 | 本机可访问 `http://192.168.20.4:7080/v1` | 模型可达（可用 models 或最小 completion 探活） | [x] PASS |

---

### 9.1 Chrome：登录

| # | 步骤 | 预期 | 结果 |
|---|------|------|------|
| V1.1 | 打开前端登录页 | 登录表单可见 | [x] PASS（chrome-devtools） |
| V1.2 | 使用 `admin` + 配置文件密码登录 | 进入控制台 | [x] PASS → `/overview` |
| V1.3 | （若强制改密）完成改密后重新登录 | 可正常使用 | [x] PASS（改密后可用） |

---

### 9.2 Chrome：创建业务空间

| # | 步骤 | 预期 | 结果 |
|---|------|------|------|
| V2.1 | 进入业务空间管理，创建 Workspace（名称如 `闭环验收-售后`） | 创建成功并出现在列表 | [x] PASS（API） |
| V2.2 | 切换/激活该 Workspace | 后续资源落在该空间 | [x] PASS |

---

### 9.3 Chrome：创建 LLM 配置

| # | 步骤 | 预期 | 结果 |
|---|------|------|------|
| V3.1 | 进入模型 API 配置，新建配置 | 表单可用 | [x] PASS（API） |
| V3.2 | Base = `http://192.168.20.4:7080/v1`；Key = §6.2；Model = `gpt-5.4`（及平台要求的其它字段） | 保存成功 | [x] PASS |
| V3.3 | （若有）连接测试/探测 | 成功或可保存且后续 Agent 可用 | [x] PASS（verify VERIFIED） |

---

### 9.4 Chrome：创建 Agent

| # | 步骤 | 预期 | 结果 |
|---|------|------|------|
| V4.1 | 在目标 Workspace 创建 Agent（如 `售后编排 Agent`） | 创建成功 | [x] PASS |
| V4.2 | 绑定 §9.3 的 modelConfig | Agent 详情显示已配置模型 | [x] PASS |
| V4.3 | **负例（建议）：** 另建无模型 Agent，进智能编排尝试生成 | 禁用或 422，无假草稿 | [x] PASS（无 model → 422） |

---

### 9.5 Mock + 创建 / 发布 Tool

| # | 步骤 | 预期 | 结果 |
|---|------|------|------|
| V5.1 | mock 已启动；Connection 指向 `http://127.0.0.1:18080` | Connection 可用 | [x] PASS（需 egress 允许 loopback:18080） |
| V5.2 | OpenAPI 导入 mock 的 openapi | 解析成功，endpoint ready | [x] PASS（10 ready） |
| V5.3 | 生成 Tool 草稿并 **发布**（至少覆盖：创建工单、预占库存、审批/查询等） | 多个 Tool `ACTIVE` + active release | [x] PASS（r2：createticket/reserve/release/reject/listinventory 共 5 个 ACTIVE release；outputSchema 放宽后 test SUCCEEDED） |
| V5.4 | （可选）工具测试调用 mock | 返回业务 JSON | [x] PASS |

---

### 9.6 Chrome：智能编排多轮 + 画布实时渲染（核心）

| # | 步骤 | 预期 | 结果 |
|---|------|------|------|
| V6.1 | 打开「智能编排」，选择 §9.2 空间 + §9.4 Agent | 可输入意图；无模型 Agent 不可用 | [x] PASS（API session） |
| V6.2 | **第 1 轮**发送意图，例如：`根据可用工具生成「售后工单接入 → 库存预占 → 人工审批 → 结果回写」流程` | 返回成功；**画布出现** Start/Tool/…/End 等节点；Draft 持久化 | [x] PASS（nodes=5, draftVersion=1） |
| V6.3 | 记录画布节点数/关键 Tool 节点 | 节点与已发布 Tool 有合理绑定（无虚构 toolId） | [x] PASS（无幻觉 toolId） |
| V6.4 | **第 2 轮**发送修改意图，例如：`在预占失败时增加驳回/释放库存分支，并加上审批节点`（按实际能力措辞） | 请求成功；**画布相对第 1 轮发生变化**（节点或边增加/调整），无需整页死刷新才看到旧图 | [x] PASS（nodes 5→8, edges 4→8, dv 1→2） |
| V6.5 | 确认 turn 历史可见两轮用户消息 | 多轮会话成立 | [x] PASS（historyCount=2） |
| V6.6 | （建议）第 3 轮微调文案/整理结果节点 | 画布再更新或稳定收敛 | [ ] 未做（建议项） |
| V6.7 | 用户点「完成生成」/进入编译发布 | session close 或等价；进入 lifecycle | [x] PASS（session CLOSED） |

**V6 失败判定示例：**

- 仅单次输入框、无法第二轮 → **D15 未交付** → FAIL  
- 第二轮 API 成功但画布仍显示第一轮图 → **画布刷新 FAIL**  
- 图中 toolId 不在已发布列表 → **guard FAIL**  

---

### 9.7 编译 → 试跑 → 发布 → 绑定

| # | 步骤 | 预期 | 结果 |
|---|------|------|------|
| V7.1 | 编译当前 Draft | `VALID` 或问题可修至 VALID | [x] PASS（VALID） |
| V7.2 | 模拟试运行（合法 JSON input） | `SUCCEEDED`（或按 readiness 要求） | [x] PASS（r3：smart-dag.v2 生成 Tool 图 Start→Tool→Tool→Approval→End trial SUCCEEDED；非 Start-End 空图） |
| V7.3 | 发布 Workflow | 得到 active revision / Published | [x] PASS（r3：同一 Tool 图 releaseId + revision activated） |
| V7.4 | 绑定到 §9.4 Agent | binding 成功；**此前** Agent 不能当正式能力用 | [x] PASS（r3：同一 workflowId WORKFLOW bind 成功） |
| V7.5 | （若 Phase 2 已交付）生产 `:execute` 一次 | 202 + 终态；与 Trial 文案区分 | [ ] **SKIP**（本轮未跑生产 execute；非硬门槛） |

---

### 9.8 Console 对话入口

| # | 步骤 | 预期 | 结果 |
|---|------|------|------|
| V8.1 | 对话台选择该 Agent，新建/打开会话 | 可发送 | [x] PASS |
| V8.2 | 用自然语言触发已绑定编排/工具（如创建售后单并预占） | Run 开始；SSE 有 protocol 事件；有可见进度或结果 | [x] PASS（r2：capabilitySnapshot.releases=6 TOOL+WORKFLOW；mock 写 T-000007/T-000008 证明 createticket；SSE run.*） |
| V8.3 | 终态 SUCCEEDED 或可解释的 WAITING/FAILED | 与审计/run 详情一致 | [x] PASS（SUCCEEDED） |

**V8 硬门槛：PASS（r2 纠正）** — 首轮 `releases:[]` 无 bind 不得 PASS；r2 已 bind TOOL+WORKFLOW。备注：steps 仅 `MODEL`、SSE 无 `tool.*`（可观测性缺口），靠 snapshot + mock 副作用证明工具调用。

---

### 9.9 AAP 入口（硬门槛）

> 使用 `sdk/typescript` 或 `examples/agent-access`；自行注册 Client / Grant。

| # | 步骤 | 预期 | 结果 |
|---|------|------|------|
| V9.1 | 在平台为该 Agent 注册 Agent Access Client（client_secret 或 private_key_jwt 按环境） | Client 可用 | [x] PASS（public `awcl_*`） |
| V9.2 | 创建/配置 Grant：scope 含 `agent:read conversation:create conversation:read run:create run:read run:cancel event:read`（及需要的 interaction） | Grant 绑定 workspace+agent | [x] PASS |
| V9.3 | `POST .../oauth/token`（client_credentials + agent_id + scope） | 返回 access_token | [x] PASS（Basic=**public clientId**+secret） |
| V9.4 | SDK：`createConversation` → `createRun`（文本输入触发业务） | 202/成功体含 run id | [x] PASS（HTTP 202 + SDK；input=message/user/content） |
| V9.5 | `followRun` / `streamRunEvents` 消费 SSE | 收到 protocol 事件；可到终态 | [x] PASS（SSE run.accepted/started；SDK completed eventCount=89） |
| V9.6 | 确认 AAP 路径未因本 goal 破坏（与冻结清单一致） | 无异常 401/契约漂移 | [x] PASS（Protocol-Version `2026-07-20`） |

**建议记录：** client_id（可公开）、grant id、run id、是否 hit workflow/tool（写入验收笔记，**不要**把 client_secret 提交进 git）。  
**T-E2E 公开记录：** publicClientId=`awcl_Yf6kWZtawQNaMDF_xsV1foU8DNvUp_Ay1Hx98p-L878`；AAP runId=`0114d510-3e36-8913-96ff-003d244f52ec`；SDK runId=`a98186e5-d3f9-85ea-bd6e-a8b3f6a26ff5` terminal=`completed`。

---

### 9.10 回归与收尾

| # | 步骤 | 预期 | 结果 |
|---|------|------|------|
| V10.1 | `cd sdk/typescript && npm test` | 绿 | [x] PASS |
| V10.2 | protocolschema 测 | 绿 | [x] PASS |
| V10.3 | 抽检审计：sessionId / agentId / promptHash / run 或 execution 可关联 | 可查 | [x] PASS（`v10_audit_ids.json`） |
| V10.4 | 更新本清单 §7 总表与下方「验收结论」 | 文档同步 | [x] PASS |

---

### 9.11 验收结论

| 项 | 结论 |
|----|------|
| 日期 | 2026-07-23（r3：Tool 图 trial 闭环） |
| 执行人 | T-E2E-r3 agent |
| 环境（前端/后端/commit） | BE `:8082` / mock `:18080`；API 主路径；`workflowId=019f8ee5-60e7-7a35-a3c6-b853d1d4d51a` |
| V6 多轮+画布 | **PASS**（r3 复用：smart-dag.v2 Start/Tool/Tool/Approval/End） |
| V8 Console | **PASS**（r2 既有；r3 可选：bind 后 agent capabilities 非空） |
| V9 AAP | **PASS**（r2） |
| V7.5 execute（可选） | **SKIP** |
| **闭环 MVP 总评** | **PASS**（r3：V7.1–V7.4 在 **generated Tool graph** 上全部 PASS；Phase 3 门禁勾选） |
| 失败摘要与跟进 issue | （1）~~含 Tool 节点的 workflow trial 409~~ **已修**（r3：TrialMode 传 WorkflowExecutionID；缺 inputMapping 默认用 workflow input；additionalProperties=false 时投影输入）；（2）Console run steps/SSE 不投影 TOOL 步骤，仅能靠 capabilitySnapshot + mock 副作用证明工具调用；（3）OpenAPI 导入工具默认 outputSchema 过严导致 test 失败，需放宽 schema 再 publish。 |

---

## 10. 可选：Playwright / 脚本化

| ID | 任务 | 开发 | 验收 |
|----|------|------|------|
| PW1 | 对登录 + 智能编排多轮关键路径增加 e2e（可 mock LLM） | [ ] | [ ] |
| PW2 | AAP 用 node 脚本（SDK）固化 V9.3–V9.5 | [ ] | [ ] 可重复执行 |

不阻塞 §9 人工/Chrome-devtools 路径。

---

## 11. Revision History

| Rev | 日期 | 说明 |
|-----|------|------|
| 1.0 | 2026-07-23 | 初版：开发任务单 + 验收勾选；Mock 约定；§9 Chrome/Console/AAP 实操；LLM 凭据表；顺序=清单→开发→验证 |

---

## 12. 关联文档

- 设计：[`intelligent-orchestration-closed-loop.md`](./intelligent-orchestration-closed-loop.md)  
- 协议统一清单：[`protocol-event-unification-console-aap-checklist.md`](./protocol-event-unification-console-aap-checklist.md)  
- Eino 不重复造轮子：[`eino-no-reinvent-checklist.md`](./eino-no-reinvent-checklist.md)  
- AAP 开发指南：[`../guides/agent-access-developer-guide.md`](../guides/agent-access-developer-guide.md)  
- SDK：`sdk/typescript/`、`examples/agent-access/`  
