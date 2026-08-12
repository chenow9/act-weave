/**
 * Offline demo stream: rich Markdown / math / code / image / A2UI without AAP credentials.
 */
import { A2UI_CATALOG_ID, A2UI_SURFACE_VERSION } from "./a2ui/generated/catalog.gen";
import { A2UI_FIXTURES_BY_NAME } from "./a2ui/generated/fixtures.gen";

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

/**
 * Mock surfaces are the shared fixtures, so the offline demo shows exactly the
 * baselines the renderer is tested against and the server accepts.
 */
export const MOCK_A2UI_FIXTURES = A2UI_FIXTURES_BY_NAME;

const FORM_HINT = /表单|form|登记|预约|填写|字段|a2ui|A2UI|结构化/i;

const CHART_HINT =
  /统计图|图表|chart|柱状|折线|饼图|环图|面积|条形|堆叠|绑定|可视化|趋势|分布|占比|bar\s*chart|line\s*chart|pie/i;

/**
 * Picks the fixture whose shape the request is asking about. Order matters: the
 * more specific words are tested first.
 */
function pickChartFixture(userText: string): string {
  const rules: Array<[RegExp, string]> = [
    [/绑定|binding|dataModel|共享数据/i, "chart-binding"],
    [/堆叠|stacked/i, "chart-stacked"],
    [/多系列|多条|对比|multi/i, "chart-multi-series"],
    [/环图|donut|doughnut/i, "chart-donut"],
    [/饼图|pie|占比|来源|分布/i, "chart-pie"],
    [/面积|area/i, "chart-area"],
    [/折线|line|趋势|成功率/i, "chart-line"],
    [/条形|横向|hbar/i, "chart-hbar"],
  ];
  return rules.find(([pattern]) => pattern.test(userText))?.[1] ?? "chart-bar";
}

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
  const fixture = wantsChart
    ? MOCK_A2UI_FIXTURES[pickChartFixture(userText)]
    : wantsForm
      ? MOCK_A2UI_FIXTURES["form"]
      : undefined;
  const formReply =
    "可以，直接在下面的表单里填写。我收到后会帮你整理成工单内容。\n\n" +
    "（Mock：A2UI 以真实表单控件渲染；按钮为展示态，catalog v1 不定义动作。）";
  const chartReply = fixture
    ? `下面用 **A2UI 图表** 渲染基线样例「${fixture.title}」（纯 SVG，视觉由客户端决定）。`
    : "";

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

  // The server stamps identity onto a validated surface, so the mock does too.
  const a2uiSurface = fixture
    ? { surfaceId: `mock:${fixture.name}`, catalogId: A2UI_CATALOG_ID, ...fixture.surface }
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
        version: A2UI_SURFACE_VERSION,
        catalogId: A2UI_CATALOG_ID,
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
