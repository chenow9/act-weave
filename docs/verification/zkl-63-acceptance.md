# ZKL-63 最终验收报告（Sentinel）

| 项 | 值 |
|---|---|
| Issue | ZKL-63 / `2eb1a799-1056-4ea5-a306-c02d087fb72e` |
| 验收人 | Sentinel · 测试工程师 |
| 状态 | **PASS** |
| 基线 | 技术方案 v1（D1–D6=A）+ checklist v1（6/6 COMPLETE） |
| 环境 | Go 1.25.11；backend 本分支二进制 `:8082`；frontend Vite `:5174` |
| 日期 | 2026-07-27 |
| 证据 | `docs/verification/zkl-63-chrome-acceptance-sentinel/`（18 PASS / 0 FAIL，脱敏） |

> Secret、密码、cookie、Access/Refresh Token、Authorization 与内部 error 原文未写入本报告。

---

## 1. 验收范围与输入

| 输入 | 核对 |
|---|---|
| 技术方案 v1 | D1–D6 = A；HIGH-02 → HIGH-01 → HIGH-03 串行 |
| Checklist v1 | 1–6 均为 `COMPLETE` |
| Verifier ID 互异 | 见下表（6 个不同 UUID） |
| 非范围 | MEDIUM/LOW/观察项、AAP OpenAPI/SDK、migration |

### Checklist / Verifier 矩阵

| # | 交付 | Verifier ID | Checklist | Sentinel |
|---:|---|---|---|---|
| 1 | HIGH-02 删除失败日志原文 | `019fa1de-9a31-70e1-aa1a-b7d7131180ad` | PASS | 静态 + 包测复验 PASS |
| 2 | HIGH-01 Access session 投影 | `019fa1e2-c81c-73c2-95b4-3879b5861825` | PASS | 静态 SQL/投影 PASS |
| 3 | HIGH-01 authn 权威校验 | `019fa1e5-688b-7f21-b609-199b399fa8f4` | PASS | 静态 + authn 测 PASS |
| 4 | HIGH-01 HTTP/application 接入 | `019fa1e9-a7e0-7ee2-a1bc-5c64f4879d79` | PASS | 静态 + invalidation 测 PASS |
| 5 | HIGH-03 前后端同一发布单元 | `019fa1f7-5f74-7401-9a12-33d8bfe36e7d` | PASS | 后端门禁 + 前端守卫/改密 PASS |
| 6 | 全量自动化 + Chrome | `019fa20a-9088-7b42-b03e-225494b6460a` | PASS | 独立 Chrome 18/18 PASS |

---

## 2. 需求 → 实现 → 测试 追踪

| 需求（Review / D） | 实现要点 | 验收证据 |
|---|---|---|
| HIGH-02 / D4：删除失败日志 raw error | `requestFailure` 无 `err`；日志仅 `error_code/type/source` | 静态 `router.go`/`errors.go`；`TestFailureLogOmitsRawErrorCanaries`；Chrome case 2 对外安全错误体 |
| HIGH-01 / D1：每请求无缓存 JOIN | `ResolveAccessSessionState` 单次 `sid+sub` JOIN | 静态 repository；identity/authn 包测 |
| HIGH-01 / D2：401 / 503 契约 | invalid→401 `UNAUTHENTICATED`；infra→503 `AUTHENTICATION_UNAVAILABLE` retryable | mapError；access_auth_invalidation 测；API smoke 401 |
| HIGH-01 / D3：ACTIVE + locked_until | `validateAccessSessionState` | authn 测 + Chrome lock/disable |
| HIGH-01 立即失效 | logout/reset/demote/lock/disable 后旧 token 401 | HTTP invalidation 测 + Chrome 3/7/8 |
| HIGH-03 / D5：must-change 白名单 | method+FullPath 精确三条；其余 403 `PASSWORD_CHANGE_REQUIRED` | must_change_password 测 + Chrome 5 |
| HIGH-03 / D6：独立改密页 | `/change-password` 在 AppShell 外；改密后清会话重登 | 前端路由/store 测 + Chrome 4/6 |

---

## 3. 静态抽检（越界与契约）

| 检查 | 结果 |
|---|---|
| 无新 migration / schema | **PASS**（`migrations/` 无本单 diff） |
| 无 AAP OpenAPI / SDK 产品改动 | **PASS** |
| 无 MEDIUM/LOW/观察项实现 | **PASS**（范围限定三项 HIGH） |
| JWT claim shape / TTL / refresh cookie | **PASS**（未改 claim；仅权威身份覆盖 role/username） |
| AAP middleware 与 Console 认证隔离 | **PASS**（Console 走 `authn.Service`；AAP 路径独立） |
| HIGH-03 前后端同单元 | **PASS**（门禁 + ChangePasswordView + 路由守卫同在） |

关键实现核对：

- **HIGH-02**：`newRequestFailure` 只存 `%T`；completion log 无 `"error"` key。
- **HIGH-01**：`AuthenticateAccessToken` 单次 `now` + 单次 resolve；身份字段取自 DB；infra wrap 带 `%w` 供 `errors.Is`。
- **HIGH-03**：allowlist 仅  
  `POST …/change-password`、`POST …/logout`、`GET …/users/me`；  
  前端 `isAuthLifecycleRequest` 排除 change-password 的 401 refresh/retry。

---

## 4. 命令与真实结果

| 命令 | 结果 |
|---|---|
| `go test ./internal/identity ./internal/authn ./internal/logging ./internal/transport/http -count=1` | **PASS**（Go 1.25.11） |
| `go test ./internal/transport/http -run 'TestFailureLogOmitsRawErrorCanaries\|TestMustChange\|TestAccessAuth\|…'` | **PASS** |
| 前端 5 文件 / 26 tests（auth/api/router/ChangePassword/login-view） | **PASS** |
| 本分支 backend 重建并监听 `:8082` | health 200；login+me 200；坏 token 401 `UNAUTHENTICATED` |
| Chrome 脚本 `frontend/e2e/zkl63-chrome-dual-profile.mjs`（Sentinel 独立证据目录） | **18 PASS / 0 FAIL** |

说明：Forge 声称全量 `go test ./...` PASS；本轮 Sentinel 对相关包与目标用例做了独立复跑。前端全量中 workflow-editor / smart-dag 既有失败不在本单 diff，不阻断本验收。

---

## 5. Chrome 路径（双 profile）

证据目录：`docs/verification/zkl-63-chrome-acceptance-sentinel/`

| Case | 结果 | 要点 |
|---|---|---|
| 1_normal_login_smoke | PASS | admin → /overview |
| 2_high02_client_safe_error | PASS | 401 `UNAUTHENTICATED` 稳定体 |
| 4 / 4b 临时密码强制改密 | PASS | 进 `/change-password`；overview 回改密页 |
| 5 服务端门禁 | PASS | 业务 403 `PASSWORD_CHANGE_REQUIRED`；GET me 200 |
| 6 改密闭环 | PASS | 单次 POST；`passwordChanged=1`；旧 token 401；新密码可进 overview |
| 3 reset 立即失效 | PASS | reset 204 → me 401 |
| 7 降权立即失效 | PASS | demote 后 admin 401；USER 重登 admin 403 |
| 8 锁定/禁用立即失效 | PASS | lock/disable 后 me 401 |
| 9 管理面/Workspace 主路径 | PASS | users + workspaces 可用 |

---

## 6. 缺陷

**无阻断缺陷。**

---

## 7. 未覆盖 / 风险（不阻断）

| 项 | 说明 |
|---|---|
| 全量 `go test ./...` | 本轮未完整重跑；相关包已绿 |
| 前端全量 npm test | 既有 workflow-editor / smart-dag 失败与本单无关 |
| MEDIUM/LOW/观察项 | 明确非本单范围 |
| 发布后观察 | 401/403/503 比例；protected P50/P95/P99；JOIN 延迟与连接池；禁止 user/session ID 作 metric label |
| 发布单元 | HIGH-02 可独立；HIGH-01 项 2–4 整体；HIGH-03 前后端同发同回滚 |

---

## 8. 结论

**PASS** — 三项 HIGH 修复符合批准方案 v1 与 checklist v1；静态范围、相关自动化与独立 Chrome 双 profile 均通过。Issue 置为 **`done`**。
