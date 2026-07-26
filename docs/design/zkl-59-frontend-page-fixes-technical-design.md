# ZKL-59 前端页面问题修复技术方案

| 字段 | 值 |
|---|---|
| 文档版本 | v0.1 |
| 日期 | 2026-07-26 |
| 作者 | Knower · 系统设计架构师 |
| 状态 | **Approved / Frozen** |
| 关联 Issue | ZKL-59 |
| 产品基线 | `docs/design/zkl-59-frontend-page-fixes-product-design.md` v1.0（Approved / Frozen） |
| UI 基线 | `docs/design/zkl-59-frontend-page-fixes-ui-design.md` UI v0.1 |
| 确认负责人 | chenow |
| 负责人确认 | Issue 评论 `e331fd53-6cf5-4792-9021-45210d7dd070`：“批准技术方案 v0.1；T1=A、T2=A、T3=A，按此方案进入 checklist 阶段。” |

> 本文 v0.1 已由负责人明确批准并冻结，是 implementation checklist 的技术事实源。实现仅能在 T1=A、T2=A、T3=A 及本文边界内推进；若需要改变已冻结的范围、架构、API、数据、权限、安全、迁移、兼容、部署或验收口径，必须停止并回到产品与技术确认闭环。

## 0. 结论

已批准采用 **T1=A：现有组件内聚的纯前端修复**：

1. 在既有 `WorkflowRevisionPanel`、`ChatExecutionView`、`DebugOutboundCredentialPanel`、`ManagementRowActions`、`ProvidersView`、`OpenAPIImportsView` 与 `ToolSchemaTreeView` 边界内补齐结构语义、局部状态、样式和测试。
2. 继续调用现有 Store 与 API；不新增或修改后端路由、请求/响应 DTO、数据库 schema、审计动作、权限 Action 或数据保留规则。
3. Workflow 修复只呈现既有 Revision 信息和既有命令，不改变 `Draft → Compilation → CompiledExecutionPlan → Revision → trial / publish / production execution` 的边界。
4. Provider 修复保持 `outbound-identity.v1`：Provider 声明 1～2 种支持模式，Connection 固定选择其中 1 种；用户业务 Token 不进入 Provider 配置、不持久化。
5. OpenAPI 只改“导入详情”弹窗；打开详情只执行既有只读 GET，不自动生成 Tool Draft。

该方案能覆盖 FE-01～FE-05 与 AC-01～AC-12，且未发现必须改变 API、数据库、审计或冻结产品范围的技术阻塞。

## 1. 现状证据与事实源

### 1.1 已批准输入

- 产品设计 v1.0 已冻结；负责人在 Issue 评论 `e1b97586-688a-400e-b2df-bdd54b80951a` 批准 D1=A、D2=A、D3=A、D4=A、D5=A。
- Canvas UI v0.1 给出桌面布局、间距、选中态、可读性、状态矩阵与 1180/1440px 验收路径；未改变产品范围。
- README 明确前端为 Vue 3 + TypeScript + Vite，桌面端基线 `min-width: 1180px`；后端与 PostgreSQL 仍为 API 和数据事实源。

### 1.2 代码证据

| 范围 | 当前证据 | 技术含义 |
|---|---|---|
| FE-01 | `frontend/src/components/workflow/WorkflowRevisionPanel.vue` 将“发布版本”、Active、Latest 放在同一文本块；`frontend/src/styles/app.css` 的详情正文使用 `overflow: auto`，Revision 行为固定三列 | 长 UUID 会与按钮竞争宽度，并把横向滚动提升到弹窗正文 |
| FE-02 | `frontend/src/views/ChatExecutionView.vue` 使用 `.chat-inline-action`，但没有对应样式；归档调用 `chat.archiveSession()` | 只需补样式与局部 Busy 防重入，归档业务行为不变 |
| FE-02 | `/chat` 内 `debug-connection-picker select` 与 `DebugOutboundCredentialPanel.vue` 的输入/按钮缺少完整交互态 | D1=A 要求对全页控件做定向补漏，而非只修截图中的归档按钮 |
| FE-03 | `ManagementRowActions.vue` 的主按钮短文案 helper 会截到 4 个字素，菜单却直接渲染完整 `label`；`ProvidersView.vue` 只给“编辑/同步”配置了 `shortLabel` | 菜单需要独立的、不截断 `shortLabel` 的可见文案 helper；完整 `label` 继续作为 `aria-label/title` |
| FE-04 | Provider 编辑页已有两个 checkbox 和条件 Broker 字段；`validateProviderDraft()` 已阻止零选择 | 业务结构已存在，只需增强显式选中态、错误定位与用户文案 |
| FE-04 | `buildOutboundIdentityContract()` 在零选择时静默补入 `REQUEST_PASSTHROUGH` | 这与 AC-08 冲突；序列化层应 fail closed，不能保留静默默认 |
| FE-04 | `backend/internal/outboundidentity/contract.go` 接受 1～2 个去重模式，并按模式校验条件块；HTTP Provider 仍只接受 `outbound-identity.v1` | 前端修复可完全复用既有后端契约，无需 API/数据迁移 |
| FE-05 | `OpenAPIImportsView.vue` 的详情头复用深色 `.openapi-modal-head`；正文只有摘要/概览各自带外边距，三个契约树贴边 | 应增加**详情专用 modifier** 和统一正文容器间距，不能影响导入/删除弹窗 |
| FE-05 | `openImportDetail()` 在详情 GET 完成后才设置 `selectedImportId`，异常没有详情内状态 | Canvas 的 Loading/Error 状态需用页面局部状态补齐，继续调用同一只读 GET |

### 1.3 API、权限与外部契约证据

- Workflow Revision 列表/差异/就绪度为 `ActionView`，激活/回滚既有命令为 `ActionPublish`；发布、试运行、停用仍走各自既有命令。
- Chat 归档调用 `POST /workspaces/{wid}/chat/sessions/{sid}:archive`，提交 `lockVersion`；后端要求 `ActionEdit`。
- Provider 查看、编辑、同步、删除继续对应既有 `ActionView`、`ActionEdit`、`ActionTest`、`ActionDelete`。
- OpenAPI 导入详情 GET 为 `ActionView`；生成 Tool Draft 为独立的 `ActionEdit` 命令。
- `docs/openapi/agent-access-v1.yaml` 是外部 AAP 契约；本单仅触及内部 Console 页面，不修改 AAP path、auth、schema 或事件协议。
- `docs/runbooks/protocol-event-console-vs-aap-entrypoints.md` 冻结 Console 与 AAP 的入口和协议边界；本单不触及消息 SSE、Agent Run 或 AAP。

## 2. 目标、非目标与不可变约束

### 2.1 目标

1. 完成 FE-01～FE-05 的布局、样式、语义、状态与无障碍修复。
2. 对长 UUID/名称/URL，以及 Empty、Loading、Error、Disabled、权限不足状态提供稳定桌面布局。
3. 用组件测试、行为测试、静态样式守卫和真实浏览器验收覆盖 AC-01～AC-12。

### 2.2 非目标

- 不重构 Workflow 编辑器、编译器、运行时、Revision 领域模型或发布流程。
- 不改变 Chat 消息协议、会话归档数据保留、Agent Run、确认流或出站凭据传递。
- 不新增 Provider/Connection 身份模式，不允许 `NONE`、`SYSTEM` 或共享业务账号。
- 不改变 OpenAPI 解析、endpoint 契约、Tool Draft 生成规则或历史导入记录。
- 不做全站设计系统重构、移动端整体适配或截图范围外的视觉翻新。
- 不新增 API、数据库迁移、审计事件、后端指标或权限规则。

### 2.3 不可变约束

- 页面打开、详情展开和样式渲染不得自动触发 compile、trial、publish、activate、rollback、disable、archive、Tool Draft 生成或 production execution。
- 所有写操作继续由既有权限和后端校验兜底；前端隐藏/Disabled 不是授权边界。
- 用户业务 Token 不写入 Provider、Connection、ChatSession、Revision、日志、本地存储或历史消息。
- 本轮只验证 CSS viewport 1180px 与 1440px 的桌面基线；不以缩小全站 `min-width` 作为修复手段。

## 3. 推荐方案与备选取舍

### 3.1 T1：实现边界

| 选项 | 内容 | 影响 |
|---|---|---|
| **A（推荐）** | 在现有页面/组件内补结构、状态、局部样式和测试；仅在已有共享组件确有复用点时扩展 helper | 回归面可控，覆盖全部 AC；无 API/数据/部署边界变化 |
| B | 先抽取新的全站 Modal、Button、ChoiceCard 设计系统，再迁移五处 | 长期复用更强，但扩大范围和回归面，不符合冻结的局部修复 |
| C | 只给截图中的选择器补 CSS，不调整结构或状态 | 改动最小，但无法可靠覆盖语义、Busy、防静默默认、Error 与无障碍 AC |

### 3.2 T2：OpenAPI 详情加载失败呈现

| 选项 | 内容 | 影响 |
|---|---|---|
| **A（推荐）** | 点击后立即打开同一个详情壳；正文显示 Loading，失败时显示可读错误与“重试/关闭”，成功后替换为详情 | 满足 Canvas 状态矩阵与 AC-12；GET 不变，布局在各状态稳定 |
| B | GET 成功后才开详情；失败仅使用页面 toast | 代码更少，但用户失去详情上下文和就地重试，Error 状态难以按详情布局验收 |

### 3.3 T3：Provider 零模式不变量

| 选项 | 内容 | 影响 |
|---|---|---|
| **A（推荐）** | 表单区块校验负责用户反馈，序列化 helper 再次断言至少一个模式；零模式时抛出本地契约错误，不发请求 | 消除静默写入，抵御未来调用路径绕过表单校验；DTO 不变 |
| B | 仅依赖 `validateProviderDraft()`，保留序列化 fallback | UI 正常路径可阻止保存，但未来调用路径仍可能静默写入透传，违反 AC-08 |

本方案按 T1=A、T2=A、T3=A 成稿并已获负责人批准；它们都不改变冻结产品范围。任何实现偏离都需要更新本文并再次获得明确确认，不能由 Forge 或 verification subagent 自行选择备选项。

## 4. 模块边界与变更面

| 模块 | 职责 | 允许变更 | 禁止变更 |
|---|---|---|---|
| `WorkflowRevisionPanel.vue` | Revision 展示与既有 action emit | 展示结构、短 ID helper、`title`、Busy/Disabled 语义 | Store 调用、命令语义、Revision 数据 |
| `styles/app.css` | Workflow 管理页共享样式 | 详情作用域内 grid/flex/overflow/focus | 全站 min-width、无关页面样式 |
| `ChatExecutionView.vue` | Chat 页面组合、归档交互 | 归档局部 Busy、防双击、缺失控件交互态 | 归档 API、消息/Run/确认协议 |
| `DebugOutboundCredentialPanel.vue` | 一次性出站凭据输入 | 输入/按钮的 hover、focus、disabled/loading 样式 | Token 生命周期、附件协议、持久化 |
| `ManagementRowActions.vue` | 通用行操作呈现 | 菜单可见文案 helper；完整 label 继续作无障碍名 | action key、事件路由、危险态规则 |
| `ProvidersView.vue` | Provider 列表与编辑表单 | shortLabel、模式卡语义/校验/文案/披露 | Provider DTO schema、Connection 规则 |
| `OpenAPIImportsView.vue` | 导入列表与详情壳 | 详情专用头、统一 body、加载/错误状态、长值提示 | 其他弹窗、解析/生成业务 |
| `ToolSchemaTreeView.vue` | 契约树/表展示 | 必要时提供内部横向滚动承载点 | 数据转换、全局表格行为 |
| 现有 Store | API 与状态事实源 | 原则上不改；只复用现有 action | 新增 endpoint、自动副作用、缓存语义变化 |
| 后端/数据库 | 权限、契约、持久化、审计事实源 | **无变更** | 路由、DTO、schema、migration、Action、审计 |

## 5. FE-01：Workflow 详情发布版本

### 5.1 结构

1. 将头部拆为：
   - 标题“发布版本”；
   - Active 与 Latest 两个独立元信息块；
   - “停用新执行”独立操作区。
2. ID 使用可收缩容器和单行省略；`title` 暴露完整值。显示 helper 可采用 `前 8 位…后 4 位`，但 DOM 的可访问名称/tooltip 必须保留完整 UUID。
3. 每个 Revision 行拆为信息区、状态区、操作区；信息区 `min-width: 0`，操作区允许 wrap。1180px 基线下若按钮总宽超出，操作区换到下一行并右对齐。
4. “激活 / 回滚 / 对比”继续 emit 现有事件；Busy/Disabled 只改变交互状态，不改变按钮宽度或标签。

### 5.2 滚动与状态

- `.workflow-detail-modal-body` 改为 `overflow-y: auto; overflow-x: hidden`。
- Revision 卡片及其所有 grid/flex 子项补 `min-width: 0`；任何必要文本截断在文本自身发生。
- Empty、Error、单 Revision、Active=Latest、多 Revision 都使用同一内容宽度。
- Diff 未选择、动作失败继续显示既有文案，不新增生命周期命令。

### 5.3 生命周期不变

FE-01 只读取 `WorkflowRevision[]`、active/latest ID 和 readiness/status，并调用既有 activate/rollback/compare/disable handler。它不创建 Draft、不发起 Compilation、不读取或改写 CompiledExecutionPlan、不创建 Revision，也不触发 trial、publish 或 production execution。

## 6. FE-02：运行调试台控件完整性

### 6.1 归档交互

1. 为 `.chat-inline-action` 定义与页面次级 ghost 控件一致的 28～32px 高度、圆角、边框、背景与文字；归档不是删除，不使用 danger 色。
2. 增加页面局部 `archivingSession`：
   - 已 Busy 时直接返回；
   - 请求期间 `disabled`、`aria-busy=true`，维持稳定宽度并显示 Loading；
   - `finally` 复位；
   - 成功后沿用 Store 返回的 `ARCHIVED` 会话和现有只读态。
3. 请求仍由 `chat.archiveSession()` 提交当前 `lockVersion`；失败沿用现有错误呈现，不乐观修改会话状态。

### 6.2 全页定向样式补漏

按 D1=A 逐项检查顶部上下文选择、历史会话、运行详情、风险确认、消息跳转、出站凭据、输入与发送控件。现有自定义控件不重写，只对缺失项补齐：

- `.chat-inline-action`；
- `debug-connection-picker select` 的 appearance、边框、背景、箭头/内距、focus-visible、disabled；
- `DebugOutboundCredentialPanel` 的 password/datetime-local 输入与附加/清除按钮的 hover、focus-visible、disabled/loading；
- 测试发现的同类原生漏出项，但不得借此重排页面。

### 6.3 业务边界

- 归档继续保留消息和会话记录，只把会话变为只读并关闭活动 stream。
- 归档不删除、不自动新建会话、不保存 Token、不改变 SSE 或 Agent Run。
- 已归档会话的发送入口继续 Disabled；后端仍拒绝非法发送。

## 7. FE-03：Provider 行操作短文案

1. 在 `ProvidersView.vue` 为四个 menu action 指定精确 `shortLabel`：
   - `编辑`
   - `同步`
   - `查看能力资产`
   - `删除`
2. 在 `ManagementRowActions.vue` 区分两个 helper：
   - 主按钮沿用现有紧凑截断策略；
   - 菜单可见文案使用完整 `shortLabel?.trim() || label.trim()`，**不做 4 字素截断**。
3. 菜单项 `aria-label` 与 `title` 继续使用完整 `label`，例如“查看 Mock Corp Expense 的能力资产”。
4. action key、当前行 Provider 对象、同步 Loading/Disabled reason、删除 danger tone 与确认流程均不变。
5. 对未提供 `shortLabel` 的其他使用方，菜单继续显示完整 `label`，避免共享组件回归。

## 8. FE-04：Provider 用户调用身份

### 8.1 呈现与无障碍

1. 区块标题和主说明使用产品 v1.0 的冻结用户文案，先说明“Provider 可多选、Connection 只能固定选择一种”。
2. 两张模式卡保持原生 checkbox 语义：
   - input 采用可访问的 visually-hidden 方式，不使用 `pointer-events: none` 破坏交互；
   - checked 时显示 checkmark +“已支持”；
   - 未选时显示明确未选状态；
   - `:focus-visible` 在卡片代理上形成可见 focus ring；
   - 不只靠绿色表达选中。
3. Broker/OBO 勾选时才渲染 Broker 字段；取消 Broker 只隐藏/排除 Broker block，不改透传选择。
4. 用户语言保留两种模式、Token 是否保存和 Provider/Connection 分工；`USER`、`private_key_jwt`、`ACCESS_TOKEN`、`expiresAt` 等精确约束放入原生 `<details>` 或等价的“查看技术约束”披露区。

### 8.2 校验与序列化

1. 新增身份区块级 error state，或由结构化校验结果将错误归属到该区块。
2. 零选择保存时：
   - 不调用 Store/API；
   - 区块设置 `aria-invalid=true`，错误文案 `role=alert`；
   - focus/scroll 到区块或第一张模式卡；
   - 全局表单错误可保留摘要，但不能成为唯一定位。
3. 删除 `buildOutboundIdentityContract()` 中静默补 `REQUEST_PASSTHROUGH` 的 fallback；序列化 helper 对零模式抛出本地契约错误，形成第二道不变量保护。
4. 有效选择继续生成原 DTO：
   - `schemaVersion: "outbound-identity.v1"`；
   - `supportedModes` 为 1～2 个既有枚举；
   - `supportedSubjectTypes: ["USER"]`；
   - 仅在选择 Broker 时包含 `brokerObo`；
   - 透传继续仅允许 Access Token 和一次性请求语义。

### 8.3 安全边界

- Provider 只声明能力和 Broker 机器认证配置，不接收最终用户业务 Token。
- Connection 仍在 Provider 支持集合中固定一种模式，不允许一次 Connection 同时使用两种。
- 用户业务 Token 仍只通过既有 run-scoped attachment/调用路径消费，不进入本地存储、会话历史或配置 DTO。

## 9. FE-05：OpenAPI 导入详情

### 9.1 详情专用视觉边界

1. 只给详情头增加 modifier（例如 `.openapi-detail-modal-head`），使用浅色背景、既有管理详情边框、图标/标题/副标题/关闭按钮层级；不改导入、新建、删除确认头。
2. `.openapi-detail-modal-body` 作为唯一正文滚动容器：
   - `padding: 20px`；
   - 统一 12～16px section gap；
   - `overflow-y: auto; overflow-x: hidden`；
   - 摘要与概览移除各自重复的外边距。
3. Header/Footer 保持现有固定区域；920px modal 宽度不变。
4. 结构表的横向溢出由表内部 wrapper 承担；共享 `ToolSchemaTreeView` 若需调整，必须用详情作用域或无副作用的内部滚动容器，不能把 overflow 传给 modal/page。

### 9.2 Loading、Error、Success

采用 T2=A：

1. 点击记录时先保存触发元素与 `selectedImportId`，打开稳定的详情壳。
2. `detailLoading=true` 时只显示 Loading，不把未知详情当 Empty。
3. 调用既有 `integration.loadOpenAPIImportDetail(record)`：
   - 成功：渲染 Store 中的 detail；
   - 失败：保留壳，显示可读错误及重试/关闭；
   - `finally`：清除 Loading；
   - 重试仍是同一个 GET。
4. 关闭时清理局部 Loading/Error，恢复触发元素焦点。
5. 打开/重试详情绝不调用 `generateToolDrafts()`；“生成 Tool 草稿”仍是用户显式触发的独立命令。

### 9.3 长值与 Empty

- 文件名、来源、Provider、Connection、URL 使用可收缩容器与 ellipsis，`title`/可访问名称保留完整值。
- Ready/Issues 等状态保留文本，不只靠颜色。
- 请求参数、Body、响应合法为空时继续使用现有 Empty 文案，且占据与非空区相同的有边界内容区。

## 10. 数据、迁移、API、错误与兼容

### 10.1 数据与迁移

| 项目 | 结论 |
|---|---|
| 数据库 schema | 无变化 |
| 数据迁移/回填 | 无 |
| 历史 Revision/ChatSession/Provider/Connection/OpenAPI Import | 不改写、不删除 |
| 缓存/索引 | 无变化 |
| Token 数据 | 不新增存储，不改变既有一次性消费边界 |

### 10.2 现有 API 复用

| 页面动作 | 既有调用 | 本方案 |
|---|---|---|
| Workflow 版本列表/差异/readiness | 现有 GET | 请求、响应、错误码不变 |
| Workflow 激活/回滚/停用 | 现有命令 endpoint | 只调整按钮布局；不自动调用 |
| Chat 归档 | `POST .../chat/sessions/{sid}:archive` + `lockVersion` | 增加前端 Busy 防重入；payload 不变 |
| Provider 查看/保存/同步/删除 | 现有 Provider API | DTO 与 endpoint 不变 |
| OpenAPI 详情 | `GET .../openapi-imports/{id}` | 用局部 Loading/Error 呈现同一调用 |
| 生成 Tool Draft | `POST .../openapi-imports/{id}:generate-tools` | 逻辑不变，且不由详情打开触发 |

后端同时注册的 canonical `__command` 与前端兼容短路径不在本单调整；不新增 alias，不修改 `docs/openapi/agent-access-v1.yaml`。

### 10.3 错误与兼容

- 401/403、404、409/lockVersion 冲突、422/校验错误及 5xx 继续由既有客户端错误归一化处理。
- Chat 归档失败不乐观切换只读；用户可在错误消失后重试。
- Provider 零模式为前端本地校验错误；若绕过前端，后端 `outbound-identity.v1` 校验仍拒绝。
- OpenAPI 详情 GET 失败不伪装为空数据；重试是只读且可重复。
- 可见短文案变化不改变 action key、自动化 API、无障碍完整标签或历史数据。

## 11. 生命周期、状态机、并发与幂等

### 11.1 Workflow 领域对象严格分离

| 概念 | 事实职责 | 本单可做 | 本单禁止 |
|---|---|---|---|
| Draft | 可编辑的 `WorkflowGraphDraft` 与版本/锁 | 在说明中保持区分 | 因打开详情而保存或改写 |
| Compilation | 对指定 Draft 的校验/编译结果，含 issues、plan hash | 不涉及 | 自动 compile |
| CompiledExecutionPlan | Compilation 产出的可执行 plan | 只作为 Revision 内既有快照概念 | 前端重算或改写 |
| Revision | publish 后的不可变发布快照，可 Active/Latest/Retired | 展示、比较、显式激活/回滚 | 通过视觉修复创建/删除 Revision |
| trial | 以显式试运行入口执行当前编译产物，不等同生产 | 不涉及 | 打开详情或保存样式时触发 |
| publish | 显式把有效 Compilation 固化为 Revision | 仅保证按钮未被裁切 | 自动发布或改变权限 |
| production execution | 使用 Active 已发布 Revision 执行 | 不涉及 | 使用 Draft/未发布 plan 代替 Active Revision |

Revision 面板的 “Active” 与 “Latest” 是两个独立指针/状态：Latest 不自动成为 Active；rollback 继续是对历史 Revision 的显式 activate 语义。任何 UI 重排不得合并这些概念。

### 11.2 前端状态与并发

| 流程 | 状态 | 并发/幂等策略 |
|---|---|---|
| Workflow action | 复用既有 busy/status | Busy 时按钮 Disabled；不增加自动 retry 或重复命令 |
| Chat archive | `idle → archiving → archived/error` | 本地 guard 防双击；后端 `lockVersion` 处理并发更新 |
| Provider save | `idle → validating → saving → success/error` | 零模式在请求前拒绝；既有 saving guard 防重复 |
| OpenAPI detail | `closed → loading → success/error → retry/close` | GET 可重复；用请求归属检查避免迟到响应覆盖已切换记录 |
| Tool Draft generate | 复用 `generatingDraftsByImportId` | 与详情加载分离；只由显式动作触发 |

OpenAPI 详情若允许在一次 GET 未完成时切换记录，实现必须用 request token/目标 ID 比对；迟到响应可以更新 Store 缓存，但不得覆盖当前弹窗的 Loading/Error/选中记录。

## 12. 权限、安全与审计

| 范围 | 既有授权 | 保持方式 |
|---|---|---|
| Workflow 查看/对比 | `ActionView` | 只读信息继续按当前权限提供 |
| Workflow 激活/回滚 | `ActionPublish` | 不新增入口、不降权；后端仍是最终边界 |
| Chat 归档 | `ActionEdit` | 按既有入口与 403 处理 |
| Provider 保存/删除/同步 | `ActionEdit` / `ActionDelete` / `ActionTest` | shortLabel 与样式不改变授权 |
| OpenAPI 详情/生成 | `ActionView` / `ActionEdit` | 详情 GET 与生成命令严格分离 |

安全与审计结论：

- 无新业务动作，因此不新增审计 event type，也不绕过现有 service/repository 侧审计。
- 归档、Provider 更新/同步/删除、Revision 操作、Tool Draft 生成继续走原 handler，现有审计和副作用保持。
- 完整对象名仍在 `aria-label/title`，短文案不降低误操作防护；删除继续 danger + 二次确认。
- Token 不出现在 UI 日志、错误文本、Provider DTO、Revision 或本地存储。
- 键盘焦点、checked、error、Ready/Issues、Disabled 与 danger 不能只靠颜色。

## 13. 可观测性

本单不引入新的后端 metric、log、trace 或审计字段。验收使用以下既有可见信号：

- API 失败继续进入当前页面错误/toast 机制；OpenAPI 详情增加就地 Error，但不吞掉原始归一化错误。
- Busy/Disabled/`aria-busy` 可从 DOM 与组件测试观察。
- 浏览器验收记录 1180/1440px 下是否出现页面/弹窗横向滚动、焦点是否可见、按钮是否被裁切。
- 网络面板必须证明：打开页面/详情没有新增写请求，OpenAPI 详情只发 GET，归档/生成仅在用户显式操作时发出。

## 14. 前后端影响与预计文件

### 14.1 前端预计修改

- `frontend/src/components/workflow/WorkflowRevisionPanel.vue`
- `frontend/src/styles/app.css`
- `frontend/src/views/ChatExecutionView.vue`
- `frontend/src/components/DebugOutboundCredentialPanel.vue`
- `frontend/src/components/ManagementRowActions.vue`
- `frontend/src/views/ProvidersView.vue`
- `frontend/src/views/OpenAPIImportsView.vue`
- `frontend/src/components/ToolSchemaTreeView.vue`（仅在现有内部 wrapper 无法承载横向滚动时）
- 对应现有测试文件；可新增 `WorkflowRevisionPanel.test.ts` 以隔离布局语义测试

### 14.2 后端、数据库与文档契约

- 后端生产代码：无修改。
- 数据库 migration：无。
- `docs/openapi` / AAP SDK：无修改。
- Runbook：无修改。
- 产品与 UI 设计：保持冻结，不反向改写。

## 15. 测试与验证

### 15.1 自动化

| 测试层 | 文件/范围 | 核心断言 |
|---|---|---|
| Workflow component/content | 新增或扩展 `WorkflowRevisionPanel.test.ts`、`workflow-view-content.test.ts`、`WorkflowView.test.ts` | Active/Latest 独立、完整 title、操作 wrap、Empty/Error/Busy、不改变 emit |
| Chat content/store | `chat-execution-view-content.test.ts`、`chat.test.ts` | 次级归档样式、Busy 防双击、lockVersion payload、ARCHIVED 只读、凭据控件交互态 |
| Shared row actions | `ManagementRowActions.test.ts` | 菜单显示完整 shortLabel（不截 4 字），aria/title 为完整 label，danger/disabled 不变 |
| Provider behavior | `providers-view-behavior.test.ts` | 四个精确短文案、双 checkbox、焦点/checkmark、零模式不发请求、区块 alert、无静默 fallback、条件 DTO |
| OpenAPI behavior/content | `openapi-imports-view-behavior.test.ts`、`openapi-imports-view-content.test.ts` | 详情专用浅色头、Loading/Error/retry、焦点恢复、长值 title、Empty、打开不生成 Tool |
| Build/type | 前端标准命令 | TypeScript/Vue build 通过，无新的 lint/type 错误 |

建议执行：

```bash
cd frontend
npm test -- --run
npm run build
```

若仓库脚本支持定向测试，可先运行上述相关测试文件，再执行完整前端测试。

### 15.2 真实浏览器

在 Chrome、CSS viewport 1180px 与 1440px 下执行 Canvas S1～S11：

1. Workflow 无/单/多 Revision、长 UUID、Active=Latest、Busy/Disabled/Error。
2. Chat 空消息、运行中、失败、待确认、归档后、侧栏打开；Tab 遍历全部控件。
3. Provider 超长中英文名菜单；双模式、单模式、零模式、Broker 条件字段与技术披露。
4. OpenAPI 长文件/Provider/Connection/URL，三种合法 Empty、详情 GET Loading/Error/retry、内部表格横向滚动。
5. 记录 `document.documentElement.scrollWidth === clientWidth`；modal body 无横向滚动，必要表格只在自身滚动。
6. 观察网络请求，证明没有打开即写入或自动生命周期副作用。

## 16. FE 与 AC 完整映射

| FE | AC | 技术落点 | 验证 |
|---|---|---|---|
| FE-01 | AC-01 | 发布头拆为标题、Active、Latest、独立操作区；长 ID title/ellipsis；body 仅纵向滚动 | component + 1180/1440 browser |
| FE-01 | AC-02 | Revision 信息/状态/操作分区，`min-width:0`，操作 wrap，Busy 宽度稳定 | component + long ID browser |
| FE-01 | AC-03 | Empty/Error 共用稳定宽度，不把长文案提升为横向滚动 | content test + browser |
| FE-02 | AC-04 | `.chat-inline-action` 次级样式；归档 Busy guard；复用 archive API 与 ARCHIVED 只读 | view/store test + browser |
| FE-02 | AC-05 | `/chat` 全交互控件补漏，focus-visible/disabled/loading 完整 | static selector guard + Tab/browser |
| FE-03 | AC-06 | menu visual helper 使用不截断 shortLabel；完整 label 留在 aria/title；danger/disabled 不变 | shared component + provider behavior |
| FE-04 | AC-07 | 原生 checkbox 语义、显式 checkmark/“已支持”、多选说明与 focus ring | provider mount + keyboard/screen-reader smoke |
| FE-04 | AC-08 | 区块 alert + 阻止请求；serializer 拒绝零模式；Broker 条件 block | provider behavior/DTO assertions |
| FE-04 | AC-09 | 用户语言主说明 + 技术约束 disclosure，保留 USER/Access Token/有效期/private_key_jwt | content test + browser |
| FE-05 | AC-10 | 详情专用浅色头、20px body、统一 gap、固定 head/footer、body 纵滚 | content test + 1180/1440 browser |
| FE-05 | AC-11 | 长值 ellipsis/title、Empty 容器、表内横滚 | behavior + long/empty browser |
| FE-01～05 | AC-12 | 不改权限/API/数据；Busy/Error/Disabled 按现有边界；无自动写副作用 | 请求 spy、权限回归、network inspection |

## 17. 发布、回滚与风险

### 17.1 发布

1. 这是前端静态资产变更，沿用现有前端构建和部署流程；不需要数据库迁移、双写、feature flag 或后端联动窗口。
2. 发布门槛：相关定向测试、完整前端测试、build、1180/1440 Chrome 验收全部通过。
3. 发布后 smoke：五个入口可打开；权限不足用户未获得新写能力；打开详情无写请求。

### 17.2 回滚

- 回滚对应前端提交/静态资产即可。
- 无 schema/data/API 变化，因此不执行数据回滚。
- 若只发现某一页面视觉回归，可回滚该页面作用域变更；不得以恢复静默身份模式 fallback 作为回滚手段，零模式必须继续 fail closed。

### 17.3 风险

| 风险 | 影响 | 缓解/停止条件 |
|---|---|---|
| 共享 `ManagementRowActions` 改动影响其他页面 | 菜单文案回归 | helper 对无 shortLabel 保持原行为；覆盖共享组件使用方测试 |
| `overflow-x:hidden` 掩盖真实溢出 | 内容不可访问 | 同时修 `min-width:0`/wrap/title；浏览器检查裁切与完整值入口 |
| Chat 全页补漏扩成重构 | 回归范围失控 | 仅改无自定义态的现有控件；不改 DOM 信息架构 |
| Provider input 视觉隐藏方式不当 | 键盘/读屏不可用 | 保留原生 input，测试 checked/focus/Space 操作 |
| OpenAPI 迟到响应覆盖当前记录 | 显示错误详情 | request token/record ID 校验；切换/关闭行为测试 |
| 详情 Loading/Error 需要新接口 | 可能越界 | 只用局部状态和既有 GET；若实现证明需要 API，立即停止回产品确认 |
| 实现发现需改权限、审计、数据或生命周期 | 违反冻结边界 | 立即停止，不做默认决定，更新技术方案并重新获负责人批准 |

## 18. 负责人批准与变更控制

### 18.1 批准记录

负责人 chenow 在 Issue 评论 `e331fd53-6cf5-4792-9021-45210d7dd070` 明确批准当前版本：

> 批准技术方案 v0.1；T1=A、T2=A、T3=A，按此方案进入 checklist 阶段。

冻结结论：

| 决策 | 已批准选项 | 实施含义 |
|---|---|---|
| T1 | A | 仅在现有组件边界内补结构、局部状态、样式和测试，不扩为全站设计系统重构 |
| T2 | A | OpenAPI 详情使用稳定壳呈现 Loading / Error / 重试，继续复用同一只读 GET |
| T3 | A | Provider 零模式同时由区块校验和序列化断言 fail closed，不静默写入透传 |

### 18.2 实施授权与停止条件

- 已据此生成 `docs/design/zkl-59-frontend-page-fixes-implementation-checklist.md` v1.0，作为 Forge 的严格串行实施与验证记录。
- Forge 只能按 checklist 的严格顺序实施；每项必须由一个新建的临时只读 verification subagent 独立验证并给出 PASS 后才能进入下一项。
- 本批准不授权 production 部署、production execution、数据库 mutation、历史数据修复或产品范围扩展。
- 如果 checklist 或实现暴露新的设计决定，或需要改变范围、架构、API、数据、权限、安全、迁移、兼容、部署、审计或验收，立即暂停并回到 Knower；涉及产品冻结项时同时回到 Atlas/负责人确认。

## 19. 版本记录

| 版本 | 状态 | 说明 |
|---|---|---|
| v0.1 | Approved / Frozen | 基于产品 v1.0、Canvas UI v0.1、真实前后端与测试证据形成；负责人在评论 `e331fd53-6cf5-4792-9021-45210d7dd070` 批准 T1=A、T2=A、T3=A |
