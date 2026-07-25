# 出站用户态鉴权重构：UI 设计

| 字段 | 内容 |
| --- | --- |
| Issue | ZKL-51「出站架构设计调整」 |
| 文档版本 | UI v0.1 |
| 日期 | 2026-07-24 |
| 状态 | Draft for Knower / Forge 引用；不改变产品或技术契约 |
| 产品基线 | `outbound-user-auth-product-design.md` v0.3（已冻结） |
| 技术基线 | `outbound-user-auth-technical-design.md` v0.1（待负责人批准 T1～T5） |
| 作者角色 | Canvas · UI 设计师 |
| 适用范围 | ServiceConnection 策略选型、硬切迁移呈现、运行调试台改名与独立调试凭据绑定；并覆盖 Provider 契约区、Tool test / Workflow trial 透传输入的最小一致面 |

> 本文是 UI 交互与呈现输入，不是产品需求、不是技术方案、不是 implementation checklist。  
> 不得新增第三种出站模式、共享账号、`NONE` 或 SYSTEM 例外。  
> 若后续 UI 选择会改变用户流程、权限可见性、危险操作语义、跨页面一致性或 AC1～AC21，必须回 Issue 请负责人确认；不得自行冻结。

### 修订记录

| 版本 | 变更 | 依据 |
| --- | --- | --- |
| UI v0.1 | 首轮 UI 输入：Connection 策略选型、迁移态、运行调试台与调试凭据、组件/状态矩阵、Forge 标注与 Chrome 验收路径 | 产品 v0.3 §9 / AC2·AC15·AC16；技术 v0.1 §4 / §5.3 / §9 / §13 |

---

## 1. 设计目标与约束

### 1.1 目标

1. 让 OWNER / ADMIN 在 ServiceConnection 上**显式固定** `BROKER_OBO` 或 `REQUEST_PASSTHROUGH`，字段随策略展开，零歧义。
2. 硬切后旧 Connection 以 **`DISABLED + MIGRATION_REQUIRED` 双维度**呈现，禁止与通用 Error 混用；提供可完成的显式迁移路径。
3. 将「对话式执行控制台」统一改名为「运行调试台」，强化内部调试定位，并提供**不进入聊天正文**的独立调试凭据绑定界面。
4. 配置状态与用户态调用结果分离；敏感 Token 永不回显、不持久、不进入历史。

### 1.2 非目标

- 不设计最终用户 OAuth consent / 长期凭据库 UI。
- 不提供 External Subject 模拟器或 SYSTEM 例外入口。
- 不在 Chat message 文本框接收业务 Token。
- 不改变 Workflow Draft / Compilation / Revision 的核心信息架构（仅增加策略/迁移门禁提示）。
- 不冻结 T1～T5 的视觉细节（机器认证形态、Vault 多实例等后端选择不影响首期布局骨架）。

### 1.3 复用基线（现有前端）

| 现有资产 | 路径 / 标识 | 本设计用法 |
| --- | --- | --- |
| 服务连接列表 / 表单 / 详情 | `ServiceConnectionsView.vue` | 改造列、状态 pill、表单第一步、迁移入口 |
| Provider 管理 | `ProvidersView.vue` | 增加「用户态出站鉴权」契约区 |
| 对话式执行 | `ChatExecutionView.vue` + 导航 `chat` | 改名 + banner + 调试凭据面板 |
| 管理列表 / 行操作 / 分段筛选 | `ManagementList`、`ManagementRowActions`、`ManagementSegmentedFilter` | 新增筛选维度与操作项 |
| 危险删除确认 | Connection 删除对话框模式 | 扩展为 impact preview 二次确认 |
| Tool test / Workflow trial | `ToolTestDialog`、`WorkflowTrialRunDialog` | 按需增加一次性 Token 区 |
| 角色能力 | `workspaces.can(…, "MANAGE")` 等 | OWNER/ADMIN 策略与 Secret；EDITOR 元数据 only |

---

## 2. 信息架构

```text
集成接入
├── Provider 管理
│   └── [新] 用户态出站鉴权契约区（supportedModes / Broker 摘要 / 透传摘要 / Subject types）
└── 服务连接
    ├── 列表（策略列 + 配置状态 + 迁移态）
    ├── 详情（配置状态 ≠ 最近用户态调用）
    ├── 新建 / 编辑表单（第一步：出站身份策略）
    └── 迁移向导（仅 MIGRATION_REQUIRED + OWNER/ADMIN）

交互与运行时
└── 运行调试台  ← 原「对话式执行控制台」
    ├── 非生产定位 banner
    ├── 当前 Subject 条
    ├── 会话 / 流式运行 / HITL / Trace（保留）
    └── [新] 调试凭据绑定抽屉 / 面板（独立 attach）

相关执行入口（最小一致面，非本页主交付但必须一致）
├── Tool test 对话框：透传 Connection 时的一次性 Token 区
└── Workflow trial 对话框：同上
```

导航变更：

| 位置 | 现文案 | 新文案 | route |
| --- | --- | --- | --- |
| 侧栏 | 对话式执行控制台 | 运行调试台 | `/chat`（保持，避免外链断裂；document title / h1 / 面包屑全部改名） |
| 页面 h1 | 对话式执行 | 运行调试台 | 同页 |
| document title | 随路由 | `运行调试台 · ActWeave` | 同页 |

> 首期**不**新增独立 `/debug` 路由，避免并行入口分裂；若产品后续要求短链，可加 redirect。

---

## 3. ServiceConnection：策略选型

### 3.1 用户路径

#### P1 新建（OWNER / ADMIN）

1. 列表 →「新建服务连接」。
2. **步骤 0 / 首屏强制区**：选择「出站身份策略」。
   - 卡片单选（非下拉隐藏）：
     - **Broker / OBO**（`BROKER_OBO`）
     - **本次请求透传**（`REQUEST_PASSTHROUGH`）
   - 选项是否可选 = Provider `supportedModes` 交集；Provider 仅支持一种时默认选中且另一项 disabled + 说明。
   - Provider 尚未声明 `outboundIdentity` 时：整组 disabled，提示「请先完成 Provider 用户态出站契约」。
3. 选择策略后展开后续字段（见 §3.2）。
4. 主操作：
   - 保存草稿 → `UNVERIFIED + migrationState=NONE`（新建无迁移态）
   - 保存并验证 → 配置级验证；成功 → `VERIFIED`
5. 不允许保存第三种模式、旧 `authMode`、共享 Secret 作为业务身份。

#### P2 编辑已有目标态 Connection

1. 打开编辑表单：策略只读展示 +「切换策略」危险入口（仅 OWNER/ADMIN）。
2. 切换策略必须走 **impact preview → 二次确认 → 新 policyVersion**（服务端 proof）。
3. EDITOR：策略、注入规则、机器凭据只读；可改名称等非敏感元数据。
4. VIEWER / 无权限：整表只读 + 缺权说明。

#### P3 从迁移态进入（见 §4）

### 3.2 表单字段布局

表单仍使用现有「全屏式 dialog workspace」模式（`connection-form-modal`），内部改为 **策略驱动分区**：

```text
┌─ 基本信息 ─────────────────────────────────────┐
│ 连接名称*  环境*  Provider*（edit 锁定）        │
│ Provider 端点（只读）                           │
└────────────────────────────────────────────────┘
┌─ 出站身份策略 * ───────────────────────────────┐
│ (○) Broker / OBO     (○) 本次请求透传          │
│ 简短差异说明 + 运行时要求摘要                    │
└────────────────────────────────────────────────┘
┌─ 策略配置（随 mode 切换） ─────────────────────┐
│ BROKER_OBO:                                      │
│   · Provider 契约摘要（endpoint / audience /     │
│     machineAuthMethod / injection 只读）         │
│   · clientId*                                    │
│   · scopes*（Provider allowlist 多选）           │
│   · maxTokenTtlSeconds（默认 300）               │
│   · 机器凭据*（password 控件，仅 configured 态） │
│ REQUEST_PASSTHROUGH:                             │
│   · Token 类型摘要（只读自 Provider）            │
│   · 注入头 / 前缀摘要（只读自 Provider）         │
│   · maxResidenceSeconds（默认 600）              │
│   · 明确文案：不保存用户业务 Token               │
└────────────────────────────────────────────────┘
┌─ 验证（可折叠） ───────────────────────────────┐
│ 配置级检查：schema / DNS·TLS / 机器凭据 active   │
│ 文案强调：不伪造最终用户、不调业务 API            │
└────────────────────────────────────────────────┘
```

**文案（中文产品名）**

| 枚举 | 列表 / 徽章 | 卡片标题 | 一行说明 |
| --- | --- | --- | --- |
| `BROKER_OBO` | Broker / OBO | Broker / OBO | 用机器信任换当前用户的短期业务 Token；不保存用户 Token |
| `REQUEST_PASSTHROUGH` | 请求透传 | 本次请求透传 | 每次执行由调用方附带 Token；离开运行后不可恢复 |

**运行时要求 chips（详情 / 列表辅助）**

| mode | chips |
| --- | --- |
| `BROKER_OBO` | `需要 Subject` · `配置级验证` · `按用户换 Token` |
| `REQUEST_PASSTHROUGH` | `需要 Subject` · `每次请求需 Token` · `不持久化 Token` |

### 3.3 列表列与筛选

在现有列基础上：

| 列 key | 标签 | 内容 |
| --- | --- | --- |
| `outboundMode` | 身份策略 | 徽章：Broker / OBO \| 请求透传 \| **需迁移**（无 mode 或 legacy） |
| `status` | 配置状态 | 未验证 / 可用 / 错误 / 已停用（保留现有语义） |
| `migrationState` | 迁移 | 无 / **需迁移**（仅 `MIGRATION_REQUIRED` 显示） |
| 移除或降级 | 原「认证方式」`authMode` | 目标态隐藏；迁移态详情内可展示「旧认证（只读）」供对照，不作为可选项 |

分段筛选建议：

- 配置状态：全部 / 已验证 / 未验证 / 错误 / 已停用  
- 迁移：全部 / **待迁移**（`MIGRATION_REQUIRED`）  
- 策略：全部 / Broker/OBO / 请求透传  

默认进入列表时：若 Workspace 存在任一 `MIGRATION_REQUIRED`，顶部展示 **迁移 banner**（见 §4.2），不强制改默认筛选。

### 3.4 详情页

详情拆为两套状态，禁止合并：

1. **配置状态卡**  
   - status + migrationState  
   - 最近配置验证时间 / 结果（稳定错误码）  
   - 机器凭据：已配置 / 未配置（无指纹展示要求可保留现有 fingerprint 若 API 返回；**禁止**展示 Secret 明文）
2. **运行时要求卡**  
   - mode chips  
   - Subject 类型支持（来自 Provider）  
   - 「最近用户态调用」摘要：成功 / 用户授权失败 / 凭据缺失…（来自审计或 invocation 摘要 API，若暂无 API 则首期占位「暂无摘要」，不得把业务 403 写成 Connection ERROR）
3. **策略契约卡**  
   - 非敏感字段只读展示  
   - policyVersion 只读

### 3.5 危险操作确认（Connection）

触发（产品 §9.5 + 技术 §5.4）：

- 策略 `BROKER_OBO` ↔ `REQUEST_PASSTHROUGH` 切换  
- 注入头 / 允许原点（Provider 侧变更在 Provider 页）/ Broker audience（Provider）  
- 更换 / 撤销机器凭据  
- 禁用被已发布物引用的 Connection  
- 迁移旧 Connection  

弹窗结构（复用删除确认布局，扩展 impact 区）：

```text
标题：确认更改出站策略 / 确认迁移 / …
影响摘要（服务端 impact API）：
  · 已发布 Tool：N
  · Agent binding：N
  · Workflow Revision：N
说明：更改后相关执行将使用新策略版本；进行中的临时 Token 将失效。
不展示：Secret、Token、Broker body
操作：取消 | 确认更改（需勾选「我了解发布物需重新验证/编译」可选，首期可用明确按钮文案代替）
```

校验：

- impact proof 过期 / lock 漂移 → 关闭确认并 toast「影响范围已变化，请重新确认」  
- 权限不足 → 403 友好文案 + 审计由后端完成  

### 3.6 权限可见性

| 角色 | 列表 | 详情 | 新建/策略/Secret | 元数据编辑 | 验证 | 迁移 |
| --- | --- | --- | --- | --- | --- | --- |
| OWNER / ADMIN | 全 | 全 | 是 | 是 | 是 | 是 |
| EDITOR | 全 | 全 | 只读 | 是 | 是 | 否（入口隐藏 + 说明） |
| OPERATOR | 全 | 全 | 只读 | 否 | 是 | 否 |
| VIEWER | 全 | 全 | 只读 | 否 | 否 | 否 |

缺权控件：`disabled` + `title` / 旁注「需要 OWNER 或 ADMIN」。

---

## 4. 硬切后 `DISABLED + MIGRATION_REQUIRED` 呈现

### 4.1 状态模型（UI 映射）

与技术 §9.1 对齐，UI 展示 **双标签**，不合并为单一 Error：

| 持久组合 | 配置状态 pill | 迁移 badge | 主 CTA | 可执行？ |
| --- | --- | --- | --- | --- |
| `DISABLED` + `MIGRATION_REQUIRED` | 已停用（灰） | **需迁移**（琥珀） | 迁移连接 | 否 |
| `UNVERIFIED` + `MIGRATION_REQUIRED` | 未验证 | 迁移中 | 继续配置 / 验证 | 否 |
| `ERROR` + `MIGRATION_REQUIRED` | 错误 | 迁移中 | 修复并验证 | 否 |
| `VERIFIED` + `NONE` | 可用 | — | 编辑 | 是 |
| 其他目标态 | 同现网 | — | 常规 | 仅 VERIFIED |

> **禁止**把 `MIGRATION_REQUIRED` 画成红色「错误」。红色保留给验证失败 / `ERROR`。迁移是「必须人工处理的阻断态」。

### 4.2 列表与全局提示

**Workspace 级 banner**（Connections 列表顶）：

```text
[!] 本空间有 N 个服务连接需要迁移到用户态出站策略
    旧共享账号与无鉴权连接已停用，相关测试 / 试运行 / 发布 / 生产执行会失败。
    [查看待迁移]  [了解策略差异]
```

**行呈现**：

- 名称旁琥珀 badge「需迁移」  
- 策略列显示「— / 旧配置」而非伪造新 mode  
- 行操作：
  - OWNER/ADMIN：`迁移连接`（主）、详情、删除（危险）  
  - 其他角色：详情；测试 / 绑定类操作 disabled，tooltip：`OUTBOUND_IDENTITY_MIGRATION_REQUIRED` 用户文案  

**稳定错误码用户文案**（列表 toast / 行内）：

| 稳定码 | 用户可见文案 |
| --- | --- |
| `OUTBOUND_IDENTITY_MIGRATION_REQUIRED` | 该连接仍使用旧鉴权，需 OWNER/ADMIN 迁移为 Broker/OBO 或请求透传后再执行 |
| `OUTBOUND_SUBJECT_REQUIRED` | 当前执行缺少最终用户身份，无法调用用户态业务 API |
| `OUTBOUND_CREDENTIAL_REQUIRED` | 请先为本次执行绑定目标连接的业务 Token |
| `OUTBOUND_CREDENTIAL_EXPIRED` | 调试/透传凭据已失效，请重新绑定后新建执行 |

### 4.3 迁移向导（OWNER / ADMIN）

入口：行操作「迁移连接」或详情主按钮。  
形态：现有 form dialog 的 **migration mode**（`formMode=migrate`），非独立路由（首期减少导航分叉）。

步骤：

1. **只读对照**  
   - 旧认证类型摘要（API Key / OAuth client / NONE…）— 只读  
   - 关联 Tool / binding / Workflow 数量（impact）  
   - 明确：「不会自动猜测目标策略，也不会复制旧 Secret 到透传」
2. **选择目标策略**  
   - 与新建相同的双卡片单选  
3. **填写目标配置**  
   - 同 §3.2  
4. **确认影响**  
   - impact proof 二次确认  
5. **保存** → 仍为 `MIGRATION_REQUIRED` + `UNVERIFIED`（技术规则）  
6. **配置级验证** → 成功才 `VERIFIED + NONE`  
7. 成功页：提示「请重新校验引用该连接的 Tool / Agent / Workflow 编译与发布」

取消 / 关闭：未保存丢弃确认（复用 dirty dialog）。

### 4.4 引用侧门禁（跨页一致）

凡绑定或执行依赖 Connection 的 UI：

| 页面 | 行为 |
| --- | --- |
| Tool 详情 / 绑定 | 下拉禁用迁移中 Connection；旁注「需迁移」 |
| Tool test | 主按钮 disabled + 稳定码文案；不打开空跑 |
| Agent capability 绑定 | 同上 |
| Workflow 编译问题面板 | issue 类型：`OUTBOUND_IDENTITY_MIGRATION_REQUIRED`，链到 Connection 详情 |
| Workflow trial / publish | 阻断 + 说明 |
| 运行调试台 | 发送前若 plan 依赖迁移 Connection → 发送 disabled + 横幅 |

---

## 5. 运行调试台：改名与独立调试凭据

### 5.1 改名与定位

| 元素 | 设计 |
| --- | --- |
| 侧栏 label | 运行调试台 |
| 图标 | 建议由 `fa-regular fa-comment-dots` 改为 `fa-solid fa-flask` 或 `fa-solid fa-terminal`（与「对话产品」区分；若改动过大可暂留图标只改文案） |
| 页内 h1 | 运行调试台 |
| 副文案 | 内部调试入口 · 非第三方最终用户产品 |
| **永久 banner**（会话区顶部，不可关闭或仅可折叠但仍在侧栏提示） | 「仅用于 Workspace 内部调试与 HITL 验证，不会把你变成第三方终端用户。」 |

### 5.2 当前 Subject 条

在 runtime header 摘要旁增加：

```text
Subject  当前用户 · ACTWEAVE USER · {displayName}
```

约束文案（tooltip）：

- Broker 调试将以**你本人**换取 Token；不能模拟 External Subject。  
- 无「切换为 SYSTEM」或「粘贴外部 sub」控件。

归档会话：Subject 条保留只读历史操作者展示（若 API 有），composer 与凭据绑定均禁用。

### 5.3 调试凭据绑定（独立 attach）

对齐技术 §5.3 两步命令：先 `POST .../outbound-credentials`，再 message 仅带 `outboundCredentialAttachmentId`。

#### 5.3.1 入口

- Composer 上方工具条按钮：**绑定出站凭据**  
  - 图标：`fa-solid fa-key`  
  - 仅当「当前 Agent 静态已知需要 `REQUEST_PASSTHROUGH` Connection」或用户主动打开时显示主按钮；Broker-only Agent 可将按钮降级为次要「查看出站要求」（只读说明，无 Token 输入）。
- 快捷键：`Alt+Shift+K`（桌面）；移动端在「更多」菜单。

#### 5.3.2 面板形态

- 桌面 ≥1024：会话区底部 **可展开抽屉**（不盖住消息主列）  
- 390×844：底部 sheet（`role="dialog"`，焦点陷阱，与现有 side panel 一致）

```text
┌─ 调试凭据绑定 ───────────────────────────── [×] ─┐
│ 一次性 · 不保存 · 不进入会话历史 · 离开页面即丢弃   │
│                                                    │
│ 目标连接 *  [下拉：仅本 Agent 需要的 PASSTHROUGH]   │
│ 业务 Token * [password 输入 · autocomplete=off]     │
│ 过期时间 *   [datetime-local 或 ISO；T3 若定为必填] │
│                                                    │
│ 状态：未绑定 | 已绑定（至 HH:mm:ss）| 已失效        │
│ [清除]                    [绑定到本次发送]          │
└────────────────────────────────────────────────────┘
```

#### 5.3.3 交互规则

1. **password 控件**：`type="password"`、`autocomplete="off"`、`spellcheck="false"`；禁止 show-password 切换（避免肩窥与截图习惯）。  
2. **不回填**：打开面板永远空值；刷新 / 路由离开 / 会话切换立即清空本地 state。  
3. **不进 Pinia persist / localStorage / sessionStorage / 消息 draft**。  
4. 绑定成功：  
   - 本地只保留 `attachmentId`、`expiresAt`、`connectionIds`、UI 状态「已绑定」；  
   - **立即清空** Token 输入框 DOM value。  
5. 发送消息：  
   - request body 仅 `content + outboundCredentialAttachmentId`；  
   - 发送成功后 attachment 视为已消费，UI 状态回到「未绑定」。  
6. 发送失败且码为 `OUTBOUND_CREDENTIAL_EXPIRED` / attachment 无效：提示重新绑定；**不**自动重试带 Token 的请求。  
7. 重复绑定同一 Connection：以前端校验 + 后端 400 为准，文案「同一连接请勿重复绑定」。  
8. 已归档会话：按钮 disabled。  
9. 多 Connection：列表逐条绑定（同一 envelope 多 binding）；UI 用可增行「再添加连接」。  
10. HITL 等待确认期间：展示「确认通过后才会读取已绑定凭据」；不在确认前展示 Token。

#### 5.3.4 Broker-only 调试

- 无 Token 输入。  
- 侧栏 / 凭据面板只读说明：「将以当前内部用户调用 Broker；若 Provider 不支持 USER Subject，执行将返回 `OUTBOUND_SUBJECT_REQUIRED`。」  
- 失败时展示稳定码 + Trace 入口，不展示 Broker body。

### 5.4 会话历史与脱敏

- 消息气泡、会话列表、运行详情、Trace **永不**渲染 Token / attachmentId。  
- 系统事件若有「凭据已附加 / 已丢弃」，仅显示非敏感分类文案。  
- Empty 态文案改为偏调试：「选择 Agent 后开始内部调试运行；涉及敏感能力时仍会请求确认。」

### 5.5 页面状态矩阵（运行调试台）

| 状态 | 表现 |
| --- | --- |
| Loading | 会话 skeleton；发送与绑定 disabled |
| Empty | 引导选择 Workspace / Agent；无历史时空态 |
| Ready | 可输入；按需绑定凭据 |
| Credential bound | 状态 chip「已绑定 · 剩余 mm:ss」倒计时（本地基于 expiresAt） |
| Credential expired | chip 变警告；发送前拦截 |
| Running / Pending | 发送 disabled；绑定区只读 |
| WAITING_CONFIRMATION | HITL 卡；说明确认后才取凭据 |
| Success | 终态 + 步骤；凭据区复位未绑定 |
| Failed | 稳定错误码 + 可打开 Trace；脱敏 |
| Archived / Agent unavailable | 只读历史；绑定与发送 disabled |
| Permission denied | 无 EXECUTE 时 composer 替换为缺权说明 |
| Migration dependency | 顶栏警告；发送 disabled |

---

## 6. Provider 契约区（支撑选型，最小交付）

在 `ProvidersView` HTTP Provider 表单增加分区 **「用户态出站鉴权」**：

- 支持策略：多选 chips，仅 `BROKER_OBO` / `REQUEST_PASSTHROUGH`  
- Broker 子表：endpoint、audience、machineAuthMethod、allowedScopes、response path、businessInjection  
- 透传子表：credentialTypes、businessInjection  
- supportedSubjectTypes：`EXTERNAL_SUBJECT`、`USER`（无 SYSTEM）  
- **禁止**任何最终用户 Token 输入控件  
- 保存后 Connection 选型选项即时受约束  

权限：与策略配置一致，OWNER/ADMIN 可写。

---

## 7. Tool test / Workflow trial 透传输入（一致面）

| 入口 | UI |
| --- | --- |
| `ToolTestDialog` | 当 Tool 绑定 Connection 为 `REQUEST_PASSTHROUGH` 且 VERIFIED：在参数区之上增加「出站凭据」password + expiresAt；提交进专用 envelope 字段，不进 tool input JSON |
| `WorkflowTrialRunDialog` | 同上；多 Connection 时按 requirements 列表逐条 |
| Connection 为 Broker | 仅说明「将以当前用户 Subject 换取」 |
| Connection 迁移中 / 非 VERIFIED | 主按钮 disabled + 迁移/验证引导 |

规则同调试台：一次性、不回显、关闭对话框即丢弃、不进 localStorage。

---

## 8. 组件复用与新增建议

| 组件（建议名） | 职责 | 复用 |
| --- | --- | --- |
| `OutboundModePicker` | 双卡片策略单选 | Connection 新建 / 编辑 / 迁移；只读态用于详情 |
| `OutboundStatusPills` | 配置状态 + 迁移 badge | 列表单元格、详情头 |
| `OutboundRuntimeChips` | 运行时要求 chips | 详情、调试台 |
| `ConnectionImpactConfirmDialog` | impact preview 二次确认 | 策略切换 / 迁移 / 禁用 / 删机凭 |
| `MigrationBanner` | Workspace 待迁移条 | Connections 列表 |
| `DebugOutboundCredentialPanel` | attach UI | 运行调试台 |
| `OneShotSecretField` | password + 不持久协议 | 调试台 / Tool test / trial / 机器凭据录入 |
| `StableErrorAlert` | 稳定码 → 用户文案映射 | 全局执行失败 |

样式：延续现有 management / chat CSS 变量与 pill 体系，不引入新设计系统。

---

## 9. 完整状态矩阵（Connection 配置面）

| 状态 | 列表 | 详情 | 表单 | 操作 |
| --- | --- | --- | --- | --- |
| Default | 正常行 | 分区卡 | 可编辑字段 | 按权限 |
| Hover | 行底纹；操作显式 | — | 控件 hover | — |
| Focus | 键盘 focus ring | 可聚焦 code | 字段 focus | 按钮 focus |
| Selected | 行选中（若支持） | 当前详情 | 策略卡片 selected | — |
| Disabled | 已停用 pill | 横幅「不可绑定/执行」 | 字段按权限 disabled | 执行类隐藏 |
| Loading | 列表 skeleton | 卡 skeleton | 提交中按钮 busy | 防重复提交 |
| Empty | 引导建 Provider→Connection | — | 空 Provider 选项说明 | 链到 Provider |
| Error | 错误 pill + 码 | 配置错误摘要 | 字段级 / 表单级错误 | 重试验证 |
| Success | 可用 pill | 已验证时间 | toast 已保存/已验证 | — |
| Migration required | 双标签 + banner | 迁移 CTA | 向导 | 禁止 test/trial |
| Permission denied | 只读 | 只读 + 说明 | 只读 | 写操作隐藏 |
| Extreme：长名称 / 多 scope | truncate + title | 折行 | 多选滚动 max-height | — |
| Extreme：影响 N 很大 | — | — | impact 列表滚动 + 总数 | 确认仍可用 |

---

## 10. 表单校验（前端）

| 字段 | 规则 |
| --- | --- |
| mode | 必填；必须 ∈ Provider.supportedModes |
| clientId（Broker） | 必填，trim 非空 |
| scopes | 非空子集 |
| maxTokenTtlSeconds | 30–900 整数 |
| maxResidenceSeconds | 30–3600 整数 |
| 机器凭据 | 新建 Broker 必填；编辑未 dirty 可保留 configured |
| 透传 Token（运行时入口） | 非空；禁止前后空白-only |
| expiresAt（若 T3=A） | 必填且 > now |
| 策略切换 | 必须持有未过期 impact proof |

错误展示：字段下 `connection-field-error`；提交失败顶栏 `role="alert"`。

---

## 11. 键盘与可访问性

- 策略卡片：`role="radiogroup"` / `role="radio"`，方向键切换，Space 选中。  
- 所有 dialog / sheet：焦点陷阱、Esc 关闭（提交中忽略）、返回焦点到触发器。  
- 迁移 badge 与状态 pill：不只靠颜色，附带文字。  
- password 字段：关联可见 label；`aria-describedby` 指向「不会保存」说明。  
- 调试台 banner：`role="status"`。  
- 稳定错误：`role="alert"`。  
- 对比度遵循现有深/浅管理台 token；琥珀迁移色与红错误色色相分离。  
- 390 宽：列表转现有移动行模式；表单单列；调试凭据用 bottom sheet。

---

## 12. 桌面与 390×844 行为

| 断点 | Connection 表单 | 迁移 banner | 调试台凭据 |
| --- | --- | --- | --- |
| ≥1280 | 双列字段网格 | 单行 banner | 底栏抽屉 |
| 768–1279 | 单列 | 折行 banner | 底栏抽屉 |
| 390×844 | 全屏 form（已有） | 堆叠 CTA 按钮 | 底部 sheet 占 70vh，可拖关闭（提交中禁用） |

---

## 13. 回归面（UI）

1. Connections 列表排序 / 筛选 / 空态 / 无 Workspace。  
2. 旧 authMode 表单路径完全移除后的 create/edit。  
3. Provider 未就绪时 Connection 禁用。  
4. 删除确认、discard dirty。  
5. Chat 会话切换、归档、HITL、取消、Trace 面板。  
6. 导航文案与路由 `/chat` 深链。  
7. Tool test / trial 无透传需求时 UI 不出现 Token 框。  
8. 权限矩阵抽样：VIEWER 只读、EDITOR 不能迁移、OPERATOR 可验证不可改策略。  
9. 确认 Token / attachmentId 不出现在 Vue DevTools 持久 store 与 DOM 回填（手工 + 单测）。

---

## 14. Forge 实施标注

### 14.1 必做（对照本 UI）

| ID | 范围 | 说明 |
| --- | --- | --- |
| F-UI-01 | `navigation.ts` + `ChatExecutionView` h1 / title | 改名「运行调试台」 |
| F-UI-02 | `ChatExecutionView` | 非生产 banner + Subject 条 |
| F-UI-03 | 新 `DebugOutboundCredentialPanel` | attach 流程；message 只带 attachmentId |
| F-UI-04 | `ServiceConnectionsView` 列表 | 策略列、迁移 badge、筛选、banner |
| F-UI-05 | Connection 表单 | `OutboundModePicker` + 策略字段替换旧 authMode UI |
| F-UI-06 | 迁移 mode | 只读旧摘要 → 选策略 → 配置 → impact → 验证 |
| F-UI-07 | impact 确认对话框 | 对接 `:impact` API |
| F-UI-08 | `ProvidersView` | outboundIdentity 契约区 |
| F-UI-09 | `ToolTestDialog` / `WorkflowTrialRunDialog` | 条件透传 Token 区 |
| F-UI-10 | 错误文案表 | 稳定码映射组件 |
| F-UI-11 | 引用侧禁用 | Tool / Agent / Workflow 选择器过滤迁移 Connection |
| F-UI-12 | 单测 | password 不持久、模式枚举、导航文案 |

### 14.2 明确不做

- 不实现第三种 mode UI。  
- 不做 External Subject 模拟器。  
- 不做 Token 历史、show password、导出。  
- 不把 Token 写入 chat message DTO。  
- 不在技术未批准前生成 checklist 任务拆解以外的实现 PR 依赖假设（T1～T5 变更只影响字段子集，不推翻本 IA）。

### 14.3 与 Knower 契约对齐检查点

- DTO：`outboundIdentity.mode`、`migrationState`、`status`、`machineCredentialConfigured`、`policyVersion`  
- Chat：`POST .../outbound-credentials` + message `outboundCredentialAttachmentId`  
- impact proof 字段名与过期行为  
- 错误码表 §10  

若 Canvas 布局要求改动上述契约，**必须**升级技术文档版本，不得只改前端。

---

## 15. Sentinel Chrome 验收路径

前置：测试 Workspace 含（1）已迁移 VERIFIED Broker Connection（2）已迁移 VERIFIED 透传 Connection（3）硬切遗留 `DISABLED+MIGRATION_REQUIRED` Connection（4）OWNER 与 EDITOR 两个账号。

### C1 策略选型

1. OWNER 打开「服务连接」→ 新建。  
2. 见策略双卡片；选 Broker，见机器凭据字段；切换透传，机器凭据消失且出现驻留时间。  
3. 保存并验证成功 → 列表策略列正确。  
4. EDITOR 打开同连接：策略与 Secret 只读。

### C2 迁移呈现

1. 列表见「需迁移」琥珀 badge，非红错误。  
2. 顶部 banner 显示待迁移数量。  
3. 对迁移连接点「测试」类操作 → disabled 或错误文案含迁移语义。  
4. OWNER 走迁移向导选透传 → 验证成功 → badge 消失、可执行。  
5. 确认无「自动转换」提示或一键猜测。

### C3 运行调试台

1. 侧栏与 h1 为「运行调试台」；banner 可见。  
2. Subject 显示当前 USER。  
3. 透传 Agent：打开绑定面板，输入 Token，绑定后输入框清空；发送后网络面板 message **无** Token 字段。  
4. 离开页面再回：无 Token 回填。  
5. 归档会话：绑定与发送不可用。  
6. HITL：确认前无 Broker 明文；失败只见稳定码。

### C4 脱敏与权限

1. 任意失败路径：DOM / 可见 toast 无 Token 子串（用可识别 canary Token 测试）。  
2. VIEWER：只读 Connection；不能打开迁移保存。  
3. 390 宽：迁移 banner、策略卡片、凭据 sheet 可用且无横向裁切关键操作。

### C5 回归

1. 会话、流式、取消、Trace 仍可用。  
2. `/chat` 深链仍打开调试台。  
3. Broker-only Agent 无强制 Token 框。

---

## 16. 本版无需负责人另批的 UI 决策（均落在产品冻结内）

| 项 | 推荐 | 理由 |
| --- | --- | --- |
| 策略控件形态 | 双卡片 radio，而非普通 select | 降低误选；强化「仅两种」 |
| 迁移入口 | 同页 form `migrate` mode | 复用 Connection 壳；减少新路由 |
| 调试台 route | 保留 `/chat` | 兼容深链与书签 |
| 凭据面板 | 底栏抽屉 / 移动 sheet | 不打断会话阅读 |
| 迁移色 | 琥珀，不与 ERROR 红合并 | 满足「双维度」 |

### 16.1 若需升级确认才改的项（当前不阻塞）

| 项 | 说明 |
| --- | --- |
| 侧栏图标是否从对话气泡改为 flask/terminal | 纯视觉，不影响 AC |
| 迁移成功后是否强制跳转 Workflow 问题面板 | 增强引导，非产品必选 |
| expiresAt 控件形态 | 依赖 T3 批准结果 |

---

## 17. 交付清单

| 项 | 值 |
| --- | --- |
| 文档路径 | `docs/design/outbound-user-auth-ui-design.md` |
| 版本 | UI v0.1 |
| 确认状态 | 供 Knower 方案引用；产品契约已冻结；技术 T1～T5 仍待负责人批准（不阻塞本 UI IA） |
| 组件 / 状态矩阵 | §8、§9、§5.5 |
| Forge 标注 | §14 F-UI-01～F-UI-12 |
| Sentinel Chrome 路径 | §15 C1～C5 |
| 覆盖需求点 | Connection 策略选型；`DISABLED+MIGRATION_REQUIRED`；运行调试台改名与独立调试凭据绑定 |

---

## 18. 给 Knower 的引用摘要

1. Connection 创建 **第一步** 固定 `BROKER_OBO` | `REQUEST_PASSTHROUGH`；列表 **策略 / 配置状态 / 迁移态** 三列分离。  
2. `MIGRATION_REQUIRED` 使用琥珀「需迁移」+ 已停用，不进通用 Error；迁移向导显式选策略，验证成功才可执行。  
3. 「运行调试台」改名 + 永久非生产 banner + Subject=当前 USER；透传凭据 **独立面板 attach**，message 只带 attachmentId；一次性 password、不回显、不持久。  
4. 危险变更统一 impact 确认；权限矩阵与产品 §10 一致。  
5. UI 不要求新增 API 模式；仅消费技术文档已列 DTO / attach / impact / 错误码。若实现中发现契约缺口，退回技术修订，不在前端发明字段语义。
