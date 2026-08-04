import { createI18n } from "vue-i18n";

import { messages } from "./messages";
import { resolveInitialLocale } from "./resolve";
import { DEFAULT_LOCALE, type AppLocale } from "./types";

const initialLocale = resolveInitialLocale();

export const i18n = createI18n({
  legacy: false,
  globalInjection: true,
  locale: initialLocale,
  fallbackLocale: DEFAULT_LOCALE,
  messages,
  missingWarn: import.meta.env.DEV,
  fallbackWarn: import.meta.env.DEV,
});

export function getI18nLocale(): AppLocale {
  const value = i18n.global.locale.value;
  return value === "zh-CN" || value === "en" ? value : DEFAULT_LOCALE;
}

export function setI18nLocale(locale: AppLocale) {
  i18n.global.locale.value = locale;
}

export type { AppLocale };
