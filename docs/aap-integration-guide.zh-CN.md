# ActWeave Agent Access Protocol（AAP）— 第三方对接指南

[English](./aap-integration-guide.md) · [文档首页](./README.zh-CN.md) · [AAP 接入索引](./integrations/aap.zh-CN.md)

| | |
| --- | --- |
| **读者** | 需要对接 ActWeave Agent 的外部平台 / 集成方 |
| **协议根路径** | `/api/agent-access/v1` |
| **机器可读契约** | [`openapi/agent-access-v1.yaml`](./openapi/agent-access-v1.yaml) |
| **TypeScript SDK** | `sdk/typescript` → `@actweave/agent-client` |
| **英文版（同等内容）** | [`aap-integration-guide.md`](./aap-integration-guide.md) |

本文是第三方对接的详细交付说明。字段级 schema、枚举以 OpenAPI / Schema Registry 为准，不要仅凭文字臆造字段。

---

## 1. AAP 是什么

**Agent Access Protocol（AAP）** 是面向服务主体的版本化对外运行 API，用于：

1. 以 **Agent Access Client** 身份完成客户端认证  
2. 获取绑定 **一个 Workspace + 一个 Agent** 的**短期 Access Token**  
3. 创建 **Conversation** 与 **Run**  
4. 通过 SSE 跟随 **Run 事件**（`Last-Event-ID` 断线续传）  
5. 对 **Interaction** 做 approve / decline / cancel  
6. （可选，**默认关闭**）上传 **File**、等待 ready，并在 Run 输入中以 `input_file` 部分引用  
7. （可选，**默认关闭**）当 Agent 开启 `enableA2UI` 时，在助手消息上接收附加的 **A2UI** 声明式界面（MVP 仅展示、无动作回传）  
8. （可选，**默认关闭**）当 files HTTP 门（含 workspace/client 白名单）、`runtimeOutboundAttachments` 与 Agent `enableOutboundAttachments` 均打开，且冻结的 `toolCalling` 支持工具时，在助手消息上接收 **出站附件**（`output_file`）。v1 发布通道为纯文本工具 `actweave.publish_attachment`；**没有**公开 ingest HTTP  
9. （可选，**默认关闭**）当 files HTTP 门、`runtimeInboundRead` 与 Agent `enableInboundRead` 均打开，且冻结的 `toolCalling` 支持工具时，Agent 可用平台工具 `actweave.read_attachment` **按需读取入站 PDF 文本**。用户消息契约不变（仍是 `input_file` + `fileId`；见 [§9.4](#94-入站按需读取可选附加)）

AAP **不是** ActWeave 管理控制台 API（`/api/v1`）。控制台用户 Session JWT 在 AAP 路由上会被 **拒绝**。Console 聊天可以把会话消息上引用的文件经 session+message 代理渲染出来（见 [§9.3](#93-出站附件可选附加)）；那**不是**文件管理 UI，也**不是** AAP 路由。

| 面 | 路径前缀 | 鉴权 | 使用者 |
| --- | --- | --- | --- |
| **AAP（本文）** | `/api/agent-access/v1` | AAP Access Token | 你的后端 / BFF / 发 token 服务 |
| 控制台 / 管理 | `/api/v1` | 平台用户 Session | ActWeave UI 与内部运维 |

---

## 2. 需要向 ActWeave 管理员索取的材料

| 项 | 说明 |
| --- | --- |
| **Base URL** | 例如 `https://actweave.example.com/api/agent-access/v1` |
| **Workspace ID** | UUID |
| **Agent ID** | 可调用的 Agent UUID |
| **Client ID** | Agent Access Client 标识 |
| **Client 凭证** | 一次性 **Client Secret**（仅展示一次），或 `private_key_jwt` 用的私钥 + 已登记 JWKS |
| **已授权 Scope** | 上限；Token 请求必须是其子集 |
| **CORS 策略** | 优先 **禁止浏览器直连**（走 BFF）。若必须浏览器直连：仅精确 HTTPS Origin |

Workspace 管理员可在控制台 **Agent Access → Client 详情 → 导出接入配置** 一次性导出上表中的公开字段（`.env` 或 JSON）。**Client Secret 不会被导出**——它只在创建或轮换凭证时展示一次，需另行交给集成方的密钥系统。

Client Secret / 私钥只应保存在**你方**密钥管理系统中。密钥不会出现在 Protocol Event、审计详情或应用日志里。

---

## 3. 核心概念

| 概念 | 含义 |
| --- | --- |
| **Agent Access Client** | 在 Workspace 中登记的 OAuth 风格客户端 |
| **Grant** | Client + Agent(s) + scopes（及可选策略）的授权 |
| **Access Token** | 短期 JWT（`EdDSA` / `at+jwt`），绑定一个 Workspace、一个 Agent、Client、主体及可选 External Subject |
| **Conversation** | 针对某一 Agent 的对话容器 |
| **Run** | Conversation 下的一次执行。默认输入为 **text**；在开启 AAP files 后，用户消息还可包含引用稳定 `fileId` 的 `input_file` 部分 |
| **File** | 可选对象，含生命周期状态。入站上传：`pending_upload` → … → `ready`，以 `input_file` 引用。Agent 生成的出站文件以 READY 写入，`purpose=AGENT_OUTPUT`，以 `output_file` 引用。GET 状态为事实源（v1 无 File SSE） |
| **A2UI** | 助手消息上可选的声明式 UI（`type: "a2ui"` content part）。Agent 级 `enableA2UI`，默认关闭。文本始终一等 |
| **出站附件** | 助手消息上可选的 `output_file` content part（仅稳定 `fileId` + 展示元数据，永不含 URL）。默认关闭；三道门 + `toolCalling` — 见 [§9.3](#93-出站附件可选附加) |
| **入站按需读取** | 可选平台工具 `actweave.read_attachment`，模型需要时按 `fileId` 抽取 PDF 文本。默认关闭。用户持久化 part 仍是 `input_file` — 见 [§9.4](#94-入站按需读取可选附加) |
| **Protocol Event** | Run 流上的持久事实（`sequence` 为游标） |
| **Interaction** | Run 因确认而等待 |
| **External Subject** | 通过 Token Exchange 绑定的终端用户身份（可选） |

每次调用的有效权限：

```text
Token scope ∩ Grant ∩ Agent 策略 ∩ Workspace 状态 ∩ Subject 归属
```

---

## 4. 推荐集成拓扑

| 拓扑 | 谁持有长期凭证 | 建议 |
| --- | --- | --- |
| **BFF（默认）** | 仅你的后端 | **生产默认**。浏览器只访问你的域名；BFF 持有 Client Secret、签发 Token，必要时代理 SSE / cancel |
| **纯服务端** | 你的后端 | 自动化场景用 `client_credentials` |
| **短期 mint + 浏览器** | mint 服务持有密钥；浏览器仅内存持有短期 Access Token | 终端用户用 Token Exchange；禁止把密钥放进 storage / cookie / URL |

**禁止**将 Client Secret、私钥或 Access Token 放入：

- URL / query / fragment  
- Cookie  
- `localStorage` / `sessionStorage`  

---

## 5. 版本策略

| 维度 | 含义 |
| --- | --- |
| URL `/v1` | 资源 / 命令主版本 |
| 请求头 `ActWeave-Protocol-Version: YYYY-MM-DD` | 可选冻结快照（服务端回显实际版本） |
| 事件 `specVersion=1.0` | v1 内信封主版本 |

**客户端兼容规则：** 忽略未知事件类型与未知字段，但只要出现 `id:` 就必须推进 sequence 游标。

v1 内允许加性变更（可选字段、新事件类型）。破坏性变更需要新主版本路径或新事件名。

---

## 6. 认证

### 6.1 Token 端点

```http
POST /api/agent-access/v1/oauth/token
Content-Type: application/x-www-form-urlencoded
```

- 请求体上限：**32 KiB**  
- 成功响应：`Cache-Control: no-store`  
- **永不**签发 `refresh_token`  
- 默认 Access Token TTL：约 **10 分钟**（**5–15 分钟**，并受 Client 配置、服务端签名窗口、Grant 到期约束）

#### grant_type

| `grant_type` | 用途 |
| --- | --- |
| `client_credentials` | 服务主体 Token（一个 `agent_id` + `scope`） |
| `urn:ietf:params:oauth:grant-type:token-exchange` | 通过 `subject_token` JWT 绑定 External Subject（RFC 8693） |

#### 客户端认证（每次请求只能一种）

| 模式 | 方式 |
| --- | --- |
| `client_secret_basic` | HTTP Basic `client_id:client_secret` |
| `private_key_jwt` | `client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer` + `client_assertion`（仅 EdDSA 或 PS256） |

不要在同一请求混用 Basic 与 assertion。`client_secret_post` 会被拒绝。

#### 示例：client_credentials

```http
POST /api/agent-access/v1/oauth/token
Authorization: Basic base64(<client_id>:<client_secret>)
Content-Type: application/x-www-form-urlencoded

grant_type=client_credentials
&agent_id=<agent-uuid>
&scope=agent:read conversation:create conversation:read run:create run:read run:cancel event:read interaction:decide
```

响应示例：

```json
{
  "access_token": "<short-lived-jwt>",
  "token_type": "Bearer",
  "expires_in": 600,
  "scope": "agent:read conversation:create conversation:read run:create run:read run:cancel event:read interaction:decide"
}
```

#### 示例：Token Exchange（绑定终端用户）

```http
POST /api/agent-access/v1/oauth/token
Authorization: Basic base64(<client_id>:<client_secret>)
Content-Type: application/x-www-form-urlencoded

grant_type=urn:ietf:params:oauth:grant-type:token-exchange
&agent_id=<agent-uuid>
&subject_token=<user-jwt>
&subject_token_type=urn:ietf:params:oauth:token-type:jwt
&requested_token_type=urn:ietf:params:oauth:token-type:access_token
&scope=...
```

受信任 Subject Issuer（精确 Issuer / Audience、算法白名单、JWKS）由 ActWeave 管理员按 Client 配置，需与你方 IdP 一致。

### 6.2 数据面使用 Access Token

```http
Authorization: Bearer <access_token>
```

| 规则 | 说明 |
| --- | --- |
| Token 形态 | 非对称 **EdDSA**，`typ=at+jwt`，固定 AAP audience |
| 公钥 JWKS | `GET /api/agent-access/v1/.well-known/jwks.json`（仅公开 OKP 字段） |
| 管理端用户 JWT | AAP 上 **不接受**（401） |
| Token 放在 query / cookie | **禁止** |
| 实时吊销 | 普通请求会比对 Token `ver` 与当前 security version；撤销 / 禁用后旧 Token 立即失效。活动中的 SSE 在有界窗口内重验（≤ 60s） |

遇到 `TOKEN_EXPIRED` 或撤销断连：签发**新** Token，并用**相同** `Last-Event-ID` 重连。

---

## 7. Scope

Grant 的 scope 是**上限**。每次 Token 请求只能申请**子集**。

| Scope | 能力 |
| --- | --- |
| `agent:read` | 读 Agent profile |
| `conversation:create` | 创建对话 |
| `conversation:read` | 读对话 |
| `run:create` | 创建 Run |
| `run:read` | 读 Run 状态 |
| `run:cancel` | 取消 Run |
| `event:read` | SSE / 事件跟随 |
| `interaction:decide` | 审批 Interaction |
| `artifact:read` | 制品访问（在授权时） |
| `file:write` | 创建上传意图 + complete（files 功能开启时） |
| `file:read` | 读文件状态 / 内容 / 签发下载（files 功能开启时） |

最小权限：只申请集成实际需要的 scope。在运营方为工作区/客户端启用 `agentAccess.files` 之前，file scope 无效。

---

## 8. 端到端快速接入

占位符：`{base}`、`{wid}`、`{aid}`、凭证。

### 步骤 1 — 签发 Access Token

见 [§6](#6-认证)。

### 步骤 2 — 创建 Conversation

```http
POST {base}/workspaces/{wid}/agents/{aid}/conversations
Authorization: Bearer <access_token>
Idempotency-Key: <canonical-uuid>
Content-Type: application/json

{"title":"Support ticket 42"}
```

### 步骤 3 — 创建 Run

```http
POST {base}/workspaces/{wid}/agents/{aid}/runs
Authorization: Bearer <access_token>
Idempotency-Key: <canonical-uuid>
Content-Type: application/json

{
  "conversationId": "<conversation-uuid>",
  "input": [{"type":"text","text":"Summarize the ticket"}]
}
```

默认部署接受 **text** 消息内容。当 AAP files 已启用且文件为 `ready` 时，还可附加 `{ "type": "input_file", "fileId": "<uuid>" }` 部分（见本页「9.1 文件（可选）」）。`createRun` **拒绝** `output_file`（`UNSUPPORTED_CONTENT_TYPE`）；助手 `output_file` 仅在出站附件开启时出现在 `item.completed` 上（见 [§9.3](#93-出站附件可选附加)）。未知内容类型 → `UNSUPPORTED_CONTENT_TYPE`。**图片** `input_file` 组装需要 **`RuntimeMultimodal`**（运营开关；默认关闭）。纯文档 `input_file`（PDF / Office / zip）不需要；可选的 PDF 按需抽文本走 `actweave.read_attachment`（[§9.4](#94-入站按需读取可选附加)）。

### 步骤 4 — 跟随 Run 事件（SSE）

```http
GET {base}/workspaces/{wid}/agents/{aid}/runs/{rid}/events
Authorization: Bearer <access_token>
Accept: text/event-stream
Last-Event-ID: 0
```

断线后用上次已成功应用的 sequence 重连：

```http
Last-Event-ID: 17
```

### 步骤 5 — 处理 Interaction（进入 waiting 时）

```http
POST {base}/workspaces/{wid}/agents/{aid}/runs/{rid}/interactions/{iid}:decide
Authorization: Bearer <access_token>
Idempotency-Key: <canonical-uuid>
If-Match: "<interaction-version>"
Content-Type: application/json

{"decision":"approve"}
```

允许的 decision：`approve` | `decline` | `cancel`（受 Interaction 与策略约束）。

---

## 9. HTTP API 索引

Base：`/api/agent-access/v1`  
鉴权：除 Token 端点与 JWKS 外，均需 `Authorization: Bearer <access_token>`。

### 通用请求头

| Header | 场景 | 用途 |
| --- | --- | --- |
| `Authorization: Bearer <access_token>` | 数据面 | 仅短期 AAP Token |
| `Idempotency-Key: <uuid>` | POST Conversation / Run / Cancel / Decide | 规范 UUID；可安全重试 |
| `If-Match: "<version>"` | Decide / Cancel（按接口要求） | 强版本 / ETag |
| `If-None-Match` | GET Conversation / Run | 条件读 |
| `Last-Event-ID: <sequence>` | GET Run events | 续传游标（整数 ≥ 0） |
| `Accept: text/event-stream` | GET Run events | SSE |
| `ActWeave-Protocol-Version` | 可选 | 协议快照日期 |

**幂等：** 每个不同命令使用**新** UUID。同 key + 同 body → 原结果；同 key + 不同 body → **409** `IDEMPOTENCY_CONFLICT`。

### Discovery / Token

| 方法 | 路径 | 鉴权 | 说明 |
| --- | --- | --- | --- |
| `GET` | `/.well-known/jwks.json` | 无 | 公钥 |
| `POST` | `/oauth/token` | Client 认证 | `client_credentials` 或 Token Exchange |

### Profile

| 方法 | 路径 | Scope |
| --- | --- | --- |
| `GET` | `/workspaces/{wid}/agents/{aid}/profile` | `agent:read` |

### Conversations

| 方法 | 路径 | Scope | 说明 |
| --- | --- | --- | --- |
| `POST` | `/workspaces/{wid}/agents/{aid}/conversations` | `conversation:create` | 需 `Idempotency-Key` |
| `GET` | `/workspaces/{wid}/agents/{aid}/conversations/{cid}` | `conversation:read` | ETag / `If-None-Match` |

### Runs

| 方法 | 路径 | Scope | 说明 |
| --- | --- | --- | --- |
| `POST` | `/workspaces/{wid}/agents/{aid}/runs` | `run:create` | 仅 text；可能 202 |
| `GET` | `/workspaces/{wid}/agents/{aid}/runs/{rid}` | `run:read` | 状态 / item 快照 |
| `POST` | `/workspaces/{wid}/agents/{aid}/runs/{rid}:cancel` | `run:cancel` | 幂等取消 + `If-Match` |
| `GET` | `/workspaces/{wid}/agents/{aid}/runs/{rid}/events` | `event:read` | SSE |

### Interactions

| 方法 | 路径 | Scope | 说明 |
| --- | --- | --- | --- |
| `POST` | `.../runs/{rid}/interactions/{iid}:decide` | `interaction:decide` | Body + `If-Match` + `Idempotency-Key` |

### Files（可选；默认关闭）

| 方法 | 路径 | Scope | 说明 |
| --- | --- | --- | --- |
| `POST` | `/workspaces/{wid}/agents/{aid}/files` | `file:write` | 创建上传意图 + 仅响应中的 `upload`（预签名 PUT）。需 `Idempotency-Key` |
| `POST` | `/workspaces/{wid}/agents/{aid}/files/{fid}:complete` | `file:write` | 确认 staging；异步 promote。需 `Idempotency-Key` |
| `GET` | `/workspaces/{wid}/agents/{aid}/files/{fid}` | `file:read` | 状态事实源（v1 无 File SSE） |
| `GET` | `/workspaces/{wid}/agents/{aid}/files/{fid}/content` | `file:read` | Bearer 流（路径 A）；大文件优先 `:download` |
| `POST` | `/workspaces/{wid}/agents/{aid}/files/{fid}:download` | `file:read` | 签发不透明下载 token（路径 B） |
| `GET` | `/files/downloads/{tokenId}` | 无（token） | 经不透明 token 拉流；**不要**带 AAP Bearer |

**v1 不提供** `GET .../files`（列表）、`DELETE .../files/{id}` 或公开 ingest 端点。无 Console 文件管理 UI；Console 聊天预览走 session+message 代理（见 [§9.3](#93-出站附件可选附加)）。

请求/响应 schema 以 [`openapi/agent-access-v1.yaml`](./openapi/agent-access-v1.yaml) 为准。

### 9.1 文件（可选）

功能开关：`agentAccess.files.enabled` 默认 **false**。关闭时文件路由以不可见隐蔽（**404**）。**图片** `input_file` 还要求 **`RuntimeMultimodal=true`**：仅图片或图+文档的 `createRun` 在该旗关闭时以 **422 `FILE_RUNTIME_UNAVAILABLE`** 失败关闭（不创建 Run）。**纯文档** `input_file`（PDF / Word / Excel / zip）**不**要求 `RuntimeMultimodal`；模型在组装期看到清单（文件名 / mediaType / 大小 / `fileId`），不是文件字节，持久化信封仍只有 `input_file` + `fileId`。可选 PDF 文本抽取见 [§9.4](#94-入站按需读取可选附加)。

助手出站附件是**另一道**运行时旗（`runtimeOutboundAttachments`）加上 Agent 策略 — 见 [§9.3](#93-出站附件可选附加)。GET File 响应里这些行的 `purpose` 可为 `AGENT_OUTPUT`；`createFile` **不接受**该 purpose。

#### 上传流程

1. **`POST .../files`**，携带 `mediaType`、`sizeBytes`（及可选 `filename`、`sha256`、`purpose`）+ `Idempotency-Key`。
2. 响应 `201` 含 `file` + **仅此处出现**的 `upload: { method: "PUT", url, headers, expiresAt }`。  
   - PUT **必须**按返回的 `headers` 原样发送。签名至少绑定 **`Content-Length`** 与 **`Content-Type`**。  
   - 后续 **GET 永不回显** `upload`、预签名头或 live 下载 URL。
3. **客户端 PUT** 原始字节到 `upload.url`（通常为对象存储）。**不要**在该 PUT 上附加 AAP Bearer。
4. **`POST .../files/{fileId}:complete`**（可选 body `{ "sha256": "..." }`）。快路径：stat staging、校验 size/MIME、CAS → `uploaded`、入队 promote。**不等待**加密完成。
5. **轮询 `GET .../files/{fileId}`** 直至 `status=ready`（SDK：`waitUntilReady`）。终态失败：`failed` / `expired`。
6. **`POST .../runs`**，content 部分示例：

```json
{
  "conversationId": "<uuid>",
  "stream": false,
  "input": [{
    "type": "message",
    "role": "user",
    "content": [
      { "type": "text", "text": "请概括这份发票" },
      { "type": "input_file", "fileId": "<file-uuid>" }
    ]
  }]
}
```

协议线与持久化 item **仅携带稳定 `fileId`** — 禁止 live 下载/预签名 URL 或 blob 明文。

#### 默认限制（以运营方配置为准）

| 项 | 默认 |
| --- | --- |
| maxBytes | 25 MiB |
| 允许 MIME | `image/png`、`image/jpeg`、`image/webp`、`image/gif`、`application/pdf`、Word（`.doc` / `.docx`）、Excel（`.xls` / `.xlsx`）、`application/zip` |
| Staging TTL | 约 15 分钟 |
| 保留 | EXPIRING 约 30 天（首次成功 createRun 引用可提升保留） |
| 下载 token TTL | client_content / tool_invoke ≤ 5m；processor_delivery ≤ 10m（硬顶 15m） |

#### 内容下载

| 路径 | 方式 | 适用 |
| --- | --- | --- |
| **A** | `GET .../files/{fileId}/content` + Bearer + `file:read` | 小文件；简单客户端 |
| **B** | `POST .../files/{fileId}:download` → 相对 `url` → `GET /files/downloads/{tokenId}`（**无** Bearer） | **`sizeBytes > 4 MiB` 时优先**；工具/处理器一律用不透明 token |

Token id 为不透明 DB 行，**不是** JWT，也**不是** MinIO 凭证。对流式文件响应的反向代理应关闭响应缓冲，并将读超时设为 ≥ 120s（至多 maxBytes）。内容流会设置 `X-Content-Type-Options: nosniff`；非 `image/*` 响应还会设置 `Content-Disposition: attachment`。

#### 处理器 Webhook 契约（合作方 / DLP / 自定义 stage）

工作区配置的 HTTPS 处理器在 promote 后收到异步 POST（HMAC，非 AAP Bearer）：

```http
POST https://partner.example/hooks/aap-file
Content-Type: application/json
X-ActWeave-Signature: t=<unix>,v1=<hmac_sha256_hex>
```

投递 body（示意）：

```json
{
  "specVersion": "file-processor.v1",
  "eventType": "file.uploaded",
  "deliveryId": "<uuid>",
  "workspaceId": "<uuid>",
  "agentId": "<uuid>",
  "fileId": "<uuid>",
  "mediaType": "application/pdf",
  "sizeBytes": 12345,
  "sha256": "...",
  "download": {
    "url": "https://aap-host/api/agent-access/v1/files/downloads/<tokenId>",
    "expiresAt": "...",
    "purpose": "processor_delivery"
  },
  "callback": {
    "url": "https://aap-host/api/agent-access/v1/internal/file-processor/callbacks/<deliveryId>",
    "expiresAt": "..."
  }
}
```

- **签名：** 对 `t + "." + rawBody` 做 HMAC-SHA256（密钥为工作区 processor secret）；头格式 `t=<unix>,v1=<hex>`。校验时间窗约 ±5 分钟。
- 投递中的 **download URL** 为短时不透明 token 代理 — **无需** AAP Bearer 即可拉取。
- **回调**（合作方 → ActWeave）同样使用 HMAC：

```http
POST /api/agent-access/v1/internal/file-processor/callbacks/{deliveryId}
Content-Type: application/json
X-ActWeave-Signature: t=<unix>,v1=<hmac_sha256_hex>
```

```json
{
  "processorId": "partner-dlp",
  "status": "succeeded",
  "artifacts": [
    {
      "kind": "DLP_REPORT",
      "mediaType": "application/json",
      "contentBase64": "..."
    }
  ],
  "attributes": { "dlpRisk": "low" }
}
```

| 规则 | 说明 |
| --- | --- |
| 回调体上限 | 请求约 384 KiB；**解码后**制品合计 ≤ **256 KiB** |
| Job 生命周期 | `PENDING` → `DELIVERED` → `SUCCEEDED` \| `FAILED` \| `TIMED_OUT` |
| 晚到回调 | 已 `TIMED_OUT` 后 → **409 `FILE_PROCESSOR_CALLBACK_LATE`**（不破坏已达终态） |
| 幂等重放 | 同一 delivery 成功回调 → 200 |
| Webhook URL 策略 | **仅 https**；拒绝 private / link-local / 元数据 IP（防 SSRF） |
| 配置面 | 工作区表 / 运营注入 — v1 **无**公开 list API、**无** Console UI |

#### 合作方工具头（`x-actweave-file`）

当工具 schema 将属性标记为 `x-actweave-file: true` 时，ActWeave 在 **invoke** 时签发短时下载 token，并**仅注入出站 wire**（持久化 / 协议投影前会 scrub）：

| 场景 | Header |
| --- | --- |
| 恰好 1 个文件 | `X-ActWeave-File-Download: <absolute-proxy-url>` |
| 多个文件 | `X-ActWeave-File-Downloads: application/json`，值为 `{"<fileId>":"<url>",...}` |

合作方可：

- 在工具输入 schema 的文件对象上声明可选 `downloadUrl` 并从 JSON body 读取，**或**
- 读取 `X-ActWeave-File-Download(s)` 头

**不**要求合作方自持 AAP token 再调 `:download`。模型可见 / 已存储的工具参数**仅**含 `fileId`（及元数据）— 永不含 live URL。

### 9.2 A2UI（可选、附加）

A2UI 是**可选、附加（additive）**能力：**文本始终是一等公民**。开启仅表示 Agent **可以**在有用时附带声明式 UI，**不**要求每条回复都含 A2UI。简单问答可只有文本；同一 Conversation 内可混有纯 text 轮与 text+a2ui 轮。

#### 启用（ActWeave 管理员 / Agents Studio）

| 层 | 字段 | 默认 |
| --- | --- | --- |
| Agent 策略 | `context_policy.aap.enableA2UI: boolean` | **`false`**（缺省 / null → false） |
| 策略 schema | 存在任一 `aap.*` 时要求 `session-context-policy.v2` | 与 `includeCompactionSummary` 同模式 |
| Run 冻结 | createRun 时写入 `context_policy_snapshot.aap.enableA2UI` | 进行中 Run **不**随 Agent 中途改配置而变 |

Workspace 作用域的 context policy **拒绝**任何 `aap` 字段；仅 Agent 级策略生效。

#### Profile 广告（`GET .../profile`）

当 `enableA2UI` 为 true 时，Agent Profile **广告**助手出站能力：

1. `supportedContent` 的 message parts 含 `"a2ui"`（稳定顺序：`text` → 可选 `input_file` → 可选 `a2ui` → 可选 `output_file`）。
2. 顶层 **`a2ui`** 对象（**仅**启用时出现；禁用时 **omit**，不发 `enabled: false`）：

```json
"a2ui": {
  "enabled": true,
  "delivery": "item_completed",
  "streaming": false,
  "actions": false,
  "maxSurfaceBytes": 65536,
  "specHint": "a2ui-surface.v1",
  "catalogIds": ["https://catalog.actweave.dev/standard/v1/catalog.json"]
}
```

| 字段 | MVP 含义 |
| --- | --- |
| `delivery: "item_completed"` | 完整 A2UI part **仅**在 **`item.completed`** 上交付 |
| `streaming: false` | MVP 无 A2UI delta / 渐进 surface |
| `actions: false` | **无**动作通道；控件为 **仅展示** |
| `maxSurfaceBytes` | 原始 `surface` JSON 大小上限（64 KiB） |
| `specHint` | 信封 / surface 版本提示 |
| `catalogIds` | surface 可声明的组件 catalog。每个 surface 都会带其中之一作为 `catalogId`；都渲染不了的客户端应忽略 `a2ui` part，只用文本 |

ETag / profile version 纳入该对象：开关翻转或广告元数据变化会使 ETag 变化。

#### 线上形态（助手出站）

在 `item.completed` 上，成功提取 A2UI 时 content 为多 part：

```json
{
  "type": "message",
  "role": "assistant",
  "status": "completed",
  "content": [
    { "type": "text", "text": "请确认预约信息：" },
    {
      "type": "a2ui",
      "version": "a2ui-surface.v1",
      "catalogId": "https://catalog.actweave.dev/standard/v1/catalog.json",
      "surface": { }
    }
  ]
}
```

| 规则 | 说明 |
| --- | --- |
| **Text 一等** | schema 上始终有 `text` part；仅在存在合法 `a2ui` 时允许 `text:""` |
| **可选 a2ui** | MVP 0 或 1 个 `a2ui` part。忽略未知 part 的客户端仍可用纯文本 |
| **入站** | `createRun` **拒绝**用户/入站 `a2ui`（`UNSUPPORTED_CONTENT_TYPE` / 4xx）。A2UI 仅助手出站 |
| **降级** | 非法 / 过大 / 投影失败 → 纯 text 成功；A2UI 损坏**不会**单独导致 Run 失败 |

#### 客户端规则（completed 权威 vs 流式预览）

1. **按今日方式流式文本。** 仅 `text_delta` 流式（index 0）。拼接的 delta 只是**实时预览**，不是终稿。
2. **delta 只承载正文。** 围栏（`<<<A2UI>>>` … `<<<END_A2UI>>>`）是平台向模型索取 surface 的编码方式，在 delta 离开服务端之前已被剥离——包括被切分在两个 chunk 之间的标记。客户端不会看到任何围栏片段，因此既不需要剥离也不需要解析。surface 本身不产生任何文本流，所以正文结束到 `item.completed` 之间会有一段停顿：保持你已有的"进行中"提示直到该 item 完成。
3. **`item.completed` 为权威。** 它会**整项替换** item 快照。使用其多 part `content`：清洗后的 text [+ 可选 `a2ui`]。优先 completed，而非任何在途 delta 缓冲。
4. **MVP 仅展示 / 客户端对 action 做 no-op。** Profile 广告 `a2ui.actions: false`。若有本地 catalog 可渲染 surface；**不要**把按钮点击、表单提交等控件动作回传到 ActWeave。**禁止**复用 `interaction.decide` 承载 A2UI 控件（审批类 Interaction 保持独立）。
5. **不实现 A2UI 时可忽略未知 part**；只要出现 `id:` 仍须推进 SSE sequence。

TypeScript SDK 辅助（`@actweave/agent-client`）：

```ts
import { findA2UIPart, isKnownA2UICatalog, iterCharts, joinTextParts, type ProtocolItem } from "@actweave/agent-client";

function readAssistant(item: ProtocolItem) {
  const text = joinTextParts(item); // 仅 text；忽略 a2ui / 未知 part
  const surface = findA2UIPart(item)?.surface; // 纯 text 时为 undefined
  // catalog 不认识：只显示文本，什么都不画
  if (!isKnownA2UICatalog(surface)) return { text, charts: [] };
  return { text, charts: iterCharts(surface) }; // series 已解析，value 仍是数字
}
```

`RunReducer` 已在 `item.completed` 时替换整 item，因此渐进 text 会被权威多 part content 覆盖。

#### Surface 契约（catalog `standard/v1`）

`surface` **不是**自由格式：服务端在落库前用组件 catalog 校验每个 surface，
不合规就整体拒绝（此时消息只带文本到达）。因此你收到的 surface 一定已在下述边界内——
契约存在的意义就是让渲染器可以依赖这一点，而不必去猜。

**形态。** surface 就是 A2UI `createSurface` 载荷，符合规范的渲染器可以零适配直接消费：

```json
{
  "surfaceId": "019ff3f0-bfdd-7b38-9c53-f90bf5812478:item_1",
  "catalogId": "https://catalog.actweave.dev/standard/v1/catalog.json",
  "components": [
    { "id": "root", "component": "Column", "children": ["t1", "c1"] },
    { "id": "t1", "component": "Text", "text": "2026 Q1 各区域营收", "variant": "heading" },
    { "id": "c1", "component": "Chart", "chartType": "bar", "unit": "万元", "series": { "path": "/revenue" } }
  ],
  "dataModel": {
    "revenue": [{ "name": "营收", "points": [{ "label": "华东", "value": 1280 }] }]
  }
}
```

组件图是**扁平**的：components 是一个列表，子节点按 id 引用，且恰有一个组件
`id: "root"`。从 `root` 开始遍历；没有被引用的组件不可达，也不会到你手上。

**组件**（`standard/v1`，共 11 个）：`Column` `Row` `Card` `Text` `Divider`
`Chart` `TextField` `CheckBox` `ChoicePicker` `DateTimeInput` `Button`。
名字**精确匹配**——没有别名，不做大小写折叠。

**图表**只承载度量值。没有颜色、尺寸、坐标轴范围、图例或格式化后的字符串可继承，
这些属于你的设计系统。

| 成员 | 契约 |
| --- | --- |
| `chartType` | `bar` `hbar` `line` `area` `pie` `donut`。`hbar` 的存在就是为了长类目标签不必旋转 |
| `series` | 1–8 条 series，每条 1–64 个 `{label, value}` 点。可内联，也可绑定 |
| `unit` | 所有值的单位（如 `万元`、`%`），绝不会揉进数字里 |
| `valueFormat` | `plain` `compact` `percent` `currency`——值应当如何读 |
| `stacked` | 仅柱状可用；用在其他形状上会被服务端拒绝 |
| `title` | 可选。图表自己的标题，与兄弟 `Text` 标题无关 |

服务端已经强制的跨字段规则：`pie` / `donut` 恰好一条 series 且无负值；
多 series 图表共享同一套标签序列。

**数据绑定。** 任何允许写值的位置，成员都可以换成指向 `dataModel` 的
JSON Pointer（RFC 6901）：`{ "path": "/revenue" }`。所有成员都走同一个解析函数——
不要靠观察值的形状去判断"这是字面量还是指针"。指针指不到东西**不是错误**，
按"成员缺失"处理即可。

**限制**（服务端已强制；你镜像同一套，外来 surface 就无法在你的客户端引发无界工作）：

| 限制 | 值 |
| --- | --- |
| `surface` JSON 字节 | 65536 |
| 每个 surface 组件数 | 64 |
| 树深度 | 16 |
| 每图 series 数 / 每 series 点数 | 8 / 64 |

**客户端义务。**

1. **先校 `catalogId`**，与 profile 的 `a2ui.catalogIds` 比对再渲染。不认识的 catalog 意味着不认识的组件：回落到文本。
2. **按组件降级，不要按消息降级。** 遇到没实现的组件，画一个占位并继续渲染它的兄弟节点。悬空子 id、超出你允许深度的树，同样处理。
3. **绝不执行 surface 里的任何东西。** 文本就是文本，不是标记：用插值渲染。`actions: false` 意味着控件只展示。
4. **不要从 id 推断语义。** `id` 是图的键，不是语义提示。

**Schema 分发。** 拉取 catalog 与 surface schema（公开、可缓存、带 `ETag`、免 token）：

```
GET {base}/api/v1/a2ui/catalogs/standard/v1/catalog.json
GET {base}/api/v1/a2ui/catalogs/standard/v1/surface.schema.json
```

`catalogId` 是**标识符**：按 A2UI 规范它不必可解析，所以请通过上面的端点取 schema，
而不是去解引用这个 id。surface schema 通过相对 `$ref` 引用 catalog，
如果你要镜像这两份文档，请保持它们同目录。

#### 非目标（MVP）

- A2UI 流式 / 渐进 surface（后续）
- 组件 action 通道 / `a2ui_action` user part（后续）
- catalog 协商：客户端还不能声明自己能渲染哪些 catalog
- `standard/v1` 之外的 catalog，以及它未纳入的 Basic Catalog 组件

### 9.3 出站附件（可选、附加）

出站附件是**可选、附加（additive）**能力：**文本始终是一等公民**。开启后，Agent **可以**在助手消息上附带 0..N 个生成文件，形态为 `type: "output_file"` content part。它们按附件卡片渲染（与用户入站附件同构），不是 Markdown 链接，也不是 `artifact` Run Item。

v1 面向模型的生产通道是**纯文本**：平台工具 `actweave.publish_attachment` 只接受 UTF-8 `text`（plain / CSV / Markdown / JSON），硬顶 **256 KiB**。**没有**模型 `base64` 字段，**没有**公开 ingest HTTP。字节落入现有 File 平面（`purpose=AGENT_OUTPUT`）；客户端用与入站相同的路径 A / 路径 B 下载。

#### 启用（三道门 + toolCalling）

下列条件必须**全部为真**，否则运行时**不注入**该工具（Run 仍以纯文本成功）：

| 层 | 字段 | 默认 |
| --- | --- | --- |
| Files HTTP 门 | `agentAccess.files.enabled` **且** workspace/client 白名单（`allowAllWorkspaces` / `workspaceIds`，`allowAllClients` / `clientIds`） | **关** |
| 运行时 | `agentAccess.files.runtimeOutboundAttachments`（环境变量 `ACTWEAVE_AAP_FILES_RUNTIME_OUTBOUND_ATTACHMENTS`） | **`false`** |
| Agent 策略 | `context_policy.aap.enableOutboundAttachments`（policy v2；缺省 / null → false） | **`false`** |
| 冻结 `toolCalling` | `function_calling` 或 `native_client_search`（v1 空串按现有 native-client-search 规则） | `none` **不注入** |

Workspace 作用域的 context policy **拒绝**任何 `aap` 字段。进行中 Run **不**随 Agent 中途改配置而变（`context_policy_snapshot.aap.enableOutboundAttachments`）。

| files.enabled | 白名单 ws+client | runtimeOutbound | policy | toolCalling 支持工具 | 行为 |
| --- | --- | --- | --- | --- | --- |
| 0 | * | * | * | * | File HTTP 404；不注入；已有 `fileId` 也拉不下 |
| 1 | 0 | * | * | * | HTTP 404；不注入；ingest 拒绝（`FILE_FEATURE_DISABLED`） |
| 1 | 1 | 0 | * | * | 入站文件可用；不注入 |
| 1 | 1 | 1 | 0 | * | 不注入 |
| 1 | 1 | 1 | 1 | 0（`none`） | **不注入、Run 成功**；纯文本 |
| 1 | 1 | 1 | 1 | 1 | 完整出站 |

**`toolCalling: none` + 非空 catalog 会打挂整轮**（`ErrAgentModelToolsUnsupported`）。纯聊天 Agent 即使打开 policy 也只是「工具不出现」，回复仍是纯文本。

**没有公开 ingest HTTP。** `createFile` **不接受** `purpose=AGENT_OUTPUT`。

#### Profile 广告（`GET .../profile`）

当 files HTTP 门对该 workspace 打开 **且** `runtimeOutboundAttachments` **且** Agent policy 开启时：

1. `supportedContent` 的 message parts 含 `"output_file"`（稳定顺序：`text` → 可选 `input_file` → 可选 `a2ui` → 可选 `output_file`）。
2. 追加 `output_file_constraints` 对象：

```json
{
  "type": "output_file_constraints",
  "mediaTypes": ["text/plain", "text/csv", "text/markdown", "application/json"],
  "maxBytes": 262144
}
```

`createRun` 仍**拒绝**用户/入站 `output_file`（`UNSUPPORTED_CONTENT_TYPE`）。旧 SDK 忽略未知 part / 未知 constraints 对象。

#### 线上形态（助手出站）

`output_file` **仅**出现在 **`item.completed`**（与 A2UI 相同）。`item.delta` 永不带文件。

```json
{
  "type": "message",
  "role": "assistant",
  "status": "completed",
  "content": [
    { "type": "text", "text": "对账单已生成。" },
    {
      "type": "output_file",
      "fileId": "019f0000-0000-7000-8000-00000000f001",
      "mediaType": "text/csv",
      "filename": "invoice-2026-08.csv",
      "sizeBytes": 4096
    }
  ]
}
```

| 规则 | 说明 |
| --- | --- |
| **Text 一等** | schema 上始终有 `text` part。仅附件轮仍会落非空信封（空文本 + `output_file`） |
| **0..N 个文件** | 服务端每轮最多 **8** 个（`MaxOutboundFilesPerTurn`）。每个 part 经 allowlist 重建：只保留 `type`、`fileId`、`mediaType`、`filename`、`sizeBytes` — **永不**含 URL / 字节 |
| **仅本 Run** | 只挂本 Run 已成功 ingest 的 `fileId`（`aap_files.source_run_id`，`purpose=AGENT_OUTPUT`）。模型不能发明 id |
| **入站** | `createRun` 拒绝 `output_file` |
| **降级** | 终态预检失败则丢掉文件、保留 text/A2UI；Run 仍成功 |
| **A2A** | `completeRunA2A` **不**挂出站附件 |

#### 工具契约（`actweave.publish_attachment`）

模型在 v1 的唯一发布通道：

```json
{
  "filename": "invoice-2026-08.csv",
  "mediaType": "text/csv",
  "text": "月份,预约,成交\n..."
}
```

| 规则 | 说明 |
| --- | --- |
| MIME | 仅 `text/plain` \| `text/csv` \| `text/markdown` \| `application/json` |
| 大小 | UTF-8 `text` ≤ **256 KiB**（`MaxPublishTextBytes`）。JSON `maxLength` 按码点；服务端再查字节长度 |
| 无 `base64` | `additionalProperties: false`；出现 `base64` 字段即拒绝 |
| 结果 | `{ ok, fileId, filename, mediaType, sizeBytes, sha256 }` — **永不**返回 URL / `downloadUrl` / content |
| 配额 | READY 工作区配额超限 → `FILE_SIZE_EXCEEDED`。本轮超过 8 个 → `FILE_OUTBOUND_TURN_LIMIT`。门未开 → `FILE_FEATURE_DISABLED` |
| 病毒 / DLP | 出站 ingest **没有病毒扫描，也没有工作区 webhook DLP**。不要发明比入站更严的 `VirusScanner`（入站 virus 本身是永 clean 的 stub） |

成功 ingest 写入 READY 的 `aap_files` 行，**`purpose=AGENT_OUTPUT`**，`source_run_id` 为本 Run。下载走现有 File HTTP（`file:read`）。

#### 客户端规则

1. **按今日方式流式文本。** 文件不出现在 `item.delta` 上。
2. **`item.completed` 为权威。** 从 completed item 读取 `output_file`（SDK：`findOutputFileParts` / `isOutputFileContentPart`）。`joinTextParts` 忽略文件 part。
3. **按 `fileId` hydrate。** 调用 `getFile` / `getFileContent`。**不要**把 `links.content` 当 `<img src>`。按 `fileId` 调和，避免后续快照（`run.completed`）重置已经 ready 的卡片。
4. **下载**走路径 A 或 B（与入站相同）。非图片响应带 `Content-Disposition: attachment` 与 `X-Content-Type-Options: nosniff`。
5. 不实现出站附件时可忽略未知 part；只要出现 `id:` 仍须推进 SSE 游标。

#### Demos（`demos/aap-chat`）

- **Mock：** story `export-csv`（**生成本月对账单**，CSV）、`site-photos`（**看看这几张现场图**，2×2 PNG 图库）、`inspection-pack`（**出一份巡检复盘包**，Markdown + 现场图 + 文件卡片 + A2UI）在 `.msg-row.is-assistant .msg-attachments` 画出助手卡片，无需后端（`npm run dev:mock`）。
- **Live：** `item.completed` → 占位卡片 → 按 `fileId` `getFile` / `getFileContent` hydrate → `patchMessages`。需要 `file:read`。

#### Console（运营面；不是 AAP）

Console 聊天在消息 DTO 上投影 `attachments` 并渲染卡片。预览/下载走：

```
GET /api/v1/workspaces/{wid}/sessions/{sid}/messages/{mid}/files/{fileId}/content
```

授权：Console `ActionView` + session 属本 workspace + 调用者可读该 session + **`fileId` 出现在该消息持久化的 `output_file`（或用户 `input_file`）parts 上**。字节来自 SecureStore（`CreatorSystem`）。Console 用户**不得**以 AAP 服务主体身份打 File 路由。这**不是**文件管理 UI，也**不是**第三方 API。

#### 回滚（运营）

1. **先关 `runtimeOutboundAttachments`**（停写 / 停注入）。
2. 停止新写 Agent 策略 `enableOutboundAttachments: true`。
3. **不要**回滚能解析 `enableOutboundAttachments` 的快照解析器（旧二进制 `DisallowUnknownFields` 会打挂已经写出该键的在途 Run）。
4. **不要**在仍有 `AGENT_OUTPUT` 行时执行 `000023` down（CHECK 回滚会拒绝）。
5. 最后才关 `files.enabled`（否则历史下载也会 404）。

`runtimeMultimodal` 正交。

#### 非目标（v1）

- 无模型 `base64`、无 25 MiB 工具参数、无 `sourceFileId` 转发
- 无出站病毒扫描 / webhook DLP
- 无公开 ingest HTTP；`createFile` 不能设 `AGENT_OUTPUT`
- 图像 / PDF 生成不是 v1 工具 enum（ingest API 可接受它们，留给未来同进程调用方）
- A2A inbound complete 不挂出站附件

### 9.4 入站按需读取（可选附加）

入站用户文件仍是 **`type: "input_file"` + 稳定 `fileId`**。**没有**新的 content part，**不**升协议版本。运行时可能在**模型** prompt 组装期追加一小段 `<actweave_attachments>` 清单；该清单**不**写入用户消息，**不**出现在客户端气泡里。

开启后，Agent 可调用平台工具 **`actweave.read_attachment`**（`fileId`，可选 PDF `pages`）抽取 **PDF 文本**（默认 1–10 页，每 call 最多 20 页，UTF-8 `text` ≤ 256 KiB）。Office / zip 会出现在清单上；读取返回 `FILE_MEDIA_TYPE_DENIED`（不解压）。可读 id 仅为本 Conversation 用户消息上的 `input_file`（跨轮；不含 `AGENT_OUTPUT`）。工具结果 JSON 无 URL / `downloadUrl` / 原始文件 base64；`text` 出现在 AAP `tool_call` 完成事件上。`toolCalling: none` **不注入**工具；Run 仍成功，只有清单。

#### 开启（三道门 + toolCalling）

| 层 | 字段 | 默认 |
| --- | --- | --- |
| Files HTTP 门 | `agentAccess.files.enabled` **且** workspace/client 白名单 | **关** |
| 运行时 | `agentAccess.files.runtimeInboundRead`（env `ACTWEAVE_AAP_FILES_RUNTIME_INBOUND_READ`） | **`false`** |
| Agent 策略 | `context_policy.aap.enableInboundRead`（policy v2；省略 / null → false；快照键在为 true 前 omitempty） | **`false`** |
| 冻结 `toolCalling` | `function_calling` 或 `native_client_search` | `none` **不注入** |

回滚：**先关 `runtimeInboundRead`**，再停止新写 `enableInboundRead: true`。已经写出 `"enableInboundRead":true` 的 Run **不要**回滚快照解析器。白名单文档「清单代替 fail-closed」在工具关闭后仍然有效（PDF/Office/zip 不再因 `MODEL_CONTENT_UNSUPPORTED` 打挂 Run）。

---

## 10. SSE 事件流

### 10.1 帧类型

#### Protocol Event（持久化 — 推进游标）

```text
id: 12
event: item.delta
data: {"specVersion":"1.0","type":"item.delta","eventId":"...","streamId":"run:...","sequence":12,...}
```

- SSE `id:` = Run 作用域内持久 **sequence**  
- 只持久化**已成功应用**的最后一个 sequence  

#### 心跳（不持久化 — 不移动游标）

```text
: ping 2026-07-21T12:00:00Z
```

约每 **15 秒**一次。仅注释行，**无** `id:`。

#### 传输信号（不持久化 — 不移动游标）

```text
event: stream.error
data: {"specVersion":"1.0","type":"stream.error","error":{"code":"TOKEN_EXPIRED","retryable":true,...}}
```

无 `id:` 行。

### 10.2 追赶 → 实时跟随

1. 服务端读取 `Last-Event-ID` 之后的高水位  
2. 分页回放 `sequence > cursor` 的历史事件  
3. 进入 live follow 并发送心跳  

### 10.3 客户端规则

1. 忽略未知事件类型 / 字段；出现 `id:` 时仍要推进 sequence  
2. `TOKEN_EXPIRED`（`retryable=true`）：新 Token + **相同**游标重连  
3. 非法游标 → HTTP **422** `REPLAY_CURSOR_INVALID`  
4. Run 终态通常包括：`completed`、`failed`、`cancelled`  

### 10.4 协议事件族（v1）

以 Schema Registry / OpenAPI 目录为准。常见族：

- `run.accepted` / `run.started` / `run.waiting` / `run.resumed` / `run.completed` / `run.failed` / `run.cancelled`  
- `item.started` / `item.delta` / `item.completed`  
- `interaction.requested` / `interaction.resolved`  
- `usage.updated`（若存在）  

### 10.5 反向代理要求（SSE）

对 `text/event-stream`：

| 要求 | 说明 |
| --- | --- |
| 关闭响应缓冲 | 如 Nginx `X-Accel-Buffering: no` |
| 保留头 | `Cache-Control: no-cache, no-transform` |
| 不对 SSE 做 gzip | 关闭动态压缩 |
| 读/空闲超时 | **≥ 60s**（建议 75s） |

---

## 11. Interaction（审批）

当 Run 需要确认时：

1. 流中出现 `interaction.requested` 与 `run.waiting`  
2. 客户端展示 Interaction UI（标题、风险、允许决策、version）  
3. 带 `If-Match` 与 `Idempotency-Key` 调用 `:decide`  
4. 流继续（`interaction.resolved`、`run.resumed` 等）  

| decision | 含义 |
| --- | --- |
| `approve` | 继续 |
| `decline` | 拒绝该步骤 |
| `cancel` | 按策略取消 |

**风险 / 主体策略（摘要）：**

- 纯服务主体通常仅能在 Grant 允许时决策 **LOW / MEDIUM**  
- **HIGH / CRITICAL** 通常需要**同一 External Subject**（Token Exchange）或 ActWeave 用户路径  
- Resume token 不会出现在 Protocol Event 或公开 DTO 中  

---

## 12. 错误

### 12.1 数据面错误包

```json
{
  "error": {
    "code": "REPLAY_CURSOR_INVALID",
    "message": "Human-readable summary without secrets.",
    "retryable": false,
    "requestId": "...",
    "traceId": "..."
  }
}
```

### 12.2 稳定错误码（节选）

| Code | HTTP / 通道 | 可重试？ | 客户端动作 |
| --- | --- | --- | --- |
| `TOKEN_EXPIRED` | 401 / SSE | 是 | 刷新 Token；同一 `Last-Event-ID` |
| `UNAUTHENTICATED` | 401 | 否* | 修正凭证（临近过期可先刷新） |
| `AUTHORIZATION_DENIED` | 403 / 404 | 否 | 检查 Grant / scope / 归属（不可见常返回 404） |
| `AUTHORIZATION_REVOKED` | SSE 断连 | 是 | 新 Token + 同一游标 |
| `REPLAY_CURSOR_INVALID` | 422 | 否 | 从已知合法 sequence 或 `0` 重置 |
| `IDEMPOTENCY_CONFLICT` | 409 | 否 | 换新 key 或保证 body 完全一致 |
| `RATE_LIMITED` | 429 | 是 | 遵守 `Retry-After` / `RateLimit-*` |
| `UNSUPPORTED_CONTENT_TYPE` | 400 | 否 | 仅使用支持的 content part |
| `SLOW_CONSUMER` | SSE 断连 | 是 | 用上次 `id` 重连 |
| `FILE_NOT_FOUND` | 404 | 否 | 不存在 / 不可见（隐蔽） |
| `FILE_FEATURE_DISABLED` | 404 | 否 | files 开关关闭（隐蔽） |
| `FILE_NOT_READY` | 422 | 处理中时可重试 | createRun 前轮询 GET |
| `FILE_UPLOAD_EXPIRED` | 422 | 否 | 重新 create；staging 已过期 |
| `FILE_SIZE_EXCEEDED` | 422 | 否 | 减小体积 / 遵守 maxBytes |
| `FILE_MEDIA_TYPE_DENIED` | 422 | 否 | 使用允许的 MIME 白名单 |
| `FILE_MEDIA_TYPE_MISMATCH` | 422 | 否 | 字节与声明 mediaType 不符 |
| `FILE_INTEGRITY_MISMATCH` | 422 | 否 | sha256 不匹配 |
| `FILE_PROCESSING_FAILED` | 422 | 否 | 勿引用失败文件 |
| `FILE_RUNTIME_UNAVAILABLE` | 422 | 否 | 图片或图+文档的 `input_file` 需要 `RuntimeMultimodal`；不创建 Run。纯文档不需要。 |
| `FILE_PROCESSOR_CALLBACK_LATE` | 409 | 否 | Job 已 TIMED_OUT；不改状态 |
| `FILE_PENDING_LIMIT` | 429 | 是 | 退避；并发 PENDING_UPLOAD 上限 |
| `FILE_OUTBOUND_TURN_LIMIT` | 工具结果 | 否 | 本轮出站文件超过 8 个 |
| `MODEL_CONTENT_UNSUPPORTED` | run failed | 否 | 模型供应商无法消费该媒体（未知类型，或图片组装失败）。白名单文档改为清单，不再因此失败。 |

OAuth Token 端点错误遵循 RFC 6749 的 `error` / `error_description`，不得回显密钥。

---

## 13. 限流与配额

多维度限流（Workspace / Client / Agent / Subject / IP，视操作而定）。响应可能包含：

- `Retry-After`  
- `RateLimit-Limit` / `RateLimit-Remaining` / `RateLimit-Reset`（若启用）  

示意默认（每进程实例；生产以运营配置为准）：

- Token 端点：Client × IP × grant  
- SSE 连接：按 Client / Subject / Run（量级约 16 / 8 / 4）  

---

## 14. TypeScript SDK（`@actweave/agent-client`）

仓库路径：`sdk/typescript`。

```ts
import {
  AgentAccessClient,
  MemoryTokenProvider,
} from "@actweave/agent-client";

const tokens = new MemoryTokenProvider({
  // 你的 BFF/mint 返回 { accessToken, expiresAt }
  refresh: async () => mintShortLivedTokenFromYourBackend(),
});

const client = new AgentAccessClient({
  baseUrl: "https://actweave.example.com/api/agent-access/v1",
  tokenProvider: tokens,
});

const { conversation } = await client.createConversation(
  workspaceId,
  agentId,
  { title: "Ticket 42" },
  { idempotencyKey: crypto.randomUUID() },
);

const run = await client.createRun(
  workspaceId,
  agentId,
  {
    conversationId: conversation.id,
    stream: false,
    input: [
      {
        type: "message",
        role: "user",
        content: [{ type: "text", text: "Hello" }],
      },
    ],
  },
  { idempotencyKey: crypto.randomUUID() },
);

for await (const { message, snapshot } of client.followRun(
  workspaceId,
  agentId,
  run.run.id,
)) {
  if (snapshot.run?.status === "waiting_interaction") {
    // 展示 Interaction UI；调用 decideInteraction
  }
  if (
    snapshot.run &&
    ["completed", "failed", "cancelled"].includes(String(snapshot.run.status))
  ) {
    break;
  }
}
```

### 14.1 文件上传（当 `agentAccess.files` 已启用）

```ts
const bytes = new Uint8Array(/* ... */); // 示例中不要写生产密钥
const created = await client.createFile(
  workspaceId,
  agentId,
  {
    filename: "invoice.png",
    mediaType: "image/png",
    sizeBytes: bytes.byteLength,
  },
  { idempotencyKey: crypto.randomUUID() },
);

// PUT 必须使用 create 返回的 headers（Content-Length / Content-Type 已签名绑定）
await client.putFileUpload(created.upload!, bytes);

await client.completeFile(workspaceId, agentId, created.file.id, undefined, {
  idempotencyKey: crypto.randomUUID(),
});

const ready = await client.waitUntilReady(workspaceId, agentId, created.file.id);

// 多模态 E2E 还依赖 ActWeave 侧 RuntimeMultimodal
await client.createRun(
  workspaceId,
  agentId,
  {
    conversationId: conversation.id,
    stream: false,
    input: [
      {
        type: "message",
        role: "user",
        content: [
          { type: "text", text: "描述这张图" },
          { type: "input_file", fileId: ready.id },
        ],
      },
    ],
  },
  { idempotencyKey: crypto.randomUUID() },
);

// 小文件：Bearer .../content。大文件（>4MiB）：优先 :download token 代理
const content = await client.getFileContent(workspaceId, agentId, ready.id);
```

SDK 保证：

- Access Token 只出现在 `Authorization`  
- 断线 / 空洞时用 `Last-Event-ID` 自动重连  
- `TOKEN_EXPIRED` / HTTP 401 时强制刷新后按原游标恢复  
- 文件 PUT **仅**使用 create 返回的 headers（对象存储 PUT 不带 AAP Bearer）  
- `getFileContent` 在 `sizeBytes > 4MiB` 时优先不透明 `:download`  

另有导出：`StaticTokenProvider`、`RunReducer`、`AAPSESession`、文件类型 / `SDK_PREFER_DOWNLOAD_TOKEN_BYTES`，A2UI 辅助 `joinTextParts` / `findA2UIPart`，以及出站辅助 `findOutputFileParts` / `isOutputFileContentPart`。详见 `sdk/typescript/README.md`、[§9.2 A2UI](#92-a2ui可选附加) 与[§9.3 出站附件](#93-出站附件可选附加)。

### 14.2 出站文件（当三道门 + policy + toolCalling 已开）

```ts
import { findOutputFileParts, joinTextParts, type ProtocolItem } from "@actweave/agent-client";

function readAssistantFiles(item: ProtocolItem) {
  const text = joinTextParts(item); // 忽略 output_file / a2ui / 未知 part
  const files = findOutputFileParts(item); // 0..N；纯文本时为空
  // 按 fileId 用 getFile / getFileContent hydrate。不要把 links.content 当 src。
  return { text, files };
}
```

`CreateRun` 输入类型**不含** `output_file`。下载与入站文件共用 `getFile` / `getFileContent`（`file:read`）。

---

## 15. CORS

| 模式 | 行为 |
| --- | --- |
| **BFF（推荐）** | AAP 关闭 CORS；浏览器不直连 AAP 源站 |
| **精确 CORS** | Client `AllowedCORSOrigins` 仅精确 HTTPS Origin（禁止 `*` 与通配） |

未授权 `Origin` 不会被反射。优先把密钥放在服务端并关闭浏览器直连。

---

## 16. 凭证轮换（集成方）

1. 管理员创建**新** Client 凭证（Secret 或更新 JWKS）  
2. 将新凭证部署到你方密钥系统  
3. 切换所有 Token 端点调用方  
4. 全量切换后吊销旧凭证  
5. Security version 递增可能导致 SSE 在 ≤ 60s 内要求重鉴权 — 用新 Token + 同一 `Last-Event-ID` 恢复  

---

## 17. 上线前检查清单

- [ ] BFF 或 mint 服务持有 Client 凭证  
- [ ] 浏览器仅持有短期 Access Token（内存）  
- [ ] 所有变更类命令带 `Idempotency-Key`  
- [ ] SSE 续传只用 `Last-Event-ID`（Token 不进 query）  
- [ ] 代理超时 ≥ 75s；`text/event-stream` 关闭缓冲  
- [ ] 精确 CORS **或** 关闭 CORS  
- [ ] Grant 与 Token 请求均最小权限  
- [ ] 已按目标风险级别测通 Interaction decide  
- [ ] 错误处理映射稳定错误码  
- [ ] 若绑定终端用户，已核对 Token Exchange Issuer 配置  
- [ ] OpenAPI / SDK 版本与部署环境一致  
- [ ] 若使用文件：运营已启用 `agentAccess.files`，且（模型 vision/PDF 场景）`RuntimeMultimodal`；Grant 含 `file:read` / `file:write`  
- [ ] 若使用文件：PUT 始终发送 create 返回的 `Content-Length` / `Content-Type`；长期日志不保存 live 下载 URL  
- [ ] 若实现处理器：校验 `X-ActWeave-Signature`、仅 https 回调 URL 策略，并处理晚到回调 `FILE_PROCESSOR_CALLBACK_LATE`  
- [ ] 若使用 A2UI：Agent 已设 `context_policy.aap.enableA2UI`；客户端以 `item.completed` 为权威；Profile `a2ui.actions: false` → 仅展示 / 提交 no-op  
- [ ] 若使用出站附件：运营已启用 files HTTP（workspace/client 白名单）**且** `runtimeOutboundAttachments`；Agent `enableOutboundAttachments`；冻结 `toolCalling` 为 `function_calling` 或 `native_client_search`（`none` 不注入）；Grant 含 `file:read`；客户端以 `item.completed` 的 `output_file` 为权威并按 `fileId` hydrate  

---

## 18. 仓库内相关产物

| 产物 | 路径 | 作用 |
| --- | --- | --- |
| **本文（中文）** | `docs/aap-integration-guide.zh-CN.md` | 人类可读对接说明 |
| **English guide** | `docs/aap-integration-guide.md` | Same content in English |
| OpenAPI | `docs/openapi/agent-access-v1.yaml` | HTTP 机器契约 |
| TypeScript SDK | `sdk/typescript/` | 客户端库 |
| 产品 README（中） | `README.zh-CN.md` | 产品与本地运行 |
| 产品 README（英） | `README.md` | Product overview & local run |

ActWeave 控制台内部事件路径（`/api/v1/...`）**不在**第三方对接范围内。对外请只使用 AAP。
