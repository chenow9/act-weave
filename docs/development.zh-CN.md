# 开发说明

[English](./development.md) · [文档首页](./README.zh-CN.md)

本页收集仓库已有的开发入口。运行前后端分离模式前，先确保 PostgreSQL、Redis、MinIO 和 `backend/config.yaml` 中的本地配置可用；最短全栈路径仍是 `docker compose up --build`。

## 运行要求

| 区域 | 仓库依据 |
| --- | --- |
| Frontend | `frontend/package.json` 固定 Node `22.22.3`、npm `10.9.8`，使用 `package-lock.json`。 |
| Backend | `backend/go.mod` 为 Go `1.25.0`；Dockerfile 使用 Go `1.25.11` 构建镜像。 |
| 数据服务 | PostgreSQL、Redis、MinIO；本地可由根目录 Compose 提供。 |

## 前端

```bash
cd frontend
npm ci
npm run dev
```

常用校验：

```bash
npm run lint
npm run format:check
npm test -- --run
npm run type-check
npm run build
npm run e2e:smoke
```

## 后端

```bash
cd backend
go run ./cmd/server
```

常用校验：

```bash
go vet ./...
go test ./...
go build ./cmd/server
```

后端服务启动时会尝试应用嵌入迁移。手工迁移命令仅用于受控操作，见[部署说明](./deployment.zh-CN.md#数据库与对象存储)。

## AAP、SDK 与协议改动

- 对外 HTTP 运行契约：[`docs/openapi/agent-access-v1.yaml`](./openapi/agent-access-v1.yaml)。
- 运行协议 schema：`backend/internal/protocolschema/schemas/aap/v1/`；生成产物包含 SDK 类型和 OpenAPI components。
- TypeScript SDK：[`sdk/typescript/`](../sdk/typescript/)。在该目录运行 `npm ci`、`npm run type-check`、`npm run check:readme-quickstart`、`npm test`、`npm run build`。

改动协议 schema 后，运行：

```bash
make generate
make protocol-compat-check
```

不要只修改 SDK 或 OpenAPI 的生成结果。兼容性检查会将当前 schema 与基线比较；相关 CI 位于 `.github/workflows/`。

## 文档与截图

- 产品与文档入口从[文档首页](./README.zh-CN.md)维护；中文 README 是双语内容的主要来源。
- 不在项目首页堆叠开发命令或所有页面截图。首页保留闭环和最短启动路径，详情进入本目录。
- 截图来自虚构 demo Workspace。重新生成前阅读[产品导览](./product-tour.zh-CN.md#重新生成截图)，因为脚本会清理已有 PNG。

## 相关文档

- [贡献指南](../CONTRIBUTING.md)
- [AAP 对接指南](./aap-integration-guide.zh-CN.md)
- [架构](./architecture.zh-CN.md)
