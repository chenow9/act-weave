# ACTWEAVE 织行

[English](./README.md)

**ActWeave（织行）** 是一套面向企业业务系统的 **Agent 编排与执行控制台**。  
它把外部业务 API 变成可治理的 **Tool**，配置可决策的 **Agent**，必要时用 **Workflow / 智能编排** 把多步业务串起来，并通过 **运行调试台与审计日志** 保证每次调用可追溯。

第三方业务系统 **不通过管理后台 Session** 调 Agent，而是走标准化的 **Agent Access Protocol（AAP）** 接入。

---

## 这个项目解决什么问题？

传统「业务系统 + 大模型」常见痛点：

| 痛点 | ActWeave 的做法 |
| --- | --- |
| 模型直接乱调 HTTP，缺权限与契约 | 业务 API 先注册为 **Tool**（契约 / 连接 / 版本 / 发布） |
| 多系统凭证散落在前端 | **Service Connection + 出站身份**（含 REQUEST_PASSTHROUGH） |
| 编排不可见、难回放 | **Workflow 图编排** + 试跑 / 发布 / 审计 |
| 合作方无法安全接入 | **AAP**：OAuth 客户端、Scope、Conversation / Run、SSE |
| 出了问题说不清 | **Agent 审计中心 / 运行日志**，链路可追踪 |

一句话：**ActWeave = 业务 Tool 治理 + Agent 配置 + 编排 + 可审计执行 + 对外 AAP 协议。**

---

## 产品能力一览

```text
┌─────────────┐     ┌──────────────┐     ┌────────────────┐
│  管理控制台  │────▶│  Agent 运行时 │────▶│  业务系统 API   │
│  配置与发布  │     │  Tool/Workflow│     │  (出站凭证)     │
└─────────────┘     └──────┬───────┘     └────────────────┘
                           │
                    ┌──────▼───────┐
                    │  AAP 数据面   │  ← 第三方 App / BFF / 合作方
                    │  Conversation │
                    │  Run + SSE    │
                    └──────────────┘
```

| 能力 | 说明 |
| --- | --- |
| **业务空间 (Workspace)** | 租户 / 项目边界，隔离配置与运行数据 |
| **Provider / 连接** | 外部系统与凭证、出站身份策略 |
| **Tool** | OpenAPI 导入或手工创建；版本、测试、发布 |
| **Agent** | 绑定模型、提示词、Tool / Workflow 能力 |
| **Workflow** | 可视化图编排，试跑与发布 |
| **智能编排 (Smart DAG)** | 用自然语言生成业务流程图草案 |
| **运行调试台** | 控制台内对话试跑 Agent（非生产入口） |
| **Agent Access** | 对外 Client、授权与协议配置 |
| **审计日志** | 运行轨迹与操作审计 |

---

## 界面预览

以下截图来自**虚构演示空间** `Acme Commerce Demo`（电商导购场景的 mock 数据：商品 / 订单 / 库存 / 退款等 Tool），**不包含真实业务租户数据**。

重新生成（会先依赖已 seed 的演示空间）：

```bash
node scripts/seed-readme-demo-workspace.mjs   # 创建/刷新 Acme Commerce Demo
node scripts/capture-readme-screenshots.mjs   # 截图到 docs/images/readme/
```

### 主导航菜单

顶部「导航中心」：按 **空间 → 构建 → 接入 → 运行 → 治理** 组织全部模块，并提供常用快捷入口。

![主导航菜单](./docs/images/readme/00-navigation-menu.png)

### 业务空间切换

在模块页右上角切换当前 Workspace；演示空间为 `Acme Commerce Demo`。

![业务空间切换](./docs/images/readme/00-workspace-switcher.png)

### 登录

![登录页](./docs/images/readme/01-login.png)

### 空间总览

平台级运行健康度（会话、工具成功率、风险提示）。总览为跨空间聚合视图。

![空间总览](./docs/images/readme/02-overview.png)

### 业务空间列表

管理 Workspace 边界、模式（生产 / 沙箱）与状态。

![业务空间](./docs/images/readme/03-workspaces.png)

### Agent 管理

维护职责、绑定空间、决策模型与系统提示词。演示 Agent：`Acme 导购助手`。

![Agent 管理](./docs/images/readme/04-agents.png)

### 工具管理

演示空间中的电商 Tool（商品列表、创建订单、库存、退款等）：契约、HTTP 路径、连接与发布状态。

![工具管理](./docs/images/readme/05-tools.png)

### 编排（Workflow）

设计、校验、试跑并发布业务流程。

![编排](./docs/images/readme/06-workflow.png)

### 智能编排（Smart DAG）

用业务目标描述生成流程图草案。

![智能编排](./docs/images/readme/07-smart-dag.png)

### 服务 Provider

注册上游业务系统（演示：`Acme Commerce API`）。

![服务 Provider](./docs/images/readme/08-providers.png)

### 服务连接

连接与出站身份（如 REQUEST_PASSTHROUGH）。

![服务连接](./docs/images/readme/09-connections.png)

### 模型 API 配置

绑定 Agent 使用的 LLM 接入点。

![模型 API](./docs/images/readme/10-model-apis.png)

### Agent Access（对外接入）

第三方 AAP Client 与授权配置。

![Agent Access](./docs/images/readme/11-agent-access.png)

### 运行调试台

控制台内试跑 Agent（内部调试，非生产 AAP 入口）。

![运行调试台](./docs/images/readme/12-chat.png)

### Agent 审计中心

运行与操作审计，支持链路回溯。

![审计日志](./docs/images/readme/13-logs.png)

---

## 文档

| 文档 | 读者 | 说明 |
| --- | --- | --- |
| **[AAP 对接指南（中文）](./docs/aap-integration-guide.zh-CN.md)** | 第三方对接 | 认证、Scope、HTTP/SSE、错误码、SDK、上线清单 |
| **[AAP Integration Guide (EN)](./docs/aap-integration-guide.md)** | Third-party integrators | Same content in English |
| [OpenAPI — Agent Access v1](./docs/openapi/agent-access-v1.yaml) | 机器 / 代码生成 | HTTP 权威契约 |
| [TypeScript SDK](./sdk/typescript/) | 集成方 | `@actweave/agent-client` |
| [AAP Chat Demo](./demos/aap-chat/) | 本地演示 | 浏览器对话 + BFF 持有 Client Secret / 业务 Token |

给外部合作方时，交付 **AAP 对接指南 + OpenAPI** 即可。第三方 Agent 访问请勿走 `/api/v1` 管理面。

---

## 仓库结构

```text
.
├── frontend/           # Vue 3 + TypeScript + Vite 控制台
├── backend/            # Go + Gin API
├── docs/               # AAP 对接指南、OpenAPI、README 截图
├── demos/aap-chat/     # AAP 对接演示（BFF + 聊天 UI）
├── sdk/typescript/     # @actweave/agent-client
├── scripts/            # 运维 / 截图等辅助脚本
└── docker-compose.yml  # 本地依赖与整栈启动
```

## 技术栈

| 层 | 选型 |
| --- | --- |
| 前端 | Vue 3.5、TypeScript、Vite 7、Pinia、Vue Router、Element Plus、Vue Flow、Axios、VXE Table |
| 后端 | Go 1.25、Gin、JWT、kin-openapi；编排内核（Agent = Eino ADK；Workflow = Eino compose） |
| 数据 | PostgreSQL（事实来源）、MinIO（加密永久对象）、Redis（仅可重建扇出） |

- PostgreSQL 保存身份、配置、版本、运行记录与审计元数据。  
- MinIO 保存加密永久业务原文；元数据与 retention 由 PostgreSQL 约束。  
- Redis 故障不得导致事实丢失；运行事件真相与 `Last-Event-ID` 回放来自 PostgreSQL。  

---

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

默认读取 [`backend/config.yaml`](./backend/config.yaml)。配置优先级：**YAML 文件 &lt; 环境变量**。可用 `ACTWEAVE_CONFIG_FILE` 指定其他文件。

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
| 初始管理员 | `ACTWEAVE_BOOTSTRAP_ADMIN_*` |

说明：

- `encryption.masterKey` 必须是 Base64 编码的 32 字节主密钥。  
- Bootstrap 仅在 `users` 为空时创建第一个 `PLATFORM_ADMIN`。  
- AAP Access Token 使用 **EdDSA/Ed25519**，不复用用户 Session 的 HS256 Secret。  
- 公钥 JWKS：`GET /api/agent-access/v1/.well-known/jwks.json`  

AAP 客户端认证、Scope、SSE、错误码等详见 **[AAP 对接指南](./docs/aap-integration-guide.zh-CN.md)**。

---

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

- API 服务在监听端口前会自动执行嵌入的待处理迁移。  
- 多实例启动时由 PostgreSQL advisory lock 串行化迁移。  
- 手工：`go run ./cmd/migrate version`、`go run ./cmd/migrate down 1`。  
- 需要 Go `1.25.x`。  

Compose 数据卷：`postgres-data`、`redis-data`、`minio-data`。`docker compose down -v` 会**永久删除**本地卷。

---

## 架构与实现要点

- **Workflow 主线**：`WorkflowGraphDraft` → 编译 → `CompiledExecutionPlan` → `WorkflowRevision` → 运行时。  
- **Tool**：经 SSRF 防护、Secret 注入、响应上限与幂等策略约束的 HTTP Executor 调用。  
- **智能编排**：多轮 Generate Session（`smart-dag.v2`），从属于已配置可用 LLM 的 Agent。  
- **AAP**：与控制台 `/api/v1` 管理面分离；第三方只使用 `/api/agent-access/v1`。  

## 已知限制

- UI 以桌面端为主（约 `min-width: 1180px`）。  
- 后端暂无统一 lint/format 脚本，以 `go test` / `go vet` 为主。  
- 部分高级 Workflow 节点后端已支持，前端编辑器尚未完整暴露。  

## 第三方对接入口

1. [docs/aap-integration-guide.zh-CN.md](./docs/aap-integration-guide.zh-CN.md)  
2. [docs/openapi/agent-access-v1.yaml](./docs/openapi/agent-access-v1.yaml)  
3. [sdk/typescript](./sdk/typescript/)（可选客户端库）  
4. [demos/aap-chat](./demos/aap-chat/)（本地 AAP 对话演示）
