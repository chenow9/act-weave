# AAP 接入索引

[English](./aap.md) · [完整中文对接指南](../aap-integration-guide.zh-CN.md) · [文档首页](../README.zh-CN.md)

AAP（Agent Access Protocol）是应用调用 ActWeave Agent Runtime 的路径，不是 Console 管理 API。它使用 `/api/agent-access/v1`，调用方以 AAP access token 调用，而非控制台用户 Session。

## 交付给集成方的材料

1. [AAP 对接指南](../aap-integration-guide.zh-CN.md)：认证、scope、Conversation、Run、SSE、错误、CORS、密钥轮换和上线检查（含可选 [A2UI §9.2](../aap-integration-guide.zh-CN.md#92-a2ui可选附加) 与[出站附件 §9.3](../aap-integration-guide.zh-CN.md#93-出站附件可选附加)）。
2. [OpenAPI](../openapi/agent-access-v1.yaml)：机器可读 HTTP 契约和字段 schema 的事实来源。
3. [TypeScript SDK](../../sdk/typescript/)：`@actweave/agent-client`（`joinTextParts` / `findA2UIPart` / `findOutputFileParts`，以及 `enableA2UI` 时读 surface 的 `isKnownA2UICatalog` / `resolveBinding` / `iterCharts`）。
4. [AAP Chat Demo](../../demos/aap-chat/)：BFF 持有 Client Secret 的本地演示。Mock story `export-csv` 与 Live hydrate 会渲染助手 `output_file` 卡片。
5. 可选 A2UI 见[对接指南 §9.2](../aap-integration-guide.zh-CN.md#92-a2ui可选附加)（MVP `actions: false`；surface 经 catalog 校验）。
6. 可选出站附件见[对接指南 §9.3](../aap-integration-guide.zh-CN.md#93-出站附件可选附加)（纯文本 `actweave.publish_attachment`；出站无病毒扫描）。

## 最短调用链

```text
Client credentials / private_key_jwt
  → AAP access token
  → Conversation
  → Run
  → SSE events (Last-Event-ID reconnect)
```

Workspace 管理员可在控制台 **Agent Access → Client 详情 → 导出接入配置** 把 Workspace / Client / Agent / Scope 交给集成方（`.env` 或 JSON）。Client Secret 不会出现在导出中。

当前默认部署接受文本 `input`。文件上传路由存在但默认关闭；端到端多模态还需 `runtimeMultimodal`。可选 A2UI 默认关闭（`context_policy.aap.enableA2UI`）；开启后文本仍为一等，`a2ui` 仅在 `item.completed` 上出现（`streaming: false`，`actions: false`），且每个 surface 都符合所广告的组件 catalog。可选出站附件默认关闭（files HTTP 白名单 + `runtimeOutboundAttachments` + `enableOutboundAttachments` + 支持工具的 `toolCalling`）；开启后助手 `output_file` 仅出现在 `item.completed`，v1 发布为纯文本（`actweave.publish_attachment`，≤256 KiB），出站不做病毒扫描。不要在浏览器保存长期 Client Secret，也不要用 `/api/v1` 作为第三方调用入口——它对第三方开放的只有 A2UI schema 分发（`GET /api/v1/a2ui/catalogs/standard/v1/catalog.json`），返回的是静态文档，不含任何工作区数据。Console 的 session+message 文件代理是运营聊天路径，不是第三方 API。

关于 AAP 与 A2A 的边界见[概念](../concepts.zh-CN.md)。
