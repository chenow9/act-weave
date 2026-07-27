# ZKL-63 后端代码 Review 报告

| 项 | 值 |
|---|---|
| Issue | ZKL-63 / `2eb1a799-1056-4ea5-a306-c02d087fb72e` |
| Reviewer | Sentinel · 测试工程师 |
| 范围 | `backend/` Go/Gin/PostgreSQL（正确性、安全、数据/契约、并发与错误处理） |
| 方法 | 静态审阅关键路径 + 包级测试抽检 |
| 测试抽检 | `go test ./internal/authn ./internal/logging ./internal/transport/http` → PASS |
| 日期 | 2026-07-27 |
| 结论 | **有可修问题**；无已确认的 Critical 在野利用路径。AAP 数据面整体纪律好于管理面。 |

> Secret / 业务原文未写入本报告；位置以包路径 + 符号名描述。

---

## 总体评价

后端在 Secret 边界、AAP 鉴权再校验、SSRF egress、错误对外映射、Refresh Token 哈希存储等方面有明显安全工程投入。主要风险集中在**管理面 Access Token 信任模型**、**请求失败日志未做敏感过滤**，以及**强制改密等策略未在服务端闭环**。

---

## 可修问题（按严重度）

### HIGH-01 — 管理面 Access Token 不重验会话 / 用户状态 / 平台角色

| | |
|---|---|
| **位置** | `backend/internal/transport/http/router.go`：`AccessTokenAuthenticator.AuthenticateAccessToken`、`authenticationMiddleware`；`platformAdmin()` / `requirePlatformAdmin` 读 JWT `PlatformRole` |
| **说明** | 认证只解析 HS256 JWT（subject / sid / platformRole），不查 `auth_sessions.revoked_at`、用户 `DISABLED`/锁定、也不重读 DB 中的 `platform_role`。密码重置 / 禁用 / 降权会 `Revoke*Sessions`，但 **Access Token 在 TTL 内（默认 ≤15min）仍可用**，且可继续以 JWT 内嵌 `PLATFORM_ADMIN` 访问 `/admin/*` 与 agent-audit。对比：AAP 在每次请求上重读 Client/Grant/SecurityVersion。 |
| **建议** | 在 middleware 中至少校验 session 未撤销 + 用户 ACTIVE；平台角色与 `must_change_password` 以 DB 为准（或缩短 TTL + 黑名单/版本号）。角色变更后立即失效 access token。 |
| **类型** | 可修 |

### HIGH-02 — HTTP 失败日志记录完整 `err.Error()`，突破 AAP 字段白名单纪律

| | |
|---|---|
| **位置** | `backend/internal/transport/http/router.go`：`requestLoggingMiddleware` 写入 `"error", failure.err.Error()` |
| **说明** | AAP 侧 `logging.AAPAttrs` / `AAPError` 有 allowlist + `looksSensitive` 防护；管理面与通用失败路径直接把内部 error 字符串打进日志。若下游 `fmt.Errorf` 链含上游响应片段、路径、配置细节，可能进入集中日志。对外 `RespondError` 映射是安全的，**泄漏面在服务端日志**。 |
| **建议** | 失败日志只保留 `error_code` / 稳定错误类型 / 源文件行；对 `err.Error()` 做与 AAP 同等 redact，或仅在 debug 下输出且仍禁止 token/secret 子串。 |
| **类型** | 可修 |

### HIGH-03 — `MustChangePassword` 仅返回客户端，服务端未强制

| | |
|---|---|
| **位置** | `authn.Service` 登录/刷新返回标志；`authenticationMiddleware` 无检查；管理重置密码设 `MustChangePassword=true` 并撤销 refresh |
| **说明** | 管理员重置临时密码后，用户仍可用 access/refresh 调用除改密以外的业务 API，直到自愿改密。与 bootstrap「必须改密」产品预期不一致。 |
| **建议** | Middleware：当 credential.must_change_password 时仅放行 `change-password` / logout / me 只读（按产品定义）；其余返回稳定码（如 `PASSWORD_CHANGE_REQUIRED`）。 |
| **类型** | 可修 |

### MEDIUM-01 — Refresh Token 重用冲突不撤销会话族

| | |
|---|---|
| **位置** | `identity.Repository.RotateRefreshToken` CAS；`authn.Service.Refresh` 将 `ErrConflict` 映射为 `ErrRefreshRejected` |
| **说明** | 并发/重放时仅一方 CAS 成功，失败者拒绝，**不会 revoke 整条 session**。测试覆盖「一胜一冲突」正确性，但缺少 reuse detection → family revoke（常见防盗用模型）。 |
| **建议** | 检测已轮转 hash 的重用时撤销该 session（或 user 全部 refresh）；可观测指标 + 审计。 |
| **类型** | 可修（策略增强） |

### MEDIUM-02 — 入库 `config.yaml` 开发默认值对生产过于开放

| | |
|---|---|
| **位置** | `backend/config.yaml`：`agentAccess.feature` 全开、`agentAudit.debug: true`、固定 jwt/masterKey/bootstrap 口令、`generateIfMissing: true` |
| **说明** | 文件已标注 local-dev，但 `ValidateServer` 不拒绝「开发密钥 + AAP 全开 + audit debug」。误用该文件启动生产会暴露完整 AAP 面、写入/暴露推理与工具原文，并使用可预测密钥。 |
| **建议** | 生产 profile：`enabled=false` 或 allowlist；`agentAudit.debug=false`；禁止 `generateIfMissing`；对默认弱 secret 启动 fail-closed 或强告警。 |
| **类型** | 可修（运维/配置门禁） |

### MEDIUM-03 — Agent Audit 在 debug=false 时仍返回消息前 80 字

| | |
|---|---|
| **位置** | `agentaudit/timeline.go`：`presentText`；`GetTrace` → `BuildTimeline`；路由仅校验 JWT `PLATFORM_ADMIN` |
| **说明** | debug 关闭时 reasoning/tool raw 会 redact，但 chat 消息内容仍以「截断 80 字」返回。业务原文可能进入 PLATFORM_ADMIN API 响应与下游日志。跨 workspace 访问对平台管理员是设计行为。 |
| **建议** | debug=false 时消息改为占位或 hash；需要可读内容时显式二次确认/范围授权。 |
| **类型** | 可修（策略） |

### LOW-01 — Smart DAG session advisory lock 使用 FNV-64 键

| | |
|---|---|
| **位置** | `smartdag/session_lock.go`：`sessionAdvisoryKey` |
| **说明** | 非密码学哈希，理论上不同 session 键碰撞会导致互斥误伤（`ErrTurnInProgress`），不造成跨 session 数据读写。 |
| **建议** | 可用 pg 双 int advisory lock（workspace uuid + session uuid 拆分）消除碰撞面。 |
| **类型** | 可修 / 低优 |

---

## 观察项（当前可接受或需产品确认）

| ID | 位置 | 说明 |
|---|---|---|
| OBS-01 | AAP vs 管理面 | AAP 每次 Authorize 重读状态 + SecurityVersion；管理面 workspace 动作调 `AuthorizeWorkspace`，但 **平台角色与会话依赖 JWT**。长期可统一「权威状态来源」。 |
| OBS-02 | Secret 子系统 | AES-GCM + AAD（workspace/secret/version）、`WithActiveSecret` wipe、DTO 不导出明文 — 模式健康。 |
| OBS-03 | SSRF | `HTTPNetworkGuard` 校验 host/port/CIDR、重定向再校验并剥离敏感头 — 模式健康。 |
| OBS-04 | 对外错误 | `RespondError` / `mapAAPError` 稳定码 + 通用文案，不回传 Cause — 契约健康。 |
| OBS-05 | 幂等 | AAP command receipt `ON CONFLICT` + request_hash 比对 — 并发语义合理。 |
| OBS-06 | Refresh Cookie | HttpOnly + Secure + SameSite=Strict + Path 收窄 — 健康。纯 HTTP 本地需注意 Secure。 |
| OBS-07 | Source IP | 仅用 `RemoteAddr`，不盲信 `X-Forwarded-For` — 正确；反代需保证 peer 可信。 |
| OBS-08 | 进程内 OAuth token 缓存 | `HTTPSecretInjector` 内存缓存多实例不共享 — 正确性无妨，可观测延迟/限流差异。 |

---

## 追踪矩阵（审查关注点）

| 关注点 | 结论摘要 |
|---|---|
| 正确性 | 幂等、lock_version、advisory lock、协议 sequence 分配总体扎实 |
| 安全 | 管理面 JWT 信任窗口 + 日志 error 串 + 改密策略缺口为主要可修项 |
| 数据/契约 | 对外错误码稳定；AAP/outbound 映射刻意隔离 |
| 并发 | Refresh CAS、command receipt、protocol next_sequence、session try-lock 有设计 |
| Secret/原文 | Secret 包与 AAP 日志较好；通用 HTTP 日志与 audit 截断为残留风险 |

---

## 建议修复优先级

1. HIGH-02 日志 redact（改动面小、风险直接）
2. HIGH-01 session/用户状态重验（安全窗口）
3. HIGH-03 强制改密
4. MEDIUM-02 生产配置门禁
5. MEDIUM-01 refresh reuse revoke
6. MEDIUM-03 / LOW-01 按产品与容量排期

---

## 未覆盖

- 全量 `go test ./...` 与生产环境动态渗透
- 数据库迁移历史逐条审计
- 前端契约与 OpenAPI 全量差分
- 真实浏览器 E2E（本 Issue 范围为后端代码 Review）
