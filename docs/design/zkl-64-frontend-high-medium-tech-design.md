# ZKL-64 前端 HIGH / MEDIUM 修复技术方案

| 属性 | 值 |
|---|---|
| 文档版本 | v0.1 |
| 日期 | 2026-07-27 |
| 状态 | **已批准**（负责人 chenow 于 2026-07-27 明确批准 v0.1，按 D1-A～D10-A 实施；评论 `12ef189b-bf80-4ff7-bbb7-58a6db2d1359`） |
| Issue | ZKL-64 / `3e27823a-4fe3-4bbe-b659-bce6e1c10fe9` |
| 评审输入 | Forge Review 评论 `8f06a596-778b-4413-b6a7-5a7b9e2a4747` |
| 代码基线 | `main` @ `fe8a1ee5cde9f13c50d571ddbcefa50fbc7d491c` |
| 已确认范围 | HIGH-01～03、MEDIUM-01～08，共 11 项 |
| 明确非范围 | LOW-01～07；不设计、不实现、不验收；不创建子 Issue |

## 1. 结论摘要

推荐把 11 项按一个 Issue 内的依赖闸门交付，核心是四个结构性改变：

1. 工作区列表 API 改为服务端分页读模型，并在工作区 DTO 中投影当前用户的 `currentUserRole`。前端权限判断只依赖该角色，不再通过全量成员列表推导。HIGH-01 与 MEDIUM-08 因此必须作为同一切片完成。
2. 所有业务路由改为异步加载，Element Plus、VXE Table、Vue Flow、CodeMirror 和 Font Awesome 仅进入实际使用它们的入口或异步块，并以 Vite manifest 的“首屏传递闭包”设置体积硬门禁。
3. HIGH-03 不是一次性重写。先拆 Integration Store，再按页面风险从 Service Connections / Tools 到 Workflow / Smart DAG / Chat，再到 Agents / OpenAPI Imports 分波迁移；每波保持 API 与用户行为不变并独立验收。
4. HTTP GET 只合并“仍在进行中的完全相同请求”，不缓存已经完成的 Axios 响应；任一写请求发出时立即使 GET 合并表失效。

MEDIUM-01 在 Review 基线中已经有可运行的前后端闭环：后端存在 `mustChangePasswordMiddleware`、`PASSWORD_CHANGE_REQUIRED`、改密路由及平台管理员后端校验；前端存在 `/change-password`、路由守卫和禁止改密请求自动重试的逻辑。本 Issue 对其做契约回归和 CI 覆盖，不重复实现。若验证发现缺陷，只修复既有已确认契约；若要改变 API、白名单、角色来源或页面流程，必须回到本方案确认。

## 2. 现状事实

### 2.1 工作区权限与分页

- `frontend/src/stores/workspaces.ts` 的 `can(workspaceId, userId, action)` 先调用 `roleFor`。除 OWNER 外，角色来自 `membersByWorkspace`。
- `can` 对未执行 `loadMembers` 的非 OWNER 用户失败关闭。`WorkflowView.vue`、`AgentAccessView.vue` 等页面调用 `can`，但没有统一保证先加载成员。
- `AppShell.vue` 只执行工作区 `load()`；`WorkspacesView.vue` 为推导角色会对当前页面的每个空间调用成员列表，形成 N+1。
- `fetchCatalog()` 固定请求 `GET /workspaces?limit=500`；`loadWorkspacePage()` 再在浏览器内过滤、排序和切页。
- 后端 `GET /api/v1/workspaces` 默认 `limit=100`、最大 500，只返回 `{items}`。`workspaceDTO` 没有当前用户角色或分页字段。
- `Repository.ListAccessible` 已联结 `workspace_members`，但只选工作区列，因此当前角色已经存在于查询上下文却被丢弃。
- 数据库已有部分索引 `workspace_members_user_active_idx (user_id, workspace_id) WHERE disabled_at IS NULL`，本方案不需要为角色投影或按用户列举新增迁移。
- 后端 `workspace_policy.go` 是唯一授权事实源。角色矩阵为：

| 角色 | VIEW | EDIT | TEST | PUBLISH | EXECUTE | MANAGE | DELETE |
|---|---:|---:|---:|---:|---:|---:|---:|
| OWNER | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| ADMIN | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |  |
| EDITOR | ✓ | ✓ | ✓ | ✓ | ✓ |  |  |
| OPERATOR | ✓ |  | ✓ |  | ✓ |  |  |
| VIEWER | ✓ |  |  |  |  |  |  |

### 2.2 首屏与模块边界

- `frontend/src/router/index.ts` 仅 Workflow、Smart DAG、Chat、Logs、Users 使用动态导入；Overview、Workspaces、Agents、Agent Access、Providers、Connections、OpenAPI Imports、Model APIs、Tools 仍为同步导入。
- `frontend/src/main.ts` 全局安装完整 Element Plus 与 VXE Table，并全局导入 Element Plus、VXE、Vue Flow、Font Awesome `all.min.css`。
- 基线构建产物：入口 JS 2,486.90 KiB（gzip 789.91 KiB），入口 CSS 1,040.49 KiB（gzip 156.43 KiB）；Workflow 异步 JS 269.36 KiB（gzip 84.82 KiB）。
- VXE 仅用于 `ToolSchemaTreeEditor.vue` 和 `ToolSchemaTreeView.vue`；Element Plus 的实际全局需求主要是 `AppSelect` 的 Select / Option 与 `v-loading` 指令。
- `frontend/src/styles/app.css` 14,312 行，同时承载全局基础样式和页面专属样式。
- 超大文件基线：

| 文件 | 行数 |
|---|---:|
| `ServiceConnectionsView.vue` | 4,423 |
| `ToolsView.vue` | 4,052 |
| `SmartDagView.vue` | 3,423 |
| `ChatExecutionView.vue` | 3,009 |
| `WorkflowView.vue` | 2,887 |
| `AgentsView.vue` | 2,619 |
| `OpenAPIImportsView.vue` | 2,389 |
| `stores/integration.ts` | 1,645 |

### 2.3 缓存、测试、CI 与部署

- `frontend/src/services/api.ts` 在 Axios 上覆写 `get`：先合并并发请求，再把完整 `AxiosResponse` 缓存 1 秒。写响应成功后才清空缓存，存在写后立即读到旧响应及复用可变响应对象的问题。
- 前端共有 82 个测试文件，其中 33 个使用 `readFileSync`；多项测试通过查找 SFC / CSS 字符串证明实现细节，而非挂载组件验证行为。
- Playwright 已存在 Workflow 场景及若干人工验收脚本；`.github/workflows/aap-gates.yml` 的 frontend job 只运行 unit、type-check、build，没有安装浏览器或执行 smoke。
- CI 使用 Node 20，Docker builder 使用浮动 `node:22-alpine`，运行镜像使用 `nginx:latest`；仓库同时提交 `package-lock.json`、`pnpm-lock.yaml`、`pnpm-workspace.yaml`。
- `frontend/nginx.conf` 没有统一安全响应头、哈希资源 immutable 缓存和 `index.html` 明确的重新验证策略；SSE location 自带 `add_header`，实施时必须处理 Nginx `add_header` 的继承覆盖规则。
- 通配路由 `:moduleId` 进入 `PlaceholderView.vue`，当前导航配置没有任何经确认的占位模块白名单，因此拼错 URL 会被误呈现为“规划中”。

### 2.4 MEDIUM-01 已有闭环

- `backend/internal/transport/http/must_change_password.go` 对强制改密用户只放行改密、登出和当前用户恢复所需请求，其余返回 403 `PASSWORD_CHANGE_REQUIRED`。
- `backend/internal/transport/http/auth_user.go`、Principal resolver 和后台管理接口使用数据库中的当前用户 / 平台角色，前端路由守卫不构成后端授权。
- 前端 `auth` Store、`/change-password` 路由及 API 401 拦截器共同完成临时密码闭环。
- 基线定向验证通过：
  - 前端 `router/access`、`services/api`、`stores/auth`、`stores/workspaces`：4 个文件、27 项通过。
  - 后端强制改密相关 `internal/transport/http`：通过。

## 3. 目标、非目标与硬约束

### 3.1 目标

- 任一可访问工作区在加载列表或详情时即可得到当前用户角色，UI 权限投影不依赖成员全量加载。
- 首屏只加载应用壳、当前入口及其必要依赖；建立可重复、可阻断 CI 的 bundle 预算。
- 把超大 Store / 页面按稳定领域边界拆开，每个阶段可发布、可验证、可回滚。
- 消除完成态 GET 短缓存导致的写后读竞态。
- 让权限、缓存、路由和管理操作由行为测试证明，并把最小端到端 smoke 接入 CI。
- 统一 Node / npm / lint / format 工具链，并补齐静态服务镜像、安全头和缓存策略。
- 未知路由明确显示 NotFound；工作区列表在大于 500 项时仍能分页和搜索。

### 3.2 非目标

- 不改变后端工作区角色矩阵、授权动作语义或“后端授权最终裁决”原则。
- 不新增角色、组织层级、批量权限编辑、字段级权限或前端授权协议。
- 不重做视觉系统、导航信息架构、业务文案或 LOW-01～07。
- 不重写所有页面业务流程，不更换 Vue / Pinia / Router / Element Plus / Axios。
- 不为 MEDIUM-01 设计新的认证协议、密码规则或管理员模型。
- 不增加数据库表、数据回填、后台迁移或 AAP OpenAPI 变更。
- 不以拆子 Issue、Stage 或持久 Agent 表示 HIGH-03 进度。

### 3.3 硬约束

- Secret、临时密码、Access Token、Refresh Cookie、Provider / Connection 凭证不得进入日志、测试快照、Trace、截图或验收证据。
- 前端权限仅改善可见性和可用性；所有写请求仍必须通过现有后端 Authorizer。
- 公开 API 错误继续使用现有 `{error:{code,message,requestId,traceId?,details?}}` 信封。
- 每一切片通过自测后，实施 checklist 将要求新的、临时、只读 verification subagent 独立验证；不得复用。

## 4. 推荐总体架构

### 4.1 工作区读模型

后端新增专用于当前 Principal 的读模型，而不把角色塞进持久化 `Workspace` 实体：

```text
AccessibleWorkspace
├── Workspace                 # 现有持久事实
└── CurrentUserRole           # 来自有效 workspace_members

WorkspacePage
├── Items []AccessibleWorkspace
└── Pagination {page,pageSize,total}
```

`GET /api/v1/workspaces` 负责服务端查询；`GET /api/v1/workspaces/:wid` 也返回 `currentUserRole`。Owner 角色仍来自创建时同步写入的 OWNER membership，不通过 `ownerUserId` 在前端猜测。

前端边界：

- `useWorkspaceStore`：当前工作区、服务端分页结果、远程搜索结果、`currentUserRole` 与选择持久化。
- `useWorkspaceMembersStore`：仅成员管理页面所需的成员 CRUD 与候选人搜索。
- `useWorkspacePermission`：纯函数式角色 → Action 投影，不发请求、不读取成员列表。
- 业务页面只消费“当前工作区 + 权限投影”；不再自行编排 `loadMembers`。

### 4.2 异步路由与依赖所有权

```text
App bootstrap
├── Vue / Router / Pinia
├── AppShell + 登录/改密
├── 最小基础样式
└── 当前 route chunk
    ├── route-owned components
    ├── route-owned store
    ├── route-owned styles
    └── heavy library only when used
```

- AppShell 子路由全部使用 `() => import(...)`；Login / ChangePassword 是否同步保留不影响业务首屏预算。
- `AppSelect.vue` 局部引入 Select / Option；Loading 指令按需注册，不再 `app.use(ElementPlus)`。
- VXE 注册和样式归 `ToolSchema*` 异步边界；Vue Flow 样式归 Workflow 编辑器；CodeMirror / YAML 编辑能力归对应编辑器块。
- Font Awesome 只保留 core + solid + regular CSS / 字体，删除 brands 与 v4 compatibility 的首屏负担；现有 class 名不改。
- `styles/app.css` 只保留 token、reset、AppShell 和共享 primitive；页面样式随页面组件加载。

### 4.3 Integration Store 与页面分波

Store 按后端资源边界拆为：

```text
stores/
├── providers.ts
├── connections.ts
├── openapiImports.ts
└── tools.ts

services/integration/
├── providers.ts
├── connections.ts
├── openapi-imports.ts
├── tools.ts
└── mappers.ts             # 纯 DTO / payload 转换，不持有状态
```

约束：

- 每个 Store 只拥有自己的集合、选中态、加载错误和 API action。
- 工作区 ID 由调用方明确传入或来自统一 active context；不通过另一个领域 Store 的内部状态隐式推导。
- Secret 只存在于提交函数的局部 payload；响应 sanitize 规则从旧 Store 原样迁移并由测试保护，不进入 Pinia State、持久化或日志。
- 可使用短期 compatibility facade 保证消费者逐个迁移，但该 facade 必须在 Store 分拆切片结束前删除，不能形成第二套长期 API。

页面拆分顺序：

1. Wave A：`ServiceConnectionsView`、`ToolsView`。
2. Wave B：`WorkflowView`、`SmartDagView`、`ChatExecutionView`。
3. Wave C：`AgentsView`、`OpenAPIImportsView`。
4. 每个页面拆成 route shell、list/detail、editor/form、dialog/drawer、page composable、page style；不按“每 500 行切一个文件”做无语义拆分。

最终维护性门槛：

- route shell 不超过 800 物理行；
- 新增/拆出的 SFC 不超过 600 行，Store / composable 不超过 500 行；
- `styles/app.css` 不超过 3,000 行，且没有页面名专属 selector；
- 例外只能是生成文件或具有单一职责、拆分会破坏原子性的声明表，并须在验证证据中说明；本次列出的 7 个页面与 Integration Store 不适用例外。

## 5. API、兼容与错误契约

### 5.1 `GET /api/v1/workspaces`

推荐新请求：

| 参数 | 类型与默认 | 规则 |
|---|---|---|
| `query` | string / 空 | trim 后对 slug、display name、creator / updater 展示名做不区分大小写匹配 |
| `status` | `ACTIVE` / `DISABLED` | 非法枚举返回 400 |
| `mode` | `PRODUCTION` / `SANDBOX` | 非法枚举返回 400 |
| `page` | integer / 1 | `>= 1` |
| `pageSize` | integer / 20 | 仅 10、20、50 |
| `sortBy` | string / `updatedAt` | `slug/displayName/status/mode/createdBy/updatedBy/createdAt/updatedAt` allowlist |
| `sortOrder` | `asc/desc` / `desc` | 与 `sortBy` 一起使用 |

推荐响应：

```json
{
  "items": [
    {
      "id": "uuid",
      "slug": "orders",
      "displayName": "订单中心",
      "currentUserRole": "EDITOR"
    }
  ],
  "pagination": {
    "page": 1,
    "pageSize": 20,
    "total": 1287
  }
}
```

示例省略现有 workspaceDTO 其他字段；它们保持兼容。排序始终追加 `id` 作为确定性 tie-breaker。SQL 的排序列必须由 allowlist 映射，不能拼接任意请求字符串。

兼容策略：

- 本 Issue 内保留旧 `limit` 参数：仅在没有 `page/pageSize` 时按旧语义处理，返回中新增 `pagination` 不破坏只读取 `items` 的客户端。
- 新前端不得再发送 `limit=500`；所有列表、切换器和工作区选择器使用服务端分页 / 远程搜索。
- ZKL-64 不删除旧 `limit`。未来移除需另行版本化，不在本范围。

### 5.2 `GET /api/v1/workspaces/:wid` 与写响应

- 详情 DTO 增加非空 `currentUserRole`；只有通过 VIEW Authorizer 后返回。
- 创建响应的角色为 OWNER。
- 更新、启停等写响应保留前端已经缓存的角色；后端若统一返回 workspaceDTO，则应投影当前 Principal 的角色，不能由 `ownerUserId` 推导。
- 角色在请求间可能变化。任一 403 必须视为后端权威结果：前端清理对应工作区权限缓存、重新拉取详情/第一页，并显示已有请求 ID；不在客户端自动重放写请求。

### 5.3 分页并发

- 采用现有 UI 的 page/pageSize 模型，而不是本次引入 cursor。
- 同时新增或删除数据可能让相邻页轻微漂移；稳定排序 + `id` tie-breaker 避免同一快照内重复顺序。
- 创建、更新、启停、删除成功后重新加载当前页；若当前页因删除变空，则退到上一有效页。
- 现有 `lockVersion` 乐观并发契约保持不变，不新增幂等键。

### 5.4 OpenAPI 与迁移

- Console `GET /api/v1/workspaces` 当前没有对应的 `docs/openapi` 公共规范；AAP 的 `agent-access-v1.yaml` 不受影响。
- 变更必须由 transport/repository contract test 固化，且 README 中的前端开发契约同步更新。
- 无数据库 schema、数据回填或停机迁移。若实施中发现需要新增列/索引，必须停止并回到方案确认。

## 6. 逐项改动边界与完成定义

### HIGH-01：工作区 RBAC `can` / `loadMembers` 不一致

**改动边界**

- 后端工作区列表/详情返回 `currentUserRole`。
- 前端 `can` 改为 `can(workspaceId, action)`，只读 DTO 角色并与后端角色矩阵一致。
- 成员列表从权限判断链路移除；`loadMemberRoles` 删除，成员数据仅服务成员管理 UI。
- 统一检查 Workspace、Workflow、Agent Access、Agents、Providers、Connections、Model APIs、Tools、OpenAPI Imports、Chat / execution 中所有可见写入口。

**非目标**

- 不用前端判断代替后端 Authorizer；不改变角色动作矩阵；不自动升级权限。

**依赖 / 风险**

- 与 MEDIUM-08 共用读模型，必须同一切片交付。
- 角色在页面打开后可被管理员撤销；必须按 403 权威回收 UI，不得保留乐观权限。

**完成定义**

- 非 OWNER 首屏无需请求 `/members` 即可看到正确的只读/可写 UI。
- 行为测试逐角色覆盖矩阵；页面挂载期间没有 N+1 成员请求。
- 未知角色和加载中状态失败关闭；后端 403 后重新同步并显示可追踪错误。

### HIGH-02：首屏 / 主包过大

**改动边界**

- 所有 AppShell 业务子路由改为动态 import。
- Element Plus、VXE、Vue Flow、CodeMirror、Font Awesome 按使用点拆分。
- Vite 开启 manifest；新增 bundle 预算脚本，按入口及其所有静态 import 去重计算 gzip。
- CI 在 build 后运行 `bundle:check`。

**非目标**

- 不通过随意 `manualChunks` 只隐藏 warning；不替换 UI 框架；不把同一依赖重复打进多个路由。

**依赖 / 风险**

- 页面专属 CSS 迁移与 HIGH-03 交叉；HIGH-02 先满足入口预算，HIGH-03 再继续降低全局 CSS。
- 动态 import 可能暴露注册时序问题，必须覆盖每个路由的首次直接访问。

**完成定义**

- 入口传递闭包 JS gzip ≤ 450 KiB；入口 CSS gzip ≤ 120 KiB；任一路由异步 JS gzip ≤ 350 KiB。
- `main.ts` 不再安装完整 Element Plus / VXE，不再导入 `all.min.css` 或工具页专属样式。
- 登录后直接访问每个业务深链均可加载，ChunkLoadError 有统一错误提示和一次显式重试入口。
- CI 对预算超限硬失败，并输出每个入口/路由块的原始值与 gzip 值。

### HIGH-03：巨型页面 / Store 拆分

**改动边界**

- 按第 4.3 节完成 Store 拆分和 Wave A～C 页面拆分。
- 每波同步迁移所属 CSS 和 content-string 测试。
- API 路径、payload、DTO sanitize、用户行为与页面 URL 保持不变。

**非目标**

- 不借拆分重做产品流程，不引入新状态管理框架，不新增跨领域总 Store。

**依赖 / 风险**

- 先完成 HIGH-01/02、MEDIUM-02 及关键行为/E2E 守护，再进入页面拆分。
- 最大风险是隐式 watch、副作用顺序和 Secret sanitize 遗失；用 characterization behavior test 固化，再逐消费者迁移。

**完成定义**

- Integration Store 被四个领域 Store 替代，无长期 facade。
- 7 个目标页面和全局 CSS 达到第 4.3 节门槛。
- 每波 unit、type-check、bundle、smoke 均通过；网络请求数、写 payload 与关键交互没有非批准变化。

### MEDIUM-01：强制改密 / 平台管理员闭环

**改动边界**

- 以既有 ZKL-63 契约做回归：临时密码登录、业务路由 403、允许改密/登出/恢复当前用户、改密后刷新会话、平台管理员前端可见性与后端拒绝。
- 将确定性 smoke 纳入新的 CI 集。

**非目标**

- 不新增密码规则、角色或 API；不把前端 `platformRole` 当作授权依据。

**依赖 / 风险**

- 现状已实现。重复改动会破坏安全边界，因此默认“零生产代码变更”。

**完成定义**

- 既有前后端定向测试继续通过；新增 E2E 证明临时密码用户无法进入业务页、改密成功后可进入。
- 普通用户即使手工访问平台管理员 URL，前端回退且后端管理 API 仍返回 403。

### MEDIUM-02：GET 1 秒短缓存写后读竞态

**改动边界**

- 删除完成响应与 TTL，仅保留同 key、同时未完成 GET 的 Promise 合并。
- GET settle 时仅在 Map 中仍是同一 Promise 才删除，避免旧 Promise 删除新请求。
- 非 GET 在“发出前”递增 generation 并清空表；成功后可再次清空作为防御。
- token 改变、登出、refresh 失败继续清空。

**非目标**

- 不引入跨页面持久缓存、HTTP cache 层或自动重试写请求。

**依赖 / 风险**

- 多个调用方收到同一 in-flight AxiosResponse；调用方不得修改响应对象。后续顺序 GET 一定创建新响应。

**完成定义**

- 并发完全相同 GET 只有一次 adapter 调用；第一个完成后再发 GET 必须再次访问 adapter。
- 写请求发出后启动的 GET 不复用写前 Promise；写完成后的 GET 必为新请求。
- signal / download 请求仍不合并；不同 params / responseType 不合并。

### MEDIUM-03：content 字符串测试 → 行为测试

**改动边界**

- 把对 `.vue` / `.css` 的 `readFileSync`、`toContain`、正则实现断言迁移为 Vue Test Utils / Router / Pinia / Axios adapter 行为断言。
- 优先迁移权限、NotFound、缓存、工作区分页和 HIGH-03 所属页面。

**非目标**

- 不以保持测试数量或覆盖率数字为目标；不把字符串测试改名后保留。

**依赖 / 风险**

- 部分旧测试同时包含真实 mount 与源码断言，只删除源码断言并补足其行为，不删除有效覆盖。

**完成定义**

- 最终不存在读取 `.vue` / `.css` 源文件来证明运行时行为的测试；基线 33 个 `readFileSync` 测试文件逐项有迁移记录。
- 权限使用角色矩阵表驱动测试；页面操作验证 DOM、可访问名称、请求和状态变化。
- 静态 artifact（Vite manifest、Nginx 配置、lockfile）的 contract test 可读取其自身文件，但不得伪装成 UI 行为测试。

### MEDIUM-04：E2E smoke + CI

**改动边界**

- 新增无 Secret、API 全拦截的 `e2e/smoke.spec.ts`。
- Playwright 针对 `vite build` 后的 `vite preview`，不以 dev server 作为 CI 验收。
- CI 安装 Chromium 并执行 smoke；失败保留 trace / screenshot artifact。

**最小 smoke**

1. 正常登录 → AppShell → 工作区远程选择并切换。
2. VIEWER 看不到工作区写入口，EDITOR 可见允许入口但看不到 MANAGE / DELETE。
3. 强制改密用户被导向改密页，成功后进入 Overview。
4. 普通用户访问平台管理员路由被拒；后端 mock 返回 403 时前端展示可追踪错误。
5. 未知深链显示 NotFound 并可返回 Overview。
6. 至少一个管理页面完成 list → create/edit 提交 → fresh reload 的 smoke，用于覆盖 MEDIUM-02。

**非目标**

- 不在 PR CI 中连接共享开发环境、真实账户或真实 Provider；既有需要 live stack 的人工验收脚本不作为 smoke 前置。

**完成定义**

- `npm run e2e:smoke` 本地与 CI 可重复通过；CI 使用 1 次 retry，失败上传 trace/screenshot，不上传请求 Secret。
- `.github/workflows/aap-gates.yml` 的 frontend job 顺序为 install → lint/format → unit/type-check → build/bundle → smoke。

### MEDIUM-05：ESLint / Prettier + 单一包管理器

**改动边界**

- npm 为唯一前端包管理器；删除 `pnpm-lock.yaml` 与 `pnpm-workspace.yaml`。
- 固定 Node 22.22.3、npm 10.9.8；`package.json` 增加 `engines` / `packageManager`，CI、开发说明与 Docker builder 对齐。
- 使用 ESLint flat config（Vue + TypeScript）和 Prettier；ESLint 不承担与 Prettier 冲突的排版规则。
- 新增 `lint`、`format`、`format:check`；CI 零 warning。

**格式化迁移**

- 不在 HIGH-03 之前制造一次 2 万行以上的混合 diff。
- 初始 `.prettierignore` 只可临时列出第 2.2 节的 7 个巨型页面、Integration Store 与 `styles/app.css`。
- 每个 HIGH-03 波次开始时先对该波文件做独立机械格式化并删除对应 ignore；最终源代码 ignore 清零。

**完成定义**

- 干净环境只有 `npm ci`；删除 pnpm 文件后 `npm ci`、lint、format check、test、type-check、build 均通过。
- 最终 `frontend/src` 无临时 Prettier 例外；生成物和第三方快照可按明确规则忽略。

### MEDIUM-06：Nginx 安全头 / 镜像 pin / assets 缓存

**改动边界**

- Builder 与 Nginx 镜像固定到明确 patch + Alpine 版本及 immutable digest；禁止 `latest` / 只写 major。
- 抽出 Nginx security-header include，所有包含自有 `add_header` 的 location 都显式 include，避免继承丢失。
- `/assets/<hash>`：`public,max-age=31536000,immutable`；`index.html` 和 SPA fallback：`no-cache`；不存在的 asset 返回 404，不 fallback 到 HTML。
- 保持现有 `/api`、SSE 路径、超时、`proxy_buffering off` 与 `X-Accel-Buffering: no`。

**最终强制头**

```text
X-Content-Type-Options: nosniff
Referrer-Policy: strict-origin-when-cross-origin
X-Frame-Options: DENY
Permissions-Policy: camera=(), microphone=(), geolocation=()
Content-Security-Policy:
  default-src 'self';
  base-uri 'self';
  object-src 'none';
  frame-ancestors 'none';
  form-action 'self';
  script-src 'self';
  style-src 'self' 'unsafe-inline';
  img-src 'self' data: blob: https:;
  font-src 'self' data:;
  connect-src 'self' https: wss:;
  worker-src 'self' blob:
```

实施阶段先以 `Content-Security-Policy-Report-Only` 跑完整 smoke 和人工核心路径，修复非预期 violation 后，同一 Issue 内切换为强制 CSP。`style-src 'unsafe-inline'` 是现有 Vue / Element 动态样式的兼容边界；不得增加 `script-src 'unsafe-inline'` 或 `unsafe-eval`。

HSTS 由已确认 HTTPS 的生产入口设置 `max-age=31536000`，不带 `includeSubDomains`；本地 HTTP 不以 HSTS 作为验收依据。

**完成定义**

- 构建证据记录镜像 tag、digest 与实际 image ID；不记录 registry credential。
- `nginx -t`、容器 curl 覆盖 index、asset 200、missing asset 404、API、SSE 与安全头。
- CSP 强制模式下 smoke 无非预期 violation，SSE 不缓冲，深链刷新仍返回 App。

### MEDIUM-07：未知路由 → NotFound

**改动边界**

- 删除无白名单的 `:moduleId` Placeholder 路由。
- AppShell 最后使用 `:pathMatch(.*)*` 指向异步 `NotFoundView.vue`。
- 页面复用 AppShell 和现有空状态组件，显示 404、当前路径、“返回概览”和浏览器返回操作。

**非目标**

- 不把真实 API 403/404 映射为路由 NotFound；不新增规划中模块。

**完成定义**

- 拼错一级和多级深链均显示 NotFound；合法深链首次访问正常。
- 未登录未知 URL 仍先进入登录，强制改密用户仍先进入改密；认证优先级不变。

### MEDIUM-08：工作区 `limit=500` 客户端分页

**改动边界**

- 与 HIGH-01 同步完成第 5.1 节服务端分页。
- AppShell 切换器和页面内工作区选择器改为带 debounce 的远程搜索，最多保留已加载页与 active workspace，不在首屏枚举全部空间。
- 持久化 active ID 不在当前页时，通过 `GET /workspaces/:wid` 恢复；403/404 时清除并选择第一页首项。
- Workspaces 管理表直接消费后端 `pagination.total`，过滤或排序变化回到第一页。

**非目标**

- 不为统计卡片加载所有页；不在浏览器模拟全量目录。

**依赖 / 风险**

- 少数现有 Store 通过 `workspaces.items.map` 跨所有工作区发请求；必须改为 active workspace 或显式远程选择，不能把“当前已加载页”误当成“全部工作区”。

**完成定义**

- 以 1,001 个可访问工作区的 repository/handler fixture 验证总数、末页、过滤、稳定排序及角色正确。
- AppShell bootstrap 固定为有限请求数；不出现 `limit=500` 或为每个 workspace 请求成员/业务资源。
- active workspace、切换、管理列表、Chat / Agent / Workflow 的工作区上下文回归通过。

## 7. 权限、安全、审计与可观测性

### 7.1 UI Action 映射

业务控件必须按其后端端点实际 Authorizer Action 绑定，不能按按钮文案猜测：

| UI 行为类别 | Action |
|---|---|
| 查看列表、详情、版本、只读日志 | VIEW |
| 创建/编辑资源、绑定配置、保存草稿 | EDIT |
| 连接测试、工具测试、Workflow trial / validate（以端点现行定义为准） | TEST |
| 发布 | PUBLISH |
| Chat / execute / run | EXECUTE |
| 成员、Agent Access client / credential / grant 管理 | MANAGE |
| 删除工作区 | DELETE |

后端现有路由若对某个具体命令使用不同 Action，测试以该端点为准，并同步更新映射表；不得为了前端方便改变后端 Action。

### 7.2 可见性规则

- 推荐永久无权限的写控件直接隐藏；加载/提交等暂态用 disabled。
- 角色未知时渲染只读骨架，不短暂显示写控件。
- 页面本身只要具有 VIEW 仍可访问；无 VIEW 或工作区被撤销时回到可用工作区/空状态。
- 后端 403 不静默吞掉：显示简短提示与 `requestId`，触发一次权限/当前工作区重新同步，不自动重放写请求。

### 7.3 日志与审计

- 不新增客户端 Secret 日志。网络错误日志只允许 method、模板化 path、status、error code、requestId/traceId；不得输出 request/response body、Authorization 或 Cookie。
- 本次分页 GET 与 UI 可见性变化不新增业务审计事件。
- 既有成员、工作区、Agent Access、发布/执行写操作继续由后端现有审计路径记录；前端隐藏控件不改变审计事实。
- bundle/CI 证据只记录尺寸、文件名、测试名和哈希，不附源码或环境变量。

## 8. 测试与验收矩阵

| 层级 | 必测内容 |
|---|---|
| Backend repository | 角色投影、有效/disabled membership、1,001 条分页、过滤、allowlist 排序、total、稳定 tie-breaker |
| Backend transport | 新 query 校验、响应兼容、详情角色、现有错误信封、403、旧 `limit` bridge |
| Permission unit | 5 角色 × 7 Action 表驱动；unknown / revoked；不触发成员请求 |
| Store unit | active ID 恢复、远程搜索、末页删除、分页 mutation reload、角色刷新 |
| API unit | in-flight GET 合并、settled 不缓存、写前失效、signal / params 分离 |
| Router unit | 每个合法路由、未知一级/多级路径、登录/改密/平台管理员优先级 |
| Component behavior | 控件可见性、表单请求、错误 requestId、加载与空状态；禁止源码字符串代替 |
| Bundle contract | manifest 入口闭包、gzip 预算、重依赖归属、无 brands/v4 资源 |
| E2E smoke | 第 6 节 MEDIUM-04 六条 |
| Nginx/container | image digest、`nginx -t`、header、cache、404 asset、SPA、API/SSE |
| Regression | `npm ci && npm run lint && npm run format:check && npm test -- --run && npm run type-check && npm run build && npm run bundle:check && npm run e2e:smoke`；受影响 Go 包和 race gate |

验收证据中不得包含 Secret。测试使用固定假数据；任何临时密码只存在于进程内 mock，不能写入 trace 或截图。

## 9. 推荐实施顺序

所有项仍记录在 ZKL-64 内，不拆子 Issue。下列每个 Gate 必须独立可回滚、通过新的 verification subagent 后才进入下一 Gate：

1. **Gate 0 — 工具链底座（MEDIUM-05）**  
   固定 Node/npm、清理 pnpm、加入 ESLint/Prettier 与临时精确 ignore。只做机械调整，不夹带业务改动。
2. **Gate 1 — P0 权限与规模（HIGH-01 + MEDIUM-08）**  
   后端分页/角色读模型 → transport contract → workspace/member Store 边界 → 所有 UI 权限消费者 → 行为测试。
3. **Gate 2 — P0 首屏与路由（HIGH-02 + MEDIUM-07）**  
   全路由懒加载、依赖按需、NotFound、manifest 与 bundle 硬门禁。
4. **Gate 3 — 请求一致性（MEDIUM-02）**  
   完成态缓存改为 in-flight coalescing，并加竞态测试。
5. **Gate 4 — 结构性改造守护（MEDIUM-03 第一批 + MEDIUM-04）**  
   先把权限/分页/缓存/路由测试行为化，再建立 CI smoke，作为 HIGH-03 保护网。
6. **Gate 5 — HIGH-03 Wave A**  
   Integration Store 拆分，迁移 Service Connections 与 Tools；清理对应 content tests / CSS / Prettier ignore。
7. **Gate 6 — HIGH-03 Wave B**  
   Workflow、Smart DAG、Chat；逐页独立验证。
8. **Gate 7 — HIGH-03 Wave C**  
   Agents、OpenAPI Imports；清理全局 CSS、剩余 content tests 和所有临时 Prettier ignore。
9. **Gate 8 — 安全闭环回归（MEDIUM-01）**  
   不改既有契约；运行后端、前端与 E2E 回归，发现设计变更则停止。
10. **Gate 9 — 生产静态服务（MEDIUM-06）**  
    pin 镜像、缓存与安全头，先 Report-Only 验证、再强制 CSP；容器级验收。
11. **Gate 10 — 全量验收与回滚演练**  
    全部门禁、1,001 workspace fixture、所有深链、构建预算、smoke、容器头与 Secret 扫描。

## 10. 发布、兼容与回滚

### 10.1 发布

- Gate 1 的后端兼容响应先部署，前端随后切换分页；由于保留 `items` 与旧 `limit`，允许短时前后端错位。
- 前端切换后观察工作区 list latency、403 比率、前端请求数、ChunkLoadError、bundle 预算和 E2E。
- HIGH-03 每波只改变内部模块边界，可以独立发布；禁止多波叠成无法定位的单次重写。
- CSP 必须经历 Report-Only 的完整 smoke，再在同一 Gate 切为 enforced。

### 10.2 回滚

- Gate 1：前端可回滚到旧读取方式，因为后端保留 `items` / `limit`；后端读模型变更只读且无 migration。
- Gate 2：逐路由动态 import 可按 route 回滚；bundle 门禁基线不能删除，只能随已批准预算调整。
- Gate 3：可回滚到“无任何 GET 合并”，不得回滚到完成响应 TTL。
- Gate 5～7：每个消费方迁移后删除 facade 前保留一个验证点；发现回归回滚当前波，不回滚已验证领域。
- Gate 9：CSP enforcement 可临时回到已验证的 Report-Only 以恢复业务，但其他安全头、镜像 digest 和 immutable assets 不回退；回退必须记录 violation。
- 全过程无数据库回滚、数据恢复或 Secret 轮换要求。

## 11. 风险清单

| 风险 | 触发 | 缓解 |
|---|---|---|
| UI 角色短暂过期 | 成员角色在页面打开后变更 | 后端 403 权威；清角色缓存、刷新、禁止写重放 |
| 分页后误把一页当全量 | 旧消费者遍历 `workspaces.items` | active context / 远程选择器 API；代码搜索与测试封锁全量假设 |
| 路由拆分只转移大包 | 重依赖仍在 main 或单一路由超大 | manifest 传递闭包 + route chunk 双预算 |
| Store 拆分改变副作用顺序 | watcher、selection、sanitize 隐式耦合 | characterization behavior test、按消费者迁移、每波独立验证 |
| 格式化吞没逻辑 diff | 一次格式化巨型文件 | 临时精确 ignore，随波独立机械格式化并最终清零 |
| CSP 破坏动态样式/SSE/API | allowlist 缺项 | Report-Only → smoke/人工路径 → enforced；不放宽 script |
| E2E 泄漏凭证 | live account、trace body | 全 API mock、固定假数据、禁止 request body artifact |
| 镜像 digest 不可拉取 | registry 更新或平台架构差异 | 记录 tag+digest+image ID；构建 amd64/arm64 目标验证 |

## 12. 待负责人确认的决策

在下列决策全部明确前，Issue 保持 `blocked`，不生成 implementation checklist。

### D1：工作区 API 与角色投影

- **A（推荐）**：扩展现有 `GET /workspaces` 为 page/pageSize 服务端分页并返回 `currentUserRole`；详情同样返回角色；保留旧 `limit` 兼容，不新增 endpoint。
  - 影响：API 增量最小；适配现有分页 UI；offset 在并发写下允许轻微漂移。
- B：新增 `/workspaces/catalog` cursor API，管理页继续使用另一个分页 API。
  - 影响：大规模滚动更稳定，但 API/Store 双模型和验收面明显扩大。
- C：只把 `limit` 提高并继续客户端分页。
  - 影响：不能解决规模、首屏、N+1 和角色投影，拒绝。

### D2：无权限控件可见性与 Canvas

- **A（推荐）**：永久无权限控件隐藏；角色加载时只读骨架；暂态 disabled；沿用现有组件，不需要 Canvas。
  - 影响：最小视觉变化，减少误触，符合后端权限事实。
- B：所有控件保留但 disabled，并为每个动作提供角色原因 tooltip / 只读 banner。
  - 影响：需要 Canvas 给出 banner、tooltip、移动端与密集表格规范后才能实施。
- C：控件可点，依赖后端 403。
  - 影响：交互差且泄漏能力面，拒绝。

### D3：HIGH-03 完成范围

- **A（推荐）**：同一 Issue 内完整交付 Store + Wave A～C，逐波验收，不拆子 Issue。
  - 影响：周期最长，但 HIGH-03 有明确完成态，不留下未处理的 Review 高风险项。
- B：只交付 Integration Store + Wave A，其他页面留在巨型状态。
  - 影响：只能算部分缓解，必须由负责人明确把其余页面移出本 Issue，否则不能宣称 HIGH-03 完成。
- C：只抽通用函数、保留页面主体。
  - 影响：不改变职责集中，拒绝。

### D4：Bundle 预算

- **A（推荐）**：入口 JS gzip 450 KiB、入口 CSS gzip 120 KiB、单 route JS gzip 350 KiB，CI 硬失败。
- B：只采用 Vite 默认 500 KiB 原始 chunk warning。
  - 影响：不能衡量入口传递依赖，也容易通过重命名/切块规避，拒绝。
- C：将推荐值先作为 warning。
  - 影响：降低首次改造阻力，但不能形成 HIGH-02 完成门禁。

### D5：GET 请求共享

- **A（推荐）**：仅共享 in-flight GET；完成后立即移除；写发出前失效。
- B：彻底移除 GET 共享。
  - 影响：语义最简单，但同挂载周期重复请求增多；可作为实现遇阻时的安全降级。
- C：保留短 TTL，增加更多 mutation invalidation key。
  - 影响：易漏端点且仍共享可变 response，拒绝。

### D6：格式化迁移

- **A（推荐）**：临时精确 ignore 巨型目标，随 HIGH-03 各波机械格式化并删除，最终 `frontend/src` 全覆盖。
- B：Gate 0 一次性格式化全部前端。
  - 影响：规则最简单，但产生超大审查 diff 并放大并行冲突。
- C：永久只格式化新文件。
  - 影响：无法建立一致门禁，拒绝。

### D7：CSP、HSTS 与镜像 pin

- **A（推荐）**：镜像 tag+digest 双 pin；CSP Report-Only 验证后在本 Issue 内 enforced；HSTS 只由 HTTPS 生产入口设置、不含子域。
- B：CSP 长期保持 Report-Only。
  - 影响：可观测但不阻断攻击，MEDIUM-06 安全闭环不足。
- C：本次不加 CSP，只加传统头。
  - 影响：范围更小，但无法覆盖 Review 指出的 CSP 风险。

### D8：NotFound 与 Canvas

- **A（推荐）**：AppShell 内复用现有空状态，提供 404、路径、返回概览/返回；不需要 Canvas。
- B：全屏品牌化 404、插画、搜索或推荐导航。
  - 影响：必须先提供 Canvas；会扩大视觉和内容范围。
- C：保留 Placeholder。
  - 影响：继续把拼错路由当作规划模块，拒绝。

### D9：工作区管理页统计与 Canvas

服务端分页后不能再用浏览器内“已加载页”计算全量 Active / Production / Bound Agents。

- **A（推荐）**：后端分页响应按可访问全集附加只读 `summary`（total/active/production/boundAgents），保持现有统计卡结构；不需要 Canvas。
  - 影响：增加一个聚合查询/CTE及 contract test，但避免 UI 退化。
- B：统计卡明确改为“当前页”，沿用现有样式。
  - 影响：不需要 Canvas，但信息价值下降，且必须改文案防止误解。
- C：重新设计管理页摘要和筛选关系。
  - 影响：需要 Canvas 后才能实现。

### D10：MEDIUM-01 处理方式

- **A（推荐）**：承认基线已实现，只做自动化回归与缺陷修复；任何契约变化重新确认。
- B：按 Review 描述重新实现整条链路。
  - 影响：重复代码且可能破坏 ZKL-63 已确认安全契约，拒绝。

## 13. Canvas 输入结论

若批准推荐选项 D2-A、D8-A、D9-A，本方案不需要新的 Canvas：权限可见性、NotFound 和统计卡均复用现有组件与布局。

以下选择会使 Canvas 成为实施前置：

- D2-B：需定义 disabled 原因、tooltip、只读 banner 和响应式表现。
- D8-B：需定义品牌 404 页面。
- D9-C：需定义分页后管理页统计与筛选的新信息架构。

在 Canvas 未提供前，Forge 不得自行设计上述扩展交互。

## 14. 批准与解除阻塞条件

负责人需明确回复：

1. “批准 ZKL-64 技术方案 v0.1，按推荐项 D1-A～D10-A 实施”；或
2. 对每个不同意的 D 项选择其他选项并说明边界。

解除阻塞条件为：负责人对**当前文档版本**、HIGH-03 完成范围、工作区 API/权限可见性、bundle 预算、格式化策略、CSP/HSTS、NotFound / Canvas 和 MEDIUM-01 验证方式作出明确批准。

批准后由 Knower 亲自生成：

`docs/design/zkl-64-frontend-high-medium-implementation-checklist.md`

Checklist 只能机械拆解获批版本，按本方案依赖顺序编号，并记录每项实现与独立 verification subagent PASS/FAIL；若 checklist 暴露新设计决策，立即停止并回到本确认闭环。
