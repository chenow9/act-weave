# 部署说明

[English](./deployment.md) · [文档首页](./README.zh-CN.md)

本页说明仓库已提供的启动与配置边界，不宣称生产就绪、高可用或安全认证。生产部署需要由部署方完成自己的网络、密钥、备份、监控、容量和合规验证。

## 本地 Compose

根目录的 `docker-compose.yml` 运行：

- PostgreSQL 17（宿主机 `15432`）
- Redis 7（宿主机 `16379`）
- MinIO（API `9000`、Console `9001`）及 bucket 初始化容器
- Go 后端（宿主机 `8082`）
- Nginx 承载的前端（宿主机 `5174`）

启动命令与开发默认凭证见[快速开始](./getting-started.zh-CN.md)。这些值来自 `docker-compose.yml` 与 `backend/config.yaml`，只适合本地开发。

## 生产前必须替换的值

不要复制仓库中的 `backend/config.yaml` 开发密钥和 bootstrap 管理员设置。至少需要通过受保护的配置位置或 Secret Manager/KMS 提供：

| 范围 | 需要确认/替换 |
| --- | --- |
| 数据与加密 | `ACTWEAVE_POSTGRES_DSN`、`ACTWEAVE_JWT_SECRET`、`ACTWEAVE_SECRET_MASTER_KEY`；使用受管理的 PostgreSQL 和备份策略。 |
| 对象存储 | MinIO/S3 兼容端点、访问密钥、私钥、bucket 生命周期和备份。 |
| AAP 签名 | `ACTWEAVE_AAP_TOKEN_ENDPOINT`、active key ID、私钥文件、轮换/预发布公钥和 token TTL；AAP access token 使用 EdDSA/Ed25519。 |
| 初始身份 | `ACTWEAVE_BOOTSTRAP_ADMIN_*`。Bootstrap 仅会在 `users` 为空时创建第一个平台管理员，但开发默认值仍不可复用。 |
| 域名与传输 | 公开 HTTPS base URL、TLS 终结、受信任反向代理、CORS 和网络访问规则。配置校验要求 AAP token endpoint 在非 loopback 场景为绝对 HTTPS URL。 |
| 上游集成 | Provider/Connection 的端点、凭证、scope、环境、出站身份和主机允许列表。 |

配置读取优先级为 YAML 文件低于环境变量，可用 `ACTWEAVE_CONFIG_FILE` 指定受保护配置文件。更多字段以 `backend/config.yaml` 和 `backend/internal/config` 的校验为准。

## 数据库与对象存储

后端在监听前应用嵌入的待处理数据库迁移，并使用 PostgreSQL advisory lock 串行化多实例迁移。手动命令可用于受控维护，但不应与自动启动迁移并行混用：

```bash
cd backend
go run ./cmd/migrate version
go run ./cmd/migrate up
```

PostgreSQL 是配置、运行记录、协议事件和审计元数据的事实来源。MinIO 保存持久对象/加密内容；Redis 用于可重建的事件扇出，不能替代持久化事实。请为 PostgreSQL 和对象存储分别设计备份、恢复测试、保留和访问控制。

## Runtime 与反向代理

- Console 管理面为 `/api/v1`；外部 Agent Runtime 为 `/api/agent-access/v1`。应为它们配置不同的身份与访问策略。
- AAP Run SSE 需要流式代理。仓库内前端 Nginx 已对 Run events 禁用缓冲，但生产边缘代理仍须核验 read/send timeout、缓冲和 `Last-Event-ID` 重连行为。
- AAP 文件路由在默认配置中关闭。若启用，上传者必须能访问 presigned URL；文件 content/download 的边缘代理需要流式设置。详见[文件上传运行手册](./runbooks/aap-file-upload.md)。
- `/metrics` 为空 bearer token 时限制为 loopback；配置 bearer token 后需要通过 Authorization 访问。请在网络层也限制抓取端点。

## 功能开关与发布边界

| 能力 | 默认/边界 |
| --- | --- |
| AAP 主运行面 | `agentAccess.feature` 在仓库本地配置中开启；生产可用 workspace/client allowlist 收敛。 |
| AAP 文件 | `agentAccess.files.enabled: false`；打开后还受 workspace/client allowlist、配额和 `runtimeMultimodal` 约束。 |
| 上下文 LLM 压缩 | `runtime.sessionContext.compaction.enabled: false`；请依据运行手册逐步开启。 |
| Tool 强制发布 | 仅平台管理员、且 `tools.allowForcePublish` 配置允许时可用；不是常规发布策略。 |
| A2A 的无认证模式 | 默认拒绝；仅在显式环境开关下可用于本地测试。 |

## 上线检查清单

1. 替换所有开发密码、JWT/加密/AAP 签名密钥，并验证轮换和撤销过程。
2. 将 PostgreSQL、对象存储、Model API 和上游 Provider 指向受控环境，验证网络/host allowlist 与最小权限 scope。
3. 为 AAP 配置 HTTPS、Client/grant、CORS（如果确实需要浏览器直连）和 SSE 断线重连测试；BFF 是默认更安全的 CORS 起点。
4. 对 Tool 依次检查 Schema、Connection、测试和发布状态；确认禁用/回滚影响。
5. 检查审计可见性、数据保留、日志脱敏和对象存储访问；不要在日志中输出 token、secret 或 presigned URL。
6. 执行备份/恢复、健康检查、监控与容量演练。仓库未提供生产 SLO/SLA 或 HA 拓扑，需由部署方定义。

## 相关文档

- [系统架构](./architecture.zh-CN.md)
- [安全策略](../SECURITY.md)
- [AAP 对接指南](./aap-integration-guide.zh-CN.md)
- [AAP 文件上传运行手册](./runbooks/aap-file-upload.md)
