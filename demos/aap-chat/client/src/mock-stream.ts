/**
 * Offline demo stream: rich Markdown / math / code / image without AAP credentials.
 */
export type MockChunk =
  | { kind: "user"; text: string }
  | { kind: "assistant_delta"; text: string }
  | { kind: "assistant_done" }
  | { kind: "tool"; name: string; status: "running" | "succeeded" | "failed"; detail?: string }
  | { kind: "status"; text: string };

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

  yield {
    kind: "tool",
    name: hasAttachments ? "demo.render_attachments" : "demo.compose_rich_reply",
    status: "succeeded",
    detail: JSON.stringify(
      { mode: "mock", attachments: names, bytes: reply.length },
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
  yield { kind: "assistant_done" };
}

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}
