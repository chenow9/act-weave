import { afterEach, describe, expect, it, vi } from "vitest";

import { copyText } from "./copy-text";

function stubExecCommand(result = true) {
  const execCommand = vi.fn(() => result);
  Object.defineProperty(document, "execCommand", {
    configurable: true,
    writable: true,
    value: execCommand,
  });
  return execCommand;
}

describe("copyText", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("uses the clipboard API when it is available", async () => {
    const writeText = vi.fn(async () => undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    await expect(copyText("awsk_live_once")).resolves.toBe(true);
    expect(writeText).toHaveBeenCalledWith("awsk_live_once");
  });

  it("falls back to execCommand on HTTP when clipboard write is blocked", async () => {
    vi.stubGlobal("navigator", {
      clipboard: {
        writeText: vi.fn(async () => {
          throw new Error("NotAllowedError");
        }),
      },
    });
    const execCommand = stubExecCommand(true);
    await expect(copyText("awsk_live_once")).resolves.toBe(true);
    expect(execCommand).toHaveBeenCalledWith("copy");
  });

  it("falls back when clipboard is missing", async () => {
    vi.stubGlobal("navigator", {});
    const execCommand = stubExecCommand(true);
    await expect(copyText("secret")).resolves.toBe(true);
    expect(execCommand).toHaveBeenCalledWith("copy");
  });

  it("returns false for empty text without touching the clipboard", async () => {
    const writeText = vi.fn(async () => undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    await expect(copyText("")).resolves.toBe(false);
    expect(writeText).not.toHaveBeenCalled();
  });
});
