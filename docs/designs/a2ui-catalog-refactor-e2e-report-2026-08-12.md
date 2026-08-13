# A2UI catalog 重构 · 真实 e2e 报告（2026-08-12）

对应 [清单](./a2ui-catalog-refactor-checklist.md) PR-11，设计见 [a2ui-catalog-refactor.md](./a2ui-catalog-refactor.md)。

| 项 | 值 |
| --- | --- |
| 分支 / worktree | `feat/a2ui-additive-capability` · `/Users/chen/Documents/act-weave-chat` |
| 浏览器 | 本机 Chrome 151.0.7922.110（Playwright `channel: "chrome"`，有头） |
| Console | Vite `http://127.0.0.1:5174` |
| Demo | UI `http://127.0.0.1:5188` + BFF `:8791`（live AAP，非 mock） |
| 后端 | `go run ./cmd/server` → `http://127.0.0.1:8082` |
| 数据面 | 既有 Docker：Postgres `:15432`、Redis `:16379`、MinIO `:9000` |
| 数据库 | **副本库 `actweave_a2ui_e2e`**（见「环境偏差」） |
| Workspace / Agent | `AI识别管理平台` `019facc3-…` / `平台助手` `019facd9-…` |
| 模型 | `sub2api` 网关 · `gpt-5.2`（VERIFIED，46ms） |
| 安全约束 | 全程 **no-delete**：只新建会话、发消息、改 Agent 标记与模型名 |

## 环境偏差（两处，均已隔离）

**副本库。** 共享开发库 `actweave` 已被隔壁 worktree（`feat/progressive-tool-disclosure`）迁移到
schema 版本 19，而本分支只带到 18，本分支的后端因此无法直接启动
（`no migration found for version 19`）。回滚共享库会破坏那个 worktree，所以做了
`CREATE DATABASE actweave_a2ui_e2e TEMPLATE actweave`，在**副本**上执行 19 的 down
并把 `schema_migrations` 置为 18。共享库全程保持 19 未动，事后复核确认。

**模型名。** 网关在账号组之间轮换，`gpt-5.4` 与 `gpt-5.6-terra` 都出现过
`404 Model … is not supported by any configured account in this group` 与 `502`；
按指示改为 `gpt-5.2` 后稳定。该改动只落在副本库，共享库的模型配置仍是 `gpt-5.4`。

## 方法

沿用 08-12 纠正后的**自然对话**方法论：给模型业务数据与阅读诉求（"哪个渠道最强"、
"名字长横着看更好读"、"中间留个空位标总人数"），**不提供任何围栏或 JSON 片段**，
chartType 完全由模型自选。每个话题开新会话，避免上一轮示例污染下一轮。

## 结果一：6 种 chartType 全部由模型自主命中

改 prompt 之前的一轮（后端旧 prompt）：

| 轮次 | 自然诉求 | 模型选择 | 结果 |
| --- | --- | --- | --- |
| 20 | 四渠道销量对比 | `bar` | 渲染成功 |
| 21 | 八个长名部门人均产出排名 | `hbar` | 渲染成功 |
| 22 | 12 天日活趋势 | `line` | 渲染成功 |
| 23 | 累计营收"堆积爬坡感" | （想用 `area`） | **失败**：`invalid_json` |
| 24 | 四产品线收入占比 | `donut` | 渲染成功 |
| 25 | 会员等级构成、中间标总数 | `donut` | 渲染成功 |
| 26 | 客户回访填报表单 | 表单（6 字段） | 渲染成功 |
| 30 | 累计营收、曲线下方填充 | `area` | 渲染成功 |
| 31 | 收入占比、别做空心 | `pie` | 渲染成功 |
| 32 | 两渠道分季度、要看合计 | （想用 `bar` + `stacked`） | **失败**：`invalid_json` |

6 种 chartType（`bar` `hbar` `line` `area` `pie` `donut`）各至少成功一次，
外加 `stacked` 与多 series 场景（见结果三）。零 placeholder：所有成功轮次的 Console
DOM 里没有任何 `[data-a2ui-placeholder]`，即没有发生降级渲染。

## 结果二：严格校验一次都没拒绝，失败全是模型的 JSON 语法

11 次尝试的指标分布：

```
aap_a2ui_emit_total{result="ok"}            9
aap_a2ui_emit_total{result="invalid_json"}  2
aap_a2ui_catalog_invalid_total              (无该 series — 从未触发)
aap_a2ui_chart_emitted_total{chart_type="bar"}   2
aap_a2ui_chart_emitted_total{chart_type="hbar"}  1
aap_a2ui_chart_emitted_total{chart_type="line"}  1
aap_a2ui_chart_emitted_total{chart_type="area"}  1
aap_a2ui_chart_emitted_total{chart_type="pie"}   1
aap_a2ui_chart_emitted_total{chart_type="donut"} 2
```

`a2ui_catalog_invalid{reason,keyword,component}` **一次都没命中**——没有未知组件、
没有多余属性、没有图表语义冲突。设计 §5.3 担心的"严格路线压低产出率"在真实模型上
没有出现：模型在 catalog 生成的 prompt 下写出的 surface 全部合规。

两次失败都是同一类：长多行围栏体末尾括号写错。从浏览器里抓到的原始流可见
第 32 轮的围栏体结尾是 `]}}`——`components` 数组从未闭合：

```json
{"components":[{"id":"root","component":"Chart","chartType":"bar","stacked":true,…,"series":[
{"name":"线上","points":[…]},
{"name":"线下","points":[…]}
]}}
```

第 23 轮的推理轨迹（`agent_run_steps.output_summary`）显示模型已经决定
`chartType: "area"`、`unit: "万元"`，只是围栏体没能解析出来。

## 结果三：只改 prompt 后，原本失败的两类场景全部通过

按清单要求**不得靠放宽校验解决**，只在生成器里补一条围栏体规则
（`backend/internal/a2ui/prompt.go`，golden 同步刷新）：

> Close every bracket: the body must parse as one complete JSON object, with no
> comments and no prose inside the fence. A body that does not parse is dropped
> whole and the user sees your prose alone.

重启后端后重跑（含第 32 轮那个原样的堆叠诉求）：

| 轮次 | 自然诉求 | 模型选择 | 结果 |
| --- | --- | --- | --- |
| 40 | 两渠道分季度、要看合计 | `bar` + `stacked`（2 series） | 渲染成功 |
| 41 | 两大区 6 个月走势对比 | `line`（2 series，带图例） | 渲染成功 |
| 42 | 20 家门店客单价排名 | `hbar`（20 points） | 渲染成功 |
| 43 | 工位方案小问卷 | 表单（6 字段 + 禁用提交） | 渲染成功 |

调整前后对比：

| | 尝试 | ok | invalid_json | catalog_invalid |
| --- | --- | --- | --- | --- |
| 改 prompt 前 | 11 | 9（81.8%） | 2（18.2%） | 0 |
| 改 prompt 后 | 4 | 4（100%） | 0 | 0 |

样本小，不足以宣称问题根除；可说的是那条规则针对的正是唯一出现过的失败模式，
且此前稳定失败的堆叠场景在规则加入后一次通过。

## 结果四：同一 surface 在 Console 与 demo 两端一致

从副本库导出今天全部 13 个 `a2ui-surface.v1` surface（`chart_messages` 的 durable
envelope），逐个喂进 demo 渲染器（浏览器内 `import('/src/a2ui/index.ts')` →
`renderSurface`）：

| 检查 | 结果 |
| --- | --- |
| 13/13 渲染出内容 | 是 |
| chartType 多重集与库内一致 | `bar×2 hbar×2 line×2 donut×2 area×1 pie×1 bar+stacked×1` |
| 表单 surface | 两端同为 6 个 `.a2ui-field` + 1 个禁用按钮 |
| placeholder 数量 | 两端均为 0 |
| 页面 JS 异常 | 无 |

堆叠图两端截图逐像素比对：网格刻度（0/1250/2500/3750/5000 件）、柱位、堆叠比例、
图例项完全一致。唯一差异是徽章文案（Console `Bar · 件` vs demo `柱状图 · 件`）——
i18n 差异，不是几何差异；Console 当前 profile 是 en 语言。

demo 还跑了 live AAP 全链路（BFF → 后端 → Agent → 真实模型）两轮自然对话，
分别拿到 `bar` 与 `donut`，说明 demo 不只是被当渲染库调用。

## 结果五：`enableA2UI=false` 的 Agent 零变化

把 `平台助手` 的 `contextPolicy.aap.enableA2UI` 置为 `false`（保留兄弟标记
`includeCompactionSummary: true`，`schemaVersion` 仍为 `session-context-policy.v2`），
用同一句会画图的诉求跑一轮：

| 检查 | 结果 |
| --- | --- |
| 流式文本里出现围栏 | 否（模型甚至回答"我当前的工具里没有图表"） |
| Console 出现 surface | 否 |
| durable content | 纯 markdown 文本，不是 `aap.message-content.v1` envelope |
| a2ui 指标增量 | 仅 `emit_total{result="none"}` +1；chart / catalog_invalid 均未动 |

改回 `true` 后同一句诉求立刻恢复出 `bar` 图，确认这是标记造成的差异而非环境漂移。
Console 开关 UI 路径本身由 `frontend/e2e/a2ui-enable.spec.ts` 与 08-11 的报告覆盖，
本轮为省时间走 API 改标记。

## 顺带验证到的历史数据行为

副本库里还留着 08-11 e2e 写下的 `a2ui-surface.v0` 消息。Console 历史里它们只显示文本、
不显示 surface，也没有 placeholder——PR-10 的"旧版本 surface 不进渲染器"在真实数据上成立。

## 未覆盖

- `Row` / `Card` / `Divider`：模型这 15 次自主选择里没用到（fixture 与单测有覆盖）。
- 组件动作：MVP 为 `actions:false`，按钮渲染为禁用态，符合预期。
- 大盘产出率：15 次真实调用不能作为产出率统计，只能作为"严格路线可用"的证据。

## 结论

| 层 | 结论 |
| --- | --- |
| 真实栈（本机 Chrome + 本地 FE/BE + Docker 数据面） | **PASS** |
| 6 种 chartType 由真实模型自主命中 | **PASS** |
| 严格 catalog 校验在真实模型下的拒绝率 | **0/15** |
| 表单场景不回归 | **PASS** |
| 同一 surface 两端渲染一致 | **PASS**（几何一致，徽章文案随 i18n） |
| `enableA2UI=false` 零变化 | **PASS** |
| invalid 率改进手段 | 仅改 prompt，未放宽任何校验 |

## 产物

均在 `/tmp/a2ui-e2e/artifacts/`（未入库）：

| 文件 | 内容 |
| --- | --- |
| `turns.jsonl` | 每轮：诉求、状态、DOM 事实、抓到的原始围栏 |
| `surfaces.json` | 从副本库导出的 13 个 surface |
| `demo-summary.json` | demo live 两轮 + 13 个跨端渲染的事实 |
| `metrics-*.txt` | 各阶段 `a2ui` 指标快照 |
| `20-*.png … 71-*.png` | Console 逐轮截图 |
| `50-*.png` `51-*.png` `60-cross-*.png` | demo live 与跨端渲染截图 |
