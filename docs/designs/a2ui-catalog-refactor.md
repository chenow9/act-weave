# A2UI Catalog Refactor — 从启发式嗅探到 Catalog 强契约（图表优先）

| 字段 | 值 |
|------|-----|
| **Rev** | 2（按「上线前修订」前提重写；Rev 1 的迁移期设计已作废） |
| **Branch** | 在 `feat/a2ui-additive-capability` 上继续（见 §1.4） |
| **Worktree** | `/Users/chen/Documents/act-weave-chat` |
| **Base** | `feat/a2ui-additive-capability` @ `b0e9fc7`（**未 push，远端无对应分支**） |
| **前序设计** | [a2ui-additive-capability.md](./a2ui-additive-capability.md) (Rev 3) |
| **前序 checklist** | [a2ui-additive-capability-checklist.md](./a2ui-additive-capability-checklist.md) |
| **实施 checklist** | [a2ui-catalog-refactor-checklist.md](./a2ui-catalog-refactor-checklist.md) |
| **Status** | 设计待评审 |
| **Created** | 2026-08-12 |
| **Revised** | 2026-08-12 |

---

## 1. 背景与问题

### 1.1 当前状态

MVP 已打通「text 一等 + 可选 a2ui part」的全链路，真实 e2e 通过。但 Q5 决策锁定了
**surface = opaque object，不强制 Google schema**，导致：

- 服务端**从不校验** surface 结构，只校验「是 JSON object」+「≤ 64 KiB」
- 模型输出什么形状都能落库，形状漂移在写入时不可见
- 客户端只能**猜**：`demos/aap-chat/client/src/a2ui-render.ts`(563 行) +
  `a2ui-chart.ts`(598 行) 中很大比例是别名表与形状嗅探

具体嗅探代价（现状代码）：

| 位置 | 嗅探内容 |
|------|----------|
| `renderSurface()` | 5 种 surface 形状顺序试探 + generic fallback |
| `isChartSurface()` | 「有 `fields` 就不是图表；有 `series`/`datasets` 就是图表；`data[0]` 含 `value`/`y`/`count`/`amount` 就是图表」 |
| `isChartKindName()` | 13 个组件名别名 |
| `resolveChartKind()` | 从 `chartType`/`chartKind`/`chartStyle`/`style`/`variant` 5 个字段兜底 |
| `normalizeSeries()` | 6 种数据形状归一化 |
| `renderFieldControl()` | `name\|id\|key`、`label\|title\|name`、`value\|defaultValue\|default` 多路兜底 |
| `resolveTextValue()` | `text\|content\|value\|label\|title` + `literalString\|literal\|path` |
| `renderByKind()` | 每类组件 3–6 个名字别名 |

真实事故已发生一次：2026-08-12 的 live e2e 中，模型输出裸 surface
`{"components":...}` 而抽取器只认信封 `{"surface":{...}}`，导致降级为纯文本
（见 [a2ui-real-e2e-report-2026-08-12.md](./a2ui-real-e2e-report-2026-08-12.md)）。
修复方式是在 `split.go` 再加一条兼容分支——**这正是本次要终止的模式**。

### 1.2 主流（Google A2UI）的做法

调研结论（`a2ui.org` v1.0 candidate / v0.9.1 current）：

1. **官方 Basic Catalog 里没有图表组件**。基础目录只有 `Text`/`Image`/`Icon`/`Video`/
   `AudioPlayer`/`Row`/`Column`/`List`/`Card`/`Tabs`/`Divider`/`Modal`/`Button`/
   `CheckBox`/`TextField`/`DateTimeInput`/`ChoicePicker`/`Slider`。
2. **图表是「自定义组件」的头号案例**（官方 sample `rizzcharts`）。
3. **Catalog 是 JSON Schema 强契约**：agent 产出的 A2UI JSON 必须通过 catalog 校验；
   渲染器按 `component` 字段**精确分派**，不做嗅探。
4. **Agent 只给语义数据，不给视觉**。`rizzcharts` 的 `Chart` 只有
   `type`(enum) / `title` / `chartData:[{label,value,drillDown?}]`——没有颜色、
   尺寸、坐标轴、字体。视觉 100% 由客户端设计系统决定。
5. **协议层与 catalog 层解耦**：`agent_to_renderer.json` 用占位
   `$ref: "catalog.json#/$defs/anyComponent"`，校验时把 `catalog.json` 映射到
   basic 或自有 catalog。同一套协议 schema 配任意 catalog。
6. **组件树是扁平邻接表**：`components: [{id, component, children:[ids]}]`，
   根节点 id 固定为 `root`，`Surface` 容器由 `createSurface` 隐式创建。
7. **数据与结构分离**：任意可绑定属性是 `Dynamic*` 类型，接受字面值或
   `{"path": "/json/pointer"}`；后续 `updateDataModel` 只推数据不重发结构。
8. **能力协商**：客户端在 metadata 里发 `supportedCatalogIds`（按偏好排序），
   agent 选最佳匹配并在 surface 生命周期内锁定；无匹配则**不发 UI**。

### 1.3 目标

把「猜」换成「契约」，且**图表是重点交付物**。

**目标：**

1. 落地一份 ActWeave 自有 catalog（JSON Schema），`Chart` 为一等组件且能力覆盖
   bar / hbar / line / area / pie / donut。
2. 服务端在写路径校验 surface 是否符合 catalog；不符合走**既有的 text-only 降级**。
3. **两个**渲染器（demo vanilla TS + Console Vue）改为注册表精确分派，
   删除全部形状嗅探与别名表。
4. Prompt 由 catalog **生成**，杜绝「prompt 说一套、校验器认另一套」的漂移。
5. 采用官方的扁平邻接表 + `{path}` 数据绑定，让流式与 actions 变成**加法**。

**非目标：**

- 完整 A2UI 消息流（`updateComponents`/`updateDataModel` 增量）——仍是
  `item.completed` 一次性投递（沿用 Q7）
- 组件 action 回传（沿用 Q3 `actions: false`）
- catalog 协商握手
- Basic Catalog 全量组件对齐（v1 只做必需子集）

### 1.4 前提：这不是迁移，是修订

**关键事实**：`feat/a2ui-additive-capability` 的 16 个提交**从未 push**，
`git ls-remote --heads origin feat/a2ui-additive-capability` 为空，
opaque surface 那一版从未上线，也从未有第三方接入。

因此**不为两个从未同时存在的状态写兼容层**。Rev 1 中所有服务于「迁移」的设计一律作废：

| Rev 1 设计 | Rev 2 处置 | 理由 |
|-----------|-----------|------|
| §5 有界规范化（7 条别名规则） | **整节删除** | 唯一理由是保护图表出现率不倒退，但没有用户就没有可倒退的东西。宽容是单向棘轮：加规则永远比改 prompt 快，规则表只会变长。上线前是唯一能免费严格的窗口。替代方案见 §5 |
| `a2ui-surface.v0` legacy 渲染路径（Q-C9） | **不写一行** | v0 从未公开；直接当它不存在，catalog 版从 v1 起。昨天 e2e 落的 demo 行清理掉（e2e 的 no-delete 是测试期安全规则，不是永久数据政策） |
| Phase A（demo）与 Phase C（Console）分批 | **合并为单次交付** | 见下 |
| Q-C1 的兼容性论证 | **论证改为架构性**（§2） | 兼容性理由已失效，但架构理由独立成立 |

**为什么两个渲染器必须同期交付**：这不是「反正要做」，而是**契约验证手段**。
只写一个渲染器，必然会把 vanilla TS + 手写 SVG 的实现便利无意识地烘进 catalog——
某个属性之所以长成那样，只是因为渲染函数刚好方便。同一份 catalog 同时喂
vanilla TS 与 Vue 两个技术栈，是 A2UI「框架无关」承诺的实际检验，也是发现
契约设计缺陷最便宜的手段。

**分支策略**：继续在本分支（或开子分支但合回 `main` 之前先并起来），
让 `main` 最终只收到一个连贯的 A2UI 特性，而不是「先建 opaque surface，
再重构成 catalog」两段历史。中间态从不存在于任何环境，因此不需要迁移路径。

---

## 2. 关键决策（待锁定）

| ID | 决策 | 推荐 |
|----|------|------|
| **Q-C1** | 协议层是否收紧 `surface`？ | **不收紧**。`content-part.schema.json` 的 `surface` 保持 `type: object`。**论证（Rev 2 改写）**：不再依赖兼容性，而是 (a) 官方 `agent_to_renderer.json` 自身就用占位 `$ref` 解耦，因为 catalog 是按客户端协商的，协议层在原理上不可能对着单一 catalog 校验；(b) 新增一种图表类型不该 bump `ProtocolVersion`。耦合成本是永久的，迁移成本是一次性的——即使现在迁移免费也不该建立耦合。副产品：本期**零** protocol-compat 变更 |
| **Q-C2** | catalog 放哪个 schemaset？ | **独立**于 `aap/v1`。放 `backend/internal/a2ui/catalogs/standard/v1/catalog.json`，自带 embed 与独立版本号 |
| **Q-C3** | 用什么校验 JSON Schema？ | **复用** `protocolschema/validate.go`（手写 draft-2020-12 子集，已支持 `$ref`/`oneOf`/`allOf`/`anyOf`/`const`/`enum`/`type`/`required`/`properties`/`additionalProperties`/`items`/`minItems`/`maxItems`/`minLength`/`maxLength`/`pattern`/`minimum`/`maximum`/`uniqueItems`/`not`）。**不引入新依赖** |
| **Q-C4** | `catalogId` 放哪？ | part 上**恒有** `content[i].catalogId`（字段已存在，且不在 KD-11 扫描豁免子树内）。若 Q-C11 采纳，则 surface 内也有一份，由服务端保证两者一致（**surface 内为准，part 上为派生便利字段**） |
| **Q-C5** | 图表数据形状：单一还是多态？ | **单一**：`series: [{name?, points:[{label,value}]}]`。pie/donut 用单 series。拒绝 `chartData`/`data`/`labels+values`/`datasets` 等并存形状。代价是单序列图多一层嵌套（约 10 token），值得 |
| ~~Q-C6~~ | ~~严格校验降低出现率怎么办~~ | **作废**（§1.4）。改为严格拒绝 + 精确诊断（§5） |
| **Q-C7** | `Text` 是否支持 Markdown？ | **v1 不支持**，纯转义文本。减少 XSS 面 |
| **Q-C8** | 如何在 schema 层表达 `actions: false`？ | `Button` 组件**不定义** `action` 属性 + `additionalProperties: false`。模型在结构上无法请求动作 |
| ~~Q-C9~~ | ~~旧 v0 数据怎么办~~ | **作废**（§1.4）。v0 从未公开，不写 legacy 路径 |
| **Q-C10** | Console(Vue) 是否本期交付图表？ | **是，同期**。理由见 §1.4（契约验证手段），细节见 §7.3 |
| **Q-C11**<br>*新增* | `surface` 是否就是官方 `createSurface` 载荷？ | **推荐采纳**。`surface = {surfaceId, catalogId, components, dataModel?}`。模型**不写** `surfaceId`/`catalogId`，由服务端在校验后确定性注入（`surfaceId = runId + ":" + itemId`）。成本≈0（服务端物化），收益是第三方可零适配接入官方渲染器。禁止 `sendDataModel`（它隐含回传通道，属 actions 范畴） |
| **Q-C12**<br>*新增* | 是否评估官方 Lit 渲染器？ | **做限时 spike（≤ 半天），不盲目承诺**。官方 Lit 渲染器产出 web components，Vue 与 vanilla TS 都能用，若可行则布局/表单组件白拿，手写渲染器可删大半（图表仍需自写，它本来就是自定义组件）。疑点：v1.0 仍是 candidate；官方渲染器假定 action 通道存在而我们是 display-only；Lit 运行时增包体；与自有设计系统的贴合度下降（而做自有 catalog 的初衷之一正是设计系统贴合）。**默认走自研渲染器**，spike 只为量化「能省多少」 |

---

## 3. Catalog 设计

### 3.1 标识与版本

| 项 | 值 |
|----|-----|
| `catalogId` | `https://catalog.actweave.dev/standard/v1/catalog.json` |
| `$id` | 同上（官方要求两者一致） |
| `protocolVersion` | `"1.0"` |
| catalog 文件 | `backend/internal/a2ui/catalogs/standard/v1/catalog.json` |
| surface 文件 | `backend/internal/a2ui/catalogs/standard/v1/surface.schema.json` |
| surface `$id` | `https://catalog.actweave.dev/standard/v1/surface.schema.json` |
| envelope `version` | `a2ui-surface.v1`（v0 从未公开，无 legacy 分支） |

`catalogId` **含 `/catalog.json` 后缀**，两个理由：(a) 官方惯例即如此
（Basic Catalog 的 catalogId 就是 `.../catalogs/basic/catalog.json`）；
(b) surface schema 通过相对 `$ref: "catalog.json#/$defs/anyComponent"` 引用 catalog，
校验器按当前文档 `$id` 的目录解析相对引用，因此两份文档必须同目录。

**两份 schema 的分工**（复刻官方的间接层）：`catalog.json` 定义组件与 `$defs`；
`surface.schema.json` 定义 surface 信封并通过相对 `$ref` 指向
`catalog.json#/$defs/anyComponent`。协议层不感知任何一份。

版本策略：**additive 改动**（新组件、新枚举值、新可选属性）沿用 `/standard/v1`；
**breaking 改动**（删属性、改必填、收窄枚举）开 `/standard/v2`，两者可并存以支持
将来的协商。

### 3.2 Surface 载荷

采纳 Q-C11 后的形态（模型只产出 `components` + `dataModel?`，其余由服务端注入）：

```json
{
  "type": "a2ui",
  "version": "a2ui-surface.v1",
  "catalogId": "https://catalog.actweave.dev/standard/v1/catalog.json",
  "surface": {
    "surfaceId": "019ff3f0-bfdd-7b38-9c53-f90bf5812478:item_1",
    "catalogId": "https://catalog.actweave.dev/standard/v1/catalog.json",
    "components": [
      { "id": "root", "component": "Column", "children": ["t1", "c1"] },
      { "id": "t1", "component": "Text", "text": "2026 Q1 各区域营收", "variant": "heading" },
      { "id": "c1", "component": "Chart", "chartType": "bar", "series": { "path": "/revenue" } }
    ],
    "dataModel": {
      "revenue": [
        { "name": "营收(万元)", "points": [
          { "label": "华东", "value": 1280 },
          { "label": "华北", "value": 940 },
          { "label": "华南", "value": 1105 }
        ]}
      ]
    }
  }
}
```

**模型可写字段**：仅 `components`（必填）与 `dataModel`（可选）。
prompt 不提及 `surfaceId`/`catalogId`——模型在生成时无法得知 run/item id，
让它写只会产出垃圾值。

**服务端注入**：校验通过后写入 `surfaceId`（确定性派生，保证渲染器生命周期内唯一）
与 `catalogId`。因此落库字节不能完全由模型输出复现——golden 测试需注入固定 id。

**禁止字段**：`sendDataModel`（隐含回传通道）、以及 `surface` 顶层其他任何键
（`additionalProperties: false`）。

**第三方桥接官方渲染器**：直接 `renderer.handle({version:"v1.0", createSurface: part.surface})`，
零适配。

### 3.3 公共类型（catalog `$defs`）

```json
{
  "ComponentId": { "type": "string", "pattern": "^[A-Za-z_][A-Za-z0-9_]{0,63}$" },
  "ChildList":   { "type": "array", "maxItems": 64, "items": { "$ref": "#/$defs/ComponentId" } },
  "DataBinding": {
    "type": "object",
    "properties": { "path": { "type": "string", "pattern": "^/", "maxLength": 256 } },
    "required": ["path"],
    "additionalProperties": false
  },
  "DynamicString": {
    "oneOf": [
      { "type": "string", "maxLength": 512 },
      { "$ref": "#/$defs/DataBinding" }
    ]
  }
}
```

`FunctionCall` 在 v1 **不支持**（无 catalog functions）。

### 3.4 v1 组件清单

沿用 Basic Catalog 命名以保持可移植性，只有 `Chart` 是自有扩展。

| 组件 | 必填 | 可选 | 说明 |
|------|------|------|------|
| `Column` | `children` | `align`, `justify` | 纵向容器 |
| `Row` | `children` | `align`, `justify` | 横向容器 |
| `Card` | `child` | `title` | 卡片容器（单子节点，同官方示例） |
| `Text` | `text` | `variant`(`body`\|`caption`\|`heading`) | 纯转义文本（Q-C7） |
| `Divider` | — | — | 分隔线 |
| **`Chart`** | `chartType`, `series` | `title`, `unit`, `valueFormat`, `stacked` | §3.5 |
| `TextField` | `label` | `value`, `variant`, `required`, `placeholder` | `variant`: `shortText`\|`longText`\|`number`\|`email`\|`tel`\|`date`\|`password` |
| `CheckBox` | `label` | `value` | |
| `ChoicePicker` | `label`, `options` | `value`, `multiple` | `options: [{value,label}]` |
| `DateTimeInput` | `label` | `value`, `mode`(`date`\|`time`\|`datetime`) | |
| `Button` | `label` | `variant`(`primary`\|`borderless`) | **无 `action` 属性**（Q-C8），渲染为 disabled |

不在 v1（后续 additive）：`Image` `Icon` `List` `Tabs` `Modal` `Slider` `Video` `AudioPlayer`。

每个组件都满足官方的**判别器规则**：`component` 属性是 `const` 且等于其在
`components` map 中的 key，配合 `$defs/anyComponent` 的 `oneOf` 实现路由分派
（校验器的 `oneOf` 要求恰好命中一支，`const` 判别器正好满足）。

### 3.4.1 组件必须扁平（实现约束）

**这是实现中发现的硬约束，违反会导致校验静默失效。**

`protocolschema` 的校验器在处理 `additionalProperties: false` 时，
只比对**同一个 schema 对象内**的 `properties`，且**不支持** `unevaluatedProperties`。
因此：

- 组件定义**不得**使用 `allOf` / `anyOf` / `oneOf` / `$ref` 做顶层组合——
  一旦组合，`additionalProperties: false` 就再也拦不住未知字段
- 每个组件必须**自行声明** `id` 与 `component`，并把两者放进 `required`
  （官方是在信封层用 `allOf` 注入 `ComponentCommon`，我们不能照抄）
- `$ref` 只可用在**属性值**位置（如 `"id": {"$ref": "#/$defs/ComponentId"}`）
- `$ref` 旁边写兄弟关键字无效（校验器遇 `$ref` 即返回），不要依赖这种写法

守卫单测：`TestCatalogComponentsAreFlat`（断言无顶层组合、`additionalProperties:false`、
必含并必填 `id`/`component`）与 `TestCatalogUsesSupportedKeywords`
（递归扫描全部关键字，任何不在校验器子集内的关键字直接 fail，防止静默漏校验）。

### 3.5 `Chart` 组件（核心）

```json
"Chart": {
  "type": "object",
  "description": "统计图表。display-only（v1 无交互）。视觉由渲染器决定。",
  "properties": {
    "component": { "const": "Chart" },
    "chartType": {
      "type": "string",
      "enum": ["bar", "hbar", "line", "area", "pie", "donut"]
    },
    "title": { "$ref": "#/$defs/DynamicString" },
    "series": {
      "oneOf": [
        {
          "type": "array",
          "minItems": 1,
          "maxItems": 8,
          "items": { "$ref": "#/$defs/ChartSeries" }
        },
        { "$ref": "#/$defs/DataBinding" }
      ]
    },
    "unit": { "type": "string", "maxLength": 16 },
    "valueFormat": {
      "type": "string",
      "enum": ["plain", "compact", "percent", "currency"]
    },
    "stacked": { "type": "boolean" }
  },
  "required": ["component", "chartType", "series"],
  "additionalProperties": false
}
```

```json
"ChartSeries": {
  "type": "object",
  "properties": {
    "name":   { "type": "string", "maxLength": 64 },
    "points": {
      "type": "array", "minItems": 1, "maxItems": 64,
      "items": { "$ref": "#/$defs/ChartPoint" }
    }
  },
  "required": ["points"],
  "additionalProperties": false
},
"ChartPoint": {
  "type": "object",
  "properties": {
    "label": { "type": "string", "maxLength": 48 },
    "value": { "type": "number" }
  },
  "required": ["label", "value"],
  "additionalProperties": false
}
```

**待确认的容量上限**（Rev 1 起即为拍定值，评审时请按真实统计场景校准）：
8 series × 64 points、`label` 48 字符、组件数 64、树深 16。

**刻意排除的属性**（`additionalProperties: false` 强制）：

| 排除项 | 理由 |
|--------|------|
| `colors` / `palette` / `color` | 视觉归客户端设计系统；避免模型产出违背品牌的配色 |
| `width` / `height` / `size` | 布局归渲染器；避免固定像素破坏响应式 |
| `legend` / `legendPosition` | 渲染器按容器宽度自行决定 |
| `xAxis` / `yAxis` / `gridLines` / `ticks` | 坐标轴是渲染细节，不是语义 |
| `font` / `fontSize` / `className` / `style` | 同上；且 `style`/`className` 是注入面 |
| `chartKind` / `chartStyle` / `type` / `kind` | 单一入口 `chartType`，消除多路兜底 |
| `data` / `chartData` / `labels` / `values` / `datasets` | 单一入口 `series`（Q-C5） |

`stacked` 保留在 schema 内，判定为**语义**而非视觉（堆叠表达「部分与整体」，
非堆叠表达「并列比较」）。这条边界评审时请确认。

**跨字段语义校验**（JSON Schema 表达不了，落 Go `validateChartSemantics`）：

| 规则 | 违反后果 |
|------|----------|
| `pie` / `donut` 必须恰好 1 个 series | `catalog_invalid` |
| `pie` / `donut` 的所有 `value` 必须 ≥ 0 | `catalog_invalid` |
| `stacked` 仅允许 `bar` / `hbar` / `area` | `catalog_invalid` |
| 多 series 时，各 series 的 `points` 长度必须一致 | `catalog_invalid` |
| 多 series 时，各 series 必须有 `name`（图例需要） | `catalog_invalid` |
| `series` 为 `{path}` 时，`dataModel` 该路径必须解析出合法 series 数组 | `catalog_invalid` |

### 3.6 组件图结构校验（Go）

JSON Schema 管不了图结构，`validateComponentGraph` 负责：

| 规则 | 说明 |
|------|------|
| 恰好一个 `id == "root"` 的组件 | 官方要求 |
| `id` 全局唯一 | 重复即 invalid |
| 所有 `children` / `child` 引用必须存在 | **生产者侧从严**（我们就是生产者）；渲染器侧仍按官方要求优雅降级为占位符（双重保险） |
| 无环 | 防渲染器死循环 |
| 树深度 ≤ 16 | 同上 |
| 组件总数 ≤ 64 | 与 `ChildList.maxItems` 一致 |
| 无孤立节点（不可从 root 到达） | invalid（上线前从严，不做「剔除后继续」的宽容） |

---

## 4. 服务端写路径

### 4.1 流程

在 `a2ui.PrepareAssistantContent` 中，`SplitTextAndA2UI` 之后、
`SerializeAssistantDurable` 之前插入：

```
SplitTextAndA2UI(full)
      │  payload.Surface（模型原样字节）
      ▼
[1] ValidateSurface(catalogId, surface)
      ├── catalog JSON Schema 校验（protocolschema 子集校验器）
      ├── validateComponentGraph
      └── validateChartSemantics
      │
      ├─ 失败 → catalog_invalid + 结构化诊断（§5）→ 复用既有 text-only 降级
      ▼
[2] Materialize：注入 surfaceId / catalogId（Q-C11）
      ▼
[3] len(materialized) > MaxSurfaceBytes ? → too_large
      ▼
SerializeAssistantDurable(text, payload)
```

**与 Rev 1 的差异**：删除 Canonicalize 步骤。模型输出**要么合规、要么降级**，
中间不做形状改写。

**关键点：**

- 大小检查放在物化**之后**（注入 id 会让字节变大）。
- 降级复用现有机制，`run` 仍成功，只是没有 a2ui part。**不新增失败模式**。
- KD-11 不变：catalog 校验是结构校验，**不得**重新引入 surface 子树的敏感键扫描。
  `password` 键名的 golden 测试必须继续通过。

### 4.2 新增 `EmitResult`

```go
EmitCatalogInvalid EmitResult = "catalog_invalid"
```

### 4.3 `catalogId` 处理

v1 只注册平台 catalog。模型不写 `catalogId`，服务端物化时注入平台默认值。
若模型仍自行写入且与平台值不符 → `catalog_invalid`（防止模型臆造 catalog）。

---

## 5. 严格拒绝 + 精确诊断（取代 Rev 1 的规范化）

Rev 1 用 7 条别名规则换取图表出现率。Rev 2 改为**用可观测性换宽容度**：
严格拒绝，但把拒绝原因做到足以直接指导 prompt 迭代。

### 5.1 诊断内容

`ValidateSurface` 失败时返回结构化诊断，而不是布尔值：

```go
type Diagnostic struct {
    Reason    string // schema | graph | chart_semantics | unknown_catalog
    Pointer   string // 失败位置的 JSON Pointer，如 /components/2/chartType
    Keyword   string // 违反的 schema 关键字，如 enum / required / additionalProperties
    Component string // 所属组件类型（可得时），如 Chart
    Expected  string // 期望值摘要，如 "bar|hbar|line|area|pie|donut"
}
```

### 5.2 硬约束：诊断只含结构，不含取值

surface 子树按 KD-11 豁免敏感键扫描，因此它**可能合法地包含用户数据**。
诊断与日志因此只允许输出：JSON Pointer 路径、关键字名、组件类型名、
schema 侧的期望值（来自 catalog，非来自 payload）。

**禁止**输出 payload 的实际取值、键名之外的字符串内容、或整段 surface JSON。
与现有策略一致（`payload_policy.go`：错误永不包含敏感值本身）。

### 5.3 迭代闭环

```
e2e / 灰度 → a2ui_catalog_invalid{reason,keyword,component} 打点
          → 采样结构化诊断日志
          → 定位模型漂移的具体字段
          → 改 prompt（§6，prompt 由 catalog 生成，改 catalog 即改 prompt）
          → 重跑 e2e
```

**只有在证明「某类漂移无论怎么调 prompt 都无法消除」之后**，才考虑引入针对该漂移的
规范化规则；引入时必须带单测、带指标、带删除条件。默认状态是**零规范化规则**。

---

## 6. Prompt 由 Catalog 生成

### 6.1 问题

当前 `prompt.go` 的 `promptAppendixV1` 是**手写**的，它描述的形状
（`{"type":"chart","chartType":"bar","labels":[...],"series":[{"name","data"}]}`）
与 catalog 契约不同。手写 prompt 与校验器分离就是 08-12 事故的根因类别。

### 6.2 方案

新增 `a2ui-prompt.v2`，由 catalog **生成**：

- 生成器：`backend/cmd/a2uipromptgen`（或并入现有 `make generate`）
- 输入：`catalogs/standard/v1/catalog.json`
- 输出：`backend/internal/a2ui/prompt_appendix_v2.gen.go`
- 内容：组件清单（名称 + 必填/可选属性 + 枚举值）、`Chart` 数据形状、
  2 个 few-shot 示例（1 图表 1 表单）、硬规则
- **不含** `surfaceId` / `catalogId`（服务端注入，§3.2）
- **漂移守卫**：单测 `TestPromptAppendixMatchesCatalog` 重新生成并与 `.gen.go`
  比对，不一致则 fail

catalog 的官方 `instructions` 字段承载给 LLM 的设计指引（何时用图表、何时用表单），
生成时并入 prompt。

### 6.3 Token 预算

目标 ≤ 900 token。控制手段：只列 v1 的 11 个组件；枚举值内联；示例压缩成单行 JSON。
生成器加断言：超预算则 build fail，强制在扩 catalog 时同步评估成本。

### 6.4 示例（生成内容节选）

```
Chart: {"component":"Chart","chartType":"bar|hbar|line|area|pie|donut",
        "series":[{"name":"s","points":[{"label":"A","value":1}]}],
        "title"?:str,"unit"?:str,"valueFormat"?:"plain|compact|percent|currency","stacked"?:bool}
  - pie/donut: 恰好 1 个 series，value ≥ 0
  - 多 series: 每个都要 name，points 长度一致
  - 不要写颜色/尺寸/坐标轴，客户端负责视觉
```

---

## 7. 渲染器重构（两个渲染器，同期交付）

### 7.1 共享契约

两个渲染器实现**同一套**分派契约，只是宿主不同：

```ts
export interface RenderCtx {
  byId: Map<string, ComponentNode>;
  dataModel: unknown;
  depth: number;
  resolveString(v: unknown): string;   // literal | {path}
  renderChild(id: string): Rendered;   // demo: string；Console: VNode
}
export type ComponentRenderer<R> = (node: ComponentNode, ctx: RenderCtx) => R;
export type Registry<R> = Record<string, ComponentRenderer<R>>;
```

规则（两端一致）：

- 分派仅凭 `node.component` 精确查表，**无** `toLowerCase()`、**无**别名
- 未注册组件 → 占位符（官方要求的优雅降级）+ 开发态 warn
- 深度 > 16 或引用缺失 → 占位符（防御性，服务端已拦）
- 所有字符串值转义；`Text` 不解析 markdown（Q-C7）
- `Button` 恒 disabled（`actions: false`）

**语义与视觉的分界线**：`chartType`/`series`/`unit`/`valueFormat`/`stacked`
来自 surface；配色、尺寸、图例位置、坐标轴刻度、字体全部由渲染器自行决定。
两端视觉**允许不同**——这正是契约正确的标志。

### 7.2 demo（vanilla TS）

```
client/src/a2ui/
  index.ts        渲染入口 renderA2UICard
  registry.ts     Record<string, ComponentRenderer<string>>
  resolve.ts      DynamicString / DataBinding（JSON Pointer）
  chart.ts        Chart 渲染器（读契约 → 调绘图）
  chart-svg.ts    bar/hbar/line/area/pie/donut 纯绘图函数
  components/     text / column / row / card / divider /
                  text-field / check-box / choice-picker / date-time-input / button
```

**删除清单：**

| 删除 | 现位置 |
|------|--------|
| `renderSurface()` 的 5 形状试探 + `renderGenericObject` | `a2ui-render.ts` |
| `isFormSurface` / `renderFormSurface` / `looksLikeFieldMap` / `renderFieldMap` | `a2ui-render.ts` |
| `renderByKind()` 的全部名称别名分支 | `a2ui-render.ts` |
| `resolveTextValue()` 的 5 路兜底 | `a2ui-render.ts` |
| `flattenChildRefs` 的 `explicitList`/`list` 兼容 | `a2ui-render.ts` |
| `mapInputType` 的 18 项别名表（改 catalog `variant` 枚举直映） | `a2ui-render.ts` |
| `isChartSurface` / `isChartKindName` / `resolveChartKind` | `a2ui-chart.ts` |
| `normalizeSeries` / `pointsFromObjects` / `zipPoints` 的多形状分支 | `a2ui-chart.ts` |

**保留**：`renderBarSvg` / `renderHBarSvg` / `renderLineSvg` / `renderPieSvg` /
`renderLegend` / `formatNum` / `escapeHtml`（真正的绘图与安全代码，质量没问题）。

规模预估：

| 文件 | 现在 | 预估 | 变化 |
|------|------|------|------|
| `a2ui-render.ts` → `a2ui/` 拆分 | 563 | ~300 | −47% |
| `a2ui-chart.ts` → `chart.ts` + `chart-svg.ts` | 598 | ~420 | −30% |
| 合计 | 1161 | ~720 | **−38%** |

`mock-stream.ts` 全部样例改 catalog v1 形态，并新增 9 个视觉基线样例：
bar / hbar / line / area / pie / donut / 多 series / stacked / `{path}` 绑定。

### 7.3 Console（Vue）

Console 当前按 KD-13 只投影 text，不渲染 surface。要让图表成为产品能力，
需要两处改动：

**数据通道**：新增 `messageDTO.a2ui`（**仅** surface，不含 raw envelope）。

- KD-13 不回归：`content` 仍是 join 后的 text；
  `contentSha256` / `contentLength` 仍只对投影后 text 重算
- session reload 后图表可重现（surface 已落库）
- 该字段仅在 durable content 为 `aap.message-content.v1` 且含 a2ui part 时出现

**渲染实现**（评审决策）：

| 方案 | 优点 | 缺点 |
|------|------|------|
| (a) 移植 SVG 绘图 | 零依赖；与 demo 视觉一致；完全可控 | 绘图代码两处维护 |
| (b) 引入 ECharts / Chart.js | 视觉与交互（tooltip/hover）更好；省绘图代码 | 增包体（ECharts 按需引入约 150–300 KB）；两端视觉不一致 |

**倾向 (b)**：Console 是产品面，tooltip 与响应式的体感差异明显；
且「两端视觉不同」不是缺点而是契约正确性的证据（§7.1）。
但需确认包体预算，可用按需引入 + 异步加载缓解。

---

## 8. 官方渲染器 spike（Q-C12）

**时间盒**：≤ 半天。**产出**：一页结论，不写生产代码。

验证问题：

1. `@a2ui/lit` 能否在 vanilla TS demo 与 Vue Console 中都跑起来（web components 互操作）
2. 我们的 surface（Q-C11 形态）能否零适配喂进去
3. 自定义 `Chart` 组件如何注册进 Lit catalog，成本多少
4. display-only（无 action 通道）是否会让官方渲染器报错或行为异常
5. 引入后包体增量
6. 自有设计系统（Console 现有样式）的贴合成本

**决策规则**：只有当 (1)(2)(4) 全部通过且 (5)(6) 可接受时才考虑采纳；
否则按 §7 自研。无论结论如何，catalog 设计**不因 spike 结果而改变**——
这也是 Q-C11「让 surface 等于官方载荷」的价值：保留选择权。

---

## 9. AAP Profile 与 SDK

### 9.1 Profile

`a2ui` 对象扩展（仍在 `enableA2UI` 时才出现）：

```json
"a2ui": {
  "enabled": true,
  "delivery": "item_completed",
  "streaming": false,
  "actions": false,
  "maxSurfaceBytes": 65536,
  "specHint": "a2ui-surface.v1",
  "catalogIds": ["https://catalog.actweave.dev/standard/v1/catalog.json"]
}
```

只新增 `catalogIds`：既有的 `specHint` 已经承载 surface 版本（现值 `a2ui-surface.v1`），
再加一个 `surfaceVersion` 只会让同一事实有两个字段。
新字段 → OpenAPI 显式声明，`aap_openapi_contract_test.go` allowlist 同步，ETag seed 纳入。

### 9.2 Catalog 分发

公开、免 token、可缓存（`ETag` + `Cache-Control`）、带 `Access-Control-Allow-Origin: *`
（静态无凭据文档，浏览器渲染器需要跨域读取）：

```
GET /api/v1/a2ui/catalogs/standard/v1/catalog.json
GET /api/v1/a2ui/catalogs/standard/v1/surface.schema.json
```

两份都分发，因为 surface schema 通过相对 `$ref` 指向 catalog，只给一份第三方无法完成校验；
两条路由是各文档 `$id` 的路径本身，因此那个相对 `$ref` 在服务端与第三方侧解析一致
（`TestA2UISchemasAreServedAsSiblings` 就守这条）。规范说 `catalogId` 不必可解析，
所以它仍是标识符，取 schema 走这两个端点。

### 9.3 SDK

- 生成类型（`a2uigen` 第三个目标，与两个渲染器同源）：`A2UISurface` /
  `A2UIComponentNode` / `A2UIChartSeries` / `A2UIChartPoint` / `A2UIDataBinding`
  + `A2UI_CATALOG_ID` / `A2UI_SURFACE_VERSION` / `A2UI_COMPONENT_NAMES` /
  `A2UI_CHART_TYPES` / `A2UI_ENUMS` / `A2UI_CHILD_MEMBERS` / `A2UI_LIMITS`。
  **不含**渲染宿主契约（`A2UIRenderCtx` / `A2UIRegistry`）：SDK 给的是读 surface 的词汇，
  不规定第三方怎么画
- helper：`findA2UIPart(item)`（已有）、新增 `resolveBinding(surface, value)`、
  `iterCharts(surface): A2UIChart[]`（series 已解析、坏点已丢弃）、
  `isKnownA2UICatalog(surface)`（渲染前的 catalog 闸门）
- 单测跑同一组共享 fixture（`sdk/typescript/test/generated/a2ui-fixtures.gen.ts`），
  因此「SDK 读得懂平台真的会发的 surface」是被守着的，不是声称的
- 文档：catalog 契约、`{path}` 解析、未知组件优雅降级的客户端义务、
  以及「surface 可直接喂官方渲染器」的说明（Q-C11 采纳后）

---

## 10. 可观测性

| 指标 | 含义 |
|------|------|
| `a2ui_catalog_invalid{reason,keyword,component}` | catalog 校验失败。`reason` ∈ `schema`/`graph`/`chart_semantics`/`unknown_catalog` |
| `a2ui_chart_emitted{chartType}` | 成功产出图表，按类型分布 |
| `a2ui_prompt_tokens` | prompt appendix token 估算（预算守卫） |

现有 `a2ui_extract_ok` / `a2ui_extract_fail` / `a2ui_preflight_fail` /
`a2ui_degraded_text` 保持不变。Rev 1 的 `a2ui_canonicalized{rule}` 随规范化一并删除。

**关键运营视图**：`a2ui_chart_emitted` 与 `a2ui_catalog_invalid{reason=schema}`
的比值就是「严格化是否伤害了图表可用性」的直接答案。
上线前的 e2e 阶段就要把这个比值调到可接受，手段是改 prompt（§5.3），不是加宽容。

---

## 11. 风险与缓解

| 风险 | 影响 | 缓解 |
|------|------|------|
| 严格校验导致图表出现率低 | 图表是本次重点，出现率低等于交付失败 | catalog 生成的精确 prompt + §5 诊断闭环；e2e 覆盖 6 种图表类型；**上线前**把 invalid 率压下来（这是选择严格路线的前提，也是它可行的原因——还没有用户，有窗口迭代 prompt） |
| 未来在压力下重新加回宽容规则 | 退化回今天的状态 | §5.3 的引入门槛写死：必须先证明 prompt 无法消除该漂移，且规则必须带单测、指标与删除条件 |
| prompt 与 catalog 再次漂移 | 复现 08-12 事故 | prompt 由 catalog 生成 + `TestPromptAppendixMatchesCatalog` 漂移守卫 |
| catalog 校验误伤敏感键 golden 用例 | KD-11 回归 | catalog 校验只做结构；显式单测：含 `password`/`accessToken`/`apiKey` 的 `TextField` 必须通过全链路 |
| 诊断日志泄漏 surface 内容 | surface 是扫描豁免区，可能含用户数据 | §5.2 硬约束：诊断只含 JSON Pointer / 关键字 / 组件名 / catalog 侧期望值 |
| `protocolschema` 子集校验器不支持某关键字 | catalog 写了但不生效（**静默漏校验**） | 新增 `TestCatalogUsesSupportedKeywords`：扫描 catalog 中出现的全部关键字，断言都在子集白名单内 |
| 物化注入 id 后超 `MaxSurfaceBytes` | 本可成功的 surface 被拒 | 大小检查放物化之后；单测覆盖临界 |
| 两个渲染器契约实现不一致 | catalog 形同虚设 | 共享 §7.1 契约类型定义；两端跑同一组 fixture（9 个基线样例）；Console e2e 与 demo e2e 用同一批 surface |
| Console 新增 `messageDTO.a2ui` 通道 | Console 数据面回归 | `contentSha256` 仍只对 text 重算；session reload 单测；字段仅在含 a2ui part 时出现 |
| 复用 `protocolschema` 引入包依赖边 | 潜在循环依赖 | `a2ui → protocolschema` 单向；`protocolschema` 不 import 任何内部包，已核实无环 |

---

## 12. 分期

| Phase | 内容 | 交付价值 |
|-------|------|----------|
| **1（单次合并）** | catalog schema + Go 校验 + 诊断 + prompt 生成 + **两个**渲染器（demo + Console）+ 真实 e2e | 图表在严格契约下端到端可用，且契约经双栈验证 |
| **2** | Profile `catalogIds` + catalog 分发端点 + SDK 类型与 helper + 集成文档 | 第三方可接 |
| **track only** | catalog 协商、`updateDataModel` 流式、actions + `actionResponse`、`drillDown`、catalog functions、Basic Catalog 剩余组件 | 对齐官方完整能力 |

Phase 1 内部的 PR 拆分与验收见 [checklist](./a2ui-catalog-refactor-checklist.md)。

---

## 13. Definition of Done（Phase 1）

1. `catalog.json` 落盘，`catalogId` 与 `$id` 一致，全部关键字在校验器子集内。
2. 服务端写路径对 surface 做 catalog + 图结构 + 图表语义三层校验；
   失败走既有 text-only 降级，`run` 仍成功。
3. **零规范化规则**：非法 surface 一律降级，不做形状改写。
4. 校验失败产出结构化诊断，且诊断不含 payload 取值（§5.2）。
5. prompt appendix 由 catalog 生成，漂移守卫单测就位。
6. demo 与 Console **两个**渲染器均为注册表精确分派；§7.2 删除清单全部清空。
7. bar / hbar / line / area / pie / donut 六种图表在**两个**渲染器中均可渲染，
   且在真实模型 e2e 中各至少成功产出一次。
8. Console 历史 text-first 不回归；`contentSha256` 仍只对 text 重算。
9. 敏感键名 golden 用例（`password` 等）继续全链路通过。
10. 默认 off 行为不变；`enableA2UI=false` 的 Agent 零行为变化。
11. **零** `ProtocolVersion` / protocol-compat 变更（Q-C1）。
12. 相关 Go 包与两端前端单测绿；`make generate` 绿。
