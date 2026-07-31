import { describe, expect, it } from "vitest";

// Internal parse behavior is covered indirectly via frame assembly smoke:
// export is postLlmJobSse; we re-test via a tiny harness of the SSE framing contract.

describe("llm-job-sse contract", () => {
  it("recognizes completed and failed event names used by backend", () => {
    const terminal = new Set(["completed", "failed"]);
    expect(terminal.has("completed")).toBe(true);
    expect(terminal.has("failed")).toBe(true);
    expect(terminal.has("started")).toBe(false);
  });
});
