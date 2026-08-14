import { describe, expect, it } from "vitest";

import { renderSurface } from "./a2ui";
import { DEMO_STORIES, pickDemoStory } from "./demo-stories";

describe("demo stories", () => {
  it("covers the product gallery the empty state advertises", () => {
    expect(DEMO_STORIES.map((story) => story.id)).toEqual([
      "booking",
      "confirm",
      "trend",
      "sources",
      "insight",
      "kpi",
      "board",
      "rank",
      "cost",
      "report",
      "markdown",
    ]);
  });

  it.each(DEMO_STORIES.filter((story) => story.surface).map((story) => [story.id] as const))(
    "renders %s without a placeholder",
    (id) => {
      const story = DEMO_STORIES.find((entry) => entry.id === id);
      const html = renderSurface(story?.surface);
      expect(html).not.toContain("data-a2ui-placeholder");
    },
  );

  it("picks a story from the suggestion prompt and from aliases", () => {
    expect(pickDemoStory("预约一场产品演示")?.id).toBe("booking");
    expect(pickDemoStory("用结构化表单收集：姓名、公司、手机、演示日期")?.id).toBe("booking");
    expect(pickDemoStory("结合趋势给个判断")?.id).toBe("insight");
    expect(pickDemoStory("用统计图展示近 6 个月预约与成交趋势")?.id).toBe("trend");
    expect(pickDemoStory("本季度经营看板")?.id).toBe("board");
    expect(pickDemoStory("复杂图表")?.id).toBe("board");
    expect(pickDemoStory("销售跟进排行")?.id).toBe("rank");
    expect(pickDemoStory("用量成本与现金流")?.id).toBe("cost");
    expect(pickDemoStory("Markdown 月报 + 统计图")?.id).toBe("report");
    expect(pickDemoStory("用 markdown 写月报并配统计图")?.id).toBe("report");
  });

  it("pairs a markdown report table with a matching A2UI chart", () => {
    const story = DEMO_STORIES.find((entry) => entry.id === "report");
    expect(story?.reply).toContain("| 月份 | 预约 | 成交 | 成交率 |");
    expect(story?.reply).toContain("| 6月 | 67 | 21 | 31% |");
    const html = renderSurface(story?.surface);
    expect(html).toContain("365");
    expect(html).toContain("145");
    expect(html).toContain('data-a2ui-chart="bar"');
    expect(html).toContain("预约");
    expect(html).toContain("成交");
  });

  it("binds one dataModel across the board KPIs and both charts", () => {
    const html = renderSurface(DEMO_STORIES.find((story) => story.id === "board")?.surface);
    expect(html).toContain("本季度经营看板");
    expect(html).toContain("196");
    expect(html).toContain("67");
    expect(html).toContain('data-a2ui-chart="line"');
    expect(html).toContain('data-a2ui-chart="bar"');
    expect(html).toContain('data-a2ui-stacked="true"');
    expect(html).toContain("合计");
    for (const week of ["W23", "W26", "W30"]) expect(html).toContain(week);
  });

  it("keeps long rank labels on a horizontal bar chart", () => {
    const html = renderSurface(DEMO_STORIES.find((story) => story.id === "rank")?.surface);
    expect(html).toContain('data-a2ui-chart="hbar"');
    expect(html).toContain("华北大客户部");
    expect(html).toContain("西部战区");
  });

  it("puts stacked cost and a below-zero cashflow chart side by side", () => {
    const html = renderSurface(DEMO_STORIES.find((story) => story.id === "cost")?.surface);
    expect(html).toContain('data-a2ui-stacked="true"');
    expect(html).toContain('data-a2ui-chart="area"');
    expect(html).toContain("推理");
    expect(html).toContain("-18 万元");
  });

  it("puts the fields the booking chip promised on the booking surface", () => {
    const html = renderSurface(DEMO_STORIES.find((story) => story.id === "booking")?.surface);
    for (const label of ["姓名", "公司", "手机", "演示日期", "期望时段"]) expect(html).toContain(label);
    expect(html).toContain("a2ui-choice-chip");
  });

  it("draws six months of bookings and deals on the trend surface", () => {
    const html = renderSurface(DEMO_STORIES.find((story) => story.id === "trend")?.surface);
    expect(html).toContain("预约");
    expect(html).toContain("成交");
    for (const month of ["3月", "4月", "5月", "6月", "7月", "8月"]) expect(html).toContain(month);
  });
});
