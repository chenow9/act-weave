/**
 * Offline demo stream: rich Markdown / math / code / image / A2UI without AAP credentials.
 */
export type MockChunk =
  | { kind: "user"; text: string }
  | { kind: "assistant_delta"; text: string }
  | {
      kind: "assistant_done";
      /** Optional A2UI surface for real UI render (display-only actions). */
      a2ui?: {
        version?: string;
        catalogId?: string;
        surface: unknown;
      };
    }
  | { kind: "tool"; name: string; status: "running" | "succeeded" | "failed"; detail?: string }
  | { kind: "status"; text: string };

/** Sample form surface matching live natural-conversation e2e shape. */
export const MOCK_A2UI_FORM_SURFACE = {
  type: "form",
  title: "产品演示预约登记",
  fields: [
    {
      type: "text",
      name: "name",
      label: "姓名",
      required: true,
      placeholder: "请输入联系人姓名",
    },
    {
      type: "text",
      name: "company",
      label: "公司",
      required: true,
      placeholder: "请输入公司名称",
    },
    {
      type: "text",
      name: "mobile",
      label: "手机",
      required: true,
      placeholder: "请输入手机号",
    },
    {
      type: "date",
      name: "demoDate",
      label: "演示日期",
      required: true,
      placeholder: "请选择希望演示的日期",
    },
  ],
} as const;

/** Sample chart surfaces for mock A2UI statistics render. */
export const MOCK_A2UI_BAR_CHART = {
  type: "chart",
  chartType: "bar",
  title: "近 6 个月预约量",
  unit: "次",
  labels: ["3月", "4月", "5月", "6月", "7月", "8月"],
  series: [
    { name: "预约", data: [12, 18, 15, 24, 31, 28] },
    { name: "成交", data: [4, 7, 6, 11, 14, 13] },
  ],
} as const;

export const MOCK_A2UI_PIE_CHART = {
  component: "PieChart",
  title: "线索来源分布",
  data: [
    { label: "官网", value: 42 },
    { label: "渠道伙伴", value: 28 },
    { label: "活动", value: 18 },
    { label: "转介绍", value: 12 },
  ],
} as const;

const FORM_HINT =
  /表单|form|登记|预约|填写|字段|a2ui|A2UI|结构化/i;

const CHART_HINT =
  /统计图|图表|chart|柱状|折线|饼图|环图|可视化|趋势|分布|bar\s*chart|line\s*chart|pie/i;

const DEMO_REPLY = `你好！这是 **ActWeave AAP Chat Demo** 的富文本渲染样例（Mock 模式，无需真实 AAP 凭证）。

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

---

你可以继续提问；在配置 \`.env\` 后切换为 **Live AAP** 模式即可对接真实 Agent。
`;

export async function* mockAssistantStream(
  userText: string,
  options?: { attachmentNames?: string[] },
): AsyncGenerator<MockChunk> {
  const names = options?.attachmentNames?.filter(Boolean) || [];
  const hasAttachments = names.length > 0;
  const isAttachmentOnly =
    hasAttachments &&
    (!userText.trim() || userText === "（见附件）" || userText === "请根据附件回答");
  yield { kind: "user", text: userText };
  yield {
    kind: "status",
    text: hasAttachments ? "Mock 模式 · 已收到附件预览" : "Mock 模式 · 本地流式渲染",
  };
  yield {
    kind: "tool",
    name: hasAttachments ? "demo.render_attachments" : "demo.compose_rich_reply",
    status: "running",
    detail: hasAttachments
      ? JSON.stringify(
          {
            note: "Mock 不上传真实文件；气泡内用本地 Object URL 渲染",
            attachments: names,
          },
          null,
          2,
        )
      : "{}",
  };

  await sleep(350);

  const wantsForm = FORM_HINT.test(userText) && !CHART_HINT.test(userText);
  const wantsChart = CHART_HINT.test(userText);
  const wantsPie = /饼图|pie|环图|donut|doughnut|来源|分布/i.test(userText);
  const formReply =
    "可以，直接在下面表单里填写这 4 项信息。我收到后会帮你整理成预约登记内容。\n\n" +
    "（Mock：A2UI 以真实表单控件渲染；提交按钮 display-only，尚未接入 ui-actions。）";
  const chartReply = wantsPie
    ? "下面用 **A2UI 饼图** 展示线索来源占比（Mock 示例数据，纯 SVG 渲染）。"
    : "下面用 **A2UI 柱状图** 展示近 6 个月预约与成交趋势（Mock 示例数据，纯 SVG 渲染）。";

  const reply = isAttachmentOnly
    ? [
        "已收到你发送的附件（**Mock** 模式，未真正上传到 AAP）：",
        "",
        ...names.map((n, i) => `${i + 1}. \`${n}\``),
        "",
        "用户气泡中应能看到图片缩略图或 PDF 卡片。",
        "",
        "配置 `.env` 并启用 `agentAccess.files` 后，Live 模式会走：",
        "",
        "```text",
        "createFile → 预签名 PUT → complete → waitUntilReady → createRun(input_file)",
        "```",
        "",
        "可继续输入文字，或切换 **Live AAP** 做真实多模态对话。",
      ].join("\n")
    : wantsChart
      ? chartReply
      : wantsForm
        ? formReply
        : hasAttachments
          ? [
              `收到 ${names.length} 个附件（${names.map((n) => `\`${n}\``).join("、")}），以及你的文字：`,
              "",
              `> ${userText.trim() || "（无文字）"}`,
              "",
              "下方是常规富文本样例（Mock）：",
              "",
              DEMO_REPLY,
            ].join("\n")
          : DEMO_REPLY;

  const a2uiSurface = wantsChart
    ? wantsPie
      ? MOCK_A2UI_PIE_CHART
      : MOCK_A2UI_BAR_CHART
    : wantsForm
      ? MOCK_A2UI_FORM_SURFACE
      : null;

  yield {
    kind: "tool",
    name: hasAttachments
      ? "demo.render_attachments"
      : wantsChart
        ? "demo.a2ui_chart"
        : wantsForm
          ? "demo.a2ui_form"
          : "demo.compose_rich_reply",
    status: "succeeded",
    detail: JSON.stringify(
      {
        mode: "mock",
        attachments: names,
        bytes: reply.length,
        a2ui: Boolean(a2uiSurface),
        chart: wantsChart,
      },
      null,
      2,
    ),
  };

  // Stream by paragraphs for a nicer typing feel
  const parts = reply.split(/(\n\n+)/);
  let acc = "";
  for (const part of parts) {
    acc += part;
    yield { kind: "assistant_delta", text: acc };
    await sleep(part.trim() ? 80 + Math.min(part.length, 120) : 40);
  }
  if (a2uiSurface) {
    yield {
      kind: "assistant_done",
      a2ui: {
        version: "a2ui-surface.v0",
        catalogId: "standard",
        surface: a2uiSurface,
      },
    };
  } else {
    yield { kind: "assistant_done" };
  }
}

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}
