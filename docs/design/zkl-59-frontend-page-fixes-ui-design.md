# ZKL-59 前端页面问题修复：UI 设计

| 字段 | 内容 |
| --- | --- |
| Issue | ZKL-59「前端页面问题修复」 |
| 文档版本 | UI v0.1 |
| 日期 | 2026-07-26 |
| 状态 | **Ready for Knower merge**；不改变产品或技术契约 |
| 产品基线 | `docs/design/zkl-59-frontend-page-fixes-product-design.md` **v1.0 / Approved / Frozen** |
| 技术基线 | 待 Knower 输出；本文件为其 UI 输入 |
| 作者角色 | Canvas · UI 设计师 |
| 冻结决策 | D1=A、D2=A、D3=A、D4=A、D5=A（负责人确认 `e1b97586-688a-400e-b2df-bdd54b80951a`） |
| 范围 | FE-01～FE-05；对齐 AC-01～AC-12 |

> 本文是 **UI 交互与呈现输入**，不是产品需求、不是技术方案、不是 implementation checklist。  
> 对齐产品 §5 / AC-01～AC-12，为 Knower 提供可实现的布局约束、选中态、间距与风格统一、可读性与组件/状态矩阵。  
> **不单独改范围**。不改变 Draft / Compilation / CompiledExecutionPlan / Revision / trial / publish / production execution 语义，不改 API/数据/权限/危险操作规则。若后续 UI 选择会触及上述边界，必须回 Issue 请负责人确认；不得自行冻结。

### 修订记录

| 版本 | 变更 | 依据 |
| --- | --- | --- |
| UI v0.1 | 首轮 UI 输入：FE-01～FE-05 布局/选中态/间距/可读性；状态矩阵；Forge 标注；Sentinel Chrome 路径 | 产品 v1.0；现有 Vue 页面与 `app.css` token |

---

## 1. 设计目标与约束

### 1.1 目标

1. **FE-01**：Workflow 详情发布版本区分层可读，长 UUID 不挤掉操作，弹窗无横向滚动。
2. **FE-02**：运行调试台「归档」及全页交互控件无浏览器原生样式漏出；状态清晰、非危险。
3. **FE-03**：Provider 行菜单可见短动作名，无障碍标签保留对象名；危险态不弱化。
4. **FE-04**：身份模式多选选中态显式（checkbox + checkmark +「已支持」），说明分层易懂。
5. **FE-05**：OpenAPI 导入详情头部与正文间距对齐同类管理详情，结构化区不贴边。

### 1.2 非目标

- 不重构 Workflow 生命周期、Chat 协议、Provider/Connection 鉴权契约、OpenAPI 解析/生成。
- 不扩大 OpenAPI 到导入/新建/删除确认弹窗（D5=A）。
- 不做全站设计系统重构或移动端整体适配；验收基线 **CSS viewport ≥ 1180px**。
- 不新增路由、不新增权限 Action。

### 1.3 结构选择

| 选项 | 内容 | 本版态度 |
| --- | --- | --- |
| **A（采用）** | 最小原位修复：改布局/文案/状态标识/缺失样式，复用现有 modal、ghost-button、status-pill、ManagementRowActions | **UI 推荐并落实** |
| B | 各页重做信息架构或统一大组件库 | 不采用；超出冻结范围 |
| C | 仅截断文案、不修分区与选中语义 | 不采用；不满足 AC |

本版 **无需要负责人额外确认的 UI 未决项**；所有呈现选择均落在已批准产品设计 D1–D5 之内。

### 1.4 复用基线（现有前端）

| 现有资产 | 路径 / 标识 | 本设计用法 |
| --- | --- | --- |
| Workflow 详情 | `WorkflowView.vue`、`WorkflowRevisionPanel.vue`、`app.css` `.workflow-*` | 改发布版本头与 revision 行分区；modal 宽 718px 保持 |
| 运行调试台 | `ChatExecutionView.vue` | 补 `.chat-inline-action`；扫描原生漏出 |
| Provider 列表/编辑 | `ProvidersView.vue`、`ManagementRowActions.vue` | 菜单可见 shortLabel；身份卡选中态与文案 |
| OpenAPI 导入详情 | `OpenAPIImportsView.vue`、`ToolSchemaTreeView` | 浅色头 + 正文安全间距 |
| 管理详情头 | `.modal-card-head`（Workflow/Tool 等） | OpenAPI 详情对齐此层级 |
| 按钮体系 | `.ghost-button`、`.primary-button`、`.icon-action-button`、`.chat-panel-icon-button` | 归档对齐次级 ghost；不发明新色相 |
| Token | `--aw-text`、`--aw-muted`、`--aw-border`、`--aw-cyan`、`--aw-red` 等 | 选中/危险/禁用只用既有 token |

---

## 2. 信息架构（本轮不变）

```text
编排 /workflow
└── 流程详情 modal（718px）
    └── [改] 发布版本区 WorkflowRevisionPanel
        ├── 头：分层 Active / Latest + 停用新执行
        └── 行：ID/时间 · 状态 pill · 操作（可换行）

运行调试台 /chat
├── 标题行：h1 + 状态 badge + [改] 归档
├── 会话 / 运行详情侧栏
└── 输入与发送（仅补缺失样式，不重排）

服务 Provider /providers
├── 列表行 [改] 更多菜单短文案
└── 新建/编辑 [改] 用户调用身份区块

OpenAPI 导入 /openapi-imports
└── [改] 导入详情 modal（仅此弹窗）
    ├── 浅色 header
    ├── 正文：hero + 六项 + 契约树 + 接口明细（统一 padding）
    └── footer：关闭 / 生成 Tool 草稿
```

**不新增路由**；不改侧栏文案。

---

## 3. 跨页面一致性规则

1. **弹窗滚动**：Header/Footer 固定（或 sticky），**仅 body 纵向滚动**；弹窗级与页面级 **禁止横向滚动**。确需横向展示时，**仅表格/代码树内部** `overflow-x: auto`。
2. **长内容**：UUID、文件名、Provider/Connection 名、URL 使用 `ellipsis` + 完整值 `title`（或等价 tooltip）；主操作区 `flex-shrink: 0`，不与长文本争同一不可收缩轴。
3. **状态不只靠颜色**：Active/Latest/History、Ready/Issues、选中/未选、危险/禁用均带文字或 icon。
4. **次级操作**：ghost / 浅底描边；**危险操作**用 danger tone + 二次确认（删除）；**归档**永远非 danger。
5. **焦点**：`:focus-visible` 可见 ring（对齐现有 teal/slate 焦点）；Disabled 不可触发。
6. **权限**：写入口隐藏或 Disabled 规则不变；UI 修复不得绕过 403。
7. **桌面基线**：验收宽度 1180px 与 1440px；`min-width: 1180px` 产品约束保留。

### 3.1 间距刻度（本单沿用）

| 用途 | 建议值 | 使用处 |
| --- | --- | --- |
| 弹窗 body 安全边距 | **18–20px** 水平与垂直 | Workflow 已有 18px；OpenAPI 详情统一到此 |
| 区块内 gap | **10–14px** | 发布版本列表、身份卡、契约区 |
| 行内控件 gap | **6–8px** | revision 操作、状态 pill |
| 卡片内 padding | **10–16px** | revision item、identity card、detail hero |
| 弹窗 footer | **14–16px** 垂直，与 body 水平一致 | 不贴边按钮 |

---

## 4. FE-01：Workflow 详情发布版本布局

**组件**：`WorkflowRevisionPanel`（`frontend/src/components/workflow/WorkflowRevisionPanel.vue` + `app.css`）  
**容器**：`.workflow-detail-modal-card` 宽 `min(718px, calc(100vw - 56px))`  
**AC**：AC-01、AC-02、AC-03

### 4.1 现状问题（UI 层）

- 头区将「Active {uuid}」「Latest {uuid}」堆在同一块，无标签分层；长 UUID 与「停用新执行」争宽。
- 行区 `grid: minmax(0,1fr) auto auto` 在 718px 内容宽下，三按钮 + 完整 UUID 易横向溢出。
- `strong`/`small` 虽有 ellipsis，但操作列未保证可换行，导致裁切。

### 4.2 目标布局

#### 4.2.1 发布版本头（`.workflow-revision-head`）

```text
┌─────────────────────────────────────────────────────────────┐
│ 发布版本                                    [ 停用新执行 ]   │  ← 标题行：标题左、按钮右
│ ┌──────────────────────┐  ┌──────────────────────────────┐ │
│ │ Active               │  │ Latest                       │ │  ← 两列 meta 卡
│ │ a1b2c3d4…e5f6  (title│  │ 9z8y7x6w…v1u0  (title 全量)  │ │
│ │ 全量 UUID)           │  │                              │ │
│ └──────────────────────┘  └──────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

**规则**

| 元素 | 规格 |
| --- | --- |
| 区块标题 | 「发布版本」；11–12px、muted、字重 ≥700 |
| 停用新执行 | 现有 `ghost-button` compact（min-height 28–34px）；`flex-shrink: 0`；文案完整不换行；Disabled 时文案「已停用」 |
| Active / Latest 行 | **两行结构**：上行 label（「Active」「Latest」），下行 monospace 截断 ID |
| ID 显示 | 推荐显示 **前 8 + … + 后 4**，或 CSS ellipsis；**`title` 必须是完整 revisionId** |
| 未设置 | Active 显示「未设置」；Latest 显示「暂无」；不占假 UUID |
| 同一版本 | Active 与 Latest 可为同一 ID；两处均显示，不强行合并（避免误解「只有一个字段」） |
| 布局 | 标题行 `display:flex; justify-content:space-between; align-items:center; gap:12px`；meta 区 `grid: 1fr 1fr`，窄时允许 **单列堆叠**（仍无横向溢出） |

#### 4.2.2 Revision 行（`.workflow-revision-item`）

```text
┌─────────────────────────────────────────────────────────────┐
│ [ID 截断 + title]                    [Active|Latest|History]│
│ 时间 · planHash 短码                                         │
│                              [激活] [回滚] [对比]  ← 可换行  │
└─────────────────────────────────────────────────────────────┘
```

**推荐 CSS 结构（语义）**

1. **主列**（`min-width: 0`）：ID + 时间行。  
2. **状态列**（`flex-shrink: 0`）：`status-pill`。  
3. **操作列**（`flex-shrink: 0` 或整行换行）：`display:inline-flex; flex-wrap:wrap; gap:6px`。

当内容宽不足时：**操作列换到下一整行右对齐**，不得溢出卡片。Active 行可保留浅绿背景（现有 `.active`），pill 文字同时表达状态。

| 状态 pill | 文案 | tone 类 |
| --- | --- | --- |
| 当前激活 | Active | `published`（现有） |
| 最新发布但非 Active | Latest | `review` |
| 历史 | History | `draft` |

#### 4.2.3 操作按钮

- 继续 `ghost-button` compact：min-height 28px、padding 0 9px、font-size 11px。  
- **激活 / 回滚**：当前 Active 行 Disabled；`busyRevisionId` 匹配时该行操作 Loading（spinner 可替换 icon 或按钮文案保持，布局宽度不跳变：建议 min-width 固定约 48–56px）。  
- **对比**：无 Active 或对比自身时 Disabled。  
- **不改变** 确认流、API、trial/publish/production 语义——仅布局。

### 4.3 状态矩阵（FE-01）

| 状态 | 头区 | 列表 | 操作 | 溢出 |
| --- | --- | --- | --- | --- |
| Default 多版本 | Active/Latest 分层 | 多行 | 可用 | 无横向 |
| Active=Latest 同一 ID | 两处同 ID | 一行 Active | 激活/回滚 Disabled | 无 |
| 单版本 | 正常 | 一行 | 对比可能 Disabled | 无 |
| Empty 无发布 | 可仍显示未设置/暂无 | Empty 文案 | 无操作行 | 无 |
| Loading 某 revision | 不变 | 该行 busy | 该行按钮 Disabled | 宽度稳定 |
| Error 动作失败 | 不变 | 行保留 | 恢复 Enabled；错误在详情既有反馈位 | 长错误不撑横滚 |
| 流程已停用 | 「已停用」Disabled | 只读 | 激活/回滚/停用按现有 | 无 |
| 权限不足 | 写按钮隐藏/Disabled | 只读 | 同现有 | 无 |

### 4.4 键盘与 a11y

- 「停用新执行」与行内按钮保持 Tab 序；`:focus-visible` 可见。  
- 截断 ID 的完整值在 `title`；若实现 tooltip 组件，同样需键盘可达焦点元素上有完整名称。  
- status-pill 文本本身可读，不单独依赖色块。

### 4.5 Forge 标注

| ID | 改动 | 约束 |
| --- | --- | --- |
| F-01a | 重排 `workflow-revision-head` 为「标题+按钮 / Active·Latest meta」 | 不改 emit `disable` 语义 |
| F-01b | revision 行改为可换行分区；ID ellipsis + title | 不改 activate/rollback/compare API |
| F-01c | 确认 modal body `overflow-x: hidden` 或 `min-width:0` 链路，消除弹窗横滚 | 仅 CSS/结构 |

---

## 5. FE-02：运行调试台控件样式

**页面**：`ChatExecutionView.vue`  
**AC**：AC-04、AC-05

### 5.1 归档按钮

**现状**：`class="chat-inline-action"` **无任何样式规则** → 浏览器原生 button。

**目标**：次级、非破坏性 inline 控件，与标题行视觉对齐。

| 属性 | 规格 |
| --- | --- |
| 可见文案 | `归档`（不变） |
| title / 辅助 | `归档当前会话（消息会永久保留）`（已有，保留） |
| 外观 | 对齐 `ghost-button` compact：高 28–32px，padding 0 10–12px，圆角 6–8px，边框 `var(--aw-border)`，背景 `#fff` 或 `#f8fafc`，字色 `#475569`，字重 700，字号 12px |
| Hover | 边框/字色偏 `--aw-cyan`；背景浅青或白 |
| Focus-visible | 2px ring（cyan/slate，对齐页内按钮） |
| Active/Pressed | 轻微 scale 或背景加深（对齐 ghost） |
| Disabled / Loading | opacity ~0.45；cursor not-allowed；Loading 时 `aria-busy`，可 spinner |
| 危险色 | **禁止**红/danger；归档 ≠ 删除 |
| 位置 | 仍在 `runtime-title-row` 内 h1 与状态 badge 旁；不换行挤破标题时允许 title-row `flex-wrap` |

**成功后**：沿用现有只读——历史消息保留、发送 Disabled、可新建会话；UI 不新增「已删除」语义。

### 5.2 全页原生样式漏出检查清单（D1=A）

对 `/chat` 下列可交互控件做一次视觉一致性检查；**只补缺失样式，不重排信息架构**：

| 区域 | 控件 | 期望对齐 |
| --- | --- | --- |
| 顶栏 | 上下文下拉、运行详情触发 | 已有自定义样式；确认无裸 button |
| 标题 | **归档** | 本单必修 |
| 会话轨 | 新建、刷新、会话行、搜索 | `chat-panel-icon-button` 等 |
| 消息区 | 新消息跳转、风险确认、出站凭据相关 | 自定义按钮类 |
| 输入区 | textarea、发送 | `chat-input-shell` |
| 侧栏 | 关闭、列表项 | 已有 |

**判定「漏出」**：使用 UA 默认 `button`/`select` 外观（系统灰底、默认边框、无统一圆角/高度/焦点环），或与相邻控件高度差 > 8px 且无设计意图。

### 5.3 状态矩阵（FE-02）

| 状态 | 归档按钮 | 发送 | 消息 |
| --- | --- | --- | --- |
| ACTIVE 会话 | 可见次级 | 按运行态 | 可追加 |
| ARCHIVED | 隐藏（现有 v-if ACTIVE） | Disabled | 只读保留 |
| 归档 Loading | busy + Disabled | — | 不闪空 |
| 归档 Error | 恢复可用；toast/既有错误 | — | 消息不丢 |
| 运行中 | 可见；是否可归档按现有逻辑 | 可能 Disabled | 流式 |
| 权限/Agent 不可用 | 按现有只读条 | Disabled | 历史可见 |

### 5.4 Forge 标注

| ID | 改动 | 约束 |
| --- | --- | --- |
| F-02a | 定义 `.chat-inline-action`（Default/Hover/Focus/Active/Disabled/Loading） | 不改 `archiveSession` API |
| F-02b | 扫描并补齐漏出控件的 class | 不重做三栏布局 |

---

## 6. FE-03：Provider 行操作短文案

**组件**：`ManagementRowActions` + `ProvidersView.providerMenuActions`  
**AC**：AC-06

### 6.1 文案契约（D2=A）

| key | 菜单**可见**文案 | `label` / `aria-label` / `title`（完整） | tone |
| --- | --- | --- | --- |
| edit | 编辑 | `编辑 {provider.name}` | default |
| sync | 同步 | `同步 {provider.name}` | primary（或 default，保持现有） |
| assets | 查看能力资产 | `查看 {provider.name} 的能力资产` | default |
| delete | 删除 | `删除 {provider.name}` | **danger** |

### 6.2 组件行为

**现状**：菜单项渲染 `{{ actionItem.label }}`（完整名）；`shortLabel` 仅用于主按钮图标下短字。

**UI 要求**

1. 菜单**可见文本**优先：`shortLabel ?? 从 label 推导的短动作名`；Provider 侧四个动作均提供 `shortLabel`。  
2. `aria-label` 与 `title` **必须**继续用完整 `label`（含 Provider 名）。  
3. Disabled + `disabledReason`：`title` 优先显示禁用原因（现有 `actionTitle` 逻辑保留）。  
4. 删除项：可见「删除」+ danger 色（现有 `.tone-danger`）；**不**因缩短文案去掉二次确认对话框。  
5. 菜单宽：保持约 208px 或按短文案收窄；**单行**显示动作名，避免因长 Provider 名折 3+ 行。  
6. 移动卡片已用短文案——本单不扩写、不强制与桌面菜单组件统一实现。

### 6.3 状态矩阵（FE-03）

| 状态 | 可见菜单 | a11y | 备注 |
| --- | --- | --- | --- |
| Default | 四短名 | 含全名 | — |
| 超长/中英混合名 | 仍短名 | title/aria 全名 | 对象仍是该行 |
| 同步 Loading | 「同步」+ spinner | busy | 行对象不变 |
| 同步 Disabled | 灰态 | title=原因 | — |
| 删除 | 红色 | 含全名 | 确认框仍显全名 |
| 无权限 | 不出现写动作 | — | 同现有 |

### 6.4 Forge 标注

| ID | 改动 | 约束 |
| --- | --- | --- |
| F-03a | `providerMenuActions` 补齐 `shortLabel`（含「查看能力资产」） | 不改 action key 与权限 |
| F-03b | `ManagementRowActions` 菜单可见文本改用 shortLabel（或兼容策略） | 回归其他页菜单：若仅 Provider 传 shortLabel，其他页可继续显示 label |

---

## 7. FE-04：Provider 出站身份模式与说明

**页面区块**：`ProvidersView` `data-testid="provider-outbound-identity"`  
**AC**：AC-07、AC-08、AC-09

### 7.1 区块文案（产品冻结，UI 原样采用）

| 槽位 | 文案 |
| --- | --- |
| 标题 | 用户调用身份 |
| 说明 | 选择这个 Provider 支持的身份方式（**可多选**）。创建 Connection 时，必须从已支持的方式中选择且只能选择一种；不支持共享账号或免鉴权。 |
| Broker 卡标题 | Broker / OBO |
| Broker 主说明 | 平台按当前用户身份换取短期业务 Token |
| 透传卡标题 | 请求透传 / 本次请求透传（与现有「本次请求透传」对齐即可） |
| 透传主说明 | 调用方为本次请求提供 Token，平台只用于本次调用且不会保存 |
| Broker 帮助 | 平台使用 private_key_jwt 向 Broker 证明自身身份；当前仅支持用户主体（USER）。 |
| 透传帮助 | 仅接收 Access Token。调用方每次提供 Token 及有效期；平台不写入会话、历史或本地存储。 |
| 校验错误 | 至少选择一种（定位到本区块） |

技术字段（`supportedModes`、`credentialTypes`、`private_key_jwt` 等）放入 **「查看技术约束」** 折叠/次级区（D4=A），默认不抢主视线。

### 7.2 选中态视觉（D3=A）

**现状问题**：`input { opacity:0; pointer-events:none }` + 仅浅绿边框 → 双选时像「坏掉的单选」。

**目标卡片结构**

```text
┌──────────────────────────────────────────┐
│ [ ] 或 [✓]                    已支持     │  ← 右上角：选中时 checkmark +「已支持」
│ 🔑  Broker / OBO                         │
│     平台按当前用户身份换取短期业务 Token   │
└──────────────────────────────────────────┘
```

| 状态 | 边框/背景 | 角标 | checkbox | 读屏 |
| --- | --- | --- | --- | --- |
| 未选 Default | 灰边 `#dbe3ef`，底 `#f8fafc` | 无或弱「未支持」**不推荐**强未选文案，避免噪声；仅靠无角标+灰卡即可 | 未勾选 | `aria-checked=false` |
| Hover 未选 | 边框略加深 | — | — | — |
| **已选** | 边 `#80cbbb` / 底 `#effaf7` + 可选 2px soft ring | **checkmark icon +「已支持」**（12px 字、teal） | 勾选 | `aria-checked=true` |
| Focus-visible | 卡片或可见 checkbox 有 focus ring | — | 键盘可切换 | 焦点可见 |
| Disabled | opacity 0.45 | — | 不可改 | — |
| Error（零选保存） | 区块 `role="alert"` 错误文案；卡可红边 **或** 仅区块级错误，避免与「已选绿」冲突 | — | — | 错误被读出 |

**实现约束（UI）**

1. 保持 **原生 checkbox 语义**（可多选）；不要改成 radio。  
2. 勿再 `pointer-events: none` 掉 input 除非有等价的可见焦点代理；推荐 **视觉隐藏但仍可聚焦**（clip/sr-only）或自定义外观但保留 input 在标签内可点。  
3. **双选** 必须同时显示两个「已支持」，表达「Provider 支持两项」，**禁止**文案暗示 Connection 同时用两项。  
4. 选 Broker 展开 Broker 字段；取消则隐藏，**不**改动透传勾选状态。

### 7.3 条件字段与技术补充

- Broker 字段区：与卡同级下方，左缩进或全宽卡片，间距 10–12px。  
- 「查看技术约束」：`<details>` 或 ghost 链接展开；等宽小字展示技术名，**不弱化** Token 不保存 / USER only。

### 7.4 状态矩阵（FE-04）

| 状态 | 卡 A | 卡 B | 条件字段 | 保存 |
| --- | --- | --- | --- | --- |
| 仅 Broker | 已支持 | 未选 | Broker 显示 | 可 |
| 仅透传 | 未选 | 已支持 | 透传帮助 | 可 |
| 双选 | 已支持 | 已支持 | Broker + 透传说明 | 可 |
| 零选 | 未选 | 未选 | 无 | 阻断 + 区块错误 |
| Loading 保存 | 冻结勾选 | 冻结 | 冻结 | 按钮 busy |
| 权限不足 | 只读展示 | 只读 | 只读 | 无保存 |

### 7.5 Forge 标注

| ID | 改动 | 约束 |
| --- | --- | --- |
| F-04a | 标题/说明/卡文案按上表 | 不改 `outbound-identity.v1` 字段 |
| F-04b | 显式选中角标 + 修复 checkbox 可访问性 | 保持至少选一校验 |
| F-04c | 技术术语收入「查看技术约束」 | 安全边界文案保留 |

---

## 8. FE-05：OpenAPI 导入详情视觉与间距

**页面**：`OpenAPIImportsView` 导入详情 dialog  
**AC**：AC-10、AC-11  
**范围**：仅「导入详情」（D5=A）；删除确认等深色头 **不改**。

### 8.1 Header 统一

**现状**：`.openapi-modal-head` 深色渐变（`#020617`）。  
**目标**：对齐 Workflow `.modal-card-head` 浅色体系。

| 元素 | 规格 |
| --- | --- |
| 背景 | `#fff`；底部分割线 `var(--aw-border-soft)` |
| Eyebrow（可选） | 小 caps / 10px / `--aw-cyan`：「OpenAPI Import」或省略 |
| 标题 | 「导入详情」18px、`#0f172a`、字重 800–900 |
| 副标题 | 「查看导入归属、连接与结构化契约」12px muted |
| 图标 | 浅底圆角方：如蓝系或 teal 软底 32×32，**非**深色反白大块 |
| 关闭 | `icon-action-button` 或同高 44×44 浅底；hover 灰底 |
| 层级 | sticky top；z-index 保持在 body 之上 |

### 8.2 正文安全间距

**现状**：hero / 六项网格有 `margin: 0 20px`；`ToolSchemaTreeView` 与 endpoint 列表 **贴 body 左右边缘**。

**目标**：body 统一水平 padding **20px**（或与 Workflow 一致 **18px**，二选一后全页统一），子块 **不再各自左右 margin 叠加不一致**。

```text
header (sticky, 浅色)
┌ body padding 20px ─────────────────────────┐
│ hero 摘要卡                                  │
│ gap 12–16                                   │
│ 六项概览 grid（3 列桌面）                    │
│ gap 12–16                                   │
│ ToolSchemaTreeView ×3（请求参数/体/响应）    │
│ gap 12–16                                   │
│ 接口明细列表                                 │
└─────────────────────────────────────────────┘
footer sticky：关闭 | 生成 Tool 草稿
```

| 分区 | padding/gap |
| --- | --- |
| body | padding 20px；`overflow-y: auto`；`overflow-x: hidden` |
| 分区纵向 gap | 12–16px 一致 |
| 树/表内部 | 若列过多，**组件内部**横向滚动 |
| Empty 树 | 与非空相同外框与边距；Empty 文案居中或左对齐均可，但不贴弹窗边 |
| 长文件名 | hero 内 ellipsis + title 全名；不挤压状态 pill 与关闭 |

### 8.3 Footer

- 浅底 `#f8fafc` + 顶部分割线；padding 16px 20px。  
- 次按钮关闭 / 主按钮生成；Loading「生成中」+ spinner；Disabled 时不改 footer 高度。

### 8.4 状态矩阵（FE-05）

| 状态 | Header | Body | Footer |
| --- | --- | --- | --- |
| Default 有契约 | 浅色 | 全分区有间距 | 生成可用（权限内） |
| 合法空参数/Body/响应 | 浅色 | Empty 文案在有边距容器 | 同现有门禁 |
| 长名称/URL | 浅色 | 截断+title | 不挤 |
| Loading 生成 | 浅色 | 可只读 | busy |
| Error 加载详情 | 浅色 | 错误可见不裁切 | 关闭可用 |
| 无 EDIT | 浅色 | 只读 | 生成隐藏/Disabled |

### 8.5 Forge 标注

| ID | 改动 | 约束 |
| --- | --- | --- |
| F-05a | 导入详情专用浅色 head（可用 modifier class，避免误伤删除确认） | 不改其他 openapi 弹窗 |
| F-05b | body 统一 padding；去掉树贴边 | 不改 GET 契约与生成规则 |

---

## 9. 完整状态总表（交付核对）

| 页面 | Default | Hover/Focus | Selected | Disabled | Loading | Empty | Error | Success | 权限不足 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| FE-01 版本头/行 | 分层+操作 | 按钮 focus ring | Active 行高亮 | 停用/自身操作 | 行 busy 宽稳定 | 无版本文案 | 反馈不撑横滚 | 动作后刷新 | 写入口隐藏 |
| FE-02 归档/控件 | 次级按钮 | hover/focus | — | 归档后发送 | 归档 busy | 空会话引导 | toast/条 | 只读收敛 | 只读 |
| FE-03 行菜单 | 短名 | 项 hover | — | 同步原因 | 同步 spinner | — | — | — | 无写项 |
| FE-04 身份卡 | 灰卡 | 卡 hover | check+已支持 | 只读 | 保存冻结 | — | 零选 alert | 保存成功 | 只读 |
| FE-05 导入详情 | 浅头+间距 | 关闭 hover | — | 生成门禁 | 生成中 | 契约 Empty | 错误可见 | 关闭 | 禁生成 |

---

## 10. 键盘与可访问性汇总

| 项 | 要求 |
| --- | --- |
| Modal | 焦点陷阱、Esc 关闭、关闭后焦点回触发控件（沿用现有） |
| FE-01 | 截断 ID 的 `title`；按钮 focus-visible |
| FE-02 | 归档 focus-visible；Disabled 不可激活 |
| FE-03 | 菜单方向键（组件已有）；`aria-label` 含 Provider 名 |
| FE-04 | 双 checkbox 可键盘切换；`aria-checked`；错误 `role="alert"` |
| FE-05 | 关闭按钮 aria-label；状态 pill 含文字 |
| 对比度 | 使用现有 token；danger/success 不只靠色相 |
| 屏幕阅读 | 缩短可见文案不得缩短 accessible name |

---

## 11. 桌面与 390×844

| 断点 | FE-01 | FE-02 | FE-03 | FE-04 | FE-05 |
| --- | --- | --- | --- | --- | --- |
| ≥1180（验收） | 718 modal；操作可换行 | 标题行可 wrap | 桌面菜单短文案 | 双列身份卡 | 详情 ≤920–980；3 列 KPI |
| 1440 | 更宽松，规则同左 | 同左 | 同左 | 同左 | 同左 |
| 390×844 | 本单不重构；若弹出全屏 sheet，操作纵向堆叠不横溢 | 单列；归档仍可见 | 移动短按钮已有 | 身份卡单列（现有 media） | 全屏；padding 仍 ≥16px |

本轮 **不**为 390 新开交互；保证桌面验收路径完整即可。

---

## 12. 与领域语义的边界（给 Knower）

| 领域对象 | UI 可改 | UI **不可**改 |
| --- | --- | --- |
| Draft / Compilation / CompiledExecutionPlan | 无（本单不涉及编辑器） | 任何生成/编译入口 |
| Revision | 展示布局、截断、按钮换行 | activate/rollback/compare/disable 规则与确认 |
| trial / publish / production execution | 无 | 不自动触发、不改文案语义 |
| ChatSession 归档 | 按钮样式与可见状态 | 消息保留规则、删除语义 |
| Provider supportedModes | 卡外观与说明分层 | 1～2 模式集合、Connection 单选契约 |
| OpenAPI Import | 详情壳与间距 | endpoint 数据、生成草稿规则 |

若技术实现发现必须改 API/DTO/库表，**停**并回产品确认。

---

## 13. Sentinel Chrome 验收路径（建议）

环境：桌面 Chrome；viewport **1180** 与 **1440**；有真实长 UUID / 长 Provider 名 / 含结构的 OpenAPI 导入数据。

| 步骤 | 路径 | 断言（对齐 AC） |
| --- | --- | --- |
| S1 | `/workflow` → 打开含 Active+Latest 的流程详情 | Active/Latest 分层；停用完整；无横滚；ID title 全量 |
| S2 | 同弹窗多 revision | 激活/回滚/对比在卡片内；可换行；Loading 不撑宽 |
| S3 | 无版本或 diff 空 | Empty 完整；无横滚 |
| S4 | `/chat` ACTIVE 会话 | 归档非原生样式；hover/focus；归档后只读且消息在 |
| S5 | `/chat` 空/运行中/侧栏/已归档 | 无原生 button/select 漏出；Disabled 不可点 |
| S6 | `/providers` 长名称行 → 更多 | 可见「编辑/同步/查看能力资产/删除」；aria 含全名；删除为红 |
| S7 | Provider 编辑身份 | 双选均「已支持」；说明可多选/Connection 单选；零选保存错误在区块 |
| S8 | 仅 Broker / 仅透传 | 字段显隐正确；技术约束可展开 |
| S9 | `/openapi-imports` → 查看详情 | 浅色头；正文分区等距不贴边；footer 固定 |
| S10 | 长文件名 + 空契约区 | 截断+可看全名；Empty 有边距；表内横滚不传弹窗 |
| S11 | 无写权限账号抽检 | 写入口隐藏/Disabled；后端 403 行为不变 |

截图建议：S1 头区、S2 行换行、S4 归档、S6 菜单、S7 双选、S9 详情全文。

---

## 14. 交给 Knower / Forge 的交付清单

1. **文档路径**：`docs/design/zkl-59-frontend-page-fixes-ui-design.md`  
2. **版本**：UI v0.1  
3. **确认状态**：产品 v1.0 已冻结；本 UI 无额外决策项，**可直接纳入技术方案**；不代替负责人对技术设计的批准。  
4. **组件/状态矩阵**：§4–§9  
5. **Forge 标注**：F-01a–c、F-02a–b、F-03a–b、F-04a–c、F-05a–b  
6. **Sentinel Chrome**：§13  
7. **不生成** implementation checklist；不写生产代码。

### 14.1 与 AC 映射（UI 侧）

| AC | UI 章节 |
| --- | --- |
| AC-01～03 | §4 FE-01 |
| AC-04～05 | §5 FE-02 |
| AC-06 | §6 FE-03 |
| AC-07～09 | §7 FE-04 |
| AC-10～11 | §8 FE-05 |
| AC-12 | §3、§9、§12 |

---

## 15. 版本记录

| 版本 | 状态 | 说明 |
| --- | --- | --- |
| UI v0.1 | Ready for Knower merge | 首轮 UI 输入；范围严格等于产品 v1.0 FE-01～FE-05 |
