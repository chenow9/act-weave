import { afterEach, describe, expect, it } from "vitest";

import { DEFAULT_LOCALE, LOCALE_STORAGE_KEY } from "../i18n/types";
import { normalizeLocale, resolveInitialLocale } from "../i18n/resolve";

describe("normalizeLocale", () => {
  it("maps Chinese variants to zh-CN", () => {
    expect(normalizeLocale("zh-CN")).toBe("zh-CN");
    expect(normalizeLocale("zh")).toBe("zh-CN");
    expect(normalizeLocale("zh-Hans-CN")).toBe("zh-CN");
    expect(normalizeLocale("zh_SG")).toBe("zh-CN");
  });

  it("maps English variants to en", () => {
    expect(normalizeLocale("en")).toBe("en");
    expect(normalizeLocale("en-US")).toBe("en");
    expect(normalizeLocale("en-GB")).toBe("en");
  });

  it("falls back to product default en for unsupported tags", () => {
    expect(normalizeLocale("ja-JP")).toBe(DEFAULT_LOCALE);
    expect(normalizeLocale("zh-TW")).toBe(DEFAULT_LOCALE);
    expect(normalizeLocale("")).toBe(DEFAULT_LOCALE);
    expect(normalizeLocale(null)).toBe(DEFAULT_LOCALE);
    expect(DEFAULT_LOCALE).toBe("en");
  });
});

describe("resolveInitialLocale", () => {
  afterEach(() => {
    localStorage.removeItem(LOCALE_STORAGE_KEY);
  });

  it("prefers user locale over storage and navigator", () => {
    expect(
      resolveInitialLocale({
        userLocale: "zh-CN",
        stored: "en",
        navigatorLanguage: "en-US",
      }),
    ).toBe("zh-CN");
  });

  it("uses storage when user is absent", () => {
    expect(
      resolveInitialLocale({
        userLocale: null,
        stored: "zh-CN",
        navigatorLanguage: "en-US",
      }),
    ).toBe("zh-CN");
  });

  it("uses navigator then default", () => {
    expect(
      resolveInitialLocale({
        userLocale: null,
        stored: null,
        navigatorLanguage: "zh-CN",
      }),
    ).toBe("zh-CN");
    expect(
      resolveInitialLocale({
        userLocale: null,
        stored: null,
        navigatorLanguage: null,
      }),
    ).toBe("en");
  });
});
