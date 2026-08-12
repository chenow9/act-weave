import { describe, expect, it } from "vitest";

import {
  A2UI_CATALOG_ID,
  A2UI_CHART_TYPES,
  A2UI_COMPONENT_NAMES,
  A2UI_LIMITS,
  A2UI_ROOT_ID,
  A2UI_SURFACE_VERSION,
  findA2UIPart,
  isA2UIComponentName,
  isA2UIDataBinding,
  isKnownA2UICatalog,
  iterCharts,
  joinTextParts,
  resolveBinding,
  type A2UISurface,
  type ProtocolItem,
} from "../src/index.js";
import { A2UI_FIXTURES, A2UI_FIXTURES_BY_NAME } from "./generated/a2ui-fixtures.gen.js";

/** A fixture surface as it reaches a client: identity stamped by the platform. */
function delivered(name: string): A2UISurface {
  const fixture = A2UI_FIXTURES_BY_NAME[name];
  if (!fixture) throw new Error(`unknown fixture: ${name}`);
  return { ...fixture.surface, surfaceId: "srf_1", catalogId: A2UI_CATALOG_ID };
}

describe("binding resolution", () => {
  it("tells a pointer from a literal", () => {
    expect(isA2UIDataBinding({ path: "/revenue" })).toBe(true);
    expect(isA2UIDataBinding({ path: "revenue" })).toBe(false);
    expect(isA2UIDataBinding({ path: 1 })).toBe(false);
    expect(isA2UIDataBinding("/revenue")).toBe(false);
    expect(isA2UIDataBinding([{ path: "/revenue" }])).toBe(false);
    expect(isA2UIDataBinding(null)).toBe(false);
  });

  it("follows a pointer into the data model and passes literals through", () => {
    const surface: A2UISurface = {
      components: [{ id: A2UI_ROOT_ID, component: "Text", text: { path: "/copy/title" } }],
      dataModel: { copy: { title: "两周请求量" }, rows: [{ label: "华东" }], "a/b": 1, "c~d": 2 },
    };

    expect(resolveBinding(surface, { path: "/copy/title" })).toBe("两周请求量");
    expect(resolveBinding(surface, "两周请求量")).toBe("两周请求量");
    expect(resolveBinding(surface, 42)).toBe(42);
    expect(resolveBinding(surface, { path: "/rows/0/label" })).toBe("华东");
    // RFC 6901 escapes: ~1 is "/" and ~0 is "~".
    expect(resolveBinding(surface, { path: "/a~1b" })).toBe(1);
    expect(resolveBinding(surface, { path: "/c~0d" })).toBe(2);
  });

  it("returns undefined where a pointer reaches nothing", () => {
    const surface: A2UISurface = { components: [], dataModel: { rows: [1] } };
    for (const path of ["/missing", "/rows/1", "/rows/-1", "/rows/x", "/rows/0/deeper"]) {
      expect(resolveBinding(surface, { path })).toBeUndefined();
    }
  });

  it("resolves nothing when the surface carries no data model", () => {
    expect(resolveBinding({ components: [] }, { path: "/title" })).toBeUndefined();
  });
});

describe("iterCharts", () => {
  it("finds every chart of every shape across the shared fixtures", () => {
    const shapes = new Set<string>();
    for (const fixture of A2UI_FIXTURES) {
      for (const chart of iterCharts(fixture.surface)) {
        shapes.add(chart.chartType);
      }
    }
    // The fixtures are the platform's own baselines, so a shape missing here
    // means the SDK cannot read a chart the platform emits.
    expect([...shapes].sort()).toEqual([...A2UI_CHART_TYPES].sort());
  });

  it("reads a chart's semantic members and leaves presentation out", () => {
    const [chart, ...rest] = iterCharts(delivered("chart-bar"));
    expect(rest).toHaveLength(0);
    expect(chart?.chartType).toBe("bar");
    expect(chart?.unit).toBe("万元");
    expect(chart?.valueFormat).toBe("compact");
    expect(chart?.stacked).toBe(false);
    expect(chart?.series).toHaveLength(1);
    expect(chart?.series[0]?.points.map((point) => point.label)).toEqual(["Q1", "Q2", "Q3", "Q4"]);
    // Values stay numbers: formatting is the client's, and the unit is separate.
    for (const point of chart?.series[0]?.points ?? []) {
      expect(typeof point.value).toBe("number");
    }
    // Nothing visual is inherited from the surface.
    for (const key of ["color", "colors", "palette", "width", "height", "axis"]) {
      expect(chart?.node[key]).toBeUndefined();
    }
  });

  it("resolves bound series so a shared dataset feeds several charts", () => {
    const charts = iterCharts(delivered("chart-binding"));
    expect(charts.length).toBeGreaterThan(1);
    for (const chart of charts) {
      expect(chart.series.length).toBeGreaterThan(0);
      for (const series of chart.series) {
        expect(series.points.length).toBeGreaterThan(0);
      }
    }
    // A bound title resolves too, so a client never renders "[object Object]".
    expect(charts.some((chart) => typeof chart.title === "string")).toBe(true);
    for (const chart of charts) {
      expect(typeof chart.title === "string" || chart.title === undefined).toBe(true);
    }
  });

  it("keeps multi-series order and names", () => {
    const [chart] = iterCharts(delivered("chart-multi-series"));
    expect(chart?.series.length).toBeGreaterThan(1);
    expect(chart?.series.every((series) => typeof series.name === "string")).toBe(true);
  });

  it("reports stacked bars as stacked", () => {
    const [chart] = iterCharts(delivered("chart-stacked"));
    expect(chart?.stacked).toBe(true);
  });

  it("drops malformed points and the series they empty", () => {
    const surface: A2UISurface = {
      components: [
        {
          id: A2UI_ROOT_ID,
          component: "Chart",
          chartType: "bar",
          series: [
            {
              name: "mixed",
              points: [
                { label: "ok", value: 1 },
                { label: "missing value" },
                { label: 7, value: 2 },
                { label: "not finite", value: Number.NaN },
                { label: "infinite", value: Number.POSITIVE_INFINITY },
                "nope",
              ],
            },
            { name: "all broken", points: [{ label: "x" }] },
            { name: "not a series", points: "nope" },
          ],
        },
      ],
    };

    const [chart] = iterCharts(surface);
    // A partially broken series must not read as a real measurement of zero.
    expect(chart?.series).toEqual([{ name: "mixed", points: [{ label: "ok", value: 1 }] }]);
  });

  it("keeps a chart whose data resolved to nothing", () => {
    const surface: A2UISurface = {
      components: [
        { id: A2UI_ROOT_ID, component: "Chart", chartType: "pie", series: { path: "/absent" } },
      ],
      dataModel: {},
    };
    const [chart] = iterCharts(surface);
    // Visible as "a chart was intended and had no data", not silently dropped.
    expect(chart?.chartType).toBe("pie");
    expect(chart?.series).toEqual([]);
  });

  it("ignores non-charts, unknown chart shapes and malformed nodes", () => {
    const surface: A2UISurface = {
      components: [
        { id: A2UI_ROOT_ID, component: "Column", children: ["t", "c1", "c2"] },
        { id: "t", component: "Text", text: "hi" },
        { id: "c1", component: "Chart", chartType: "radar", series: [] },
        { id: "c2", component: "Chart", chartType: "bar", series: [{ points: [{ label: "a", value: 1 }] }] },
      ],
    };
    expect(iterCharts(surface).map((chart) => chart.id)).toEqual(["c2"]);
    expect(iterCharts({ components: [] })).toEqual([]);
  });
});

describe("catalog vocabulary", () => {
  it("matches what the platform advertises and stamps", () => {
    expect(A2UI_CATALOG_ID).toBe("https://catalog.actweave.dev/standard/v1/catalog.json");
    expect(A2UI_SURFACE_VERSION).toBe("a2ui-surface.v1");
    expect(A2UI_COMPONENT_NAMES).toContain("Chart");
    expect(isA2UIComponentName("Chart")).toBe(true);
    // Exact names only: a client that folds case would accept surfaces the
    // server rejects.
    expect(isA2UIComponentName("chart")).toBe(false);
    expect(A2UI_LIMITS.maxComponents).toBeGreaterThan(0);
    expect(A2UI_LIMITS.maxTreeDepth).toBeGreaterThan(0);
  });

  it("recognises our catalog and refuses another", () => {
    expect(isKnownA2UICatalog(delivered("chart-bar"))).toBe(true);
    expect(isKnownA2UICatalog({ components: [], catalogId: "https://example.test/other.json" })).toBe(false);
    // Absent catalogId: a surface as a model emits it, before the platform
    // stamps identity. A client is never handed one.
    expect(isKnownA2UICatalog({ components: [] })).toBe(false);
    expect(isKnownA2UICatalog(null)).toBe(false);
  });

  it("covers every fixture component with a known name except the degrade case", () => {
    for (const fixture of A2UI_FIXTURES) {
      const known = fixture.surface.components.every((node) => isA2UIComponentName(node.component));
      expect(known).toBe(fixture.expect === "renders");
    }
  });
});

// The reason the helpers exist: read a completed assistant message end to end.
describe("reading an assistant message", () => {
  it("takes text from the text part and charts from the surface", () => {
    const item: ProtocolItem = {
      id: "81000000-0000-4000-8000-0000000000a1",
      type: "message",
      status: "completed",
      role: "assistant",
      content: [
        { type: "text", text: "季度营收如下。" },
        {
          type: "a2ui",
          version: A2UI_SURFACE_VERSION,
          catalogId: A2UI_CATALOG_ID,
          surface: delivered("chart-bar"),
        },
      ],
    };

    expect(joinTextParts(item)).toBe("季度营收如下。");
    const part = findA2UIPart(item);
    expect(part?.version).toBe(A2UI_SURFACE_VERSION);

    const surface = part?.surface;
    expect(isKnownA2UICatalog(surface)).toBe(true);
    if (!isKnownA2UICatalog(surface)) return;
    expect(iterCharts(surface).map((chart) => chart.chartType)).toEqual(["bar"]);
  });

  it("leaves a client with text alone when the surface is from another catalog", () => {
    const item: ProtocolItem = {
      id: "81000000-0000-4000-8000-0000000000a2",
      type: "message",
      status: "completed",
      role: "assistant",
      content: [
        { type: "text", text: "季度营收如下。" },
        {
          type: "a2ui",
          version: "a2ui-surface.v2",
          surface: { catalogId: "https://catalog.actweave.dev/standard/v2/catalog.json", components: [] },
        },
      ],
    };

    expect(joinTextParts(item)).toBe("季度营收如下。");
    expect(isKnownA2UICatalog(findA2UIPart(item)?.surface)).toBe(false);
  });
});
