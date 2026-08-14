# ActWeave 文档

[English](./README.md) · [项目首页](../README.zh-CN.md)

本文档集以「先理解边界，再接入与运行」组织。产品入口只保留闭环与最短启动路径；协议、运维和开发细节在此处分层维护。

## 选择你的路径

| 你想做什么 | 从这里开始 |
| --- | --- |
| 了解 ActWeave 解决的问题和产品闭环 | [项目首页](../README.zh-CN.md) · [概念](./concepts.zh-CN.md) |
| 在本地启动控制台与后端 | [快速开始](./getting-started.zh-CN.md) |
| 了解控制面、运行面和数据组件 | [系统架构](./architecture.zh-CN.md) |
| 浏览 Console 页面与截图 | [产品导览](./product-tour.zh-CN.md) |
| 从 Web、App 或 BFF 调用 Agent | [AAP 对接指南](./aap-integration-guide.zh-CN.md) · [AAP 接入索引](./integrations/aap.zh-CN.md) |
| 读取机器契约或使用 SDK | [AAP OpenAPI](./openapi/agent-access-v1.yaml) · [TypeScript SDK](../sdk/typescript/) |
| 将既有 HTTP 服务导入为 Tool | [OpenAPI 到 Tool](./integrations/openapi.md) |
| 部署、维护或排查 | [部署](./deployment.zh-CN.md) · [运行手册](./runbooks/) |
| 修改前后端或协议 | [开发](./development.zh-CN.md) · [贡献指南](../CONTRIBUTING.md) |
| 报告安全问题 | [安全](../SECURITY.md) |

## 文档约定

- **Console API** 指控制台管理面 `/api/v1`；它不是第三方 Agent Runtime 的入口。
- **AAP** 指 `/api/agent-access/v1` 运行面。对外 HTTP 契约以 [OpenAPI 文件](./openapi/agent-access-v1.yaml) 为准。
- 文档中的「已支持」仅指当前仓库可找到的代码、路由、配置或测试依据；受默认关闭的功能会注明开关和限制。
- 运行手册是运维补充材料，不替代公开接口契约。

## 双语索引

| 中文 | English |
| --- | --- |
| [架构](./architecture.zh-CN.md) | [Architecture](./architecture.md) |
| [快速开始](./getting-started.zh-CN.md) | [Getting started](./getting-started.md) |
| [产品导览](./product-tour.zh-CN.md) | [Product tour](./product-tour.md) |
| [概念](./concepts.zh-CN.md) | [Concepts](./concepts.md) |
| [部署](./deployment.zh-CN.md) | [Deployment](./deployment.md) |
| [开发](./development.zh-CN.md) | [Development](./development.md) |
| [安全](./security.zh-CN.md) | [Security](./security.md) |
| [AAP 对接](./aap-integration-guide.zh-CN.md) | [AAP integration](./aap-integration-guide.md) |

## 维护状态

目前未找到公开版本 tag、发布说明或生产 SLO/SLA。仓库使用 [Apache License 2.0](../LICENSE) 开源。部署方应依据[部署](./deployment.zh-CN.md)、[安全](../SECURITY.md)和自身环境验证决定是否上线。
