# AAP 接入索引

[English](./aap.md) · [完整中文对接指南](../aap-integration-guide.zh-CN.md) · [文档首页](../README.zh-CN.md)

AAP（Agent Access Protocol）是应用调用 ActWeave Agent Runtime 的路径，不是 Console 管理 API。它使用 `/api/agent-access/v1`，调用方以 AAP access token 调用，而非控制台用户 Session。

## 交付给集成方的材料

1. [AAP 对接指南](../aap-integration-guide.zh-CN.md)：认证、scope、Conversation、Run、SSE、错误、CORS、密钥轮换和上线检查。
2. [OpenAPI](../openapi/agent-access-v1.yaml)：机器可读 HTTP 契约和字段 schema 的事实来源。
3. [TypeScript SDK](../../sdk/typescript/)：`@actweave/agent-client`。
4. [AAP Chat Demo](../../demos/aap-chat/)：BFF 持有 Client Secret 的本地演示。

## 最短调用链

```text
Client credentials / private_key_jwt
  → AAP access token
  → Conversation
  → Run
  → SSE events (Last-Event-ID reconnect)
```

当前默认部署接受文本 `input`。文件上传路由存在但默认关闭；端到端多模态还需 `runtimeMultimodal`。不要在浏览器保存长期 Client Secret，也不要用 `/api/v1` 作为第三方调用入口。

关于 AAP 与 A2A 的边界见[概念](../concepts.zh-CN.md)。
