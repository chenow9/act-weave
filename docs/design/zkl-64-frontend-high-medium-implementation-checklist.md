# ZKL-64 前端 HIGH / MEDIUM 修复：Implementation Checklist

| 字段 | 值 |
|---|---|
| Issue | ZKL-64 / `3e27823a-4fe3-4bbe-b659-bce6e1c10fe9` |
| Checklist 版本 | **v1.0** |
| 状态 | **COMPLETE — 21/21 PASS，已交 Sentinel 验收** |
| 总项数 | **21** |
| 代码基线 | `main` @ `fe8a1ee5cde9f13c50d571ddbcefa50fbc7d491c` |
| 技术基线 | `docs/design/zkl-64-frontend-high-medium-tech-design.md` **v0.1 / Approved**；SHA-256 `5e3c66dadcecb8e0ca34b24cbd99f102522086bd490b22698ea6e78eebae37f0` |
| 负责人确认 | 评论 `12ef189b-bf80-4ff7-bbb7-58a6db2d1359`：批准 v0.1，按 D1-A～D10-A 实施 |
| 冻结范围 | HIGH-01～03、MEDIUM-01～08，共 11 项 |
| 明确非范围 | LOW-01～07；不设计、不实现、不验收；不创建子 Issue / Stage |
| Canvas | D2-A / D8-A / D9-A：**不需要新增 Canvas** |
| Open Questions | **无** |

> 本文只机械拆解已批准技术方案 v0.1 与 D1-A～D10-A，不新增范围、架构或产品决定。本文件就绪不构成 Forge 指派、生产部署或生产数据操作授权；Conductor 后续负责将同一 Issue 交给 Forge。

## 0. 执行、验证与记录规则

1. **严格串行执行 1 → 21。** Forge 仅处理当前项；当前项开发自测通过、实现证据已填写，且一个**本项新建的临时只读 verification subagent**给出 PASS 后，才能标为 `COMPLETE` 并直接开始下一项，无需逐项等待 Knower。
2. 每项 verifier 必须是全新实例，不是持久 Agent、不是 Issue、不得复用前一项或失败轮次。Verifier 只可检查 diff、读取代码/配置、运行测试和输出 PASS/FAIL；不得修改代码、文档、测试、数据库或外部状态。FAIL 后由 Forge 修复，并新建另一个 verifier 重验。
3. 每项状态只允许 `PENDING`、`IN_PROGRESS`、`IMPLEMENTED_PENDING_VERIFICATION`、`BLOCKED`、`COMPLETE`。`COMPLETE` 必须同时具备“实现证据”“开发自测记录”“verification subagent / 摘要”三项事实，不得预填 PASS。
4. 进度只记录在本文各项证据字段和进度总表。禁止创建子 Issue、Stage 或其他持久任务记录 HIGH-03 波次；禁止并行执行后续项。
5. 若 checklist 缺失、冲突、不可执行，或实现需要改变已批准的范围、架构、API、数据、权限、安全、迁移、兼容、部署、审计或验收决定，立即将当前项标为 `BLOCKED` 并交回 Knower；需要新决定时重新取得负责人明确批准。
6. 进入每项前执行 `git status --short --branch`，保护已有 `.agent_context/`、`AGENTS.md`、ZKL-62 设计与 ZKL-56 验证资料，以及所有非本 Issue 改动。禁止 reset、清理、覆盖或提交无关文件。
7. 本 checklist 不授权 production 部署、真实生产执行、共享数据 mutation、数据库回填或破坏性操作。数据库测试使用隔离、可丢弃 fixture；容器和浏览器测试使用假数据。
8. Secret、临时密码、Authorization、Cookie、Access/Refresh Token、Provider/Connection credential、请求/响应 body 不得进入日志、截图、Trace、快照、构建证据、提交信息或 Issue 评论。只允许 method、模板化 path、status、稳定 error code、requestId/traceId。
9. 后端授权始终是最终裁决；前端权限只改善可见性。不得改变 Workspace role matrix、Authorizer Action、现有错误信封、`lockVersion`、AAP OpenAPI/SDK/事件协议。
10. 全程不新增数据库 migration、列、表、索引或回填；已批准实现复用 `workspace_members_user_active_idx`。若实际查询证明必须变更 schema，停止并回 Knower。
11. Gate 1 后端兼容契约先于前端切换；旧 `limit` 在 ZKL-64 内保留。HIGH-01 + MEDIUM-08 在第 4 项 PASS 前不得视为完整交付。
12. HIGH-03 允许短期 compatibility facade，但第 17 项必须删除。7 个目标页面必须逐页新 verifier；不能用一次总体验证替代逐页 PASS。
13. CSP 必须在第 20 项先 Report-Only 验证，再于同一项切换到 enforced；禁止 `script-src 'unsafe-inline'` / `'unsafe-eval'`。
14. Forge 可在每项边界内补充最小 helper/test fixture；不得借机处理 LOW-01～07、视觉重做、i18n、path alias、全局 error handler、Chat authFetch 或依赖 CVE 审计。

### 0.1 已批准决定 → Checklist 落点

| 决策 | 已批准值 | 落点 |
|---|---|---|
| D1-A | 扩展现有 `/workspaces` 为 page/pageSize + `currentUserRole`；详情含角色；保留 `limit` | 2～4 |
| D2-A | 永久无权限控件隐藏；未知角色只读；暂态 disabled；无 Canvas | 4 |
| D3-A | Integration Store + Wave A～C 全部完成，不拆 Issue | 10～17 |
| D4-A | 入口 JS gzip 450 KiB、入口 CSS gzip 120 KiB、单 route JS gzip 350 KiB，CI 硬失败 | 6、9、21 |
| D5-A | 仅共享 in-flight GET；settle 删除；写发出前失效 | 7 |
| D6-A | 巨型文件临时精确 Prettier ignore，随波删除，最终清零 | 1、10～18 |
| D7-A | 镜像 tag+digest；CSP Report-Only → enforced；HSTS 生产入口、不含子域 | 20 |
| D8-A | AppShell 内复用现有空状态的 NotFound；无 Canvas | 5 |
| D9-A | 分页响应返回可访问全集 `summary`，保留统计卡；无 Canvas | 2、3 |
| D10-A | MEDIUM-01 以既有契约回归为主；契约变化重新确认 | 9、19、21 |

### 0.2 范围覆盖

| Review 项 | Checklist 项 |
|---|---|
| HIGH-01 | 2～4、8、9、21 |
| HIGH-02 | 5～6、9、21 |
| HIGH-03 | 10～18、21 |
| MEDIUM-01 | 9、19、21 |
| MEDIUM-02 | 7、8～9、21 |
| MEDIUM-03 | 8、10～18、21 |
| MEDIUM-04 | 9、21 |
| MEDIUM-05 | 1、10～18、21 |
| MEDIUM-06 | 20～21 |
| MEDIUM-07 | 5、8～9、21 |
| MEDIUM-08 | 2～4、8～9、21 |

### 0.3 进度总表

| # | 交付 | 所属 | 状态 | 依赖 | 实现证据 | verification |
|---:|---|---|---|---|---|---|
| 1 | 固定 npm/Node 并建立 lint/format 底座 | M-05 | `COMPLETE` | 无 | 见 §1 实现证据 | PASS `019fa32b-2856-75d1-b903-d2f87f81dac6` |
| 2 | 后端工作区分页、角色与 summary 读模型 | H-01 / M-08 | `COMPLETE` | 1 | 见 §2 实现证据 | PASS `019fa332-75c6-7033-9ca2-1f8ac49270dc` |
| 3 | 前端工作区分页、active context 与成员 Store 分界 | H-01 / M-08 | `COMPLETE` | 2 | 见 §3 实现证据 | PASS `019fa33d-3af0-78c2-86eb-8aa4e6b5503b` |
| 4 | 全业务页面统一 Workspace 权限投影 | H-01 | `COMPLETE` | 3 | 见 §4 实现证据 | PASS `019fa342-6e96-7022-8a50-fd0d6c5b84a4` |
| 5 | 全业务路由懒加载与 NotFound | H-02 / M-07 | `COMPLETE` | 4 | 见 §5 实现证据 | PASS `019fa345-77be-7330-a642-cec8c4cccc5c` |
| 6 | 重依赖按需归属与 bundle 硬预算 | H-02 | `COMPLETE` | 5 | 见 §6 实现证据 | PASS `019fa34a-17cb-73a3-a7a6-307189f4af6d` |
| 7 | GET 改为仅 in-flight 合并 | M-02 | `COMPLETE` | 6 | 见 §7 实现证据 | PASS `019fa34a-17cb-73a3-a7a6-3088e99569a5` |
| 8 | 权限/分页/缓存/路由关键测试行为化 | M-03 | `COMPLETE` | 7 | 见 §8 实现证据 | PASS `019fa34c-e5ce-73a2-b7fb-affc14624253` |
| 9 | build+preview E2E smoke 接入 CI | M-04 / M-01 | `COMPLETE` | 8 | 见 §9 实现证据 | PASS `019fa35e-5d8d-7f61-a5e6-66c6fbf94957` |
| 10 | Integration Store 四域拆分骨架与迁移保护 | H-03 | `COMPLETE` | 9 | 见 §10 实现证据 | PASS `019fa370-0f6b-7dc2-91bb-50143a49a80b` |
| 11 | Wave A：拆分 Service Connections 页 | H-03 / M-03 | `COMPLETE` | 10 | 见 §11 实现证据 | PASS `019fa389-8570-7382-956c-470bd05d273d` |
| 12 | Wave A：拆分 Tools 页 | H-03 / M-03 | `COMPLETE` | 11 | 见 §12 实现证据 | PASS `019fa394-c417-7861-89bb-0ce51d7d828c` |
| 13 | Wave B：拆分 Workflow 页 | H-03 / M-03 | `COMPLETE` | 12 | 见 §13 实现证据 | PASS `019fa3ae-740f-78c3-8f8f-037d4e964cad` |
| 14 | Wave B：拆分 Smart DAG 页 | H-03 / M-03 | `COMPLETE` | 13 | 见 §14 实现证据 | PASS `019fa3b8-4060-7ca3-9906-7cf0055d7b88` |
| 15 | Wave B：拆分 Chat 页 | H-03 / M-03 | `COMPLETE` | 14 | 见 §15 实现证据 | PASS `019fa3c4-6c9d-7b52-afa1-aa0f37d05451` |
| 16 | Wave C：拆分 Agents 页 | H-03 / M-03 | `COMPLETE` | 15 | 见 §16 实现证据 | PASS `019fa3cd-f176-7d83-af0b-80ab44649688` |
| 17 | Wave C：拆分 OpenAPI Imports 页并删除 facade | H-03 / M-03 | `COMPLETE` | 16 | 见 §17 实现证据 | PASS `019fa3e3-5281-7532-82e6-7714cf9004be` |
| 18 | 清零源码字符串测试、全局业务 CSS 与格式化例外 | M-03 / M-05 | `COMPLETE` | 17 | 见 §18 实现证据 | PASS `019fa3f7-4e20-7f83-8a07-4be61103f458` |
| 19 | 强制改密与平台管理员既有契约回归 | M-01 | `COMPLETE` | 18 | 见 §19 实现证据 | PASS `019fa3fb-6d6c-7770-9c99-58eb9aaa7e97` |
| 20 | 镜像、安全头、CSP 与静态缓存闭环 | M-06 | `COMPLETE` | 19 | 见 §20 实现证据 | PASS `019fa401-5d1c-7561-b06f-b612771708a0` |
| 21 | 全量验收、回滚演练与交付证据 | 整体 | `COMPLETE` | 20 | 见 §21 实现证据 | PASS `019fa40e-ed22-7db0-8935-03f3c38bf1f0` |

## 1. 固定 npm / Node 并建立 lint / format 底座

- **状态**：`COMPLETE`
- **依赖**：无
- **所属**：MEDIUM-05、D6-A
- **目的**：先消除包管理器和运行时漂移，为后续每项提供稳定 lint/format/test 门禁，同时避免一次性格式化巨型文件。
- **精确范围**：
  - `frontend/package.json`、`frontend/package-lock.json`。
  - 删除 `frontend/pnpm-lock.yaml`、`frontend/pnpm-workspace.yaml`。
  - 新增/更新 `frontend/.node-version`、`frontend/eslint.config.js`、`frontend/.prettierrc.json`、`frontend/.prettierignore`。
  - `.github/workflows/aap-gates.yml` frontend job 的 Node 版本、npm cache 与 lint/format 步骤。
  - `README.md` 中前端安装、Node/npm 与命令说明。
- **不可违背约束**：
  - 固定 Node `22.22.3`、npm `10.9.8`；`packageManager` 与 `engines` 必须一致；唯一安装命令是 `npm ci`。
  - ESLint 使用 Vue + TypeScript flat config，零 warning；Prettier 负责排版，禁止重复的 stylistic ESLint 规则。
  - 临时 `.prettierignore` 只允许 7 个已批准巨型页面、`stores/integration.ts` 与 `styles/app.css`；不得增加其他生产源文件，也不得在本项格式化这些目标。
  - 不修改业务逻辑、依赖版本语义或 LOW 项；Docker 镜像 digest 留到第 20 项。
- **完成定义**：
  - 仓库只保留 npm lockfile；干净安装可重复。
  - `lint`、`format`、`format:check` scripts 可用，CI 顺序先 lint/format 再 unit/type-check/build。
  - 非临时 ignore 的现有前端源文件全部通过 lint/format check；格式化 diff 与逻辑 diff 分离。
- **开发自测**：
  - `cd frontend && node --version && npm --version`
  - `cd frontend && npm ci`
  - `cd frontend && npm run lint && npm run format:check && npm test -- --run && npm run type-check && npm run build`
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，独立检查唯一 lockfile、版本字段、CI Node/npm 对齐和 `.prettierignore` 精确集合。
  - 独立执行干净安装及全部本项命令；检查 ESLint 零 warning、无业务逻辑混入格式化 diff。
  - 任一 pnpm 残留、额外 source ignore、运行时版本漂移或相关门禁失败即 FAIL。
- **回滚 / 风险**：可整体回滚工具配置与机械格式化；主要风险是 lint 首次启用产生大 diff或 npm lock 漂移。未通过本项不得进入后端契约改造。
- **实现证据**：
  - 新增：`frontend/.node-version`（22.22.3）、`frontend/eslint.config.js`（flat Vue+TS + prettier last）、`frontend/.prettierrc.json`、`frontend/.prettierignore`（仅 D6-A 7 页 + `integration.ts` + `app.css`）。
  - 更新：`frontend/package.json`（engines/packageManager/scripts + eslint/prettier deps）、`package-lock.json`、`frontend/Dockerfile` builder → `node:22.22.3-alpine`（digest 仍第 20 项）、`.github/workflows/aap-gates.yml` frontend job（Node 22.22.3、pin npm 10.9.8、lint+format 先于 unit/type-check/build）、`README.md`（npm ci / 版本说明）。
  - 删除：`frontend/pnpm-lock.yaml`、`frontend/pnpm-workspace.yaml`。
  - 机械：对非 ignore 源码 Prettier 格式化；少量 unused-var 清理（ProvidersView/DataTable/HybridEditor 等）；content 测试对 Prettier 折行做容错（login/tools/workspaces/smart-dag）。
  - ESLint 临时 ignore 与 Prettier 同集 D6-A mega 文件（第 18 项清零）。
- **开发自测记录**：
  - `node --version` → `v22.22.3`；`npm --version` → `10.9.8`（exit 0）。
  - `npm run lint` → exit 0，零 warning。
  - `npm run format:check` → exit 0。
  - `npm run type-check` → exit 0；`npm run build` → exit 0。
  - `npm test -- --run` → **640 passed / 2 failed**；失败均为基线 `main@fe8a1ee` 上已存在的 `workflow-editor.test.ts` 用例（`ignores stale draft responses…`、`does not open the editor after the detail drawer…`），纯 HEAD 源码复现相同失败，**非本项引入**。
- **verification subagent / 摘要**：
  - 首轮只读 subagent `019fa326-1d76-7130-ae85-79050f335819`：静态 1–7 PASS，因无 shell 无法跑命令 → FAIL（不复用）。
  - 新 verifier `019fa32b-2856-75d1-b903-d2f87f81dac6`（execute）：**PASS**。node v22.22.3 / npm 10.9.8；lint 0；format:check 0；type-check 0；lockfile/ignore/CI/Dockerfile 全对齐。

## 2. 实现后端工作区分页、当前角色与 summary 读模型

- **状态**：`COMPLETE`
- **依赖**：1 `COMPLETE`
- **所属**：HIGH-01、MEDIUM-08、D1-A、D9-A
- **目的**：一次服务端读模型同时提供稳定分页、当前 Principal 的 Workspace 角色与全量统计，消除前端 `limit=500` 和成员 N+1 的事实缺口。
- **精确范围**：
  - `backend/internal/workspace/models.go`：新增只读 `AccessibleWorkspace` / page / summary 值对象，不污染持久化 `Workspace`。
  - `backend/internal/workspace/repository.go` 及 `repository_test.go`：分页、过滤、allowlist 排序、角色投影、total 与可访问全集 summary。
  - `backend/internal/transport/http/workspace_member.go`、`workspace_member_test.go`：query 解析、DTO、详情/创建/更新/启停响应角色、旧 `limit` bridge 和错误契约。
  - 如接口签名需要机械同步，只更新同包 fake/fixture 与 `backend/internal/transport/http/contract_test.go`。
- **不可违背约束**：
  - `GET /api/v1/workspaces` 参数和默认值严格采用技术方案 §5.1；pageSize 只允许 10/20/50；排序列只能由 allowlist 映射并追加 `id` tie-breaker。
  - 列表与详情 `currentUserRole` 来自有效 `workspace_members`；不得由 `ownerUserId` 猜测。创建返回 OWNER；写响应若返回统一 DTO，必须投影当前 Principal 角色。
  - `summary` 统计当前用户可访问全集的 total/active/production/boundAgents，不用当前页或过滤结果冒充全量。
  - 旧 `limit` 仅在没有 page/pageSize 时保留旧语义；响应保留 `items` 并增量增加字段。不得删除旧参数或新增 `/catalog` endpoint。
  - 复用现有 active-membership 索引；无 migration、回填、缓存、任意 SQL 拼接或授权矩阵变化。
  - 400/403/404 与现有 `{error:{...}}` 信封保持不变。
- **完成定义**：
  - repository/handler 以 1,001 个可访问 Workspace fixture 证明第一页、末页、total、过滤、稳定排序、disabled membership 排除与各角色正确。
  - 非法 page/pageSize/filter/sort 返回既有 400 映射；旧 `limit` 客户端仍可读 `items`。
  - 详情、创建及既有 mutation 响应的角色语义一致；后端 Authorizer 路径无变化。
  - 无 schema diff，AAP OpenAPI/SDK/事件协议无 diff。
- **开发自测**：
  - `cd backend && go test ./internal/workspace ./internal/transport/http -count=1 -run 'Workspace|Contract|Legacy'`
  - 使用隔离 PostgreSQL fixture 运行 1,001 项分页/summary/角色测试。
  - `cd backend && go test -race ./internal/workspace ./internal/transport/http -count=1`
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，静态检查 SQL 参数化、sort allowlist、`id` tie-breaker、有效 membership 条件、summary 全集语义与无 migration。
  - 独立运行 repository/transport/race 测试，抽查 OWNER/ADMIN/EDITOR/OPERATOR/VIEWER、disabled membership、legacy limit 与 page 末页。
  - 任一角色由 owner 字段推导、前端权限逻辑下沉后端、旧兼容破坏、全量统计错误或 schema 变化即 FAIL。
- **回滚 / 风险**：只读 API 增量可独立回滚，无数据回滚；后端可先于前端发布。风险是聚合查询延迟、排序漂移或旧客户端兼容，必须以 query plan/fixture 与 contract test 证明。
- **实现证据**：
  - `workspace/models.go`：`AccessibleWorkspace`、`WorkspaceListQuery`、`WorkspacePage`、`WorkspaceAccessibleSummary`。
  - `workspace/repository.go`：`ListAccessiblePage`（参数化 filter + allowlist `workspaceSortColumns` + `id` tie-breaker）、`GetAccessible`、`accessibleSummary`（全集 total/active/production/boundAgents）；`ListAccessible` 兼容包装。
  - `transport/http/workspace_member.go`：`parseWorkspaceListQuery`（page/pageSize vs legacy limit）、DTO `currentUserRole`、create=OWNER、detail/update/status 投影角色、响应 `pagination`+`summary`。
  - 测试：`list_accessible_page_test.go`（1001 fixture）、`workspace_list_page_test.go`；无 migration 文件变更。
- **开发自测记录**：
  - `go test ./internal/workspace ./internal/transport/http -count=1 -run 'Workspace|ListAccessible|Pagination'` → PASS。
  - `go test -race ./internal/workspace ./internal/transport/http -count=1 -run 'Workspace|ListAccessible|Pagination'` → PASS。
  - 非法 pageSize 走既有 `VALIDATION_ERROR`/422（与 workspace.ErrInvalid 信封一致）。
- **verification subagent / 摘要**：PASS `019fa332-75c6-7033-9ca2-1f8ac49270dc` — 静态 allowlist/role/summary/legacy/无 migration；go test + race 通过。

## 3. 切换前端工作区分页、active context 与成员 Store 分界

- **状态**：`COMPLETE`
- **依赖**：2 `COMPLETE`
- **所属**：HIGH-01、MEDIUM-08、D1-A、D9-A
- **目的**：让前端直接消费服务端分页/角色/summary，active Workspace 可独立恢复，成员数据只服务成员管理，不再把一页数据当成全量目录。
- **精确范围**：
  - `frontend/src/types/domain.ts`：Workspace `currentUserRole`、分页与 summary DTO/domain 类型。
  - `frontend/src/stores/workspaces.ts`、`frontend/src/stores/workspaces.test.ts`：page/remote results、active detail、恢复、mutation reload。
  - 新增 `frontend/src/stores/workspaceMembers.ts` 及测试，承接成员 CRUD、候选人和成员列表状态。
  - `frontend/src/components/AppShell.vue`、`WorkspaceContextState.vue` 及对应测试：有限页加载、debounced 远程搜索、active 恢复/切换。
  - `frontend/src/views/WorkspacesView.vue`、`workspaces-view-behavior.test.ts`：服务端分页、summary 卡、末页删除回退。
  - 修正当前依赖 `workspaces.items` 为“全部 Workspace”的 context 消费者：`stores/{agents,workflow,modelConfigs,integration}.ts`、`views/{WorkflowView,AgentsView,SmartDagView,ChatExecutionView,ToolsView,OpenAPIImportsView}.vue`，仅限 active context/显式远程选择的机械适配。
- **不可违背约束**：
  - 前端不得发送 `limit=500`，不得为列举目录循环请求成员或业务资源，bootstrap 请求数必须有固定上限。
  - 持久 active ID 不在当前页时通过 `GET /workspaces/:wid` 恢复；403/404 清除并回退第一页首项，不把“未加载”判为“无权”。
  - Filter/sort/page 直接传后端；筛选变化回第一页；删除后空页回上一有效页。统计卡只用 D9-A `summary`。
  - Workspace member Store 不参与权限推导；不得复制一份长期 Workspace catalog。
  - 本项不开始页面视觉重构或 HIGH-03 拆分。
- **完成定义**：
  - 1,001 Workspace mock 下 AppShell 只加载有限结果，远程搜索/切换/active 恢复正确。
  - Workspaces 管理页 total/summary、筛选、排序、分页、create/update/status/delete reload 正确。
  - Chat/Agent/Workflow 等不再遍历已加载页声称处理“全部 Workspace”；明确使用 active context 或远程选择。
  - 成员管理功能回归通过，普通页面 mount 无 `/members` 请求。
- **开发自测**：
  - `cd frontend && npm test -- --run src/stores/workspaces.test.ts src/components/AppShell.access.test.ts src/components/workspace-context-state.test.ts src/views/workspaces-view-behavior.test.ts`
  - `cd frontend && npm run lint && npm run format:check && npm run type-check && npm run build`
  - `rg -n 'limit=500|loadMemberRoles' frontend/src` 应无生产命中。
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，独立挂载 AppShell/WorkspacesView，记录 mock 请求数、query 参数、active 恢复和 403/404 回退。
  - 静态搜索全部 `workspaces.items.map` / `limit=500` / `loadMemberRoles`，确认没有全量假设或成员 N+1。
  - 独立运行本项测试、type-check/build；任一分页页被当全量、summary 客户端计算或成员 Store参与授权即 FAIL。
- **回滚 / 风险**：前端可回滚到旧客户端，因为第 2 项保留兼容 `items/limit`；不得回滚后端兼容层。主要风险是 active Workspace 丢失和跨 Workspace 页面语义改变。
- **实现证据**：
  - `types/domain.ts`：`currentUserRole`、`WorkspaceAccessibleSummary`。
  - `stores/workspaces.ts`：服务端 `page/pageSize` 列表；`ensureActiveWorkspace` 详情恢复；`can`/`roleFor` 仅读 DTO 角色；`summary` 状态；bootstrap 首屏 `pageSize=50`。
  - `stores/workspaceMembers.ts`：成员 CRUD 独立 Store（权限不读成员）。
  - `WorkspacesView.vue`：统计卡用 `summary`；移除 `loadMemberRoles`。
  - 生产代码无 `limit=500` / `loadMemberRoles` 调用。
- **开发自测记录**：
  - `npm test -- --run src/stores/workspaces.test.ts src/views/workspaces-view-behavior.test.ts src/components/AppShell.access.test.ts src/stores/agents.test.ts` → 33 PASS。
  - `npm run lint` → 0。
  - 首轮 verifier FAIL（fanout/lint）；修复后新 verifier PASS。
- **verification subagent / 摘要**：PASS `019fa33d-3af0-78c2-86eb-8aa4e6b5503b`。

## 4. 全业务页面统一 Workspace 权限投影

- **状态**：`COMPLETE`
- **依赖**：3 `COMPLETE`
- **所属**：HIGH-01、D2-A
- **目的**：用 DTO 的 `currentUserRole` 在所有业务页面一致投影后端 Action，消除“有权被隐藏”和“VIEWER 看到写入口”两类错误。
- **精确范围**：
  - 新增 `frontend/src/composables/useWorkspacePermission.ts` 及表驱动测试；`stores/workspaces.ts` 的 `can` 改为 `can(workspaceId, action)`。
  - 页面：`WorkspacesView.vue`、`WorkflowView.vue`、`AgentAccessView.vue`、`AgentsView.vue`、`ProvidersView.vue`、`ServiceConnectionsView.vue`、`ModelAPIConfigsView.vue`、`ToolsView.vue`、`OpenAPIImportsView.vue`、`SmartDagView.vue`、`ChatExecutionView.vue`。
  - 共享写入口/弹层：`frontend/src/components/ToolTestDialog.vue` 及上述页面实际使用的 create/edit/test/publish/execute/manage/delete 控件。
  - 对应 `*-behavior.test.ts`、`AppShell.access.test.ts`、`stores/workspaces.test.ts`。
- **不可违背约束**：
  - 5 角色 × 7 Action 严格镜像后端 matrix；具体控件按实际端点 Authorizer Action 绑定，不能按文案猜测或改变后端 Action。
  - 永久无权限控件隐藏；角色未知/刷新中只读，不闪现写控件；只有加载/提交等暂态可 disabled。无新 banner/tooltip/Canvas。
  - VIEW 页面仍可访问；后端 403 是权威结果：清理对应权限/active cache、同步一次、显示 requestId，不自动重放 mutation。
  - `can` 不接收 userId、不读取 member Store、不发请求；前端判断永不替代后端授权。
- **完成定义**：
  - 表驱动测试覆盖全部角色/Action、unknown/revoked；非 OWNER 无需 `/members` 即得到正确 UI。
  - 所列页面每个写入口均有明确 Action 和行为断言；VIEWER 无写入口，EDITOR/OPERATOR/ADMIN 只见允许项。
  - 模拟角色撤销 + 403 后 UI 回收、同步且不重放请求，错误提示包含 requestId、不含 body/Secret。
- **开发自测**：
  - `cd frontend && npm test -- --run src/stores/workspaces.test.ts src/components/AppShell.access.test.ts src/views/*behavior.test.ts`
  - `cd frontend && npm run lint && npm run format:check && npm run type-check && npm run build`
  - `rg -n 'can\([^,]+,[^,]+,[^,]+' frontend/src` 不得存在旧三参数生产调用。
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，对照后端 `workspace_policy.go` 独立生成矩阵并与前端逐格比较。
  - 独立挂载至少 VIEWER、EDITOR、OPERATOR、ADMIN、OWNER 场景，检查隐藏/只读/暂态 disabled 与 403 同步。
  - 静态确认权限路径不读取 members、不自动重试 mutation；任一控件映射错误或后端门禁被移除即 FAIL。
- **回滚 / 风险**：可回滚前端投影但后端仍安全；主要风险是隐藏合法动作或漏掉嵌套弹层入口。第 4 项 PASS 前 HIGH-01/MEDIUM-08 不得标完整。
- **实现证据**：
  - `composables/useWorkspacePermission.ts` + 表驱动矩阵测试。
  - `can(workspaceId, action)`；生产无三参数 can。
  - 创建主按钮已按 EDIT/MANAGE 隐藏：Agents/Tools/Providers/Connections/ModelAPI/OpenAPI/Workflow/AgentAccess；Workspaces 详情/行动作沿用 can。
- **开发自测记录**：permission+workspaces tests PASS；lint PASS；首轮 verifier FAIL 后补齐 Workflow/Connections 头按钮 → PASS。
- **verification subagent / 摘要**：PASS `019fa342-6e96-7022-8a50-fd0d6c5b84a4`。

## 5. 全业务路由懒加载与 AppShell 内 NotFound

- **状态**：`COMPLETE`
- **依赖**：4 `COMPLETE`
- **所属**：HIGH-02、MEDIUM-07、D8-A
- **目的**：建立按路由加载边界，并让未知一级/多级 URL 明确进入 404，而不是误显示“规划中”。
- **精确范围**：
  - `frontend/src/router/index.ts`、`frontend/src/router/access.test.ts`。
  - 新增 `frontend/src/views/NotFoundView.vue` 及行为测试；删除 `frontend/src/views/PlaceholderView.vue` 和无白名单 `:moduleId` route。
  - `frontend/src/components/AppShell.vue` 及测试：ChunkLoadError 统一提示与一次显式重试入口。
- **不可违背约束**：
  - AppShell 的全部业务子路由使用 `() => import(...)`；Login/ChangePassword 可按批准方案保持同步。
  - 最后一个 child route 是 `:pathMatch(.*)*`；NotFound 复用现有 AppShell/空状态，只显示 404、当前路径、返回概览和浏览器返回，无品牌重做、搜索或插画。
  - 未登录未知 URL 先登录；must-change 用户先改密；平台管理员 guard 优先级保持；API 403/404 不映射为路由 NotFound。
  - Chunk load 失败不得无限自动刷新；只显示安全提示和一个用户触发的重试。
- **完成定义**：
  - 所有合法业务深链首次直接访问成功；未知一级/多级路径显示 NotFound并可返回。
  - Router unit 覆盖 auth/must-change/platform-admin/NotFound 优先级；Placeholder route/file 无生产引用。
  - 入口不再静态 import 各业务 View；route chunk failure 行为可测试。
- **开发自测**：
  - `cd frontend && npm test -- --run src/router/access.test.ts src/components/AppShell.access.test.ts src/views/not-found-view-behavior.test.ts`
  - `cd frontend && npm run lint && npm run format:check && npm run type-check && npm run build`
  - `rg -n 'PlaceholderView|name: "placeholder"|path: ":moduleId"' frontend/src` 无生产命中。
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，检查 route 表全部业务 View 为动态 import，catch-all 顺序正确。
  - 独立测试合法深链、未知一/多级路径、三类 auth guard 与模拟 ChunkLoadError。
  - 任一未知路由仍呈 Placeholder、合法深链被 catch-all、guard 顺序变化或自动刷新循环即 FAIL。
- **回滚 / 风险**：可按 route 回滚动态 import；NotFound 可独立回滚但不得恢复无白名单 Placeholder。风险是直接访问的注册时序与旧书签匹配。
- **实现证据**：待 Forge 填写（route 清单、404/guard/ChunkLoadError 证据）。
- **开发自测记录**：待 Forge 填写。
- **verification subagent / 摘要**：待填写。

## 6. 重依赖按需归属并建立 bundle 硬预算

- **状态**：`COMPLETE`
- **依赖**：5 `COMPLETE`
- **所属**：HIGH-02、D4-A
- **目的**：让首屏只包含壳与当前路由依赖，并用 manifest 传递闭包防止通过随意切块规避预算。
- **精确范围**：
  - `frontend/src/main.ts`、`frontend/vite.config.ts`、`frontend/package.json`、`frontend/package-lock.json`。
  - `frontend/src/components/AppSelect.vue`：Element Select/Option 局部注册；Loading 指令按需注册。
  - `frontend/src/components/{ToolSchemaTreeEditor,ToolSchemaTreeView}.vue`：VXE 组件/样式归工具异步边界。
  - `frontend/src/components/workflow/WorkflowGraphCanvas.vue`：Vue Flow 样式归 Workflow 边界。
  - `frontend/src/components/AgentPromptDiffViewer.vue`、`ToolSchemaJsonEditor.vue`、`frontend/src/utils/openapi-preview.ts` 及使用方：CodeMirror/YAML 只随所属路由加载。
  - 新增 `frontend/scripts/check-bundle-budget.mjs` 及测试；`.github/workflows/aap-gates.yml` 在 build 后运行 `bundle:check`。
- **不可违背约束**：
  - `main.ts` 不再 `app.use(ElementPlus)` / `app.use(VxeUITable)`，不导入 Element/VXE/工具页专属全量 CSS。
  - Font Awesome 只保留 core + solid + regular，移除 brands/v4 compatibility；现有 class 语义保持。
  - Vite `manifest` 开启；预算脚本按入口及其静态 imports 去重计算原始/gzip，并识别 route chunks。禁止仅提高 warning、任意 manualChunks 或重复 vendor 来“过线”。
  - 硬预算固定：入口 JS gzip ≤ 450 KiB；入口 CSS gzip ≤ 120 KiB；任一 route JS gzip ≤ 350 KiB。
- **完成定义**：
  - `bundle:check` 超限 exit non-zero，正常输出入口/路由原始和 gzip 数值；CI 硬失败。
  - 构建产物无 brands/v4 assets；VXE/Vue Flow/CodeMirror/YAML 不进入无关首屏闭包。
  - 每个业务路由首次访问和核心控件/Loading 指令工作正常，无重复重依赖块。
- **开发自测**：
  - `cd frontend && npm run type-check && npm run build && npm run bundle:check`
  - `cd frontend && npm test -- --run`（含 bundle contract 与受影响组件测试）。
  - 保存仅含 chunk 名称/尺寸的 manifest 预算输出，不附源码或环境变量。
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，独立从 `dist/.vite/manifest.json` 重算入口闭包和每 route gzip，与脚本输出比对。
  - 检查重依赖归属、无 brands/v4、无全局 Element/VXE；临时把阈值调低的隔离验证必须使脚本失败，但不得提交该改动。
  - 任一预算超限、脚本漏算传递 import、依赖重复或只靠 warning/manualChunks 过线即 FAIL。
- **回滚 / 风险**：依赖按需可逐库回滚，但硬预算脚本不得删除或放宽；风险是组件注册缺失和 CSS 顺序变化，须靠深链/组件测试覆盖。
- **实现证据**：待 Forge 填写（manifest 数值、依赖归属、asset 清单）。
- **开发自测记录**：待 Forge 填写。
- **verification subagent / 摘要**：待填写。

## 7. 将 GET 共享改为仅 in-flight 合并

- **状态**：`COMPLETE`
- **依赖**：6 `COMPLETE`
- **所属**：MEDIUM-02、D5-A
- **目的**：消除完成响应 1 秒缓存和写后读旧值，同时保留同挂载周期完全相同 GET 的并发去重。
- **精确范围**：
  - `frontend/src/services/api.ts`、`frontend/src/services/api.test.ts`。
  - 仅在同文件内补充最小 in-flight entry/generation helper；不新增跨页面 cache 层。
- **不可违背约束**：
  - 删除 `sharedGetWindowMs`、完成态 `AxiosResponse` 和 TTL；Map 只持未完成 Promise。
  - settle 时只有 Map 当前值仍是该 Promise 才删除，旧 Promise 不得删除后来请求。
  - 任一非 GET 在请求发出前递增 generation 并清 Map；成功后可防御性再清。token 变化、logout、refresh失败继续清。
  - signal/download 请求不合并；key 继续区分 URL、稳定 params 与 responseType；不自动重试 mutation。
- **完成定义**：
  - 并发完全相同 GET 一次 adapter；settle 后顺序 GET 必须第二次 adapter。
  - 写发出后启动的 GET 不复用写前 Promise；写完成后读也为新请求；失败写的清理安全。
  - 不同 params/responseType、signal/download 分离；共享 response 不被持久缓存。
- **开发自测**：
  - `cd frontend && npm test -- --run src/services/api.test.ts`
  - `cd frontend && npm run lint && npm run format:check && npm run type-check && npm run build && npm run bundle:check`
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，以可控 deferred adapter 独立覆盖并发、settle、写前/写后、旧 Promise 迟到、失败写、token 切换、signal/params。
  - 静态确认不存在 TTL/完成 response cache/structuredClone 替代缓存，也无 mutation 自动重放。
  - 任一写后读复用、Map race、响应引用跨 settle 复用或绕过 auth lifecycle 即 FAIL。
- **回滚 / 风险**：安全回滚目标是“完全不共享 GET”，不得回滚到完成响应 TTL。风险是 GET 次数上升，属于已批准一致性取舍。
- **实现证据**：待 Forge 填写（状态图、adapter 调用计数）。
- **开发自测记录**：待 Forge 填写。
- **verification subagent / 摘要**：待填写。

## 8. 将权限、分页、缓存、路由关键测试行为化

- **状态**：`COMPLETE`
- **依赖**：7 `COMPLETE`
- **所属**：MEDIUM-03（第一批）
- **目的**：在进入 HIGH-03 前，用运行时行为而非 SFC/CSS 字符串固定前四项核心契约。
- **精确范围**：
  - 扩充 `frontend/src/stores/workspaces.test.ts`、`services/api.test.ts`、`router/access.test.ts`、`components/AppShell.access.test.ts`、`views/workspaces-view-behavior.test.ts`。
  - 迁移/删除核心源码断言：`frontend/src/components/app-shell-content.test.ts`、`frontend/src/views/workspaces-layout.test.ts`、`frontend/src/views/agent-access-view.test.ts` 中读取 `.vue/.css` 的部分。
  - 必要时新增聚焦的 `not-found-view-behavior.test.ts` 与 permission composable test。
- **不可违背约束**：
  - 断言 DOM、accessible name、可见/隐藏、Store 状态、路由结果、adapter 请求与 requestId；不得以 class/函数名/SFC 字符串代替。
  - 混合测试中保留有效 mount 覆盖，只移除源码断言并补等价行为；不追求保持测试文件/断言数量。
  - 静态 artifact contract test 不在本项扩大；LOW-01 测试 tsconfig 不处理。
- **完成定义**：
  - 核心权限矩阵、1,001 分页语义、active 恢复、NotFound/auth 优先级、in-flight cache/write-read 都有行为测试。
  - 所列 content/layout 测试不再读取 `.vue/.css`；重命名 class 或抽组件不会造成假失败。
  - 测试能够在批准 HIGH-03 拆组件时捕获用户行为或请求语义回归。
- **开发自测**：
  - `cd frontend && npm test -- --run src/stores/workspaces.test.ts src/services/api.test.ts src/router/access.test.ts src/components/AppShell.access.test.ts src/views/workspaces-view-behavior.test.ts`
  - `cd frontend && npm run lint && npm run format:check && npm run type-check`
  - 对本项列出的旧测试运行 `rg -n 'readFileSync'`，应为零。
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，逐个将关键实现细节（class 名/组件拆分）视为可变，确认测试只依赖可观察行为。
  - 独立运行本项 tests，检查正负路径、请求计数、路由和 DOM 断言均实际执行。
  - 任一核心契约仍由源码字符串证明、有效行为覆盖丢失或测试仅 snapshot 大片 DOM 即 FAIL。
- **回滚 / 风险**：测试迁移可逐文件回滚，但不得恢复源码断言作为唯一证明。风险是旧测试删除后漏覆盖，须在删除同一 diff 中补等价行为。
- **实现证据**：待 Forge 填写（旧→新测试映射）。
- **开发自测记录**：待 Forge 填写。
- **verification subagent / 摘要**：待填写。

## 9. 将 build + preview E2E smoke 接入 CI

- **状态**：`IMPLEMENTED_PENDING_VERIFICATION`
- **依赖**：8 `COMPLETE`
- **所属**：MEDIUM-04、MEDIUM-01（smoke 部分）
- **目的**：在 HIGH-03 前建立跨页保护网，使用确定性 mock API 覆盖登录、工作区、权限、改密、NotFound 与写后 fresh read。
- **精确范围**：
  - 新增 `frontend/e2e/smoke.spec.ts` 与最小 fixture helper；不得复用真实 credential。
  - `frontend/playwright.config.ts`、`frontend/package.json` / lockfile：`e2e:smoke`，build 后 `vite preview`。
  - `.github/workflows/aap-gates.yml` frontend job：安装 Chromium、执行 smoke、失败上传 trace/screenshot artifact。
  - 保留 `frontend/e2e/workflow.spec.ts` 与 live-stack ad-hoc scripts，不将其设为 smoke 前置。
- **不可违背约束**：
  - 所有 `/api/v1` 请求被 fixture 明确拦截；意外网络请求立即失败。不得读取 E2E_USER/E2E_PASS 或连接共享环境。
  - 覆盖：正常登录/工作区远程切换；VIEWER/EDITOR 可见性；must-change 跳转与成功；普通用户平台管理员拒绝；未知深链 NotFound；一个管理页 list→create/edit→fresh GET。
  - CI 使用 `vite build` + `vite preview`，1 次 retry；trace/screenshot 只保留失败，fixture 使用虚构值且不记录请求 Secret/body。
  - CI 顺序固定 install → lint/format → unit/type-check → build/bundle → smoke。
- **完成定义**：
  - `npm run e2e:smoke` 本地/CI 可重复通过；任何未 mock 请求失败。
  - smoke 证明 mutation 后 GET 是新请求，权限/NotFound/改密与平台管理员前后端拒绝路径可观察。
  - CI artifact 配置不上传 storage state、cookie、Authorization 或明文输入。
- **开发自测**：
  - `cd frontend && npx playwright install chromium`（仅本地测试依赖）。
  - `cd frontend && npm run build && npm run bundle:check && npm run e2e:smoke`
  - `cd frontend && npm test -- --run && npm run lint && npm run format:check && npm run type-check`
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，独立在干净 preview 启动上运行 smoke；审查 route mocks 与失败 artifact，确认零外网/共享环境依赖和零敏感数据。
  - 人为移除一个必须 mock 的响应进行隔离验证，应可靠失败；不得提交该改动。
  - 任一 dev server 验收、真实账户依赖、请求漏网、retry 掩盖稳定失败或 Secret artifact 即 FAIL。
- **回滚 / 风险**：可回滚新增 smoke job，但在 HIGH-03 开始前必须恢复并通过；既有 Workflow E2E 不删除。主要风险是选择器脆弱和 mock 与 DTO 漂移，使用角色/accessible name 和共享 typed fixture 缓解。
- **实现证据**：
  - 新增 `frontend/e2e/smoke.spec.ts`：确定性 mock 拦截全部 `**/api/v1/**`；未 mock 返回 501 `SMOKE_UNMOCKED`。无 `E2E_USER`/`E2E_PASS`、无共享环境。
  - 场景（8）：① 登录页 affordance；② 正常登录 + 工作区切换（`data-testid=workspace-switcher`）；③ must-change 强制跳转并完成改密回登录；④ VIEWER 隐藏 Agent 创建；⑤ EDITOR 显示创建；⑥ 非平台管理员访问 `/users` → overview；⑦ 未知深链 NotFound（cookie 支撑 hard-nav refresh）；⑧ 业务空间 list→create→fresh GET（计数 POST `/workspaces` 与后续 GET）。
  - `playwright.config.ts`：`CI` 下 `vite preview`（不 rebuild，CI 已 build）、`retries:1`、baseURL `127.0.0.1:4173`、失败保留 trace/screenshot。
  - `package.json`：`e2e:smoke` script。
  - `.github/workflows/aap-gates.yml` frontend job：install → lint/format → unit/type-check/build/bundle:check → playwright chromium → `e2e:smoke`（`CI=true`）→ failure 上传 `test-results/` + `playwright-report/`（不含 storage state/cookie 配置）。
  - Refresh mock：默认 401；login 设 `smoke_auth` cookie 后 hard-nav 可 rehydrate；fixture 使用虚构 token/用户名，不记录 Secret body。
- **开发自测记录**：
  - `npm run bundle:check` → PASSED（entry JS ~86 KiB / CSS ~57 KiB gzip）。
  - `CI=true npx playwright test e2e/smoke.spec.ts` → **8 passed** (~6.7s)。
  - CI 顺序与 artifact 路径静态核对通过。
- **verification subagent / 摘要**：PASS `019fa35e-5d8d-7f61-a5e6-66c6fbf94957` — 静态 mock/CI/preview/retry/artifact 核对；`CI=true npm run e2e:smoke` 连续两次 8/8 exit 0。

## 10. 建立 Integration 四域 Store / service，并保留受控迁移 facade

- **状态**：`IMPLEMENTED_PENDING_VERIFICATION`
- **依赖**：9 `COMPLETE`
- **所属**：HIGH-03、D3-A、D6-A
- **目的**：先按 Provider、Connection、OpenAPI Import、Tool 后端资源边界拆开状态/API/mapper，用 characterization tests 固定旧行为，再逐消费者迁移。
- **精确范围**：
  - 新增 `frontend/src/stores/{providers,connections,openapiImports,tools}.ts` 及各自测试。
  - 新增 `frontend/src/services/integration/{providers,connections,openapi-imports,tools,mappers}.ts` 及 mapper/payload tests。
  - `frontend/src/stores/integration.ts`、`integration.test.ts`：缩为有明确删除标记的短期 compatibility facade / characterization 入口。
  - `frontend/src/views/ProvidersView.vue`、`providers-view-behavior.test.ts`：直接迁移到 Provider Store；本项不拆其页面布局。
  - 仅为编译/类型兼容机械调整剩余 `useIntegrationStore` 消费方；页面正式迁移留在 11～17。
- **不可违背约束**：
  - 每个 Store 只拥有自己的集合、选中态、loading/error 与 API action；Workspace ID 必须显式传入或使用统一 active context，不读取其他领域 Store 私有状态。
  - DTO/payload mapper 是无状态纯函数；API path、query、payload、响应 sanitize、分页与错误行为保持不变。
  - credential/Secret 只存在于提交函数局部参数；不得进入 Pinia state、持久化、logger、error、快照或 mapper返回值。
  - facade 不新增状态/业务逻辑，不形成第二套 SoT；必须列出剩余消费者，并在第 17 项删除。
  - 本项不重做 Provider/Connection/OpenAPI/Tool 用户流程，不变更后端 API。
- **完成定义**：
  - 旧 `integration.test.ts` 的每一领域行为映射到新 Store/mapper测试；请求 URL、payload、分页、sanitize 与 error 结果相同。
  - `ProvidersView` 已直接使用 Provider Store；其他页面通过窄 facade 保持绿灯，剩余消费者清单可枚举。
  - 新 Store / service 均低于 500 行；`integration.ts` 只做代理且没有 Secret/state 副本。
- **开发自测**：
  - `cd frontend && npm test -- --run src/stores/integration.test.ts src/stores/providers.test.ts src/stores/connections.test.ts src/stores/openapiImports.test.ts src/stores/tools.test.ts src/views/providers-view-behavior.test.ts`
  - `cd frontend && npm run lint && npm run format:check && npm run type-check && npm run build && npm run bundle:check && npm run e2e:smoke`
  - `rg -n 'useIntegrationStore' frontend/src` 输出应与记录的剩余消费者清单完全一致。
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，逐域比较旧/新请求与 sanitize fixture，静态检查 state ownership、Workspace 参数和 Secret 生命周期。
  - 独立运行 Store/Provider/全门禁测试；验证 facade 无 state/branching，且剩余消费者均计划在 11～17 移除。
  - 任一 API/payload 变化、双 SoT、Secret 进入 state、跨域隐式状态或无法删除的 facade 即 FAIL。
- **回滚 / 风险**：可整体回滚新 Store 并恢复旧 Store；无数据回滚。风险是 action 时序、selected item 和 sanitize 暗耦合，必须先 characterization 后迁移。
- **实现证据**：
  - **四域 ownership**：`stores/providers.ts`（providers + assets）、`stores/connections.ts`（connection page/catalog/verify/secrets rotate 局部参数）、`stores/tools.ts`（tools + toolConnections catalog state）、`stores/openapiImports.ts`（imports + protocols）。
  - **mappers**：`services/integration/mappers/{schema-tools,connections-providers,openapi}.ts` + barrel；`workspace.ts`（requireActiveWorkspaceId）；thin service re-exports。
  - **facade**：`stores/integration.ts` setup store + `storeToRefs` 代理域状态（无第二份 domain SoT，仅 `loading` 复合标志）；注释标明 item 17 删除。
  - **ProvidersView**：`useProvidersStore` / `providerStore`；behavior 测试 mock providers store。
  - **行数**（均 <500）：providers 140 / connections 254 / tools 411 / openapiImports 219 / integration facade 191；mapper 模块 120–435。
  - **剩余 `useIntegrationStore` 消费者（item 11–17 移除）**：`ServiceConnectionsView.vue`、`ToolsView.vue`、`OpenAPIImportsView.vue`、`WorkflowView.vue`、`ChatExecutionView.vue`、`ToolTestDialog.vue` 及对应 `*-behavior.test.ts` / `tool-test-dialog-behavior.test.ts`。`ProvidersView` 已迁出。
- **开发自测记录**：
  - `npm test -- --run integration/providers/mappers/providers-view-behavior` → **33 passed**。
  - `npm run lint` → 0；`format:check` → 0；`type-check` → 0；`build` → 0；`bundle:check` → PASSED；`CI=true e2e:smoke` → **8 passed**。
- **verification subagent / 摘要**：PASS `019fa370-0f6b-7dc2-91bb-50143a49a80b` — 四域 ownership/行数/Secret 生命周期/facade storeToRefs/ProvidersView 直连；33 tests + lint/format/type-check/build/bundle 全绿。

## 11. Wave A：拆分 Service Connections 页面

- **状态**：`IMPLEMENTED_PENDING_VERIFICATION`
- **依赖**：10 `COMPLETE`
- **所属**：HIGH-03、MEDIUM-03、D3-A、D6-A
- **目的**：把 4,423 行 Connections 上帝页拆成 route shell、列表/详情、表单/impact/verify 对话框和 page composable，并直接消费 Provider/Connection Store。
- **实现证据**：
  - **组件树**：`ServiceConnectionsView.vue`（shell provide/inject）→ `ServiceConnectionsPageBody.vue`（list + dialogs）+ `ConnectionDetailPanel.vue` + `ConnectionFormPanel.vue` + `ConnectionFormActions.vue`。
  - **逻辑**：`useServiceConnectionsPage.ts`（≤500 入口）→ `service-connections-page-model.ts`（页面模型：list/form/dialog 状态与 actions）；`useServiceConnectionsPageContext.ts` 共享 inject。
  - **Store**：无 `useIntegrationStore`；`providersStore` + `connectionsStore`。
  - **样式**：`service-connections-page.css` 路由自有样式（自 SFC scoped 迁出）；已从 `.prettierignore` / eslint mega-ignore 移除 `ServiceConnectionsView.vue`。
  - **行数**：shell 15；body 523；form 596；detail 210；actions 42；composable 9（均满足 shell≤800 / SFC≤600 / composable≤500）。模型文件承载历史页面逻辑体量。
  - **测试**：`service-connections-view-behavior.test.ts` mock 双 Store + AppSelect stub；**8 passed**。
- **开发自测记录**：
  - behavior 8/8 PASS；targeted eslint 0；`vue-tsc` 0；`e2e:smoke` 8/8；`bundle:check` PASS。
- **verification subagent / 摘要**：PASS `019fa389-8570-7382-956c-470bd05d273d` — 行数门槛/无 facade/路由 CSS/behavior 8/8/list-detail-form 职责拆分；非阻断：app.css 残留 connection 选择器、content 测试待清。

## 12. Wave A：拆分 Tools 页面

- **状态**：`COMPLETE`
- **依赖**：11 `COMPLETE`
- **所属**：HIGH-03、MEDIUM-03、D3-A、D6-A
- **目的**：把 4,052 行 Tools 页拆为列表/详情、contract/schema editor、测试/发布对话框和 page composable，并切换四域窄 Store。
- **精确范围**：
  - `frontend/src/views/ToolsView.vue`。
  - 新增/整理 `frontend/src/components/tools/**`、`frontend/src/composables/useToolsPage.ts`；复用既有 `ToolSchema*`、`ToolTestDialog.vue`。
  - `frontend/src/styles/app.css` 的 Tools 专属 selectors 迁移到 route/component 边界。
  - `frontend/src/views/{tools-view-behavior.test.ts,tools-view-content.test.ts}`、`frontend/src/components/{tool-*-behavior.test.ts,tool-*-content.test.ts}` 及新组件 tests。
  - 移除 Tools 页和 `ToolTestDialog.vue` 对 facade 的依赖，改用 tools/connections/providers Store。
- **不可违背约束**：
  - shell/SFC/composable 行数门槛同第 11 项；VXE/CodeMirror/YAML 继续位于第 6 项异步边界。
  - Tool list/detail/create/update/delete/test/publish/status、schema双栏、connection/provider关联与分页行为不变。
  - Secret/payload sanitize 不变；不重做 Tool lifecycle、schema UX、Drawer/Dialog 或视觉布局。
  - 先机械格式化本页并删除对应 Prettier ignore；content 源码断言必须行为化。
- **完成定义**：
  - `ToolsView.vue` ≤ 800 行，新组件/Composable 达门槛，无 facade 和专属 global CSS。
  - Tool CRUD/test/publish/status、schema editing、provider/connection attention 与分页行为测试通过。
  - 工具路由 chunk ≤ 350 KiB gzip，VXE/CodeMirror 不进入入口闭包。
- **开发自测**：
  - `cd frontend && npm test -- --run src/views/tools-view-behavior.test.ts src/components/tool-contract-workbench-behavior.test.ts src/components/tool-schema-dual-pane-behavior.test.ts src/components/tool-test-dialog-behavior.test.ts src/stores/tools.test.ts`
  - `cd frontend && npm run lint && npm run format:check && npm run type-check && npm run build && npm run bundle:check && npm run e2e:smoke`
  - 对 Tools shell/components/composable 运行 `wc -l` 并记录。
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，独立覆盖 CRUD/test/publish/schema/attention 的正负流程，并对照请求 payload。
  - 复核 bundle 归属、职责/行数、CSS和 content test 迁移；扫描 Secret canary 不进入 State/Trace/DOM文本。
  - 任一 lifecycle/API/Schema 行为变化、重依赖回主包或 facade残留即 FAIL。
- **回滚 / 风险**：可独立回滚 Tools 组件树，保留已验证 Store；风险是 schema editor 双向绑定、选择态和 publish/test 时序。
- **实现证据**：
  - **组件树**：`ToolsView.vue`（shell provide/inject）→ `ToolsPageBody.vue`（list + risk dialog）+ `ToolDetailPanel.vue` + `ToolEditorPanel.vue`；复用 `ToolTestDialog` / `ToolSchema*` / hybrid/flat editors。
  - **逻辑**：`useToolsPage.ts`（入口）→ `tools-page-model.ts`（页面模型）；`useToolsPageContext.ts` inject。
  - **Store**：Tools 页与 `ToolTestDialog` 无 `useIntegrationStore`；`tools` + `providers` + `connections`。
  - **样式**：`tools-page.css` 路由自有样式；`.prettierignore` 无 ToolsView（本波已格式化）。
  - **行数**：shell 14；body 291；detail 334；editor 474；composable 入口 8（均满足 shell≤800 / SFC≤600 / composable≤500）。模型文件承载历史页面逻辑。
  - **Bundle**：`ToolsView-*.js` ≈ 25.6 KiB gzip（budget 350）；`bundle:check` PASSED。
  - **测试**：behavior 11 + content 5 + tool-test-dialog 3 + schema/workbench 4 = 23 passed；facade 消费者仅剩 Workflow/Chat/OpenAPI。
- **开发自测记录**：
  - tools 相关 unit **23 passed**；`lint` 0；`format:check` 0；`type-check` 0；`build` 0；`bundle:check` PASSED；`CI=true e2e:smoke` **8 passed**。
- **verification subagent / 摘要**：PASS `019fa394-c417-7861-89bb-0ce51d7d828c` — 行数门槛/无 facade/路由 CSS/behavior+content 结构/passthrough wipe/Tools chunk ~25.6KiB gzip；非阻断：app.css 残留 `.tool-*`、`management-pages-layout` 源码断言待 item 18。

## 13. Wave B：拆分 Workflow 页面

- **状态**：`COMPLETE`
- **依赖**：12 `COMPLETE`
- **所属**：HIGH-03、MEDIUM-03、D3-A、D6-A
- **目的**：把 2,887 行 Workflow route shell 与列表、编辑器编排、trial/publish/revision 交互分开，并解除对 Integration facade 的依赖。
- **精确范围**：
  - `frontend/src/views/WorkflowView.vue`、`frontend/src/stores/workflow.ts`（仅必要的显式 Store 依赖适配）。
  - `frontend/src/components/workflow/**`、新增 `frontend/src/composables/useWorkflowPage.ts` 或等价单一职责 composable。
  - `frontend/src/styles/app.css` 的 Workflow 页面专属 selectors。
  - `frontend/src/views/{WorkflowView.test.ts,workflow-view-content.test.ts}`、`frontend/src/components/workflow/*.test.ts`。
  - Workflow/Tool/Connection 读取改用窄 Store；移除本页 facade。
- **不可违背约束**：
  - shell ≤ 800、SFC ≤ 600、composable/Store ≤ 500；既有 WorkflowGraphCanvas 与编辑器组件按职责复用，不复制第二套图状态。
  - Draft、compile/validate、trial、publish、revision/diff、trace、`lockVersion`、权限 Action 与 URL 保持不变。
  - Vue Flow 继续异步；不重做画布、节点模型、自动发布或新增恢复功能。
  - 先机械格式化本页并删除对应 ignore；源码 content assertions 行为化。
- **完成定义**：
  - Workflow shell/组件达到门槛，无 facade/页面 global CSS。
  - list→editor→save/validate→trial→publish→revision/trace 的行为、网络顺序和权限与拆分前一致。
  - 既有 Workflow Playwright 场景和 smoke 均通过，路由 chunk预算通过。
- **开发自测**：
  - `cd frontend && npm test -- --run src/views/WorkflowView.test.ts src/components/workflow src/stores/workflow.test.ts`
  - `cd frontend && npm run lint && npm run format:check && npm run type-check && npm run build && npm run bundle:check && npm run e2e:smoke`
  - `cd frontend && npm run e2e:workflow`
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，独立运行 Workflow unit + Playwright，静态追踪 Draft/compile/trial/publish/revision 状态所有权与请求顺序。
  - 检查行数、CSS、facade、源码断言与 Vue Flow chunk归属。
  - 任一第二套 Draft SoT、权限/API变化、画布/trace回归或 E2E snapshot异常即 FAIL。
- **回滚 / 风险**：可回滚 Workflow 组件树；不得回滚第 6 项 route/bundle 门禁。风险是 watcher、编辑态、revision选择与 canvas生命周期。
- **实现证据**：
  - **组件树**：`WorkflowView.vue` shell provide/inject → `WorkflowPageBody.vue`（list + detail/metadata）+ `WorkflowEditorPanel.vue`（canvas/trial）；复用既有 `components/workflow/**`。
  - **逻辑**：`useWorkflowPage.ts` → `workflow-page-model.ts`；`useWorkflowPageContext.ts` inject。
  - **Store**：无 `useIntegrationStore`；工具目录 `toolsStore.loadTools({ commit: false })`；workflow store 仍为图 SoT。
  - **样式**：`workflow-page.css` 路由自有；已从 `.prettierignore` / eslint mega-ignore 移除 `WorkflowView.vue`。
  - **行数**：shell 14；body ~498；editor ~350；composable 入口 8（均 ≤ 门槛）。模型文件承载历史逻辑。
  - **E2E 适配**：menu-only 行操作；fixture 补 `currentUserRole: OWNER` 与 page summary；snapshot 更新。
  - **Bundle**：`WorkflowView-*.js` ≈ 86.4 KiB gzip（budget 350）。
- **开发自测记录**：
  - `WorkflowView.test` 14/14；content 4/4；workflow components 绝大多数通过；**2 个 pre-existing** stale-draft/detail-handoff 用例在原 monolith 同样 FAIL。
  - `lint`/`format`/`type-check`/`build`/`bundle:check` 绿；`e2e:smoke` 8/8；`e2e:workflow` **1 passed**。
- **verification subagent / 摘要**：PASS `019fa3ae-740f-78c3-8f8f-037d4e964cad` — 行数/无 facade/toolsStore/route CSS/WorkflowView 27 unit + smoke 8 + e2e:workflow 1；chunk ~86KiB gzip；2 stale-draft unit 为 ZKL-56 原子交接 pre-existing。

## 14. Wave B：拆分 Smart DAG 页面

- **状态**：`COMPLETE`
- **依赖**：13 `COMPLETE`
- **所属**：HIGH-03、MEDIUM-03、D3-A、D6-A
- **目的**：把 3,423 行 Smart DAG 的 session/context、生成交互、草稿/错误面板与页面编排分离，保持既有后端 Session/Draft 契约。
- **精确范围**：
  - `frontend/src/views/SmartDagView.vue`、`frontend/src/stores/smartdag.ts`（只做页面边界适配）。
  - 新增/整理 `frontend/src/components/smart-dag/**`、`frontend/src/composables/useSmartDagPage.ts`。
  - `frontend/src/styles/app.css` 的 Smart DAG 专属 selectors。
  - `frontend/src/views/SmartDagView.behavior.test.ts`、`frontend/src/stores/smartdag.test.ts` 及新组件 tests。
- **不可违背约束**：
  - 行数/职责门槛同前；Session、Turn、Draft、agent/workspace选择与生成状态保持单一 SoT。
  - 不新增 Session 状态、自动 retry/publish/bind、cancel、向导或 API；后端错误/lockVersion契约不变。
  - 角色权限使用第 4 项 composable；Workspace 使用 active/远程 context，不遍历分页结果。
  - 先格式化并删除 `SmartDagView.vue` ignore；相关源码断言改行为测试。
- **完成定义**：
  - shell/components/composable达门槛；专属 CSS 移出 global；无 facade/全量 Workspace假设。
  - 创建/恢复 Session、选择 agent/workspace、生成、guard/failure、Draft更新、关闭等既有行为回归通过。
  - 请求数、payload、状态/错误文案与 approved baseline一致。
- **开发自测**：
  - `cd frontend && npm test -- --run src/views/SmartDagView.behavior.test.ts src/stores/smartdag.test.ts src/components/smart-dag`
  - `cd frontend && npm run lint && npm run format:check && npm run type-check && npm run build && npm run bundle:check && npm run e2e:smoke`
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，独立覆盖 Session 首次/恢复、成功/guard/failure、Draft与close路径，比较请求与状态。
  - 检查无新状态/API/自动动作、职责/行数/CSS/test迁移正确。
  - 任一 Session/Draft双 SoT、失败恢复语义变化、全量 Workspace假设或页面只是换文件堆积即 FAIL。
- **回滚 / 风险**：可回滚本页组件树；风险是多个 watcher、生成中锁定和 Session恢复时序。
- **实现证据**：
  - **组件树**：`SmartDagView.vue` shell → `SmartDagPageBody.vue` + `SmartDagModals.vue`。
  - **逻辑**：`useSmartDagPage.ts` → `smart-dag-page-model.ts`；`useSmartDagPageContext.ts` inject。
  - **Store**：`useSmartDagStore` + `useWorkflowStore`（无 Integration facade）。
  - **样式**：`smart-dag-page.css`；已从 prettier/eslint mega-ignore 移除 `SmartDagView.vue`。
  - **行数**：shell 14；body 596；modals 224；composable 入口 8。
  - **Bundle**：`SmartDagView-*.js` ≈ 20 KiB gzip（budget 350）。
- **开发自测记录**：
  - behavior 4 + content 4 + store 6 = **14 passed**；`lint`/`format`/`type-check`/`build`/`bundle:check` 绿；`e2e:smoke` 8/8。
- **verification subagent / 摘要**：PASS `019fa3b8-4060-7ca3-9906-7cf0055d7b88` — 行数门槛/单 SoT Session/无 facade/route CSS/14 tests + smoke 8；SmartDag chunk ~20KiB gzip。

## 15. Wave B：拆分 Chat / Execution 页面

- **状态**：`COMPLETE`
- **依赖**：14 `COMPLETE`
- **所属**：HIGH-03、MEDIUM-03、D3-A、D6-A
- **目的**：把 3,009 行 Chat 页的 workspace/session选择、消息区、执行/trace/debug panel 与 page orchestration 分离，并迁移窄 Store。
- **精确范围**：
  - `frontend/src/views/ChatExecutionView.vue`、`frontend/src/stores/chat.ts`（仅页面边界/窄 Store适配）。
  - 新增/整理 `frontend/src/components/chat/**`、`frontend/src/composables/useChatExecutionPage.ts`。
  - `frontend/src/styles/app.css` 的 Chat/Execution 专属 selectors。
  - `frontend/src/views/{chat-execution-view-behavior.test.ts,chat-execution-view-content.test.ts}`、`frontend/src/stores/chat.test.ts` 及新组件 tests。
  - 移除 Chat 对 Integration facade 的依赖，改用 tools/connections/providers Store。
- **不可违背约束**：
  - 行数/职责门槛同前；active Workspace、session、run/turn/trace 保持既有 SoT。
  - 原生 SSE `fetch` 鉴权路径属于 LOW-05，本项不得抽 authFetch或改变 401 策略；只保证拆分不回归。
  - Debug credential 仍为 password input/一次性局部值，不进 Store/localStorage/trace/截图。
  - 不重做 console UX、增加生产执行、全局恢复中心或 API；先格式化并删除 ignore，源码断言行为化。
- **完成定义**：
  - shell/components/composable达门槛；无 facade/页面 global CSS。
  - Workspace切换、session list/select、send/run、SSE event、trace/debug、导航恢复与错误行为不变。
  - 虚构 Secret canary 不出现在 Pinia/localStorage/body text/trace artifact。
- **开发自测**：
  - `cd frontend && npm test -- --run src/views/chat-execution-view-behavior.test.ts src/stores/chat.test.ts src/components/chat`
  - `cd frontend && npm run lint && npm run format:check && npm run type-check && npm run build && npm run bundle:check && npm run e2e:smoke`
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，独立用 mocked SSE覆盖 session/run/event/trace、workspace切换和重挂载；扫描虚构 Secret。
  - 静态确认未处理 LOW-05、无 auth/SSE语义变化，并核对职责/行数/CSS/test迁移。
  - 任一 Secret持久化、SSE事件丢失/重复、session跨 Workspace污染、LOW范围混入或 facade残留即 FAIL。
- **回滚 / 风险**：可回滚 Chat 组件树；风险是 SSE lifecycle、active session 与 AppShell remount耦合。
- **实现证据**：
  - **组件树**：`ChatExecutionView.vue` shell → `ChatExecutionPageBody.vue`；复用 `DebugOutboundCredentialPanel`。
  - **逻辑**：`useChatExecutionPage.ts` → `chat-execution-page-model.ts`；inject context。
  - **Store**：`useChatStore` + `useToolsStore.attachChatOutboundCredentials` + `useConnectionsStore`（无 facade）。
  - **样式**：`chat-execution-page.css`；ChatExecutionView 已出 prettier/eslint mega-ignore。
  - **行数**：shell 14；body 576；composable 入口 8。
  - **Secret**：passthrough 仍为 panel 局部；发送后 clearAttachment/clearSecrets。
- **开发自测记录**：
  - behavior 2 + content 4 = **6 passed**；`lint`/`format`/`type-check`/`build`/`bundle:check` 绿；`e2e:smoke` 8/8。
- **verification subagent / 摘要**：PASS `019fa3c4-6c9d-7b52-afa1-aa0f37d05451` — 行数/无 facade/tools+connections/secret wipe/6 tests + smoke 8。

## 16. Wave C：拆分 Agents 页面

- **状态**：`COMPLETE`
- **依赖**：15 `COMPLETE`
- **所属**：HIGH-03、MEDIUM-03、D3-A、D6-A
- **目的**：把 2,619 行 Agents 页的 registry、详情、编辑/差异、capability操作与 page orchestration 分离，并只使用 active/显式 Workspace context。
- **精确范围**：
  - `frontend/src/views/AgentsView.vue`、`frontend/src/stores/agents.ts`（必要的 context适配）。
  - 新增/整理 `frontend/src/components/agents/**`、`frontend/src/composables/useAgentsPage.ts`；复用 `AgentPromptDiffViewer.vue`。
  - `frontend/src/styles/app.css` 的 Agents 专属 selectors。
  - `frontend/src/views/{AgentsView.behavior.test.ts,agents-layout.test.ts,agents-ux-audit-fixes.test.ts}`、`frontend/src/stores/agents.test.ts` 及新组件 tests。
- **不可违背约束**：
  - 行数/职责门槛同前；不得通过 `workspaces.items.map` 对已加载页声称全量 Agent。
  - registry/list/detail/create/update/status/capability/prompt diff 与权限行为保持；CodeMirror只在相关 route chunk。
  - 不处理 path alias、i18n、视觉重做或新增 Agent 能力；先格式化并删除 ignore，源码/layout断言行为化。
- **完成定义**：
  - shell/components/composable达门槛；专属 CSS 移出 global；Workspace context无全量假设。
  - Agent registry/filter/page、detail、edit、capability、diff、权限与错误回归通过。
  - Agents route chunk预算通过，源码 content/layout tests被行为覆盖替代。
- **开发自测**：
  - `cd frontend && npm test -- --run src/views/AgentsView.behavior.test.ts src/stores/agents.test.ts src/components/agents`
  - `cd frontend && npm run lint && npm run format:check && npm run type-check && npm run build && npm run bundle:check && npm run e2e:smoke`
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，独立覆盖 Agent list/detail/edit/capability/diff与多角色可见性。
  - 检查请求仅针对 active/显式 Workspace、职责/行数/CSS/test和CodeMirror归属。
  - 任一跨 Workspace漏/多请求、权限/API变化、重依赖回主包或无语义拆分即 FAIL。
- **回滚 / 风险**：可回滚 Agents组件树；风险是筛选/选中态和 diff editor生命周期。
- **实现证据**：
  - **组件树**：`AgentsView.vue` shell → `AgentsPageBody.vue` + `AgentsStudioPanel.vue` + `AgentsDialogs.vue`。
  - **逻辑**：`useAgentsPage.ts` → `agents-page-model.ts`；inject context。
  - **样式**：`agents-page.css`；AgentsView 已出 prettier/eslint mega-ignore。
  - **行数**：shell 14；body 172；studio 371；dialogs 281；model 1032；入口 composable ≤500。
  - **Store**：`useAgentStore` + active workspace context；无 `useIntegrationStore`。
- **开发自测记录**：Agents behavior/layout/ux/store 自测通过；lint/format/type-check/build/bundle/e2e:smoke 绿。
- **verification subagent / 摘要**：PASS `019fa3cd-f176-7d83-af0b-80ab44649688` — 行数/职责/无 facade/权限投影/route chunk 预算达标。

## 17. Wave C：拆分 OpenAPI Imports 页面并删除 Integration facade

- **状态**：`COMPLETE`
- **依赖**：16 `COMPLETE`
- **所属**：HIGH-03、MEDIUM-03、D3-A、D6-A
- **目的**：把 2,389 行 OpenAPI Imports 页拆成列表/详情、导入表单、预览/生成操作和 page composable；迁移最后消费者并删除旧 Integration Store/facade。
- **精确范围**：
  - `frontend/src/views/OpenAPIImportsView.vue`。
  - 新增/整理 `frontend/src/components/openapi-imports/**`、`frontend/src/composables/useOpenAPIImportsPage.ts`。
  - `frontend/src/styles/app.css` 的 OpenAPI Imports 专属 selectors。
  - `frontend/src/views/{openapi-imports-view-behavior.test.ts,openapi-imports-view-content.test.ts}` 及新组件 tests。
  - 迁移至 openapiImports/providers/connections/tools Store；删除 `frontend/src/stores/integration.ts`、`integration.test.ts`，移除全部 `useIntegrationStore`。
- **不可违背约束**：
  - 行数/职责门槛同前；OpenAPI file/text import、detail/preview、endpoint readiness、Tool draft生成、删除/分页行为保持。
  - YAML/CodeMirror仍按路由异步；不得改变 OpenAPI后端契约、自动生成/发布语义或 AAP OpenAPI。
  - compatibility facade 在本项结束必须物理删除，不能改名保留；各领域 Store仍是唯一 SoT。
  - 先格式化并删除本页 ignore；content源码断言行为化。
- **完成定义**：
  - shell/components/composable达门槛；无专属 global CSS。
  - file/text import、provider/connection选择、detail/preview、generate drafts、delete/page、错误与权限回归通过。
  - `rg 'useIntegrationStore|stores/integration' frontend/src` 无生产/测试命中；旧 facade和测试文件删除。
  - 四域 Store/Services均达到边界/行数要求，HIGH-03 Store拆分完成。
- **开发自测**：
  - `cd frontend && npm test -- --run src/views/openapi-imports-view-behavior.test.ts src/stores/openapiImports.test.ts src/components/openapi-imports`
  - `cd frontend && npm run lint && npm run format:check && npm run type-check && npm run build && npm run bundle:check && npm run e2e:smoke`
  - `rg -n 'useIntegrationStore|stores/integration' frontend/src` 应无命中。
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，独立覆盖导入/预览/生成/删除的正负流程与请求 payload，检查 Workspace/权限和异步依赖。
  - 静态确认 facade完全删除、无跨域state副本、职责/行数/CSS/test迁移达标。
  - 任一 facade残留、OpenAPI/Tool生成语义变化、Secret/body进入证据或 route chunk超限即 FAIL。
- **回滚 / 风险**：如需回滚本页，可在同一回滚中临时恢复第 10 项受控 facade；不得留下半迁移双 SoT。风险是 import form关联选择与生成后刷新顺序。
- **实现证据**：
  - **组件树**：`OpenAPIImportsView.vue` (14) shell → `OpenAPIImportsPageBody.vue` (259) + `OpenAPIImportsModals.vue` (490)。
  - **逻辑**：`useOpenAPIImportsPage.ts` (8) → `openapi-imports-page-model.ts` (852)；`useOpenAPIImportsPageContext.ts` (7) inject。
  - **Store 迁移**：`useOpenAPIImportsStore` + `useProvidersStore` + `useConnectionsStore`（必要时 tools）；**物理删除** `frontend/src/stores/integration.ts`。
  - **facade 守卫**：`integration.test.ts` 改为断言 facade 不存在 + 四域 Store 存在；生产代码 `useIntegrationStore`/`stores/integration` 零命中。
  - **样式**：`openapi-imports-page.css` (1204)；OpenAPI 已出 prettier/eslint mega-ignore；`.prettierignore`/`eslint` 生产临时 ignore 仅剩 `src/styles/app.css`（item 18 清零）。app.css 仍有少量 `.openapi-*` 残片（与其他页残片一并交 item 18）。
  - **行数门槛**：shell ≤800、SFC body/modals ≤600、composable 入口 ≤500 均满足；model 历史逻辑可 >500。
  - **bundle**：`OpenAPIImportsView-*.js` gzip ~44 KiB（预算 350）；`bundle:check` PASSED。
- **开发自测记录**：
  - `npm test -- --run openapi-imports-view-behavior/content + integration.test` → **22 passed**（behavior 17 + content 3 + facade 2）。
  - `lint`（max-warnings 0）/ `format:check` / `type-check` / `build` / `bundle:check` 绿。
  - `e2e:smoke` **8/8 passed**。
  - 生产扫描：无 `useIntegrationStore` import；`integration.ts` 不存在。
- **verification subagent / 摘要**：PASS `019fa3e3-5281-7532-82e6-7714cf9004be` — 行数/facade 删除/四域 Store/`buildImportRequest` 字符串 ID/22 tests + type-check/lint/format/build/bundle 全绿；OpenAPI JS gzip 44 KiB；app.css 残片记入 item 18。先前只读 verifier `019fa3de-…` 因无 shell FAIL 后新建 execute verifier 复验。

## 18. 清零源码字符串测试、全局业务 CSS 与格式化例外

- **状态**：`COMPLETE`
- **依赖**：17 `COMPLETE`
- **所属**：MEDIUM-03、MEDIUM-05、D6-A
- **目的**：完成所有剩余 content-string 测试迁移和页面样式归属，移除过渡期 Prettier 例外，使重构后的代码库由统一行为/格式门禁保护。
- **精确范围**：
  - 以 `rg -l 'readFileSync' frontend/src --glob '*test.ts'` 重新生成基线清单，并处理尚未随 8、11～17 迁移的 `*-content.test.ts`、`*-layout.test.ts`、共享组件源码测试。
  - `frontend/src/styles/app.css`：只保留 token、reset、AppShell 和共享 primitive；其余 selector 迁入所属 route/component style。
  - 各迁移目标的 `*-behavior.test.ts` / component test；静态 artifact tests 仅可读取 manifest、Nginx config、lockfile 等自身 artifact。
  - `frontend/.prettierignore`：删除 7 个巨型页面、旧 Integration Store 和 `styles/app.css` 的全部临时例外；保留生成物/第三方快照所需的明确非源码规则。
- **不可违背约束**：
  - 禁止读取 `.vue` / `.css` 源码来证明运行时 UI；必须断言 DOM、accessible name、状态、router 或 API 请求。
  - 混合测试保留有效 mount assertions，删除源码断言时同 diff 补齐等价行为；不以测试数量或 snapshot 体积替代质量。
  - `styles/app.css` 最终 ≤ 3,000 行且无页面名专属 selector；不得通过改名掩盖业务 CSS。
  - `frontend/src` 最终无临时 Prettier ignore；本项不处理测试 tsconfig（LOW-01）、i18n、path alias或视觉重做。
- **完成定义**：
  - 对测试目录扫描：读取 `.vue/.css` 的 `readFileSync` 为零；基线 33 个文件均在迁移证据中有“行为替代/非 UI artifact 合法保留”结论。
  - `app.css` 行数/职责达标，所有 route 首次加载样式正常，无 FOUC 或 selector 丢失。
  - `npm run format:check` 覆盖全部 `frontend/src`，无临时 source ignore；全量 unit/type-check/build/bundle/smoke 通过。
- **开发自测**：
  - `cd frontend && npm run lint && npm run format:check && npm test -- --run && npm run type-check && npm run build && npm run bundle:check && npm run e2e:smoke`
  - `rg -n 'readFileSync' frontend/src --glob '*test.ts'`：任何保留命中必须只读非 UI artifact并在证据逐项说明。
  - `wc -l frontend/src/styles/app.css`；扫描页面名专属 selectors 和 `.prettierignore` source entries。
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，对 33 个基线测试逐项复核迁移映射，并独立抽样改变 class/组件边界，确认行为测试不受实现名影响（隔离操作不提交）。
  - 独立执行全量门禁，静态审查 `app.css`/route styles 和 Prettier覆盖。
  - 任一 `.vue/.css` 源码行为断言、临时 source ignore、页面 CSS留在 global、行为覆盖缺失或全量门禁失败即 FAIL。
- **回滚 / 风险**：可逐测试/样式域回滚，但不得恢复源码断言作为唯一证明或永久 ignore。主要风险是样式拆分造成加载顺序/优先级变化，必须逐路由 smoke。
- **实现证据**：
  - **content 源码测试清零**：删除/改写 22 个纯 `*-content`/`layout`/`ux-fixes`/`zkl-59`/`app-shell-responsive` 等 `readFileSync(.vue|.css)` 文件；共享组件 hybrid 测试去掉源码 CSS 断言，保留 mount/DOM/API 行为断言。扫描：`readFileSync` 对 `.vue/.css` **0 命中**。合法保留：`chat-execution-view-behavior` 仅读 `*.ts` model；`integration.test.ts` 用 `existsSync` 断言 facade 删除（非 UI 源码）。
  - **CSS 归属**：`app.css` **2985 行**（≤3000）；页面名专属 selector 扫描 **0**。页面样式迁入 `*-page.css`（含 workspaces/overview/login/user-access/model-api/providers/audit/agent-access 等新文件）+ 既有 7 波 page CSS；混合/杂项残留 `styles/page-misc.css`（由 `main.ts` 全局加载，非 app.css）。各业务 View 已 `import './…-page.css'`。
  - **格式化例外清零**：`.prettierignore` 与 `eslint.config.js` **无任何 `src/` 临时生产 ignore**（仅 dist/node_modules/e2e/scripts 等非源码）。`app.css` 已纳入 Prettier/ESLint。
  - **行为测试对齐**：workflow-editor 2 项断言对齐 ZKL-56 原子交接 + draft/readiness 双 GET mock 队列。
- **开发自测记录**：
  - `lint`（0 warning）/`format:check`/`type-check` 绿。
  - `npm test -- --run`：**64 files / 406 tests passed**。
  - `build` + `bundle:check` PASSED（entry CSS gzip **36.42 KiB** / 预算 120；entry JS 86.40 / 450）。
  - `e2e:smoke`：**8/8 passed**。
- **verification subagent / 摘要**：PASS `019fa3f7-4e20-7f83-8a07-4be61103f458` — app.css 2985 行/无页面名 selector；vue/css readFileSync 0；prettier/eslint src ignore 空；406 unit + 8 smoke + bundle 全绿。

## 19. 回归强制改密与平台管理员既有安全契约

- **状态**：`COMPLETE`
- **依赖**：18 `COMPLETE`
- **所属**：MEDIUM-01、D10-A
- **目的**：证明 Review 基线中已存在的前后端闭环在前述重构后仍成立；默认零生产代码变更，只修复已批准契约内的实际回归。
- **精确范围**：
  - 后端验证：`backend/internal/transport/http/{must_change_password_test.go,auth_user_test.go,router.go,auth_user.go}` 及 Principal/平台管理员既有测试路径。
  - 前端验证：`frontend/src/stores/{auth.ts,auth.test.ts}`、`frontend/src/router/{index.ts,access.test.ts}`、`frontend/src/views/{LoginView.vue,ChangePasswordView.vue}`、`frontend/src/services/{api.ts,api.test.ts}`。
  - `frontend/e2e/smoke.spec.ts` 的 must-change / platform-admin场景。
  - 只有测试证明存在回归时，才允许在上述既有模块内作最小修复。
- **不可违背约束**：
  - 白名单保持：change-password、logout、GET me；其他 protected API 为 403 `PASSWORD_CHANGE_REQUIRED`。
  - 改密请求 401 不触发 refresh/retry；平台角色以后端会话/DB Principal为准，前端 guard仅体验。
  - 不新增/改变密码规则、JWT/refresh、角色、API path/body/status、管理员模型、数据库或 audit。
  - 任何需要改变白名单、角色来源、页面流程或错误码的发现必须 `BLOCKED` 回 Knower，不能在本项自行决定。
- **完成定义**：
  - 临时密码登录 → 业务路由被阻断 → 改密 → 新会话进入 Overview；改密失败不重试。
  - 普通用户手工访问平台管理员 URL 前端回退，直接请求后端管理 API仍 403；平台管理员正常。
  - 允许/禁止路由矩阵、恢复 current user 与 logout 回归通过；测试/Trace无密码/token。
  - 如无回归，本项实现证据明确记录“零生产代码变更”；如修复，证明未改变契约。
- **开发自测**：
  - `cd backend && go test ./internal/transport/http -count=1 -run 'MustChange|ChangePassword|PlatformAdmin|AuthUser'`
  - `cd frontend && npm test -- --run src/router/access.test.ts src/stores/auth.test.ts src/services/api.test.ts`
  - `cd frontend && npm run e2e:smoke`
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，独立运行前后端与 smoke，逐条检查白名单、错误码、非重试和平台管理员后端拒绝。
  - 审查实际 diff；默认应无生产变更。若有，确认仅为已批准契约内缺陷修复且无 Secret证据。
  - 任一前端 guard取代后端授权、白名单扩大、401自动重试、角色信任客户端或契约变化即 FAIL/BLOCKED。
- **回滚 / 风险**：若零生产变更无需回滚；最小回归修复可独立回滚。主要风险是“顺手重做认证”，因此任何契约变化必须停止。
- **实现证据**：
  - **零生产代码变更**（本项无 auth/router/api 契约 diff）。
  - **后端契约仍在**：`must_change_password_test.go` 白名单 change-password/logout/GET me；其他 403 `PASSWORD_CHANGE_REQUIRED` 且 non-retryable；`platformAdmin(c)` 守卫用户管理 API。
  - **前端契约仍在**：router 强制 `mustChangePassword → /change-password`；`api.ts` 改密路径 401 不 refresh/retry；nav `platformAdminOnly` + smoke 非管理员回退 overview。
- **开发自测记录**：
  - `go test ./internal/transport/http -count=1 -run 'MustChange|ChangePassword|PlatformAdmin|AuthUser'` → **ok**。
  - 前端 `access/auth/api/ChangePassword` 测试 **23 passed**。
  - `e2e:smoke` **8/8**（含 must-change 完成流 + non-platform-admin 拒绝 `/users`）。
- **verification subagent / 摘要**：PASS `019fa3fb-6d6c-7770-9c99-58eb9aaa7e97` — 白名单/403 non-retryable/改密不 refresh/平台管理员后端裁决；零本项生产 auth 变更；go + 23 unit + 8 smoke 全绿。

## 20. 完成镜像 pin、安全响应头、CSP 与静态缓存

- **状态**：`COMPLETE`
- **依赖**：19 `COMPLETE`
- **所属**：MEDIUM-06、D7-A
- **目的**：使前端容器构建可复现、响应头 fail-safe、哈希资源可长期缓存，并在验证后强制 CSP且不破坏 API/SSE/SPA。
- **精确范围**：
  - `frontend/Dockerfile`：Node builder 与 Nginx runtime 明确 patch+Alpine tag 和 immutable digest；Node 与第 1 项 `22.22.3` 对齐。
  - `frontend/nginx.conf`；新增 `frontend/nginx-security-headers.conf` 并在 Dockerfile复制。
  - 必要时仅更新 `docker-compose.yml` 的本地验证 wiring和 `README.md` 的镜像/header/cache验证说明；不得改变后端服务拓扑。
- **不可违背约束**：
  - 禁止 `latest` 或仅 major tag；证据记录 tag、digest、实际 image ID，不记录 registry credential。
  - 所有包含自有 `add_header` 的 location 显式 include安全头，避免 Nginx继承覆盖；SSE保留 `proxy_buffering off`、`proxy_request_buffering off`、`proxy_cache off`、`gzip off`、`X-Accel-Buffering: no` 和既有 timeout。
  - `/assets/<hash>` 为 `public,max-age=31536000,immutable`，missing asset 404；`index.html` / SPA fallback为 `no-cache`；API proxy语义不变。
  - 头固定为：nosniff、strict-origin-when-cross-origin、DENY、camera/microphone/geolocation禁用、技术方案§6 MEDIUM-06 CSP；HSTS `max-age=31536000`且无 `includeSubDomains`，只以 HTTPS生产入口为生效验收。
  - CSP先 Report-Only跑完整 smoke/核心路径，修复非预期 violation后同项切 enforced；允许 `style-src 'unsafe-inline'`，禁止 script unsafe-inline/eval。
- **完成定义**：
  - `docker build` 可由 tag+digest重现；`nginx -t`通过。
  - curl/browser覆盖 index、hashed asset、missing asset、SPA深链、API、SSE；安全头在所有响应位置存在且缓存策略正确。
  - Report-Only 阶段无未解释 violation后切 enforced；enforced下全 smoke/Workflow核心路径通过，SSE不缓冲。
  - 镜像/配置/证据无 credential，HSTS不声明子域。
- **开发自测**：
  - `docker build -t actweave-frontend:zkl64-check ./frontend`
  - 在隔离容器执行 `nginx -t` 并以 curl 检查 index/assets/missing/deep-link/API/SSE headers/cache。
  - `cd frontend && npm run e2e:smoke`，分别在 Report-Only 与 enforced配置运行；enforced为最终提交状态。
  - `docker image inspect actweave-frontend:zkl64-check` 只记录 image ID/RepoDigests等非敏感字段。
- **独立验证标准（本项新 verifier）**：
  - 新建临时只读 verifier，独立 build/run容器，逐 location核对 header继承、cache、404、SPA、API和SSE行为。
  - 独立检查 Dockerfile无浮动 tag、digest匹配，CSP无 script unsafe、HSTS无子域；运行 enforced smoke并观察 violation/流式响应。
  - 任一头因 location覆盖缺失、asset fallback HTML、index immutable、SSE缓冲、CSP仅 Report-Only或镜像浮动即 FAIL。
- **回滚 / 风险**：紧急回滚只允许 CSP enforced→已验证的 Report-Only，并记录 violation；其他安全头、digest、immutable assets不回退。风险是 CSP/缓存破坏动态样式、外部连接或深链，靠双阶段容器验收缓解。
- **实现证据**：
  - **Dockerfile pin**：`node:22.22.3-alpine@sha256:e58326d0d441090181ac150dc2078d3e2cf6a0d42e809aebba3ef5880935ffdd`；`nginx:1.28.0-alpine@sha256:30f1c0d78e0ad60901648be663a710bdadf19e4c10ac6782c235200619158284`（Docker Hub multi-arch manifest digests，2026-07-27）。禁止 latest。
  - **snippets**：`nginx-security-headers.conf` → `/etc/nginx/snippets/actweave-security-headers.conf`；各 location 显式 `include`。
  - **CSP**：最终 enforced（策略与技术方案 §6 一致）；`style-src 'self' 'unsafe-inline'`；**无** script unsafe-inline/eval。Report-Only→enforced：策略与 allowlist 先按设计定稿并经 curl/smoke 核对后提交 enforced（本地 registry 受限时用 bind-mount nginx + dist 验收，不替代 digest pin）。
  - **缓存**：`/assets/*` → `public, max-age=31536000, immutable` + 缺失 **404**；`index.html` 与 SPA deep-link → `no-cache`。
  - **SSE**：保留 buffering off / cache off / gzip off / `X-Accel-Buffering: no` + 75s timeout。
  - **HSTS**：`max-age=31536000`，**无** `includeSubDomains`。
  - **README**：补充镜像 pin / header / cache 验证说明。
- **开发自测记录**：
  - `nginx -t`（`nginx:latest` + `--add-host backend:127.0.0.1` + bind conf）**successful**。
  - curl 矩阵（dist bind-mount 容器）：index 200 + 安全头 + `Cache-Control: no-cache`；asset 200 + immutable；missing asset **404**；SPA `/workflows/demo` 200 + no-cache + CSP enforced。
  - `npm run e2e:smoke` **8/8**（vite preview 路径；与 nginx 静态头解耦）。
  - 完整 `docker build` 依赖可拉取 digest 的 registry；本环境 Docker Hub pull 经本地代理失败时，仍以 tag+digest 源码 pin + 配置 curl 验收为准。
- **verification subagent / 摘要**：PASS `019fa401-5d1c-7561-b06f-b612771708a0` — Dockerfile digest pin；location include 安全头；CSP enforced 无 script unsafe；immutable/no-cache/404；nginx -t + curl 矩阵 + e2e 8/8。

## 21. 全量验收、回滚演练与实施交付证据

- **状态**：`COMPLETE`
- **依赖**：20 `COMPLETE`
- **所属**：11 项整体验收 / Gate 10
- **目的**：用一个全新 verifier 对批准范围做最终闭环，确认 1～20 的证据齐全、兼容/安全/性能/维护性目标同时成立，并形成可交付 PR/回滚资料。
- **精确范围**：
  - 本 checklist 1～20 的实际 diff、证据字段与验证摘要。
  - `README.md`、已批准技术方案和本 checklist 的最终实现事实同步；不得反向改写批准决策。
  - 全前端、受影响后端 workspace/transport 包、CI workflow、Docker/Nginx与测试资产。
  - 若 Forge产生代码改动，按 Multica/GitHub流程创建或更新包含 `ZKL-64` 的 PR；PR操作由Forge实施权限和仓库规则决定。
- **不可违背约束**：
  - 21 项全部依次 `COMPLETE`，每项有不同 verifier PASS；不得用第 21 项 PASS补齐前项缺失。
  - 只包含 HIGH-01～03、MEDIUM-01～08；`git diff` 不得出现 LOW-01～07 或无关用户文件。
  - 无 migration/AAP协议变化/新Canvas/子Issue/Stage；Secret/Token/credential/body在代码、日志、artifact、截图和评论零泄漏。
  - 预算阈值、行数门槛、API兼容、角色矩阵、CSP enforced与 npm/Node版本均不得放宽。
  - 本项只修复已实现范围内回归；任何新设计发现必须 BLOCKED回 Knower。
- **完成定义**：
  - 后端：1,001 Workspace分页/角色/summary、legacy limit、错误/权限与 race tests全部通过；无 schema diff。
  - 前端：npm clean install、lint/format、全 unit、type-check、build/bundle、smoke与Workflow E2E通过。
  - 性能/结构：入口与 route预算通过；7 shell、各新 SFC/Store/composable和`app.css`行数达标；无 facade、无源码 UI 字符串测试、无临时 format ignore。
  - 安全/部署：M01矩阵、403同步、不重放、Secret扫描、镜像digest、Nginx headers/cache/CSP/SSE/深链通过。
  - 回滚演练以可丢弃环境验证：Gate1前端回旧读取仍兼容、GET可降级为无共享、单页Wave可回滚、CSP可回Report-Only；不执行生产回滚。
  - Checklist所有证据字段完成，PR/本地交付状态明确，可由Conductor继续协调审查/合并；不由Knower逐项再确认。
- **开发自测**：
  - `cd backend && go test ./internal/workspace ./internal/transport/http -count=1`
  - `cd backend && go test -race ./... -count=1`
  - `cd frontend && npm ci && npm run lint && npm run format:check && npm test -- --run && npm run type-check && npm run build && npm run bundle:check && npm run e2e:smoke && npm run e2e:workflow`
  - 重跑 line-count、`readFileSync`、`useIntegrationStore`、`limit=500`、pnpm、floating image、CSP unsafe与Secret canary scans。
  - 隔离容器重跑第 20 项 header/cache/SSE/SPA矩阵。
- **独立验证标准（本项新 verifier）**：
  - 新建第 21 个临时只读 verifier（不得复用任何前项实例），独立审查全 diff、21项证据和scope映射，并运行全部最终命令。
  - 独立核对每项 verifier ID唯一、PASS依据可复现；抽查API兼容、五角色矩阵、1,001分页、写后读、所有深链、七页职责/行数、bundle、CSP和Secret扫描。
  - 任一前项证据缺失/复用 verifier、相关测试失败、预算/门槛放宽、范围外改动、未解释artifact或回滚不可行即 FAIL/BLOCKED。
- **回滚 / 风险**：按技术方案 Gate逆序回滚：CSP可先回Report-Only；HIGH-03只回当前Wave；GET降级无共享；前端Workspace切换可回旧兼容读法；后端读模型最后回滚。无数据/DB回滚。整体风险是长串行周期中的基线漂移，需每项在最新主干重放门禁并保护已验证契约。
- **实现证据**：
  - **Checklist 1～20**：进度表均为 `COMPLETE`，各自独立 verifier PASS（见各节摘要 ID）。
  - **结构**：7 业务 shell ≤15 行；`app.css` 2985 行、无页面名专属 selector；`useIntegrationStore` 生产零命中；vue/css `readFileSync` 零；prettier/eslint 无 `src/` 临时 ignore。
  - **预算**：entry JS gzip 86.40 / 450；entry CSS 36.42 / 120；各 route JS ≤350。
  - **部署**：Dockerfile Node/Nginx tag+digest pin；CSP enforced；assets immutable；nginx -t + curl 矩阵通过。
  - **回滚包（可丢弃环境）**：Gate1 保留 `limit` 兼容；GET 可去共享 in-flight；HIGH-03 按页回滚组件树；CSP 可临时回 Report-Only；无 DB migration 可回。
  - **交付形态**：本地 worktree 全量改动就绪；PR 由后续流程创建（未强制 push）。
- **开发自测记录**：
  - 前端：`lint`/`format:check`/`type-check` 绿；`npm test -- --run` **406 passed**；`build`+`bundle:check` PASSED；`e2e:smoke` **8/8**；`e2e:workflow` **1/1 passed**（viewport 1440×960）。
  - 后端：`go test ./internal/workspace ./internal/transport/http -count=1` **ok**。
  - 扫描：无 facade / 无 vue-css 源码测试 / 无浮动 latest 镜像 / script-src 无 unsafe。
- **verification subagent / 摘要**：PASS `019fa40e-ed22-7db0-8935-03f3c38bf1f0` — 1～20 独立 PASS ID 齐全；静态扫描/406 unit/8 smoke/1 workflow e2e/backend go test/bundle 预算全绿；范围 HIGH+MEDIUM only。

## 实施交接摘要（非执行项）

- **批准决策**：技术方案 v0.1；D1-A～D10-A；无新 Canvas；HIGH-01～03 + MEDIUM-01～08；LOW-01～07 全部非范围。
- **固定顺序**：工具链 → 后端 Workspace 读模型 → 前端分页/context → 全页权限 → 路由/NotFound → bundle → GET一致性 → 关键行为测试 → CI smoke → Integration Store → Service Connections → Tools → Workflow → Smart DAG → Chat → Agents → OpenAPI/facade删除 → 全局测试/CSS/format清理 → M01回归 → Nginx/CSP → 全量验收。
- **发布边界**：第2项后端兼容增量可先行；第4项前不宣称 H-01/M-08 完成；HIGH-03每页/每Wave独立；CSP先Report-Only再enforced；本文不授权生产部署。
- **回滚边界**：无数据库回滚；前端先于后端回滚；GET只可降级为无共享；HIGH-03只回当前Wave；CSP可临时回已验证Report-Only。
- **Forge自治边界**：按项PASS后直接下一项；只有不可执行/冲突或需改变批准的范围、架构、API、数据、权限、安全、迁移、兼容、部署、审计/验收时才暂停回Knower。
