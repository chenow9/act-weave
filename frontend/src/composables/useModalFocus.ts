import { nextTick, onBeforeUnmount, watch, type Ref, type WatchSource } from "vue";

import { restoreFocus } from "../utils/focus-modality";

interface ModalFocusOptions {
  visible: WatchSource<boolean>;
  modalRef: Ref<HTMLElement | null>;
  onClose: () => void;
  initialFocusSelector?: string;
}

interface ModalFocusEntry {
  modalRef: Ref<HTMLElement | null>;
}

const modalFocusStack: ModalFocusEntry[] = [];

const focusableSelector = [
  "button:not(:disabled)",
  "[href]",
  "input:not(:disabled)",
  "select:not(:disabled)",
  "textarea:not(:disabled)",
  "[tabindex]:not([tabindex='-1'])",
].join(",");

function focusableElements(root: HTMLElement) {
  return Array.from(root.querySelectorAll<HTMLElement>(focusableSelector)).filter((element) => {
    const rect = element.getBoundingClientRect();
    return rect.width > 0 || rect.height > 0 || element === document.activeElement;
  });
}

export function useModalFocus(options: ModalFocusOptions) {
  let restoreTarget: HTMLElement | null = null;
  const stackEntry: ModalFocusEntry = {
    modalRef: options.modalRef,
  };

  function removeFromStack() {
    const entryIndex = modalFocusStack.indexOf(stackEntry);
    if (entryIndex >= 0) {
      modalFocusStack.splice(entryIndex, 1);
    }
  }

  function pushToStack() {
    removeFromStack();
    modalFocusStack.push(stackEntry);
  }

  function isTopmostModal() {
    return modalFocusStack[modalFocusStack.length - 1] === stackEntry;
  }

  function focusInitialElement() {
    const modal = options.modalRef.value;
    if (!modal) return;

    const initialTarget =
      modal.querySelector<HTMLElement>(options.initialFocusSelector || "[data-modal-initial-focus]") ||
      focusableElements(modal)[0];
    initialTarget?.focus();
  }

  function handleKeydown(event: KeyboardEvent) {
    const modal = options.modalRef.value;
    if (!modal || !isTopmostModal()) return;

    if (event.key === "Escape") {
      event.preventDefault();
      options.onClose();
      return;
    }

    if (event.key !== "Tab") return;

    const focusable = focusableElements(modal);
    if (!focusable.length) {
      event.preventDefault();
      modal.focus();
      return;
    }

    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
      return;
    }
    if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  function removeListener() {
    window.removeEventListener("keydown", handleKeydown);
  }

  watch(
    options.visible,
    async (visible) => {
      removeListener();
      if (!visible) {
        removeFromStack();
        restoreFocus(restoreTarget);
        restoreTarget = null;
        return;
      }

      restoreTarget = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      pushToStack();
      window.addEventListener("keydown", handleKeydown);
      await nextTick();
      focusInitialElement();
    },
    { immediate: true },
  );

  onBeforeUnmount(() => {
    removeFromStack();
    removeListener();
    restoreFocus(restoreTarget);
    restoreTarget = null;
  });
}
