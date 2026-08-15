/**
 * Product-facing A2UI stories for the chat demo.
 *
 * Shared catalog fixtures stay the renderer / server baseline. These surfaces
 * exist so empty-state suggestions and mock replies match what a business user
 * asked for, using only catalog v1 components.
 */

import { A2UI_CATALOG_ID, A2UI_SURFACE_VERSION, type A2UISurface } from "./a2ui/generated/catalog.gen";
import type { A2UIExtract } from "./a2ui";

export interface DemoStoryAttachment {
  name: string;
  mediaType: string;
  /** UTF-8 payload for text/* / json cards (Mock blob + download). */
  text?: string;
  /** Browser-only colored preview tile for image/* Mock cards. */
  preview?: { title: string; tone: string };
}

export interface DemoStory {
  id: string;
  /** Empty-state chip. */
  label: string;
  /** Sent as the user message. */
  prompt: string;
  /** Extra phrases that should pick this story. */
  aliases?: readonly string[];
  keywords: RegExp;
  /** Assistant prose. Product voice, not protocol notes. */
  reply: string;
  surface?: A2UISurface;
  /** Mock outbound cards (assistant bubble). */
  attachments?: readonly DemoStoryAttachment[];
}

const TREND_MONTHS = ["3月", "4月", "5月", "6月", "7月", "8月"] as const;
const BOOKINGS = [42, 51, 58, 67, 71, 76] as const;
const DEALS = [18, 22, 29, 21, 24, 31] as const;

function series(name: string, values: readonly number[]): { name: string; points: Array<{ label: string; value: number }> } {
  return {
    name,
    points: TREND_MONTHS.map((label, index) => ({ label, value: values[index] ?? 0 })),
  };
}

const bookingSurface: A2UISurface = {
  components: [
    { id: "root", component: "Column", children: ["heading", "caption", "row1", "row2", "slot", "note", "rule", "actions"] },
    { id: "heading", component: "Text", text: "预约产品演示", variant: "heading" },
    { id: "caption", component: "Text", text: "填一下联系方式和期望时间，我来帮你排期。", variant: "caption" },
    { id: "row1", component: "Row", children: ["name", "company"] },
    { id: "row2", component: "Row", children: ["phone", "when"] },
    {
      id: "name",
      component: "TextField",
      label: "姓名",
      placeholder: "怎么称呼",
      variant: "shortText",
      required: true,
      value: "张三",
    },
    {
      id: "company",
      component: "TextField",
      label: "公司",
      placeholder: "公司或团队",
      variant: "shortText",
      value: "星云科技",
    },
    {
      id: "phone",
      component: "TextField",
      label: "手机",
      placeholder: "11 位手机号",
      variant: "tel",
      required: true,
      value: "13800121206",
    },
    {
      id: "when",
      component: "DateTimeInput",
      label: "演示日期",
      mode: "date",
      value: "2026-08-20",
    },
    {
      id: "slot",
      component: "ChoicePicker",
      label: "期望时段",
      value: ["pm"],
      options: [
        { value: "am", label: "上午" },
        { value: "pm", label: "下午" },
        { value: "eve", label: "晚上" },
      ],
    },
    {
      id: "note",
      component: "TextField",
      label: "备注",
      variant: "longText",
      placeholder: "想看的模块、参会人数…",
    },
    { id: "rule", component: "Divider" },
    { id: "actions", component: "Row", children: ["submit", "cancel"], justify: "end" },
    { id: "submit", component: "Button", label: "提交预约", variant: "primary" },
    { id: "cancel", component: "Button", label: "取消", variant: "borderless" },
  ],
};

const confirmSurface: A2UISurface = {
  components: [
    { id: "root", component: "Column", children: ["heading", "card", "actions"] },
    { id: "heading", component: "Text", text: "演示已排期", variant: "heading" },
    { id: "card", component: "Card", title: "预约详情", child: "details" },
    { id: "details", component: "Column", children: ["who", "when", "phone"] },
    { id: "who", component: "Text", text: "张三 · 星云科技" },
    { id: "when", component: "Text", text: "8 月 20 日（周四）14:00–15:00" },
    { id: "phone", component: "Text", text: "手机 138****1206", variant: "caption" },
    { id: "actions", component: "Row", children: ["cal", "resched"], justify: "end" },
    { id: "cal", component: "Button", label: "加入日历", variant: "primary" },
    { id: "resched", component: "Button", label: "改期", variant: "borderless" },
  ],
};

const trendSurface: A2UISurface = {
  components: [
    { id: "root", component: "Column", children: ["heading", "caption", "chart"] },
    { id: "heading", component: "Text", text: "近 6 个月预约与成交", variant: "heading" },
    { id: "caption", component: "Text", text: "预约持续上升；6 月成交回落，7–8 月开始恢复。", variant: "caption" },
    {
      id: "chart",
      component: "Chart",
      chartType: "line",
      valueFormat: "plain",
      series: [series("预约", BOOKINGS), series("成交", DEALS)],
    },
  ],
};

const sourcesSurface: A2UISurface = {
  components: [
    { id: "root", component: "Column", children: ["heading", "caption", "chart"] },
    { id: "heading", component: "Text", text: "线索来源分布", variant: "heading" },
    { id: "caption", component: "Text", text: "官网仍是主来源，转介绍占比在抬升。", variant: "caption" },
    {
      id: "chart",
      component: "Chart",
      chartType: "pie",
      valueFormat: "percent",
      series: [
        {
          points: [
            { label: "官网", value: 38 },
            { label: "转介绍", value: 24 },
            { label: "展会", value: 18 },
            { label: "企业微信", value: 14 },
            { label: "其他", value: 6 },
          ],
        },
      ],
    },
  ],
};

const insightSurface: A2UISurface = {
  components: [
    { id: "root", component: "Column", children: ["heading", "body", "chart", "actions"] },
    { id: "heading", component: "Text", text: "6 月成交掉了一截", variant: "heading" },
    {
      id: "body",
      component: "Text",
      text: "预约还在涨，成交从 29 降到 21。建议把演示后 48 小时跟进写进流程，再看 8 月能否站稳。",
    },
    {
      id: "chart",
      component: "Chart",
      chartType: "bar",
      valueFormat: "plain",
      series: [series("预约", BOOKINGS), series("成交", DEALS)],
    },
    { id: "actions", component: "Row", children: ["export", "detail"], justify: "end" },
    { id: "export", component: "Button", label: "导出摘要", variant: "primary" },
    { id: "detail", component: "Button", label: "查看明细", variant: "borderless" },
  ],
};

const kpiSurface: A2UISurface = {
  components: [
    { id: "root", component: "Column", children: ["heading", "row"] },
    { id: "heading", component: "Text", text: "本周关键指标", variant: "heading" },
    { id: "row", component: "Row", children: ["c1", "c2", "c3"], align: "stretch" },
    { id: "c1", component: "Card", title: "本周预约", child: "c1col" },
    { id: "c1col", component: "Column", children: ["c1n", "c1s"] },
    { id: "c1n", component: "Text", text: "28", variant: "heading" },
    { id: "c1s", component: "Text", text: "较上周 +12%", variant: "caption" },
    { id: "c2", component: "Card", title: "成交率", child: "c2col" },
    { id: "c2col", component: "Column", children: ["c2n", "c2s"] },
    { id: "c2n", component: "Text", text: "22%", variant: "heading" },
    { id: "c2s", component: "Text", text: "较上周 −4pt", variant: "caption" },
    { id: "c3", component: "Card", title: "待跟进", child: "c3col" },
    { id: "c3col", component: "Column", children: ["c3n", "c3s"] },
    { id: "c3n", component: "Text", text: "11", variant: "heading" },
    { id: "c3s", component: "Text", text: "其中 3 条超 3 天", variant: "caption" },
  ],
};

const WEEKS = ["W23", "W24", "W25", "W26", "W27", "W28", "W29", "W30"] as const;
const WEEKLY_BOOKINGS = [18, 21, 24, 19, 27, 29, 31, 28] as const;
const WEEKLY_DEALS = [7, 8, 11, 6, 9, 10, 12, 11] as const;

function weekSeries(name: string, values: readonly number[]) {
  return {
    name,
    points: WEEKS.map((label, index) => ({ label, value: values[index] ?? 0 })),
  };
}

/** One dataModel feeds the heading, three KPIs, a line chart and a stacked bar. */
const boardSurface: A2UISurface = {
  components: [
    { id: "root", component: "Column", children: ["heading", "caption", "kpis", "weekly", "channels", "actions"] },
    { id: "heading", component: "Text", text: { path: "/title" }, variant: "heading" },
    { id: "caption", component: "Text", text: { path: "/caption" }, variant: "caption" },
    { id: "kpis", component: "Row", children: ["k1", "k2", "k3"], align: "stretch" },
    { id: "k1", component: "Card", title: { path: "/kpis/bookings/label" }, child: "k1col" },
    { id: "k1col", component: "Column", children: ["k1n", "k1s"] },
    { id: "k1n", component: "Text", text: { path: "/kpis/bookings/value" }, variant: "heading" },
    { id: "k1s", component: "Text", text: { path: "/kpis/bookings/hint" }, variant: "caption" },
    { id: "k2", component: "Card", title: { path: "/kpis/deals/label" }, child: "k2col" },
    { id: "k2col", component: "Column", children: ["k2n", "k2s"] },
    { id: "k2n", component: "Text", text: { path: "/kpis/deals/value" }, variant: "heading" },
    { id: "k2s", component: "Text", text: { path: "/kpis/deals/hint" }, variant: "caption" },
    { id: "k3", component: "Card", title: { path: "/kpis/overdue/label" }, child: "k3col" },
    { id: "k3col", component: "Column", children: ["k3n", "k3s"] },
    { id: "k3n", component: "Text", text: { path: "/kpis/overdue/value" }, variant: "heading" },
    { id: "k3s", component: "Text", text: { path: "/kpis/overdue/hint" }, variant: "caption" },
    {
      id: "weekly",
      component: "Chart",
      chartType: "line",
      title: "近 8 周预约与成交",
      valueFormat: "plain",
      series: { path: "/weekly" },
    },
    {
      id: "channels",
      component: "Chart",
      chartType: "bar",
      stacked: true,
      title: "渠道贡献（堆叠）",
      valueFormat: "plain",
      series: { path: "/channels" },
    },
    { id: "actions", component: "Row", children: ["export", "detail"], justify: "end" },
    { id: "export", component: "Button", label: "导出看板", variant: "primary" },
    { id: "detail", component: "Button", label: "下钻渠道", variant: "borderless" },
  ],
  dataModel: {
    title: "本季度经营看板",
    caption: "预约仍在涨；W26 成交掉了一截，官网贡献最大，转介绍在抬升。",
    kpis: {
      bookings: { label: "本季预约", value: "196", hint: "较上季 +18%" },
      deals: { label: "本季成交", value: "67", hint: "成交率 34%" },
      overdue: { label: "超期未跟进", value: "14", hint: "其中 5 条 >7 天" },
    },
    weekly: [weekSeries("预约", WEEKLY_BOOKINGS), weekSeries("成交", WEEKLY_DEALS)],
    channels: [
      {
        name: "官网",
        points: [
          { label: "6月", value: 28 },
          { label: "7月", value: 31 },
          { label: "8月", value: 36 },
        ],
      },
      {
        name: "转介绍",
        points: [
          { label: "6月", value: 14 },
          { label: "7月", value: 18 },
          { label: "8月", value: 22 },
        ],
      },
      {
        name: "展会",
        points: [
          { label: "6月", value: 9 },
          { label: "7月", value: 7 },
          { label: "8月", value: 11 },
        ],
      },
    ],
  },
};

const rankSurface: A2UISurface = {
  components: [
    { id: "root", component: "Column", children: ["heading", "caption", "chart"] },
    { id: "heading", component: "Text", text: "本月销售跟进排行", variant: "heading" },
    { id: "caption", component: "Text", text: "长标签走横向条形，避免旋转或截断。", variant: "caption" },
    {
      id: "chart",
      component: "Chart",
      chartType: "hbar",
      unit: "次",
      valueFormat: "plain",
      series: [
        {
          points: [
            { label: "李想 · 华北大客户部", value: 48 },
            { label: "王敏 · 华东渠道组", value: 41 },
            { label: "赵临 · 华南解决方案", value: 36 },
            { label: "陈舟 · 西部战区", value: 29 },
            { label: "周宁 · 中部新建组", value: 17 },
          ],
        },
      ],
    },
  ],
};

const costSurface: A2UISurface = {
  components: [
    { id: "root", component: "Column", children: ["heading", "caption", "pair"] },
    { id: "heading", component: "Text", text: "用量成本与净现金流", variant: "heading" },
    {
      id: "caption",
      component: "Text",
      text: "左：三项成本堆叠（USD）。右：净现金流穿零，基线不是轴底。",
      variant: "caption",
    },
    { id: "pair", component: "Row", children: ["cost", "cash"], align: "stretch" },
    {
      id: "cost",
      component: "Chart",
      chartType: "bar",
      stacked: true,
      title: "用量成本",
      unit: "USD",
      valueFormat: "currency",
      series: [
        {
          name: "推理",
          points: [
            { label: "5月", value: 1820 },
            { label: "6月", value: 2140 },
            { label: "7月", value: 2605 },
            { label: "8月", value: 2480 },
          ],
        },
        {
          name: "嵌入",
          points: [
            { label: "5月", value: 320 },
            { label: "6月", value: 415 },
            { label: "7月", value: 508 },
            { label: "8月", value: 490 },
          ],
        },
        {
          name: "存储",
          points: [
            { label: "5月", value: 96 },
            { label: "6月", value: 104 },
            { label: "7月", value: 131 },
            { label: "8月", value: 148 },
          ],
        },
      ],
    },
    {
      id: "cash",
      component: "Chart",
      chartType: "area",
      title: "净现金流",
      unit: "万元",
      series: [
        {
          points: [
            { label: "4月", value: 42 },
            { label: "5月", value: -18 },
            { label: "6月", value: 7 },
            { label: "7月", value: 63 },
            { label: "8月", value: 88 },
          ],
        },
      ],
    },
  ],
};

const reportSurface: A2UISurface = {
  components: [
    { id: "root", component: "Column", children: ["kpis", "chart"] },
    { id: "kpis", component: "Row", children: ["k1", "k2", "k3"], align: "stretch" },
    { id: "k1", component: "Card", title: "半年预约", child: "k1col" },
    { id: "k1col", component: "Column", children: ["k1n", "k1s"] },
    { id: "k1n", component: "Text", text: "365", variant: "heading" },
    { id: "k1s", component: "Text", text: "3–8 月合计", variant: "caption" },
    { id: "k2", component: "Card", title: "半年成交", child: "k2col" },
    { id: "k2col", component: "Column", children: ["k2n", "k2s"] },
    { id: "k2n", component: "Text", text: "145", variant: "heading" },
    { id: "k2s", component: "Text", text: "成交率 39.7%", variant: "caption" },
    { id: "k3", component: "Card", title: "低谷月", child: "k3col" },
    { id: "k3col", component: "Column", children: ["k3n", "k3s"] },
    { id: "k3n", component: "Text", text: "6月 31%", variant: "heading" },
    { id: "k3s", component: "Text", text: "预约仍在涨，成交掉档", variant: "caption" },
    {
      id: "chart",
      component: "Chart",
      chartType: "bar",
      title: "预约 vs 成交（与上文表格同一组数）",
      valueFormat: "plain",
      series: [series("预约", BOOKINGS), series("成交", DEALS)],
    },
  ],
};

export const MARKDOWN_REPORT_REPLY = `## 3–8 月转化月报

上半年预约从 **42** 爬到 **76**（+81%），成交没有跟上。6 月成交率掉到 **31%**，是这半年最低。

### 分月明细

| 月份 | 预约 | 成交 | 成交率 |
| --- | ---: | ---: | ---: |
| 3月 | 42 | 18 | 43% |
| 4月 | 51 | 22 | 43% |
| 5月 | 58 | 29 | 50% |
| 6月 | 67 | 21 | 31% |
| 7月 | 71 | 24 | 34% |
| 8月 | 76 | 31 | 41% |

合计预约 \`365\`，成交 \`145\`，整体成交率约 $145/365 \\approx 39.7\\%$。

### 建议

1. **先补 6 月掉档**：演示后 48 小时内必须有一次跟进
2. **8 月已回升到 41%**，把同样节奏写进华东 / 华南组的 checklist
3. 线索结构见下图；官网仍是大头，转介绍在抬

> 下面的 KPI 和柱状图与表格是同一组数，方便对照，不另算一套。
`;

export const MARKDOWN_SAMPLE_PROMPT = "展示一段 Markdown + 数学公式样例";
export const MARKDOWN_RICH_PROMPT = "展示一段 Markdown + 数学公式 + 代码 + 图片的样例回复";

export const MARKDOWN_SAMPLE_REPLY = `你好！这是 **ActWeave AAP Chat Demo** 的富文本渲染样例。

## 1. Markdown

- 支持标题、列表、**加粗**、*斜体*
- 链接：[AAP 对接指南](https://github.com/actweave/act-weave)
- 表格：

| 能力 | 说明 |
| --- | --- |
| Markdown | CommonMark 子集 + GFM 表格 |
| 数学公式 | KaTeX（\`$\` / \`$$\`） |
| 代码高亮 | highlight.js |
| 图片 | https 安全 URL |

## 2. 数学公式

行内公式：质能方程 $E = mc^2$，以及贝叶斯定理 $P(A\\mid B)=\\dfrac{P(B\\mid A)P(A)}{P(B)}$。

块级公式：

$$
\\int_{-\\infty}^{\\infty} e^{-x^2}\\,dx = \\sqrt{\\pi}
$$

$$
\\nabla \\times \\mathbf{B} = \\mu_0\\mathbf{J} + \\mu_0\\varepsilon_0\\frac{\\partial\\mathbf{E}}{\\partial t}
$$

## 3. 代码

\`\`\`typescript
import { AgentAccessClient, StaticTokenProvider } from "@actweave/agent-client";

const client = new AgentAccessClient({
  baseUrl: "https://host/api/agent-access/v1",
  tokenProvider: new StaticTokenProvider(token),
});

for await (const { snapshot } of client.followRun(workspaceId, agentId, runId)) {
  renderItems(snapshot.items);
  if (snapshot.run && ["completed", "failed", "cancelled"].includes(String(snapshot.run.status))) {
    break;
  }
}
\`\`\`

\`\`\`bash
# 启动 demo（BFF + UI）
cd demos/aap-chat && npm install && npm run dev
\`\`\`

## 4. 图片

![ActWeave motif](https://images.unsplash.com/photo-1635070041078-e363dbe005cb?w=720&q=80)

> 生产集成请走 **BFF**：Client Secret 只放在服务端，浏览器仅持有短期 Access Token。
`;

export const GENERIC_REPLY =
  "可以。点下面的建议就能看预约表单、经营看板、跟进排行或用量成本；也可以直接打字提问。";

export const MONTHLY_STATEMENT_CSV = [
  "月份,预约,成交",
  "3月,42,18",
  "4月,51,22",
  "5月,58,29",
  "6月,67,21",
  "7月,71,24",
  "8月,76,31",
  "",
].join("\n");

export const INSPECTION_PACK_REPLY = `## 星云便利 · 湖滨店巡检复盘

8 月 15 日巡检总分 **72**。货架缺货和收银台排队是主要扣分项，成交率从上周的 **41%** 掉到 **34%**。

### 问题清单

| 点位 | 等级 | 说明 |
| --- | --- | --- |
| 货架 B3 | P1 | 爆款空位超过 4 小时 |
| 收银台 | P2 | 高峰只有 1 个窗口 |

建议今天先补 \`SKU-1882\` / \`SKU-2041\`，晚高峰加开一个收银窗口。

1. 现场图在气泡里，可点开或下载
2. CSV / JSON 是同一组明细，方便进表格
3. 下面的指标卡可以对一下数字

> 图、文件和看板都是这一次巡检，不是另一套数。
`;

export const INSPECTION_FINDINGS_CSV = [
  "点位,等级,问题,建议",
  "货架B3,P1,爆款空位超过4小时,今日补货",
  "收银台,P2,高峰只有1个窗口,加开窗口",
  "",
].join("\n");

export const INSPECTION_FINDINGS_JSON = `${JSON.stringify(
  {
    store: "星云便利 · 湖滨店",
    date: "2026-08-15",
    score: 72,
    conversion: 0.34,
    findings: [
      { spot: "货架B3", severity: "P1", sku: ["SKU-1882", "SKU-2041"] },
      { spot: "收银台", severity: "P2", waitMinutes: 7 },
    ],
  },
  null,
  2,
)}\n`;

const inspectionPackSurface: A2UISurface = {
  components: [
    { id: "root", component: "Column", children: ["heading", "caption", "kpis", "actions"] },
    { id: "heading", component: "Text", text: "巡检结论", variant: "heading" },
    { id: "caption", component: "Text", text: "先补货，再开第二个收银窗口。", variant: "caption" },
    { id: "kpis", component: "Row", children: ["k1", "k2", "k3"], align: "stretch" },
    { id: "k1", component: "Card", title: "缺货 SKU", child: "k1col" },
    { id: "k1col", component: "Column", children: ["k1n", "k1s"] },
    { id: "k1n", component: "Text", text: "6", variant: "heading" },
    { id: "k1s", component: "Text", text: "其中 2 个爆款", variant: "caption" },
    { id: "k2", component: "Card", title: "排队峰值", child: "k2col" },
    { id: "k2col", component: "Column", children: ["k2n", "k2s"] },
    { id: "k2n", component: "Text", text: "7 分钟", variant: "heading" },
    { id: "k2s", component: "Text", text: "晚高峰单窗口", variant: "caption" },
    { id: "k3", component: "Card", title: "成交率", child: "k3col" },
    { id: "k3col", component: "Column", children: ["k3n", "k3s"] },
    { id: "k3n", component: "Text", text: "34%", variant: "heading" },
    { id: "k3s", component: "Text", text: "较上周 −7pt", variant: "caption" },
    { id: "actions", component: "Row", children: ["restock", "later"], justify: "end" },
    { id: "restock", component: "Button", label: "下发补货单", variant: "primary" },
    { id: "later", component: "Button", label: "改到明天", variant: "borderless" },
  ],
};

export const DEMO_STORIES: readonly DemoStory[] = [
  {
    id: "booking",
    label: "预约一场产品演示",
    prompt: "预约一场产品演示",
    aliases: ["用结构化表单收集：姓名、公司、手机、演示日期", "帮我预约演示"],
    keywords: /表单|登记|姓名|手机|填写|演示日期|预约.*(演示|表单)/i,
    reply: "把联系方式和期望日期填在下面，我来帮你排期。",
    surface: bookingSurface,
  },
  {
    id: "confirm",
    label: "确认刚才的预约",
    prompt: "确认刚才的预约",
    aliases: ["已记下，帮我确认一下预约信息"],
    keywords: /确认|已记下|排期|改期|加入日历/i,
    reply: "已按你刚填的信息排好。时间如需调整，点改期即可。",
    surface: confirmSurface,
  },
  {
    id: "trend",
    label: "近 6 个月预约与成交",
    prompt: "近 6 个月预约与成交",
    aliases: ["用统计图展示近 6 个月预约与成交趋势", "看近半年预约和成交"],
    keywords: /近\s*6\s*个月|近六个月|预约与成交|预约和成交/i,
    reply: "近半年预约在爬升，成交在 6 月掉了一截，之后慢慢回来。",
    surface: trendSurface,
  },
  {
    id: "sources",
    label: "线索来源分布",
    prompt: "线索来源分布",
    aliases: ["用饼图展示线索来源分布", "线索都是从哪来的"],
    keywords: /饼图|来源|线索|分布|占比/i,
    reply: "这是当前线索结构。官网仍是大头，转介绍在涨。",
    surface: sourcesSurface,
  },
  {
    id: "insight",
    label: "结合趋势给个判断",
    prompt: "结合趋势给个判断",
    aliases: ["结合趋势给一个简要判断"],
    keywords: /判断|洞察|摘要|结合趋势|为什么成交/i,
    reply: "数字和动作放在一起：先看 6 月那截下跌，再决定要不要改跟进节奏。",
    surface: insightSurface,
  },
  {
    id: "kpi",
    label: "本周关键指标",
    prompt: "本周关键指标",
    aliases: ["本周关键指标怎么样"],
    keywords: /指标|本周|KPI|关键/i,
    reply: "本周预约不错，成交率回了一点，还有 11 条待跟进。",
    surface: kpiSurface,
  },
  {
    id: "board",
    label: "本季度经营看板",
    prompt: "本季度经营看板",
    aliases: ["看一张经营看板", "综合统计", "复杂图表"],
    keywords: /看板|经营|综合统计|多图|复杂/i,
    reply: "KPI、周趋势和渠道堆叠都读同一份 dataModel。悬停堆叠柱能看到分项和合计。",
    surface: boardSurface,
  },
  {
    id: "rank",
    label: "销售跟进排行",
    prompt: "销售跟进排行",
    aliases: ["跟进排行", "条形图排行"],
    keywords: /排行|排名|条形|hbar/i,
    reply: "按跟进次数排。人名和部门比较长，所以用横向条，不旋转标签。",
    surface: rankSurface,
  },
  {
    id: "cost",
    label: "用量成本与现金流",
    prompt: "用量成本与现金流",
    aliases: ["用量成本", "净现金流", "堆叠成本"],
    keywords: /成本|用量|现金流|堆叠/i,
    reply: "左边是推理 / 嵌入 / 存储的堆叠成本，右边净现金流在 5 月穿零。",
    surface: costSurface,
  },
  {
    id: "report",
    label: "Markdown 月报 + 统计图",
    prompt: "Markdown 月报 + 统计图",
    aliases: ["用 markdown 写月报并配统计图", "分析报告带表格", "markdown 统计表格"],
    keywords: /月报|分析报告|markdown.*统计|统计.*表|带表|表格/i,
    reply: MARKDOWN_REPORT_REPLY,
    surface: reportSurface,
  },
  {
    id: "markdown",
    label: "Markdown 与公式样例",
    prompt: MARKDOWN_SAMPLE_PROMPT,
    aliases: [MARKDOWN_RICH_PROMPT],
    keywords: /markdown|数学|公式|代码高亮|富文本/i,
    reply: MARKDOWN_SAMPLE_REPLY,
  },
  {
    id: "export-csv",
    label: "生成本月对账单",
    prompt: "生成本月对账单",
    aliases: ["导出本月对账单", "导出 CSV 对账单"],
    keywords: /对账单|invoice-2026-08|export-csv/i,
    reply: "这是本月对账单，CSV 可直接下载。",
    attachments: [
      {
        name: "invoice-2026-08.csv",
        mediaType: "text/csv",
        text: MONTHLY_STATEMENT_CSV,
      },
    ],
  },
  {
    id: "site-photos",
    label: "看看这几张现场图",
    prompt: "看看这几张现场图",
    aliases: ["多张图片", "现场照片", "巡检照片"],
    keywords: /现场图|现场照片|巡检照片|多张图片|site-photos/i,
    reply: "这是本次巡检的 4 张现场图，可点开预览或下载。",
    attachments: [
      { name: "storefront.png", mediaType: "image/png", preview: { title: "门头", tone: "#0d9488" } },
      { name: "aisle.png", mediaType: "image/png", preview: { title: "货架", tone: "#d97706" } },
      { name: "counter.png", mediaType: "image/png", preview: { title: "收银台", tone: "#2563eb" } },
      { name: "parking.png", mediaType: "image/png", preview: { title: "停车位", tone: "#475569" } },
    ],
  },
  {
    id: "inspection-pack",
    label: "出一份巡检复盘包",
    prompt: "出一份巡检复盘包",
    aliases: ["图文混合", "图片文件 markdown a2ui", "混合附件"],
    keywords: /复盘包|图文混合|混合附件|巡检复盘|inspection-pack/i,
    reply: INSPECTION_PACK_REPLY,
    surface: inspectionPackSurface,
    attachments: [
      { name: "aisle.png", mediaType: "image/png", preview: { title: "货架", tone: "#d97706" } },
      { name: "counter.png", mediaType: "image/png", preview: { title: "收银台", tone: "#2563eb" } },
      { name: "sku-gaps.csv", mediaType: "text/csv", text: INSPECTION_FINDINGS_CSV },
      { name: "inspection-2026-08-15.json", mediaType: "application/json", text: INSPECTION_FINDINGS_JSON },
    ],
  },
];

export const SUGGESTION_STORIES: readonly DemoStory[] = DEMO_STORIES;

export function pickDemoStory(userText: string): DemoStory | undefined {
  const text = userText.trim();
  if (!text) return undefined;
  const exact = DEMO_STORIES.find(
    (story) => story.prompt === text || story.label === text || story.aliases?.includes(text),
  );
  if (exact) return exact;
  // More specific stories first so "结合趋势" does not fall through to the trend chart.
  const order = [
    "inspection-pack",
    "site-photos",
    "export-csv",
    "report",
    "board",
    "cost",
    "rank",
    "insight",
    "confirm",
    "sources",
    "kpi",
    "trend",
    "booking",
    "markdown",
  ] as const;
  for (const id of order) {
    const story = DEMO_STORIES.find((entry) => entry.id === id);
    if (story?.keywords.test(text)) return story;
  }
  return undefined;
}

export function stampDemoSurface(surface: A2UISurface, id: string): A2UISurface {
  return {
    ...surface,
    surfaceId: `demo:${id}`,
    catalogId: A2UI_CATALOG_ID,
  };
}

export function demoA2UIExtract(surface: A2UISurface, id: string): A2UIExtract {
  const stamped = stampDemoSurface(surface, id);
  return {
    version: A2UI_SURFACE_VERSION,
    catalogId: A2UI_CATALOG_ID,
    surface: stamped,
    rawJson: JSON.stringify(
      {
        type: "a2ui",
        version: A2UI_SURFACE_VERSION,
        catalogId: A2UI_CATALOG_ID,
        surface: stamped,
      },
      null,
      2,
    ),
  };
}
