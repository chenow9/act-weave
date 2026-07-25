# Agent Access Protocol — BFF 与短期委托 Token 示例

本目录演示 v1 推荐的两种 Web/App 接入拓扑，并接入 `@actweave/agent-client` 的 `TokenProvider` / `AgentAccessClient`。

完整接入文档：

- Developer Guide（Quickstart）：[`docs/guides/agent-access-developer-guide.md`](../../docs/guides/agent-access-developer-guide.md)
- API Reference：[`docs/guides/agent-access-api-reference.md`](../../docs/guides/agent-access-api-reference.md)
- Operator Runbook：[`docs/runbooks/agent-access-operator-runbook.md`](../../docs/runbooks/agent-access-operator-runbook.md)
- Migration Guide：[`docs/guides/agent-access-migration-guide.md`](../../docs/guides/agent-access-migration-guide.md)

> **不要**把本示例中的占位 Secret 用于生产。所有密钥只来自环境变量（见 `.env.example`）。

## 安全边界（必读）

```text
┌──────────────────────────────────────────────────────────────────────┐
│  Browser / Native App                                                │
│  - 业务 Session（你们自己的登录态）                                    │
│  - 可选：短期 AAP Access Token（仅内存 MemoryTokenProvider）          │
│  - 禁止：Client Secret、长期 Credential、localStorage / Cookie /     │
│          URL Query 存放 AAP Token（禁止 access_token= 形式）          │
└───────────────┬──────────────────────────────▲───────────────────────┘
                │                              │
     (1) BFF 默认拓扑                    (2) 短期委托 Token 拓扑
                │                              │
                ▼                              │
┌───────────────────────────┐    ┌─────────────┴──────────────────────┐
│  Business BFF             │    │  Business Mint Service             │
│  - 持有 Client Secret     │    │  - 校验业务 Session                 │
│  - client_credentials     │    │  - 签发/持有 Subject JWT            │
│  - 代理 SSE / Cancel      │    │  - RFC 8693 Token Exchange         │
│  - 传递 Abort + 背压      │    │  - 返回 5～10 分钟短 Token（no-store）│
└─────────────┬─────────────┘    └─────────────┬──────────────────────┘
              │                                │
              └────────────┬───────────────────┘
                           ▼
              ┌────────────────────────────┐
              │  ActWeave AAP              │
              │  /oauth/token              │
              │  /workspaces/.../events    │
              └────────────────────────────┘
```

| 规则 | BFF | 直连短 Token |
| --- | --- | --- |
| Client Secret 位置 | 仅 BFF 进程环境变量 | 仅 Mint 服务进程环境变量 |
| 浏览器是否见 AAP Token | 否 | 是（短 TTL，仅堆内存） |
| AAP CORS | 可关闭（浏览器不直连 AAP） | 需精确 Origin 白名单 |
| Token 存 localStorage / Cookie / `access_token=` Query | **禁止** | **禁止** |
| SSE Authorization | BFF → AAP 的 `Authorization: Bearer` | 浏览器 → AAP 的 `Authorization: Bearer` |

## 1) BFF 代理（默认）

路径：`bff/`

1. BFF 用 `client_credentials` + `MemoryTokenProvider.refresh` 换短期 AAP Token。
2. 浏览器只携带**业务 Session** 访问 BFF：
   - `GET /api/aap/runs/:runId/events`（可带 `Last-Event-ID`）
   - `POST /api/aap/runs/:runId/cancel`
3. BFF 将上游 AAP SSE 以 pull 方式转发，浏览器断开时 `abort` 上游（取消传播），`res.write` + `drain` 处理背压。

相关文件：

- `bff/server.ts` — 最小 Node HTTP BFF
- `bff/proxy-sse.ts` — SSE 代理、取消、背压、Last-Event-ID
- `shared/aap-oauth.ts` — `client_credentials` / Token Exchange

## 2) 短期委托 Token（Token Exchange）

路径：`direct/`

1. Mint 服务在服务端校验业务 Session，构造 Subject JWT，调用 RFC 8693 Token Exchange。
2. 响应 JSON：`{ accessToken, expiresIn }`，`Cache-Control: no-store`，**不** `Set-Cookie`。
3. 浏览器：

```ts
import { createBrowserDirectClient } from "./direct/browser-client.js";

const browser = createBrowserDirectClient({
  aapBaseUrl: import.meta.env.AAP_BASE_URL, // 无密钥
  mintUrl: "https://app.example.com/api/aap/mint-token",
  getBusinessAuthorization: () => `Bearer ${businessSession}`,
});

// Token 只在 MemoryTokenProvider 内存中；logout 时：
browser.clearAAPToken();
```

相关文件：

- `direct/mint-server.ts` — 业务侧 mint / Token Exchange
- `direct/browser-client.ts` — `MemoryTokenProvider` + `AgentAccessClient`

## 环境变量

```bash
cp .env.example .env
# 填写 AAP_BASE_URL / AAP_CLIENT_ID / AAP_CLIENT_SECRET 等
# 切勿把 .env 提交到仓库
```

`.env.example` 中 **没有** 真实 Secret 默认值；`AAP_CLIENT_SECRET` 必须由部署注入。

## 验证

```bash
# 需先构建 SDK：cd ../../sdk/typescript && npm run build
cd examples/agent-access
npm install
npm test
npm run security-scan
# 等价清单扫描（文档中的禁止说明可命中，源码不得命中）：
# rg -n 'localStorage|access_token=' .
```

测试覆盖：

- BFF：`client_credentials` → `TokenProvider`、SSE 代理取消/背压、业务 Session 门禁、无 Set-Cookie
- 直连：Token Exchange form、mint `no-store`、浏览器 `MemoryTokenProvider` 跟随 SSE
- 安全扫描：源码无 `localStorage` / `access_token=`

## 明确不做（留给 M9-T7）

- 真实 AAP E2E / Golden Trace 契约验收
- 生产级业务 Session、Subject JWT 签发与 JWKS 轮换
- 浏览器打包与 CORS 生产配置自动化

## 设计偏差

- Mint 服务的 demo `mintSubjectToken` 返回占位串；接入真实 Trusted Subject Issuer 签名 JWT 属于部署配置，不是本示例的硬编码密钥。
- 本地 demo 允许 `http://127.0.0.1` AAP base；非 loopback 要求 `https:`。
