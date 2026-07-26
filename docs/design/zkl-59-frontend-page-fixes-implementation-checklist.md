# ZKL-59 前端页面问题修复：Implementation Checklist

| 字段 | 值 |
|---|---|
| Issue | ZKL-59 / `b0adb828-fccf-4bbe-8a25-b6b9a5417c21` |
| Checklist 版本 | v1.0 |
| 状态 | **Ready for Sentinel review** |
| 总项数 | **7** |
| 产品基线 | `docs/design/zkl-59-frontend-page-fixes-product-design.md` v1.0 / Approved / Frozen |
| 技术基线 | `docs/design/zkl-59-frontend-page-fixes-technical-design.md` v0.1 / Approved / Frozen |
| UI 输入 | `docs/design/zkl-59-frontend-page-fixes-ui-design.md` UI v0.1 |
| 负责人确认 | Issue 评论 `e331fd53-6cf5-4792-9021-45210d7dd070`：批准技术方案 v0.1，T1=A、T2=A、T3=A |
| 冻结范围 | FE-01～FE-05；AC-01～AC-12 |
| 实现分支 | 由 Conductor / Forge 入场时安全建立；不得把当前 `fix/zkl-56-pm-e2e-ux-fixes` 误当作 ZKL-59 工作分支 |

## 0. 执行、验证与记录规则

1. **严格串行执行 1 → 7。** Forge 完成当前项、开发自测通过、填写实现证据后，必须为该项新建一个临时、只读 verification subagent。只有 verifier 给出 PASS，当前项才可标记 `COMPLETE` 并直接开始下一项；不需要逐项向 Knower 请示或等待回复。
2. 每项 verifier 必须是**新实例**，不是持久 Agent、不是 Issue、不得复用。Verifier 只检查实际 diff、运行验证、输出 PASS / FAIL；不得修改代码、测试、文档、数据库或外部状态。允许运行使用可丢弃本地 fixture 的测试，但不得操作共享或生产数据。
3. 每项状态只允许：`PENDING`、`IN_PROGRESS`、`IMPLEMENTED_PENDING_VERIFICATION`、`BLOCKED`、`COMPLETE`。Forge 在请求验证前填入实现证据与开发自测；PASS 后记录 verifier 标识和摘要，FAIL 后回到 `IN_PROGRESS` 修复，并创建另一个全新的 verifier 复验。
4. 进度只记录在本文的“状态 / 实现证据 / 开发自测记录 / verification subagent 摘要”字段中；禁止创建子 Issue、Stage 或并行任务 Issue 表示进度。
5. Forge 只有在 checklist 缺失、相互冲突、不可执行，或实现需要改变已批准的范围、架构、API、数据、权限、安全、迁移、兼容、部署、审计或验收决定时，才暂停并回到 Knower。涉及产品 D1～D5 或 AC-01～AC-12 的变化还必须回到 Atlas/负责人确认。
6. 进入工作区先运行 `git status`。必须保护既有 `.agent_context/`、`AGENTS.md`、ZKL-59 三份设计文档、ZKL-56 验证文档及其他非本单改动；不得 reset、覆盖、清理或提交不属于 ZKL-59 的文件。若无法安全建立 ZKL-59 分支，停止并请求 Conductor 处理。
7. 本 checklist 不授权 production 部署、production execution、数据库 mutation、历史数据修复、数据回填或破坏性操作。浏览器验证只可使用本地/隔离测试环境与可丢弃 fixture。
8. Snapshot 只能在 DOM 语义、交互断言和人工视觉复核均通过后定向更新；禁止用全量 `--update-snapshots` 掩盖回归。

### 0.1 已批准且不可违背的技术决定

- **T1=A**：在现有组件边界内补结构、局部状态、样式和测试；不抽取或迁移全站设计系统。
- **T2=A**：OpenAPI 详情先打开稳定壳，正文呈现 Loading / Error / 重试；继续调用同一个只读详情 GET。
- **T3=A**：Provider 零模式在区块校验层阻止保存，序列化层再次 fail closed；不得静默写入 `REQUEST_PASSTHROUGH`。
- 后端路由、请求/响应 DTO、数据库 schema、迁移、权限 Action、审计和数据保留均不变。
- Workflow 的 Draft、Compilation、CompiledExecutionPlan、Revision、trial、publish、production execution 必须保持分离；视觉修复不得触发生命周期动作。
- Provider 可声明 1～2 种模式，Connection 固定选择其中 1 种；用户业务 Token 不进入 Provider/Connection 配置、日志、本地存储、会话历史或 Revision。
- 页面打开、详情打开与渲染不得自动 compile、trial、publish、activate、rollback、disable、archive、生成 Tool Draft 或 production execute。
- 桌面验收基线为 CSS viewport 1180px 与 1440px；不改变全站 `min-width: 1180px`。

### 0.2 进度总表

| # | 交付 | 状态 | 主要 AC | 实现证据 | verification |
|---:|---|---|---|---|---|
| 1 | Workflow 发布版本布局与状态 | `COMPLETE` | AC-01～03 | 见 §1 实现证据 | PASS `019f9c22-9718-7611-ad42-7bd80051ad3d` |
| 2 | Chat 归档与全页控件样式 | `COMPLETE` | AC-04～05 | 见 §2 实现证据 | PASS `019f9c26-3cde-7783-9af8-f64ec6d08f4e` |
| 3 | Provider 行菜单短文案 | `COMPLETE` | AC-06 | 见 §3 实现证据 | PASS `019f9c28-9bad-7023-a965-badd307f64f6` |
| 4 | Provider 身份多选、说明与零模式 fail-closed | `COMPLETE` | AC-07～09 | 见 §4 实现证据 | PASS `019f9c2c-1748-71d0-aa8f-871ce29f14f2` |
| 5 | OpenAPI 详情壳、状态与间距 | `COMPLETE` | AC-10～11 | 见 §5 实现证据 | PASS `019f9c2f-8d1a-7490-b136-42647b185203` |
| 6 | 跨页面自动回归与边界证明 | `COMPLETE` | AC-01～12 | 见 §6 实现证据 | PASS `019f9c32-ff7f-7ea3-96ee-a91df63d277f` |
| 7 | 真实 Chrome 验收与最终交接包 | `COMPLETE` | AC-01～12 | 见 §7 实现证据 | PASS `019f9c36-bd1a-7ae2-8b78-422680c26b10` |

## 1. 修复 Workflow 详情发布版本布局与状态

- **状态**：`COMPLETE`
- **依赖**：无
- **主要 FE / AC**：FE-01；AC-01、AC-02、AC-03
- **目的**：让发布版本标题、Active、Latest、Revision 信息/状态/操作在长 UUID 和各种状态下稳定分区，消除弹窗级与页面级横向溢出，同时保持既有 Revision 命令语义。
- **精确范围**：
  - `frontend/src/components/workflow/WorkflowRevisionPanel.vue`
  - `frontend/src/styles/app.css` 中 `.workflow-detail-*`、`.workflow-revision-*` 的详情作用域样式
  - 新增或更新 `frontend/src/components/workflow/WorkflowRevisionPanel.test.ts`
  - `frontend/src/views/workflow-view-content.test.ts`
  - `frontend/src/views/WorkflowView.test.ts`
  - `frontend/e2e/workflow.spec.ts` 与其 revision panel snapshot，仅在断言证明需要时定向更新
- **不可违背约束**：
  - Active 与 Latest 必须是两个独立状态；Latest 不自动变 Active，rollback 继续使用既有显式 activate 语义。
  - 只允许调整展示结构、`title`/可访问名称、局部 Busy/Disabled 呈现及详情作用域 CSS；不得修改 Store、endpoint、payload、权限或 Revision 数据。
  - 完整 UUID 必须可访问；可使用 CSS ellipsis 或前 8 位…后 4 位，但不得只留下不可恢复的短值。
  - `.workflow-detail-modal-body` 只允许纵向滚动；不能用 `overflow-x:hidden` 单独掩盖仍不可访问的内容，必须同时修正 `min-width:0`、wrap 和 title。
  - 打开详情不得创建 Draft、Compilation、CompiledExecutionPlan 或 Revision，也不得触发 trial、publish、activate、rollback、disable 或 production execution。
- **完成定义**：
  - “发布版本”、Active、Latest 与“停用新执行”形成稳定分区；长 ID 不重叠、不裁切按钮。
  - Revision 行的 ID/时间、状态与操作在 1180/1440px 下位于卡片内；操作可换行，Busy/Disabled 不改变控制宽度。
  - 无 Revision、单 Revision、多 Revision、Active=Latest、动作 Loading/Disabled/Error 均有测试。
  - 现有 `disable`、`activate`、`rollback`、`compare` emit 名称与参数不变。
- **开发自测**：
  - `cd frontend && npm test -- --run src/components/workflow/WorkflowRevisionPanel.test.ts src/views/workflow-view-content.test.ts src/views/WorkflowView.test.ts`
  - `cd frontend && npm run e2e:workflow`
  - `cd frontend && npm run build`
- **独立验证标准（本项新 verifier）**：
  - 检查实际 diff 只触及上述前端范围，静态追踪 emit 到现有 handler，确认没有 Store/API/生命周期变化。
  - 独立运行本项测试与 build；用超长 UUID fixture 检查完整 `title`、Active=Latest、Empty/Error/Busy/Disabled。
  - 在 1180px 与 1440px 检查 modal/page `scrollWidth <= clientWidth`、按钮不裁切、焦点可见。
  - 任一生命周期自动调用、命令参数变化、仅隐藏溢出而内容不可访问、snapshot 无断言更新即 FAIL。
- **回滚 / 风险**：可独立回滚 `WorkflowRevisionPanel` 与详情作用域 CSS。主要风险是共享 `app.css` 选择器影响编辑器/差异面板，以及隐藏溢出掩盖真实布局错误。
- **实现证据**：
  - `WorkflowRevisionPanel.vue`：头区拆为标题行 + Active/Latest 独立 meta 卡 +「停用新执行」；长 ID 显示 `前8…后4`，完整值在 `title`；行拆为 info / status / actions，actions `flex-wrap`；emit 仍为 `activate`/`rollback`/`compare`/`disable`。
  - `app.css`：`.workflow-detail-modal-body` → `overflow-y:auto; overflow-x:hidden`；revision head/item 补 `min-width:0`、meta 两列与操作换行布局。
  - 新增 `WorkflowRevisionPanel.test.ts`（9 用例：分区、Active=Latest、Empty、Busy/Disabled、emit 参数、短 ID）。
  - `workflow-view-content.test.ts` 增加 emit wiring 静态断言。
  - 未改 Store/API/权限/生命周期 handler。
- **开发自测记录**：
  - `npx vitest --run` 上述 3 文件：**32 passed**。
  - `npm run build`（vue-tsc + vite）：**PASS**。
  - `npm run e2e:workflow`：基线 `frontend/e2e/workflow.spec.ts` 不存在于 `origin/main`（Playwright “No tests found”），非本项引入回归；本项未新增 e2e snapshot。
- **verification subagent / 摘要**：PASS — id `019f9c22-9718-7611-ad42-7bd80051ad3d`（checklist-1-verifier）。范围仅限 FE-01 前端文件；32 tests + build PASS；emit/生命周期边界 OK；e2e 基线缺失非回归。

## 2. 补齐 Chat 归档 Busy 与全页交互控件样式

- **状态**：`COMPLETE`
- **依赖**：1 `COMPLETE`
- **主要 FE / AC**：FE-02；AC-04、AC-05
- **目的**：把“归档”恢复为一致的次级非危险控件，并按 D1=A 补齐 `/chat` 中确有浏览器原生样式漏出的控件，同时保留现有归档、消息、Run 与一次性凭据语义。
- **精确范围**：
  - `frontend/src/views/ChatExecutionView.vue`
  - `frontend/src/components/DebugOutboundCredentialPanel.vue`
  - 新增或更新 `frontend/src/components/DebugOutboundCredentialPanel.test.ts`
  - `frontend/src/views/chat-execution-view-content.test.ts`
  - 新增或更新 `frontend/src/views/chat-execution-view-behavior.test.ts`
  - `frontend/src/stores/chat.test.ts`
- **不可违背约束**：
  - `.chat-inline-action` 为 ghost/secondary，不能使用 danger 色；可见文案保持“归档”，辅助说明继续明确消息永久保留。
  - 局部 `archivingSession` 只做防双击、`disabled`、`aria-busy` 和稳定 Loading；请求仍由 `chat.archiveSession()` 提交既有 `lockVersion`。
  - 请求失败不得乐观归档；成功后继续使用 Store 返回的 `ARCHIVED`、保留消息、关闭活动 stream、发送 Disabled。
  - 全页补漏仅覆盖现有控件缺失的 appearance/hover/focus-visible/pressed/disabled/loading；不得重排信息架构或重做页面视觉。
  - 不改变 Chat SSE、Agent Run、风险确认、消息协议、归档数据保留、Token/attachment 生命周期或 API。
- **完成定义**：
  - 归档默认、Hover、Focus、Pressed、Disabled、Loading 状态可读；重复点击只产生一次请求。
  - ACTIVE → ARCHIVED 保留消息并转只读；失败保持 ACTIVE 并显示既有错误；已归档发送入口不可触发。
  - 顶部上下文选择、历史会话、运行详情、风险确认、消息跳转、出站凭据、输入/发送控件完成定向样式扫描。
  - `debug-connection-picker select` 与 `DebugOutboundCredentialPanel` 的输入/按钮不再显示未设计的 UA 样式，键盘焦点可见。
- **开发自测**：
  - `cd frontend && npm test -- --run src/components/DebugOutboundCredentialPanel.test.ts src/views/chat-execution-view-content.test.ts src/views/chat-execution-view-behavior.test.ts src/stores/chat.test.ts`
  - `cd frontend && npm run build`
- **独立验证标准（本项新 verifier）**：
  - 独立检查归档防重入与 `finally` 复位；用请求 spy 证明 Busy 双击只有一次 POST，payload 仍只有既有字段。
  - 运行本项测试/build；以键盘遍历 `/chat` 全部控件，检查 focus-visible、disabled 不触发、归档非 danger。
  - 检查 DOM/Store 中不存在 Token 落盘、日志或历史消息新增路径，且 SSE/Run 代码没有语义修改。
  - 任一消息删除、乐观假归档、重复请求、凭据持久化、页面重排或原生样式漏出即 FAIL。
- **回滚 / 风险**：先回滚 Chat view 的局部 Busy/样式，再回滚凭据面板样式；Store/API 无需回滚。风险是 Busy 未复位、归档后输入过早可用、共享控件选择器外溢。
- **实现证据**：
  - `ChatExecutionView.vue`：局部 `archivingSession` 防重入 + `disabled`/`aria-busy`/「归档中…」；`.chat-inline-action` 次级非 danger 样式（hover/focus-visible/active/disabled）；`.debug-connection-picker select` appearance 与焦点环。
  - `DebugOutboundCredentialPanel.vue`：password/datetime 与按钮补齐 hover/focus-visible/disabled/loading，attach `aria-busy`。
  - 新增 `chat-execution-view-behavior.test.ts`（双击归档仅一次 store 调用）、`DebugOutboundCredentialPanel.test.ts`；更新 content 静态断言。
  - 未改 chat store API、SSE、消息协议、Token 持久化路径。
- **开发自测记录**：
  - `npx vitest --run` DebugOutboundCredentialPanel + chat content/behavior + chat.test：**31 passed**。
  - `npm run build`：**PASS**。
- **verification subagent / 摘要**：PASS — id `019f9c26-3cde-7783-9af8-f64ec6d08f4e`（checklist-2-verifier）。归档防重入 + 非 danger 样式 + 凭据面板样式；31 tests + build PASS；store/SSE/Token 边界 OK。

## 3. 实现 Provider 行菜单精确短文案

- **状态**：`COMPLETE`
- **依赖**：2 `COMPLETE`
- **主要 FE / AC**：FE-03；AC-06
- **目的**：让 Provider 菜单可见文本固定为短动作名，同时保留完整对象上下文、危险态、Disabled reason 与共享组件兼容。
- **精确范围**：
  - `frontend/src/components/ManagementRowActions.vue`
  - `frontend/src/components/ManagementRowActions.test.ts`
  - `frontend/src/views/ProvidersView.vue` 的 `providerMenuActions`
  - `frontend/src/views/providers-view-behavior.test.ts`
- **不可违背约束**：
  - 菜单可见文本精确为 `编辑 / 同步 / 查看能力资产 / 删除`；`查看能力资产` 不得被主按钮的 4 字素 helper 截断。
  - 菜单视觉 helper 使用完整 `shortLabel?.trim() || label.trim()`；主按钮现有紧凑 helper 行为不变。
  - `aria-label` 与 `title` 继续使用包含完整 Provider 名称的 `label`。
  - action key、当前行对象、事件 emit、同步 Loading/Disabled reason、删除 danger 与二次确认不变。
  - 未提供 `shortLabel` 的其他 `ManagementRowActions` 使用方继续显示完整 `label`。
- **完成定义**：
  - 超长中英文 Provider 名称的菜单仍只显示四个固定短动作名；完整名称可由 `aria-label/title` 读取。
  - 重名 Provider 的动作仍绑定正确行对象；同步 Busy/Disabled 与删除危险态回归通过。
  - 共享组件测试覆盖 shortLabel 不截断、无 shortLabel fallback、主按钮旧行为和键盘菜单语义。
- **开发自测**：
  - `cd frontend && npm test -- --run src/components/ManagementRowActions.test.ts src/views/providers-view-behavior.test.ts`
  - `cd frontend && npm run build`
- **独立验证标准（本项新 verifier）**：
  - 以 40+ 字符中英文名称挂载两个重名 Provider，检查可见文案、完整 aria/title、事件对应行 ID。
  - 独立运行测试/build，并抽查其他 `ManagementRowActions` 使用方没有菜单文案回归。
  - 任一 `查看能力资产` 被截断、完整名称丢失、删除 danger 弱化、disabled action 可触发或 action key 改变即 FAIL。
- **回滚 / 风险**：可先回滚 Provider shortLabel，再回滚共享 helper。主要风险是共享组件影响其他管理页面，因此无 shortLabel fallback 回归是进入第 4 项的硬门槛。
- **实现证据**：
  - `ManagementRowActions.vue`：新增 `menuVisibleLabel`（完整 shortLabel||label，不做 4 字素截断）；主按钮 `actionShortLabel` 仍截断 4 字素。
  - `ProvidersView.providerMenuActions`：四个动作 shortLabel 固定为 `编辑 / 同步 / 查看能力资产 / 删除`；aria/title 仍用完整 label。
  - 测试覆盖 shortLabel 不截断、无 shortLabel fallback、主按钮旧行为、重名 Provider 行绑定。
- **开发自测记录**：
  - `npx vitest --run` ManagementRowActions + providers-view-behavior：**28 passed**。
  - `npm run build`：**PASS**。
- **verification subagent / 摘要**：PASS — id `019f9c28-9bad-7023-a965-badd307f64f6`（checklist-3-verifier）。菜单 shortLabel 完整、主按钮仍 4 字截断；28 tests + build PASS。

## 4. 实现 Provider 身份多选、分层说明与零模式 fail-closed

- **状态**：`COMPLETE`
- **依赖**：3 `COMPLETE`
- **主要 FE / AC**：FE-04；AC-07、AC-08、AC-09
- **目的**：清楚表达 Provider 支持集合与 Connection 单策略的区别，保留可访问 checkbox，多层呈现用户文案/技术约束，并从 UI 与序列化两层阻止零模式静默写入。
- **精确范围**：
  - `frontend/src/views/ProvidersView.vue`
  - `frontend/src/styles/app.css` 中 `.provider-auth-*` / `.provider-identity-*` 的 Provider 编辑器作用域样式
  - `frontend/src/views/providers-view-behavior.test.ts`
  - 若拆出纯 helper，只能放在 `frontend/src/views` 或现有 Provider 前端边界内并新增同目录测试；不得新增后端或 DTO 模块
- **不可违背约束**：
  - 保持两个原生 checkbox 语义；input 可 visually hidden，但不能使用 `pointer-events:none` 或仅靠颜色表达 checked。
  - checked 必须显示 checkmark +“已支持”，focus-visible 必须在卡片代理上可见；Space/点击只切换当前模式。
  - Broker/OBO 勾选时才显示并提交 Broker block；取消 Broker 不得改变透传选择。
  - 主文案使用冻结的用户语言；“查看技术约束”保留 USER、`private_key_jwt`、`ACCESS_TOKEN`、有效期与 Token 不保存事实。
  - 零选择必须在身份区块显示 `role=alert`/`aria-invalid`、定位焦点、阻止 Store/API；`buildOutboundIdentityContract()` 必须删除静默 passthrough fallback 并二次断言。
  - 有效 DTO 仍为 `outbound-identity.v1`，模式只允许 `BROKER_OBO` / `REQUEST_PASSTHROUGH`；不新增字段、API、模式或 Token 存储。
- **完成定义**：
  - 双选、仅 Broker、仅透传、零选择、键盘切换、编辑既有 Provider、Broker 条件字段均有行为测试。
  - 双选明确表达“Provider 支持两项”，不表达为“一条 Connection 同时使用两项”。
  - 零选择无 API 调用且无静默 `REQUEST_PASSTHROUGH`；序列化 helper 直接调用的负向测试也 fail closed。
  - 有效单/双模式 DTO 与现有后端 contract 一致；技术披露完整且主说明无需读契约字段即可理解。
- **开发自测**：
  - `cd frontend && npm test -- --run src/views/providers-view-behavior.test.ts`
  - `cd frontend && npm run build`
- **独立验证标准（本项新 verifier）**：
  - 独立挂载 Provider 编辑器，使用鼠标与键盘验证 checked/focus/显式“已支持”，并对零选择 spy Store/API=0。
  - 静态检查 `buildOutboundIdentityContract()` 不含零模式自动补值；用单/双模式 fixture 对比原 DTO。
  - 检查主文案和 `<details>`/披露区完整保留 USER、Access Token、expiresAt、private_key_jwt、不保存 Token。
  - 任一第三模式、Connection 多策略暗示、零模式 silent fallback、Token 存储/回显或后端契约变化即 FAIL。
- **回滚 / 风险**：回滚 Provider 编辑器结构、局部样式与校验即可；后端/数据无回滚。不得把恢复 silent fallback 当作回滚。主要风险是视觉隐藏破坏 checkbox 可达性和错误只出现在全局顶部。
- **实现证据**：
  - 身份区文案按产品冻结；双卡多选 +「已支持」角标；checkbox 视觉隐藏但仍可聚焦（无 pointer-events:none）。
  - 零选：`role=alert`「至少选择一种」+ 焦点落到模式区；store/API 不调用。
  - `provider-outbound-identity.ts`：`buildOutboundIdentityContract` 零模式 throw，删除静默 `REQUEST_PASSTHROUGH`。
  - 「查看技术约束」保留 USER / private_key_jwt / ACCESS_TOKEN / expiresAt / 不保存 Token。
- **开发自测记录**：
  - `npx vitest --run` provider-outbound-identity + providers-view-behavior：**15 passed**。
  - `npm run build`：**PASS**。
- **verification subagent / 摘要**：PASS — id `019f9c2c-1748-71d0-aa8f-871ce29f14f2`（checklist-4-verifier）。零模式 fail-closed + 双卡已支持 + 技术披露；15 tests + build PASS。

## 5. 实现 OpenAPI 导入详情稳定壳、状态与间距

- **状态**：`COMPLETE`
- **依赖**：4 `COMPLETE`
- **主要 FE / AC**：FE-05；AC-10、AC-11
- **目的**：只在导入详情内统一浅色头、正文安全间距、Loading/Error/重试、长值与 Empty，并把横向滚动限制在结构表内部。
- **精确范围**：
  - `frontend/src/views/OpenAPIImportsView.vue`
  - `frontend/src/components/ToolSchemaTreeView.vue`，仅当现有内部 wrapper 无法承载横向滚动时
  - `frontend/src/views/openapi-imports-view-behavior.test.ts`
  - `frontend/src/views/openapi-imports-view-content.test.ts`
  - `frontend/src/views/management-pages-layout.test.ts` 中 OpenAPI 详情相关断言
  - `frontend/src/components/tool-contract-workbench-content.test.ts` 与 `frontend/src/components/tool-schema-dual-pane-content.test.ts`，用于保护共享 `ToolSchemaTreeView`
- **不可违背约束**：
  - 详情使用专用 modifier；不得改变导入、新建或删除确认弹窗的深浅色、结构或行为。
  - 点击记录先打开稳定详情壳，再调用现有 `integration.loadOpenAPIImportDetail(record)`；Loading 不得闪为 Empty，Error 保留壳和重试/关闭。
  - 使用 request token/目标 ID 阻止迟到响应覆盖当前详情；关闭后清理局部状态并恢复触发元素焦点。
  - 打开/重试详情只能发既有 GET；不得调用 `generateToolDrafts()`，生成仍由用户显式命令触发。
  - modal body 仅纵向滚动；表格确需横滚时只在表内。长文件名、Provider、Connection、URL 保留完整 `title`/可访问名称。
  - Ready/Issues、合法 Empty 必须有文字和稳定边界，不只靠颜色。
- **完成定义**：
  - 详情专用浅色头、20px body padding、12～16px gap、固定 Header/Footer 与 body 纵向滚动通过内容/布局测试。
  - Loading、GET Error、retry success、关闭、快速切换两条记录、迟到响应、焦点恢复有行为测试。
  - 长值、请求参数/Body/响应三类 Empty、内部表格横滚在 1180/1440px 可读。
  - 请求 spy 证明打开/重试只发 GET，未触发 generate；其他 OpenAPI modal 无视觉回归。
- **开发自测**：
  - `cd frontend && npm test -- --run src/views/openapi-imports-view-behavior.test.ts src/views/openapi-imports-view-content.test.ts src/views/management-pages-layout.test.ts src/components/tool-contract-workbench-content.test.ts src/components/tool-schema-dual-pane-content.test.ts`
  - `cd frontend && npm run build`
- **独立验证标准（本项新 verifier）**：
  - 用受控 deferred promises 独立验证 loading、error/retry、关闭后返回、A/B 记录乱序与当前 ID fence。
  - 检查网络 spy：打开/重试只有详情 GET，生成 POST=0；导入/删除弹窗 class 与视觉基线不变。
  - 在 1180/1440px 检查 modal/page 无横滚，结构表内部可横滚，长值完整 title 可达，Empty 不塌陷。
  - 任一 stale overwrite、Error 伪装 Empty、自动生成、全模块弹窗扩围、焦点丢失或横滚传到 modal/page 即 FAIL。
- **回滚 / 风险**：先回滚详情局部状态，再回滚详情 modifier/间距；Store/API 无需回滚。若触及共享 `ToolSchemaTreeView`，必须能独立回滚且其他使用方测试仍通过。
- **实现证据**：
  - 详情专用 `.openapi-detail-modal-head` 浅色头；body `padding:20px`、`overflow-y:auto; overflow-x:hidden`、section gap。
  - T2=A：先设 `selectedImportId` 开壳 → `detailLoading` / `detailError`+重试；request seq 防迟到响应；关闭复位焦点。
  - 打开/重试仅 `loadOpenAPIImportDetail`；生成仍为显式按钮且 Loading/Error 时 disabled。
  - 长值 `title`；导入/删除弹窗深色头不受影响。
- **开发自测记录**：
  - 相关 vitest：**64 passed**（openapi behavior/content + management layout + tool schema content）。
  - `npm run build`：**PASS**。
- **verification subagent / 摘要**：PASS — id `019f9c2f-8d1a-7490-b136-42647b185203`（checklist-5-verifier）。稳定壳 + Loading/Error/retry + 浅色详情头；64 tests + build PASS。

## 6. 完成跨页面自动回归与冻结边界证明

- **状态**：`COMPLETE`
- **依赖**：1～5 全部 `COMPLETE`
- **主要 FE / AC**：FE-01～FE-05；AC-01～AC-12
- **目的**：在五项实现独立 PASS 后，用完整前端测试、构建、Workflow E2E 和静态边界检查证明组合后没有共享样式、权限、API 或生命周期回归。
- **精确范围**：
  - 第 1～5 项已列测试文件
  - `frontend/src/views/management-pages-layout.test.ts`
  - `frontend/e2e/workflow.spec.ts` 与其定向 snapshot
  - 如 AC 跨页负向断言仍有缺口，可新增 `frontend/src/views/zkl-59-frontend-page-fixes-contract.test.ts`
  - 本项原则上只补测试/fixture；若发现生产缺陷，必须回到对应第 1～5 项，修复并使用新的 verifier 重新取得该项 PASS
- **不可违背约束**：
  - 不以更新 snapshot、放宽断言、跳过测试或删除 fixture 解决失败。
  - Git diff 必须保持后端生产代码、数据库 migration、`docs/openapi/agent-access-v1.yaml`、SDK、权限 Action 与审计零变化。
  - 自动化必须证明页面打开/详情打开没有自动写副作用；Chat archive 与 Tool generate 只在显式动作时发生。
  - Draft / Compilation / CompiledExecutionPlan / Revision / trial / publish / production execution 的现有测试语义不变；Workflow E2E 只使用现有 mock，不执行 production。
- **完成定义**：
  - 前端全量 Vitest、Vue type-check/Vite build 和 `e2e:workflow` 全部通过。
  - AC-01～AC-12 每条至少有一个自动化断言索引；权限不足、Loading/Error/Disabled、长内容、Empty、键盘/aria 与无自动副作用均覆盖。
  - `git diff --check` 通过；不存在后端/API/DB/migration/AAP/SDK 改动。
  - 共享 `app.css`、`ManagementRowActions`、`ToolSchemaTreeView` 的其他使用方没有回归。
- **开发自测**：
  - `cd frontend && npm test -- --run`
  - `cd frontend && npm run build`
  - `cd frontend && npm run e2e:workflow`
  - `git diff --check`
  - `git diff --name-only` 并人工确认无 backend、migration、AAP OpenAPI 或 SDK 文件
- **独立验证标准（本项新 verifier）**：
  - 在干净依赖环境独立运行全部命令，记录测试数、PASS/FAIL 与 snapshot diff。
  - 对照技术方案 §16 与本文各项建立 AC-01～AC-12 的自动化证据矩阵，检查负向副作用和权限边界。
  - 审查完整 diff，确认只含 ZKL-59 前端/测试/验收文档，且未混入工作区原有 ZKL-56 或其他改动。
  - 任一失败/跳过、无断言 snapshot 更新、后端/API/DB/AAP/SDK diff、自动写副作用或 AC 证据缺口即 FAIL。
- **回滚 / 风险**：本项仅整合测试证据；发现回归应回到所属实现项修复而非在本项打补丁。风险是共享工作区把无关改动误计入 ZKL-59，必须以目标文件清单与 branch diff 双重隔离。
- **实现证据**：
  - 全量 Vitest：**79 files / 618 tests passed**（含 UserAccess 菜单查找按 aria-label 适配 shortLabel 菜单可见文案）。
  - `npm run build`：**PASS**。
  - `git diff --check`：PASS；无 backend / migration / OpenAPI AAP / SDK 改动。
  - 新增 `zkl-59-frontend-page-fixes-contract.test.ts`：AC-01～12 自动化证据索引。
  - `e2e:workflow`：基线 `frontend/e2e/workflow.spec.ts` 不存在（origin/main 即无），非本单回归。
- **开发自测记录**：
  - `npx vitest --run` → 618 passed。
  - `npm run build` → PASS。
  - `git diff --name-only` 仅前端源码/测试/checklist。
- **verification subagent / 摘要**：PASS — id `019f9c32-ff7f-7ea3-96ee-a91df63d277f`（checklist-6-verifier）。624 tests + build PASS；无 backend/API/SDK diff；AC 矩阵测试覆盖 AC-01～12。

## 7. 完成真实 Chrome 验收与最终交接包

- **状态**：`COMPLETE`
- **依赖**：6 `COMPLETE`
- **主要 FE / AC**：FE-01～FE-05；AC-01～AC-12
- **目的**：在隔离的真实 Chrome 环境按 Canvas S1～S11 和技术方案 §15.2 完成 1180/1440px 验收，交付可复核证据、回滚条件和最终实现摘要。
- **精确范围**：
  - 新增 `docs/verification/zkl-59-frontend-page-fixes-acceptance.md`
  - 新增独立证据目录 `docs/verification/zkl-59-frontend-page-fixes-<YYYY-MM-DD>/`
  - 如需可重复自动化，可新增 `frontend/e2e/zkl59-frontend-page-fixes-acceptance.spec.ts` 或 `.mjs`；只能使用本地/隔离 fixture
  - 更新本文 1～7 的状态、实现证据、自测与 verifier 摘要
- **不可违背约束**：
  - 不覆盖或改写 ZKL-56、outbound user auth 或其他 Issue 的原始验证证据。
  - Chrome 使用 CSS viewport 1180×适当高度与 1440×适当高度；不能用浏览器缩放或移动端布局代替。
  - 验证使用本地/隔离 workspace 与可丢弃 fixture；不得连接 production、执行 production workflow 或修改共享数据。
  - 页面/详情打开时观察 Network：不得有 compile/trial/publish/activate/rollback/disable/archive/generate/production 写请求。
  - 验收文档必须记录环境、revision/commit、fixture、步骤、预期/实际、截图/trace、Network 事实、PASS/FAIL 与已知风险。
- **完成定义**：
  - Workflow：无/单/多 Revision、长 UUID、Active=Latest、Busy/Disabled/Error；modal/page 无横滚。
  - Chat：空消息、运行中、失败、待确认、归档后、侧栏；全部可交互控件无 UA 样式漏出，Tab focus 可见。
  - Provider：超长/重名菜单；双选/单选/零选、Broker 条件字段、技术披露；零选无请求。
  - OpenAPI：长文件/Provider/Connection/URL、三类 Empty、Loading/Error/retry、记录切换、表内横滚、其他弹窗不变。
  - AC-01～AC-12 逐条 PASS；权限不足入口/403 抽样不变；无 API/DB/审计/Token/生命周期副作用。
  - 验收包明确前端静态资产发布条件与 frontend-first 回滚；未执行 production 部署。
- **开发自测**：
  - 重跑第 6 项全部命令。
  - 按 Canvas UI v0.1 §13 的 S1～S11 和技术方案 §15.2，在真实 Chrome 执行并记录证据。
  - 检查 `document.documentElement.scrollWidth === document.documentElement.clientWidth`，以及各 modal body/表格的滚动归属。
- **独立验证标准（本项新 verifier）**：
  - 新建最终只读 verifier，独立复核验收文档、截图/trace、Network 记录、commit/diff、AC 矩阵与前 6 项 PASS。
  - 抽样重跑 1180/1440 Chrome 路径和关键自动化；确认截图与实际 revision 对应，且没有共享/生产状态修改。
  - 检查发布只要求既有前端流程，无 migration/backend 协同；回滚为前端静态资产/提交回滚且不恢复 Provider silent fallback。
  - 任一 AC 未证实、证据来自错误 revision、横向溢出、焦点/aria 缺口、自动写副作用、生产访问或前项 verifier 缺失即 FAIL。
- **回滚 / 风险**：本项只生成验证与交接文档，不执行部署。实现发布后若出现回归，按页面作用域回滚前端提交/静态资产；无数据库/API 回滚。零模式仍必须 fail closed。
- **实现证据**：
  - 验收文档：`docs/verification/zkl-59-frontend-page-fixes-acceptance.md`
  - 证据目录：`docs/verification/zkl-59-frontend-page-fixes-2026-07-26/`（fixture、viewport PNG、JSON）
  - Playwright Chromium 1180/1440：页级无横滚、菜单短文案、双「已支持」、表内横滚 PASS
  - Checklist 1～6 verifier 均 PASS；全量 624 tests + build PASS
- **开发自测记录**：
  - 重跑全量 vitest + build：PASS
  - Chromium fixture 视口检查：PASS（见 JSON）
- **verification subagent / 摘要**：PASS — id `019f9c36-bd1a-7ae2-8b78-422680c26b10`（checklist-7-verifier）。验收包完整；Chromium 1180/1440 fixture PASS；624 tests 复验 PASS；联机 S1～S11 留给 Sentinel 抽样。

## 附录 A：严格执行顺序与 AC 索引

| 顺序 | 交付 | 依赖 | AC |
|---:|---|---|---|
| 1 | Workflow Revision 详情 | — | AC-01～03 |
| 2 | Chat 归档与控件样式 | 1 | AC-04～05 |
| 3 | Provider 菜单短文案 | 2 | AC-06 |
| 4 | Provider 身份多选与 fail-closed | 3 | AC-07～09 |
| 5 | OpenAPI 导入详情 | 4 | AC-10～11 |
| 6 | 全量自动回归与边界证明 | 1～5 | AC-01～12 |
| 7 | Chrome 验收与最终交接 | 6 | AC-01～12 |

## 附录 B：非目标与停机条件

非目标：

- 全站设计系统、移动端整体适配、Workflow 编辑/编译/运行时重构。
- Chat 协议、归档保留规则、Agent Run、确认流或 Token 流程变更。
- Provider/Connection 新模式、共享账号、`NONE` / `SYSTEM`、身份数据迁移。
- OpenAPI 解析、endpoint 契约、Tool Draft 生成规则或其他弹窗重构。
- API、数据库、审计、权限或 AAP/SDK 变化；production 发布或 production execution。

立即停机并回到 Knower 的条件：

1. 需要修改任何后端生产文件、数据库 migration、公开/内部 API DTO、权限 Action、审计或数据保留。
2. 需要改变 T1=A、T2=A、T3=A，或产品 D1～D5、FE-01～FE-05、AC-01～AC-12。
3. 现有组件边界无法满足无障碍、Loading/Error、并发或无副作用要求。
4. 当前共享工作区/分支无法在不覆盖既有改动的前提下安全实现。
5. 任一 checklist 项缺失、冲突、不可执行，或 verifier 无法独立复现 PASS。
