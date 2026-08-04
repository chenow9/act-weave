import { createI18n } from "vue-i18n";

import { setI18nLocale } from "../i18n";
import { messages } from "../i18n/messages";
import type { AppLocale } from "../i18n/types";

/**
 * Create a fresh i18n instance for unit tests (defaults to zh-CN for stable assertions).
 * Also aligns the app singleton used by tt() so store/composable strings match.
 */
export function createTestI18n(locale: AppLocale = "zh-CN") {
  setI18nLocale(locale);
  return createI18n({
    legacy: false,
    globalInjection: true,
    locale,
    fallbackLocale: "en",
    messages,
    missingWarn: false,
    fallbackWarn: false,
  });
}
