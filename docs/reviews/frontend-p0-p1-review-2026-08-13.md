# 前端 P0 / P1 审查报告

- 审查日期：2026-08-13
- 审查分支：`codex/review-frontend-p0-p1`
- 审查基线：`3952634`（当前本地 `main`，比 `origin/main` 多 1 个提交）
- 主要范围：`frontend/`；为验证认证契约，交叉核对了 `backend/internal/authn`、`backend/internal/identity` 与 `backend/internal/transport/http`
- 结论：**P0 0 项，P1 2 项；两项 P1 均已修复并通过回归验证**

## 定级口径

- **P0**：可直接造成大范围不可逆数据损失、未授权远程代码执行、核心生产面完全不可用，且没有现实绕行方案。
- **P1**：认证或授权边界失效、会话安全承诺失效、关键业务路径在正常条件下稳定失败，或存在高概率的数据完整性/跨租户风险。

## 汇总

| 编号     | 级别 | 状态   | 标题                                                                 | 主要影响                                                       |
| -------- | ---- | ------ | -------------------------------------------------------------------- | -------------------------------------------------------------- |
| FE-P1-01 | P1   | 已修复 | 三套 Refresh Token 刷新路径未共享 singleflight，与一次性轮换契约冲突 | 并发 401 可随机清除有效 Cookie、清空登录态或让后续刷新永久失败 |
| FE-P1-02 | P1   | 已修复 | 退出失败被吞掉并立即展示登录页，7 天 Refresh 会话仍有效              | 用户以为已退出，重新加载或恢复网络后却可被静默重新登录         |

未发现达到 P0 门槛的问题。

## FE-P1-01：三套 Refresh Token 刷新路径未共享 singleflight（已修复）

### 证据

Axios 通用客户端只在 `frontend/src/services/api.ts:131` 和 `frontend/src/services/api.ts:233` 内部维护 `refreshInFlight`。以下两个原生 `fetch` 路径绕过该协调器，直接再次调用同一个 `/auth/refresh`：

- Chat Run SSE：`frontend/src/stores/chat.ts:354`、`frontend/src/stores/chat.ts:634`
- LLM Job SSE：`frontend/src/services/llm-job-sse.ts:106`

这意味着同一个页面内，普通 API、Chat SSE、Prompt Enhancement/Smart DAG SSE 可同时发起刷新；多个浏览器标签页也天然各自拥有独立的内存 singleflight。

后端契约明确不是“并发刷新都成功”：

- `backend/internal/authn/service.go:418` 每次刷新都会生成替换 Token，并在 `:449` 对旧 Token Hash 做 compare-and-swap。
- `backend/internal/identity/session_repository_test.go:182` 明确断言两个并发轮换只能有一个成功，另一个冲突。
- `backend/internal/transport/http/auth_user.go:124` 在任何刷新失败时都会执行 `clearRefreshCookie`；成功响应则在 `:130` 写入新 Cookie。
- Axios 刷新失败还会在 `frontend/src/services/api.ts:242` 清除 Access Token 并触发 `onExpired`；两个 SSE 路径则没有共享该会话状态机。

因此，胜者写入新 Cookie、败者清 Cookie 的响应先后顺序会决定浏览器最终状态。即使胜者的 Access Token 暂时可用，Refresh Cookie 也可能已被败者删除，下一次过期后会话必然终止。

### 复现路径

1. 登录并启动一个仍在运行的 Chat Run，保持事件流连接。
2. 等待 Access Token 过期，或在测试环境让普通 API 与事件流同时返回 401。
3. 同时触发一个普通 Axios 请求，以及 Chat SSE 重连或 LLM Job SSE 请求。
4. 观察网络面板出现两个 `POST /api/v1/auth/refresh`。
5. 后端只接受其中一个旧 Token 轮换；另一个返回 401 并发送删除 Cookie 的 `Set-Cookie`。
6. 根据响应顺序，前端会立即清空登录态，或保留一枚无法继续刷新的 Access Token；当前流/生成任务也可能失败。

### 影响

- 正在运行的 Chat、Prompt Enhancement、Smart DAG 生成任务可能在 Token 过期点随机中断。
- 用户会无规律地被退出，或在下一次 Access Token 过期时被退出。
- 多标签页会放大竞态；仅把两个 SSE 改为调用当前模块私有函数，还不足以解决跨标签并发。

### 修复实现与验收

1. `frontend/src/services/api.ts` 导出唯一的 `refreshAuthSession()`；Axios、Chat SSE、LLM Job SSE 和会话恢复全部复用该入口，由同一个模块级 Promise 合并同标签页请求。
2. `refreshAuthSession()` 在支持 Web Locks 的浏览器中使用同源独占锁，使多个标签页按顺序读取最新 Cookie 并轮换，避免并发复用旧 Token。
3. 统一刷新成功后的 Access Token 与 Auth Store 状态同步；刷新失败统一触发会话过期处理。
4. 后端刷新失败响应不再清除 Refresh Cookie，避免并发败者的响应删除胜者刚写入的新 Cookie；回放旧 Token 仍返回拒绝。
5. 已增加 Axios/fetch 共用 singleflight、Chat SSE、LLM Job SSE 以及后端旧 Token 回放不清 Cookie 的回归测试。

## FE-P1-02：退出失败仍被当作成功（已修复）

### 证据

- `frontend/src/stores/auth.ts:114` 仅在内存中有 Access Token 时发起服务端注销，随后在等待结果前立即执行 `clearSession()`。
- 同一方法在 `frontend/src/stores/auth.ts:120` 吞掉所有注销错误，并把服务端注销降级成 best effort。
- `frontend/src/components/AppShell.vue:262` 不等待 `auth.logout()`，立即跳转到登录页，因此 UI 始终表现为“退出成功”。
- 注释声称 Refresh Cookie “short-lived”，但 `backend/internal/application/application.go:307` 的生产配置是 7 天，且 `backend/internal/authn/service.go:187` 强制最短 TTL 也是 7 天。
- Refresh Cookie 为 HttpOnly，前端 JavaScript 无法自行删除；只有 `backend/internal/transport/http/auth_user.go:134` 成功撤销服务端会话后才会在 `:144` 清 Cookie。

### 复现路径

1. 正常登录。
2. 在浏览器网络面板阻断 `POST /api/v1/auth/logout`，或让请求超时/返回 5xx。
3. 点击“退出登录”；页面立即进入登录页且没有任何失败提示。
4. 恢复网络并重新加载页面。
5. 路由初始化调用 `restoreSession()`；仍有效的 HttpOnly Refresh Cookie 可再次换取 Access Token，用户被静默重新登录。

### 影响

- 在共享电脑、值班终端或离职/交接场景中，用户明确执行退出后，会话最多仍可继续有效 7 天。
- UI 给出错误的安全确认，用户没有机会知道服务端会话未撤销。
- 当本地 Token 已先被其他错误清除时，`this.token ? ... : Promise.resolve()` 甚至不会尝试用仍存在的 Refresh Cookie 注销。

### 修复实现与验收

1. Auth Store 无论内存中是否仍有 Access Token，都会调用 Cookie 注销端点；只有服务端确认成功后才清空本地会话。
2. `/auth/logout` 改为公开、Cookie 驱动且幂等：无 Cookie 或 Cookie 已失效均返回 204；真实存储/服务故障仍返回错误，前端不得伪装成功。
3. AppShell 等待注销完成后才导航到登录页；失败时保留当前认证 UI，显示持久错误和“重试退出”操作。
4. 自动 401 清理路径只清理本地状态，不会误发注销请求；显式用户退出仍执行完整服务端撤销。
5. 已增加 Auth Store、AppShell、后端注销契约，以及“首次注销失败、留在应用内、显式重试成功”的 Playwright E2E。

## 验证结果

前端命令使用仓库声明的 Node.js `22.22.3` / npm `10.9.8`；后端命令使用 Go `1.25.11`。

| 检查                                                                     | 结果                                                                                           |
| ------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------- |
| `npm run lint`                                                           | 通过                                                                                           |
| `npm run type-check`                                                     | 通过                                                                                           |
| `npm test -- --run`                                                      | 通过：77 个测试文件、604 个测试                                                                |
| `npm run build`                                                          | 通过                                                                                           |
| `npm run bundle:check`                                                   | 通过                                                                                           |
| `npm run e2e`                                                            | 通过：Chromium 11/11；测试期间有 2 条被代理到未启动后端的后台 `/agents` 请求，但未导致用例失败 |
| `go test ./internal/transport/http ./internal/authn ./internal/identity` | 通过：HTTP 认证契约、Token 轮换及身份服务相关套件                                              |
| `npm audit --omit=dev --registry=https://registry.npmjs.org`             | 未通过：0 critical、2 high、2 moderate、1 low                                                  |

### 依赖审计说明

依赖告警已核对，但没有在本次报告中提升为 P0/P1：

- `markdown-it@14.1.0` 的已知 linkify ReDoS 在当前配置中由 `linkify: false` 关闭（`frontend/src/utils/markdown.ts:6`）；相关修复版本为 14.1.1，仍应尽快升级。[GHSA-38c4-r59v-3vqw](https://github.com/advisories/GHSA-38c4-r59v-3vqw)
- `dompurify@3.2.4` 命中多条告警。当前管线先用 `html: false` 的 Markdown 解析，再使用不含 raw-text wrapper 的标签白名单，最后把结果放入普通 `div`，与已公开的 raw-text wrapper / re-contextualization 前提不一致；本次未复现 XSS，但安全边界依赖已知受影响版本，仍应单独升级并补充回归 payload。[GHSA-v2wj-7wpq-c8vv](https://github.com/advisories/GHSA-v2wj-7wpq-c8vv)、[GHSA-h8r8-wccr-v5f2](https://github.com/advisories/GHSA-h8r8-wccr-v5f2)
- `postcss` 与 `nanoid` 告警来自 Vite 构建链；当前未发现生产运行时可达路径，应随构建依赖升级处理。

## 审查边界

- 本报告聚焦 P0/P1，不收录一般可用性、可访问性、代码风格、低概率竞态或纯性能类 P2/P3 问题。
- Playwright E2E 使用 mocked API；真实 Cookie 的刷新回放与注销语义由后端 HTTP 集成测试覆盖。浏览器多标签协调依赖 Web Locks，当前单测覆盖共享刷新入口，尚未建立真实多标签浏览器压力测试。
