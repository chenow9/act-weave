/**
 * Renderer tests. The shared fixtures are the baseline: they are the same
 * surfaces the server validates and Console renders, so a disagreement here is a
 * disagreement about the contract rather than about this demo.
 */

import { describe, expect, it } from "vitest";

import { renderA2UICard, renderSurface } from "./index";
import { A2UI_COMPONENT_NAMES, A2UI_LIMITS } from "./generated/catalog.gen";
import { A2UI_FIXTURES, A2UI_FIXTURES_BY_NAME } from "./generated/fixtures.gen";
import { registry } from "./registry";

const PLACEHOLDER = "data-a2ui-placeholder";

function fixture(name: string) {
  const found = A2UI_FIXTURES_BY_NAME[name];
  if (!found) throw new Error(`missing fixture ${name}`);
  return found;
}

function render(name: string): string {
  return renderSurface(fixture(name).surface);
}

function count(html: string, pattern: RegExp): number {
  return html.match(pattern)?.length ?? 0;
}

describe("registry", () => {
  it("has a renderer for every catalog component", () => {
    expect(Object.keys(registry).sort()).toEqual([...A2UI_COMPONENT_NAMES].sort());
  });
});

describe("fixtures", () => {
  const renders = A2UI_FIXTURES.filter((entry) => entry.expect === "renders");

  it.each(renders.map((entry) => [entry.name, entry.title] as const))(
    "renders %s without any placeholder (%s)",
    (name) => {
      const html = render(name);
      expect(html).not.toContain(PLACEHOLDER);
      expect(html.length).toBeGreaterThan(0);
    },
  );

  it("covers every chart type with a drawing", () => {
    for (const chartType of ["bar", "hbar", "line", "area", "pie", "donut"]) {
      const entry = A2UI_FIXTURES.find(
        (candidate) => candidate.expect === "renders" && JSON.stringify(candidate.surface).includes(`"${chartType}"`),
      );
      expect(entry, `no fixture uses chartType ${chartType}`).toBeDefined();
    }
  });
});

describe("dispatch", () => {
  it("matches component names exactly, with no case folding or aliases", () => {
    for (const name of ["column", "COLUMN", "Col", "TextBlock"]) {
      const html = renderSurface({ components: [{ id: "root", component: name, children: [] }] });
      expect(html, name).toContain(PLACEHOLDER);
    }
  });

  it("keeps known siblings when one component is from a newer catalog", () => {
    const html = render("unknown-component");
    expect(count(html, new RegExp(PLACEHOLDER, "g"))).toBe(1);
    expect(html).toContain("Gauge");
    expect(html).toContain("SLA 概览");
    expect(html).toContain("近 30 天可用性");
  });

  it("needs a root component", () => {
    const html = renderSurface({ components: [{ id: "lonely", component: "Divider" }] });
    expect(html).toContain(PLACEHOLDER);
    expect(html).toContain("root");
  });

  it("reports an empty surface instead of throwing", () => {
    expect(renderSurface(null)).toContain(PLACEHOLDER);
    expect(renderSurface({})).toContain(PLACEHOLDER);
    expect(renderSurface({ components: [] })).toContain(PLACEHOLDER);
  });
});

describe("defensive walking", () => {
  it("places a placeholder for a dangling child reference", () => {
    const html = renderSurface({
      components: [
        { id: "root", component: "Column", children: ["ghost", "here"] },
        { id: "here", component: "Text", text: "在" },
      ],
    });
    expect(html).toContain("ghost");
    expect(html).toContain("在");
    expect(count(html, new RegExp(PLACEHOLDER, "g"))).toBe(1);
  });

  it("stops at a cycle rather than recursing forever", () => {
    const html = renderSurface({
      components: [
        { id: "root", component: "Column", children: ["loop"] },
        { id: "loop", component: "Column", children: ["root"] },
      ],
    });
    expect(html).toContain("循环引用");
  });

  it("stops below the depth limit", () => {
    const depth = A2UI_LIMITS.maxTreeDepth + 4;
    const components = Array.from({ length: depth }, (_unused, index) => ({
      id: index === 0 ? "root" : `n${index}`,
      component: "Column",
      children: index === depth - 1 ? [] : [`n${index + 1}`],
    }));
    const html = renderSurface({ components });
    expect(html).toContain("嵌套超过");
  });
});

describe("layout", () => {
  it("turns catalog align and justify values into classes", () => {
    const html = renderSurface({
      components: [{ id: "root", component: "Row", align: "center", justify: "spaceBetween", children: [] }],
    });
    expect(html).toContain("a2ui-align-center");
    expect(html).toContain("a2ui-justify-spaceBetween");
  });

  it("ignores an align value outside the catalog rather than naming a class after it", () => {
    const html = renderSurface({
      components: [{ id: "root", component: "Row", align: "baseline", justify: "evenly", children: [] }],
    });
    expect(html).not.toContain("a2ui-align-");
    expect(html).not.toContain("a2ui-justify-");
  });
});

describe("data binding", () => {
  it("resolves a bound title and shares one dataset between charts", () => {
    const html = render("chart-binding");
    // /caption reaches three places: the heading Text, the first chart's title,
    // and that chart's aria-label.
    expect(count(html, /两周请求量/g)).toBe(3);
    // Production week 1 is 48200 in compact form, once per chart.
    expect(count(html, /48\.2k/g)).toBeGreaterThanOrEqual(2);
  });
});

describe("charts", () => {
  it.each([
    ["chart-bar", "bar"],
    ["chart-hbar", "hbar"],
    ["chart-line", "line"],
    ["chart-area", "area"],
    ["chart-pie", "pie"],
    ["chart-donut", "donut"],
  ])("draws %s as an svg tagged %s", (name, chartType) => {
    const html = render(name);
    expect(html).toContain(`data-a2ui-chart="${chartType}"`);
    expect(html).toContain("<svg");
    expect(html).toMatchSnapshot();
  });

  it("stacks segments only when asked, one per series and category", () => {
    const html = render("chart-stacked");
    expect(html).toContain('data-a2ui-stacked="true"');
    // 3 series × 3 months.
    expect(count(html, /<rect /g)).toBe(9);
  });

  it("groups rather than stacks by default", () => {
    const html = render("chart-bar");
    expect(html).not.toContain("data-a2ui-stacked");
    expect(count(html, /<rect /g)).toBe(4);
  });

  it("names every series in the legend of a multi-series chart", () => {
    const html = render("chart-multi-series");
    for (const name of ["生产", "预发", "开发"]) expect(html).toContain(name);
    expect(html).toContain("a2ui-chart-legend-series");
  });

  it("omits a zero slice from a donut without leaving a gap", () => {
    const html = render("chart-donut");
    // 4 points, one of them zero.
    expect(count(html, /<path /g)).toBe(3);
    expect(html).toContain("a2ui-chart-center");
  });

  it("keeps the percent symbol and drops a duplicate unit", () => {
    const html = render("chart-line");
    expect(html).toContain("96.1%");
  });

  it("formats currency with grouping and the unit", () => {
    const html = render("chart-stacked");
    expect(html).toContain("2,605 USD");
  });

  it("reports a chart whose series resolve to nothing", () => {
    const html = renderSurface({
      components: [{ id: "root", component: "Chart", chartType: "bar", series: { path: "/absent" } }],
      dataModel: {},
    });
    expect(html).toContain("统计图无有效数据");
  });

  it("rejects a chartType this catalog does not define", () => {
    const html = renderSurface({
      components: [{ id: "root", component: "Chart", chartType: "radar", series: [{ points: [{ label: "a", value: 1 }] }] }],
    });
    expect(html).toContain(PLACEHOLDER);
  });
});

describe("form components", () => {
  const html = render("form");

  it("renders every input type from the catalog variants", () => {
    expect(html).toContain('type="text"');
    expect(html).toContain("<textarea");
    expect(html).toContain("<select");
    expect(html).toContain('type="checkbox"');
    expect(html).toContain('type="datetime-local"');
    expect(html).toContain("<hr");
  });

  it("keeps every button disabled, because the catalog defines no action", () => {
    const buttons = html.match(/<button[^>]*>/g) ?? [];
    expect(buttons.length).toBe(2);
    for (const button of buttons) expect(button).toContain("disabled");
  });

  it("marks a required field and preselects a bound choice", () => {
    expect(html).toContain("a2ui-req");
    expect(html).toContain('value="api" selected');
  });
});

describe("escaping", () => {
  it("escapes text instead of interpreting markup", () => {
    const html = renderSurface({
      components: [{ id: "root", component: "Text", text: `<img src=x onerror="alert(1)">` }],
    });
    expect(html).not.toContain("<img");
    expect(html).toContain("&lt;img");
  });

  it("does not interpret Markdown in Text", () => {
    const html = renderSurface({
      components: [{ id: "root", component: "Text", text: "**粗体** 与 [链接](https://example.com)" }],
    });
    expect(html).toContain("**粗体**");
    expect(html).not.toContain("<strong");
    expect(html).not.toContain("<a ");
  });

  it("escapes an unknown component name in its placeholder", () => {
    const html = renderSurface({
      components: [{ id: "root", component: "<script>alert(1)</script>" }],
    });
    expect(html).not.toContain("<script");
    expect(html).toContain("&lt;script");
  });

  it("escapes the card header meta", () => {
    const card = renderA2UICard({
      version: `<img src=x>`,
      catalogId: "https://catalog.actweave.dev/standard/v1/catalog.json",
      surface: fixture("chart-bar").surface,
      rawJson: `{"note":"<script>"}`,
    });
    expect(card).not.toContain("<img");
    expect(card).not.toContain("<script>");
    expect(card).toContain("display-only");
  });
});
