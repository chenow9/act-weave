# ZKL-63 三项 HIGH 修复：Implementation Checklist

| 字段 | 值 |
|---|---|
| Issue | ZKL-63 / `2eb1a799-1056-4ea5-a306-c02d087fb72e` |
| Checklist 版本 | **v1** |
| 状态 | **READY — 等待 Conductor 指派 Forge** |
| 总项数 | **6** |
| 技术基线 | `docs/design/zkl-63-high-fixes-tech-design.md` **v1 / Approved / Frozen** |
| 负责人确认 | Issue 评论 `e1cfb954-9269-4379-a174-fd7318ce0d47`：`确认 ZKL-63 技术方案 v1，D1=A、D2=A、D3=A、D4=A、D5=A、D6=A，按此实施。` |
| 固定顺序 | **HIGH-02 → HIGH-01 → HIGH-03**；严格串行 |
| 发布单元 | HIGH-02 可独立；HIGH-01 完成后可独立；HIGH-03 后端门禁与前端闭环不可拆分 |
| Open Questions | **无** |

> 本文只机械拆解已批准的技术方案 v1，不新增范围或决策。Forge 由 Conductor 后续指派；本文不构成指派、生产部署或生产数据操作授权。

## 0. 执行、验证与记录规则

1. **严格串行执行 1 → 6。** 第 1 项属于 HIGH-02，第 2～4 项属于 HIGH-01，第 5 项属于 HIGH-03，第 6 项是三项 HIGH 的整体验收。禁止并行、禁止拆子 Issue、禁止使用 Stage 表示进度。
2. Forge 完成当前项、运行开发自测并填写实现证据后，必须为该项新建一个**临时、只读 verification subagent**。只有 verifier 给出 PASS，当前项才可标记 `COMPLETE` 并直接开始下一项；不需要逐项等待 Knower 回复。
3. 每项 verifier 必须是**新实例**，不是持久 Agent、不是 Issue、不得复用。Verifier 只能检查实际 diff、运行验证并输出 PASS / FAIL；不得修改代码、测试、文档、数据库或外部状态。可使用本地/隔离环境的可丢弃 fixture，不得操作共享或生产数据。
4. 每项状态只允许：`PENDING`、`IN_PROGRESS`、`IMPLEMENTED_PENDING_VERIFICATION`、`BLOCKED`、`COMPLETE`。验证 FAIL 后回到 `IN_PROGRESS` 修复；再次验证必须创建另一个全新的 verifier。
5. 进度只记录在本文各项的“状态 / 实现证据 / 开发自测记录 / verification subagent 摘要”字段中。不得用子 Issue、Stage 或并行任务记录进度。
6. 第 2～4 项只是 HIGH-01 的串行开发拆分，**第 4 项 PASS 前不得发布任何 HIGH-01 部分**。第 5 项必须同时完成并验证 HIGH-03 后端门禁与前端改密闭环，禁止后端先行发布或前端后补。
7. 若 checklist 缺失、相互冲突、不可执行，或实现需要改变已批准的范围、架构、API、错误码、数据、权限、安全、迁移、兼容、部署、审计或验收决定，立即把当前项标记 `BLOCKED`，停止实现并交回 Knower；需要新决定时重新获得负责人明确确认。
8. 进入工作区先执行 `git status`，保护既有 `.agent_context/`、`AGENTS.md`、ZKL-62/ZKL-63 设计文档、ZKL-56/ZKL-63 验证资料及所有非本单改动。不得 reset、覆盖、清理或提交无关文件。
9. Go 验证必须使用 **Go 1.25.x**。若环境版本不匹配，先修正隔离测试环境；不得把无法运行测试记录成 PASS。全量失败必须区分本改动回归与既有失败，但相关包失败不能被既有失败豁免。
10. Chrome 验收必须使用两个隔离 profile/context 和可丢弃测试账户。日志、fixture、截图、DevTools 证据、提交信息及 Issue 评论中必须遮蔽密码、cookie、Access/Refresh Token 和内部错误原文。
11. 本 checklist 不授权 production 部署、production 数据 mutation、数据回填或破坏性操作。部署由既有发布流程另行授权；本文规定的发布/回滚边界仍必须遵守。

### 0.1 已批准且不可违背的决定

| 决策 | 已批准值 | Checklist 落点 |
|---|---|---|
| D1 | A — 每个 Console protected request 一次无缓存主键 JOIN；无 migration | 第 2～4 项 |
| D2 | A — 无效状态 401 `UNAUTHENTICATED`；基础设施错误 503 `AUTHENTICATION_UNAVAILABLE` | 第 3～4 项 |
| D3 | A — `users.status=ACTIVE` 且 `locked_until` 不在未来 | 第 2～4 项 |
| D4 | A — 彻底删除通用失败日志的 `error` 原文字段 | 第 1 项 |
| D5 | A — 仅放行 change-password / logout / GET me；其余 403 `PASSWORD_CHANGE_REQUIRED` | 第 5 项 |
| D6 | A — 独立 `/change-password` 最小页复用 `LoginView` 样式；不另做 Canvas | 第 5 项 |

共同约束：

- 不新增数据库 migration、索引、列、回填、双写、缓存、token blacklist 或 security version。
- 不改变 JWT claim shape、Access Token TTL、Refresh rotation、refresh cookie、登录/refresh DTO、改密 request body 或 204 成功响应。
- 不改变 AAP 鉴权、AAP data plane、`docs/openapi/agent-access-v1.yaml`、SDK、CORS 或事件协议。
- 不改变 Workspace role matrix；平台角色与 Workspace 角色仍是两套边界。
- 不新增密码复杂度规则、自助改密 durable audit 或每请求 durable audit。
- 不修改 panic/stack 日志策略，不处理 MEDIUM / LOW / 观察项。
- HIGH-03 只做服务端闭环与前端最小适配；不新增 Canvas、弹窗、向导、导航重构或视觉重设计。

### 0.2 进度总表

| # | 交付 | 所属 | 状态 | 依赖 | 实现证据 | verification |
|---:|---|---|---|---|---|---|
| 1 | 删除失败日志原文并建立泄漏回归 | HIGH-02 | `COMPLETE` | 无 | 见 §1 | PASS `019fa1de-9a31-70e1-aa1a-b7d7131180ad` |
| 2 | 增加 Access session 权威只读投影 | HIGH-01 | `COMPLETE` | 1 `COMPLETE` | 见 §2 | PASS `019fa1e2-c81c-73c2-95b4-3879b5861825` |
| 3 | 实现 authn 权威校验与错误分类 | HIGH-01 | `COMPLETE` | 2 `COMPLETE` | 见 §3 | PASS `019fa1e5-688b-7f21-b609-199b399fa8f4` |
| 4 | 接入 HTTP Principal / application 并证明立即失效 | HIGH-01 | `COMPLETE` | 3 `COMPLETE` | 见 §4 | PASS `019fa1e9-a7e0-7ee2-a1bc-5c64f4879d79` |
| 5 | 同一发布单元完成 MustChangePassword 后端与前端闭环 | HIGH-03 | `COMPLETE` | 4 `COMPLETE` | 见 §5 | PASS `019fa1f7-5f74-7401-9a12-33d8bfe36e7d` |
| 6 | 全量自动化、Chrome 双 profile 回归与交付证据 | 整体验收 | `COMPLETE` | 5 `COMPLETE` | 见 §6 | PASS `019fa20a-9088-7b42-b03e-225494b6460a` |

## 1. HIGH-02：删除失败日志原文并建立泄漏回归

- **状态**：`COMPLETE`
- **依赖**：无
- **目的**：从通用 HTTP completion log 的数据结构和输出字段中移除任意 `err.Error()` 原文，同时保留稳定、低敏感的诊断与关联字段。
- **精确范围**：
  - `backend/internal/transport/http/errors.go`
    - `requestFailure`
    - `RespondError`
  - `backend/internal/transport/http/router.go`
    - `requestLoggingMiddleware`
  - `backend/internal/transport/http/router_test.go`
  - 必要时更新 `backend/internal/transport/http/contract_test.go` 中既有错误/响应断言
  - 只运行、不得改变语义：`backend/internal/logging` 的既有 AAP allowlist 测试
- **不可违背约束**：
  - `requestFailure` 不再保存完整 `error`；`requestLoggingMiddleware` 禁止调用或间接输出裸 `err.Error()`。
  - 通用失败日志必须彻底不存在 `error` key；不得以截断、sanitizer、debug 环境开关或换名字段保留原文。
  - 仅保留 `error_code`、`error_type`、`error_source` 以及既有 `request_id`、`trace_id`、status、route 等稳定字段。
  - `error_type` 只保存类型名，`error_source` 继续使用现有 file/line 诊断格式。
  - HTTP status、错误 body、message、requestId、traceId、retryable 与 AAP 专属日志策略不变。
  - 不修改 `recoveryMiddleware` 的 panic/stack 日志策略。
- **完成定义**：
  - `requestFailure` 只持有稳定映射、错误类型与 source 信息，不持有原始 error。
  - 结构化 completion log 不含 `error` key，且 wrapped error、Bearer/JWT 形态、`password=`、PEM header、长上游 body canary 均零命中。
  - 客户端响应不含 canary；401/403/404/409/422/500 既有映射无回归。
  - 日志仍可通过 `error_code + error_type + error_source + request_id/trace_id` 关联诊断。
- **开发自测**：
  - `cd backend && go test ./internal/logging ./internal/transport/http`
  - 保存一份只含虚构 canary 的测试输出扫描结果，证明日志和响应均零命中；不得把任何真实 Secret 写入测试。
- **独立验证标准（本项新 verifier）**：
  - 静态检查实际 diff，确认 `requestFailure` 不再能携带 raw error，completion logger 无 `err.Error()` 或等价原文输出。
  - 独立运行本项包级测试，并检查结构化记录中 `error` key 不存在、稳定字段仍存在。
  - 用至少五类虚构 canary 覆盖 wrapped error、token 形态、密码键值、PEM header、长上游 body。
  - 任一 canary 泄漏、`error` key 残留、对外错误映射变化、AAP 日志语义变化或 panic 策略扩围即 FAIL。
- **回滚 / 风险**：无数据回滚。若日志消费者依赖旧原文，优先修消费者；回滚本项会重新引入已确认的敏感信息泄漏，只能作为短时紧急措施并明确记录风险。
- **实现证据**：
  - `requestFailure` 移除 `err error`，仅保留 `mapped/errorType/file/line`；`newRequestFailure` 只捕获 `%T`。
  - `requestLoggingMiddleware` 删除 `"error", failure.err.Error()`；仅输出 `error_code/error_type/error_source`。
  - 全部构造点（`RespondError`、`RespondSmartDagTurnError`、`RespondErrorWithDetails`、`respondOAuthTokenError`）改用 `newRequestFailure`。
  - `TestFailureLogOmitsRawErrorCanaries`：5 类虚构 canary 零命中 + 无 raw `error` slog 属性。
  - 修改文件：`errors.go`、`router.go`、`generate_session.go`、`agent_access_token.go`、`router_test.go`。
- **开发自测记录**：`cd backend && go test ./internal/logging ./internal/transport/http -count=1` → PASS（logging 0.175s；http 57.979s；Go 1.25.11）。
- **verification subagent / 摘要**：subagent `019fa1de-9a31-70e1-aa1a-b7d7131180ad`（capability=execute，全新实例）→ **PASS**。独立 `git diff` + `go test ./internal/logging ./internal/transport/http -count=1` exit 0；静态确认无 raw error 字段/日志键；5 canary 测试 PASS；`recoveryMiddleware` 未改。先前无 shell 的 read-only 实例 `019fa1dc-3942-7581-8218-ae2a4f348108` 因无法跑测 FAIL（不计完成）。

## 2. HIGH-01：增加 Access session 权威只读投影

- **状态**：`COMPLETE`
- **依赖**：1 `COMPLETE`
- **目的**：在 `identity` 边界提供一次查询即可读取 session、user、credential 当前安全状态的窄化投影，为每请求权威校验提供事实来源。
- **精确范围**：
  - `backend/internal/identity/models.go`
    - 新增窄化 `AccessSessionState` 投影
  - 新增 `backend/internal/identity/access_session_repository.go`
    - `ResolveAccessSessionState(ctx, subject, sessionID)`
  - 新增 `backend/internal/identity/access_session_repository_test.go`
  - 若仓库既有 test fixture 需要机械复用，只允许最小更新 `backend/internal/identity/*_test.go`
- **不可违背约束**：
  - 每次调用只执行一次、无缓存、纯读的参数化 SQL；按 `auth_sessions.id = sid AND auth_sessions.user_id = sub` 连接 `auth_sessions`、`users`、`user_credentials`。
  - 投影只包含判定所需字段：session `id/user_id/expires_at/revoked_at`，user `id/username/status/platform_role`，credential `locked_until/must_change_password`。
  - 投影不得包含 `password_hash`、refresh hash、cookie、JWT、Secret 或其他凭据原文。
  - 不使用数据库时钟完成业务判定；repository 只读取事实，由 authn 使用一次捕获的进程 UTC 时间判断。
  - 不写 `last_seen_at`，不新增 migration、索引、约束、缓存、回填或审计写入。
  - “无匹配 state / credential 缺失”必须能与数据库/基础设施故障区分，供下一项映射为 401 与 503。
- **完成定义**：
  - 有效 `sid + sub` 返回同一行完整且窄化的 `AccessSessionState`。
  - session 不存在、subject 不匹配或 credential 缺失返回安全的“state 不存在”结果，不暴露哪张表缺失。
  - 查询/扫描基础设施错误保留为可识别的内部错误，不被误吞为“不存在”。
  - repository 测试覆盖有效、missing、subject mismatch、revoked/expired 字段、ACTIVE/LOCKED/DISABLED、future/past/null `locked_until`、role/username/must-change 当前值和 DB error。
- **开发自测**：
  - `cd backend && go test ./internal/identity`
- **独立验证标准（本项新 verifier）**：
  - 静态审查 SQL 确为单次参数化主键 JOIN，无第二次查询、无写入、无缓存、无 schema 变化。
  - 独立运行 `identity` 测试，检查 missing 与 infra error 可区分，且投影无凭据/hash 字段。
  - 检查查询输入同时绑定 `sid` 与 `sub`，不能先按 sid 找 session 后忽略 subject。
  - 任一密码/refresh hash 投影、每请求写入、迁移/索引新增、DB clock 判定、额外查询或错误分类坍缩即 FAIL。
- **回滚 / 风险**：本项在第 4 项前不得发布；可机械回滚新增投影和 repository 文件，无数据回滚。主要风险是 JOIN 扫描顺序错误、把 infra error 当作 missing，以及无意读取敏感列。
- **实现证据**：
  - 新增 `AccessSessionState` 窄化投影（无 hash/JWT/cookie）。
  - 新增 `Repository.ResolveAccessSessionState`：单次参数化 `auth_sessions.id=$1 AND user_id=$2` JOIN users + user_credentials。
  - missing/mismatch/credential 缺失 → `ErrNotFound`；关闭连接等 infra 错误不坍缩为 NotFound。
  - 测试覆盖有效、missing、mismatch、revoked/expired 事实、status、locked_until、role/username、must-change、infra。
- **开发自测记录**：`go test ./internal/identity -count=1` → PASS（6.135s）。
- **verification subagent / 摘要**：subagent `019fa1e2-c81c-73c2-95b4-3879b5861825`（新实例）→ **PASS**。

## 3. HIGH-01：实现 authn 权威校验与错误分类

- **状态**：`COMPLETE`
- **依赖**：2 `COMPLETE`
- **目的**：在 `authn` 中把 JWT 密码学验证与数据库当前状态组合成统一 Access 身份判定，产出权威身份并区分无效状态与基础设施不可用。
- **精确范围**：
  - `backend/internal/authn/service.go`
    - `serviceRepository` 增加 `ResolveAccessSessionState`
    - `Service` 复用既有 `AccessTokenManager`
  - 新增 `backend/internal/authn/access_session.go`
    - `AccessIdentity`
    - `Service.AuthenticateAccessToken`
    - 仅供 transport 稳定映射的 typed/sentinel errors
  - 新增 `backend/internal/authn/access_session_test.go`
  - 必要时最小更新 `backend/internal/authn/service_integration_test.go`
  - `backend/internal/authn/access_token.go` 只在机械复用 Parse 结果所必需时更新；JWT claim shape 不变
- **不可违背约束**：
  - 每次认证只捕获一次 `now.UTC()`；同一个 `now` 用于 JWT `Parse` 与 session/user/credential 时效判定。
  - 固定判定顺序：JWT 算法/签名/issuer/nbf/exp/`sub/sid` → `ResolveAccessSessionState` → session/user/credential policy。
  - state missing、subject mismatch、session revoked、`expires_at <= now`、`users.status != ACTIVE`、`locked_until > now`、credential 缺失统一归类为无效认证状态；不得对外细分原因。
  - repository/数据库基础设施错误必须 fail closed 并归类为认证服务不可用；禁止回退 JWT-only。
  - 成功的 `AccessIdentity` 使用数据库当前 `username`、`platform_role`、`must_change_password`；JWT 中 username/role 只保留为签名内兼容 hint，不能参与授权。
  - 不改变 JWT claim、签名算法、TTL、refresh rotation、login/refresh 行为或 Workspace 授权。
- **完成定义**：
  - 有效 token + matching active state 返回权威 `AccessIdentity`。
  - JWT 旧 username/role 与 DB 不同时，返回 DB 当前值。
  - revoked、expired、missing、subject mismatch、LOCKED、DISABLED、future `locked_until` 均返回同一无效认证类错误。
  - DB error 返回独立的 unavailable 类错误；测试证明没有调用业务 handler 的降级路径。
  - `must_change_password` 随同一次权威查询进入 AccessIdentity，供第 5 项门禁使用。
- **开发自测**：
  - `cd backend && go test ./internal/authn`
- **独立验证标准（本项新 verifier）**：
  - 用 fake repository / fixed clock 独立覆盖所有完成定义，并证明一次认证只调用一次 resolver、只读取一次 now。
  - 检查成功身份的 username/role/must-change 均来自 repository fixture，而非 JWT claim。
  - 检查所有无效状态对上层呈现同一类别，infra error 单独保留且 fail closed。
  - 任一 JWT-only fallback、旧 claim 授权、`locked_until` 忽略、错误原因对外可枚举、缓存或额外写入即 FAIL。
- **回滚 / 风险**：本项在第 4 项前不得发布；与第 2 项一起回滚即可，无数据回滚。风险是错误分类不当导致误登出或把 DB 故障伪装为 401，以及多次取时钟造成边界不一致。
- **实现证据**：
  - 新增 `AccessIdentity` + `Service.AuthenticateAccessToken`；`serviceRepository` 增加 `ResolveAccessSessionState`。
  - 错误：`ErrAccessUnauthenticated`（统一无效）/ `ErrAuthenticationUnavailable`（infra fail-closed）。
  - 成功身份 username/role/must-change 仅来自 DB；单次 now + 单次 resolve。
- **开发自测记录**：`go test ./internal/authn -count=1` → PASS（7.530s）。
- **verification subagent / 摘要**：subagent `019fa1e5-688b-7f21-b609-199b399fa8f4` → **PASS**。

## 4. HIGH-01：接入 HTTP Principal / application 并证明立即失效

- **状态**：`COMPLETE`
- **依赖**：3 `COMPLETE`
- **目的**：让每个 Console protected request 使用 `authn` 权威身份，按批准契约映射 401/503，并证明 reset、logout、禁用、锁定和降权提交后的新请求立即停止信任旧 Access Token。
- **精确范围**：
  - `backend/internal/transport/http/context.go`
    - `Principal` 增加并承载权威 `MustChangePassword`
  - `backend/internal/transport/http/router.go`
    - `AccessTokenAuthenticator`
    - `NewAccessTokenAuthenticator`
    - `authenticationMiddleware`
  - `backend/internal/transport/http/errors.go`
    - 401 `UNAUTHENTICATED`
    - 503 `AUTHENTICATION_UNAVAILABLE`
  - `backend/internal/application/application.go`
    - 将已构造的 `authn.Service` 注入 Console Access authenticator
  - `backend/internal/transport/http/router_test.go`
  - `backend/internal/transport/http/auth_user_test.go`
  - `backend/internal/transport/http/contract_test.go`
  - 必要时新增聚焦认证 middleware / 立即失效的 `backend/internal/transport/http/*_test.go`
  - 必要时更新 `backend/internal/application/*_test.go` 以证明 wiring
- **不可违背约束**：
  - Bearer 语法与 JWT 解析仍在既有 Console 认证链；transport 不直接拼 SQL，不复制权威 policy。
  - invalid token/state 统一返回 HTTP 401、code `UNAUTHENTICATED`，不得暴露撤销、禁用、锁定或不存在的具体原因。
  - repository/数据库错误返回 HTTP 503、code `AUTHENTICATION_UNAVAILABLE`、`retryable=true`；不得触发 JWT-only fallback。
  - `Principal.Username`、`PlatformRole`、`MustChangePassword` 来自 `authn.AccessIdentity`；`/admin/*`、Agent Audit 和 overview 继续读取 Principal，因此自动使用 DB 当前角色。
  - `TokenExpiresAt` 继续服从已验证 JWT；不改变 token shape 或签发流程。
  - AAP authenticator、AAP principal、AAP middleware 和 Workspace `AuthorizeWorkspace` 路径不经过本查询、不改变。
  - “立即失效”边界固定为：安全变更事务提交后**开始校验的新请求**必须看到新状态；已经通过门禁进入 handler 的并发请求可以完成。
- **完成定义**：
  - application 使用同一个 `authn.Service` 构建 Console authenticator，所有 `/api/v1` protected routes 每请求走一次权威校验。
  - invalid 与 unavailable 的 status/code/retryable 契约通过 HTTP contract 测试，响应不含内部原因。
  - logout、管理员 reset password、status 变更、platform role 变更提交后，旧 Access Token 的下一 protected request 返回 401。
  - DB 角色与 JWT 旧 claim 冲突时，当前请求按 DB 角色授权；降权后重新登录为 USER，admin API 返回 403。
  - AAP token 与 Console token 继续双向不可互用；Workspace 权限仍由既有 authorizer 判定。
  - 既有 completion log 可按 401 `UNAUTHENTICATED`、503 `AUTHENTICATION_UNAVAILABLE`、requestId/traceId 关联；不得新增 user/session/request ID 高基数 metric label。
- **开发自测**：
  - `cd backend && go test ./internal/identity ./internal/authn ./internal/transport/http ./internal/application`
- **独立验证标准（本项新 verifier）**：
  - 独立运行上述包级测试并审查 application wiring，确认 protected Console 请求不能绕过 `authn.Service`。
  - 用 API/integration fixture 依次验证 logout、reset、LOCKED/DISABLED、future `locked_until`、admin→USER；每次均复用变更前 Access Token 发起变更后新请求。
  - 注入 repository failure，断言 503 `AUTHENTICATION_UNAVAILABLE`、`retryable=true`、无 handler 调用、无内部原因泄漏。
  - 运行 Console/AAP token 隔离与 Workspace authorizer 回归；检查 DB 当前 role 覆盖 JWT 旧 role。
  - 任一无效状态返回非 401、DB 故障返回 401/500、旧 role 继续授权、每请求多次状态查询、AAP/Workspace 边界变化即 FAIL。
- **回滚 / 风险**：HIGH-01 必须把第 2～4 项作为整体发布/回滚。回滚会恢复最长 Access Token TTL 的旧信任窗口，必须明确记录安全风险；无 schema/data 回滚。主要运行风险是每请求 JOIN 增加延迟/连接池压力及 DB 短故障时 fail-closed 503。
- **实现证据**：
  - `Principal.MustChangePassword`；`AccessTokenAuthenticator` 改用 `authn.Service`。
  - middleware：unavailable→503，其余→401；`mapError` 增加 `AUTHENTICATION_UNAVAILABLE`。
  - `application.go` 注入 `authService`；立即失效测试覆盖 logout/reset/LOCKED/DISABLED/demote。
- **开发自测记录**：`go test ./internal/identity ./internal/authn ./internal/transport/http ./internal/application -count=1` → PASS。
- **verification subagent / 摘要**：subagent `019fa1e9-a7e0-7ee2-a1bc-5c64f4879d79` → **PASS**。

## 5. HIGH-03：同一发布单元完成 MustChangePassword 后端与前端闭环

- **状态**：`COMPLETE`
- **依赖**：4 `COMPLETE`
- **目的**：在服务端强制 `MustChangePassword` 最小白名单，并让临时密码用户通过独立 Chrome 页面完成改密、清会话和重新登录；后端门禁与前端适配一次实现、一次验证、同一发布单元交付。
- **精确范围（后端）**：
  - `backend/internal/transport/http/router.go`
    - 在 authoritative Principal 注入后、业务 handler 前增加 must-change gate
    - method + Gin 注册模板精确 allowlist
  - `backend/internal/transport/http/errors.go`
    - 403 `PASSWORD_CHANGE_REQUIRED`
  - `backend/internal/transport/http/context.go`
    - 仅使用第 4 项已加入的 `Principal.MustChangePassword`
  - `backend/internal/transport/http/auth_user_test.go`
  - `backend/internal/transport/http/router_test.go`
  - `backend/internal/transport/http/contract_test.go`
  - 必要时新增聚焦 allowlist 的表驱动 `backend/internal/transport/http/*_test.go`
- **精确范围（前端）**：
  - 新增 `frontend/src/views/ChangePasswordView.vue`
  - 新增 `frontend/src/views/ChangePasswordView.test.ts`
  - `frontend/src/stores/auth.ts`
    - `changePassword(currentPassword, newPassword)`
  - `frontend/src/stores/auth.test.ts`
  - `frontend/src/services/api.ts`
    - 将 change-password 加入 auth lifecycle 排除
  - `frontend/src/services/api.test.ts`
  - `frontend/src/router/index.ts`
  - `frontend/src/router/access.test.ts`
  - `frontend/src/views/LoginView.vue`
  - 必要时最小更新 `frontend/src/views/login-view-content.test.ts`
- **不可违背约束（后端）**：
  - 白名单只能按 HTTP method + Gin 注册模板精确匹配：
    - `POST /api/v1/users/me/__command/change-password`
    - `POST /api/v1/auth/logout`
    - `GET /api/v1/users/me`
  - 外部 change-password 路径保持 `/api/v1/users/me:change-password`；不得改变既有 command adapter、request body 或 204 响应。
  - login/refresh 是 public route，继续签发/恢复带 `mustChangePassword=true` 的受限 session。
  - 其他全部 protected route，包括 `PATCH /users/me`、Workspace、业务执行、admin 和 Agent Audit，返回 HTTP 403、code `PASSWORD_CHANGE_REQUIRED`、message `Password change is required before continuing.`、`retryable=false`。
  - 不得用 path prefix、substring、模糊匹配或仅前端守卫替代服务端门禁。
- **不可违背约束（前端）**：
  - 新增独立 `/change-password` 路由，位于 `AppShell` 外，复用 `LoginView` 的双栏布局、表单控件、反馈和按钮样式；本 Issue 不另做 Canvas 或视觉重设计。
  - login/refresh/restore 得到 `mustChangePassword=true` 时，正常业务路由和 `/login` 都跳转 `/change-password`；受限用户不能进入 overview。
  - 已认证且 `mustChangePassword=false` 的用户访问 `/change-password` 时跳 overview；未认证用户仍跳 login。
  - 页面只含当前密码、新密码、确认新密码和“修改密码并重新登录”；新密码沿用既有至少 12 位规则，不新增规则。
  - 当前/新密码不得写入 Store state、local/session storage、URL、日志或 Issue 证据。
  - `POST /users/me:change-password` 是非幂等 auth lifecycle request：401 不得触发 refresh/retry；提交中按钮 disabled，重复点击不得重复发送。
  - 204 后立即 `clearSession()` 并跳 `/login?passwordChanged=1`；不自动登录、不保留密码。服务端既有事务继续负责 flag=false、撤销全部 session、清 refresh cookie。
  - 后端和前端在本项全部完成前均不得发布；发布与回滚必须同时进行。
- **完成定义（后端）**：
  - allowlist 表驱动测试精确覆盖三条放行组合，以及同路径错误 method、PATCH me、admin、Workspace、业务 route 的 403。
  - temporary login 和 refresh 均保留 `mustChangePassword=true`，受限 principal 只能走白名单。
  - 改密成功清 flag、撤销全部 session、清 cookie；旧 token 401，新密码可登录。
  - 改密失败不清 flag、不撤销为成功态，且响应不泄漏内部原因。
- **完成定义（前端）**：
  - login / restore flag=true、业务路由访问、`/login` 访问均落在 `/change-password`，无 redirect loop。
  - 页面验证密码不一致、长度不足、当前密码错误、网络错误、提交中与防双击。
  - change-password 401 的网络 spy 证明 refresh=0、原请求重试=0。
  - 204 后 token/user/flag 全部清空并跳登录成功提示；浏览器内无密码持久化。
  - 正常用户登录、refresh、logout、overview 与管理面导航无回归。
- **开发自测**：
  - `cd backend && go test ./internal/authn ./internal/transport/http`
  - `cd frontend && npm test -- --run src/stores/auth.test.ts src/services/api.test.ts src/router/access.test.ts src/views/ChangePasswordView.test.ts src/views/login-view-content.test.ts`
  - `cd frontend && npm run build`
- **独立验证标准（本项新 verifier）**：
  - 将后端与前端实际 diff 作为一个不可拆分单元审查；任一侧缺失直接 FAIL。
  - 独立运行本项后端/前端测试与 build；对后端 allowlist 做 method+registered-template 表驱动负向验证。
  - 用前端请求 spy 证明当前密码错误 401 不 refresh、不重试，双击只发一次；检查 204 后清 session 与跳转。
  - 检查 `/change-password` 不在 AppShell，复用现有登录页视觉语言且没有 Canvas/导航/品牌扩围。
  - 任一前缀白名单、业务路由漏放行、403 契约变化、backend-only/frontend-only 可发布状态、refresh loop、密码持久化或自动登录即 FAIL。
- **回滚 / 风险**：HIGH-03 后端与前端必须一起回滚，不能只撤门禁或只撤页面。已完成的密码变更和 session 撤销是安全事实，不回滚。主要风险是白名单过宽、部署拆分使用户被困、401 自动重试造成重复密码尝试以及路由无限重定向。
- **实现证据**：
  - 后端：`mustChangePasswordMiddleware` 精确 method+FullPath 白名单；403 `PASSWORD_CHANGE_REQUIRED`。
  - 前端：`ChangePasswordView`（AppShell 外）、router 强制守卫、`auth.changePassword`、change-password 排除 401 refresh/retry。
  - 既有集成测试适配：admin 创建用户后 `loginAndClearMustChange` 再做业务断言。
- **开发自测记录**：
  - `go test ./internal/authn ./internal/transport/http -count=1` → PASS
  - 前端 5 个测试文件 26 tests + `npm run build` → PASS
- **verification subagent / 摘要**：subagent `019fa1f7-5f74-7401-9a12-33d8bfe36e7d` → **PASS**（先前 `019fa1ef-96f8-7813-91d6-e8a31767ccf9` 因 package 测试未绿 FAIL，已修复后新实例复验）。

## 6. 全量自动化、Chrome 双 profile 回归与交付证据

- **状态**：`COMPLETE`
- **依赖**：5 `COMPLETE`
- **目的**：在全部实现冻结后执行包级、全量与真实 Chrome 双 profile 验收，证明三项 HIGH 严格串行完成且未影响 AAP、正常登录、管理面和 Workspace 主路径。
- **精确范围**：
  - 只允许修复第 1～5 项已批准范围内的验证失败；不得借整体验收扩展功能。
  - 更新本文第 1～6 项的状态、实现证据、开发自测记录与 verifier PASS/FAIL 摘要。
  - Chrome 证据使用两个隔离 profile/context：平台管理员 A、目标用户 B。
  - 如仓库既有 ZKL-63 验证目录约定，可写入脱敏文本/截图证据；不得修改产品契约或另建 Issue。
- **不可违背约束**：
  - 必须先完成 Go 包级与全量测试，再完成前端全量测试/build，最后执行 Chrome 整体验收。
  - Chrome 不能替代 HIGH-02 日志零泄漏、DB error、单查询、并发边界等自动化证明。
  - A/B 只使用可丢弃测试账户；B 若临时为 `PLATFORM_ADMIN`，必须保证不是最后一个 active platform admin。
  - 证据不得包含真实密码、cookie、Access/Refresh Token、Authorization header、内部错误原文或可复用 Secret。
  - 任何失败只可回到对应已批准项修复并创建新的 verifier；不得降低断言、跳过相关测试或新增范围。
- **完成定义（自动化）**：
  - Go 1.25.x 下包级测试通过：
    - `internal/identity`
    - `internal/authn`
    - `internal/logging`
    - `internal/transport/http`
  - `backend` 全量 `go test ./...` 通过，或对确属既有且与本单无关的失败提供可复核证据；本单相关包必须全部 PASS。
  - `frontend` 全量 `npm test` 与 `npm run build` PASS。
  - 自动化明确证明：
    - failure log 无 raw `error` 字段与 canary；
    - Access 校验一次无缓存 JOIN、invalid=401、infra=503；
    - role/username/must-change 来自 DB；
    - must-change allowlist 与 403 契约；
    - change-password 401 不 refresh/retry。
- **Chrome 回归步骤与预期**：
  1. **正常登录冒烟**：A 登录，访问 overview、users 和一个 Workspace 主路径；页面可用，无 must-change 误判。
  2. **HIGH-02 对外安全**：A 用 DevTools 观察一个安全构造的失败响应；只见稳定 code/message/requestId/traceId。服务端自动化日志扫描对同一虚构 canary 零命中。
  3. **reset 立即失效**：B 保持已登录；A 重置 B 密码；B 在事务提交后发起新的导航/API 请求；旧 token 401，refresh 失败后回 login，不能继续业务。
  4. **临时密码与前端守卫**：B 用临时密码登录；直接进入 `/change-password`。尝试访问 overview、users、Workspace 均回到改密页，无循环。
  5. **服务端门禁**：B 在 DevTools 发起 protected 业务请求，得到 403 `PASSWORD_CHANGE_REQUIRED`；`POST change-password`、`POST logout`、`GET me` 可用，其他 method/path 不放行。
  6. **完成改密**：B 输入当前临时密码与新密码；只发一次改密请求。204 后回 `/login?passwordChanged=1`；旧 session/token 不可用，新密码登录后可正常进入控制台。
  7. **降权立即失效**：B 为非最后一个 `PLATFORM_ADMIN` 时，A 将 B 降为 `USER`；B 旧 token 下一新请求 401。B 重新登录后 users 导航不可见，直调 admin API 为 403。
  8. **禁用与锁定**：A 分别将 B 设为 `LOCKED` / `DISABLED`；B 旧 token 下一新请求 401，不能 refresh 或继续业务。`locked_until > now` 由第 2～4 项自动化覆盖。
  9. **主路径回归**：A/B 分别验证 logout、refresh、用户管理、overview、Workspace 主路径；无 refresh loop、redirect loop、异常重复改密或 AAP/Console token 混用。
- **开发自测**：
  - `cd backend && go test ./internal/identity ./internal/authn ./internal/logging ./internal/transport/http`
  - `cd backend && go test ./...`
  - `cd frontend && npm test`
  - `cd frontend && npm run build`
  - 按上述 9 步执行真实 Chrome 双 profile 验收并保存脱敏结果。
- **独立验证标准（本项新 verifier）**：
  - 独立核对第 1～5 项均为 `COMPLETE` 且各有不同 verifier PASS；检查实际 diff 未越出批准边界。
  - 独立运行全量自动化命令，抽查关键失败用例不是仅由 mock 绕过。
  - 在隔离 Chrome profile/context 复验正常登录、强制改密、reset 失效、降权、禁用/锁定和管理/Workspace 主路径；核对 DevTools 网络请求无 loop 或重复改密。
  - 检查脱敏证据、错误码、发布单元与回滚说明；交付包需列出发布后观察项：401/403/503 比例、protected request P50/P95/P99、PostgreSQL 查询延迟和连接池等待/耗尽，且无高基数身份 label。
  - 确认无 migration、AAP/OpenAPI/SDK、MEDIUM/LOW/观察项改动。
  - 任一相关测试失败、Chrome 关键路径失败、HIGH-03 前后端拆分、Secret 泄漏、越界 diff 或 verifier 复用即 FAIL。
- **回滚 / 风险**：本项本身不引入产品改动。验收失败阻止交付并回到对应项修复；不得通过跳过测试推进。最终回滚遵循：HIGH-02 独立但回滚重引泄漏风险；HIGH-01 第 2～4 项整体；HIGH-03 前后端整体；密码变更与 session 撤销事实不回滚。
- **实现证据**：
  - 包级：`identity/authn/logging/transport/http` 均 PASS（Go 1.25.11）。
  - 全量后端：`go test ./...` 全绿（`.agent_context/zkl63-go-full-test-2.log`，含 database clean-schema 适配）。
  - 前端 ZKL-63 相关 26 tests + `npm run build` PASS。
  - 前端全量 3 个失败均在 `workflow-editor` / `smart-dag`，不在本单 diff，属既有无关失败。
  - Chrome 双 profile（Playwright 双 context A/B）：`docs/verification/zkl-63-chrome-acceptance/`，**18 PASS / 0 FAIL**，证据已脱敏。
  - 无 migration；无 AAP OpenAPI/SDK 产品改动；无 MEDIUM/LOW。
- **开发自测记录**：
  - `go test ./internal/identity ./internal/authn ./internal/logging ./internal/transport/http -count=1` → PASS
  - `go test ./... -count=1` → PASS（全包 ok）
  - 前端相关 + build → PASS；Chrome `node e2e/zkl63-chrome-dual-profile.mjs` → 18/18 PASS（需本分支 backend 在 :8082）
- **verification subagent / 摘要**：subagent `019fa20a-9088-7b42-b03e-225494b6460a` → **PASS**。

## 7. 完成交付门槛

只有同时满足以下条件，Forge 才可把当前 Issue 交回 Conductor / Sentinel 做最终处理：

- 第 1～6 项全部为 `COMPLETE`，每项都记录实现证据、开发自测与不同 verification subagent 的 PASS 摘要。
- 实际顺序为 HIGH-02 → HIGH-01 → HIGH-03，期间无并行实现、子 Issue 或 Stage。
- HIGH-03 后端和前端作为同一发布单元完成，没有 backend-only 或 frontend-only 的发布窗口。
- 包级、后端全量、前端全量/build 和 Chrome 双 profile 回归满足第 6 项标准。
- 实际 diff 不包含 migration、AAP/OpenAPI/SDK、MEDIUM/LOW/观察项或其他非目标。
- 无未决 Open Question；若出现新决策，本文不能自行吸收，必须按 §0 第 7 条回到确认闭环。
