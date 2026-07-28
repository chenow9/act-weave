# ACTWEAVE

[English](./README.md)

ActWeave 是面向业务操作编排的控制台型产品，核心对象包括 Agent、Tool、Workflow 与可审计执行。**第三方平台通过 Agent Access Protocol（AAP）对接 Agent**，不要使用管理控制台的用户 Session API。

## 文档

| 文档 | 读者 | 说明 |
| --- | --- | --- |
| **[AAP 对接指南（中文）](./docs/aap-integration-guide.zh-CN.md)** | 第三方对接 | 完整协议：认证、Scope、HTTP/SSE、错误码、SDK、上线清单 |
| **[AAP Integration Guide (EN)](./docs/aap-integration-guide.md)** | Third-party integrators | Same content in English |
| [OpenAPI — Agent Access v1](./docs/openapi/agent-access-v1.yaml) | 机器 / 代码生成 | HTTP 权威契约 |
| [TypeScript SDK](./sdk/typescript/) | 集成方 | `@actweave/agent-client` |

给外部合作方时，交付 **AAP 对接指南 + OpenAPI** 即可。第三方 Agent 访问请勿走 `/api/v1` 管理面。

## 产品域

| 域 | 作用 |
| --- | --- |
| **Workspace** | 业务空间 / 租户边界 |
| **Agent** | 默认执行代理与提示词配置 |
| **ServiceConnection** | 外部系统连接 |
| **Tool** | 可被 Agent / Workflow 调用的业务能力 |
| **Workflow** | 显式图编排与审批 |
| **Execution / AuditLog** | 执行记录与审计 |
| **ChatSession** | 控制台对话入口（内部 UI） |
| **AAP Conversation / Run** | 对外协议中的对话与执行 |

业务对象与链路由实际配置决定，仓库启动后**不**预置业务示例数据。

## 仓库结构

```text
.
├── frontend/           # Vue 3 + TypeScript + Vite 控制台
├── backend/            # Go + Gin API
├── docs/               # AAP 对接指南与 OpenAPI
├── sdk/typescript/     # @actweave/agent-client
└── docker-compose.yml  # 本地依赖与整栈启动
```

## 技术栈

| 层 | 选型 |
| --- | --- |
| 前端 | Vue 3.5、TypeScript、Vite 7、Pinia、Vue Router、Element Plus、Vue Flow、Axios、VXE Table |
| 后端 | Go 1.25、Gin、JWT、kin-openapi；编排内核（Agent = Eino ADK；Workflow = Eino compose） |
| 数据 | PostgreSQL（事实来源）、MinIO（加密永久对象）、Redis（仅可重建扇出） |

- PostgreSQL 保存身份、配置、版本、运行记录与审计元数据；不存在全量 JSONB 状态快照库。  
- MinIO 保存加密永久业务原文；元数据、分类、hash、retention 仍由 PostgreSQL 约束。  
- Redis 故障不得导致事实丢失；运行事件真相与 `Last-Event-ID` 回放来自 PostgreSQL。  

## 快速启动

### 方式一：Docker Compose

```bash
docker compose up --build
```

空数据卷首次启动会创建**开发**管理员：

| | |
| --- | --- |
| 用户名 | `admin` |
| 临时密码 | `actweave-admin-dev-change-me` |

登录后必须修改密码。生产环境禁止使用该凭据。

默认本地端口：

| 服务 | 地址 |
| --- | --- |
| 前端 | http://127.0.0.1:5174 |
| 后端 | http://127.0.0.1:8082 |
| PostgreSQL | 127.0.0.1:15432 |
| Redis | 127.0.0.1:16379 |
| MinIO API | 127.0.0.1:9000 |
| MinIO Console | 127.0.0.1:9001 |

健康检查：`GET http://127.0.0.1:8082/api/v1/health`

### 方式二：前后端分开运行

**前端**（固定 Node `22.22.3` / npm `10.9.8`，仅使用 `package-lock.json`）：

```bash
cd frontend
npm ci
npm run dev
```

**后端：**

```bash
cd backend
go run ./cmd/server
```

默认读取 [`backend/config.yaml`](./backend/config.yaml)。配置优先级：**YAML 文件 &lt; 环境变量**。可用 `ACTWEAVE_CONFIG_FILE` 指定其他文件。未知字段、多文档 YAML、非法布尔值会导致启动失败。

生产环境应将配置复制到受保护路径，通过 Secret Manager / KMS 注入密钥，不要直接使用仓库中的开发值。

常用覆盖示例：

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

常用环境变量：

| 类别 | 变量 |
| --- | --- |
| 服务 | `ACTWEAVE_API_ADDR`、`ACTWEAVE_LOG_LEVEL`、`ACTWEAVE_LOG_FORMAT`（`text` \| `json`） |
| 数据 / 加密 | `ACTWEAVE_POSTGRES_DSN`、`ACTWEAVE_JWT_SECRET`、`ACTWEAVE_SECRET_MASTER_KEY` |
| AAP 签名 | `ACTWEAVE_AAP_TOKEN_ENDPOINT`、`ACTWEAVE_AAP_SIGNING_ACTIVE_KID`、`ACTWEAVE_AAP_SIGNING_PRIVATE_KEY_FILE`、`ACTWEAVE_AAP_SIGNING_GENERATE_IF_MISSING`、`ACTWEAVE_AAP_SIGNING_MAX_TOKEN_TTL_SECONDS` |
| MinIO | `ACTWEAVE_MINIO_ENDPOINT`、`ACTWEAVE_MINIO_ACCESS_KEY`、`ACTWEAVE_MINIO_SECRET_KEY`、`ACTWEAVE_MINIO_USE_SSL`、`ACTWEAVE_MINIO_REGION` |
| 初始管理员 | `ACTWEAVE_BOOTSTRAP_ADMIN_USERNAME`、`ACTWEAVE_BOOTSTRAP_ADMIN_PASSWORD`、`ACTWEAVE_BOOTSTRAP_ADMIN_DISPLAY_NAME`、`ACTWEAVE_BOOTSTRAP_ADMIN_LOCALE`、`ACTWEAVE_BOOTSTRAP_ADMIN_TIMEZONE` |

说明：

- `encryption.masterKey` 必须是 Base64 编码的 32 字节主密钥。  
- Bootstrap 用户名与至少 12 位密码必须成对提供；仅在 `users` 为空时创建第一个 `PLATFORM_ADMIN`，之后修改 bootstrap 配置**不会**更新已有用户。  
- 运行期用户通过前端「用户与权限」或 `/api/v1/admin/users` 管理。Workspace 角色在 `workspace_members`，与平台角色独立。  
- 系统始终保留至少一个 `ACTIVE + PLATFORM_ADMIN`。  
- AAP Access Token 使用 **EdDSA/Ed25519**，不复用用户 Session 的 HS256 Secret。本地开发可在缺失时生成 `backend/.local/` 下权限 `0600` 的密钥；生产必须 `generateIfMissing=false` 并挂载稳定的 PKCS#8 PEM。  
- 公钥 JWKS：`GET /api/agent-access/v1/.well-known/jwks.json`  

AAP 客户端认证、Scope、SSE、错误码等详见 **[AAP 对接指南](./docs/aap-integration-guide.zh-CN.md)**。

## 常用命令

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
```

统一使用 **npm** + `package-lock.json`（`npm ci`），不要使用 pnpm/yarn。

### 后端

```bash
cd backend
go run ./cmd/migrate up
go build ./cmd/server
go test ./...
```

数据库迁移：

- API 服务在监听端口前会自动执行嵌入的待处理迁移；dirty 或失败会阻止启动。  
- 多实例启动时由 PostgreSQL advisory lock 串行化迁移。  
- 手工：`go run ./cmd/migrate version`、`go run ./cmd/migrate down 1`（镜像内：`/app/actweave-migrate`）。  
- 需要 Go `1.25.x`。  

Compose 数据卷：`postgres-data`、`redis-data`、`minio-data`。`docker compose down` 保留数据；`docker compose down -v` 会**永久删除**本地卷。生产恢复需要 PostgreSQL、MinIO 与对应加密密钥同时可用。

## 控制台能力（概要）

- 总览、业务空间、Agent、服务连接、OpenAPI 导入、模型 API 配置、工具、编排、智能编排、对话控制台、审计日志，以及平台管理员可见的用户与权限。  
- Workflow 主线：`WorkflowGraphDraft` → 编译 → `CompiledExecutionPlan` → `WorkflowRevision` → 运行时。旧 `Workflow.dsl` / canvas 写路径已删除。  
- Tool 经受 SSRF、Secret 注入、响应上限与幂等策略约束的 HTTP Executor 调用；一期不提供 Internal/MCP/Connector/Shell executor。  
- 智能编排主路径为多轮 Generate Session（`smart-dag.v2`），从属于已配置可用 LLM 的 Agent，不降级为无模型规则假生成。  

## 已知限制

- UI 以桌面端为主（约 `min-width: 1180px`）。  
- 后端暂无统一 lint/format 脚本，以 `go test` / `go vet` 为主。  
- CI 工作流可能仍较精简。  
- 部分高级 Workflow 节点后端已支持，前端编辑器尚未完整暴露。  

## 第三方对接入口

1. [docs/aap-integration-guide.zh-CN.md](./docs/aap-integration-guide.zh-CN.md)  
2. [docs/openapi/agent-access-v1.yaml](./docs/openapi/agent-access-v1.yaml)  
3. [sdk/typescript](./sdk/typescript/)（可选客户端库）
