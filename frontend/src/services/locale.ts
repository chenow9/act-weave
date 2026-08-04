import zhCn from "element-plus/es/locale/lang/zh-cn";
import en from "element-plus/es/locale/lang/en";
import type { Language } from "element-plus/es/locale";

import { getI18nLocale, setI18nLocale } from "../i18n";
import { normalizeLocale, readStoredLocale, resolveInitialLocale, writeStoredLocale } from "../i18n/resolve";
import { tt } from "../i18n/tt";
import { APP_LOCALES, DEFAULT_LOCALE, LOCALE_STORAGE_KEY, type AppLocale, isAppLocale } from "../i18n/types";
import { apiClient, toAPIError, type AuthUserDTO } from "./api";

export {
  APP_LOCALES,
  DEFAULT_LOCALE,
  LOCALE_STORAGE_KEY,
  type AppLocale,
  isAppLocale,
  normalizeLocale,
  readStoredLocale,
  resolveInitialLocale,
  writeStoredLocale,
};

export function elementPlusLocale(app: AppLocale): Language {
  return app === "zh-CN" ? zhCn : en;
}

export function applyDocumentLocale(locale: AppLocale) {
  if (typeof document === "undefined") {
    return;
  }
  document.documentElement.lang = locale;
  document.title = tt("common.appTitle");
}

export type SetLocaleOptions = {
  /** When true with lockVersion, PATCH /users/me. Failure does not roll back UI (Q5). */
  syncServer?: boolean;
  lockVersion?: number;
  onUserUpdated?: (user: AuthUserDTO) => void;
  onSyncFailed?: (message: string) => void;
};

/**
 * Switch UI language: i18n + localStorage + document + optional server PATCH.
 * Does not roll back UI on server failure (Q5).
 */
export async function setLocale(next: AppLocale, options: SetLocaleOptions = {}): Promise<void> {
  const locale = normalizeLocale(next);
  setI18nLocale(locale);
  writeStoredLocale(locale);
  applyDocumentLocale(locale);

  const shouldSync = options.syncServer === true && typeof options.lockVersion === "number";
  if (!shouldSync) {
    return;
  }

  try {
    const user = await patchUserLocale(locale, options.lockVersion!);
    options.onUserUpdated?.(user);
  } catch (error) {
    const apiErr = toAPIError(error);
    if (apiErr.code === "CONFLICT" || apiErr.status === 409) {
      try {
        const me = await apiClient.get<AuthUserDTO>("/users/me");
        const user = await patchUserLocale(locale, me.data.lockVersion);
        options.onUserUpdated?.(user);
        return;
      } catch {
        options.onSyncFailed?.(tt("common.localeSyncFailed"));
        return;
      }
    }
    options.onSyncFailed?.(tt("common.localeSyncFailed"));
  }
}

async function patchUserLocale(locale: AppLocale, lockVersion: number): Promise<AuthUserDTO> {
  const response = await apiClient.patch<AuthUserDTO>("/users/me", {
    locale,
    lockVersion,
  });
  return response.data;
}

/** Apply locale from authenticated user without re-PATCHing. */
export function applyUserLocale(userLocale: string | null | undefined) {
  const locale = normalizeLocale(userLocale);
  setI18nLocale(locale);
  writeStoredLocale(locale);
  applyDocumentLocale(locale);
  return locale;
}

export function currentLocale(): AppLocale {
  return getI18nLocale();
}
