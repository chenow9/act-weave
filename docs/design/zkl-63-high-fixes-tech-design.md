# ZKL-63 三项 HIGH 修复技术设计

| 项 | 值 |
|---|---|
| Issue | ZKL-63 / `2eb1a799-1056-4ea5-a306-c02d087fb72e` |
| 文档版本 | **v1** |
| 状态 | **待负责人确认；禁止实施、禁止生成 implementation checklist、禁止交 Forge** |
| 需求确认 | 负责人已确认选项 B：串行修复 `HIGH-02 → HIGH-01 → HIGH-03`，并使用 Chrome 验证与回归 |
| 代码基线 | `a623b6f7e5971d5cf99972fab11163230e65e029` |
| Review 基线 | `docs/verification/zkl-63-backend-code-review.md` |
| 外部协议基线 | `docs/openapi/agent-access-v1.yaml`；本方案不得改变 AAP path、auth、schema、CORS 或事件协议 |
| UI / Canvas 输入 | 当前 Issue 无 Canvas 或专属 UI 设计输入；v1 推荐复用现有登录页视觉语言做最小改密闭环，是否无需 Canvas 见 D6 |

> 本文只设计已确认的 3 个 HIGH。MEDIUM、LOW、观察项、panic 日志策略、Refresh reuse family revoke、生产配置门禁及 Agent Audit 原文策略均不在本 Issue 范围。

## 1. 已冻结范围与实施顺序

实施必须严格串行：

1. **HIGH-02**：HTTP 失败日志不再保存或输出裸 `err.Error()`。
2. **HIGH-01**：Console Access Token 每次请求重验权威 session / user / credential 状态，并使用数据库平台角色。
3. **HIGH-03**：`MustChangePassword` 服务端强制；补齐浏览器可完成的最小改密路径。

顺序不能通过子 Issue、Stage 或并行任务表达。本版只给技术方案，不是实施 checklist。

## 2. 现状证据

### 2.1 HIGH-02：安全响应与不安全日志并存

- `backend/internal/transport/http/errors.go` 的 `RespondError` 已把内部错误映射为稳定 `code/message/requestId/traceId`，不会把 Cause 返回客户端。
- `backend/internal/transport/http/router.go` 的 `requestLoggingMiddleware` 仍写入：
  - `error_code = failure.mapped.code`
  - `error = failure.err.Error()`
  - `error_type`
  - `error_source`
- `backend/internal/transport/http/router_test.go` 当前甚至正向断言内部 canary 文本出现在日志；该断言与 HIGH-02 的目标相反。
- `backend/internal/logging/aap_fields.go` 已证明 AAP 面采用 allowlist / sensitive marker 防护，但它是 AAP 专属策略，不能把管理面错误原文继续当成稳定日志字段。

### 2.2 HIGH-01：JWT 验签后直接信任 claim

- `backend/internal/authn/access_token.go` 使用 HS256、issuer、时间和 `sid/sub` 校验；Access Token 默认 TTL 为 15 分钟。
- `backend/internal/transport/http/router.go` 的 `AccessTokenAuthenticator.AuthenticateAccessToken` 仅解析 JWT，并把 claim 中的 `username/platformRole` 直接写入 `Principal`。
- `authenticationMiddleware` 不读取 `auth_sessions`、`users` 或 `user_credentials`。
- `platformAdmin()`、`agent_audit.go` 与 `workspace_overview.go` 使用 `Principal.PlatformRole`，因此目前会使用 JWT 内嵌旧角色。
- `backend/internal/identity/auth_repository.go` 已在密码重置、用户禁用/锁定、平台角色变更时事务性撤销 session；但当前只阻止 refresh，不能阻止尚未过期的 Access Token。
- `backend/internal/database/migrations/000003_identity.up.sql` 已有完成修复所需的数据：
  - `auth_sessions.user_id/expires_at/revoked_at`
  - `users.status/platform_role`
  - `user_credentials.locked_until/must_change_password`
- `auth_sessions.id`、`users.id`、`user_credentials.user_id` 均为主键；按 `sid + sub` 的单行 JOIN 不需要新增索引或迁移。
- Workspace 业务权限已经由 `authz.Service.AuthorizeWorkspace` 每次读取当前状态；本方案补齐的是其之前的 Console 用户 session / 平台角色入口门禁。

### 2.3 HIGH-03：后端返回 flag，但前后端都未闭环

- 登录与 refresh 响应已经返回 `mustChangePassword`。
- 管理员创建用户、重置密码与 bootstrap 会设置 `must_change_password=true`。
- `authn.Service.ChangePassword` 成功后会在一个事务内：
  - 替换密码；
  - 设置 `must_change_password=false`；
  - 撤销该用户全部 session。
- `authenticationMiddleware` 当前不检查该 flag。
- `frontend/src/stores/auth.ts` 保存 `mustChangePassword`，但没有 `changePassword` action。
- `frontend/src/router/index.ts` 只有登录、平台管理员守卫，没有强制改密守卫。
- `frontend/src/views/LoginView.vue` 登录成功后无条件跳转 `overview`。
- 前端没有改密页面或对 `/users/me:change-password` 的调用。若只上服务端门禁，临时密码用户会被困住，Chrome 验收也无法完成。
- `frontend/src/services/api.ts` 对一般 401 会自动 refresh 并重试；改密接口当前未列入 auth lifecycle 排除项。若当前密码错误返回 401，自动重试会造成重复密码尝试，必须一并修正。

### 2.4 契约与运行资料

- 内部 Console API `/api/v1` 没有独立 OpenAPI 文件，现有路由和错误契约以 `backend/internal/transport/http/contract_test.go`、`errors.go` 及前端 DTO 为事实。
- `docs/runbooks/protocol-event-console-vs-aap-entrypoints.md` 明确 `/api/v1` 用户 session 与 `/api/agent-access/v1` AAP token 是不同入口。本方案只改前者。
- 当前仓库没有适用于本改密流程的专属 Canvas 输入或身份运行手册。
- 本轮尝试运行 `go test ./internal/authn ./internal/logging ./internal/transport/http` 时，本机 `go version go1.20.7` 无法解析项目 `go 1.25.0`，因此本轮不宣称测试通过。Review 报告记录了 Sentinel 在匹配环境中的同组包级测试 PASS；实施验收仍必须在 Go 1.25.x 重跑。

## 3. 目标与非目标

### 3.1 目标

1. 通用 HTTP 失败日志只包含稳定、低敏感的诊断字段，不包含任意内部错误原文。
2. 每个 Console protected request 都以数据库当前状态判定：
   - JWT 是否有效；
   - session 是否属于该 subject、未撤销且未过期；
   - user 是否可用；
   - credential 是否处于允许状态；
   - 当前平台角色与是否必须改密。
3. 密码重置、禁用、锁定或角色变更提交后，后续新请求不再享受旧 Access Token 的 15 分钟信任窗口。
4. `must_change_password=true` 时，服务端只放行最小恢复路径，其他 protected API 返回稳定错误。
5. 临时密码用户可以在 Chrome 内完成改密、重新登录并回到正常控制台。
6. 保持 AAP、Workspace 成员角色、现有登录/refresh DTO、改密请求/成功响应以及数据库 schema 不变。

### 3.2 非目标

- 不改 HS256 签名算法、JWT claim shape、Access Token TTL 或 Refresh Token rotation 模型。
- 不引入 token blacklist、Redis、security-version 列或 auth state cache。
- 不改变 Workspace role matrix 或让平台管理员自动获得 Workspace 权限。
- 不修改 AAP 鉴权、AAP SecurityVersion 或外部 OpenAPI / SDK。
- 不新增密码复杂度规则；仍沿用现有最少 12 位约束。
- 不改变 `recoveryMiddleware` 的 panic/stack 日志策略；它不是 HIGH-02 Review 指定的位置。
- 不为每次成功认证写 durable audit row。
- 不处理本 Issue 已明确排除的 MEDIUM / LOW / 观察项。

## 4. 推荐架构总览

```text
HTTP protected request
  → parse + verify Console JWT
  → authn AuthenticateAccessToken
      → identity ResolveAccessSessionState (one indexed DB query)
      → validate sid/sub/session/user/credential
      → build authoritative AccessIdentity
  → transport Principal (DB username/role/must-change)
  → must-change route gate
      → allowlisted recovery route, or
      → 403 PASSWORD_CHANGE_REQUIRED
  → existing route handler / workspace authorizer
```

模块边界：

| 模块 | 负责 | 不负责 |
|---|---|---|
| `identity` | 单查询读取 session + user + credential 的安全状态投影 | JWT 解析、HTTP 错误、路由白名单 |
| `authn` | JWT 验证、权威状态判定、错误分类、产出 `AccessIdentity` | Gin、平台业务 handler |
| `transport/http` | Bearer 语法、`Principal` 注入、强制改密路由门禁、稳定 HTTP 映射、安全请求日志 | 直接拼 SQL、信任 JWT role |
| `application` | 注入 `authn.Service` / authenticator | 复制认证规则 |
| `frontend auth/router` | 保存 flag、强制导航、改密提交、成功后清 session 并重新登录 | 绕过服务端门禁 |

## 5. HIGH-02：失败日志安全化

### 5.1 推荐行为

`requestFailure` 不再持有可被后续 logger 误用的完整 `error`。`RespondError` 完成映射后，只保存：

- `mapped`（稳定 HTTP status/code/message；日志只使用 code）；
- `errorType`（由 `%T` 在内存中生成的类型名）；
- `file/line`（保持现有 source 诊断格式，避免本 Issue 顺带改变日志消费契约）。

`requestLoggingMiddleware` 失败字段固定为：

```text
error_code
error_type
error_source
```

明确删除 `error` 字段，不做截断后保留，也不提供 debug 绕过。这样从结构上保证任意 `fmt.Errorf` 链、上游响应片段、路径、Secret、token 或业务原文都不会从该通道进入日志。

### 5.2 API、数据与兼容

- HTTP status、错误 body、requestId/traceId、retryable 均不变。
- 无数据库、配置或前端变更。
- 日志消费者若依赖 `error` 文本必须改为 `error_code + error_type + error_source + request_id/trace_id`。
- AAP 专属日志过滤保持不变。

### 5.3 验证

- 反转 `router_test.go` 现有泄漏断言：canary 必须不出现，且结构化日志不存在 `error` key。
- 覆盖 wrapped error、Bearer/JWT 形态、`password=`、PEM header、长上游 body canary。
- 保留 `error_code/error_type/error_source/request_id/trace_id` 断言，保证仍可定位。
- 对外响应继续断言不含 canary。

## 6. HIGH-01：Access Token 权威状态重验

### 6.1 身份读取模型

在 `identity` 增加只读投影（建议命名 `AccessSessionState`）及方法（建议命名 `ResolveAccessSessionState`）。输入：

- JWT `sub`；
- JWT `sid`。

`authn` 在一次认证中只捕获一次当前 UTC 时间，并用它同时完成 JWT 与返回状态的时效判定；SQL 只读取事实，不混用数据库时钟和进程时钟。

一次查询按 `auth_sessions.id = sid AND auth_sessions.user_id = sub` JOIN：

- session：`id/user_id/expires_at/revoked_at`；
- user：`id/username/status/platform_role`；
- credential：`locked_until/must_change_password`。

投影不得包含 `password_hash`、refresh hash、cookie、JWT 或其他凭据原文。

### 6.2 authn 判定顺序

1. 使用现有 `AccessTokenManager.Parse` 验证算法、签名、issuer、nbf/exp、`sub/sid`。
2. 读取 `AccessSessionState`。
3. 任一条件不满足则拒绝：
   - state 不存在或 credential 关联缺失；
   - session 的 user 与 `sub` 不一致；
   - `revoked_at != nil`；
   - `expires_at <= now`；
   - `users.status != ACTIVE`；
   - `locked_until > now`（见 D3）。
4. 成功 Principal 使用数据库的：
   - `username`；
   - `platform_role`；
   - `must_change_password`。
5. JWT 中原有 `username/platformRole` claim 暂保留以兼容已签发 token shape，但只作为签名内 hint，不再参与授权。

推荐在 `authn` 新建独立文件承载 `AccessIdentity` 和认证方法，避免继续把安全策略放在 Gin adapter 内。`httptransport.AccessTokenAuthenticator` 只把 `authn.AccessIdentity` 映射成 `Principal`。

### 6.3 错误契约

| 情况 | HTTP | code | 行为 |
|---|---:|---|---|
| JWT 无效/过期、state 不存在、session 撤销/过期/subject 不匹配、user 非 ACTIVE、credential 当前锁定 | 401 | `UNAUTHENTICATED` | 不暴露具体失败原因；前端可尝试一次 refresh，失败后清 session |
| 权威状态查询发生数据库/基础设施错误 | 503 | `AUTHENTICATION_UNAVAILABLE` | `retryable=true`；不伪装成用户凭据错误，不主动清除有效本地 session |
| 校验成功但必须改密且路由不在白名单 | 403 | `PASSWORD_CHANGE_REQUIRED` | 见 HIGH-03 |

数据库错误必须 fail closed；不能回退为“只信 JWT”。

### 6.4 性能与一致性

- 每个 `/api/v1` protected request 增加一次按三个主键的单行 JOIN。
- 不更新 `last_seen_at`，避免把读门禁变为每请求写放大。
- 不做进程缓存，确保角色、禁用、session 撤销和 must-change 在提交后对下一次校验可见。
- PostgreSQL `READ COMMITTED` 下，保证“状态变更事务提交后开始校验的新请求”看到新状态。已经完成门禁并进入 handler 的并发请求可能继续完成；本 Issue 不引入跨整个 handler 的数据库锁。
- 角色/状态/密码安全变更仍由现有事务更新事实并撤销 session；本方案只补上 Access Token 对撤销事实的读取。

### 6.5 权限影响

- `/admin/*`、Agent Audit、overview 中的平台角色均改用 DB 当前值。
- Workspace 权限仍由既有 `AuthorizeWorkspace` 再读成员和 Workspace 状态；不合并两套角色。
- AAP token 和 principal context 完全不经过本查询。

## 7. HIGH-03：MustChangePassword 服务端闭环

### 7.1 服务端门禁

在成功注入 authoritative Principal 后、调用业务 handler 前检查 `MustChangePassword`。

推荐白名单按“HTTP method + Gin 注册模板”匹配，不使用前缀或任意字符串 contains：

| 外部路径 | Gin 内部注册路径 | 方法 | 原因 |
|---|---|---:|---|
| `/api/v1/users/me:change-password` | `/api/v1/users/me/__command/change-password` | POST | 唯一解除 flag 的路径 |
| `/api/v1/auth/logout` | 同路径 | POST | 用户可安全退出并撤销当前 session |
| `/api/v1/users/me` | 同路径 | GET | 只读展示当前账户；不允许资料修改 |

`/auth/login` 与 `/auth/refresh` 是 public route，不经过该门禁：

- login 必须继续签发受限 session，用户才能提交当前密码完成改密；
- refresh 可在页面刷新后恢复 `mustChangePassword` 状态，但新 Access Token 仍受相同服务端门禁。

其余 protected route，包括 `PATCH /users/me`、Workspace、业务执行、管理面和 Agent Audit，统一返回：

```json
{
  "error": {
    "code": "PASSWORD_CHANGE_REQUIRED",
    "message": "Password change is required before continuing.",
    "requestId": "...",
    "traceId": "...",
    "retryable": false,
    "details": []
  }
}
```

推荐 HTTP 403：调用者已经认证，但当前安全策略不允许该操作。不能使用 401，否则前端会错误触发 refresh/retry；也不推荐 409 把权限前置条件混入资源冲突语义。

### 7.2 前端最小闭环

推荐增加独立 `/change-password` 路由，复用 `LoginView` 的双栏布局、输入框、反馈和按钮样式，不进入 `AppShell`：

1. 登录/refresh 后 `mustChangePassword=true`：
   - 任意正常业务路由重定向 `/change-password`；
   - `/login` 也重定向 `/change-password`，而不是 overview。
2. `mustChangePassword=false` 的已登录用户访问 `/change-password` 时重定向 overview。
3. 页面字段：
   - 当前密码；
   - 新密码（至少 12 位）；
   - 确认新密码；
   - “修改密码并重新登录”。
4. `auth.changePassword(currentPassword, newPassword)` 调用现有 `POST /users/me:change-password`。
5. 成功 204 后：
   - 服务端已撤销全部 session 并清 refresh cookie；
   - 前端立即 `clearSession()`；
   - 跳转 `/login?passwordChanged=1`；
   - 不自动登录、不保留当前密码或新密码。
6. `api.ts` 把 `/users/me:change-password` 列为 auth lifecycle request，当前密码错误的 401 不得自动 refresh/retry。
7. 提交中禁用按钮，防止非幂等改密请求被双击。

这不是视觉重设计；不改变导航、信息架构或品牌样式。若负责人要求新弹窗、向导、密码规则说明或其他视觉方案，需先补 Canvas/UI 输入并重新确认。

### 7.3 兼容

- 登录/refresh `AuthTokenResponse` shape 不变。
- 改密 request body 与 204 成功响应不变。
- 新增的 `PASSWORD_CHANGE_REQUIRED` 是条件性错误；不处于 must-change 状态的客户端行为不变。
- 旧客户端在 must-change 状态下至少会安全收到 403，不会继续访问业务 API；它需要升级 UI 才能自助解除状态。

## 8. 数据、迁移与部署单元

### 8.1 数据与迁移

- **不新增数据库迁移。**
- 不修改现有列、索引、约束、JWT claim 或 refresh cookie。
- 权威读取仅使用现有三表主键连接。
- 无数据回填、双写、兼容读或清理任务。

### 8.2 部署单元

- HIGH-02 可独立部署。
- HIGH-01 可在 HIGH-02 验证通过后独立部署。
- HIGH-03 的后端门禁与前端改密页/守卫必须作为同一发布单元；禁止先发布门禁、后补 UI，避免临时密码用户无路可走。
- 三项可以是依赖顺序明确的提交，但验收证据必须逐项记录；最终再做一次整体验收。

## 9. 状态机、并发与幂等

### 9.1 请求状态机

```text
TOKEN_INVALID
  → 401 UNAUTHENTICATED

TOKEN_VALID
  → STATE_UNAVAILABLE
      → 503 AUTHENTICATION_UNAVAILABLE
  → SESSION_OR_USER_INVALID
      → 401 UNAUTHENTICATED
  → STATE_VALID + MUST_CHANGE=false
      → existing protected handler
  → STATE_VALID + MUST_CHANGE=true + allowlisted route
      → recovery handler
  → STATE_VALID + MUST_CHANGE=true + other route
      → 403 PASSWORD_CHANGE_REQUIRED
```

### 9.2 安全变更时序

```text
admin reset/status/role transaction commits
  → authoritative row changed
  → affected sessions revoked in same transaction
  → next protected request reads revoked/current state
  → old Access Token rejected
```

改密成功：

```text
password replacement + flag=false + all sessions revoked commit
  → 204 + clear refresh cookie
  → frontend clears Access Token
  → user logs in with new password
  → new session has mustChangePassword=false
```

### 9.3 幂等

- Access state validation是纯读，不引入 receipt、lock version 或写入幂等问题。
- session revoke 继续使用现有幂等语义。
- 改密不是幂等命令：第一次成功后旧“当前密码”失效；前端和 API client 均不得自动重试或双击重发。
- 日志安全化不改变请求处理幂等性。

## 10. 安全、审计与可观测性

### 10.1 安全边界

- 数据库不可用时 fail closed，绝不退回 JWT-only。
- 认证失败响应不区分 session 不存在、已撤销、用户禁用或 credential 锁定，避免状态探测。
- `Principal` 中的 role / must-change 必须来自同一次权威查询。
- 日志、测试证据、Chrome 截图和 Issue 评论不得包含真实密码、cookie、Access/Refresh Token 或业务原文。

### 10.2 审计

- 现有管理员 reset/status/role 的 durable identity audit 保持不变。
- 本 Issue 不为每个成功 Access Token 校验写 durable audit，避免高基数/高写放大。
- `PASSWORD_CHANGE_REQUIRED` 与认证不可用通过现有 HTTP completion log 的稳定 `error_code/request_id/trace_id` 关联。
- 自助改密新增 durable audit 不属于已确认 HIGH 范围；若负责人要求，需另行确认范围。

### 10.3 日志与监控

保留并验证：

- `event=http.request.completed`
- `status`
- `error_code`
- `error_type`
- `error_source`
- `request_id`
- `trace_id`
- 已成功认证时的 `user_id`

观察指标/查询：

- 401 `UNAUTHENTICATED` 比例；
- 403 `PASSWORD_CHANGE_REQUIRED` 比例；
- 503 `AUTHENTICATION_UNAVAILABLE` 比例；
- protected request P50/P95/P99 延迟与 PostgreSQL 查询延迟；
- 数据库连接池等待/耗尽。

本 Issue 不把 user/session/request ID 放入 metric label；这些只用于结构化日志关联。

## 11. 精确改动面

预计实施只触及下列范围；checklist 获批后再把它们拆成机械步骤：

| 范围 | 预计文件/符号 |
|---|---|
| 安全失败日志 | `backend/internal/transport/http/errors.go`：`requestFailure/RespondError`；`router.go`：`requestLoggingMiddleware`；`router_test.go` |
| 权威状态投影 | `backend/internal/identity/models.go`；新增窄化 repository 文件或 `auth_repository.go`；对应 repository tests |
| authn policy | `backend/internal/authn/access_token.go` 或新增 `access_session.go`；`service.go` 的 repository interface；authn tests |
| transport principal/gate | `backend/internal/transport/http/context.go`、`router.go`、`errors.go`、`auth_user_test.go`、authentication/contract tests |
| 应用 wiring | `backend/internal/application/application.go` |
| 前端 auth | `frontend/src/stores/auth.ts`、`services/api.ts` 及 tests |
| 前端路由/页面 | `frontend/src/router/index.ts`、`router/access.test.ts`、新增最小改密 view/test，必要的现有登录样式复用 |
| 文档 | 本文；方案批准后才生成 `docs/design/zkl-63-implementation-checklist.md` |

不得修改：

- `docs/openapi/agent-access-v1.yaml`
- AAP auth / data-plane package与 SDK
- 数据库 migration 文件
- MEDIUM / LOW 对应代码

## 12. 测试与验收

### 12.1 HIGH-02 自动化

- 通用 HTTP 500 / wrapped error canary 不出现在日志和响应。
- 日志不存在 `error` 原文字段，仍有稳定 code/type/source/correlation。
- 401/403/404/409/422/500 映射不变。
- AAP 日志 allowlist 既有测试继续通过。

### 12.2 HIGH-01 自动化

Repository / authn：

- valid token + valid matching session + ACTIVE user → PASS。
- session missing、subject mismatch、revoked、expired → `UNAUTHENTICATED`。
- user `LOCKED/DISABLED` → `UNAUTHENTICATED`。
- `locked_until > now` → 按 D3 决策断言。
- role / username 使用 DB 当前值，不使用 JWT 旧 claim。
- credential row 缺失 → fail closed。
- DB error → 503 `AUTHENTICATION_UNAVAILABLE`，且 `retryable=true`。

HTTP 集成：

- logout 后旧 Access Token 下一请求 401。
- reset password 后旧 Access Token 下一请求 401。
- status 变更后旧 Access Token 下一请求 401。
- platform role 变更后旧 Access Token 下一请求 401；重新登录后按新角色授权。
- AAP token 与 Console token 继续双向不可互用。

### 12.3 HIGH-03 自动化

后端：

- temporary login 返回 `mustChangePassword=true`。
- allowlist method/path 表驱动测试：
  - change-password、logout、GET me 放行；
  - PATCH me、admin、workspace、business route 全部 403 `PASSWORD_CHANGE_REQUIRED`。
- refresh 后仍为受限 principal。
- 改密成功 flag 清除、session 撤销、cookie 清除；旧 token 401，新密码登录成功。
- 失败改密不会清 flag，不会被客户端自动重试。

前端：

- login / restore flag=true → `/change-password`。
- 强制状态访问 overview/users/workspace → 重定向改密页。
- 普通用户不能停留在改密页。
- 新密码与确认不一致、长度不足、当前密码错误、网络错误、提交中状态。
- 204 后清 token/user/flag，跳登录成功提示，不保存密码。
- change-password 的 401 不触发 refresh/retry。

### 12.4 命令门禁

在 Go 1.25.x 环境：

```bash
cd backend
go test ./internal/identity ./internal/authn ./internal/logging ./internal/transport/http
go test ./...
```

前端：

```bash
cd frontend
npm test
npm run build
```

任一全量失败必须区分“本改动回归”与“既有失败”，但不得以既有失败掩盖相关包失败。

### 12.5 Chrome 整体验收

使用两个隔离 Chrome profile/context（平台管理员 A、目标用户 B），仅使用可丢弃测试账户。证据中遮蔽密码、cookie 和 token。

| 场景 | Chrome 操作 | 预期 |
|---|---|---|
| 正常登录冒烟 | A 正常登录，访问 overview、users、一个 Workspace 主路径 | 页面可用；无强制改密误判 |
| HIGH-02 对外安全 | 用 Chrome DevTools 观察一个安全构造的失败响应 | 只见稳定 error code/message/requestId；服务端测试日志 canary 零命中 |
| reset 立即失效 | B 已登录；A 在用户与权限重置 B 密码；B 再导航/请求 | 旧 token 401，refresh 失败后回登录，不可继续业务 |
| 临时密码登录 | B 用临时密码登录 | 直接进入 `/change-password`，不能进入 overview |
| 服务端门禁 | 在 DevTools 对任一业务 API 发请求 | 403 `PASSWORD_CHANGE_REQUIRED`；change-password/logout/GET me 可用 |
| 完成改密 | B 输入当前临时密码与新密码 | 204 后回登录；旧 session/token 不可用；新密码登录后正常 |
| 降权立即失效 | B 先为非最后一个 PLATFORM_ADMIN；A 将 B 降为 USER | B 旧 token 下一请求 401；重新登录后 users 导航不可见、直调 admin API 为 403 |
| 禁用/锁定 | A 将 B 设为 LOCKED/DISABLED | B 旧 token 下一请求 401；不能 refresh 或继续业务 |
| 回归 | A/B 分别验证 logout、refresh、用户管理和 Workspace 主路径 | 无 refresh loop、无无限重定向、无异常重复改密请求 |

Chrome 只是整体验收之一；HIGH-02 的“日志无泄漏”和并发/数据库错误必须由自动化测试与日志扫描证明。

## 13. 发布、回滚与风险

### 13.1 发布

1. HIGH-02 上线后确认日志 pipeline 仍可按 code/type/source 诊断，canary 零泄漏。
2. HIGH-01 上线后观察 protected API 延迟、DB pool 和 401/503。
3. HIGH-03 前后端同批上线，先用测试用户验证强制改密，再做正常用户回归。
4. 最终执行 §12 全套自动化与 Chrome 验收。

### 13.2 回滚

- 无 schema/data migration，应用回滚不需要数据回滚。
- HIGH-02 若日志消费器不兼容，优先修消费器；回滚会重新引入已确认泄漏风险，只能作为短时紧急措施。
- HIGH-01 可独立回滚，但会恢复最长 Access Token TTL 的旧信任窗口；回滚期间须记录安全风险。
- HIGH-03 必须前后端一起回滚；不能只回滚 UI 或只回滚门禁。
- 已由用户完成的密码变更和 session 撤销是安全事实，不回滚。

### 13.3 主要风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| 每请求 DB JOIN 增加延迟/连接压力 | 所有 Console protected API | 单行主键 JOIN、纯读、不更新 last_seen；压测并监控 P95/P99 与 pool |
| DB 短故障导致 fail-closed | Console 暂时 503 | 独立 `AUTHENTICATION_UNAVAILABLE`，避免误清 session；不降级 JWT-only |
| 先上门禁后上 UI | 临时密码用户被困 | HIGH-03 前后端原子发布 |
| 前端 401 自动重试改密 | 重复密码失败/锁定 | change-password 加入 auth lifecycle 排除；按钮防重 |
| 路由白名单过宽/前缀匹配错误 | 绕过强制改密 | method + 注册模板精确 allowlist、表驱动全路由测试 |
| credential 临时锁参与 Access 判定 | 已登录用户在锁定窗口被阻断 | 由 D3 明确确认并用 Chrome/集成测试锁定语义 |
| 已进入 handler 的并发请求继续完成 | “立即失效”存在请求边界 | 明确保证为“事务提交后开始校验的新请求”；不以全局锁扩大范围 |

## 14. 待负责人确认的决策

以下均影响安全、API、兼容、部署或验收；v1 不代替负责人作默认决定。

### D1 — Access 权威状态策略

**事实：** 现有表和主键足够做每请求单查询；当前 JWT-only 会保留最长 15 分钟旧权限。

| 选项 | 方案 | 影响 |
|---|---|---|
| **A（推荐）** | 每个 protected request 做一次无缓存主键 JOIN | 无迁移、提交后下一请求生效；增加 DB 读与延迟 |
| B | 新增 auth security version + cache/失效通知 | 可降低 DB 读；需迁移、版本传播、缓存一致性和多实例失效，明显扩大范围 |
| C | 保持 JWT-only，仅缩短 TTL | 改动小，但不能满足“撤销/降权后立即失效”目标 |

**推荐 A。**

### D2 — 权威查询失败的 HTTP 语义

**事实：** 把 DB 故障也返回 401 会触发前端 refresh、清 session，并误导为凭据问题。

| 选项 | 方案 | 影响 |
|---|---|---|
| **A（推荐）** | 无效状态 → 401 `UNAUTHENTICATED`；基础设施错误 → 503 `AUTHENTICATION_UNAVAILABLE` | 语义准确、可重试、避免误登出；新增稳定错误码 |
| B | 所有失败统一 401 `UNAUTHENTICATED` | 对外最少变化；DB 故障会造成 refresh loop/误登出，观测困难 |

**推荐 A。**

### D3 — credential 临时锁是否阻断现有 Access Token

**事实：** `users.status` 有 LOCKED；密码失败还可仅设置 `user_credentials.locked_until`。当前登录会检查两者。

| 选项 | 方案 | 影响 |
|---|---|---|
| **A（推荐）** | 要求 user ACTIVE 且 `locked_until` 不在未来 | 登录与 Access 使用同一锁定语义；攻击者触发账户锁定时，已有 session 在窗口内也会被阻断 |
| B | 只检查 `users.status=ACTIVE`，忽略 `locked_until` | 降低锁定导致的已登录用户中断；临时 credential lock 只阻止新登录，不阻止旧 token |
| C | 只检查 session，不检查两种用户锁 | 不满足 HIGH-01 的用户状态目标 |

**推荐 A。**

### D4 — HIGH-02 日志字段策略

**事实：** 稳定 code/type/source/requestId 已足以关联诊断；任意错误原文无法可靠证明无 Secret。

| 选项 | 方案 | 影响 |
|---|---|---|
| **A（推荐）** | 彻底删除通用失败日志的 `error` 原文字段 | 泄漏面最小；依赖文本的日志查询需迁移 |
| B | 使用统一 sanitizer 后保留截断消息 | 保留部分诊断；规则永远可能漏掉未知敏感格式 |
| C | 仅 debug 环境保留原文 | 仍存在配置误用/集中采集泄漏，不建议 |

**推荐 A。**

### D5 — Must-change 白名单与错误

**事实：** change-password 必须放行；logout 是安全退出；GET me 是只读。401 会触发 refresh。

| 选项 | 方案 | 影响 |
|---|---|---|
| **A（推荐）** | 放行 change-password、logout、GET me；其他 protected route 返回 403 `PASSWORD_CHANGE_REQUIRED` | 可恢复、可退出、可展示账户；契约清晰 |
| B | 只放行 change-password、logout；GET me 也阻断 | 白名单更窄；当前 refresh response 已含 user，仍可实现 UI |
| C | 只做前端守卫，不做后端门禁 | 可被 API 客户端绕过，不满足 HIGH-03 |

**推荐 A。**

### D6 — 前端闭环与 Canvas

**事实：** 当前没有改密页面；仅加后端门禁无法完成 Chrome 验收。

| 选项 | 方案 | 影响 |
|---|---|---|
| **A（推荐）** | 新增独立 `/change-password` 最小页，复用 LoginView 样式；本 Issue 不另做 Canvas | 路由/状态清晰，改动有限，可直接 Chrome 验收 |
| B | 登录页内切换成改密步骤 | 文件更少；登录/已认证状态混在同页，刷新和路由守卫更复杂 |
| C | API-only，不补 UI | 后端安全但用户被困，无法满足已确认 Chrome 验收 |

**推荐 A。** 若负责人要求新的视觉/交互方向，则先补 Canvas 输入再修订方案。

## 15. 解除阻塞条件

负责人必须在当前 Issue 对 **本文 v1** 明确写出“确认 / 批准 / 按此实施”，并确认 D1～D6。可直接回复：

```text
确认 ZKL-63 技术方案 v1，D1=A、D2=A、D3=A、D4=A、D5=A、D6=A，按此实施。
```

收到明确批准后，Knower 才会生成 `docs/design/zkl-63-implementation-checklist.md`，只机械拆解已批准内容；在此之前不交 Forge。
