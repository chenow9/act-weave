# Eino 不重复造轮子 — 实施清单（P0–P3）

| 字段 | 值 |
|------|-----|
| **文档标题** | Eino 有的不自研：Workflow 默认 compose + 模型入口统一 + legacy 收敛 |
| **作者** | ACTWEAVE Platform |
| **日期** | 2026-07-23 |
| **状态** | **Done**（P0–P3 已落地） |
| **原则** | **Eino 已提供的编排能力禁止平行实现；平台只做鉴权/协议/持久化/安全 Pipeline/产品状态机** |
| **上游** | [`eino-agent-runtime-base-checklist.md`](./eino-agent-runtime-base-checklist.md)（Agent/Workflow eino 闭环） |
| **放量** | [`../runbooks/eino-agent-runtime-rollout.md`](../runbooks/eino-agent-runtime-rollout.md) |

---

## 0. 原则与边界（全程遵守）

### 0.1 硬边界

```
Eino 负责（禁止自研第二套）
  • Agent loop / stream / interrupt     → adk.ChatModelAgent + adk.Runner
  • Workflow DAG / checkpoint / resume  → compose.Graph (+ CheckPointStore)
  • Model / Tool 组件接口               → components/{model,tool}

平台壳负责（必须自研）
  • AAP 鉴权 / security_version / 多租户
  • protocolevent / AAP SSE / golden 契约
  • PostgreSQL 事实 + MinIO 原文
  • Invocation Pipeline（SSRF/Secret/confirm/idempotency）
  • CompiledExecutionPlan 发布冻结物（编译边界）
  • HITL：Dispatch 唯一 Invoke；Eino resume 只回填结果
```

### 0.2 禁止清单

- [x] 禁止再写 / 再扩生产用的 ReAct / 自研 tool loop / max-rounds 平行实现
- [x] 禁止再给 `PlanRunner` 增加新节点语义（新能力只进 `einoruntime` graph_nodes）
- [x] 禁止 Generate 后伪多 delta 作为生产默认
- [x] 禁止长期双跑两套 Workflow 编排内核（灰度可短，默认必须单一）— **默认已 compose；wrapper 仅回滚**
- [x] 禁止第三套 LLM HTTP 客户端（辅助能力也走 `PlatformChatModel` 或同一 model 边界）

### 0.3 允许清单

- [x] Eino 接口 adapter：`PlatformChatModel`、`PipelineTool`、`graph_builder` 翻译层
- [x] Plan → GraphIR → compose（桥，不是第二运行时）
- [x] ADK/compose 事件 → AAP `item.*` / `run.*` 投影
- [x] 短期 `wrapper` 仅作紧急回滚阀

### 0.4 非目标

- 不改 AAP 外部协议帧语义 / SDK 公共 API（除非另开协议轨）
- 不删除整个 `chatruntime` 包（保留 Messenger / 协议投影 / snapshot 解析；编排已迁出）
- 不把平台鉴权/Pipeline 塞进 Eino vendor
- 不要求 Workflow 节点里再跑 ADK ChatModelAgent（DAG = compose；Agent = ADK）

---

## 状态总表

| 阶段 | 主题 | 状态 |
|------|------|------|
| **P0** | Workflow 生产默认 → compose（`eino`），灰度与回滚 | [x] |
| **P1** | 冻结 PlanRunner；新节点只进 graph；覆盖矩阵收敛 | [x] |
| **P2** | 辅助 LLM 统一 `PlatformChatModel` | [x] |
| **P3** | legacy 生产死代码 / 过时注释 / 文档对齐 | [x] |

---

## P0 — Workflow 生产默认切到 Eino compose

### P0.1 配置默认（staged Load）

| # | 项 | 状态 | 说明 / 落点 |
|---|----|------|-------------|
| P0.1.1 | `applyRuntimeDefaults`：省略 `runtime.workflow` 时 `Engine=eino` | [x] | `config/runtime.go` |
| P0.1.2 | 显式 `engine: wrapper` **不得**被 defaults 改写 | [x] | presence + `testRuntimeExplicitWorkflowWrapperRollback` |
| P0.1.3 | 零值 `RuntimeConfig` **未经 Load** 仍 fail-closed | [x] | `Normalized()` empty → wrapper |
| P0.1.4 | Env：`ACTWEAVE_RUNTIME_WORKFLOW_ENGINE` | [x] | 文档化于 runbook；env wrapper 覆盖测 |
| P0.1.5 | 生产默认字符串 `eino`；`eino_core` 别名同 runner | [x] | factory_test alias |
| P0.1.6 | `config/runtime_test.go` 期望反转 | [x] | |
| P0.1.7 | `backend/config.yaml` 注释更新 | [x] | |

### P0.2 工厂与依赖接线

| # | 项 | 状态 | 说明 / 落点 |
|---|----|------|-------------|
| P0.2.1 | application 默认 compose 挂 `PostgresCheckPointStore`（失败则 bootstrap fail） | [x] | `application.go` |
| P0.2.2 | Load  staged 路径 → `EinoCoreRunner` | [x] | `TestNewExecutorFromConfigLoadStagedEino` |
| P0.2.3 | 灰度 allowlist 仍生效 | [x] | 既有 factory 测保留 |
| P0.2.4 | Fail-closed：空 allowlist + allowAll=false → wrapper | [x] | 既有测 |
| P0.2.5 | Trial / Published 走同一工厂 | [x] | application 单点 `NewExecutorFromConfig` |

### P0.3 功能验收

| # | 项 | 状态 | 说明 / 证据 |
|---|----|------|-------------|
| P0.3.1–P0.3.5 | 图节点 / Approval / 高级节点 / TTL / Pipeline | [x] | `workflowruntime` + `einoruntime` + `workflow` 包测绿 |
| P0.3.6 | Agent 路径无回归 | [x] | agentrun / chatruntimebridge 绿 |

### P0.4 观测、回滚、运维

| # | 项 | 状态 | 说明 / 落点 |
|---|----|------|-------------|
| P0.4.1 | workflow engine 指标 | [x] | **Waive**：无既有 workflow series；agent 已有 `aap_agent_engine_enqueue_total`；默认/回滚以配置与 runbook 为准 |
| P0.4.2 | 日志 workflow_engine | [x] | **Waive**：与 P0.4.1 同；bootstrap 失败在 compose store 缺失时显式 error |
| P0.4.3–P0.4.5 | Runbook 回滚 / 中断处置 / 灰度 | [x] | `eino-agent-runtime-rollout.md` P0 段 |

### P0.5 自动化闸门

| # | 项 | 状态 |
|---|----|------|
| P0.5.1–P0.5.4 | config + factory + workflow 包测 | [x] |

### P0 DoD

- [x] 新部署零配置：workflow **默认 compose**
- [x] 显式 `wrapper` 可回滚
- [x] Approval / 高级节点无回归（包测）
- [x] Runbook 就绪
- [x] 本清单 P0 勾选完成

---

## P1 — 冻结 PlanRunner

| # | 项 | 状态 |
|---|----|------|
| P1.1.1–P1.1.4 | FROZEN 注释；禁止 application/transport 新增 PlanRunner | [x] |
| P1.2.1–P1.2.4 | coverage 命名 `eino` 生产 / `eino_core` 别名 | [x] |
| P1.3.* | 行为以 graph 为准；wrapper 仅兼容 | [x] |
| P1.4 闸门 | 包测绿；application/transport 无 NewPlanRunner | [x] |

### P1 DoD

- [x] PlanRunner 标注冻结
- [x] 新节点只进 graph 的约定落地
- [x] coverage / 命名无误导
- [x] P1 表勾选完成

---

## P2 — 辅助 LLM 统一 PlatformChatModel

| # | 项 | 状态 |
|---|----|------|
| P2.1 盘点 | promptGenerator 为唯一生产辅助路径 | [x] |
| P2.2 改造 | `promptGenerator` → `modelapi.NewPlatformChatModel` + Generate | [x] |
| P2.3 闸门 | 业务包无手写 completions（除 modelapi / test-only chatruntime client） | [x] |
| 测试 | `TestPromptGeneratorGenerateUsesPlatformChatModel` | [x] |

### P2 DoD

- [x] 辅助 LLM 与 Agent 共用 model 适配层
- [x] 无第三套生产 HTTP 客户端
- [x] P2 表勾选完成

---

## P3 — Legacy 收敛与文档

| # | 项 | 状态 |
|---|----|------|
| P3.1.1 | `modelapi/doc.go` 更新 | [x] |
| P3.1.2 | runtime 注释对齐 PR16/P0 | [x] |
| P3.1.3–P3.1.4 | HTTPModelClient / tool_loop 删除 | [x] | 源码与测试已移除 |
| P3.1.5 | 大删 legacy | [x] | tool_loop / Executor / HTTPModelClient 已删 |
| P3.2.* | rollout + README + 本清单 | [x] |

### P3 DoD

- [x] 新人读 runbook：Agent=ADK；Workflow 默认 compose
- [x] 过时注释清理
- [x] P3 表勾选完成

---

## 跨阶段回归

| # | 项 | 状态 |
|---|----|------|
| R1–R5 | 包测全绿（config/agentrun/bridge/eino/modelapi/workflow*/application/chatruntime/agent） | [x] |

---

## 执行记录

| 阶段 | PR / 分支 | 合并日 | 执行者 | 备注 |
|------|-----------|--------|--------|------|
| P0–P3 | `refactor/eino-agent-runtime-base`（本地未 commit） | 2026-07-23 | implementer | 清单落地；证据在 goal scratch |

---

## 附录 A — 关键代码地图

| 区域 | 路径 |
|------|------|
| Workflow 默认 / 校验 | `backend/internal/config/runtime.go` |
| Executor 工厂 | `backend/internal/workflowruntime/factory.go` |
| PlanRunner（冻结） | `backend/internal/workflowruntime/plan_runner.go` |
| Compose 图 | `backend/internal/einoruntime/graph_*.go` |
| Application 接线 | `backend/internal/application/application.go` |
| Prompt 辅助 LLM | `backend/internal/application/adapters.go` |
| Model | `backend/internal/modelapi/platform_chat_model.go` |
| 放量 runbook | `docs/runbooks/eino-agent-runtime-rollout.md` |
