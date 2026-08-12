# A2UI Catalog Refactor — Implementation Checklist

| 字段 | 值 |
|------|-----|
| **Rev** | 2（按「上线前修订」前提重写；Rev 1 的 PR-A4 规范化与分批上线已作废） |
| **Branch** | 在 `feat/a2ui-additive-capability` 上继续（见设计 §1.4） |
| **Worktree** | `/Users/chen/Documents/act-weave-chat` |
| **Base** | `feat/a2ui-additive-capability` @ `b0e9fc7`（未 push） |
| **Design** | [a2ui-catalog-refactor.md](./a2ui-catalog-refactor.md) (Rev 2) |
| **Status** | Phase 1 实施中（PR-3…PR-8 已落地，见下） |
| **Created** | 2026-08-12 |
| **Revised** | 2026-08-12 |

## Legend

`[ ]` 未做 · `[~]` 进行中 · `[x]` 完成 · `[-]` 本阶段不做

## 实施进度

| PR | 状态 | 落地位置 | 验证 |
|----|------|----------|------|
| PR-3 catalog schema | 完成 | `backend/internal/a2ui/catalogs/standard/v1/` | `catalog_test.go` 契约守卫 10 项 |
| PR-4 外部文档校验入口 | 完成 | `backend/internal/protocolschema/external.go` | `external_test.go`；pattern 编译缓存把大 surface 校验从 5.6 ms / 9.7 MB 降到 0.39 ms / 98 KB |
| PR-5 ValidateSurface | 完成 | `validate.go` `graph.go` `chart_semantics.go` `diagnostic.go` | `validate_test.go`、`validate_bench_test.go` |
| PR-6 写路径接入 | 完成 | `prepare.go` `materialize.go` `chatruntimebridge/a2ui_terminal.go` | `split_test.go`、`a2ui_terminal_test.go`、`metrics/a2ui_test.go` |
| PR-7 prompt 由 catalog 生成 | 完成 | `prompt.go` + `testdata/prompt_appendix_v2.golden.md` | golden + token 预算 + 「只提 catalog 成员」+「禁止平台字段」四道守卫 |
| PR-8 共享契约 + fixture | 完成 | `a2uifixtures/surfaces/`（11 个）→ `cmd/a2uigen` → 两端 `a2ui/generated/` | `a2uifixtures/fixtures_test.go`；`make a2ui-check` 漂移闸门（已实测会拦） |
| PR-9 demo 渲染器 | 完成 | `demos/aap-chat/client/src/a2ui/`（registry + resolve + 11 渲染器 + SVG） | `a2ui.test.ts` / `resolve.test.ts` 共 56 例（含 6 图快照）；CI `demo-aap-chat` job |
| PR-10 Console 渲染器 | 完成 | `frontend/src/components/a2ui/`（11 个 SFC + 几何 + i18n）、`chat.A2UISurfacesFromDurable` → `messageDTO.a2ui` | 前端 83 例（含 6 图快照）；后端 DTO/投影单测；`npm test` 570 例全绿 |
| PR-11 真实 e2e | 完成 | 报告 `a2ui-catalog-refactor-e2e-report-2026-08-12.md`；prompt 补一条围栏体规则 | 本机 Chrome + 真实模型 15 次自然对话：6 种 chartType 全命中、`catalog_invalid` 0 次、2 次模型 JSON 括号错，补规则后 4/4 通过 |

**PR-8 的单一来源机制（实施决策）**：fixture 与 catalog 事实以 Go 为源，
`backend/cmd/a2uigen` 生成两端各自的 `generated/{catalog,fixtures}.gen.ts`，
`make a2ui-check` + CI 用「重新生成后 git diff 必须为空」保证两份拷贝不漂移。
沿用仓库既有的 `protocolgen → sdk/typescript/src/generated` 约定，
两端因此都不需要跨项目相对导入，也不需要新增依赖。
生成物只含**事实与类型**（组件名、枚举、上限、结构类型、`A2UIRegistry<R>` 分派契约）；
绑定解析等**行为**仍由各端自己实现，并各自用同一组 fixture 做单测——
这正是「契约经双栈验证」的含义。

---

## 0. 开工前置

- [ ] 评审并锁定设计 §2：Q-C1 Q-C2 Q-C3 Q-C4 Q-C5 Q-C7 Q-C8 Q-C10 Q-C11 Q-C12
      （Q-C6 / Q-C9 已作废，无需评审）
- [ ] 校准容量上限（设计 §3.5）：8 series × 64 points、`label` 48 字符、
      组件数 64、树深 16 —— 均为拍定值，按真实统计场景确认
- [ ] 确认 `stacked` 归类为语义而非视觉（设计 §3.5）
- [ ] 确认分支策略：中间态不进 `main`，`main` 只收到一个连贯的 A2UI 特性
- [ ] 清理昨天 e2e 落的 `a2ui-surface.v0` demo 消息（v0 无 legacy 支持）
- [ ] 确认不影响 `/Users/chen/Documents/act-weave`（`feat/progressive-tool-disclosure`）

---

## Phase 1 — 核心交付（单次合并）

### PR-1: 设计文档落盘（docs-only）

- [ ] `docs/designs/a2ui-catalog-refactor.md` (Rev 2)
- [ ] `docs/designs/a2ui-catalog-refactor-checklist.md` (Rev 2)
- [ ] 与前序设计交叉链接（已在旧 checklist「Optional later」处加指针）

### PR-2: 官方渲染器 spike（Q-C12，时间盒 ≤ 半天）

**产出一页结论，不写生产代码。** 与 PR-3 可并行。

- [ ] `@a2ui/lit` 能否在 vanilla TS 与 Vue 中同时工作（web components 互操作）
- [ ] Q-C11 形态的 surface 能否零适配喂入
- [ ] 自定义 `Chart` 组件注册进 Lit catalog 的成本
- [ ] display-only（无 action 通道）下官方渲染器是否报错或行为异常
- [ ] 包体增量测量
- [ ] Console 现有设计系统的贴合成本
- [ ] 结论写入 `docs/designs/a2ui-official-renderer-spike-<date>.md`
- [ ] **决策规则**：仅当互操作 + 零适配 + display-only 三项通过且包体/样式成本
      可接受时才考虑采纳；否则按设计 §7 自研。**catalog 设计不因结论改变**

### PR-3: Catalog schema（Q-C1/Q-C2/Q-C5/Q-C8/Q-C11）

- [ ] 新建 `backend/internal/a2ui/catalogs/standard/v1/catalog.json`
- [ ] `$id` == `catalogId` == `https://catalog.actweave.dev/standard/v1`
- [ ] `protocolVersion: "1.0"`；`instructions` 写 LLM 设计指引（何时用图表/表单）
- [ ] `$defs`：`ComponentId` / `ChildList` / `DataBinding` / `DynamicString` /
      `ChartSeries` / `ChartPoint`
- [ ] 11 个组件：`Column` `Row` `Card` `Text` `Divider` `Chart` `TextField`
      `CheckBox` `ChoicePicker` `DateTimeInput` `Button`
- [ ] 每个组件 `component` 为 `const` 且等于其 map key（判别器规则）
- [ ] 每个组件 `additionalProperties: false`
- [ ] `Button` **不定义** `action` 属性（Q-C8）
- [ ] `Chart` 排除颜色/尺寸/坐标轴/别名字段（设计 §3.5 表）
- [ ] surface 顶层 schema：`{surfaceId, catalogId, components, dataModel?}`，
      `additionalProperties: false`，禁止 `sendDataModel`（Q-C11）
- [ ] `backend/internal/a2ui/catalog.go`：`//go:embed` + `CatalogID` 常量 +
      `CatalogDocument()` + `RegisteredCatalogIDs()`
- [ ] 常量 `EnvelopeVersionV1 = "a2ui-surface.v1"`（**删除** v0 常量，无 legacy）
- [ ] **单测** `TestCatalogUsesSupportedKeywords`：递归收集 catalog 内出现的全部
      JSON Schema 关键字，断言均在 `protocolschema` 子集白名单内（防静默漏校验）
- [ ] **单测** `TestCatalogDiscriminatorRule`：每个组件的 `component.const` == key
- [ ] **单测** `TestCatalogIDMatchesSchemaID`

**验收：** catalog 可被校验器加载，且不含静默失效的关键字。

### PR-4: 校验器复用入口（Q-C3）

依赖：PR-3

- [ ] `protocolschema` 新增导出入口，可对**外部文档**做子集校验。现有
      `validationRegistry` 是 `map[$id]document` + `sync.OnceValue`，外部文档需要
      独立 registry 实例，建议导出可复用的编译产物而非每次解析：
      `CompileExternal(document []byte) (*ExternalSchema, error)` +
      `(*ExternalSchema).Validate(fragment string, raw json.RawMessage) error`
- [ ] **catalog 编译结果缓存**（`sync.OnceValue` 或包级 var）——每条 assistant
      消息都要校验，不可重复解析 catalog JSON
- [ ] 不改动现有 `ValidateEventData` / `ValidateDocument` 行为
- [ ] 确认依赖方向 `a2ui → protocolschema` 单向无环
      （`protocolschema` 不 import 任何内部包，已核实）
- [ ] **单测**：外部文档 + `$ref`(`#/$defs/...`)/`oneOf`/`enum`/`const` 组合正确通过/拒绝
- [ ] **单测**：现有协议校验回归绿
- [ ] **基准**：单次 surface 校验耗时可接受（catalog 已编译缓存）

**验收：** 无新依赖即可校验 catalog。

### PR-5: Surface 校验 + 结构化诊断

依赖：PR-3, PR-4

- [ ] `backend/internal/a2ui/validate.go`：
      `ValidateSurface(catalogID string, surface json.RawMessage) (*Diagnostic, error)`
- [ ] `Diagnostic` 结构：`Reason` / `Pointer` / `Keyword` / `Component` / `Expected`（设计 §5.1）
- [ ] **硬约束**：诊断只含 JSON Pointer、关键字名、组件类型名、catalog 侧期望值。
      **禁止**输出 payload 取值 / 字符串内容 / 整段 surface（设计 §5.2）
- [ ] `validateComponentGraph`（设计 §3.6）
  - [ ] 恰好一个 `id == "root"`
  - [ ] `id` 唯一
  - [ ] `children` / `child` 引用全部可解析
  - [ ] 无环
  - [ ] 深度 ≤ 16
  - [ ] 组件数 ≤ 64
  - [ ] 无孤立节点（不可从 root 到达 → invalid，不做剔除后继续）
- [ ] `validateChartSemantics`（设计 §3.5 表）
  - [ ] `pie`/`donut` 恰好 1 series
  - [ ] `pie`/`donut` 所有 value ≥ 0
  - [ ] `stacked` 仅 `bar`/`hbar`/`area`
  - [ ] 多 series 各 `points` 长度一致
  - [ ] 多 series 各 series 必须有 `name`
  - [ ] `series` 为 `{path}` 时按 `dataModel` 解析并校验解析结果
- [ ] 模型自写 `catalogId` 且与平台值不符 → invalid（设计 §4.3）
- [ ] **单测**：6 种 `chartType` 各一个合法样例
- [ ] **单测**：每条图表语义规则各一个反例，断言 `Diagnostic.Pointer` 指向正确位置
- [ ] **单测**：每条图结构规则各一个反例（含环、缺失引用、深度超限、孤立节点）
- [ ] **单测**：`{path}` 绑定解析成功 / 路径不存在 / 解析出非法结构
- [ ] **单测**：诊断输出**不含** payload 取值（构造含特征字符串的非法 surface，
      断言诊断与日志中不出现该字符串）
- [ ] **Golden**：`TextField` 的 `label`/`placeholder` 含 `password` / `accessToken` /
      `apiKey` / `bearerToken` → catalog 校验**通过**（KD-11 不回归）
- [ ] **明确不实现**：任何形状改写 / 别名映射 / 规范化（设计 §5）

**验收：** 合法 surface 通过；非法 surface 给出可直接指导 prompt 迭代的诊断。

### PR-6: 写路径接入 + 物化 + 降级 + 指标

依赖：PR-5

- [ ] `PrepareAssistantContent` 插入 ValidateSurface → 物化 → 大小检查
- [ ] 物化：注入 `surfaceId = runId + ":" + itemId` 与平台 `catalogId`（Q-C11）
- [ ] 大小检查在物化**之后**（`MaxSurfaceBytes`）
- [ ] 新增 `EmitCatalogInvalid EmitResult = "catalog_invalid"`
- [ ] 校验失败 → 复用既有 text-only 降级（`run` 仍 SUCCEEDED，KD-16 非空保证不变）
- [ ] `SerializeAssistantDurable` 写 `version: a2ui-surface.v1` + `catalogId`
- [ ] `metrics/a2ui.go` 新增：`a2ui_catalog_invalid{reason,keyword,component}`、
      `a2ui_chart_emitted{chartType}`、`a2ui_prompt_tokens`
- [ ] 采样结构化诊断日志（受 §5.2 约束）
- [ ] `AAP_A2UI_PROJECTION=off` 紧急开关行为不变
- [ ] **单测**：非法 surface → text-only 且 run 成功
- [ ] **单测**：物化后落库字节含注入的 `surfaceId`/`catalogId`
- [ ] **单测**：`surfaceId` 确定性（同 run+item 得同值）
- [ ] **单测**：物化后超 `MaxSurfaceBytes` → `too_large` 降级
- [ ] **单测**：`enableA2UI=false` 零行为变化
- [ ] **单测**：预检与 `marshalProjectionItem` 仍同源（不得绕过 `ValidateProjectionItem`）

**验收：** 严格化不引入新的失败模式。

### PR-7: Prompt 由 catalog 生成

依赖：PR-3。可与 PR-4…PR-6 并行。

- [ ] 生成器 `backend/cmd/a2uipromptgen`（或并入现有 `make generate` 流程）
- [ ] 输出 `backend/internal/a2ui/prompt_appendix_v2.gen.go`
- [ ] 内容：11 组件清单 + `Chart` 数据形状 + 枚举值 + 2 个 few-shot + 硬规则
- [ ] **不含** `surfaceId` / `catalogId`（服务端注入，模型不该写）
- [ ] 并入 catalog 的 `instructions` 字段
- [ ] 常量 `PromptTemplateV2 = "a2ui-prompt.v2"`
- [ ] `AppendPromptRules` 切到 v2；保留幂等与 KD-17（`drive()` 仅注入一次，resume 不重注）
- [ ] Token 预算 ≤ 900，生成器超预算即 build fail
- [ ] **单测** `TestPromptAppendixMatchesCatalog`：重新生成并与 `.gen.go` 比对
- [ ] **单测**：`AppendPromptRules` 幂等（含已注入字符串二次调用为 no-op）
- [ ] **单测**：prompt 中每个组件名与枚举值都在 catalog 内存在
- [ ] **单测**：prompt **不含** `surfaceId` / `sendDataModel`
- [ ] `make generate` 纳入 CI

**验收：** prompt 与 catalog 不可能漂移。

### PR-8: 共享渲染契约 + fixture

依赖：PR-3。可与 PR-4…PR-7 并行。

- [ ] 共享类型定义（`RenderCtx` / `ComponentRenderer<R>` / `Registry<R>`，设计 §7.1）
- [ ] 共享 fixture：9 个基线 surface（bar / hbar / line / area / pie / donut /
      多 series / stacked / `{path}` 绑定）+ 1 个表单 + 1 个未知组件
- [ ] fixture 供 demo 与 Console **两端**复用（单一来源，避免各写一套）
- [ ] fixture 与后端校验共享：每个 fixture 必须能通过 PR-5 的 `ValidateSurface`
      （单测断言，防止前端基线用了非法 surface）

**验收：** 两端有同一组事实基线。

### PR-9: demo 渲染器重构（vanilla TS）

依赖：PR-8

- [x] 新目录 `demos/aap-chat/client/src/a2ui/`（设计 §7.2 结构）
- [x] `registry.ts`：精确名称查表，无 `toLowerCase()`、无别名
- [x] `resolve.ts`：`DynamicString` / `DataBinding`（JSON Pointer）解析
- [x] `chart.ts` + `chart-svg.ts`：保留 6 种绘图函数与 legend / formatNum
- [x] `components/`：11 个组件各一个渲染器
- [x] 未注册组件 → 占位符 + 开发态 warn
- [x] 深度 > 16 / 引用缺失 → 占位符（防御性），另加 `children` 环引用检测
- [x] `Text` 纯转义、不解析 markdown（Q-C7）
- [x] `Button` 恒 disabled（`actions: false`）
- [x] **删除** 设计 §7.2 清单的全部条目（旧文件 `a2ui-render.ts` / `a2ui-chart.ts` 整体删除）
  - [x] `renderSurface` 5 形状试探 + `renderGenericObject`
  - [x] `isFormSurface` / `renderFormSurface` / `looksLikeFieldMap` / `renderFieldMap`
  - [x] `renderByKind` 名称别名分支
  - [x] `resolveTextValue` 5 路兜底
  - [x] `flattenChildRefs` 的 `explicitList` / `list`
  - [x] `mapInputType` 18 项别名表（改 catalog `variant` 枚举直映）
  - [x] `isChartSurface` / `isChartKindName` / `resolveChartKind`
  - [x] `normalizeSeries` / `pointsFromObjects` / `zipPoints` 多形状分支
- [x] **删除** v0 legacy 分支（不写）
- [x] `mock-stream.ts` 全部样例改 catalog v1 形态，接入 PR-8 fixture
- [x] XSS：所有字符串值继续走 `escapeHtml`
- [x] 单测：注册表分派、未知组件占位、`{path}` 解析、6 种图表快照
  - vitest 3.2.4 落在 demo 自身（`npm test`），56 例；对齐/枚举类名只取 catalog 枚举值
  - `styles.css` 同步删除已无渲染器引用的旧类（`a2ui-form` / `a2ui-kv*` /
    `a2ui-unknown*` / `a2ui-radio*` / `a2ui-image` / `a2ui-spacer` / `a2ui-fallback` 等）
- [x] CI：新增 `demo-aap-chat` job（`npm ci` + test + type-check + build）

**验收：** 零嗅探；6 种图表可渲染；渲染逻辑 1161 → 932 行（-20%），
并新增 ~350 行以共享 fixture 为基线的单测（原先为 0）。

### PR-10: Console 渲染器（Vue）+ 受控数据通道（Q-C10）

依赖：PR-8。数据通道部分依赖 PR-6。

- [x] **决策：移植 SVG（零依赖）**。`chart-geometry.ts` 只算坐标与 path，SVG 元素写在
      Vue 模板里（无 `v-html`），bundle 不增（`bundle:check` 通过），
      交互需求出现后再评估换库
- [x] `messageDTO.a2ui` 受控通道：**仅** surface，不含 raw envelope
      （`chat.A2UISurfacesFromDurable`，`JoinTextPartsFromDurable` 的读取对偶）
- [x] 字段仅在 durable content 为 `aap.message-content.v1` 且含 a2ui part 时出现；
      并且只取 `version == a2ui-surface.v1` 的 part（旧版本 surface 对 Console 不可见）
- [x] **KD-13 不回归**：`content` 仍是 join 后 text；
      `contentSha256` / `contentLength` 仍只对投影后 text 重算（单测断言 body 中无 surface）
- [x] Console 渲染器复用 PR-8 共享契约与 fixture
- [x] 11 个组件的 Vue 实现 + 未注册组件占位
  - 递归与守卫集中在 `A2UINode.vue`（未知组件 / 悬挂引用 / 环引用 / 超深度 / 空容器），
    子组件成员由新生成事实 `A2UI_CHILD_MEMBERS` 提供，新容器组件无需改渲染器
- [x] 图表 6 种类型渲染（含 stacked、多 series、`{path}` 绑定、上限截断）
- [x] i18n（zh-CN / en）：新增 `a2ui` 命名空间，占位符文案与 display-only 提示；
      英文占位符有单测覆盖
- [x] session reload 后图表可重现（GET 携带 `a2ui`），
      并额外让 `item.completed` 也携带 surface，使图表在本轮对话结束时即出现
- [x] 前端单测：注册表全覆盖、异名不分派、`{path}` 解析、6 种图表快照、11 个 fixture 基线
      （`a2ui.test.ts` / `resolve.test.ts` / `chart-geometry.test.ts` 共 83 例）
- [x] 后端单测：`messageDTO.a2ui` 出现/不出现（4 种不出现的情形）；
      `chat.A2UISurfacesFromDurable` 单测；hash 与返回 body 一致

**验收：** 图表成为 Console 产品能力；契约经双栈验证。

### PR-11: 真实 e2e + 报告

依赖：PR-6, PR-7, PR-9, PR-10

- [x] 真实浏览器（本机 Chrome 151，Playwright `channel: "chrome"`，有头）+ 本地 FE/BE
      + 现有 Docker 数据面。Chrome MCP 不可用，改用 Playwright 驱动同一台 Chrome
- [x] **自然对话**方法论：只给业务数据与阅读诉求，不给围栏或 JSON 片段
- [x] 6 种 `chartType` 各至少成功一次（`bar` `hbar` `line` `area` `pie` `donut`
      全部模型自选），外加 `stacked` 与多 series
- [x] 同一 surface 两端渲染一致：库里 13 个 surface 逐个喂 demo 渲染器，
      chartType 多重集一致、placeholder 均为 0、堆叠图几何逐像素一致
- [x] 表单场景不回归（两轮问卷各 6 字段 + 禁用提交按钮）
- [x] `enableA2UI=false` 的 Agent 零变化：无围栏、durable 为纯文本、
      仅 `emit_total{result="none"}` +1；改回 true 后立刻恢复
- [x] `a2ui_catalog_invalid{reason,keyword,component}` 实际命中分布：**0 次**
      （15 次真实 emit 无未知组件 / 多余属性 / 图表语义冲突）
- [x] 唯一失败模式是模型自己的 JSON 括号错（`invalid_json` 2/11）。
      只在 `prompt.go` 补一条「围栏体须为完整 JSON 对象」规则并刷 golden，
      未放宽任何校验；补规则后原本稳定失败的堆叠场景 4/4 通过
- [x] **no-delete**：只新建会话、发消息、改 Agent 标记与模型名
- [x] 报告落盘 `docs/designs/a2ui-catalog-refactor-e2e-report-2026-08-12.md`

**环境偏差（报告有详细记录）：** 共享开发库已被隔壁 worktree 迁移到 schema 19，
本分支只到 18，因此在 `actweave_a2ui_e2e` 副本库上（回滚 19 的 down 到 18）运行，
共享库全程未动；模型网关账号组轮换，按指示把模型名改到 `gpt-5.2`（仅副本库）。

**验收：** 严格路线在真实模型下可用，且有数据支撑。

---

## Phase 2 — 第三方接入

- [x] Profile `a2ui` 增 `catalogIds`（`specHint` 已承载 surface 版本，不再另加 `surfaceVersion`）
- [x] `docs/openapi/agent-access-v1.yaml` 显式声明新字段（`catalogIds` 进 `required`）
- [x] `aap_openapi_contract_test.go` 无需改：它只枚举 profile 顶层字段，`a2ui` 早已在列；
      `catalogIds` 由 `aap_agent_profile_test.go` 断言
- [x] ETag / version seed 纳入 catalog 元数据稳定子集（`aapAgentProfileVersionSeed.A2UI`）
- [x] 集成文档去掉过期的 v0 广告与 `catalogId: "standard"`，改为实际广告值 +
      指向 surface 契约设计（en / zh-CN 同步；SDK 测试样例同步到 v1 扁平形态）
- [x] `GET /api/v1/a2ui/catalogs/standard/v1/{catalog.json,surface.schema.json}`
      （公开免 token、`ETag` + `Cache-Control`、`ACAO: *`；路由即各文档 `$id` 的路径，
      相对 `$ref` 因此在第三方侧同样可解析）
- [x] SDK 类型由 `a2uigen` 生成（第三个目标）：`A2UISurface` / `A2UIComponentNode` /
      `A2UIChartSeries` / `A2UIChartPoint` / `A2UIDataBinding` + catalog 常量；
      不含渲染宿主契约
- [x] SDK helper：`resolveBinding` / `iterCharts` / `isKnownA2UICatalog` /
      `isA2UIDataBinding`，并从 `src/index.ts` 导出
- [x] SDK 单测（17 例）跑共享 fixture：6 种 chartType 全覆盖、绑定与 RFC 6901 转义、
      坏点丢弃、异 catalog 拒绝、完整读一条 assistant 消息
- [x] `make a2ui-check` 覆盖 SDK 的两份生成文件（漂移即红）
- [x] `docs/aap-integration-guide.md` + `.zh-CN.md`：surface 契约章节（形态 / 11 组件 /
      图表语义 / `{path}` 解析 / 限制 / 4 条客户端义务 / schema 分发 / 官方渲染器）；
      非目标同步（Console 渲染与服务端 schema 校验已不再是非目标）

---

## Track only（不在本次范围）

- [ ] `[-]` catalog 协商（createRun 接受 `supportedCatalogIds`，agent 选最佳匹配并锁定）
- [ ] `[-]` `updateDataModel` 增量刷新（原 P2 流式）
- [ ] `[-]` `updateComponents` / `deleteSurface` 完整消息流
- [ ] `[-]` 组件 actions + `actionResponse` RPC（原 P3）
- [ ] `[-]` `drillDown`（依赖 actions）
- [ ] `[-]` catalog `functions`（`required`/`email` 等渲染器侧校验函数）
- [ ] `[-]` Basic Catalog 剩余组件（`Image` `Icon` `List` `Tabs` `Modal` `Slider` `Video` `AudioPlayer`）
- [ ] `[-]` `Text` 的 Markdown 支持（Q-C7 单独评审）
- [ ] `[-]` **永不**把 A2UI action 接到 `interaction.decide`

---

## Cross-cutting acceptance（Phase 1 done）

- [ ] **零** `ProtocolVersion` / protocol-compat 变更（Q-C1）
- [ ] **零规范化规则**：非法 surface 一律降级，代码中不存在别名映射或形状改写
- [ ] 默认 off：`enableA2UI` 未开启的 Agent 与改前完全一致
- [ ] 严格化不新增失败模式：所有校验失败都走 text-only 降级，`run` 成功
- [ ] 诊断不泄漏 surface 取值（有专项单测）
- [ ] KD-11 不回归：敏感键名 surface golden 全链路通过
- [ ] KD-13 不回归：Console `content` 仍 text-first，`contentSha256` 只对 text 重算
- [ ] KD-16 不回归：降级路径 `RecordAssistantResult` 恒非空
- [ ] KD-17 不回归：prompt 仅在 `drive()` 注入一次，resume 不重注
- [ ] 两端渲染器共享同一契约与 fixture，无各自的兼容分支
- [ ] `go test` 相关包绿；`make generate` 绿；两端前端单测绿

---

## Suggested implementation order

```text
  PR-1 (docs)
    │
    ├── PR-2 (spike，独立，不阻塞)
    │
  PR-3 (catalog) ──┬── PR-4 → PR-5 → PR-6 ──┬────────────┐
                   │                         │            │
                   ├── PR-7 (prompt) ────────┤            ├── PR-11 (e2e)
                   │                         │            │
                   └── PR-8 (契约+fixture) ──┼── PR-9  ───┤
                                             └── PR-10 ───┘
```

关键路径：`PR-3 → PR-4 → PR-5 → PR-6 → PR-11`。
PR-7 / PR-8 在 PR-3 落地后即可并行；PR-9 与 PR-10 在 PR-8 后并行
（PR-10 的数据通道部分需等 PR-6）。PR-2 spike 全程不阻塞。

## Definition of Done（Phase 1）

见设计文档 §13。
