import type { AppLocale } from "../i18n/types";
import { currentLocale } from "../services/locale";

export function intlLocaleForDates(app?: AppLocale): string {
  const locale = app ?? currentLocale();
  return locale === "zh-CN" ? "zh-CN" : "en-US";
}

export function getCollatorLocale(app?: AppLocale): string {
  const locale = app ?? currentLocale();
  return locale === "zh-CN" ? "zh-CN" : "en";
}

export function formatDateTime(value: string | Date, app?: AppLocale): string {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString(intlLocaleForDates(app), {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** Compact ISO-like YYYY-MM-DD HH:mm via sv-SE (language-independent table format). */
export function formatDateTimeIsoLike(value: string | Date): string {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString("sv-SE", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}
