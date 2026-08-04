import { i18n } from "./index";

/**
 * Translate outside of setup() (Pinia stores, utils, services).
 * Prefer useI18n() inside Vue setup / composables.
 */
export function tt(key: string, named?: Record<string, unknown>): string {
  if (named) {
    return String(i18n.global.t(key, named));
  }
  return String(i18n.global.t(key));
}

export function te(key: string): boolean {
  return i18n.global.te(key);
}
