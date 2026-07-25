# E2E 全链路验收报告：业务空间 → Agent → 透传工具 → AAP 多轮

| 字段 | 内容 |
| --- | --- |
| 日期 | 2026-07-25 |
| 环境 | 本地：前端 `5174` / 后端 `8082` / mock-biz `18080` / PG `15432` |
| 登录 | `backend/config.yaml` bootstrapAdmin：`admin` / `actweave-admin-dev-change-me` |
| 模型 | `http://192.168.20.4:7080/v1` · `gpt-5.4` · 用户提供的 API Key |
| 出站 | `REQUEST_PASSTHROUGH` |
| AAP | TypeScript SDK `@actweave/agent-client` · client_credentials |
| 业务空间 | `e2e-expense-pt` / `019f9547-83ae-7196-ac36-9d7ded7fea29` |
| Agent | `费用助手` / `019f9549-66fe-7186-b1b4-e6a358d99683` |
| Connection | `expense-pt-local` / `019f954a-5b36-704a-bec2-cfb470fa222b` |
| 结论 | **通过（含回归）**：平台配置 + 透传 + AAP 多轮多工具/审批已闭环；模型网关 `tools/tool_choice` 兼容已验证；`/logs` 时间轴含 user/model/tool/审批结果/final |
| 回归时间 | 2026-07-25 二次排查后重跑 `e2e/aap-multi-turn-expense.mjs` 成功 |

---

## 1. 测了什么

### 1.1 Mock 第三方业务系统（Gin）

路径：`e2e/mock-biz/`

| 能力 | 端点 |
| --- | --- |
| 发业务 Token | `POST /oauth/token` |
| 用户身份 | `GET /v1/me` |
| 报销列表/创建/详情/提交 | `/v1/expenses*` |
| 待审批 / 审批决定 | `/v1/approvals/*` |
| 部门预算 | `GET /v1/budget/summary` |
| OpenAPI | `/openapi.yaml` |

用户：`wang.li`（员工）/ `chen.wei`（主管）/ `zhao.min`（种子待审单）。

### 1.2 Chrome 真人路径

| 步骤 | 结果 |
| --- | --- |
| 登录（已有会话） | PASS |
| 新建业务空间 `e2e-expense-pt` | PASS（自动切换为当前空间） |
| 模型 API 配置 gpt-5.4 | PASS（见前端问题：默认值拼接） |
| Agent「费用助手」可见 | PASS（API 创建后列表可见） |
| 工具管理 8 工具 / 6 已发布 | PASS |
| 全链路审计日志 | PASS：5 轮 AAP + 1 轮调试共 6 Trace，成功率 100% |

### 1.3 API 配置链路

| 步骤 | 结果 |
| --- | --- |
| Provider Mock Corp Expense + dual-mode outboundIdentity | PASS |
| Connection REQUEST_PASSTHROUGH + egress `127.0.0.1:18080` | PASS |
| Connection verify | PASS（修复后） |
| OpenAPI 文件导入 8 endpoints | PASS |
| Tool test + publish（透传 Token） | 6/8 PASS；path 参数工具 2 个 403 |
| Agent 绑定 6 个已发布工具 + connectionId | PASS |
| AAP Client + Grant（全 scope） | PASS |

### 1.4 AAP SDK 多轮（业务话术，不点名工具）

脚本：`e2e/aap-multi-turn-expense.mjs`  
结果：`docs/verification/e2e-full-chain-2026-07-25/aap-multi-turn-result.json`

| 轮次 | 角色 | 业务问题摘要 | createRun | SSE 终态 |
| --- | --- | --- | --- | --- |
| 1 | wang.li | 身份 + 预算 + 在途报销 | 202 | completed |
| 2 | wang.li | 提交杭州差旅 3560 | 202 | completed |
| 3 | wang.li | 确认单号状态 + 预算 | 202 | completed |
| 4 | chen.wei | 待审列表并审批通过 | 202 | completed |
| 5 | chen.wei | 确认待审清空 + 预算 | 202 | completed |

透传：`outboundCredentials` 随每轮 createRun 注入对应业务 Token。

### 1.5 链路审计核对（二次重跑后）

Chrome `/logs` + API `GET .../agent-audit/traces`（示例 Trace `019f9753-ac0c-…` 审批轮）：

| 期望环节 | 是否完整 | 证据 |
| --- | --- | --- |
| 用户提问 | **有** | 时间轴「用户输入」含业务原文 |
| 模型推理 | **有** | 「大模型推理」含 `Planning…` 文本（gateway 在 high effort 下可返回） |
| 工具调用 | **有** | `getme` / `listpendingapprovals` / `createexpense` / `listmyexpenses` / `getbudgetsummary` / `decideapproval` |
| 审批 | **有（业务工具审批）** | `decideapproval` 将 `exp-102`/`exp-101` 置为 `APPROVED`；平台 HITL 非本场景（`requiresConfirmation=false`） |
| 最终结果 | **有** | 时间轴「最终输出」含单号/状态/预算摘要 |

各轮 agent-run step 摘要见 `aap-rerun-run-analysis.json`（toolCount 3/1/2/5/2）。

### 1.6 模型网关 tools 探针（直连）

证据：`model-gateway-tools-probe.json`

| 请求 | finish_reason | 结果 |
| --- | --- | --- |
| 无 tools 普通对话 | `stop` | 正常中文 completion |
| `tools` + `tool_choice=auto` | `tool_calls` | 返回 `getme` function call |
| `tools` + force function | `tool_calls` | 返回 `getme` |
| 多 tools + system 业务提示 | `tool_calls` | 至少调用 `getme` |

结论：**网关兼容 OpenAI tools/tool_choice**；首轮 E2E 无 tool 更可能是当时网关瞬时异常/脏响应（`Context needed…`），而非平台不支持 tools。

### 1.7 运行调试台对照（同一 Agent + 透传）

Console chat session `019f9750-bba4-…` + attach outbound + 业务问句：

- steps: `MODEL → TOOL(getme) → MODEL → TOOL(getbudgetsummary) → MODEL`
- mock 收到 `/v1/me`、`/v1/budget/summary` 且 200
- 与 AAP 路径行为一致：**会 tool call**

---

## 2. 发现并修复的问题

| ID | 问题 | 处理 |
| --- | --- | --- |
| F1 | 双模式 Connection **verify** 走 legacy SecretInjector，透传连接永远 `CONNECTION_UPSTREAM_ERROR`，阻断 OpenAPI 导入 | **已修**：`serviceConnectionVerifier` 对 dual-mode 仅做 egress + HTTP 探测（T5） |
| F2 | Tool **test/invoke** 剥离了 `outboundCredentials` 但未 Vault attach（`_ = split.CredentialsRaw`） | **已修**：TestService / DirectInvocation + BindingAttacher |
| F3 | AAP **createRun** 用 `DisallowUnknownFields` 解码，带 `outboundCredentials` → 422；且未接入透传 | **已修**：`ReadOutboundCredentialsBody` + RunService attach |
| F4 | Agent 绑定 connection 加载 SQL：`connection_id <> ''` 对 UUID 列非法 → createRun 500 | **已修**：仅 `IS NOT NULL` |
| F5 | SDK `CreateRunRequest` 无 `outboundCredentials` 类型 | **已补** types（runtime 本可透传 JSON） |
| F6 | AAP Idempotency-Key 必须规范 UUID | E2E 脚本改为 `crypto.randomUUID()` |

### 修复涉及主要文件

- `backend/internal/application/adapters.go`（verify dual-mode）
- `backend/internal/application/connection_verifier_test.go`
- `backend/internal/tool/test_service.go` / `direct_invocation.go`
- `backend/internal/transport/http/tool_openapi.go`
- `backend/internal/transport/http/aap_run_routes.go`
- `backend/internal/aap/run.go` / `outbound_attach.go`
- `backend/internal/application/application.go`
- `backend/internal/execution/outbound_injector.go`
- `sdk/typescript/src/models.ts`
- `e2e/mock-biz/*` · `e2e/aap-multi-turn-expense.mjs`

---

## 3. 未关闭 / 残留问题

| ID | 严重度 | 说明 |
| --- | --- | --- |
| R1 | **关闭** | 网关 tools 兼容已确认；调试台与 AAP 重跑均 tool call。首轮失败归因临时脏 completion，非结构性不兼容。 |
| R2 | 中 | `getexpense` / `submitexpense` 未发布（path 参数 tool test 403）；本轮闭环未依赖它们。 |
| R3 | 中 | URL 型 OpenAPI 导入 dual-mode 限制仍在；文件上传可绕过。 |
| R4 | 低 | Provider discovery sync 失败（与文件导入无关）。 |
| R5 | **关闭** | 已去掉「创建空间自动生成默认 Agent」相关文案；创建后需手动建 Agent。 |
| R6 | 低 | 模型配置弹窗默认值 fill 易拼接；成功后 dialog 偶发 busy。 |
| R7 | 低 | SDK `createRun` `Accept: application/json` 与 `stream:true` 冲突（E2E 用 stream:false + followRun）。 |
| R8 | 低 | E2E 脚本 SSE reducer 未把 tool 事件记入 `toolCalls` 字段（agent-run/审计已证明 tool 发生）；脚本可增强。 |
| R9 | 低 | 模型偶发 `decideapproval` 用 `APPROVED` 而非 `APPROVE` → 一次 HTTP 400 后自愈重试成功。 |

---

## 4. 前端页面观察

| 页面 | 观察 |
| --- | --- |
| 业务空间 | 创建流畅；数量 23→24；当前空间切换正确 |
| 模型 API | 可创建；默认值 + fill 易拼坏 URL/模型名 |
| Agent 管理 | 列表展示正常（名称/模型/空间） |
| 工具管理 | 8 工具、6 已发布、Provider/Connection 展示正确 |
| 全链路审计 | 平台管理员可见；统计与 5 轮 AAP Trace 对齐；详情含 input/reasoning/output |

---

## 5. 复现命令（摘要）

```bash
# mock
cd e2e/mock-biz && go build -o mock-biz . && ./mock-biz

# backend（含修复）
cd backend && go build -o /tmp/actweave-server ./cmd/server
NO_PROXY='*' /tmp/actweave-server

# AAP 多轮（依赖 /tmp/aap_e2e_config.json 与已配置空间）
NO_PROXY='*' node e2e/aap-multi-turn-expense.mjs
```

Chrome：`http://127.0.0.1:5174` · 空间「E2E费用报销透传全链路」· `/logs`。

---

## 6. 总体判断（更新）

| 层 | 状态 |
| --- | --- |
| 控制台注册空间 / 模型 / Agent / 工具绑定 | **通过** |
| REQUEST_PASSTHROUGH 配置与 Connection verify | **通过** |
| Tool 测试透传 Token 到 mock | **通过** |
| 模型网关 tools/tool_choice | **通过**（直连探针） |
| 运行调试台同 Agent+透传 tool call | **通过** |
| AAP SDK 多轮 + 多工具 + 业务审批 | **通过**（二次重跑） |
| `/logs` 时间轴 user/model/tool/审批结果/final | **通过**（Chrome 点开审批轮详情） |

### 二次重跑关键证据

- 结果 JSON：`aap-multi-turn-result.json`（含各轮 `agentRunToolCount`）
- step 分析：`aap-rerun-run-analysis.json`
- 网关探针：`model-gateway-tools-probe.json`
- Chrome：Trace `019f9753-ac0c-7e96-b14b-04500c376fc7` 含 `decideapproval`×成功 + 最终 APPROVED 汇总
