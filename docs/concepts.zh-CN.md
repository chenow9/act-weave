# 概念与协议边界

[English](./concepts.md) · [文档首页](./README.zh-CN.md)

本页说明各概念在 ActWeave 中的职责。它不是通用协议实现清单：只有当前仓库可核实的实现才标为「已支持」。

## 概念速查

| 概念 | 在 ActWeave 中承担的职责 | 当前状态 |
| --- | --- | --- |
| **Workspace** | 配置和运行数据的业务边界；Console 侧由成员关系和 RBAC 约束。 | 已支持 |
| **Provider** | 上游业务服务及其服务发现/认证契约的配置对象。 | 已支持 |
| **Service Connection** | Provider 的运行连接、环境和出站身份配置。 | 已支持 |
| **OpenAPI** | 描述/导入既有 HTTP 服务，将 endpoint 生成或同步为 Tool 草稿。 | 已支持 |
| **Tool** | Agent/Workflow 可调用的结构化业务能力，包含 Schema、action、Connection 和发布状态。 | 已支持 |
| **Workflow** | 以图表示的确定性多步骤执行；有草稿、编译、试跑与发布。 | 已支持 |
| **Agent** | 使用模型、提示词、上下文和已绑定能力进行动态决策的运行单元。 | 已支持 |
| **AAP** | 面向业务应用的 ActWeave Agent Runtime 接入协议。 | 已支持 |
| **A2A** | Agent 发现、委派和任务协作；也可用于 ActWeave 与外部 Agent 的网关路径。 | 已支持，需按 exposure/remote 配置 |
| **MCP** | 通常用于向 Agent 提供标准化工具能力。 | 当前仓库未实现 MCP server/client 运行面 |
| **Console API** | 管理 Workspace、Agent、Tool、Workflow、Client 等控制面资源。 | 已支持；不是第三方运行入口 |

## Tool、Provider 与 Connection

三者不是同一个对象：

- **Provider** 描述上游服务，以及可选的 OpenAPI 文档和认证契约。
- **Connection** 绑定一个 Provider 的实际运行端点、环境和出站身份。前端与后端能找到 `REQUEST_PASSTHROUGH` 和 Broker/OBO 配置路径。
- **Tool** 定义可调用的业务动作及其输入/输出 Schema，并引用运行所需 Connection；它通过测试和发布后才可作为已发布能力被绑定。

因此 OpenAPI 是导入/描述来源，不是运行调用本身；Tool 才是 Agent 或 Workflow 面对的可调用契约。

## Agent 与 Workflow

**Agent** 在模型、提示词、上下文和已绑定能力基础上作动态决策，例如选择调用哪个 Tool，或委派给另一个 Agent。

**Workflow** 是确定性的多步骤图。当前实现覆盖图草稿、编译、试跑、修订与发布。Agent 可以绑定 Tool/Workflow；Workflow 也会通过 Tool Runtime 调用已配置的业务能力。

生成对话位于 Workflow 编辑器内，可帮助生成图草案。它不再是独立的 Console 页面，文档也不把它描述为自动发布或无审批上线。

## 管理面与运行面

| 项目 | Console API（管理面） | AAP（运行面） |
| --- | --- | --- |
| 基础路径 | `/api/v1` | `/api/agent-access/v1` |
| 调用主体 | 已登录的 Console 用户 | Web/App/BFF/第三方系统的 AAP Client 或授权 subject |
| 认证 | 用户会话访问令牌，后端重新校验身份和权限 | AAP access token；Client 凭证或私钥 JWT token exchange 后使用 |
| 主要资源 | Workspace、Provider、Connection、Tool、Agent、Workflow、Client/grant | Agent profile、Conversation、Run、SSE、Interaction、可选文件接口 |
| 权限模型 | Workspace RBAC 与平台管理员权限 | Client/grant、workspace、agent、scope 与 subject 约束 |

外部集成方应使用 AAP，而不是重放/借用 Console 用户 Session。参见[AAP 对接指南](./aap-integration-guide.zh-CN.md)。

## AAP、A2A 与 MCP

### AAP：应用调用托管 Runtime

AAP 面向 Web、App、BFF 和第三方业务系统。它定义 Client 到 Runtime 的身份、授权、Conversation/Run 生命周期、幂等请求和 SSE 事件消费。仓库包含：

- OAuth token endpoint 和 JWKS；
- AAP Client、credential、grant 和 external subject 的管理面；
- Conversation、Run、取消、Interaction 决策和 SSE 路由；
- [OpenAPI 契约](./openapi/agent-access-v1.yaml)、协议 schema 与 [TypeScript SDK](../sdk/typescript/)。

### A2A：Agent 间协作

当前代码包含：同 Workspace Agent 委派绑定、A2A inbound exposure、Agent Card、持久任务存储、取消路径和 A2A outbound remote。入站 exposure 使用允许列表，生产默认不允许 `AuthMode=NONE`。外部 A2A 互操作仍取决于双方 Agent Card、认证方式和网络/主机允许列表，不能简化成「任意 A2A Agent 都可直接接入」。

### 为什么已有 A2A 仍需要 AAP？

A2A 的主调用者是 **Agent**，它围绕发现、任务和委派协作组织；AAP 的主调用者是 **业务应用或其 BFF**，它围绕应用身份、Conversation、Run、SSE 和应用侧会话组织。它们的调用主体、会话/任务生命周期、认证入口和权限边界不同。实践中，一个 BFF 可以用 AAP 调用主 Agent，而主 Agent 再经配置的委派或 A2A 与其他 Agent 协作。

### MCP：当前边界

MCP 是常见的 Agent 工具标准，但当前仓库未发现 MCP server、MCP client 或 MCP 公开端点。Tool 具备自己的 Provider/Connection/Schema/Runtime 路径；不要把这种 Tool 治理等同于已实现 MCP 兼容。

## Conversation、Run 与 Trace

- **Conversation**：AAP 运行会话边界。由授权调用方创建并被其权限范围约束。
- **Run**：一次 Agent 运行。AAP 可创建、查询、取消并通过 SSE 跟随运行事件；`Last-Event-ID` 支持从 PostgreSQL 事件事实回放。
- **Trace**：审计查询视角，可将发起方、Run、模型 turn、委派、Workflow 步骤和 Tool 调用串联起来。

可阅读的正文、请求/响应字段取决于数据保存、访问角色和调试设置。不要依赖文档假设所有内容总会保留或显示。

## 进一步阅读

- [系统架构](./architecture.zh-CN.md)
- [OpenAPI 到 Tool](./integrations/openapi.md)
- [AAP 对接指南](./aap-integration-guide.zh-CN.md)
- [产品导览](./product-tour.zh-CN.md)
