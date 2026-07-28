# ACTWEAVE

## 项目简介

ACTWEAVE 是一个面向业务操作编排的控制台型项目。当前主线能力集中在以下几个对象：

- `Workspace`：业务空间/租户边界
- `Agent`：默认执行代理与提示词配置
- `ServiceConnection`：外部服务连接
- `Tool`：可被 Agent 或 Workflow 调用的业务能力
- `Workflow`：显式编排与审批链路
- `Execution` / `AuditLog`：执行记录与审计输出
- `ChatSession`：对话式执行入口

业务对象和执行链路由实际配置决定，仓库启动后不再预置业务示例数据。

## 仓库结构

```text
.
├── frontend/              # 主前端，Vue 3 + TypeScript + Vite
├── backend/               # 主后端，Go + Gin
├── docs/                  # AAP（Agent Access Protocol）文档与 OpenAPI
└── docker-compose.yml     # 本地依赖与容器启动入口
```

## 当前主线

- [frontend](/Users/chen/Documents/act-weave/frontend) 与 [backend](/Users/chen/Documents/act-weave/backend) 是当前主线。

## 技术栈

### 前端

- Vue `3.5`
- TypeScript `6`
- Vite `7`
- Pinia
- Vue Router
- Element Plus
- Vue Flow
- Axios
- VXE Table

### 后端

- Go `1.25`
- Gin
- JWT (`golang-jwt/jwt/v5`)
- kin-openapi
- 自研 `workflowcompiler` / `workflowruntime` / `toolruntime`（编排内核：Agent=Eino ADK；Workflow 默认 Eino compose，`wrapper` 仅回滚）

### 基础设施

- PostgreSQL
- Redis
- MinIO
- Docker Compose

说明：

- PostgreSQL 是身份、配置、版本、运行记录和审计元数据的唯一事实来源；后端直接使用分域 Repository，不存在全量 JSONB 快照或 `actweave_state`。
- MinIO 保存加密的永久业务原文与受控导出对象；对象元数据、分类、hash 和 retention 仍由 PostgreSQL 约束。
- Redis 仅为可重建的实时扇出边界预留，运行事件事实和 `Last-Event-ID` 回放来自 PostgreSQL；Redis 故障不得丢失事实。

## 快速启动

### 方式一：容器整体启动

```bash
docker compose up --build
```

首次使用空数据卷时，Compose 会创建开发管理员 `admin`，临时密码为 `actweave-admin-dev-change-me`，登录后必须修改。该凭据只用于本地开发，生产部署必须通过 Secret Manager 注入独立强密码。

默认端口：

- 前端：`http://127.0.0.1:5174`
- 后端：`http://127.0.0.1:8082`
- PostgreSQL：`127.0.0.1:15432`
- Redis：`127.0.0.1:16379`
- MinIO API：`127.0.0.1:9000`
- MinIO Console：`127.0.0.1:9001`

### 方式二：前后端分开运行

前端（固定 Node `22.22.3` / npm `10.9.8`，仅使用 `package-lock.json`）：

```bash
cd frontend
# 推荐：fnm / nvm / asdf 读取 .node-version
npm ci
npm run dev
```

后端：

```bash
cd backend
go run ./cmd/server
```

后端默认读取 [`backend/config.yaml`](backend/config.yaml)。该文件包含完整的本地开发配置；生产环境应复制为受保护的独立文件，并通过 Secret Manager 注入 JWT、加密主密钥、数据库和对象存储凭据，不应直接使用仓库中的开发值。

配置优先级为“YAML 文件 < 环境变量”。通过 `ACTWEAVE_CONFIG_FILE` 指定其他配置文件；已设置的环境变量会覆盖对应 YAML 值。例如：

```bash
cd backend
ACTWEAVE_CONFIG_FILE=/etc/actweave/config.yaml \
ACTWEAVE_POSTGRES_DSN='postgres://user:password@database:5432/actweave?sslmode=require' \
ACTWEAVE_JWT_SECRET='replace-with-a-random-secret-of-at-least-32-bytes' \
ACTWEAVE_AAP_SIGNING_PRIVATE_KEY_FILE='/run/secrets/aap-signing-private.pem' \
ACTWEAVE_AAP_SIGNING_GENERATE_IF_MISSING=false \
ACTWEAVE_AAP_TOKEN_ENDPOINT='https://actweave.example.com/api/agent-access/v1/oauth/token' \
go run ./cmd/server
```

支持覆盖的环境变量：

- 服务与日志：`ACTWEAVE_API_ADDR`、`ACTWEAVE_LOG_LEVEL`、`ACTWEAVE_LOG_FORMAT`（本地可读的 `text` 或日志平台使用的 `json`）
- 数据与加密：`ACTWEAVE_POSTGRES_DSN`、`ACTWEAVE_JWT_SECRET`、`ACTWEAVE_SECRET_MASTER_KEY`
- AAP Token/Client 认证：`ACTWEAVE_AAP_TOKEN_ENDPOINT`、`ACTWEAVE_AAP_SIGNING_ACTIVE_KID`、`ACTWEAVE_AAP_SIGNING_PRIVATE_KEY_FILE`、`ACTWEAVE_AAP_SIGNING_GENERATE_IF_MISSING`、`ACTWEAVE_AAP_SIGNING_MAX_TOKEN_TTL_SECONDS`
- MinIO：`ACTWEAVE_MINIO_ENDPOINT`、`ACTWEAVE_MINIO_ACCESS_KEY`、`ACTWEAVE_MINIO_SECRET_KEY`、`ACTWEAVE_MINIO_USE_SSL`、`ACTWEAVE_MINIO_REGION`
- 初始管理员：`ACTWEAVE_BOOTSTRAP_ADMIN_USERNAME`、`ACTWEAVE_BOOTSTRAP_ADMIN_PASSWORD`、`ACTWEAVE_BOOTSTRAP_ADMIN_DISPLAY_NAME`、`ACTWEAVE_BOOTSTRAP_ADMIN_LOCALE`、`ACTWEAVE_BOOTSTRAP_ADMIN_TIMEZONE`

配置文件使用严格字段校验，未知字段、多个 YAML 文档和非法布尔值会阻止启动。环境变量被显式设置为空时也会覆盖文件值，并由必填校验报告错误，不会静默回退。`encryption.masterKey` 必须是 Base64 编码的 32 字节主密钥；Bootstrap 用户名和至少 12 位的密码必须成对提供，它们只在 `users` 为空时以事务锁创建第一个管理员。

AAP Access Token 固定使用独立的 `EdDSA/Ed25519` 密钥，不复用用户 Session JWT 的 `HS256` Secret。仓库内的本地配置只会在 `backend/.local/` 缺失时创建权限为 `0600` 的开发密钥；生产必须把 `generateIfMissing` 设为 `false`，从 Secret Manager/KMS 受控挂载稳定的 PKCS#8 PEM 文件。Public JWKS 位于 `GET /api/agent-access/v1/.well-known/jwks.json`，只包含公开 OKP 字段。AAP 配置、鉴权与数据面约定见 [`docs/guides/agent-access-developer-guide.md`](docs/guides/agent-access-developer-guide.md) 与 [`docs/guides/agent-access-api-reference.md`](docs/guides/agent-access-api-reference.md)。

AAP Client 通过 `POST /api/agent-access/v1/oauth/token` 获取短期 Token；请求必须使用 `application/x-www-form-urlencoded`，携带 `grant_type=client_credentials`、一个 `agent_id` 和当前 Agent Grant 的 Scope 子集。Client 可使用 HTTP Basic 中的一次性注册 Secret，或提交标准 `client_assertion_type` 与 `private_key_jwt` Assertion；两种认证方式不能混用，也不支持 `client_secret_post`。成功响应遵循 OAuth `access_token/token_type/expires_in/scope` 字段且不签发 Refresh Token，并强制 `Cache-Control: no-store`。Token TTL 为 5～15 分钟，同时受 Client 配置、服务端签名窗口和 Grant 到期时间约束；每个 Token 只绑定一个 Workspace 和一个 Agent。AAP 数据面使用独立的 `EdDSA + typ=at+jwt + iss/aud` 验证器和 Principal Context；平台用户 `HS256` Session Token 与 AAP Token 在两个方向均不可互用。

AAP 普通数据面请求会把 Token 的 `ver` 与数据库当前 Service Principal `security_version` 实时比较，并重新检查 Workspace、Agent、Client 与 Grant 状态。撤销 Credential/Grant 或禁用 Client 会在同一事务内递增版本，因此旧 Token 对新请求立即失效。活动 SSE 使用最长 60 秒的有界版本缓存；本机提交后通知先失效缓存再唤醒连接，通知丢失或跨节点延迟时仍会由 60 秒周期重验回源数据库。断开返回无 Cursor 的 `AUTHORIZATION_REVOKED` Transport Signal，客户端应取得新 Token 并使用原 `Last-Event-ID` 恢复，不能继续使用旧 Token。

### Bootstrap 与运行期用户管理边界

- `bootstrapAdmin` 只在 `users` 表为空时创建第一个 `PLATFORM_ADMIN`。已有用户后再修改 YAML 或 `ACTWEAVE_BOOTSTRAP_ADMIN_*` 不会更新用户名、密码、状态或角色。
- 运行期平台用户和平台角色由 PostgreSQL 的 `users` / `user_credentials` 保存。平台管理员应通过前端“用户与权限”页面或 `/api/v1/admin/users` API 创建用户、修改资料/状态/平台角色、解锁或重置密码，不应把业务用户写入配置文件。
- Workspace 角色由 `workspace_members` 保存，并由 Workspace OWNER/ADMIN 在业务空间成员管理中分配。它与 `PLATFORM_ADMIN` / `USER` 平台角色是两个独立授权层级。
- 系统始终保留至少一个 `ACTIVE + PLATFORM_ADMIN`；最后一个有效平台管理员不能被降级、停用或锁定。用户管理命令会写入关联 requestId/traceId 的审计事件，密码和 Secret 不进入审计载荷。

健康检查使用 `GET http://127.0.0.1:8082/api/v1/health`。前端与 Dockerfile builder 统一使用 **npm** + `package-lock.json`（`npm ci`）；不要使用 pnpm/yarn。Node / npm 版本见 `frontend/package.json` 的 `engines` / `packageManager` 与 `frontend/.node-version`。

前端容器镜像（`frontend/Dockerfile`）固定 **Node `22.22.3-alpine` + digest** 与 **Nginx `1.28.0-alpine` + digest**（禁止 `latest`）。Nginx 配置见 `frontend/nginx.conf` 与 `frontend/nginx-security-headers.conf`：统一安全响应头（含 enforced CSP）、`/assets/*` immutable 长缓存、`index.html`/SPA fallback `no-cache`、缺失 asset 404、SSE 非缓冲代理。本地可用 `nginx -t`（需解析 compose 服务名 `backend`）与对运行中 frontend 容器 `curl -sI` 核对头与缓存策略；HSTS 仅对 HTTPS 生产入口有浏览器效力（不带 `includeSubDomains`）。

### AAP SSE 代理要求

AAP Run Event Stream 每 15 秒发送一次无 `id` 的 `: ping <timestamp>` 注释。反向代理必须保留 `Cache-Control: no-cache, no-transform` 和 `X-Accel-Buffering: no`，关闭响应缓冲、Cache 与 gzip 动态压缩，并把读/空闲超时配置为至少 60 秒（仓库 Nginx 开发配置使用 75 秒）。Console 与 AAP 事件入口差异见 [`docs/runbooks/protocol-event-console-vs-aap-entrypoints.md`](docs/runbooks/protocol-event-console-vs-aap-entrypoints.md)；OpenAPI 契约见 [`docs/openapi/agent-access-v1.yaml`](docs/openapi/agent-access-v1.yaml)。

## 当前可用命令

### 前端

```bash
cd frontend
npm ci
npm run lint
npm run format:check
npm run dev
npm run build
npm test -- --run
npm run type-check
npm run e2e:workflow
```

### 后端

```bash
cd backend
go run ./cmd/migrate up
go build ./cmd/server
go test ./...
```

数据库迁移说明：

- API 服务在开始监听端口前会自动执行嵌入二进制的全部待处理迁移；迁移失败或数据库处于 dirty 状态时服务直接启动失败。
- API 服务和 `cmd/migrate` 共用同一 YAML/环境变量配置加载器；迁移命令只校验其实际需要的 `database.dsn`。
- 多个 API 实例同时启动时由 PostgreSQL advisory lock 串行化迁移，其他实例随后得到已是最新版本的结果。
- 手工检查或回滚单步可运行 `go run ./cmd/migrate version`、`go run ./cmd/migrate down 1`；容器镜像内对应命令为 `/app/actweave-migrate`。
- 后端要求 Go `1.25.x`；若系统默认 `go` 较旧，请先切换到匹配版本。

数据卷与恢复说明：

- Compose 使用 `postgres-data`、`redis-data`、`minio-data` 命名卷。`docker compose down` 保留数据；`docker compose down -v` 会永久删除本地卷，只应用于明确的空库验收或重置。
- 生产升级前应备份 PostgreSQL 与 MinIO，并保存对应加密主密钥/历史解密密钥；只有数据库或只有对象存储的备份不能完整恢复永久原文。
- 恢复时先恢复 PostgreSQL 和 MinIO，再使用相同密钥启动后端；服务会在接收流量前补齐迁移。不得在缺失旧解密密钥时旋转或丢弃密钥。
- Redis 只保存可重建状态，不作为备份事实来源。


## 当前工程现状

- 主界面模块包括：总览、业务空间、Agent 管理、服务连接、OpenAPI 导入、模型 API 配置、工具管理、编排、智能编排、对话式执行控制台、审计日志，以及仅平台管理员可见的用户与权限管理。
- Workflow 新链路是唯一主线：`WorkflowGraphDraft` -> `WorkflowCompilation` -> `CompiledExecutionPlan` -> `WorkflowRevision` -> `workflowruntime / Eino callable`。
  `Workflow.dsl`、`Workflow.canvasGraph` 及旧 API 兼容写路径已删除。
- Tool 测试和标准调用都通过受 SSRF、Secret 注入、响应上限、幂等与永久载荷策略约束的 HTTP Executor；一期不提供 Internal/MCP/Connector/Shell executor。
- 前后端类型契约目前依赖手工同步，没有自动生成流程。

## 智能编排（Intelligent Orchestration）

产品主路径是 **多轮 Generate Session**（`smart-dag.v2`），从属于 Workspace 内 **已配置可用 LLM 的 Agent**，**不**降级为无模型的规则假生成。

### 多轮流程

1. **选 Workspace + Agent**（Agent 必须绑定可用 `modelConfig`；否则建会话/发 turn 返回 `422 AGENT_MODEL_REQUIRED`）。
2. **创建会话** `POST /api/v1/workspaces/{wid}/workflow-generate-sessions`（body：`agentId`，可选 `workflowId` 在已有 Draft 上继续；**禁止**请求体 `modelConfigId` 绕过）。
3. **多轮意图** `POST .../workflow-generate-sessions/{sid}/turns`（`message` + 可选 `feedback` 失败回流）：
   - 管理端固化 System Prompt（D16）+ Agent + 已发布 Tool 目录 + 当前 Draft 图 + 历史轮次
   - **唯一 LLM 入口** `modelapi.PlatformChatModel`（Agent 的 modelConfig）
   - 解析 `workflow.graph.v1` JSON → **确定性 Guard**（catalog toolId / D8 节点白名单 / Start–End）→ 通过后才 Create/UpdateDraft
   - Guard 拒绝：保留上一轮合法 Draft，响应带 `guardReport`
4. **关闭会话** `POST .../workflow-generate-sessions/{sid}:close`。
5. **生命周期（手动，无 auto-publish）**：编译 → 试运行（trial）→ 发布 revision → **绑定**生成会话所用 Agent → 多入口使用：
   - Console 对话 / Workflow 生产 `:execute`
   - （契约内）AAP 路径调用已绑定 Agent 的能力

### 审计与可观测

- Draft `ui` / 响应字段：`generatedBy=smart-dag.v2`、`sessionId`、`agentId`、`modelConfigId`、`promptId`、`promptHash`、`generationId`、`traceId`。
- 指标（`GET /metrics`）：`smartdag_generate_total{result}`、`smartdag_guard_reject_total`、`workflow_trial_total`、`workflow_production_execute_total`；日志事件 `smartdag.generate.*` / `workflow.trial.*` / `workflow.production_execute.*`（不记录 prompt 全文）。

### 相关文档（AAP）

- API 参考：[`docs/guides/agent-access-api-reference.md`](docs/guides/agent-access-api-reference.md)
- 开发指南：[`docs/guides/agent-access-developer-guide.md`](docs/guides/agent-access-developer-guide.md)
- 迁移指南：[`docs/guides/agent-access-migration-guide.md`](docs/guides/agent-access-migration-guide.md)
- OpenAPI：[`docs/openapi/agent-access-v1.yaml`](docs/openapi/agent-access-v1.yaml)
- Console vs AAP 入口：[`docs/runbooks/protocol-event-console-vs-aap-entrypoints.md`](docs/runbooks/protocol-event-console-vs-aap-entrypoints.md)

> 遗留的 `POST .../workflows:generate`（`smart-dag.v1` 规则路径）不是产品主路径；Console 智能编排 UI 使用 session/turns。

## 已知限制

- 前端：`npm run lint`（ESLint 零 warning）、`npm run format` / `npm run format:check`（Prettier）；`npm run build` 已包含 `vue-tsc --noEmit`。
- 后端暂无统一 `lint`/`format` 脚本；以 `go test` / `go vet` 为主。
- 暂无 CI 工作流。
- UI 目前明显以桌面端为主，`body` 与壳层存在 `min-width: 1180px` 约束。
- 高级 Workflow 节点类型（如 `HTTP`、`SubWorkflow`、`Parallel`、`ForEach`）后端已支持编译/运行时接口，但前端编辑器未完整暴露对应配置能力。

## 建议先读的文档

- [`docs/guides/agent-access-developer-guide.md`](docs/guides/agent-access-developer-guide.md) — AAP 快速接入与鉴权
- [`docs/guides/agent-access-api-reference.md`](docs/guides/agent-access-api-reference.md) — AAP HTTP/SSE API
- [`docs/openapi/agent-access-v1.yaml`](docs/openapi/agent-access-v1.yaml) — OpenAPI 契约
- [`docs/guides/agent-access-migration-guide.md`](docs/guides/agent-access-migration-guide.md) — 迁移说明
- [`docs/runbooks/protocol-event-console-vs-aap-entrypoints.md`](docs/runbooks/protocol-event-console-vs-aap-entrypoints.md) — Console 与 AAP 事件入口
