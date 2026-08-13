import { describe, expect, it } from "vitest";

import {
  isDataBinding,
  resolveBoolean,
  resolveChoiceValues,
  resolvePointer,
  resolveSeries,
  resolveString,
} from "./resolve";

describe("isDataBinding", () => {
  it("accepts only an object with an absolute pointer", () => {
    expect(isDataBinding({ path: "/a" })).toBe(true);
    expect(isDataBinding({ path: "a" })).toBe(false);
    expect(isDataBinding({ path: 1 })).toBe(false);
    expect(isDataBinding({})).toBe(false);
    expect(isDataBinding([{ path: "/a" }])).toBe(false);
    expect(isDataBinding(null)).toBe(false);
    expect(isDataBinding("/a")).toBe(false);
  });
});

describe("resolvePointer", () => {
  const model = {
    caption: "两周请求量",
    traffic: { all: [{ name: "生产" }, { name: "预发" }] },
    "a/b": "slash",
    "c~d": "tilde",
    zero: 0,
    flag: false,
  };

  it("walks objects and array indices", () => {
    expect(resolvePointer(model, "/caption")).toBe("两周请求量");
    expect(resolvePointer(model, "/traffic/all/1/name")).toBe("预发");
  });

  it("returns the root for an empty pointer", () => {
    expect(resolvePointer(model, "")).toBe(model);
    expect(resolvePointer(model, "/")).toBe(model);
  });

  it("unescapes ~1 and ~0 per RFC 6901", () => {
    expect(resolvePointer(model, "/a~1b")).toBe("slash");
    expect(resolvePointer(model, "/c~0d")).toBe("tilde");
  });

  it("distinguishes a falsy value from a missing one", () => {
    expect(resolvePointer(model, "/zero")).toBe(0);
    expect(resolvePointer(model, "/flag")).toBe(false);
    expect(resolvePointer(model, "/missing")).toBeUndefined();
  });

  it("gives up rather than guessing", () => {
    expect(resolvePointer(model, "relative")).toBeUndefined();
    expect(resolvePointer(model, "/traffic/all/9")).toBeUndefined();
    expect(resolvePointer(model, "/traffic/all/x")).toBeUndefined();
    expect(resolvePointer(model, "/caption/deeper")).toBeUndefined();
  });
});

describe("resolveString", () => {
  it("passes literals through and follows bindings", () => {
    expect(resolveString("直接", {})).toBe("直接");
    expect(resolveString({ path: "/title" }, { title: "绑定" })).toBe("绑定");
  });

  it("renders numbers and booleans, and nothing else", () => {
    expect(resolveString(42, {})).toBe("42");
    expect(resolveString(true, {})).toBe("true");
    expect(resolveString({ path: "/missing" }, {})).toBe("");
    expect(resolveString(undefined, {})).toBe("");
    expect(resolveString({ nested: 1 }, {})).toBe("");
  });
});

describe("resolveBoolean", () => {
  it("is true only for a real boolean true", () => {
    expect(resolveBoolean(true, {})).toBe(true);
    expect(resolveBoolean({ path: "/on" }, { on: true })).toBe(true);
    expect(resolveBoolean("true", {})).toBe(false);
    expect(resolveBoolean(1, {})).toBe(false);
    expect(resolveBoolean(undefined, {})).toBe(false);
  });
});

describe("resolveChoiceValues", () => {
  it("normalizes one selection, several, or a binding", () => {
    expect(resolveChoiceValues("api", {})).toEqual(["api"]);
    expect(resolveChoiceValues(["api", "console"], {})).toEqual(["api", "console"]);
    expect(resolveChoiceValues({ path: "/picked" }, { picked: ["runtime"] })).toEqual(["runtime"]);
    expect(resolveChoiceValues([1, "api"], {})).toEqual(["api"]);
    expect(resolveChoiceValues(undefined, {})).toEqual([]);
  });
});

describe("resolveSeries", () => {
  it("reads inline and bound series the same way", () => {
    const inline = [{ name: "生产", points: [{ label: "周一", value: 1 }] }];
    expect(resolveSeries(inline, {})).toEqual(inline);
    expect(resolveSeries({ path: "/traffic" }, { traffic: inline })).toEqual(inline);
  });

  it("drops malformed points and empty series", () => {
    const series = resolveSeries(
      [
        {
          points: [
            { label: "ok", value: 2 },
            { label: "missing value" },
            { label: 5, value: 5 },
            { label: "not finite", value: Number.NaN },
            "not an object",
          ],
        },
        { points: [] },
        { points: "not an array" },
      ],
      {},
    );
    expect(series).toEqual([{ points: [{ label: "ok", value: 2 }] }]);
  });

  it("returns nothing when the binding does not resolve to an array", () => {
    expect(resolveSeries({ path: "/nope" }, {})).toEqual([]);
    expect(resolveSeries({ path: "/scalar" }, { scalar: 3 })).toEqual([]);
    expect(resolveSeries(undefined, {})).toEqual([]);
  });
});
