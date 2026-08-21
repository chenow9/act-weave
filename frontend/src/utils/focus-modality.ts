const FOCUS_MODALITY_ATTR = "data-aw-focus-modality";
const POINTER_EVENTS = ["pointerdown", "mousedown", "touchstart"] as const;

export type FocusModality = "pointer" | "keyboard";

function isKeyboardFocusKey(event: KeyboardEvent) {
  if (event.metaKey || event.ctrlKey || event.altKey) return false;
  switch (event.key) {
    case "Tab":
    case "ArrowUp":
    case "ArrowDown":
    case "ArrowLeft":
    case "ArrowRight":
    case "Home":
    case "End":
    case "Escape":
      return true;
    default:
      return false;
  }
}

export function currentFocusModality(root: HTMLElement = document.documentElement): FocusModality {
  return root.getAttribute(FOCUS_MODALITY_ATTR) === "keyboard" ? "keyboard" : "pointer";
}

export function installFocusModality(doc: Document = document) {
  const root = doc.documentElement;
  root.setAttribute(FOCUS_MODALITY_ATTR, currentFocusModality(root));
  const controller = new AbortController();
  const setPointer = () => root.setAttribute(FOCUS_MODALITY_ATTR, "pointer");
  const onKeyDown = (event: Event) => {
    if (isKeyboardFocusKey(event as KeyboardEvent)) {
      root.setAttribute(FOCUS_MODALITY_ATTR, "keyboard");
    }
  };
  for (const type of POINTER_EVENTS) {
    doc.addEventListener(type, setPointer, { capture: true, passive: true, signal: controller.signal });
  }
  doc.addEventListener("keydown", onKeyDown, { capture: true, signal: controller.signal });
  return () => {
    controller.abort();
    root.removeAttribute(FOCUS_MODALITY_ATTR);
  };
}

export function restoreFocus(target: HTMLElement | null | undefined, doc: Document = document) {
  if (!target?.isConnected) return;
  const keyboard = currentFocusModality(doc.documentElement) === "keyboard";
  target.focus({
    preventScroll: true,
    focusVisible: keyboard,
  } as FocusOptions);
}
