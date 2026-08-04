export type AppLocale = "zh-CN" | "en";

export const APP_LOCALES: readonly AppLocale[] = ["zh-CN", "en"] as const;

/** Product default (Q4): en when no preference is known. */
export const DEFAULT_LOCALE: AppLocale = "en";

export const LOCALE_STORAGE_KEY = "actweave.locale";

export function isAppLocale(value: string): value is AppLocale {
  return value === "zh-CN" || value === "en";
}
