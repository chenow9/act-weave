import {
  DEFAULT_LOCALE,
  LOCALE_STORAGE_KEY,
  type AppLocale,
  isAppLocale,
} from "./types";

/** Normalize any BCP-47-ish string to an AppLocale. */
export function normalizeLocale(input: string | null | undefined): AppLocale {
  if (!input || !String(input).trim()) {
    return DEFAULT_LOCALE;
  }
  const raw = String(input).trim();
  if (isAppLocale(raw)) {
    return raw;
  }
  const lower = raw.toLowerCase().replace(/_/g, "-");
  if (lower === "zh" || lower.startsWith("zh-hans") || lower.startsWith("zh-cn") || lower.startsWith("zh-sg")) {
    return "zh-CN";
  }
  if (lower.startsWith("en")) {
    return "en";
  }
  return DEFAULT_LOCALE;
}

export function readStoredLocale(): AppLocale | null {
  if (typeof localStorage === "undefined") {
    return null;
  }
  try {
    const raw = localStorage.getItem(LOCALE_STORAGE_KEY);
    if (!raw) return null;
    return normalizeLocale(raw);
  } catch {
    return null;
  }
}

export function writeStoredLocale(locale: AppLocale) {
  if (typeof localStorage === "undefined") {
    return;
  }
  try {
    localStorage.setItem(LOCALE_STORAGE_KEY, locale);
  } catch {
    // ignore quota / private mode
  }
}

export function resolveInitialLocale(options?: {
  userLocale?: string | null;
  navigatorLanguage?: string | null;
  stored?: string | null;
}): AppLocale {
  if (options?.userLocale) {
    return normalizeLocale(options.userLocale);
  }
  const stored =
    options?.stored !== undefined ? (options.stored ? normalizeLocale(options.stored) : null) : readStoredLocale();
  if (stored) {
    return stored;
  }
  const nav =
    options?.navigatorLanguage !== undefined
      ? options.navigatorLanguage
      : typeof navigator !== "undefined"
        ? navigator.language || navigator.languages?.[0]
        : null;
  if (nav) {
    return normalizeLocale(nav);
  }
  return DEFAULT_LOCALE;
}
