# Chrome 验收：A→B（B 调工具）审计演示

## 前置

1. Postgres 已启动（`docker compose up -d` 或本地 `data/postgres`）。
2. 迁移到最新：

```bash
cd backend && go run ./cmd/migrate up
```

3. 启动后端 / 前端：

```bash
# 后端
cd backend && go run ./cmd/server

# 前端
cd frontend && npm run dev
```

4. 使用仓库既有开发账号登录（bootstrap admin，见 `backend/config.yaml` 的 `bootstrapAdmin`；**不要**在报告/聊天中粘贴真实密码）。默认本地入口为 Vite 端口 **`http://127.0.0.1:5174`**（见 `frontend/vite.config.ts`）。

## 配置步骤（UI）

1. 以 **PLATFORM_ADMIN** 登录。
2. 打开业务空间 → **Agents**。
3. 创建/确认两个 ACTIVE Agent：
   - **Agent A**（调用方）：提示词中说明可使用工具 `call_b` 委派任务。
   - **Agent B**（被调用方）：绑定至少一个已发布 **TOOL**（任意 HTTP 工具即可）；提示词要求完成任务时调用该工具。
4. 编辑 Agent A → 面板 **「Agent 委派与 A2A」**：
   - 目标 Agent = B
   - callableName = `call_b`（稳定标识，非 Agent ID）
   - mode = `INLINE`
   - contextPolicy 默认 `TASK_ONLY`
   - 保存绑定
5. 打开 **Chat 执行**，选择 Agent A，发送例如：`请把「查天气」任务委派给 call_b 并汇报结果`。
6. 打开 **Agent 审计中心**（仅平台管理员）→ 找到对应 Trace：
   - 时间线应出现 **Agent 调用** 节点（`agent_delegation`）
   - 可展开：子 Agent 模型推理、子 Agent 工具调用、返回
   - 根 Agent 后续模型与最终输出
   - 子 Agent 文本不应混入根最终输出以外的「伪 TOOL」伪装

## API 等价配置

管理面 HTTP 路由统一挂在 **`/api/v1`** 前缀下：

```http
POST /api/v1/workspaces/{wid}/agents/{agentA}/delegation-bindings
Content-Type: application/json

{
  "targetAgentId": "{agentB}",
  "callableName": "call_b",
  "description": "Delegate lookup tasks",
  "mode": "INLINE",
  "contextPolicy": "TASK_ONLY",
  "enabled": true
}
```

## A2A 快速闭环（可选）

- **Inbound 暴露（管理 API）**：`POST /api/v1/workspaces/{wid}/a2a/exposures`（agentId + publicName）
- **Agent Card**：`GET /a2a/workspaces/{wid}/agents/{agentId}/.well-known/agent-card.json`
- **Invoke**：`POST /a2a/workspaces/{wid}/agents/{agentId}/invoke`（JSON-RPC，需 AGENT_ACCESS 鉴权时带 Authorization）
- **Outbound**：在 Agent 编辑页添加远端 binding（endpoint + allowedHosts）

## 断言清单

- [ ] go.mod 锁定 `github.com/cloudwego/eino v0.9.13`，无 replace
- [ ] 绑定保存时拒绝 self-loop / 重名 alias / 环
- [ ] 审计表 `agent_run_delegations` 有 SUCCEEDED/FAILED 终态
- [ ] `agent_run_steps` 含子步 `agent_id` / `delegation_id` / `parent_step_id`
- [ ] 前端时间线可展开 Agent 调用层级
