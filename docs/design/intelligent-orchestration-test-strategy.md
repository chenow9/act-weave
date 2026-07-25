# 智能编排 — 单元/集成测试策略（Phase 0）

| 字段 | 值 |
|------|-----|
| **文档标题** | Intelligent Orchestration — Test Strategy |
| **状态** | Draft（PR-CL0 / T-P0；用例清单书面化，测试代码随 WP1+ 落地） |
| **日期** | 2026-07-23 |
| **设计** | [`intelligent-orchestration-closed-loop.md`](./intelligent-orchestration-closed-loop.md) |
| **API 草案** | [`intelligent-orchestration-api-draft.md`](./intelligent-orchestration-api-draft.md) |
| **清单** | [`intelligent-orchestration-closed-loop-checklist.md`](./intelligent-orchestration-closed-loop-checklist.md) Phase 1 / §4 T1–T6 |

---

## 1. 目标

在实现 WP1（多轮 Generate Session + Guard + System Prompt）前，锁定 **可验收的单测/契约测清单**，避免实现期漂移。本文件 **不** 替代 `go test` / `npm test` 本身；Phase 1 门禁要求下列用例落地且绿。

---

## 2. 代码锚点（包 / 模块）

| 层 | 路径 | 职责 |
|----|------|------|
| 生成核心 | `backend/internal/smartdag/` | Session/Turn 服务、LLM 生成、deterministic Guard |
| 模型 | `backend/internal/modelapi/platform_chat_model.go` | 唯一 LLM 入口；测试用 **fake `PlatformChatModel`** |
| Workflow 资产 | `backend/internal/workflow/` | Create / UpdateDraft / compile / publish |
| HTTP | `backend/internal/transport/http/workflow.go`（及后续 session handlers） | Console 路由、错误码映射 |
| Capability bind | `backend/internal/capability/binding_repository.go` | WORKFLOW bind（WP3） |
| FE store | `frontend/src/stores/smartdag.ts` | session/turns 适配、画布刷新 SoT |
| FE 视图 | `frontend/src/views/SmartDagView.vue` | 多轮 UI、无模型禁用、完成生成 close |
| 协议（不破坏） | `backend/internal/protocolschema/`、`docs/openapi/agent-access-v1.yaml`、`sdk/typescript` | F1–F7 回归 |

---

## 3. 计划用例（必须）

### 3.1 fake `PlatformChatModel` — 多轮构图与修订

| 项 | 内容 |
|----|------|
| **ID** | UT-GEN-MULTI-TURN |
| **包** | `backend/internal/smartdag/`（主）；transport 契约测可选 |
| **安排** | fake model：turn1 输出合法 Start→Tool→End 图；turn2 用户「加审批」→ 插入 `Approval` 节点边更新 |
| **断言** | 两轮均 guard 通过；`draftVersion` **递增**；同一 `workflowId`；`generatedBy=smart-dag.v2`；session 历史含 2 turns；返回含 `turnId` / `generationId` |
| **对应** | D3 / D8 / D15；checklist P1.3.x / P1.5.3 |

### 3.2 Agent 无 LLM → `AGENT_MODEL_REQUIRED`

| 项 | 内容 |
|----|------|
| **ID** | UT-GEN-NO-MODEL |
| **包** | `smartdag` + transport |
| **安排** | Agent 无 `modelConfig` 或 config 不可用 |
| **断言** | `POST .../workflow-generate-sessions` → **422** `AGENT_MODEL_REQUIRED`，**无 session 行**；若已有 session 但模型中途失效，turn → 422 同 code，**不写 Draft** |
| **禁止** | 请求体 `modelConfigId` 绕过；rules 静默 201 |
| **对应** | D2；P1.1.2 / P1.1.3 |

### 3.3 Guard 幻觉 toolId — 不 clobber 合法 Draft

| 项 | 内容 |
|----|------|
| **ID** | UT-GUARD-HALLUCINATED-TOOL |
| **包** | `smartdag` Guard + session 服务 |
| **安排** | turn1 成功写入 Draft；turn2 fake model 输出 **catalog 外** `toolId` |
| **断言** | turn2 **422** + `guardReport`；DB 中 Draft graph / `draftVersion` **仍为 turn1**；可记 failed turn 历史；session 仍 `OPEN` 可再试 |
| **对应** | D3；P1.4.1 |

### 3.4 Catalog-only tools、D8 节点、maxNodes、Start/End

| 项 | 内容 |
|----|------|
| **ID** | UT-GUARD-GRAPH-SHAPE |
| **包** | `smartdag` Guard 单测表驱动 |
| **断言** | 仅 catalog 内 toolId；仅 D8：`Start` / `Tool` / `Transform` / `Condition` / `Approval` / `End`；`maxNodes` 超限拒绝；缺 Start 或 End 拒绝；schema `workflow.graph.v1`；MVP **无** SubWorkflow（D9） |
| **对应** | D8 / D9；P1.4.2 / P1.4.3 |

### 3.5 System Prompt D16 — active 版本 + 审计

| 项 | 内容 |
|----|------|
| **ID** | UT-PROMPT-D16 |
| **包** | `smartdag` / prompt 配置存储 |
| **断言** | 调用使用 **active** 智能编排 System Prompt 版本；审计字段含 `promptId` + `promptHash`；生成请求/响应 **无** 用户可改 system prompt 字段 |
| **FE** | SmartDag **无** 用户 System Prompt 编辑器（UI 抽检 / 组件测） |
| **对应** | D16；P1.2.1–P1.2.3 |

### 3.6 Close session → 后续 turn 409

| 项 | 内容 |
|----|------|
| **ID** | UT-SESSION-CLOSE |
| **包** | `smartdag` + transport |
| **安排** | create → turn 成功 → `POST ...:close` → 再 `POST .../turns` |
| **断言** | close 后 status `CLOSED`；后续 turn **409**；Draft 仍可读 |
| **对应** | P1.3.4 |

### 3.7 （可选）跨 workspace Agent

| 项 | 内容 |
|----|------|
| **ID** | UT-GEN-CROSS-WS |
| **断言** | `agentId` 属于其他 workspace → **4xx**（404/422），不建 session |
| **对应** | P1.1.1 |

---

## 4. 扩展用例（WP2+，本 Phase 仅登记）

| ID | 主题 | 要点 |
|----|------|------|
| UT-EXEC-202 | 生产 `:execute` | active revision → 202 + `executionId`；非 active/未发布 4xx |
| UT-EXEC-IDEM | Idempotency-Key | 重复提交不双跑 |
| UT-EXEC-E1 | `GET .../executions/{eid}/events` | protocol 形状帧；终态可观测 |
| UT-BIND-PUBLISHED | bind | 未发布 revision → 4xx；publish 后 WORKFLOW bind 成功 |
| UT-FEEDBACK-TURN | 失败回流 | turn + `feedback` 修订出新 Draft，不 auto-publish（D5/D14） |
| FE-SD-CANVAS | 画布刷新 | 每轮成功后 store 以 turn 返回 draft 为 SoT 重绘 |
| FE-SD-NO-MODEL | 无模型 UI | 禁用发送 + 引导配置 Agent LLM |
| F1–F7 | AAP 冻结 | `go test ./internal/protocolschema/...`；`cd sdk/typescript && npm test` |

---

## 5. Fake 策略

```text
PlatformChatModel (interface / inject)
  └─ FakePlatformChatModel
        - scripted responses by turn index or prompt substring
        - 可注入 malformed JSON / 幻觉 toolId / 合法图
        - 禁止单测打真实外网 LLM（验收 §9 才用真实模型）
```

- Catalog：内存已发布 Tool 列表（id/name/slug/schema 摘要）。
- Agent fixture：有/无 `modelConfigId`；跨 workspace 第二 workspace。
- Prompt fixture：固定 active `promptId`/`promptHash`。

---

## 6. 门禁命令（实现后）

| 门禁 | 命令 |
|------|------|
| T1 | `cd backend && go test ./internal/smartdag/... ./internal/workflow/...` |
| T2 | 相关 transport / acceptance |
| T3 | `cd frontend && npm test`（smartdag store / SmartDag） |
| T4–T5 | AAP / protocolschema 冻结 |
| 迁移 | `go test` 触及 `database.MigrateToLatest` 的包在 schema 变更后保持 latest version 断言同步 |

**T-P0 本身：** 仅文档 + 空表迁移；无业务 handler 时 **不要求** 新增 smartdag 行为测。迁移加入后须保证 `MigrateToLatest` 相关测的 **version 数字** 与最新 migration 一致。

---

## 7. 变更记录

| 版本 | 日期 | 说明 |
|------|------|------|
| 0 | 2026-07-23 | PR-CL0 / T-P0：书面用例清单 |
