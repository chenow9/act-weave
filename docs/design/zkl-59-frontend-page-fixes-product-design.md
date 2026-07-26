# ZKL-59 前端页面问题修复产品设计

| 字段 | 值 |
|---|---|
| 文档版本 | v1.0 |
| 日期 | 2026-07-26 |
| 作者 | Atlas · 产品经理 |
| 状态 | **Approved / Frozen** |
| 关联 Issue | ZKL-59 |
| 确认负责人 | chenow |
| 负责人确认 | Issue 评论 `e1b97586-688a-400e-b2df-bdd54b80951a`：“批准 v0.1；D1=A、D2=A、D3=A、D4=A、D5=A” |

> 本文已由负责人明确批准并冻结，是后续技术设计与验收的产品事实源。任何影响范围、流程、权限、业务规则、数据保留或验收口径的变化，必须回到需求确认流程重新批准。

## 1. 目标

修复 5 张截图对应的四个页面中的布局、控件样式、操作文案和契约说明问题，使用户能够：

1. 在 Workflow 详情中完整阅读发布版本与版本差异，不出现内容挤压、按钮被裁切或横向滚动。
2. 在运行调试台中看到风格一致、状态清楚的操作控件，不出现浏览器原生样式“漏出”。
3. 在 Provider 列表中快速识别行操作，不因重复 Provider 名称造成菜单过长。
4. 正确理解 Provider 可以声明一个或两个出站身份模式，而每个 Connection 只能固定选择其中一种。
5. 在 OpenAPI 导入详情中获得与其他管理页面一致的弹窗层级、间距与可读性。

## 2. 用户与场景

| 用户 | 场景 | 本单关注点 |
|---|---|---|
| Workspace OWNER / ADMIN | 管理 Provider、Connection、Workflow 与导入记录 | 能准确理解身份模式，安全完成配置与危险操作 |
| Workspace EDITOR | 编辑 Workflow、Provider 非敏感配置和 OpenAPI 导入 | 操作入口清晰，状态与错误不被布局遮挡 |
| Workspace OPERATOR | 运行调试、试运行与诊断 | 调试台控件风格与 Disabled/失败状态明确 |
| Workspace VIEWER | 查看获授权资产 | 写操作继续按现有权限隐藏或 Disabled，不因本单获得额外权限 |

## 3. 事实、假设与约束

### 3.1 已验证事实

1. 仓库主前端为 Vue 3；页面以桌面端为主，README 明确存在 `min-width: 1180px` 约束。
2. Workflow 详情弹窗宽度为 718px；发布版本头当前把 `Active`、`Latest` 与长 UUID 放在同一内容块，发布版本行同时容纳 UUID、状态和三个按钮。截图中出现文本连写、右侧按钮裁切和弹窗横向滚动。
3. 运行调试台“归档”按钮使用 `chat-inline-action`，当前代码中没有对应样式规则，因此显示为浏览器原生按钮。归档会话保留消息，但归档后不能继续发送。
4. Provider 行操作当前可见文案包含完整 Provider 名称，例如“查看 Mock Corp Expense 的能力资产”，而操作所属行已能提供 Provider 上下文。
5. 现有通用 `ManagementRowActions` 支持完整 `label` 作为 `aria-label/title`，但菜单可见文本当前也直接使用完整 `label`。
6. 已批准的 `docs/design/outbound-user-auth-product-design.md` 已冻结以下业务语义：
   - Provider 声明支持的出站身份策略集合；
   - Provider 可同时支持 `BROKER_OBO` 与 `REQUEST_PASSTHROUGH`；
   - 每个 ServiceConnection 必须从 Provider 的集合中固定选择且只能选择一种；
   - 不支持共享业务账号、`NONE` 或 `SYSTEM` 例外。
7. 后端 `outbound-identity.v1` 接受 1～2 个 `supportedModes`；前端现用两个 checkbox，至少选择一个，选中 Broker/OBO 时才展示 Broker 字段。
8. Provider 身份模式卡片当前把原生 checkbox 完全隐藏，只通过浅绿色边框和背景表达选中，因此两张卡同时选中时容易被误认为单选控件异常。
9. Provider 页面当前直接展示 `supportedModes`、`credentialTypes=ACCESS_TOKEN`、`private_key_jwt`、`USER`、`expiresAt` 等契约术语，缺少面向配置用户的解释。
10. OpenAPI 导入详情复用独立的深色渐变弹窗头；Workflow、Provider 等管理弹窗采用浅色头部。详情正文只有摘要卡与信息网格有外边距，三个 `ToolSchemaTreeView` 直接贴近详情容器左右边缘。
11. OpenAPI 详情继续以现有 `GET /workspaces/{wid}/openapi-imports/{id}` 返回的导入记录与 endpoint 契约为事实；本单不要求变更接口。
12. 本单截图及代码检查未发现需要改变 Workflow Draft、Compilation、CompiledExecutionPlan、Revision、trial、publish 或 production execution 语义的需求。

### 3.2 已确认口径

1. 检查 `/chat` 页面全部可交互控件是否出现浏览器原生样式，只补缺失样式，不重做信息架构。
2. Provider 行操作可见文案固定为“编辑 / 同步 / 查看能力资产 / 删除”，完整对象名保留在 `aria-label/title`。
3. Provider 身份模式保持 checkbox 多选，卡片右上角显示 checkmark +“已支持”。
4. 鉴权主流程使用用户语言，技术字段放入“查看技术约束”补充区。
5. OpenAPI 视觉统一只覆盖“导入详情”弹窗及其正文间距，不扩大到导入、新建或删除确认弹窗。

### 3.3 实施假设

1. 五项问题可仅通过前端布局、可见文案、状态标识和测试修复，不需要数据库迁移或 API 变更；若技术设计证明需要改变数据/API，必须回到产品确认。
2. 本轮验收基线为产品已声明支持的桌面宽度（CSS viewport ≥ 1180px）；移动端整体重构不在本单。

### 3.4 硬约束

- 当前产品范围已经冻结；实现中出现的新需求或范围变化必须回到本确认流程。
- 不创建子 Issue、Stage 或并行实现任务。
- 不改变角色、权限 Action 或后端 403 边界。
- 不自动 compile、trial、publish、activate、rollback、disable 或触发 production execution。
- 不改变 Provider/Connection 的身份契约业务语义，不保存或回显用户业务 Token。
- 不删除或回填历史 Workflow Revision、ChatSession、Provider、Connection、OpenAPI Import、Tool 或审计数据。

## 4. 范围与非目标

### 4.1 本轮范围

| ID | 页面 | 本轮结果 |
|---|---|---|
| FE-01 | `/workflow` 流程详情 | 修复发布版本头、版本行、操作区的布局与长内容适配，消除弹窗级横向溢出 |
| FE-02 | `/chat` 运行调试台 | 补齐“归档”样式，并检查该页面所有交互控件无浏览器原生样式漏出 |
| FE-03 | `/providers` Provider 列表 | 行操作显示短动作名，同时保留包含 Provider 名称的无障碍标签与危险态 |
| FE-04 | `/providers` Provider 编辑 | 明确身份模式可多选、选中态、每种模式含义及 Provider/Connection 分工 |
| FE-05 | `/openapi-imports` 导入详情 | 统一详情弹窗视觉层级与正文间距，保证结构化契约分区不贴边 |

### 4.2 非目标

- Workflow 生命周期、Revision 激活/回滚/对比规则重构。
- 运行调试台消息协议、归档数据保留规则、Agent Run 或出站 Token 流程变更。
- Provider/Connection 身份模式新增、删减或迁移。
- Provider 列表信息架构、批量操作或删除规则重构。
- OpenAPI 解析、endpoint 数据、Tool 草稿生成规则或历史数据修复。
- 全站设计系统重构、移动端整体适配或与截图无关的视觉翻新。

## 5. 问题清单与期望行为

### FE-01：Workflow 详情发布版本布局异常

**现象**

- “发布版本 Active UUID Latest UUID”文本连在一起，层级不可辨。
- 长 Revision ID 挤压“停用新执行”及行内“激活 / 回滚 / 对比”按钮。
- 弹窗正文出现横向滚动，右侧内容和按钮被裁切。

**期望行为**

1. “发布版本”、Active、Latest 分层展示；长 ID 可省略显示并通过 `title` 或等价方式查看完整值。
2. “停用新执行”保持完整可见，不与版本标识竞争同一不可收缩区域。
3. 每条 Revision 的 ID/时间、状态、操作形成稳定分区；窄到支持下限时操作允许换行，但不得越出卡片。
4. 弹窗只允许正文纵向滚动；弹窗本身及页面不得因该区域产生横向滚动。
5. 空版本、单版本、多版本、Active/Latest 同一版本、操作 Loading/Disabled 时均保持布局稳定。

**影响页面**

- `/workflow` → Workflow 列表 → 流程详情。
- 仅影响详情展示；不改变 Draft、Compilation、CompiledExecutionPlan、Revision、trial、publish 或 production execution。

### FE-02：运行调试台控件样式丢失

**现象**

- 标题旁“归档”显示为浏览器原生按钮，与同页按钮的高度、圆角、颜色和焦点态不一致。
- 代码检查确认 `chat-inline-action` 当前无样式规则。

**期望行为**

1. “归档”使用次级、非破坏性样式，并有明确 hover、focus-visible、pressed、Disabled/Loading 状态。
2. 可见文案保持“归档”；辅助说明继续明确“消息会永久保留”。归档不等同于删除，不使用红色危险态。
3. 成功归档后沿用现有只读状态：历史消息保留、继续发送 Disabled，可新建会话。
4. 对 `/chat` 的顶部上下文选择、历史会话、运行详情、风险确认、消息跳转、出站凭据、输入与发送控件进行一次“原生样式漏出”检查；只补缺失样式，不重排页面。
5. 键盘焦点必须可见，Disabled 控件不能触发动作。

**影响页面**

- `/chat` 运行调试台及其历史会话、运行详情侧栏。

### FE-03：Provider 行操作名称过长

**现象**

- 菜单重复显示当前行 Provider 名称，长名称导致菜单多行、扫描困难。

**期望行为**

1. 推荐可见操作名固定为：`编辑`、`同步`、`查看能力资产`、`删除`。
2. 完整无障碍标签与 tooltip 保留对象上下文，例如“编辑 Mock Corp Expense”“删除 Mock Corp Expense”。
3. `同步` Loading/Disabled 及原因继续保留；`删除`保持危险色和现有二次确认，不因缩短文案弱化风险。
4. 超长、中英文混合或重名 Provider 不改变操作对应的行对象。

**影响页面**

- `/providers` 桌面表格行操作。
- 移动卡片现有短文案不扩写。

### FE-04：Provider 出站身份模式与说明难懂

**现象**

- 两张模式卡都呈浅绿色，但没有显式 checkbox/checkmark，用户无法确认是否“两项都已选择”。
- 标题说明“Connection 只能固定选择一种”，但未先说明 Provider 本身可以支持多种。
- 底部直接展示技术字段和值，配置用户难以理解。

**已冻结业务语义**

- Provider 层是“支持集合”，可以选择一种或两种。
- Connection 层是“实际策略”，必须从集合中选择且只能选一种。
- 至少选择一种；未选择时禁止保存并说明原因。

**推荐主文案**

- 区块标题：`用户调用身份`
- 区块说明：`选择这个 Provider 支持的身份方式（可多选）。创建 Connection 时，必须从已支持的方式中选择且只能选择一种；不支持共享账号或免鉴权。`
- Broker/OBO 卡片：`平台按当前用户身份换取短期业务 Token`
- 请求透传卡片：`调用方为本次请求提供 Token，平台只用于本次调用且不会保存`
- Broker 帮助：`平台使用 private_key_jwt 向 Broker 证明自身身份；当前仅支持用户主体（USER）。`
- 透传帮助：`仅接收 Access Token。调用方每次提供 Token 及有效期；平台不写入会话、历史或本地存储。`

**期望行为**

1. 区块明确标注“可多选”；每张卡显示未选/已选的显式文字或 checkmark，不能只靠颜色。
2. checkbox 语义、键盘操作、焦点态和读屏选中状态保持可用。
3. 选择 Broker/OBO 时显示 Broker 字段；取消时隐藏，但不得误改另一模式。
4. 同时选择两项时清楚表达“Provider 支持两项”，不得表达为“一个 Connection 同时使用两项”。
5. 两项都未选时保存失败，错误定位到本区块；不得静默改成请求透传。
6. 技术名可作为精确补充保留，但主说明必须先给出用户语言；不得隐藏 Token 不保存、USER 限制等安全边界。

**影响页面**

- `/providers` → 新建/编辑 Provider → 用户态出站鉴权契约。
- 不改变现有 `outbound-identity.v1` DTO、后端校验或 Connection 固定选择规则。

### FE-05：OpenAPI 导入详情风格与间距不一致

**现象**

- 详情头部为大面积深色，与同类管理详情弹窗的浅色头部不一致。
- 请求参数、请求体、响应结果等结构化视图直接贴近正文左右边缘，视觉上拥挤。

**期望行为**

1. 推荐仅把“导入详情”改为与 Workflow/Provider 同类详情一致的浅色头部；图标、标题、副标题、关闭按钮层级清楚。
2. 文件摘要、六项概览、请求参数、请求体、响应结果、接口明细使用统一的正文左右安全间距和纵向间距。
3. Header 与 Footer 可保持固定，只有正文纵向滚动；正文不产生弹窗级横向滚动。
4. 结构表确需横向展示时，仅表格内部滚动，不把滚动传递到整个弹窗。
5. 长文件名、Provider/Connection 名称和 URL 截断但可查看完整值；Ready/Issues 等状态不只靠颜色。
6. 合法空请求参数、空 Body 或空响应继续显示对应 Empty 文案，并保持与非空分区相同间距。

**影响页面**

- `/openapi-imports` → 导入记录 → 查看详情。
- 不改变导入、新建或删除确认弹窗，除非负责人选择扩大范围。

## 6. 状态、异常、权限与危险操作

| 状态 | 统一要求 |
|---|---|
| Loading | 原有 Busy/Loading 语义保留；按钮 Disabled，布局不跳动，不先把未知数据渲染为“空” |
| Empty | Workflow 无 Revision、Chat 无消息、OpenAPI 契约为空时显示现有 Empty 说明，容器和间距不塌陷 |
| Error | 现有错误信息和重试/关闭入口保持可见，不被溢出裁切；不伪装为成功或合法空数据 |
| Success | 操作结果沿用现有刷新、toast 或只读收敛逻辑 |
| Disabled | 视觉上可辨、不可点击，保留可读的禁用原因或上下文 |
| Permission denied | 写入口继续按既有 Action 隐藏或 Disabled；直接请求仍由后端 403 拒绝 |

危险操作边界：

- “归档”保留会话与消息，不新增删除含义。
- Provider“删除”继续使用危险色、名称确认和关联 Connection/Tool 风险提示。
- Workflow“停用新执行 / 激活 / 回滚 / 发布”只修布局，不改变确认、授权或执行规则。
- “生成 Tool 草稿”只修详情容器视觉，不自动触发、不改变 endpoint 选择与生成规则。

## 7. 数据、API、审计与安全影响

- **数据**：预期无数据库 schema、历史数据或保留策略变化。
- **API**：复用现有 Workflow、ChatSession、Provider、Connection 和 OpenAPI Import API；预期无请求/响应字段变化。
- **领域对象**：不改变 Workflow 生命周期、Provider 支持集合、Connection 单策略或 OpenAPI endpoint 契约。
- **审计**：不新增业务动作；现有归档、Provider 更新/删除、Revision 操作、Tool 草稿生成审计继续生效。
- **安全**：技术说明改写不得弱化以下事实：不保存用户业务 Token；Provider 不接收最终用户 Token；Connection 只能选择 Provider 已支持的一种策略；权限不足不能通过前端样式变更绕过。
- **无障碍**：选中态、错误态、Ready/Issues、危险态不能只靠颜色；所有缩短的可见操作仍保留完整 `aria-label/title`。

## 8. 依赖与风险

### 8.1 依赖

- Workflow 详情及 `WorkflowRevisionPanel` / `WorkflowRevisionDiff`。
- ChatExecutionView 现有状态与归档 Store 行为。
- `ManagementRowActions` 的可见文案与无障碍标签能力。
- 已批准的出站用户鉴权产品设计和后端 `outbound-identity.v1` 校验。
- OpenAPI 导入详情与 `ToolSchemaTreeView`。
- 真实浏览器下的桌面宽度、长 UUID/长名称和键盘验收。

### 8.2 风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| 只用省略号掩盖 Workflow 布局问题 | 用户仍无法操作或读取完整 Revision | 分区、换行、完整值 tooltip 与无横向溢出同时验收 |
| Provider 菜单缩短后丢失对象上下文 | 读屏用户或重名行误操作 | 可见短文案，完整 `aria-label/title` 保留名称 |
| 身份模式继续只靠颜色 | 两项同时选中仍被误解 | 显式 checkmark/“已支持”及 checkbox 语义 |
| 过度口语化删掉安全约束 | 用户误以为 Token 会保存或 Connection 可动态切换 | 技术细节放补充层，安全边界保留在主说明 |
| 扩大全部 OpenAPI 弹窗样式范围 | 回归面超出截图诉求 | 默认只改导入详情，其他弹窗需负责人另行选择 |
| 只在单一测试数据验收 | 长 UUID/名称、空数据或 Disabled 再次溢出 | 覆盖长短内容、空/错/忙/禁用状态 |

## 9. Given / When / Then 验收标准

### AC-01 Workflow 发布版本头

- Given Workflow 存在 Active 与 Latest Revision，ID 为完整 UUID
- When 用户打开流程详情
- Then “发布版本”、Active、Latest 层级可辨，完整 ID 可访问
- And “停用新执行”完整可见，不与 ID 重叠
- And 弹窗与页面无横向滚动

### AC-02 Workflow Revision 行

- Given 同时存在 Active、Latest 与历史 Revision
- When 用户在 CSS viewport 1180px 与 1440px 查看详情
- Then 每行 ID、时间、状态和操作均在卡片内
- And “激活 / 回滚 / 对比”可换行但不被裁切
- And Loading/Disabled 状态不改变布局宽度

### AC-03 Workflow 空与异常状态

- Given 无发布版本、差异未选择或版本动作失败
- When 对应状态出现
- Then Empty/Error 文案完整可见
- And 不因空值、错误码或长文案产生横向溢出

### AC-04 归档按钮与行为

- Given 当前会话为 ACTIVE
- When 用户查看运行调试台标题区
- Then “归档”呈现与页面一致的次级按钮、hover 和 focus-visible 样式
- When 用户触发归档
- Then 消息保留、会话转只读、发送 Disabled
- And 未产生删除或 Token 保留语义变化

### AC-05 运行调试台样式完整性

- Given `/chat` 处于空消息、运行中、失败、待确认、已归档及侧栏打开状态
- When 检查所有可交互按钮、输入、下拉与选择控件
- Then 不存在未设计的浏览器原生样式漏出
- And Disabled 控件不可触发，键盘焦点可见

### AC-06 Provider 操作短文案

- Given Provider 名称为超长中英文混合文本
- When 打开该行更多操作
- Then 可见文本为“编辑 / 同步 / 查看能力资产 / 删除”
- And 每项 `aria-label/title` 含完整 Provider 名称
- And 删除仍为危险态，同步 Disabled 原因仍可读取

### AC-07 Provider 支持集合

- Given 用户有 Provider 策略配置权限
- When 打开支持 Broker/OBO 与请求透传的 Provider
- Then 两张卡都显示明确“已选/已支持”状态
- And 页面说明 Provider 可多选、Connection 只能固定选择一种
- And 键盘与读屏能识别两个 checkbox 的 checked 状态

### AC-08 Provider 模式校验与条件字段

- Given 用户取消全部模式
- When 保存 Provider
- Then 保存被阻止并在本区块显示“至少选择一种”
- And 不静默写入请求透传
- Given 用户只选择 Broker/OBO 或只选择请求透传
- Then 只展示并提交该模式对应字段，不误改另一模式

### AC-09 Provider 帮助文案

- Given 用户不了解 `supportedModes`、`private_key_jwt` 或 `credentialTypes`
- When 阅读用户调用身份区块
- Then 不阅读技术文档也能说明两种模式、Provider/Connection 分工和 Token 是否保存
- And 技术补充仍准确表达 USER、Access Token、有效期与机器认证限制

### AC-10 OpenAPI 详情视觉与间距

- Given 导入详情含摘要、六项概览和请求/响应结构
- When 打开详情
- Then 头部与同类管理详情采用一致的浅色层级
- And 所有正文分区具有一致左右安全间距和纵向间距
- And Header/Footer 固定、正文纵向滚动，弹窗无整体横向滚动

### AC-11 OpenAPI 长内容与 Empty

- Given 文件名、Provider、Connection、URL 很长，或请求参数/Body/响应任一区域合法为空
- When 查看详情
- Then 长值不挤压状态与关闭按钮，完整值可访问
- And Empty 文案仍处于有边界、有间距的内容区
- And 表格需要横向滚动时只在表格内部发生

### AC-12 权限、API 与副作用

- Given 用户权限不足，或任一页面处于 Loading/Error/Disabled
- When 用户尝试写操作
- Then 前端维持既有隐藏/Disabled，后端仍返回现有 403/错误
- And 本单不新增 API、数据迁移、自动发布、自动归档、自动生成或审计绕过

## 10. 已冻结决策

负责人 chenow 在 Issue 评论 `e1b97586-688a-400e-b2df-bdd54b80951a` 明确批准 v0.1，并选择全部推荐项：

| 决策 | 冻结结论 | 主要影响 |
|---|---|---|
| D1 | A：检查 `/chat` 全部交互控件，只补原生样式漏出 | 覆盖全页样式完整性，不重做信息架构 |
| D2 | A：可见文案为“编辑 / 同步 / 查看能力资产 / 删除” | 完整 Provider 名称仅保留在无障碍标签与 tooltip |
| D3 | A：checkbox 多选 + checkmark +“已支持” | 保持 Provider 支持集合、Connection 固定一种的既有契约 |
| D4 | A：用户语言主说明 +“查看技术约束” | 兼顾易懂与契约精确性 |
| D5 | A：只统一 OpenAPI“导入详情” | 不改该模块其他弹窗，控制回归面 |

业务范围、流程、权限、业务规则、数据保留和验收口径的剩余未决项：**无**。

## 11. 冻结与交接

### 11.1 已冻结范围与非目标

- 冻结范围：FE-01～FE-05，即 Workflow 详情布局、运行调试台样式完整性、Provider 行操作短文案、Provider 身份模式呈现与说明、OpenAPI 导入详情视觉和间距。
- 冻结非目标：第 4.2 节全部项目；尤其不改变 Workflow 生命周期、Chat 协议、Provider/Connection 身份契约、OpenAPI 解析/生成规则、权限模型、数据保留或移动端整体布局。
- 冻结验收：第 9 节 AC-01～AC-12 全部作为交付验收标准。

### 11.2 交给 Knower 的输入

1. 以第 3.1 节现状事实和 5 张 Issue 截图为问题基线，不重新解释业务语义。
2. 技术方案必须逐项映射 FE-01～FE-05 与 AC-01～AC-12，并覆盖长 UUID/长名称、Empty/Error/Loading/Disabled、权限不足和键盘/读屏。
3. 优先复用或扩展现有 `WorkflowRevisionPanel`、`ChatExecutionView`、`ManagementRowActions`、`ProvidersView`、`OpenAPIImportsView` 与 `ToolSchemaTreeView`，但具体实现由技术设计决定。
4. 保持现有 API、数据库和审计边界；若证明需要 API/数据变化，或实现中出现范围变化，停止并回到负责人确认。
5. Workflow 相关技术方案必须继续区分 Draft、Compilation、CompiledExecutionPlan、Revision、trial、publish 与 production execution，不得用视觉修复改变生命周期动作。
6. Provider 鉴权技术方案必须保持：Provider 可支持一项或两项模式、Connection 固定选择其中一种、至少选择一项、用户业务 Token 不保存。
7. 本文不授权创建子 Issue、Stage、并行任务或直接进入生产代码实现；由 Conductor 恢复 Issue 为 `todo` 并完成下一阶段交接。

### 11.3 版本记录

| 版本 | 状态 | 说明 |
|---|---|---|
| v0.1 | Approved source | 首轮草案；负责人批准 D1=A、D2=A、D3=A、D4=A、D5=A |
| v1.0 | Approved / Frozen | 仅记录批准结果、冻结决策和交接输入；产品范围与 v0.1 一致 |
