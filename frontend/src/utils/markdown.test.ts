import { describe, expect, it } from "vitest";

import { renderMarkdown } from "./markdown";

describe("renderMarkdown", () => {
  it("renders assistant markdown while escaping raw html", () => {
    const html = renderMarkdown("# 结果\n\n- **区域数量**：3\n- 使用 `pageNum`\n\n<script>alert(1)</script>");

    expect(html).toContain("<h1>结果</h1>");
    expect(html).toContain("<li><strong>区域数量</strong>：3</li>");
    expect(html).toContain("<code>pageNum</code>");
    expect(html).toContain("&lt;script&gt;alert(1)&lt;/script&gt;");
    expect(html).not.toContain("<script>");
  });

  it("uses fallback html for empty content", () => {
    expect(renderMarkdown("", "暂无内容")).toBe("<p>暂无内容</p>");
  });
});
