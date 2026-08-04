# 快速开始

[English](./getting-started.md) · [文档首页](./README.zh-CN.md)

本页只说明本地整栈启动。它使用仓库的 `docker-compose.yml`，并不构成生产部署方案。

## 前置条件

- Docker Desktop（或兼容 Docker Engine）
- Docker Compose v2（`docker compose`）
- 可访问构建镜像与依赖所需的网络

## 启动

```bash
git clone https://github.com/chenow9/act-weave.git
cd act-weave
docker compose up --build
```

首次构建会拉取镜像、构建前后端、启动 PostgreSQL/Redis/MinIO，并启动后端与 Console。后端在监听前执行嵌入式数据库迁移；不要额外手工执行迁移来完成这条 Compose 快速路径。

## 本地地址和开发凭证

| 服务 | 地址 |
| --- | --- |
| Console | <http://127.0.0.1:5174> |
| 后端 health | <http://127.0.0.1:8082/api/v1/health> |
| PostgreSQL | `127.0.0.1:15432` |
| Redis | `127.0.0.1:16379` |
| MinIO API / Console | <http://127.0.0.1:9000> / <http://127.0.0.1:9001> |

空 PostgreSQL 数据卷会创建开发管理员：

| 用户名 | 临时密码 |
| --- | --- |
| `admin` | `actweave-admin-dev-change-me` |

该账号和 Compose 中的数据库/MinIO 凭证只用于本地开发。首次登录后修改密码；不得将这些值带入生产环境。

## 下一步

1. 先阅读[概念](./concepts.zh-CN.md)，区分 Console API、AAP、A2A 与 Tool。
2. 在 Workspace 内配置 Model API、Provider 和 Service Connection，导入/创建并发布 Tool。
3. 创建 Agent，绑定已发布 Tool 或 Workflow，并在 Console 试跑。
4. 需要应用接入时，阅读[AAP 对接指南](./aap-integration-guide.zh-CN.md)并使用[OpenAPI 契约](./openapi/agent-access-v1.yaml)。

## 本地故障排查

- 检查 `docker compose ps` 与后端 health 地址。
- 后端、前端和依赖服务使用 Compose 容器名；从宿主机访问时使用上表端口。
- Compose volume 为 `postgres-data`、`redis-data`、`minio-data`。执行 `docker compose down -v` 会永久删除本地开发数据。

配置、密钥、TLS、生产对象存储和边缘代理请看[部署说明](./deployment.zh-CN.md)。
