# ZKL-56 PM E2E 缺陷修复：UI 设计

| 字段 | 内容 |
| --- | --- |
| Issue | ZKL-56「修复 PM E2E 走查缺陷（UX-01～07）」 |
| 文档版本 | UI v0.1 |
| 日期 | 2026-07-25 |
| 状态 | **Ready for Knower merge**；不改变产品或技术契约 |
| 产品基线 | `zkl-56-pm-e2e-ux-fixes-product-design.md` **v1.0 / Approved** |
| 技术基线 | `zkl-56-pm-e2e-ux-fixes-tech-design.md` **v0.1 / Awaiting Approval** |
| 作者角色 | Canvas · UI 设计师 |
| 冻结决策 | D1=B、D2=A、D3=A、D4=A、D5=A |
| 范围 | UX-01～07；非目标 UX-08～10 |
| 工作分支 | `fix/zkl-56-pm-e2e-ux-fixes` |

> 本文是 **UI 交互与呈现输入**，不是产品需求、不是技术方案、不是 implementation checklist。  
> 对齐产品 §5 / AC-01～AC-15，并为 Knower 技术 v0.1 的 **T10=A（最小原位方案）** 提供可实现的页面态、文案、恢复动作与组件/状态矩阵。  
> **不改变** 用户主流程、权限可见性、危险操作语义或验收口径。若后续 UI 选择会触及上述边界，必须回 Issue 请负责人确认；不得自行冻结。

### 修订记录

| 版本 | 变更 | 依据 |
| --- | --- | --- |
| UI v0.1 | 首轮 UI 输入：Workflow 编辑入口、Console 终态/意图、Smart DAG 恢复卡、OpenAPI 双栏、Tool 三状态；状态矩阵、关键文案、Forge 标注、Sentinel Chrome 路径 | 产品 v1.0 §5/§9；技术 v0.1 §4/§10/T10=A；走查报告与现有 Vue 页面 |

---

## 1. 设计目标与约束

### 1.1 目标

1. **Workflow**：从详情进入编辑器时，Loading / Success / Failed 在原上下文可见，禁止静默回列表。
2. **Console**：Run 顶部状态、意图、输入区在 terminal 后 5 秒内一致收敛；纯文本成功不因无关 Tool 呈现“系统不可用”。
3. **Smart DAG**：失败后在 Copilot 面板内展示可行动恢复卡（阶段、retryable、会话状态、重试/关闭/新建）。
4. **OpenAPI**：服务地址规范化；契约按选中 endpoint 展示；合法空 vs 导入不完整 vs 加载错误三者可区分。
5. **Tool**：生命周期 / 历史测试 / 当前可调用性三维分开展示，禁止把“连接验证失败”显示成“连接缺失”。

### 1.2 非目标

- 不新增大屏 wizard、统一“恢复中心”、全局 Toast 可行动性改造（UX-08）。
- 不新增已发布 Tool 只读重测（UX-09）、登录 refresh 噪声修复（UX-10）。
- 不新增 in-flight cancel UI（D3=A）。
- 不自动回填历史 OpenAPI（D4=A）、不自动撤销 Published（D5=A）。
- 不做视觉系统重构；延续现有 management / chat / smart 的 glass-panel、status-pill、modal 体系。

### 1.3 结构选择（对齐 Knower T10）

| 选项 | 内容 | 本版态度 |
| --- | --- | --- |
| **A（采用）** | 最小原位：详情内状态、Console 状态条、Smart 恢复卡、OpenAPI 双栏、Tool 三状态 | **UI 推荐并落实** |
| B | 五处全屏 wizard / 统一恢复中心 | 不采用；扩大流程与回归面 |
| C | toast-only | 不采用；不满足 AC-02/07/08/11/13 |

本版 **无需要负责人额外确认的 UI 未决项**；所有呈现选择均落在已批准产品设计之内。

### 1.4 复用基线（现有前端）

| 现有资产 | 路径 | 本设计用法 |
| --- | --- | --- |
| Workflow 列表 / 详情 / 编辑器 | `WorkflowView.vue`、`workflow/*` | 详情内 Loading/Error/Retry；handoff 时机 |
| 运行调试台 | `ChatExecutionView.vue`、`stores/chat.ts`、`run-event-stream.ts` | 顶部状态条 + 意图 + 输入收敛；degraded banner |
| Smart DAG | `SmartDagView.vue`、`stores/smartdag.ts` | Copilot 内恢复卡；保留 Guard 区 |
| OpenAPI 导入 | `OpenAPIImportsView.vue`、`ToolSchemaTreeView`、`openapi-preview.ts` | 详情双栏；完整性 banner；生成门禁 |
| Tool 治理 | `ToolsView.vue`、`utils/tool-governance.ts` | 三状态 pill + 详情 strip；catalog loading |
| 管理壳 | `ManagementList`、`ManagementRowActions`、`status-pill`、modal-backdrop | 不新增设计系统 |
| 权限 | `workspaces.can(...)` | EDIT/TEST/PUBLISH/EXECUTE 入口隐藏或 Disabled |

---

## 2. 信息架构（本轮不变的页面树）

```text
编排 /workflow
├── 列表
├── 流程详情 modal  ← 编辑入口 Loading/Error 原位
└── 流程图编辑器 full-bleed  ← 仅 READY 后挂载

运行调试台 /chat
├── 会话列表
├── 运行摘要条（状态 / 意图 / 能力数）
├── 消息流 + Tool 失败结构化气泡
└── 输入区（composer）

智能编排 /smart-dag
├── 画布
└── AI Copilot 面板
    ├── 会话/草稿摘要
    ├── 轮次历史
    ├── [新] 失败恢复卡
    └── 输入 + 生成动作

OpenAPI 导入 /openapi-imports
└── 导入详情 modal
    ├── 摘要 KPI（地址 / 接口数 / 可生成）
    ├── [改] endpoint 列表（左或上）
    └── [改] 选中 endpoint 契约（右或下）

工具管理 /tools
├── 列表统一 pill
└── 详情三状态 stack + 治理 strip
```

**不新增路由**；不改侧栏导航文案。

---

## 3. UX-01：Workflow 详情 → 编辑器

### 3.1 用户路径

#### P1 成功进入（AC-01）

1. 用户在详情点击 **「编辑流程图」**（仅 `can EDIT` 渲染）。
2. **详情 modal 保持打开**；主按钮组进入 busy：
   - 「编辑流程图」：`aria-busy=true`，文案改为「加载中…」，Disabled。
   - 其他写操作（校验 / 试运行 / 发布 / 编辑信息）一并 Disabled，避免竞态。
3. 详情 body 顶部插入 **内联状态条**（非全屏空画布）：
   - 图标 spinner + 标题「正在加载最新草稿」
   - 副文「{Workflow 名称} · 同步 Draft 与编译结果」
4. 后端返回且 request token 匹配 → **先挂载编辑器** → 下一 tick **再关闭详情**。
5. 编辑器顶部沿用现有 feedback 条展示「已加载最新草稿」。

#### P2 加载失败可恢复（AC-02）

1. 详情 **不关闭**；编辑器 **不出现**；不显示默认空图。
2. 详情内替换状态条为 **Error alert**（`role="alert"`）：
   - 标题：`无法打开流程图`
   - 正文：安全用户文案 + `requestId`（有则显示；无则省略）
   - 动作：**重试加载**（primary）、**关闭**（ghost，仅关详情）
3. 「编辑流程图」恢复 Enabled（可再次点击，等同重试）。
4. 重试成功走 P1 handoff。

#### P3 Stale / 切换（AC-03）

- 用户关闭详情或切换另一 Workflow：旧请求返回时 **不写 store、不弹错、不打开错误编辑器**。
- 连续点两个 Workflow 的编辑：仅后一个 request token 可 commit。

#### P4 权限

| 角色 | 编辑入口 |
| --- | --- |
| OWNER / ADMIN / EDITOR | 显示「编辑流程图」 |
| OPERATOR / VIEWER | **不渲染**该按钮；详情只读 |
| 直接 API 403 | 若绕过 UI，详情保留，不打开编辑器 |

### 3.2 状态矩阵（Workflow 详情编辑入口）

| 状态 | 详情 modal | 编辑器 | 主按钮 | 文案 |
| --- | --- | --- | --- | --- |
| Default | 打开 | 隐藏 | 可用 | — |
| Loading | **保持打开** + 内联 loading 条 | 隐藏 | 全组 Disabled | 「正在加载最新草稿」 |
| Success | 关闭（editor 已挂载后） | 显示 | 编辑器内操作 | — |
| Failed | 保持打开 + alert | 隐藏 | 重试可用 | 见 §3.3 |
| Empty Draft | 保持打开 + empty alert | 隐藏 | 禁 compile/trial/publish；可刷新 | 「草稿不可用」 |
| Stale | 当前详情不变 | 不变 | — | 无提示 |
| Permission denied | 无编辑入口 | — | — | — |
| Extreme：长名称 | 名称 truncate + title | 顶栏 truncate | — | — |

### 3.3 关键文案

| 场景 | 标题 | 正文 | 动作 |
| --- | --- | --- | --- |
| Loading | 正在加载最新草稿 | {name} · 同步 Draft 与编译结果 | 无 |
| Failed 通用 | 无法打开流程图 | 加载 Workflow 草稿失败；原详情已保留。{requestId?} | 重试加载 / 关闭 |
| Empty Draft | 草稿不可用 | 当前 Workflow 没有可读草稿，无法进入画布。 | 刷新详情 / 关闭 |
| 403 | （无入口） | — | — |

**禁止**：失败时仅列表级 `workflowActionNote`、关闭详情无反馈、展示空白 Start/End 默认图伪装成功。

### 3.4 实现锚点（供 Forge）

- `openWorkflowEditor`：**删除**“先关详情再 load”的顺序；改为详情内 LOADING → READY handoff。
- 可移除或降级现有 full-bleed loading overlay（`editorDraftLoadState === 'loading' && !workflowEditorVisible`）为详情内状态条，避免用户看到“空壳编辑器闪一下再消失”。
- request token 逻辑保留并强化。

---

## 4. UX-02 / UX-03：Console 能力与终态

### 4.1 信息架构（原位增强，不改布局骨架）

现有 `runtime-summary-list` 与顶部状态 badge 为 SoT 呈现面：

```text
[会话标题区]  状态 badge：排队中 | 执行中 | 待确认 | 已完成 | 运行失败 | 已取消
[摘要条]      意图 · 本轮能力
[可选 banner] 实时连接中断 / 校准中
[消息流]
[composer]    Enabled / Disabled 随终态
```

### 4.2 终态映射（与产品 §5.2 一致）

| Run 事实 | 顶部 badge 文案 | 意图 | 输入区 | 允许动作 |
| --- | --- | --- | --- | --- |
| PENDING | 排队中 | 识别中 | Disabled | 等待 |
| RUNNING | 执行中 | 识别中 或 执行中* | Disabled | 等待 |
| WAITING_CONFIRMATION | 待确认 | 待确认 | Disabled | 确认 / 拒绝 |
| SUCCEEDED | 已完成 | 已完成 | Enabled | 继续对话 |
| FAILED | **运行失败** | **未完成** | Enabled | 重试发送 / 继续输入 |
| CANCELLED | 已取消 | 未完成 | Enabled | 重新发起 |

\*意图在 RUNNING 且已有 TOOL step 时显示「执行中」；仅 MODEL 阶段显示「识别中」。  
**文案统一**：FAILED 顶部用「运行失败」（产品表），替代当前部分路径的「失败」单字，避免与步骤失败混淆。

### 4.3 收敛规则（AC-06）

1. 收到 `run.completed | run.failed | run.cancelled` **或** GET 校准到终态后，**同一 tick 更新**：
   - `runStatus` badge
   - `runtimeIntentLabel`
   - `conversationBusy` / composer disabled
2. **5 秒内** UI 不得再显示「执行中 / 意图识别中」与终态消息并存。
3. 终态 **单调**：后续旧 RUNNING 的 GET 不得降级 UI。
4. 重复 terminal frame：消息去重；badge 不变。

### 4.4 Tool 调用失败呈现（AC-05，用户可见层）

当某次 Tool invocation 在外部请求前失败：

- 消息流中出现 **结构化失败气泡**（assistant 或 system 样式，复用现有 error 样式）：
  - 标题：`工具调用未执行`
  - 正文：`「{Tool 显示名}」当前不可调用：{用户向原因}`  
    例：`服务连接未就绪` / `连接需处理` / `身份模式待迁移`
  - 元信息：`错误码 {code}` · `requestId` · 可选 `traceId`（可复制 monospaced 小字）
  - 动作（链接按钮，非强制）：**查看服务连接**（跳转 `/connections` 或带 query 的连接详情，若路由已有）、**打开审计 Trace**（若已有入口）
- **禁止**：Secret、Token、Broker body、内部 locator。
- **禁止**：把该失败渲染为「运行成功」。

纯文本成功（AC-04）时：不因 snapshot 中存在不可用 Tool 显示全局阻断 banner；能力数仍可显示 snapshot 中的能力项数（目录可见 ≠ 调用成功）。

### 4.5 SSE 降级 banner

| 条件 | banner（`role="status"`） | 输入区 |
| --- | --- | --- |
| 重连中 | 实时状态重连中… | 若 Run 未终态则保持 Disabled |
| 重连预算耗尽且 Run 未终态 | 实时状态连接中断，正在以持久记录校准。 | Disabled 至 GET 结果 |
| GET 校准到终态 | 移除 banner；应用 §4.2 | 按终态 |
| GET 仍 RUNNING | 无法确认实时进度，可刷新页面。 | Enabled（允许用户离开/重试），不假装成功 |

### 4.6 状态矩阵（Console）

| 状态 | 顶部 | 意图 | 消息区 | 输入 |
| --- | --- | --- | --- | --- |
| Loading sessions | skeleton | — | skeleton | Disabled |
| Empty session | 待运行 | — | 空态引导 | Enabled（有 Agent） |
| Sending / PENDING / RUNNING | 排队中/执行中 | 识别中/执行中 | live 更新 | Disabled |
| WAITING_CONFIRMATION | 待确认 | 待确认 | HITL 卡 | Disabled |
| Success | 已完成 | 已完成 | 最终消息 | Enabled |
| Failed | 运行失败 | 未完成 | 错误消息 + 可选 Tool 卡 | Enabled |
| Degraded | 校准中 / 中断 | 随 GET | 保留已渲染消息 | 见 §4.5 |
| Archived / Agent 不可用 | — | — | 只读 | Disabled + 新建会话 CTA |
| No EXECUTE | — | — | 只读说明 | 替换为缺权文案 |
| Extreme：超长错误 | — | — | 错误气泡 max-height + 展开 | — |

### 4.7 关键文案

| 键 | 文案 |
| --- | --- |
| failed badge | 运行失败 |
| intent incomplete | 未完成 |
| degraded | 实时状态连接中断，正在以持久记录校准。 |
| tool gate | 工具调用未执行 · 「{name}」当前不可调用（{reason}） |
| pure text success | （无额外 banner；正常 assistant 回复） |

---

## 5. UX-04：Smart DAG 失败恢复

### 5.1 位置与结构

在 `smart-copilot-panel` 内、**轮次历史与 Guard 报告之间**（或紧挨失败 turn 下方）插入持久 **恢复卡** `SmartDagRecoveryCard`：

```text
┌ 本轮生成未完成 ─────────────────────┐
│ 阶段：模型调用 · 会话仍为 OPEN        │
│ {安全 message}                       │
│ 错误码 {code} · requestId · traceId  │
│ [重试本轮] [关闭会话…]   或 [新建会话] │
└──────────────────────────────────────┘
```

- **替代**“仅 toast”作为主恢复面；toast 可保留 6～8s 作瞬时反馈，但卡必须常驻直到下一成功 turn、新建会话或用户关闭会话。
- 画布与上一版合法 Draft **完整保留**；输入框保留本轮文本。

### 5.2 阶段标签（用户语言）

| Stage 码 | 展示 |
| --- | --- |
| SESSION | 会话创建 |
| MODEL_CALL | 模型调用 |
| OUTPUT_PARSE | 输出解析 |
| GUARD | 图校验（Guard） |
| DRAFT_PERSIST | 草稿保存 |
| UNKNOWN | 未知阶段 |

正文模板（retryable）：

> 本轮在「{阶段}」失败，未修改上一版合法草稿。

### 5.3 动作矩阵（AC-07 / AC-08）

| 条件 | 输入框 | 生成按钮 | 恢复动作 |
| --- | --- | --- | --- |
| GENERATING | Disabled | busy「生成中…」 | 关闭会话 **Disabled**；tooltip：本轮不支持执行中取消 |
| OPEN + retryable | 保留文本，Enabled | **重试本轮** primary | **关闭会话** ghost → 确认 |
| OPEN + non-retryable | Enabled 或按 code 禁发 | 不显示无效重试 | **关闭会话** / **新建会话**；配置类 code 可链到 Agent/模型设置 |
| CLOSED | Disabled | Disabled | 仅 **新建会话** |
| 无 EDIT | 全部写操作隐藏/Disabled | — | 只读历史 |
| Guard 拒绝（可修订） | Enabled | 可再次发送修订 | 保留现有 Guard 列表；**不**当成本地成功 |

### 5.4 关闭会话确认（危险边界，低风险）

- 二次确认 dialog（复用现有 confirm modal 模式）：
  - 标题：`关闭生成会话？`
  - 正文：`关闭后不能继续本会话的生成轮次。已生成的 Workflow 草稿、历史轮次和审计记录会保留，不会删除。`
  - 主按钮：`关闭会话`（warning tone）
  - 次按钮：`取消`
- 关闭成功后：恢复卡切换为 CLOSED 态文案 + 新建会话 CTA。

### 5.5 状态矩阵（Smart DAG Copilot）

| 状态 | 恢复卡 | 画布 | Toast |
| --- | --- | --- | --- |
| IDLE | 无 | 空/上次 | — |
| GENERATING | 可选“进行中”占位（**不伪造真实进度百分比**；可保留现有步骤动画作装饰） | 冻结交互 | 可选 |
| SUCCEEDED | 移除失败卡；轮次历史追加成功 | 刷新 Draft | 成功 |
| FAILED_RETRYABLE | 显示卡 + 重试/关闭 | 保留 | 错误可淡出 |
| FAILED_FINAL | 显示卡 + 关闭/新建 | 保留 | 错误可淡出 |
| CLOSED | 显示卡 + 新建 | 只读草稿 | — |
| BUSY conflict | 短时 alert「其他操作进行中」 | 不变 | 是 |
| Permission | 无生成入口 | 只读 | — |

### 5.6 关键文案

| 键 | 文案 |
| --- | --- |
| card title | 本轮生成未完成 |
| retryable body | 本轮在「{stage}」失败，未修改上一版合法草稿。 |
| closed body | 生成会话已关闭；历史与草稿已保留。 |
| retry CTA | 重试本轮 |
| close CTA | 关闭会话 |
| new CTA | 新建会话 |
| no cancel | 生成进行中，暂不支持取消；请等待本轮结束。 |

---

## 6. UX-05 / UX-06：OpenAPI 详情

### 6.1 布局（双栏，桌面；单列堆叠，窄屏）

```text
┌ 导入详情 ──────────────────────────────────────┐
│ Hero：文件名 / status / Workspace              │
│ KPI：归属 · Provider · 连接 · **服务地址**      │
│      接口数量 · 可生成数 · [完整性 badge]       │
│ [完整性 / 加载 Error banner 若需要]             │
│ ┌ Endpoint 列表 (1/3) ┐ ┌ 选中契约 (2/3) ┐   │
│ │ GET /a  ready       │ │ 请求参数        │   │
│ │ POST /b ready  ✓    │ │ Body            │   │
│ │ ...                 │ │ 响应            │   │
│ │                     │ │ issues          │   │
│ └─────────────────────┘ └─────────────────┘   │
│ [关闭]              [生成 Tool 草稿]           │
└────────────────────────────────────────────────┘
```

### 6.2 服务地址（AC-09）

- 展示字段：**一个**规范化 HTTP(S) URL（技术 `normalizeServiceBaseURL` 的结果）。
- 无绑定 Connection：显示 **「未配置」**，**禁止** fallback 到 `integration.serviceConnections[0]`。
- 复制：可选 monospaced + title 全文；禁止展示拼接错误的双端口。

### 6.3 Endpoint 与契约（AC-10 / AC-11）

1. **移除**“顶部用第一条 endpoint 冒充全量 request/body/response 树”的汇总区作为主契约；契约 **仅对选中 endpoint** 展示。
2. 打开详情：Loading skeleton（列表 + 契约双栏），**禁止**先闪「0 节点」。
3. 列表项展示：`METHOD path` · summary · ready pill · issues 计数。
4. 默认选中：第一条 **eligible**（ready 且未生成且非认证基建）；若无 eligible 则第一条。
5. 合法空 schema 文案（按块）：
   - 请求参数：`该接口未声明请求参数`
   - Body：`该接口未声明请求体`
   - 响应：`该接口未声明响应结构`
6. **导入详情不完整**（摘要 totalEndpoints > 0 且列表为空，或 integrity=INCOMPLETE）：
   - banner `role="alert"`：`导入详情不完整，已禁止生成 Tool；请重新导入或联系管理员。`
   - 附 requestId；动作：**刷新详情**、（有 EDIT）**重新导入** 引导
   - **生成 Tool 草稿** Disabled
7. ready 数与列表 `ready=true` 不一致：KPI 旁 warning「数据异常」；生成仍 fail-closed（仅选中且 ready 的项）。

### 6.4 生成 Tool 交互

- 列表支持 **多选 checkbox**（默认勾选全部 eligible）。
- 主按钮：`生成 Tool 草稿（{n}）`；n=0 时 Disabled。
- 生成中：按钮 busy；列表选择 Disabled。
- 成功：toast + 保持详情或提示去 Tool 管理；不自动关闭导致用户看不见结果亦可接受，但须有明确成功文案。

### 6.5 状态矩阵（OpenAPI 详情）

| 状态 | 列表 | 契约区 | 生成按钮 |
| --- | --- | --- | --- |
| Loading | skeleton | skeleton「正在加载端点契约」 | Disabled |
| Complete + endpoints | 可选列表 | 选中契约 | n>0 且 EDIT 时 Enabled |
| Valid empty import（0/0） | empty「未解析到接口」 | — | Disabled |
| Valid empty schema on endpoint | 正常 | 分块 empty 文案 | 不受影响 |
| Incomplete / mismatch | banner + 空/异常列表 | 不伪造 | **Disabled** |
| Load Error | 保留摘要 KPI | error + 重试 | Disabled |
| No EDIT | 只读 | 只读 | 隐藏或 Disabled |
| Generating | 冻结 | 冻结 | busy |

### 6.6 关键文案

| 键 | 文案 |
| --- | --- |
| address missing | 未配置 |
| loading contracts | 正在加载端点契约 |
| empty import | 未解析到接口 |
| empty body | 该接口未声明请求体 |
| incomplete | 导入详情不完整，已禁止生成 Tool；请重新导入或联系管理员。 |
| data anomaly | 可生成数与接口明细不一致，已按明细门禁处理 |
| generate | 生成 Tool 草稿（{n}） |

---

## 7. UX-07：Tool 三维状态

### 7.1 展示模型

所有列表 pill 与详情 stack **固定三维**（可合成一行主 pill，但详情必须可拆开阅读）：

1. **生命周期**：草稿 / 待配置 / 待发布 / 已发布 / 已停用  
2. **最近测试**：未测试 / 测试通过 / 测试失败 + **时间**（有则显示；无真实记录不得因 Published 推断“测试通过”）  
3. **当前可调用性**：

| 码 | 用户文案 | tone |
| --- | --- | --- |
| AVAILABLE | 当前可调用 | success |
| NEEDS_ATTENTION | 当前不可调用（连接需处理） | danger |
| DISABLED | 当前不可调用（连接已停用） | neutral |
| MIGRATION_REQUIRED | 当前不可调用（身份待迁移） | warning |
| MISSING | 当前不可调用（连接缺失） | danger |
| LOADING | 连接状态加载中 | neutral |
| UNKNOWN | 连接状态未知 | neutral |

**合成主 pill 示例（列表）**：

- `已发布 · 当前可调用`
- `已发布 · 当前不可调用（连接需处理）`
- `已发布 · 当前不可调用（连接已停用）`
- `已发布 · 当前不可调用（连接缺失）`

详情治理 strip 补充：

- `测试通过于 2026-07-25 14:32；当前连接状态已变化`（当历史测试通过但 availability ≠ AVAILABLE）

### 7.2 关键修正（相对现状）

| 现状问题 | 目标行为 |
| --- | --- |
| catalog 未完成时 `!connection` →「连接缺失」 | catalog `LOADING` → **加载中**；仅 loaded 且 ID 无实体 → MISSING |
| Published 无 TestRecord 推断测试通过 | 显示 **未测试** 或后端真实 latestTest；禁止假通过 |
| 列表 subtitle 写死「连接缺失」 | 有实体则显示连接名；MISSING 才「连接缺失」 |
| 单一「连接需处理」不够区分停用/迁移/缺失 | 按上表分文案 |

### 7.3 恢复动作（不扩大范围）

| availability | 详情 CTA |
| --- | --- |
| NEEDS_ATTENTION | **处理服务连接** → 跳转连接详情/列表筛选 |
| DISABLED | 说明连接已停用；链到连接页（需 MANAGE 者可启用——本轮不新做启用流） |
| MIGRATION_REQUIRED | 链到连接迁移说明（复用既有出站迁移入口，不新设计） |
| MISSING | **修复绑定**（编辑 Tool 连接字段，需 EDIT） |
| LOADING/UNKNOWN | 无破坏性 CTA；可 **刷新连接目录** |

**不**提供：自动撤销发布、一键重测已发布版本（UX-09 非目标）。

### 7.4 状态矩阵（Tool）

| 状态 | 列表 pill | 详情 stack | 说明 |
| --- | --- | --- | --- |
| Loading catalog | `… · 连接状态加载中` | 第三维 Loading | 禁止 MISSING |
| Catalog error | `… · 连接状态未知` | UNKNOWN + 重试加载 | — |
| Published + test pass + conn ERROR | `已发布 · 当前不可调用（连接需处理）` | 三层分开展示 | AC-12 |
| Published + true missing | `已发布 · 当前不可调用（连接缺失）` | MISSING + 修复绑定 | AC-13 |
| Disabled tool | 已停用 | 生命周期优先 | 不强调连接 |
| No VIEW | 页面不可见 | — | — |

---

## 8. 跨页面一致性规则

1. **原位优先**：Loading / Error / Retry 出现在触发上下文（详情、Copilot、状态条），不静默跳转列表。
2. **终态单调**：Console Run、Smart 会话 CLOSED、Workflow handoff 成功后，旧异步响应不得回写。
3. **三层不混用**（Tool）：生命周期 ≠ 历史测试 ≠ 当前可调用。
4. **诊断脱敏**：仅稳定码、阶段、requestId/traceId、公开资源名；永不 Secret/Token。
5. **权限同构**：无权限 = 入口隐藏或 Disabled + 后端 403；mutation 在成员加载未知时 **fail closed**。
6. **危险操作**：本轮仅 Smart「关闭会话」需确认；无删除/自动发布按钮新增。

---

## 9. 组件建议（最小新增）

| 建议名 | 形态 | 使用处 | 备注 |
| --- | --- | --- | --- |
| `InlineLoadErrorBar` | 内联 status/alert + 重试 | Workflow 详情、OpenAPI 详情 | 可先做局部 markup，不强制拆文件 |
| `RuntimeStatusStrip` | badge + 意图 + 可选 degraded | Console 顶栏 | 基于现有 DOM 收敛逻辑 |
| `SmartDagRecoveryCard` | 持久卡片 | Smart Copilot | 替代 toast-only |
| `OpenAPIEndpointPicker` | 可选列表 + 多选 | OpenAPI 详情左栏 | |
| `OpenAPIEndpointContractPane` | 三树 + issues | OpenAPI 详情右栏 | 复用 `ToolSchemaTreeView` |
| `ToolAvailabilityMeta` | 三维解析 | `tool-governance.ts` + ToolsView | 扩展现有函数，避免第三套状态源 |
| `StableDiagMeta` | 小字 requestId/traceId | 各 Error 面 | 可复制 |

样式：复用 `status-pill`、`tool-status-pill`、`role="alert"`、现有 modal/drawer footer。

---

## 10. 完整状态总表（交付核对）

| 页面 | Default | Hover/Focus | Selected | Disabled | Loading | Empty | Error | Success | 权限不足 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Workflow 详情编辑 | 按钮可用 | focus ring | — | 无 EDIT 隐藏 | 详情内条 | 草稿不可用 | alert+重试 | handoff 编辑器 | 无入口 |
| Console 运行条 | 待运行 | — | 当前会话 | 运行中禁输入 | 会话 skeleton | 空对话引导 | 运行失败收敛 | 已完成 | composer 替换 |
| Smart 恢复卡 | 无卡 | 按钮 focus | — | 生成中禁关闭 | 生成占位 | 无 Draft 提示 | 恢复卡 | 卡消失+画布更新 | 无生成 |
| OpenAPI 详情 | KPI+双栏 | endpoint 行 hover | 当前 endpoint | 不完整禁生成 | 双栏 skeleton | 0 接口 | 保留摘要+重试 | 契约可读 | 禁生成 |
| Tool 列表/详情 | 三态 pill | 行 hover | 详情打开 | Tool/连接停用 | catalog Loading | 真缺失 | UNKNOWN | AVAILABLE | 只读 |

---

## 11. 键盘与可访问性

- 所有 modal：焦点陷阱、Esc 关闭（Loading/提交中可选忽略 Esc 或仅允许 Esc 取消非破坏等待）、关闭后焦点回触发控件。
- Workflow Loading：`aria-busy` 在详情 dialog；状态条 `role="status"`；Error `role="alert"`。
- Console：消息区保持 `aria-live="polite"`；终态切换不重复播报整页。
- Smart 恢复卡：`role="alert"`；主按钮 Tab 序为「重试 → 关闭」。
- OpenAPI endpoint 列表：方向键可选中（若实现成本高，至少保证鼠标/点击 + 可见 focus ring）。
- Tool pill：不只靠颜色；文字包含生命周期与可调用性。
- requestId/traceId：等宽、可复制；`aria-label`「请求编号」。
- 对比度：danger/warning 与 success 使用现有 token，不引入新色相系统。

---

## 12. 桌面与 390×844

| 断点 | Workflow 详情 | Console | Smart Copilot | OpenAPI 详情 | Tool 详情 |
| --- | --- | --- | --- | --- | --- |
| ≥1280 | 居中 modal | 三栏现状 | 左面板+画布 | 双栏 endpoint/契约 | 宽 modal |
| 768–1279 | 全宽 modal | 堆叠 | 面板可折叠 | 上下堆叠 | 全宽 |
| 390×844 | 全屏 sheet；加载条吸顶；动作按钮纵向 | 单列；状态条换行；composer 底栏 | Copilot 默认展开占上半；画布可折叠 | 全屏；endpoint 横向 chip 滚动 + 契约下方 | 全屏；三 pill 换行 |

本轮 **不**为 390 单独开新交互流程；保证关键动作（重试、关闭会话、生成禁用原因）在一屏内可达。

---

## 13. AC → UI 验收映射

| AC | UI 验证点 |
| --- | --- |
| AC-01 | 点击编辑 → 详情内 Loading → 编辑器出现；无默认空图 |
| AC-02 | 强制 Draft 失败 → 详情仍在 + 重试；无静默列表 |
| AC-03 | 快切两个 Workflow；OPERATOR/VIEWER 无编辑按钮 |
| AC-04 | 纯文本成功；无“连接未就绪”全局阻断（无关 Tool） |
| AC-05 | 实际 Tool 失败气泡：码 + 名 + 可行动；无 Secret |
| AC-06 | 终态后 5s 内 badge/意图/输入一致；刷新仍终态 |
| AC-07 | 恢复卡阶段/OPEN/重试/关闭；重试成功不自动 publish |
| AC-08 | CLOSED 禁发送 + 新建；关闭确认不删草稿 |
| AC-09 | 地址单一端口；无连接「未配置」 |
| AC-10 | N 条 endpoint 可切换各自契约；ready 一致 |
| AC-11 | 合法空文案 vs 不完整禁生成 |
| AC-12 | 已发布·不可调用（需处理）+ 历史测试时间 |
| AC-13 | Loading 非 MISSING；真缺失才 MISSING |
| AC-14 | VIEWER 无写入口；错误无 Secret |
| AC-15 | Chrome 路径见 §15 |

---

## 14. Forge 实施标注（待技术方案批准后）

> 非 checklist；仅 UI 层优先顺序与文件提示。真正 checklist 仍由 Knower 在技术批准后发布。

| 序 | 项 | 主要文件 | 完成定义（UI） |
| --- | --- | --- | --- |
| F1 | Workflow 详情内 Loading/Error/Retry + handoff | `WorkflowView.vue`、`workflow.ts` | AC-01～03 视觉与交互 |
| F2 | Console 终态单调 + 文案 + degraded | `ChatExecutionView.vue`、`chat.ts`、`run-event-stream.ts` | AC-06；FAILED=运行失败 |
| F3 | Tool 失败结构化气泡 | `ChatExecutionView.vue` | AC-05 可见层 |
| F4 | Smart 恢复卡 + 关闭确认 | `SmartDagView.vue`、`smartdag.ts` | AC-07～08；非 toast-only |
| F5 | OpenAPI 双栏 + 地址 + 完整性 | `OpenAPIImportsView.vue`、`openapi-preview.ts` | AC-09～11 |
| F6 | Tool 三维 + catalog loading | `tool-governance.ts`、`ToolsView.vue` | AC-12～13 |
| F7 | 权限入口隐藏矩阵 | 上述 views + `workspaces.ts` | AC-14 UI 侧 |

**明确不做（本轮）**：UX-08 Toast 全局改造、UX-09 只读重测、UX-10 登录噪声、Smart 取消按钮、OpenAPI 自动修复按钮。

---

## 15. Sentinel Chrome 路径（UI 观察点）

环境：真实 Chrome；视口建议 1600×1100 + 抽样 390×844。

1. **Workflow**  
   - 详情 → 见 Loading 条（详情未关）→ 编辑器。  
   - Mock/故障 Draft → 详情 Error + 重试 → 成功进入。  
   - VIEWER：无「编辑流程图」。

2. **Console**  
   - 纯文本成功：无无关连接阻断。  
   - 诱发 Tool 门禁失败：气泡含码与 Tool 名。  
   - 丢 terminal：5s 内「运行失败 / 未完成 / 输入可用」。

3. **Smart DAG**  
   - 失败：恢复卡阶段 + OPEN + 重试/关闭。  
   - 关闭确认文案含“不删除草稿”。  
   - CLOSED：新建会话。

4. **OpenAPI**  
   - 地址无双端口。  
   - 切换 endpoint 契约变化。  
   - 不完整：banner + 生成 Disabled。

5. **Tool**  
   - catalog 慢网：先「加载中」非「缺失」。  
   - Published + 连接 ERROR：合成文案正确；历史测试时间仍在。

6. **安全抽检**  
   - DOM/截图/Network 预览无 Secret；requestId 可见。

证据写入新 verification 目录，不覆盖 `pm-e2e-ux-2026-07-25`。

---

## 16. 与 Knower 技术方案的衔接

| 技术项 | UI 立场 |
| --- | --- |
| T1=A Draft+Readiness 并行 | UI 只消费 context 成功/失败；不假设新聚合 API |
| T2=A lazy resolve | UI 不在发送前展示“能力已全部预检失败” |
| T3=A terminal+GET | UI 严格单调终态（§4.3） |
| T4/T5 Smart | 恢复卡字段对齐 `SmartDagFailureState` |
| T6=A integrity | incomplete banner + 生成门禁 |
| T7=A latestTest + catalog | 三维分离；Loading≠MISSING |
| T8=A 权限矩阵 | 入口隐藏/Disabled 规则 §3.1 / §5.3 |
| T9 backend-first | 前端对缺失 additive 字段 fallback：阶段 UNKNOWN、无 requestId 则省略 |
| **T10=A** | **本文件即为展开规格** |

若技术批准时 T10 改为 B/C，或任一选择改变用户流程/AC，须退回 Canvas 修订并可能请负责人确认。

---

## 17. 确认状态

| 项 | 状态 |
| --- | --- |
| 产品设计 | v1.0 Approved（输入） |
| 本 UI 设计 | **UI v0.1** — 供 Knower 并入技术方案；**无新增负责人未决项** |
| 是否改变冻结流程/AC | **否** |
| 是否交 Forge | **否**（待技术批准 + checklist） |
| 生产代码 | **无** |

交付路径：`docs/design/zkl-56-pm-e2e-ux-fixes-ui-design.md`
