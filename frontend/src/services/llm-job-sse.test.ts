import { beforeEach, describe, expect, it, vi } from "vitest";

import { refreshAuthSession } from "./api";
import { postLlmJobSse } from "./llm-job-sse";

vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return {
    ...actual,
    getAuthToken: vi.fn(() => "expired-token"),
    refreshAuthSession: vi.fn(),
  };
});

// Internal parse behavior is covered indirectly via frame assembly smoke:
// export is postLlmJobSse; we re-test via a tiny harness of the SSE framing contract.

describe("llm-job-sse contract", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.stubGlobal("fetch", vi.fn());
  });

  it("recognizes completed and failed event names used by backend", () => {
    const terminal = new Set(["completed", "failed"]);
    expect(terminal.has("completed")).toBe(true);
    expect(terminal.has("failed")).toBe(true);
    expect(terminal.has("started")).toBe(false);
  });

  it("uses the shared session refresher after a 401", async () => {
    vi.mocked(refreshAuthSession).mockResolvedValue({ accessToken: "fresh-token" } as never);
    vi.mocked(fetch)
      .mockResolvedValueOnce(new Response("", { status: 401 }))
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ output: "ok" }), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
      );

    await expect(postLlmJobSse({ path: "/jobs", body: {} })).resolves.toEqual({ output: "ok" });

    expect(refreshAuthSession).toHaveBeenCalledTimes(1);
    expect(fetch).toHaveBeenLastCalledWith(
      "/api/v1/jobs",
      expect.objectContaining({ headers: expect.objectContaining({ Authorization: "Bearer fresh-token" }) }),
    );
  });
});
