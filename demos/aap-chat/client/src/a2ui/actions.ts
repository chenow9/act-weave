/**
 * Display-only A2UI actions. Catalog v1 has no action contract, so a click never
 * leaves the page: it only tells the reader the control is a preview.
 *
 * Listeners live on the document because the transcript replaces markup on every
 * stream tick the same way chart hover does.
 */

const TOAST_MS = 2400;

let installed = false;
let toastTimer = 0;

export function installA2UIActions(): void {
  if (installed || typeof document === "undefined") return;
  installed = true;
  document.addEventListener("click", onActionClick);
}

function onActionClick(event: Event): void {
  const target = event.target instanceof Element ? event.target : null;
  const button = target?.closest<HTMLButtonElement>("[data-a2ui-action]");
  if (!button) return;
  event.preventDefault();
  showDemoToast(button.getAttribute("data-a2ui-toast") || "展示态 · 本轮不提交");
}

export function showDemoToast(message: string): void {
  if (typeof document === "undefined") return;
  const existing = document.querySelector(".demo-toast");
  existing?.remove();
  const toast = document.createElement("div");
  toast.className = "demo-toast";
  toast.setAttribute("role", "status");
  toast.textContent = message;
  document.body.appendChild(toast);
  window.clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => toast.remove(), TOAST_MS);
}
