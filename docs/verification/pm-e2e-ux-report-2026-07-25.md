# ACTWEAVE PM E2E 体验走查报告

- 日期：2026-07-25（Asia/Singapore）
- 环境 URL：`http://127.0.0.1:5174`（前端）、`http://127.0.0.1:8082`（后端）
- 分支 / commit：`main` / `b85e2452e5431e9aa2f90910e85ca6bcf373dcb8`
- 浏览器：Google Chrome 150，Playwright 驱动，真实本地前后端与数据库（非接口 Mock）
- 视口 / Locale：1600 × 1100 / `zh-CN`
- 账号：`admin`（PLATFORM_ADMIN）
- 主要 Workspace：`e2e-expense-pt` /「E2E费用报销透传全链路」
- 截图目录：`docs/verification/pm-e2e-ux-2026-07-25/`
- 测试数据变更：
  - 新建 Workflow Draft：`PM E2E 最小编排 1784989338754`
  - 对 `expense-pt-local` 执行了一次真实连接验证；因本地 `127.0.0.1:18080` 服务不可达，状态由「可用」变为「需处理」
  - Smart DAG 发起真实生成会话/turn；失败后页面显示会话仍为 OPEN
  - 运行调试台发送了文本指令；未删除、停用、发布、轮换凭据或修改用户权限

## 走查范围与覆盖矩阵

| 功能域 | 覆盖 | 实际操作 | 结果 |
|---|---|---|---|
| 1. 登录 / 登出 / 会话过期 | 部分 | 错误密码、正确登录、登出 | 登录/登出成功；错误提示带请求 ID。未等待真实 JWT TTL；首次无会话恢复会产生 401 Console 噪声 |
| 2. Workspace 切换与无空间/无权限态 | 部分 | 列表搜索、跨 Workspace 切换、空新建表单禁用态 | 搜索和切换成功；当前仅有管理员账号，未覆盖无空间/无权限账号 |
| 3. Overview | 已测 | 近 7 天、日期输入、KPI、图表、每日明细 | 主路径成功；日期逆序输入会被控件归一化为有效区间 |
| 4. Provider | 部分 | 列表/详情、创建表单必填校验、真实同步失败 | 表单校验正常；同步失败只给诊断码，缺少行动建议 |
| 5. 服务连接 | 部分 | 列表/详情、新建表单、真实验证失败 | 失败诊断稳定，但提示不可行动；状态变化引发 Tool 展示矛盾 |
| 6. 工具管理 | 部分 | KPI、筛选、详情、测试弹窗、已发布 Tool 重测 | 详情和门禁可见；连接异常后 8 个 Tool 标为需处理；已发布版本无法直接重测 |
| 7. OpenAPI 导入 | 部分 | Ready 记录详情、导入表单、Provider/Connection 选择 | 表单主路径可达；详情地址和契约预览存在错误；未重复生成 Tool 草稿 |
| 8. Agent | 已测 | 筛选、Prompt Revision、Capability Binding、编辑表单 | 主要管理入口成功；未创建/删除 Agent |
| 9. Workflow | 阻塞 | 创建 Draft、详情、尝试进入编辑器 | Draft 创建成功；「编辑流程图」无反馈返回列表，画布/编译/trial/publish 被阻塞 |
| 10. Smart DAG | 已测（失败路径） | 选择 Workspace/Agent、输入目标、真实生成 | 约 78 秒后 HTTP 500；保留上一画布，无本地假成功，但恢复指引不足 |
| 11. 运行调试台 | 已测（失败路径） | 发送明确“不调用工具”的文本指令、等待终态 | 被无关连接未就绪阻断；错误消息已出现但状态 150 秒后仍为「执行中」 |
| 12. Agent Access | 已测 | Client 详情、凭证生命周期、接入配置 | 公开 Hint、信任根与不可恢复边界清晰；未轮换/撤销/禁用 |
| 13. 模型 API 配置 | 部分 | 列表、持久化测试动作 | 测试动作可点击；页面未保留明显的本次测试结果，未修改配置 |
| 14. 用户与权限 | 部分 | 列表、搜索管理员、状态/角色/语言时区 | 管理员可访问；未执行创建、停用、重置密码等危险操作 |
| 15. 审计日志 | 已测 | 刷新、打开 Trace 详情、检查脱敏与时间线 | 成功；可看到本次 Console 失败原因和请求文本 |

## 执行摘要

1. 登录、Workspace 切换、Overview、Agent、Agent Access、审计等基础管理路径稳定，空态、禁用态和安全说明整体清晰。
2. Workflow 是本轮最直接的主路径阻塞：成功创建 Draft 后，从详情点击「编辑流程图」只关闭弹窗并回到列表，20 秒内无编辑器、无 Loading、无错误提示，因此无法继续编译、试运行或发布。
3. Console 将 Agent 已绑定但当前不可用的 Tool 当成启动前置条件；即使用户明确要求“不调用任何工具”，文本对话仍被 `OUTBOUND_IDENTITY_CONNECTION_NOT_READY` 阻断。
4. Console 已渲染终态错误消息后，顶部仍保持「执行中 / 意图识别中」至少 150 秒，状态、输入反馈与审计事实不一致。
5. Smart DAG 的失败边界比 Console 更完整（没有本地假成功），但真实生成等待约 78 秒后仅返回 500 + 请求 ID；会话仍显示 OPEN，页面缺少可行动的诊断、重试/关闭建议。
6. OpenAPI Ready 记录详情同时出现重复端口和空契约树，削弱用户对“8 个接口、8 个可生成”的信任。
7. Connection 失败后，Tools 页能把风险汇总为「需处理 8」，但详情使用「连接缺失」描述一个实际存在、只是验证失败的连接，并继续并列展示「已发布 / 测试通过」，语义不自洽。
8. 没有发现 P0 数据破坏或权限越权；本轮共记录 4 个 P1、5 个 P2、1 个 P3。

## 问题清单

| ID | 严重度 | 模块 | 问题 |
|---|---|---|---|
| UX-01 | P1 | Workflow | 「编辑流程图」关闭详情后静默回到列表，编辑器未出现，阻塞 compile → trial → publish |
| UX-02 | P1 | Console / Agent Runtime | 纯文本且明确“不调用工具”的请求仍因已绑定 Tool 的连接未就绪而失败 |
| UX-03 | P1 | Console | 已显示终态错误消息，顶部仍长期保持「执行中 / 意图识别中」 |
| UX-04 | P1 | Smart DAG | 真实生成等待约 78 秒后 500，只给请求 ID；会话仍 OPEN 且缺少恢复动作 |
| UX-05 | P2 | OpenAPI | Ready 导入详情把服务地址渲染为 `http://127.0.0.1:18080:18080` |
| UX-06 | P2 | OpenAPI | 8 个接口 / 8 个可生成，但请求参数、Body、响应结果均显示 0 节点 |
| UX-07 | P2 | Connection / Tool | 连接只是验证失败却被 Tool 标为「连接缺失」，同时并列「已发布 / 测试通过」 |
| UX-08 | P2 | Connection / Provider | 失败 Toast 只显示稳定诊断码，缺少失败目标、可能原因和下一步入口 |
| UX-09 | P2 | Tool | 已发布 Tool 的测试弹窗不可执行，要求先创建 Draft，连接退化时缺少只读重测/诊断路径 |
| UX-10 | P3 | 登录 | 未登录首次进入会触发 `/auth/refresh` 401 并写浏览器 Console error，虽不影响用户登录 |

## 分模块详细发现

### UX-01 · Workflow「编辑流程图」静默失败

- 严重度：P1
- 模块 / 页面：编排 `/workflow`
- 前置：已成功创建 Draft `PM E2E 最小编排 1784989338754`
- 复现：
  1. 进入「编排」，搜索该 Draft。
  2. 点击该行，打开「流程详情」。
  3. 点击底部主按钮「编辑流程图」。
  4. 等待 20 秒。
- 期望：显示编辑器 Loading，成功后进入画布；加载失败时保留上下文并显示可重试错误。
- 实际：详情弹窗关闭，页面回到列表；编辑器不出现，也没有 Toast、错误码或重试入口。由于入口被阻塞，无法从真实 UI 继续 Compilation、trial 与 publish。
- 截图：
  - 详情与主按钮：[76-workflow-detail.png](pm-e2e-ux-2026-07-25/76-workflow-detail.png)
  - 点击后静默回到列表：[final-error-02.png](pm-e2e-ux-2026-07-25/final-error-02.png)
- 建议：
  - 将 `loadEditorDraft` 的 `loading / failed / stale` 三态显式呈现。
  - 详情在编辑器确认挂载前不要关闭；失败时保留弹窗、显示请求 ID 和「重试加载」。
  - 增加 E2E：创建 Draft → 详情 → 编辑画布 → compile → trial → publish。

### UX-02 · 纯文本 Console 被无关 Tool 连接阻断

- 严重度：P1
- 模块 / 页面：运行调试台 `/chat`
- 复现：
  1. Workspace 选择「E2E费用报销透传全链路」，Agent 选择「费用助手」。
  2. 不绑定出站凭据。
  3. 输入：`只回复“PM E2E 调试台已连通”，不要调用任何工具。`
  4. 点击「发送」。
- 期望：Agent 完成纯文本回复；只有模型实际决定调用需出站身份的 Tool 时，才检查该 Tool 的 Connection。
- 实际：运行在回复前失败：`resolve capability "createexpense": OUTBOUND_IDENTITY_CONNECTION_NOT_READY`。用户没有要求调用 `createexpense`，也没有进入工具选择阶段。
- 截图：[async-error-05.png](pm-e2e-ux-2026-07-25/async-error-05.png)
- 审计证据：[91-audit-trace-detail.png](pm-e2e-ux-2026-07-25/91-audit-trace-detail.png)
- 建议：
  - Capability 目录加载与可调用性分离；不可用 Tool 应作为带原因的 unavailable capability，而不是阻断 Agent 初始化。
  - 仅在实际 Tool invocation 前检查对应 Connection 和 outbound credential。
  - 若产品策略要求所有绑定必须就绪，应在「发送」前禁用并列出全部阻塞项，而不是运行后失败。

### UX-03 · Console 终态消息与顶部状态不一致

- 严重度：P1
- 模块 / 页面：运行调试台 `/chat`
- 复现：
  1. 按 UX-02 发起请求。
  2. 等待错误气泡出现。
  3. 继续观察顶部状态 150 秒。
- 期望：错误气泡出现时，run 状态同步为「运行失败」，意图阶段结束，输入恢复为可明确重试状态。
- 实际：错误气泡已显示两次，但顶部仍为「执行中」，右侧意图仍为「识别中」；150 秒后仍未进入终态。
- 截图：[async-error-05.png](pm-e2e-ux-2026-07-25/async-error-05.png)
- 建议：
  - 将 SSE / polling 的 terminal error 与 message persistence 合并为同一个状态提交。
  - 对终态事件丢失增加超时回源；看到 assistant error message 时不应继续保持 RUNNING。
  - 提供「运行失败 · 重试」而不是让用户判断是否仍在执行。

### UX-04 · Smart DAG 长等待后 500，恢复路径不足

- 严重度：P1
- 模块 / 页面：智能编排 `/smart-dag`
- 复现：
  1. 选择 Workspace「E2E费用报销透传全链路」。
  2. 选择 Agent「费用助手」。
  3. 输入「生成一个从开始到结束的最小报销登记流程，不调用工具。」
  4. 点击「开始多轮生成」，等待终态。
- 期望：在可理解的进度/超时预算内生成正式 Workflow Draft；失败时说明失败阶段、是否可重试、当前会话是否可继续。
- 实际：
  - Loading 持续约 78 秒。
  - `POST .../turns` 返回 HTTP 500。
  - 页面仅提示「智能生成失败，未创建本地替代草稿」和请求 ID。
  - 当前草稿仍为占位图，会话显示 OPEN；没有就地「重试本轮 / 关闭会话 / 查看诊断」动作。
- 截图：
  - Loading：[95-smart-dag-generating.png](pm-e2e-ux-2026-07-25/95-smart-dag-generating.png)
  - 失败终态：[96-smart-dag-terminal.png](pm-e2e-ux-2026-07-25/96-smart-dag-terminal.png)
- 建议：
  - 展示服务端阶段：模型调用、JSON 解析、Guard、Draft 持久化。
  - 明确超时预算和可取消能力；失败时保留输入并提供「重试本轮」。
  - 若会话仍 OPEN，明确说明可继续；若不可继续，主动提供关闭/新建会话。

### UX-05 · OpenAPI 详情重复拼接端口

- 严重度：P2
- 模块 / 页面：OpenAPI 导入 `/openapi-imports`
- 复现：
  1. 搜索 `openapi.yaml`。
  2. 打开任一 Ready 导入记录详情。
  3. 查看「服务地址」。
- 期望：`http://127.0.0.1:18080`
- 实际：`http://127.0.0.1:18080:18080`
- 截图：[39-openapi-detail-contract.png](pm-e2e-ux-2026-07-25/39-openapi-detail-contract.png)
- 建议：服务地址统一使用规范化 URL；不要对已经包含端口的 `serviceBaseUrl` 再拼 `port` 字段。

### UX-06 · Ready 导入记录的结构化契约为空

- 严重度：P2
- 模块 / 页面：OpenAPI 导入详情
- 复现：
  1. 打开状态 Ready、接口数量 8、可生成 8 的记录。
  2. 查看请求参数、Body、响应结果及接口明细。
- 期望：至少显示 8 个接口明细；有契约的接口显示对应 schema，无契约的接口逐项标明原因。
- 实际：请求参数、Body、响应结果均显示 `0 个节点 · 0 个必填 · 0 层结构`，没有接口明细，页面同时宣称 8 个可生成。
- 截图：[39-openapi-detail-contract.png](pm-e2e-ux-2026-07-25/39-openapi-detail-contract.png)
- 建议：将导入 summary 与 detail DTO 的契约完整性加入一致性校验；详情缺失时显示「详情数据未保存/加载失败」，不要呈现为合法空契约。

### UX-07 · Connection / Tool 状态语义矛盾

- 严重度：P2
- 模块 / 页面：服务连接 `/connections`、工具管理 `/tools`
- 复现：
  1. 对 `expense-pt-local` 执行验证；本地服务不可达，连接变为「需处理」。
  2. 打开 Tool「获取当前登录业务用户」详情。
- 期望：Tool 显示「连接异常 / CONNECTION_NETWORK_ERROR」，并明确已发布 Release 是否仍可执行。
- 实际：
  - Connection 实体仍存在，但 Tool 显示「连接缺失」。
  - 同一详情并列显示「已发布」「测试通过」「连接缺失」。
  - Tools KPI 从「需处理 0」变为「需处理 8」，但用户难以判断 Release 是否已自动下线。
- 截图：
  - Connection 状态：[35-connection-real-verify.png](pm-e2e-ux-2026-07-25/35-connection-real-verify.png)
  - Tool 详情：[36-tool-real-detail.png](pm-e2e-ux-2026-07-25/36-tool-real-detail.png)
- 建议：区分 `MISSING / DISABLED / NEEDS_ATTENTION / MIGRATION_REQUIRED`；在 Tool 中展示解析到的 Connection 名称、当前状态和运行影响。

### UX-08 · Provider / Connection 失败提示不可行动

- 严重度：P2
- 模块：Provider 同步、Connection 验证
- 复现：
  1. 对 `Mock Corp Expense` 执行同步。
  2. 对 `expense-pt-local` 执行验证。
- 期望：提示失败目标、探测 URL、诊断类别、可编辑字段和「编辑后重试」入口。
- 实际：Toast 分别只给 `PROVIDER_DISCOVERY_FAILED`、`CONNECTION_NETWORK_ERROR`；用户必须自行判断是文档 URL、服务端点、CIDR、代理还是凭据问题。
- 截图：
  - Provider：[10-provider-sync-result.png](pm-e2e-ux-2026-07-25/10-provider-sync-result.png)
  - Connection：[35-connection-real-verify.png](pm-e2e-ux-2026-07-25/35-connection-real-verify.png)
- 建议：稳定码保留给支持人员，同时增加用户语言和一键编辑对应配置区。

### UX-09 · 已发布 Tool 缺少只读重测/诊断路径

- 严重度：P2
- 模块 / 页面：工具管理
- 复现：
  1. 打开已发布 Tool 的「测试工具」。
  2. 观察执行按钮。
- 期望：不修改 Release 的前提下，可对当前 Active Release 执行只读诊断；若必须创建 Draft，应提供一键「从当前版本创建 Draft 并测试」。
- 实际：弹窗提示「当前只有已发布版本，不能直接重测。请先编辑 Tool 创建新的 Draft Version」，执行按钮不可用。连接刚退化时，用户不能快速确认影响。
- 截图：[37-tool-test-dialog.png](pm-e2e-ux-2026-07-25/37-tool-test-dialog.png)
- 建议：拆分「版本验收测试」与「线上连接诊断」；后者不得改变版本状态。

### UX-10 · 未登录恢复请求产生 Console 噪声

- 严重度：P3
- 模块：登录
- 复现：新浏览器上下文直接进入 `/login`。
- 期望：无 Refresh Session 是正常匿名状态，不产生前端 Console error。
- 实际：`POST /api/v1/auth/refresh` 返回 401，Chrome Console 记录 `Failed to load resource`；用户界面不受影响。
- 截图：[01-login-invalid-credential.png](pm-e2e-ux-2026-07-25/01-login-invalid-credential.png)（界面正常；Console 证据见执行记录）
- 建议：登录页不主动恢复，或用不会触发资源错误噪声的 session-probe 语义处理匿名态。

## 体验亮点

- 登录错误文案带请求 ID，便于支持与审计定位。
- Overview 的 KPI、趋势、每日明细和风险摘要在同页形成闭环；快捷日期筛选响应清晰。
- Provider → Connection → OpenAPI → Tool 的术语边界总体明确，表单对 Secret 不回显的说明充分。
- Agent Capability Binding 明确展示 FOLLOW/PIN、Connection 选择和启用状态；Prompt Revision 明文不回传的安全边界清楚。
- Agent Access 对公开 Hint、不可恢复凭据、Issuer/JWKS/CORS 的说明具体，危险操作没有误触。
- 审计 Trace 可把用户输入、最终输出、耗时和脱敏主体串成时间线，成功支撑本次问题定位。

## 优先改进建议（Top 5）

1. **恢复 Workflow 主路径**：修复详情 → 编辑器加载，并为 Draft load 的 Loading/Error/Stale 建立可恢复 UI；加真实闭环 E2E。
2. **解耦 Agent 启动与 Tool 可用性**：纯文本任务不应被未调用 Capability 的连接状态阻断；不可用 Tool 应降级为目录级提示。
3. **统一 Console 终态**：终态消息、run 状态、意图状态和输入可用性必须原子更新；增加超时回源。
4. **产品化 Smart DAG 失败恢复**：阶段化进度、超时预算、取消、重试本轮、关闭会话和 Guard/模型错误摘要。
5. **修复 OpenAPI / Connection / Tool 的状态与契约一致性**：规范 URL、补齐 detail schema、一致展示 Connection 状态和 Release 运行影响。

## 附录

### 阻塞项

- Workflow 编辑器主入口静默失败，导致真实画布、Compilation、trial、publish 未能继续。
- `expense-pt-local` 所依赖的本地业务服务 `127.0.0.1:18080` 不可达，Connection 真实验证为 `CONNECTION_NETWORK_ERROR`。
- 因连接未就绪，Console 的文本请求被 `OUTBOUND_IDENTITY_CONNECTION_NOT_READY` 阻断。
- Smart DAG 生成 turn 返回 HTTP 500；请求 ID：`019f99af-8ebe-7c4c-b3a5-2d2cd429bcc8`。

### 未覆盖原因

- 会话过期：未等待真实 JWT TTL；没有修改系统时钟或伪造服务端 Token。
- 无 Workspace / 无权限：当前只有 PLATFORM_ADMIN 可用，未创建低权限账号以避免扩展数据和权限副作用。
- Provider/Connection/Tool 删除、Agent Access 轮换/撤销、用户停用/密码重置：均为危险或高副作用操作，本轮只验证入口和禁用/确认边界。
- OpenAPI 重新上传与生成 Tool：当前已有 3 条相同 Ready 记录和 8 个 Tool，为避免重复数据未再次生成。
- Workflow trial / publish：被 UX-01 主路径阻塞，未绕过 UI 直接调用 API。

### Given / When / Then 回归验收建议

1. **Workflow 编辑器**
   - Given 已创建且可读取的 Workflow Draft
   - When 用户从流程详情点击「编辑流程图」
   - Then 先显示 Loading，最终进入编辑器；失败时详情保持打开并提供请求 ID 与重试

2. **Console 纯文本**
   - Given Agent 绑定了一个 Connection 不可用的 Tool
   - When 用户发送明确无需工具的纯文本任务
   - Then Agent 可完成模型回复；只有实际选择该 Tool 时才进入 Connection/凭据门禁

3. **Console 终态**
   - Given Agent Run 返回 terminal failure
   - When 错误消息已持久化并渲染
   - Then 顶部状态、意图状态和运行详情均在一个刷新周期内显示失败，输入恢复可重试

4. **Smart DAG 失败恢复**
   - Given 生成 turn 在模型、解析、Guard 或持久化任一步骤失败
   - When 后端返回错误
   - Then 页面展示失败阶段、请求 ID、会话是否仍可用，并提供重试本轮或关闭会话

5. **OpenAPI 详情一致性**
   - Given 导入摘要为 8 个接口、8 个可生成
   - When 用户打开详情
   - Then URL 只含一个端口，接口明细数量为 8，契约缺失需逐接口解释而不是全局显示合法空树

