import { describe, expect, it } from "vitest";

import { chartGeometry, formatValue, unionLabels, type ChartInput } from "./chart-geometry";
import type { A2UIChartSeries, A2UIChartType } from "./generated/catalog.gen";

function input(chartType: A2UIChartType, series: A2UIChartSeries[], stacked = false): ChartInput {
  return {
    chartType,
    series,
    stacked,
    format: (value) => String(value),
    seriesName: (index, name) => name ?? `series ${index + 1}`,
  };
}

const twoSeries: A2UIChartSeries[] = [
  {
    name: "生产",
    points: [
      { label: "一月", value: 10 },
      { label: "二月", value: 20 },
    ],
  },
  {
    name: "预发",
    points: [
      { label: "一月", value: 5 },
      { label: "三月", value: 15 },
    ],
  },
];

describe("formatValue", () => {
  it("appends a unit except to a percentage, which already reads as one", () => {
    expect(formatValue(1280, "plain", "万元")).toBe("1280 万元");
    expect(formatValue(96.05, "percent", "%")).toBe("96.05%");
    expect(formatValue(96.05, "percent", "")).toBe("96.05%");
  });

  it("shortens large numbers and groups currency", () => {
    expect(formatValue(48200, "compact", "")).toBe("48.2k");
    expect(formatValue(2_400_000, "compact", "")).toBe("2.4M");
    expect(formatValue(3_000_000_000, "compact", "")).toBe("3B");
    expect(formatValue(2605, "currency", "USD")).toBe("2,605 USD");
    expect(formatValue(1234567.5, "currency", "")).toBe("1,234,567.5");
  });

  it("keeps integers intact and trims trailing zeros", () => {
    expect(formatValue(100, "plain", "")).toBe("100");
    expect(formatValue(1.5, "plain", "")).toBe("1.5");
    expect(formatValue(1.5000001, "plain", "")).toBe("1.5");
    expect(formatValue(Number.NaN, "plain", "")).toBe("—");
  });
});

describe("unionLabels", () => {
  it("keeps first-seen order across series, which is the order a model listed them", () => {
    expect(unionLabels(twoSeries)).toEqual(["一月", "二月", "三月"]);
  });
});

describe("bars", () => {
  it("draws one bar per series and category, treating a gap as zero", () => {
    const geometry = chartGeometry(input("bar", twoSeries));
    expect(geometry.bars).toHaveLength(6);
    expect(geometry.categoryLabels.map((label) => label.text)).toEqual(["一月", "二月", "三月"]);
    // The missing 生产/三月 point is drawn as a zero-height bar on the baseline.
    expect(geometry.bars.filter((bar) => bar.height === 0)).toHaveLength(2);
  });

  it("stacks segments so a category's total is the sum of its series", () => {
    const stacked = chartGeometry(input("bar", twoSeries, true));
    const january = stacked.bars.filter((bar) => bar.title.includes("一月"));
    expect(january).toHaveLength(2);
    // Stacked segments share the same band, so they overlap in x and differ in y.
    expect(january[0]?.x).toBe(january[1]?.x);
    expect(january[0]?.y).not.toBe(january[1]?.y);
  });

  it("grows horizontal bars rightwards from a shared left baseline", () => {
    const geometry = chartGeometry(input("hbar", twoSeries));
    const xs = new Set(geometry.bars.map((bar) => bar.x));
    expect(xs.size).toBe(1);
    expect(geometry.categoryLabels[0]?.anchor).toBe("end");
    // A row per category rather than a fixed height.
    expect(geometry.height).toBeGreaterThan(80);
  });

  it("names series in the legend only when there are several", () => {
    expect(chartGeometry(input("bar", twoSeries)).legend.map((item) => item.label)).toEqual(["生产", "预发"]);
    expect(chartGeometry(input("bar", [twoSeries[0] as A2UIChartSeries])).legend).toEqual([]);
  });

  it("falls back to a series name when the surface left it unnamed", () => {
    const unnamed = chartGeometry(
      input("bar", [{ points: [{ label: "a", value: 1 }] }, { points: [{ label: "a", value: 2 }] }]),
    );
    expect(unnamed.legend.map((item) => item.label)).toEqual(["series 1", "series 2"]);
  });
});

describe("lines", () => {
  it("draws a path per series, with a dot per category", () => {
    const geometry = chartGeometry(input("line", twoSeries));
    expect(geometry.lines).toHaveLength(2);
    expect(geometry.lines[0]?.line.startsWith("M")).toBe(true);
    expect(geometry.lines[0]?.dots).toHaveLength(3);
    expect(geometry.lines[0]?.area).toBeUndefined();
  });

  it("closes an area path back along its baseline", () => {
    const geometry = chartGeometry(input("area", twoSeries));
    expect(geometry.lines[0]?.area?.endsWith("Z")).toBe(true);
  });

  it("adds a gridline and a value tick for each step of the axis", () => {
    const geometry = chartGeometry(input("line", twoSeries));
    expect(geometry.grid).toHaveLength(5);
    expect(geometry.valueLabels).toHaveLength(5);
    expect(geometry.valueLabels.at(0)?.text).toBe("0");
  });
});

describe("pie and donut", () => {
  const composition: A2UIChartSeries[] = [
    {
      points: [
        { label: "API", value: 60 },
        { label: "Console", value: 40 },
        { label: "空", value: 0 },
      ],
    },
  ];

  it("omits a zero slice and reports each share in the legend", () => {
    const geometry = chartGeometry(input("pie", composition));
    expect(geometry.slices).toHaveLength(2);
    expect(geometry.radial).toBe(true);
    expect(geometry.legend.map((item) => item.share)).toEqual(["60.0%", "40.0%", "0.0%"]);
  });

  it("labels a donut with the total and cuts an inner radius", () => {
    const geometry = chartGeometry(input("donut", composition));
    expect(geometry.centerLabel).toBe("100");
    // A donut slice is a ring segment: two arcs rather than a wedge from center.
    expect(geometry.slices[0]?.path.match(/A/g)).toHaveLength(2);
    expect(chartGeometry(input("pie", composition)).slices[0]?.path.match(/A/g)).toHaveLength(1);
  });

  it("draws nothing when no value is positive", () => {
    const geometry = chartGeometry(input("donut", [{ points: [{ label: "zero", value: 0 }] }]));
    expect(geometry.slices).toEqual([]);
    expect(geometry.centerLabel).toBeUndefined();
  });

  it("ignores series past the first, which the catalog does not allow here", () => {
    const geometry = chartGeometry(input("pie", twoSeries));
    expect(geometry.slices).toHaveLength(2);
  });
});

describe("value axis", () => {
  it("always includes zero so bars share a baseline", () => {
    const geometry = chartGeometry(
      input("bar", [
        {
          points: [
            { label: "a", value: 40 },
            { label: "b", value: 60 },
          ],
        },
      ]),
    );
    expect(geometry.valueLabels.at(0)?.text).toBe("0");
  });

  it("rounds the upper bound out to a readable number", () => {
    const geometry = chartGeometry(input("bar", [{ points: [{ label: "a", value: 137 }] }]));
    expect(geometry.valueLabels.at(-1)?.text).toBe("200");
  });

  it("spans both signs when a value is negative", () => {
    const geometry = chartGeometry(
      input("bar", [
        {
          points: [
            { label: "a", value: -30 },
            { label: "b", value: 10 },
          ],
        },
      ]),
    );
    const bounds = geometry.valueLabels.map((label) => Number(label.text));
    expect(Math.min(...bounds)).toBeLessThanOrEqual(-30);
    expect(Math.max(...bounds)).toBeGreaterThanOrEqual(10);
  });
});
