/**
 * Vitest global setup: install vue-i18n so components that call useI18n()
 * work without each test repeating plugins: [createTestI18n()].
 *
 * Default locale is zh-CN for stable Chinese string assertions.
 * Also force the app-level i18n singleton (used by tt() in stores/composables)
 * to zh-CN — otherwise CI (en-US navigator) renders English while setup()
 * components using useI18n() show Chinese.
 *
 * Tests that need English should pass createTestI18n("en") via mount() and
 * may call setI18nLocale("en") when they also assert tt() output.
 */
import { config } from "@vue/test-utils";

import { setI18nLocale } from "../i18n";
import { createTestI18n } from "./i18n";

setI18nLocale("zh-CN");
config.global.plugins = [...(config.global.plugins || []), createTestI18n("zh-CN")];
