import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";

import AppSelect from "./AppSelect.vue";

const here = dirname(fileURLToPath(import.meta.url));

describe("AppSelect", () => {
  it("treats an empty string as no selection so the placeholder can show", () => {
    const wrapper = mount(AppSelect, {
      props: {
        modelValue: "",
        options: [{ label: "新能源巡检助手", value: "agent-1" }],
        placeholder: "请选择",
        filterable: true,
        ariaLabel: "Agent",
      },
    });
    const input = wrapper.get("input[role='combobox']");
    expect(input.attributes("aria-label")).toBe("Agent");
    expect((input.element as HTMLInputElement).value).toBe("");
  });

  it("clears nested focus rings on inner combobox and wrapped search fields", () => {
    const css = readFileSync(resolve(here, "../styles/app.css"), "utf8");
    expect(css).toMatch(/\.app-select \.el-select__input:focus-visible \{[^}]*box-shadow:\s*none/s);
    expect(css).toMatch(
      /:is\(\s*\.app-select,\s*\.el-select,\s*\.search-box[\s\S]*?\)\s*:is\(input, textarea\):is\(:focus, :focus-visible\) \{[^}]*box-shadow:\s*none/s,
    );
    expect(css).toMatch(
      /html\[data-aw-focus-modality="pointer"\][\s\S]*?\[role="combobox"\][\s\S]*?box-shadow:\s*none !important/s,
    );
  });
});
