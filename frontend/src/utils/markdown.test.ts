import { describe, expect, it } from "vitest";

import { renderMarkdown } from "./markdown";

describe("renderMarkdown", () => {
  it("renders headings, lists, emphasis, code and fences while blocking raw html", () => {
    const html = renderMarkdown(
      "# 结果\n\n- **区域数量**：3\n- 使用 `pageNum`\n\n```js\nconst x = 1;\n```\n\n<script>alert(1)</script>",
    );

    expect(html).toContain("<h1>结果</h1>");
    expect(html).toContain("<li>");
    expect(html).toContain("<strong>区域数量</strong>");
    expect(html).toContain("<code>pageNum</code>");
    expect(html).toContain("<pre>");
    expect(html).not.toContain("<script>");
  });

  it("strips images and dangerous urls", () => {
    const html = renderMarkdown(
      "![x](https://evil.example/a.png)\n\n[ok](https://example.com)\n\n[bad](javascript:alert(1))\n\n[data](data:text/html;base64,AAA)",
    );
    expect(html).not.toContain("<img");
    expect(html).toContain('href="https://example.com"');
    expect(html).toContain('rel="noopener noreferrer"');
    expect(html).not.toContain('href="javascript:');
    expect(html).not.toContain('href="data:');
  });

  it("uses fallback html for empty content", () => {
    expect(renderMarkdown("", "暂无内容")).toBe("<p>暂无内容</p>");
  });
});
