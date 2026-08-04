# 安全说明

[English](./security.md) · [根安全策略](../SECURITY.md) · [文档首页](./README.zh-CN.md)

本页是安全边界与运维注意事项的索引，不替代部署方的威胁建模或合规评估。

## 已有的相关实现线索

- Console 管理面和 AAP 运行面使用不同路径与认证中间件；AAP access token 不复用 Console 用户 Session JWT。
- AAP Client、credential、grant、scope、workspace/agent 约束和 external subject 由管理面配置。
- Tool 运行路径包含 SSRF 防护、Secret 注入、响应上限与幂等约束；Provider/Connection 管理上游端点和出站身份。
- AAP token 使用 EdDSA/Ed25519 签名并公开 JWKS；配置支持 key ID 和轮换相关字段。
- 审计、持久对象和文件路径都有访问/保留控制代码与运行手册；能否读取具体正文仍受权限与配置影响。

这些实现不等同于对任意部署的安全保证。生产部署仍应阅读[部署说明](./deployment.zh-CN.md)并完成自身验证。

## 操作要求

- 不要在 README、Issue、日志、截图、Demo 或浏览器存储中提交 Client Secret、私钥、JWT、数据库密码、对象存储密钥、presigned URL 或真实业务数据。
- 使用 BFF 作为浏览器接入的默认模式；只有确有必要时才为 AAP Client 配置精确 HTTPS CORS origin。
- 生产环境应替换开发配置、启动管理员、JWT/加密密钥、AAP 签名密钥和全部 Compose 服务凭证。
- 在开放 Provider、Connection、A2A remote 或 AAP Client 前，校验主机 allowlist、scope、网络边界与最小权限。
- AAP 文件功能默认关闭；不要在完成对象存储可达性、GC、配额和代理验证前启用。

报告漏洞请使用[根安全策略](../SECURITY.md)；当前仓库未配置专用安全邮箱，相关未决项也记录在那里。
