/**
 * Console renderer tests, over the same fixtures the server validates and the
 * demo renders. A disagreement here is a disagreement about the contract, not
 * about Console.
 */

import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";

import A2UISurface from "./A2UISurface.vue";
import { A2UI_CATALOG_ID, A2UI_COMPONENT_NAMES, A2UI_LIMITS } from "./generated/catalog.gen";
import { A2UI_FIXTURES, A2UI_FIXTURES_BY_NAME } from "./generated/fixtures.gen";
import { registry } from "./registry";
import { createTestI18n } from "../../test-utils/i18n";

function render(surface: unknown) {
  return mount(A2UISurface, {
    props: { surface, uid: "test" },
    global: { plugins: [createTestI18n("zh-CN")] },
  });
}

function fixture(name: string) {
  const found = A2UI_FIXTURES_BY_NAME[name];
  if (!found) throw new Error(`missing fixture ${name}`);
  return found;
}

function renderFixture(name: string) {
  return render(fixture(name).surface);
}

const PLACEHOLDER = "[data-a2ui-placeholder]";

describe("registry", () => {
  it("has a component for every catalog component", () => {
    expect(Object.keys(registry).sort()).toEqual([...A2UI_COMPONENT_NAMES].sort());
  });
});

describe("fixtures", () => {
  const renders = A2UI_FIXTURES.filter((entry) => entry.expect === "renders");

  it.each(renders.map((entry) => [entry.name, entry.title] as const))(
    "renders %s without any placeholder (%s)",
    (name) => {
      const wrapper = renderFixture(name);
      expect(wrapper.findAll(PLACEHOLDER)).toHaveLength(0);
      expect(wrapper.find("[data-a2ui-surface]").exists()).toBe(true);
    },
  );

  it("marks every surface display-only", () => {
    expect(renderFixture("form").text()).toContain("仅展示");
  });
});

describe("dispatch", () => {
  it("matches component names exactly, with no case folding or aliases", () => {
    for (const name of ["column", "COLUMN", "Col", "TextBlock"]) {
      const wrapper = render({ components: [{ id: "root", component: name, children: [] }] });
      expect(wrapper.findAll(PLACEHOLDER).length, name).toBeGreaterThan(0);
    }
  });

  it("keeps known siblings when one component is from a newer catalog", () => {
    const wrapper = renderFixture("unknown-component");
    expect(wrapper.findAll(PLACEHOLDER)).toHaveLength(1);
    expect(wrapper.text()).toContain("Gauge");
    expect(wrapper.text()).toContain("SLA 概览");
    expect(wrapper.text()).toContain("近 30 天可用性");
  });

  it("refuses a surface written against another catalog", () => {
    const wrapper = render({
      catalogId: "https://catalog.example.com/other/v9/catalog.json",
      components: [{ id: "root", component: "Text", text: "should not render" }],
    });
    expect(wrapper.findAll(PLACEHOLDER)).toHaveLength(1);
    expect(wrapper.text()).not.toContain("should not render");
  });

  it("accepts a surface stamped with our own catalogId", () => {
    const wrapper = render({
      surfaceId: "srf_1",
      catalogId: A2UI_CATALOG_ID,
      components: [{ id: "root", component: "Text", text: "stamped" }],
    });
    expect(wrapper.findAll(PLACEHOLDER)).toHaveLength(0);
    expect(wrapper.text()).toContain("stamped");
  });

  it("needs a root component", () => {
    const wrapper = render({ components: [{ id: "lonely", component: "Divider" }] });
    expect(wrapper.find(PLACEHOLDER).text()).toContain("root");
  });

  it("reports an empty surface instead of throwing", () => {
    for (const empty of [null, undefined, {}, { components: [] }, { components: "nope" }, []]) {
      expect(render(empty).findAll(PLACEHOLDER).length).toBeGreaterThan(0);
    }
  });
});

describe("defensive walking", () => {
  it("places a placeholder for a dangling child reference", () => {
    const wrapper = render({
      components: [
        { id: "root", component: "Column", children: ["ghost", "here"] },
        { id: "here", component: "Text", text: "在" },
      ],
    });
    expect(wrapper.findAll(PLACEHOLDER)).toHaveLength(1);
    expect(wrapper.text()).toContain("ghost");
    expect(wrapper.text()).toContain("在");
  });

  it("stops at a cycle rather than recursing forever", () => {
    const wrapper = render({
      components: [
        { id: "root", component: "Column", children: ["loop"] },
        { id: "loop", component: "Column", children: ["root"] },
      ],
    });
    expect(wrapper.text()).toContain("循环引用");
  });

  it("stops below the depth limit", () => {
    const depth = A2UI_LIMITS.maxTreeDepth + 4;
    const components = Array.from({ length: depth }, (_unused, index) => ({
      id: index === 0 ? "root" : `n${index}`,
      component: "Column",
      children: index === depth - 1 ? [] : [`n${index + 1}`],
    }));
    expect(render({ components }).text()).toContain("嵌套超过");
  });

  it("reports a container whose children are not ids", () => {
    const wrapper = render({ components: [{ id: "root", component: "Column", children: [42, null] }] });
    expect(wrapper.findAll(PLACEHOLDER)).toHaveLength(1);
  });

  it("reports a Card without a child", () => {
    const wrapper = render({ components: [{ id: "root", component: "Card", title: "空卡片" }] });
    expect(wrapper.text()).toContain("空卡片");
    expect(wrapper.findAll(PLACEHOLDER)).toHaveLength(1);
  });
});

describe("layout", () => {
  it("turns catalog align and justify values into classes", () => {
    const wrapper = render({
      components: [
        { id: "root", component: "Row", align: "center", justify: "spaceBetween", children: ["t"] },
        { id: "t", component: "Text", text: "x" },
      ],
    });
    const row = wrapper.find(".a2ui-row");
    expect(row.classes()).toContain("a2ui-align-center");
    expect(row.classes()).toContain("a2ui-justify-spaceBetween");
  });

  it("ignores values outside the catalog rather than naming a class after them", () => {
    const wrapper = render({
      components: [
        { id: "root", component: "Row", align: "baseline", justify: "evenly", children: ["t"] },
        { id: "t", component: "Text", text: "x" },
      ],
    });
    const classes = wrapper.find(".a2ui-row").classes().join(" ");
    expect(classes).not.toContain("a2ui-align-");
    expect(classes).not.toContain("a2ui-justify-");
  });
});

describe("data binding", () => {
  it("resolves a bound heading, title and series from one dataModel", async () => {
    const wrapper = renderFixture("chart-binding");
    // The heading Text and the first chart's title both bind to /caption.
    expect(wrapper.text().match(/两周请求量/g)?.length).toBe(2);
    // Production week 1 is 48200, which the bound series reports compact.
    await wrapper.findAll(".a2ui-chart-hit")[0]?.trigger("mousemove");
    expect(wrapper.find(".a2ui-chart-tip").text()).toContain("48.2k");
  });

  it("renders nothing for a binding that does not resolve", () => {
    const wrapper = render({
      components: [{ id: "root", component: "Text", text: { path: "/nope" } }],
      dataModel: {},
    });
    expect(wrapper.find(".a2ui-text").text()).toBe("");
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
    const wrapper = renderFixture(name);
    const figure = wrapper.find(`[data-a2ui-chart="${chartType}"]`);
    expect(figure.exists()).toBe(true);
    expect(figure.find("svg").exists()).toBe(true);
    expect(wrapper.html()).toMatchSnapshot();
  });

  it("stacks segments only when asked, one per series and category", () => {
    const wrapper = renderFixture("chart-stacked");
    expect(wrapper.find('[data-a2ui-stacked="true"]').exists()).toBe(true);
    // 3 series × 3 months.
    expect(wrapper.findAll(".a2ui-chart-bar")).toHaveLength(9);
  });

  it("groups rather than stacks by default", () => {
    const wrapper = renderFixture("chart-bar");
    expect(wrapper.find("[data-a2ui-stacked]").exists()).toBe(false);
    expect(wrapper.findAll(".a2ui-chart-bar")).toHaveLength(4);
  });

  it("names every series in the legend of a multi-series chart", () => {
    const wrapper = renderFixture("chart-multi-series");
    const legend = wrapper.find(".a2ui-chart-legend-series");
    expect(legend.exists()).toBe(true);
    for (const name of ["生产", "预发", "开发"]) expect(legend.text()).toContain(name);
  });

  it("omits a zero slice from a donut and labels the total", () => {
    const wrapper = renderFixture("chart-donut");
    // 4 points, one of them zero.
    expect(wrapper.findAll(".a2ui-chart-slice")).toHaveLength(3);
    expect(wrapper.find(".a2ui-chart-center").exists()).toBe(true);
  });

  it("shows each slice's share in a pie legend", () => {
    const legend = renderFixture("chart-pie").find(".a2ui-chart-legend");
    expect(legend.exists()).toBe(true);
    expect(legend.text()).toMatch(/%/);
  });

  it("keeps the percent symbol and drops a duplicate unit", async () => {
    const wrapper = renderFixture("chart-line");
    // The fourth day of the fixture reads 96.1.
    await wrapper.findAll(".a2ui-chart-hit")[3]?.trigger("mousemove");
    expect(wrapper.find(".a2ui-chart-tip").text()).toContain("96.1%");
  });

  it("formats currency with grouping and the unit", async () => {
    const wrapper = renderFixture("chart-stacked");
    // 推理 peaks at 2605 in the third month.
    await wrapper.findAll(".a2ui-chart-hit")[2]?.trigger("mousemove");
    expect(wrapper.find(".a2ui-chart-tip").text()).toContain("2,605 USD");
  });

  it("reports a chart whose series resolve to nothing", () => {
    const wrapper = render({
      components: [{ id: "root", component: "Chart", chartType: "bar", series: { path: "/absent" } }],
      dataModel: {},
    });
    expect(wrapper.text()).toContain("统计图无有效数据");
    expect(wrapper.find("svg").exists()).toBe(false);
  });

  it("rejects a chartType this catalog does not define", () => {
    const wrapper = render({
      components: [
        { id: "root", component: "Chart", chartType: "radar", series: [{ points: [{ label: "a", value: 1 }] }] },
      ],
    });
    expect(wrapper.findAll(PLACEHOLDER)).toHaveLength(1);
  });

  it("truncates series and points to the contract limits", () => {
    const series = Array.from({ length: A2UI_LIMITS.maxChartSeries + 3 }, (_unused, index) => ({
      name: `s${index}`,
      points: Array.from({ length: A2UI_LIMITS.maxChartPoints + 5 }, (_p, point) => ({
        label: `p${point}`,
        value: point + 1,
      })),
    }));
    const wrapper = render({ components: [{ id: "root", component: "Chart", chartType: "bar", series }] });
    expect(wrapper.findAll(".a2ui-chart-legend-series li")).toHaveLength(A2UI_LIMITS.maxChartSeries);
    expect(wrapper.findAll(".a2ui-chart-bar")).toHaveLength(A2UI_LIMITS.maxChartSeries * A2UI_LIMITS.maxChartPoints);
  });
});

describe("hover detail", () => {
  async function hover(wrapper: ReturnType<typeof render>, index: number) {
    const targets = wrapper.findAll(".a2ui-chart-hit");
    await targets[index]?.trigger("mousemove");
    return wrapper.find(".a2ui-chart-tip");
  }

  it("names the hovered category and every series reading there", async () => {
    const wrapper = renderFixture("chart-stacked");
    expect(wrapper.findAll(".a2ui-chart-hit")).toHaveLength(3);
    expect(wrapper.find(".a2ui-chart-tip").exists()).toBe(false);

    const tip = await hover(wrapper, 0);
    expect(tip.text()).toContain("5月");
    for (const series of ["推理", "嵌入", "存储"]) expect(tip.text()).toContain(series);
    // 1820 + 320 + 96, a number the stack draws but never labels.
    expect(tip.text()).toContain("合计");
    expect(tip.text()).toContain("2,236 USD");
  });

  it("follows the cursor to another category and clears when it leaves", async () => {
    const wrapper = renderFixture("chart-bar");
    expect((await hover(wrapper, 0)).text()).toContain("Q1");
    expect((await hover(wrapper, 3)).text()).toContain("Q4");

    await wrapper.find(".a2ui-chart-body").trigger("mouseleave");
    expect(wrapper.find(".a2ui-chart-tip").exists()).toBe(false);
  });

  it("emphasises the hovered category and lets the rest recede", async () => {
    const wrapper = renderFixture("chart-bar");
    await hover(wrapper, 1);
    expect(wrapper.find(".a2ui-chart-body").classes()).toContain("is-hovering");
    const active = wrapper.findAll(".a2ui-chart-bar").filter((bar) => bar.classes().includes("is-active"));
    expect(active).toHaveLength(1);
    expect(wrapper.find(".a2ui-chart-band").exists()).toBe(true);
  });

  it("stands a guide line at the hovered point of a line chart", async () => {
    const wrapper = renderFixture("chart-line");
    await hover(wrapper, 2);
    const guide = wrapper.find(".a2ui-chart-guide");
    expect(guide.exists()).toBe(true);
    expect(guide.attributes("x1")).toBe(guide.attributes("x2"));
    const dots = wrapper.findAll(".a2ui-chart-dot");
    expect(dots.filter((dot) => dot.classes().includes("is-active"))).toHaveLength(1);
  });

  it("reports a slice's share when its own shape is hovered", async () => {
    const wrapper = renderFixture("chart-pie");
    const tip = await hover(wrapper, 0);
    expect(tip.text()).toMatch(/%/);
    // A wedge is already a target, so nothing is drawn behind it.
    expect(wrapper.find(".a2ui-chart-band").exists()).toBe(false);
  });

  /**
   * The native SVG tooltip is what this replaced. Leaving one behind would pop a
   * second, differently styled detail over the first after a delay.
   */
  it("draws no native svg tooltip anywhere", () => {
    for (const name of ["chart-bar", "chart-hbar", "chart-line", "chart-area", "chart-pie", "chart-donut"]) {
      expect(renderFixture(name).findAll("title"), name).toHaveLength(0);
    }
  });
});

describe("form components", () => {
  it("renders every input type from the catalog variants", () => {
    const wrapper = renderFixture("form");
    expect(wrapper.find('input[type="text"]').exists()).toBe(true);
    expect(wrapper.find("textarea").exists()).toBe(true);
    expect(wrapper.find("select").exists()).toBe(true);
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(true);
    expect(wrapper.find('input[type="datetime-local"]').exists()).toBe(true);
    expect(wrapper.find("hr").exists()).toBe(true);
  });

  it("keeps every button disabled, because the catalog defines no action", () => {
    const buttons = renderFixture("form").findAll("button");
    expect(buttons).toHaveLength(2);
    for (const button of buttons) expect(button.attributes("disabled")).toBeDefined();
  });

  it("marks a required field and preselects a bound choice", () => {
    const wrapper = renderFixture("form");
    expect(wrapper.find(".a2ui-req").exists()).toBe(true);
    const selected = wrapper.findAll("option").filter((option) => option.element.selected);
    expect(selected.map((option) => option.attributes("value"))).toContain("api");
  });

  it("gives controls ids unique to their surface", () => {
    const first = render(fixture("form").surface);
    const label = first.find("label.a2ui-label");
    expect(label.attributes("for")).toContain("test");
    const other = mount(A2UISurface, {
      props: { surface: fixture("form").surface, uid: "second" },
      global: { plugins: [createTestI18n("zh-CN")] },
    });
    expect(other.find("label.a2ui-label").attributes("for")).not.toBe(label.attributes("for"));
  });
});

describe("escaping", () => {
  it("interpolates text instead of interpreting markup", () => {
    const wrapper = render({
      components: [{ id: "root", component: "Text", text: `<img src=x onerror="alert(1)">` }],
    });
    expect(wrapper.find("img").exists()).toBe(false);
    expect(wrapper.find(".a2ui-text").text()).toBe(`<img src=x onerror="alert(1)">`);
  });

  it("does not interpret Markdown in Text", () => {
    const wrapper = render({
      components: [{ id: "root", component: "Text", text: "**粗体** 与 [链接](https://example.com)" }],
    });
    expect(wrapper.find("strong").exists()).toBe(false);
    expect(wrapper.find("a").exists()).toBe(false);
    expect(wrapper.text()).toContain("**粗体**");
  });

  it("escapes an unknown component name in its placeholder", () => {
    const wrapper = render({ components: [{ id: "root", component: "<script>alert(1)</script>" }] });
    expect(wrapper.html()).not.toContain("<script");
    expect(wrapper.find(PLACEHOLDER).text()).toContain("script");
  });
});

describe("locales", () => {
  it("renders placeholders in English too", () => {
    const wrapper = mount(A2UISurface, {
      props: { surface: fixture("unknown-component").surface, uid: "en" },
      global: { plugins: [createTestI18n("en")] },
    });
    expect(wrapper.find(PLACEHOLDER).text()).toContain("does not support");
    expect(wrapper.text()).toContain("Display only");
  });
});
