# ActWeave AAP Chat Demo

一套与 ActWeave 控制台风格一致的 **Agent Access Protocol（AAP）对话 Demo**。

- 富文本：Markdown、数学公式（KaTeX）、代码高亮、图片  
- **附件**：选文件 / 拖拽 / 粘贴 → 上传 → 带 `fileId` 发送 → 对话气泡渲染预览  
- 架构：**BFF 持有 Client Secret**，浏览器只拿短期 Access Token  
- 两种模式：**Live AAP**（真实 Agent） / **Mock**（离线富文本预览）

## 快速开始（Mock UI）

无需 AAP 凭证，先看对话框与渲染效果：

```bash
cd demos/aap-chat
npm install
npm run dev:mock
```

打开 [http://127.0.0.1:5188](http://127.0.0.1:5188)，点击建议气泡或「插入富文本样例」。  
也可点作曲家左侧 **附件** 选择图片/PDF：Mock 模式会在气泡中渲染本地预览（不上传）。

## 附件（选文件 · 上传 · 气泡渲染）

| 能力 | 说明 |
| --- | --- |
| 选择 | 曲作家「附件」按钮、拖到输入区、或粘贴图片 |
| 类型 / 限制 | `png` / `jpeg` / `webp` / `gif` / `pdf`，单文件 ≤ 25MB，最多 8 个 |
| Mock | 本地 Object URL 预览进用户气泡，不走 AAP |
| Live | 浏览器用短期 Token：`createFile` → 预签名 PUT → `complete` → `waitUntilReady`，再 `POST /bff/chat` 带 `fileIds` |
| 协议 | BFF 将每个 `fileId` 编入 user message 的 `input_file` content part |
| 气泡 | 图片缩略图可点击放大；PDF 显示文件名 + 大小 + 短 fileId |

Live 附件还需要：

1. `.env` 的 `AAP_SCOPES` 含 `file:write file:read`（见 `.env.example`）  
2. ActWeave 服务端 `agentAccess.files.enabled=true`，并完成文件相关 migration / MinIO  
3. Agent 侧若做多模态理解，需启用 RuntimeMultimodal（见对接指南）

## 对接真实 AAP

### 1. 准备凭证

向 ActWeave 管理员索取：

| 变量 | 说明 |
| --- | --- |
| `AAP_BASE_URL` | 如 `http://127.0.0.1:8082/api/agent-access/v1` |
| `AAP_CLIENT_ID` / `AAP_CLIENT_SECRET` | Agent Access Client |
| `AAP_WORKSPACE_ID` / `AAP_AGENT_ID` | 目标 Workspace + Agent |
| `AAP_SCOPES` | Grant 子集（见 `.env.example`） |
| `OUTBOUND_CONNECTION_ID` | 可选；业务 Service Connection UUID（`REQUEST_PASSTHROUGH`） |
| 业务 ACCESS_TOKEN | **不要**写进 git；页面「绑定到 BFF」或本地 `OUTBOUND_ACCESS_TOKEN` |

### 2. 配置

```bash
cp .env.example .env
# 编辑 .env 填入凭证
```

若 Agent 能力绑定了 **REQUEST_PASSTHROUGH** Connection（如「AI识别管理平台」），还必须：

1. `.env` 中填写 `OUTBOUND_CONNECTION_ID`（控制台 → 服务连接 → 该 Connection 的 ID）  
2. 启动后在 Demo 页顶部 **绑定业务 ACCESS_TOKEN**（Token 只进 BFF 内存）  
3. 确认 Agent 能力绑定的 `connection_id` 指向同一 Connection（否则工具不会注入业务鉴权）

### 3. 启动

```bash
npm install
npm run dev
```

- UI：http://127.0.0.1:5188  
- BFF：http://127.0.0.1:8790（Vite 将 `/bff` 代理到此）

BFF 已配置且凭证有效时，页面右上角显示 **Live AAP**；否则自动回落 Mock。  
配置了 `OUTBOUND_CONNECTION_ID` 但未绑定业务 Token 时，发送消息会提示先绑定出站凭证。

## 架构

```text
Browser (Vite UI)
   │  POST /bff/outbound-credentials  { value }   ← 可选：业务 Token 仅存 BFF
   │  SDK createFile / put / complete / waitUntilReady  ← 附件（短期 token）
   │  POST /bff/chat  { text, conversationId?, fileIds? }
   ▼
BFF (server/index.mjs)
   │  client_credentials → access_token
   │  create conversation + run（content: text + input_file*）
   │  (+ outboundCredentials write-only envelope when bound)
   ▼
AAP  /api/agent-access/v1
   ▲
Browser SDK followRun(SSE)  ← 仅使用短期 access_token
```

**禁止**把 `AAP_CLIENT_SECRET` 或业务 ACCESS_TOKEN 放进前端或浏览器存储。

## 富文本能力

| 类型 | 实现 |
| --- | --- |
| Markdown | `markdown-it` |
| 数学 | `markdown-it-texmath` + `katex`（`$...$` / `$$...$$`） |
| 代码 | `highlight.js` |
| 图片 | 允许 `http(s)://`，DOMPurify 消毒 |
| 附件 | 曲作家选/拖/粘贴；Live 走 AAP File；气泡内图/PDF 卡片 |
| 工具调用 | 渲染 AAP `tool_call` item 卡片 |

## 主要文件

```text
demos/aap-chat/
  server/index.mjs      # Token + createRun BFF
  client/src/main.ts    # 对话框 UI
  client/src/markdown.ts
  client/src/aap.ts     # SDK 封装
  client/src/mock-stream.ts
  .env.example
```

## 故障排查

| 现象 | 处理 |
| --- | --- |
| 一直 Mock | 检查 `.env` 与 `npm run dev`（需 BFF） |
| `token failed` | Client/Secret/Agent 是否匹配 Grant |
| SSE 连不上 | 确认 AAP 已启用且 CORS 策略；本 Demo 用 BFF 签发 token 后浏览器直连 AAP base |
| `OUTBOUND_CREDENTIAL_REQUIRED` | 配置 `OUTBOUND_CONNECTION_ID` 并在页面绑定业务 Token |
| 工具 401 / 出站失败 | ① 业务 Token 是否有效 ② Agent 能力是否绑定了该 Connection ③ Connection 模式是否为 REQUEST_PASSTHROUGH |
| 附件上传 403 / scope | Grant 是否含 `file:write`/`file:read`；`AAP_SCOPES` 是否包含 |
| 附件 `files disabled` | 服务端 `agentAccess.files.enabled` 是否打开 |
| 上传后 createRun 无多模态 | Agent / RuntimeMultimodal 与文件 READY 状态 |

更完整的协议说明见仓库根目录：

- `docs/aap-integration-guide.zh-CN.md`
- `sdk/typescript/README.md`
