import { afterEach, describe, expect, it, vi } from "vitest";

import { currentFocusModality, installFocusModality, restoreFocus } from "./focus-modality";

describe("focus modality", () => {
  afterEach(() => {
    document.documentElement.removeAttribute("data-aw-focus-modality");
  });

  it("treats pointer input as pointer and Tab as keyboard", () => {
    const stop = installFocusModality();
    expect(currentFocusModality()).toBe("pointer");

    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true }));
    expect(currentFocusModality()).toBe("keyboard");

    document.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true }));
    expect(currentFocusModality()).toBe("pointer");
    stop();
  });

  it("ignores modified keydown when switching to keyboard", () => {
    const stop = installFocusModality();
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", ctrlKey: true, bubbles: true }));
    expect(currentFocusModality()).toBe("pointer");
    stop();
  });

  it("restores focus without a visible ring after pointer input", () => {
    const stop = installFocusModality();
    const button = document.createElement("button");
    button.textContent = "查看";
    document.body.append(button);
    const focus = vi.spyOn(button, "focus");

    document.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true }));
    restoreFocus(button);

    expect(focus).toHaveBeenCalledWith({ preventScroll: true, focusVisible: false });
    button.remove();
    stop();
  });

  it("restores a visible ring after keyboard navigation", () => {
    const stop = installFocusModality();
    const button = document.createElement("button");
    document.body.append(button);
    const focus = vi.spyOn(button, "focus");

    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true }));
    restoreFocus(button);

    expect(focus).toHaveBeenCalledWith({ preventScroll: true, focusVisible: true });
    button.remove();
    stop();
  });
});
