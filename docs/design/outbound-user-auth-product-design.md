# 出站用户态鉴权重构：产品设计

| 字段 | 内容 |
| --- | --- |
| Issue | ZKL-51「出站架构设计调整」 |
| 文档版本 | v0.3 |
| 日期 | 2026-07-24 |
| 状态 | Approved / Frozen |
| 负责人确认 | 已批准；原文见评论 `eb38577f-5fc8-4a3f-96b3-5cace02dc7e7`（“批准 v0.3”） |
| 适用范围 | HTTP Tool 出站调用；Provider、ServiceConnection、Tool / Agent / Workflow 执行入口 |

> 本文是已批准并冻结的产品需求，不是技术实施方案。后续如出现新的范围、流程、权限、业务规则、数据保留或验收口径变化，必须回到需求确认流程重新批准。

### 修订记录

| 版本 | 变更 | 依据 |
| --- | --- | --- |
| v0.1 | 首轮产品设计，提出 O1～O8 | Atlas 草案 |
| v0.2 | 吸收 O1=C、O2～O8=A；将共享业务账号的最终移除写入范围；新增 O9～O11 | 负责人评论 `11c23b39-0fa8-4f45-8c45-835d82cd1abe` |
| v0.3 | 吸收 O9=A、O10=A、O11=B；收敛为上线即严格双模式 | 负责人评论 `895e5619-418f-4c82-86db-a15d3a4c8f6c` |
| v0.3 Approved | 全文批准并冻结，未改变 v0.3 产品内容 | 负责人评论 `eb38577f-5fc8-4a3f-96b3-5cace02dc7e7` |

## 1. 背景与问题

ACTWEAVE 当前把第三方服务的端点与鉴权契约放在 Provider，把某个环境下的账号和 Secret 放在 ServiceConnection。HTTP Tool 执行时解析 Connection，读取持久 Secret；对 OAuth2 场景以 `client_credentials` 获取并缓存服务账号 Token，再注入业务请求。

这适用于“一个集成账号代表所有调用者”的共享账号场景，不适用于第三方平台按最终用户隔离数据权限的场景：同一个 Tool 被不同用户调用时，应当使用对应用户的业务身份访问第三方 API。

本需求希望新增两种用户态出站鉴权：

1. **Broker / OBO 模式**：ACTWEAVE 不保存用户业务 Token；使用集成机器信任和当前执行主体，向第三方 Token Broker / OBO 服务换取短期用户业务 Token，再调用业务 API。
2. **请求透传模式**：第三方没有 Broker 时，调用方在本次执行请求中携带当前用户业务 Token；ACTWEAVE 不建立长期用户凭据库。

负责人已选择将现有“对话式执行控制台”改名并收敛为“运行调试台”，保留内部调试、HITL 与 Trace 能力。

## 2. 事实基线

以下内容来自当前 README、页面、API、领域对象与测试，属于已验证事实：

### 2.1 Provider 与 ServiceConnection

- Provider 保存服务端点、HTTP/OpenAPI 驱动配置和无 Secret 的 `service-auth.v1` 鉴权契约。
- 当前 Provider 鉴权方案只正式支持 `NONE` 与 `OAUTH2_CLIENT`。
- ServiceConnection 保存 `authMode`、非敏感 `authConfig`、一个持久 Secret 引用、授权 Scope、策略、验证状态。
- Connection Secret 会落入受版本管理的 Secret 存储；API 只返回 `credentialConfigured` 和指纹，不回传明文。
- Connection 的新增、修改目前使用 Workspace `EDIT` 权限；验证使用 `TEST` 权限。

### 2.2 Tool 出站执行

- Tool / Agent binding / Workflow 最终解析为固定的 Provider、Connection 与 Capability Release。
- HTTP 执行器只接受解析后的 Connection 快照；持久 Secret 在回调期间最小化注入，不序列化到结果。
- 当前支持固定 API Key / Bearer 等遗留头部注入，以及 OAuth2 `client_credentials` / refresh token。
- 当前 OAuth 缓存键以 Connection、Secret 与配置为主，不包含最终用户主体；因此本质上是共享服务账号 Token。
- 出站 HTTP 已有主机、端口、CIDR、重定向与敏感头保护；敏感头跨源重定向时会移除。

### 2.3 执行主体

- AAP 已支持 RFC 8693 Token Exchange，用来验证第三方用户身份并签发 ACTWEAVE 自己的数据面 Token。
- AAP 外部用户执行时，会把 `SERVICE_PRINCIPAL` Actor 与 `EXTERNAL_SUBJECT` Subject 固化到 Run、WorkflowExecution、ToolInvocation 的不可变 Principal Snapshot。
- ACTWEAVE 只持久化外部主体的内部 UUID 和哈希映射，不持久化原始第三方 `sub`。
- 内部控制台用户执行时，Actor 与 Subject 都是当前 ACTWEAVE `USER`。
- 上述能力解决的是“谁可以调用 ACTWEAVE”，尚未解决“ACTWEAVE 用哪个最终用户身份调用第三方业务 API”。

### 2.4 运行入口与调试入口

- AAP `POST .../runs` 当前只接受文本 input、stream、conversationId 和普通 metadata，没有独立的临时出站凭据字段。
- 内部 Tool invoke、Chat message、Workflow trial / production execute 也没有统一的临时凭据 envelope。
- “对话式执行控制台”当前不是空壳：它承载 Workspace / Agent 选择、会话历史、流式运行状态、风险确认、取消、归档与 Trace 查看。
- 删除该页面会同时失去现有内部人工调试与 HITL 验证入口；仅改名不会改变其内部用户 JWT 与 Workspace RBAC 边界。

### 2.5 已确认范围解释

- “删除共享账号”包括删除由 Connection 长期持有、代表业务 API 身份的 API Key、固定 Bearer Token 与 OAuth client credentials；Broker 自身所需的集成机器信任仍保留，模型 API 配置不属于本需求。
- `NONE` 一并删除；所有 HTTP Tool 出站业务 API 都必须选择 Broker/OBO 或请求透传。
- 无最终用户 Subject 的 SYSTEM / 定时任务首期不能调用需要用户态鉴权的业务 API。
- 产品尚未上线，不设置兼容或迁移版本；新方案上线即硬切为严格双模式。

## 3. 目标用户

### 3.1 主要用户

- **第三方平台集成管理员**：配置 Broker/OBO、机器信任、业务 API 注入规则与允许 Scope。
- **第三方平台后端开发者**：通过 AAP 发起代表最终用户的 Run，并在无 Broker 时附带本次执行的业务 Token。
- **ACTWEAVE Workspace OWNER / ADMIN**：审批用户态出站策略、管理机器凭据、查看审计。
- **Workspace EDITOR / OPERATOR**：配置或测试 Tool，执行已批准的 Agent / Workflow。

### 3.2 非主要用户

- 直接把 ACTWEAVE 控制台当作第三方平台终端用户产品的业务用户。
- 需要在 ACTWEAVE 长期保存、续期或管理每个最终用户 refresh token 的身份团队。

## 4. 产品目标

1. 同一个已发布 Tool 能按本次执行的最终用户身份访问第三方业务 API。
2. Broker/OBO 模式下，ACTWEAVE 永不接收、持久化或回放用户长期业务凭据。
3. 请求透传模式下，不建立长期用户凭据库；临时 Token 不进入 PostgreSQL、MinIO、日志、审计 payload、事件、聊天消息、模型上下文或 Tool input。
4. 鉴权策略由 Provider 能力约束、由 Connection 实例选择，并在 Tool / Agent / Workflow 发布或绑定时可验证。
5. 用户态 Token 必须绑定 Workspace、Subject、Connection 与执行范围，不能被另一个用户、连接或 Run 复用。
6. 失败时可区分配置错误、缺少临时 Token、Token 过期、Broker 拒绝、业务 API 拒绝，而不泄露 Token 或上游敏感详情。
7. 保留可测试、可审计、可撤销的管理体验，并明确调试入口与生产入口的边界。
8. 上线后只存在 Broker/OBO 与请求透传两种出站身份策略，不提供共享账号、SYSTEM 例外或 `NONE`。

## 5. 非目标

- 不建设最终用户 OAuth consent、refresh token、离线授权或用户凭据管理中心。
- 不让 LLM、Prompt、Tool 参数或 Workflow DSL 读取、生成、选择或转发业务 Token。
- 不把 AAP 入站 Access Token 直接转发给第三方业务 API。
- 不允许一个“通用透传 Token”按模型决定发送到任意 Provider / Connection。
- 不在本需求中支持 Internal / MCP / Connector / Shell executor；首期只覆盖 HTTP Tool。
- 不为公开 API 提供 `NONE` 例外；即使上游无需鉴权，Connection 仍必须选择两种用户态策略之一。
- 不改变 Workflow 的 Draft、Compilation、CompiledExecutionPlan、Revision 生命周期语义。
- 不承诺第三方业务 Token 过期后的自动 refresh；Broker 可重新换取短 Token，透传模式由调用方重新提供。
- 不因为某个用户的 401/403 把整个 Connection 标记为全局不可用。

## 6. 核心概念

### 6.1 出站身份策略

Provider 声明可支持的出站身份策略；ServiceConnection 从中选择一个策略：

| 策略 | 产品名称 | 用户 Token 来源 | ACTWEAVE 持久化内容 |
| --- | --- | --- | --- |
| `BROKER_OBO` | Broker / OBO | ACTWEAVE 以机器信任 + Subject Assertion 向 Broker 换取 | Broker 配置、机器凭据引用、注入规则；不保存用户业务 Token |
| `REQUEST_PASSTHROUGH` | 本次请求透传 | 调用方随本次顶层执行请求提供 | 只保存策略与注入规则；不保存用户业务 Token |

目标态不提供 `SERVICE_ACCOUNT` 或 `NONE`。现有 API Key、固定 Bearer Token、OAuth client credentials 与无鉴权 Connection 都不能继续作为 Tool 出站策略；需要使用业务 API 的配置必须显式改为 `BROKER_OBO` 或 `REQUEST_PASSTHROUGH`，不得自动猜测目标。

### 6.2 临时出站凭据 Envelope

临时业务 Token 必须放在顶层执行请求的专用 `credentialBindings` / 等价 envelope 中，而不是：

- 用户文本 input；
- Tool input schema；
- Workflow Draft / DSL；
- AAP metadata；
- HTTP `Authorization`（该头已用于认证 ACTWEAVE 调用方）；
- Chat 消息正文。

一个 binding 至少绑定：

- `connectionId`；
- `subject`（从认证上下文得出，调用方不可覆盖）；
- Token 类型与注入目标；
- 本次 Run / Trial / Direct Invocation；
- 过期时间或最大驻留时间。

API 成功响应、幂等请求哈希、审计与事件只记录“是否提供、目标 Connection、策略、结果码”，不记录 Token、Token 指纹、声明或可逆摘要。

### 6.3 Subject Assertion

Broker/OBO 模式下，ACTWEAVE 向 Broker 提交短期、单次或短窗口的签名 Subject Assertion，建议包含：

- ACTWEAVE Workspace、Connection、Run / Invocation；
- Actor 类型与内部 ID；
- Subject 类型与内部 ID；
- Broker audience、申请 Scope；
- `iat`、`exp`、`jti`。

Assertion 不包含原始第三方 subject token 或业务 Token。Broker 负责把 ACTWEAVE 的 Subject 标识映射为第三方用户并执行自己的授权策略。

## 7. 目标流程

### 7.1 Broker / OBO 主流程

1. OWNER / ADMIN 在 Provider 声明 Broker/OBO 契约：Broker endpoint、机器认证方式、可申请 Scope、响应 Token 路径、业务 API 注入位置。
2. 在 ServiceConnection 选择 `BROKER_OBO`，绑定机器凭据并完成“配置级验证”。
3. 调用方通过 AAP 或允许的内部入口发起执行；认证层确定 Actor / Subject。
4. Tool Invocation 解析到该 Connection 后，验证 Principal Snapshot 中存在允许的 Subject。
5. ACTWEAVE 生成短期 Subject Assertion，以 Connection 的机器信任调用 Broker。
6. Broker 返回当前 Subject 的短期业务 Token。
7. ACTWEAVE 仅在允许的业务 API 原点和头部中注入该 Token，完成 Tool 请求。
8. Token 到期、Run 结束、权限撤销或 Connection 变更时，临时缓存立即失效。
9. 审计仅记录策略、Subject 内部引用、Connection、Broker 结果分类、业务 API 结果分类与 Trace。

### 7.2 请求透传主流程

1. OWNER / ADMIN 在 ServiceConnection 选择 `REQUEST_PASSTHROUGH`，配置允许的 Token 类型、目标头部、前缀、最大驻留时间和业务 API 原点。
2. 调用方发起顶层 Run / Trial / Direct Invocation，在专用凭据 envelope 中按 `connectionId` 提供 Token。
3. 入口层把 Token 写入运行期临时 Secret 隔离区，只返回不可猜测的内存句柄；持久化执行记录不含 Token 与句柄明文。
4. Tool Invocation 校验当前 Subject、Run 与 Connection 后读取临时 Token。
5. 执行器只向该 Connection 的允许原点注入 Token，调用结束后清理局部明文。
6. Run 结束、Token 到期、临时隔离区丢失或达到最大驻留时间时销毁 Token。
7. 缺失或过期时首期失败关闭并返回稳定错误码；调用方使用新 Token 新建或重试 Run，不支持执行中重新绑定。

### 7.3 异常流程

| 场景 | 产品行为 |
| --- | --- |
| 没有 Subject，却调用 `BROKER_OBO` | 不调用 Broker；返回 `OUTBOUND_SUBJECT_REQUIRED` |
| 未提供透传 Token | 不调用业务 API；返回 `OUTBOUND_CREDENTIAL_REQUIRED` |
| Token 过期 / 临时隔离区丢失 | 返回 `OUTBOUND_CREDENTIAL_EXPIRED`；不自动改用共享账号 |
| Broker 401/403 | 返回 `OUTBOUND_BROKER_DENIED`，记录非敏感原因分类 |
| Broker 5xx / timeout | 按安全重试策略处理；不得转为业务 API 请求 |
| 业务 API 401/403 | 归类为当前 Subject 的授权失败，不把 Connection 置为全局 `ERROR` |
| Connection 被禁用或策略变更 | 新 Invocation 失败关闭；进行中的明文 Token 不再可取 |
| 请求给 Agent / Plan 不允许使用的 Connection 附 Token | 请求校验失败；若是允许但本 Run 最终未使用的 Connection，Run 结束时直接销毁 Token |
| 同一请求重复绑定同一 Connection | 400 validation error，不做“最后一个覆盖” |
| 高风险操作等待人工确认 | 确认前不得预取 Broker Token；确认后再即时获取或读取仍有效的透传 Token |

## 8. Workflow 生命周期

| 阶段 | 用户态鉴权行为 |
| --- | --- |
| Draft | 只引用 Tool / Connection 和策略，不包含用户 Token |
| Compilation | 验证节点引用的 Connection 存在、策略受 Provider 支持；标记运行期凭据需求 |
| CompiledExecutionPlan | 固化 Connection 与出站身份策略快照，不固化业务 Token |
| Revision | 发布物保存策略契约与要求，不保存测试 Token |
| trial | 以调试者 Subject 执行；透传模式必须本次提交临时 Token |
| publish | 发布校验不得因为没有最终用户 Token 失败；只校验契约完整性 |
| production execution | 使用顶层执行的 Principal Snapshot；Broker 即时换取，透传从本 Run 的临时隔离区读取 |

同一个生产执行中不得从 `BROKER_OBO` 自动降级到旧共享账号，也不得从一个用户 Token 切换为另一个用户 Token。CompiledExecutionPlan 与实际 Connection 策略版本不一致时应失败关闭或要求重新编译，不能静默采用新策略。

## 9. 页面与交互设计

### 9.1 Provider

新增“用户态出站鉴权”契约区：

- 支持策略：Broker/OBO、请求透传；
- Broker endpoint、机器认证、Subject Assertion audience、Scope 映射；
- Token response path、业务 API 注入头与前缀；
- 支持的 Subject 类型；
- 敏感头与允许原点摘要。

Provider 页面不得接收最终用户业务 Token。机器 Secret 仍通过 Secret 引用管理。

### 9.2 ServiceConnection

创建 / 编辑表单的第一步选择“出站身份策略”，后续字段按策略展开：

- Broker/OBO：Broker 配置摘要、机器凭据、Subject 支持、Scope、缓存上限；
- 请求透传：Token 类型、注入位置、最大驻留时间、缺失凭据行为；
- 旧共享业务账号：只在迁移清单中展示，不允许作为目标态新策略保存。

列表新增“身份策略”列；详情区分别展示：

- **配置状态**：未验证 / 已验证 / 错误 / 已禁用；
- **运行时要求**：需要 External Subject / 每次请求需 Token；
- **最近配置验证**与**最近用户态调用结果摘要**，二者不能混成一个状态。

### 9.3 运行调试台（已确认）

将“对话式执行控制台”改名为“运行调试台”，定位为内部调试入口：

- 页面显著标注“仅用于内部调试，不是第三方最终用户入口”；
- 展示当前 Subject 类型（ACTWEAVE USER / External Subject 模拟不可用）；
- Broker 模式可以当前内部用户身份进行测试，但不得伪装第三方用户；
- 透传模式如开放，Token 输入框必须是一次性、不可回显、离开页面即丢弃，并明确“不会保存”；
- 会话历史只保留指令、结果与非敏感执行事件，不保留 Token；
- 已归档会话保持只读，仍不允许恢复历史 Token。

### 9.4 页面状态

| 状态 | Provider / Connection | 运行调试台 |
| --- | --- | --- |
| Loading | Skeleton；保存、验证、删除禁用 | 会话与 Agent 未就绪时禁用发送 |
| Empty | 引导先建 Provider，再建 Connection | 引导选择 Workspace / Agent |
| Error | 显示稳定错误码与重试；不显示 Broker body / Token | 显示运行失败分类与 Trace |
| Success | 展示已保存、已验证、锁版本 | 展示 Run 终态与 Tool 步骤 |
| Disabled | Connection 不可被新绑定 / 新执行 | 已禁用连接导致发送前提示或执行失败关闭 |
| Permission denied | 只读字段与缺权说明 | 无 `EXECUTE` 权限不可发送 |

### 9.5 危险操作

以下操作必须二次确认并写审计：

- 在 Broker/OBO 与请求透传之间切换；
- 更改 Token 注入头、允许原点、Broker audience；
- 更换 / 撤销机器凭据；
- 禁用正在被已发布 Tool / Workflow 使用的 Connection；
- 迁移或停用仍被已发布 Tool / Workflow 引用的旧共享账号 Connection。

确认弹窗必须展示受影响的已发布 Tool、Agent binding 和 Workflow Revision 数量；不能展示 Secret。

## 10. 权限（已确认）

| 操作 | OWNER | ADMIN | EDITOR | OPERATOR | VIEWER |
| --- | --- | --- | --- | --- | --- |
| 查看脱敏配置 | 是 | 是 | 是 | 是 | 是 |
| 配置 Broker / 注入规则 / 机器凭据 | 是 | 是 | 否 | 否 | 否 |
| 编辑非敏感 Connection 元数据 | 是 | 是 | 是 | 否 | 否 |
| 配置级验证 | 是 | 是 | 是 | 是 | 否 |
| 使用当前用户执行 | 是 | 是 | 是 | 是 | 否 |
| 提交请求透传 Token | 是 | 是 | 是 | 是 | 否 |
| 删除 / 禁用 | 是 | 是 | 否 | 否 | 否 |

平台管理员不自动跨 Workspace 获得用户态业务 Token 的读取权；任何角色都没有读取已提交 Token 的 API。

## 11. 数据、API 与安全影响

### 11.1 持久数据

允许持久化：

- Provider 支持的出站身份策略与非敏感 Broker 契约；
- Connection 选择的策略、机器 Secret 引用、Scope、注入规则、最大驻留策略；
- 发布物中的策略要求与版本；
- 审计中的策略、Connection、Subject 内部引用、结果分类。

禁止持久化：

- 用户业务 access token / refresh token；
- 可用于还原 Token 的加密密文、指纹、哈希、JWT claims；
- Broker 返回的完整响应；
- 运行期临时 Secret 句柄与 Token 的可关联稳定标识；
- Token 出现在 input/output 原文、永久对象、错误详情、Trace 标签。

### 11.2 API 方向

具体字段由架构设计冻结，但产品契约要求：

- Provider / Connection DTO 新增版本化 `outboundIdentity` 配置，所有 Secret 继续只返回 configured 状态；
- 首期由 AAP create Run、内部 direct Tool invoke、Workflow trial 接受相同的专用 `credentialBindings` envelope；production Workflow 只继承其顶层 AAP Run 的绑定，不另开 Token 输入面；
- Chat 文本 message API 不接受把 Token 塞进 message；若调试台支持透传，应走独立的凭据附加命令；
- 幂等重放只有在同一 Subject、同一策略且凭据仍有效时可复用原 Run；不得要求客户端用相同 Token 明文参与持久幂等哈希；
- 对外返回稳定错误码，不返回 Token、Broker body、Secret 名称或内部存储位置。

### 11.3 运行期隔离

- 临时 Token 只存在于专用运行期 Secret 隔离区，不进入普通 context map、JSON snapshot 或队列 payload。
- Token 必须绑定 Subject + Run + Connection；跨任一维度读取均拒绝。
- Broker Token 缓存键必须至少包含 Subject、Connection、Scope、策略版本；严禁沿用当前仅 Connection 级 OAuth 缓存。
- 所有 Token 获取与注入都在高风险确认完成后进行。
- 禁止跨源重定向携带敏感头；目标 host / port / CIDR 继续受现有 egress policy 限制。
- 原始错误在日志落盘前必须经过 Token 与认证头清洗。
- ACTWEAVE 进程重启后，透传 Token 不可恢复；Run 明确失败关闭，调用方使用新 Token 新建或重试 Run，不能从持久层恢复或对原 Run 重新绑定。

### 11.4 审计

建议新增或扩展非敏感事件：

- `outbound.identity.policy.created|updated|disabled`
- `outbound.credential.attached|expired|discarded`
- `outbound.broker.exchange.succeeded|failed`
- `outbound.business_api.authorization_denied`

事件字段只允许：Workspace、Connection、Run / Invocation、Subject 内部引用、策略、Scope 名称、结果码、耗时、Trace、操作者；禁止 Token、Assertion、Broker body、业务响应原文。

## 12. 上线硬切与旧配置处理

产品尚未上线，不设置兼容版本或 Workspace 自选窗口；新方案上线即只接受 Broker/OBO 与请求透传。

1. Provider / Connection 的新建、编辑、验证与执行 API 只接受 `BROKER_OBO`、`REQUEST_PASSTHROUGH`，拒绝 API Key、固定 Bearer、业务 API OAuth client credentials 与 `NONE`。
2. 升级时如本地开发 / 测试库存在旧 Connection，应标记为 `DISABLED + MIGRATION_REQUIRED`；不得继续执行，也不得自动推断目标策略。
3. OWNER / ADMIN 若需保留旧开发数据，必须显式选择两种目标策略之一、重新配置、完成验证，并重新校验 Tool、Agent binding、Workflow Compilation / CompiledExecutionPlan / Revision。
4. 引用未迁移 Connection 的 Tool test、Workflow trial、publish 和 production execution 全部失败关闭，并返回稳定的 `OUTBOUND_IDENTITY_MIGRATION_REQUIRED`；不能回退旧执行器。
5. 旧持久 Secret 不得复制到请求透传；在引用为零、缓存失效并记录审计后删除。
6. Broker 机器信任仍可使用 Workspace Secret，但只能调用 Broker，不能直接作为业务 API 的共享用户身份。
7. 模型 API 配置的模型服务凭据不属于本次移除范围。
8. 首期只支持 HTTP Tool；其他 executor 遇到用户态策略时明确报不支持。

## 13. 依赖

- 架构师确认运行期 Secret 隔离区、多实例路由、进程重启与清理机制。
- 安全评审 Subject Assertion、Broker machine trust、Token 注入与日志清洗。
- AAP / SDK 评审 additive `credentialBindings` 或独立 attach 命令的兼容策略。
- 执行内核将 Principal Snapshot 和临时凭据句柄贯穿 Agent、Workflow、Tool Invocation。
- Provider / Connection 鉴权契约升级与迁移。
- 前端 Provider、ServiceConnection、Tool test、Workflow trial、运行调试台改造。
- 审计、指标、错误码和端到端安全测试。

## 14. 风险

| 风险 | 影响 | 产品约束 |
| --- | --- | --- |
| Token 进入模型或永久原文 | 严重数据泄露 | 专用 envelope；schema、日志与存储测试必须证明不可达 |
| Connection 级缓存串用户 | 越权访问 | 缓存必须包含 Subject，且策略变更立即失效 |
| 透传 Token 在异步 Run 中过期 | 中途失败 | 明确 TTL、失败码与是否支持重新绑定 |
| Broker 无法识别内部 Subject UUID | OBO 不可用 | 负责人确认 Subject 映射合同 |
| 高风险确认前预取 Token | 扩大 Token 暴露窗口 | 确认后才获取 / 读取 |
| 用户 401 污染全局状态 | 误报连接故障 | 配置状态与用户调用状态分离 |
| 调试台被误当生产终端 | 绕过集成边界 | 改名、显著提示、权限与一次性 Token |
| 旧开发 / 测试配置无法执行 | 本地数据需重配 | 上线即标记 `MIGRATION_REQUIRED`，只允许显式改成两种目标策略 |
| SYSTEM / 定时任务没有用户 Subject | 任务无法调用用户态业务 API | 返回 `OUTBOUND_SUBJECT_REQUIRED`，不提供共享账号例外 |
| 无鉴权公开 API 也必须选择用户态策略 | 接入成本上升 | 产品严格保持双模式；不提供 `NONE` |

## 15. 已解决决策

O1～O8 来自负责人评论 `11c23b39-0fa8-4f45-8c45-835d82cd1abe`；O9～O11 来自评论 `895e5619-418f-4c82-86db-a15d3a4c8f6c`，均已写入 v0.3：

| 决策 | 负责人选择 | v0.3 结果 |
| --- | --- | --- |
| O1 共享账号 | C | 目标态删除共享业务账号，新旧 Connection 必须迁移 |
| O2 选择层级 | A | Provider 声明支持集合，ServiceConnection 固定选择 |
| O3 透传过期 / 重启 | A | Run 失败关闭；调用方使用新 Token 新建或重试，不支持原 Run 重新绑定 |
| O4 Broker 用户标识 | A | Subject Assertion 只含 ACTWEAVE 内部 External Subject UUID，由 Broker 映射 |
| O5 首期入口 | A | AAP Run + direct Tool invoke + Workflow trial；production Workflow 从 AAP Run 继承；Chat 走调试命令 |
| O6 对话台 | A | 改名“运行调试台”，保留会话、流式执行、HITL 与 Trace |
| O7 配置权限 | A | OWNER / ADMIN 配策略与 Secret；EDITOR 只改非敏感元数据 |
| O8 Broker Token 缓存 | A | 按 Subject + Connection + Scope + 策略版本短期缓存，TTL 不超过 Token 与 Run |
| O9 上线切换 | A | 产品未上线，无兼容窗口；上线即硬切，旧配置不可执行 |
| O10 SYSTEM / 定时任务 | A | 无最终用户 Subject 时返回 `OUTBOUND_SUBJECT_REQUIRED`，不允许用户态出站 |
| O11 `NONE` | B | 删除 `NONE`，所有出站业务 API 必须选择 Broker/OBO 或请求透传 |

## 16. 未解决事项

- 业务范围、流程、权限、数据保留与验收口径的未决项：**无**。
- v0.3 已由负责人在评论 `eb38577f-5fc8-4a3f-96b3-5cace02dc7e7` 明确批准并冻结。

## 17. 验收标准（Given / When / Then）

### AC1 Provider 声明策略

- Given 用户有策略配置权限
- When 新建或编辑 HTTP Provider
- Then 只可声明支持 Broker/OBO 与请求透传；共享业务账号与 `NONE` 均被拒绝，且不能在 Provider 中录入最终用户 Token

### AC2 Connection 固定选择

- Given Provider 支持两种用户态策略
- When 创建 ServiceConnection
- Then 必须明确选择一种策略；保存后列表和详情均展示策略与运行时要求

### AC3 Broker 主流程

- Given AAP Run 的 Principal Snapshot 包含 External Subject，Connection 为 `BROKER_OBO`
- When Tool Invocation 开始
- Then ACTWEAVE 以机器信任与短期 Subject Assertion 向 Broker 换 Token，并只向目标业务 API 注入返回 Token

### AC4 Broker 不保存用户 Token

- Given Broker 返回用户短期业务 Token
- When Run 完成、失败、取消或 Token 到期
- Then PostgreSQL、MinIO、事件、审计、日志、聊天消息、Tool input/output 和模型上下文均不存在该 Token

### AC5 Broker 无 Subject 失败关闭

- Given 执行没有符合策略的 Subject
- When 调用 `BROKER_OBO` Connection
- Then 不请求 Broker，返回 `OUTBOUND_SUBJECT_REQUIRED`，且不降级到共享账号

### AC6 透传主流程

- Given 调用方为当前 Subject 在顶层执行请求中绑定了目标 Connection 的有效 Token
- When Tool 使用该 Connection
- Then Token 只在本 Run、本 Subject、本 Connection 范围内可取，并被注入允许的业务 API 头

### AC7 透传 Token 不进入业务输入

- Given 调用方提交透传 Token
- When Agent / Workflow / Tool 执行并生成审计与事件
- Then LLM Prompt、Tool input schema、Workflow 数据、普通 metadata、永久原文与 API 响应均不包含 Token

### AC8 缺失 / 过期凭据

- Given Tool 需要透传 Token，但 Token 缺失、过期或运行期隔离区已丢失
- When Invocation 开始
- Then 业务 API 不被调用，Run 失败关闭并返回稳定错误码；调用方必须使用新 Token 新建或重试 Run

### AC9 用户隔离

- Given 用户 A 与用户 B 先后调用同一 Connection
- When 两次 Tool Invocation 执行
- Then A 的 Broker / 透传 Token 不能被 B 命中、读取或发送，缓存与审计可证明 Subject 隔离

### AC10 目标隔离

- Given 同一 Run 为 Connection A 提供 Token
- When Tool 使用 Connection B 或请求发生跨源重定向
- Then Token 不被发送，系统失败关闭并记录非敏感安全事件

### AC11 高风险确认

- Given Tool 需要人工确认
- When Run 尚未被批准
- Then 系统不预取 Broker Token、不读取透传 Token；批准后才进行最小窗口的获取与注入

### AC12 配置验证与用户授权分离

- Given Connection 的 Broker / endpoint 配置已验证
- When 某个最终用户收到业务 API 403
- Then 该 Invocation 显示用户授权失败，但 Connection 不被全局改为 `ERROR`

### AC13 权限不足

- Given 用户没有确认后的策略 / Secret 配置权限
- When 尝试创建、修改、切换或删除用户态 Connection
- Then API 拒绝并写授权拒绝审计；页面保持脱敏只读且说明所需权限

### AC14 Workflow 生命周期

- Given Workflow Draft 引用了用户态 Connection
- When 编译、发布、trial 与生产执行
- Then Draft / Plan / Revision 只保存策略要求；trial 本次提供临时凭据；生产使用顶层 Principal 与凭据绑定，不保存 Token

### AC15 共享业务账号迁移

- Given 本地开发 / 测试库存在共享账号或 `NONE` Connection
- When 启用新方案
- Then 旧 Connection 立即变为 `DISABLED + MIGRATION_REQUIRED`；所有引用失败关闭，OWNER / ADMIN 必须显式改为 Broker/OBO 或请求透传并重新验证，系统不得自动转换

### AC16 调试台

- Given 原对话式执行能力升级完成
- When 用户进入原对话式执行页面
- Then 页面名称为“运行调试台”，展示非生产定位、当前 Subject 和凭据一次性说明，并继续支持会话、流式状态、HITL、取消与 Trace

### AC17 错误与日志脱敏

- Given Token、Assertion 或 Broker 响应包含可识别的敏感字符串
- When 任一交换、注入、业务调用或清理步骤失败
- Then 客户端与日志只出现稳定错误码和安全摘要，敏感字符串不出现

### AC18 禁用与撤销

- Given Connection 被禁用、机器凭据撤销或策略版本变化
- When 新 Tool Invocation 尝试获取用户 Token
- Then 失败关闭；对应临时缓存失效，不继续使用旧 Token

### AC19 Broker Token 缓存隔离

- Given 同一 Subject 在同一 Run 中多次调用相同 Connection 与 Scope
- When Broker Token 仍在有效期内且策略版本未变
- Then 可以命中 Subject + Connection + Scope + 策略版本绑定的短期缓存；Run 结束、Token 到期或策略变化后缓存失效

### AC20 SYSTEM / 定时任务

- Given 执行 Actor 为 SYSTEM 且没有最终用户 Subject
- When Tool 需要 Broker/OBO 或请求透传
- Then 业务 API 不被调用，返回 `OUTBOUND_SUBJECT_REQUIRED`；不得使用专用 SYSTEM Token 或共享账号例外

### AC21 删除无鉴权连接

- Given 业务 API 不要求任何身份
- When 管理员创建或迁移 Connection
- Then `NONE` 被拒绝，管理员仍必须选择 Broker/OBO 或请求透传；编译与执行路径也不得接受缺失的出站身份策略

## 18. 当前结论

v0.3 已把“用户态出站身份”收敛为 Provider 能力 + Connection 固定策略。Broker/OBO 是生产首选，请求透传是私有化环境的受限兜底；共享业务账号、`NONE` 和 SYSTEM 例外全部删除，上线即硬切。现有 AAP External Subject 与不可变 Principal Snapshot 可以作为用户绑定基础，但当前 Secret Injector、Connection 状态、Run API 与缓存模型都需要新增明确边界。

O1～O11 已全部解决，当前没有业务未决项。负责人已明确批准 v0.3，产品范围、非目标与 AC1～AC21 均已冻结，可以由 Conductor 按流程恢复 Issue 并交给 Knower 开展架构设计。

## 19. 冻结基线与 Knower 输入

### 19.1 冻结范围

- 首期只覆盖 HTTP Tool 出站调用。
- Provider 声明支持的鉴权策略集合，ServiceConnection 必须固定选择 `BROKER_OBO` 或 `REQUEST_PASSTHROUGH`，不得由模型、Tool input 或单次 Run 动态切换。
- Broker/OBO 使用集成机器信任与只含 ACTWEAVE 内部 External Subject UUID 的短期 Subject Assertion；Token 缓存必须绑定 Subject + Connection + Scope + 策略版本，TTL 不超过 Token 与 Run。
- 请求透传首期覆盖 AAP Run、direct Tool invoke、Workflow trial；production Workflow 从 AAP Run 继承；运行调试台通过独立调试凭据命令绑定。Token 只存在于运行期隔离区，绑定 Subject + Run + Connection，过期或重启后失败关闭并要求以新 Token 新建或重试 Run。
- 无最终用户 Subject 的 SYSTEM / 定时任务失败并返回 `OUTBOUND_SUBJECT_REQUIRED`。
- 产品尚未上线，上线即硬切；共享 API Key、固定 Bearer、业务 API OAuth client credentials 与 `NONE` 均不再支持。旧开发 / 测试 Connection 标记为 `DISABLED + MIGRATION_REQUIRED`，引用它们的验证与执行路径全部失败关闭。
- 原“对话式执行控制台”改名为“运行调试台”，保留会话、流式执行、HITL、取消与 Trace，并明确它不是第三方最终用户入口。
- OWNER / ADMIN 可配置策略与 Secret；EDITOR 只可修改非敏感元数据；执行与测试权限按本文权限矩阵实施。
- Workflow Draft、Compilation、CompiledExecutionPlan 与 Revision 只保存策略要求；trial 与 production execution 才使用当前 Principal 获取或绑定临时凭据。

### 19.2 冻结非目标

- 不建设最终用户 OAuth consent、refresh token 或长期凭据库。
- 不把调用 ACTWEAVE 的 AAP Token 直接转发给第三方业务 API。
- 不允许模型、Prompt、Tool input/output、Workflow 数据或普通 metadata 接触出站 Token。
- 不扩展到 HTTP Tool 以外的执行器，不支持每次 Run 动态切换 Connection 策略。
- 不保留共享账号、`NONE`、公开 API 或 SYSTEM Subject 例外。
- 不提供原 Run 的 Token 自动刷新、恢复或重新绑定。
- 不改变 Workflow 既有 Draft、Compilation、Plan、Revision、trial、publish 与 production execution 的语义。

### 19.3 验收基线

第 17 节 AC1～AC21 全部作为冻结验收标准，覆盖配置、Broker/OBO、透传、用户与目标隔离、HITL、权限、Workflow 生命周期、硬切迁移、调试台、错误脱敏、撤销、缓存、SYSTEM 与删除 `NONE`。

### 19.4 交给 Knower 的输入

- 唯一产品基线：本文 v0.3、负责人批准评论 `eb38577f-5fc8-4a3f-96b3-5cace02dc7e7` 与 AC1～AC21。
- 已验证实现事实：当前 `service-auth.v1` 只有 `NONE` / `OAUTH2_CLIENT`；Connection 有持久 Secret；HTTP OAuth 缓存不感知 Subject；AAP Principal Snapshot 已传播 External Subject；各执行入口尚无统一临时凭据 envelope。
- 架构设计需定义：版本化 `outboundIdentity` 契约；运行期临时 Secret 的隔离、绑定与清理；Broker Assertion、机器信任、缓存、失效与撤销；各入口的 additive credential binding 与调试台绑定命令；上线硬切和旧配置失败语义；编译 / 就绪校验；稳定错误码、审计脱敏与安全测试。
- 架构阶段不得新增第三种鉴权模式、共享账号、`NONE` 或 SYSTEM 例外；任何产品范围变化必须退回本确认流程。
