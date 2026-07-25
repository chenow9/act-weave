import { describe, expect, it, vi } from "vitest";

import { MemoryTokenProvider, StaticTokenProvider } from "../src/index.js";

describe("MemoryTokenProvider", () => {
  it("caches tokens in memory and force-refreshes on demand", async () => {
    let n = 0;
    const refresh = vi.fn(async () => {
      n += 1;
      return { accessToken: `token-${n}`, expiresIn: 600 };
    });
    const provider = new MemoryTokenProvider({ refresh, skewSeconds: 30 });

    await expect(provider.getAccessToken()).resolves.toBe("token-1");
    await expect(provider.getAccessToken()).resolves.toBe("token-1");
    expect(refresh).toHaveBeenCalledTimes(1);

    await expect(provider.getAccessToken({ forceRefresh: true })).resolves.toBe("token-2");
    expect(refresh).toHaveBeenCalledTimes(2);
    expect(provider.hasCachedToken()).toBe(true);

    provider.clear();
    expect(provider.hasCachedToken()).toBe(false);
    await expect(provider.getAccessToken()).resolves.toBe("token-3");
  });

  it("refreshes when the token is near expiry", async () => {
    let now = 1_000_000;
    let n = 0;
    const provider = new MemoryTokenProvider({
      clock: () => now,
      skewSeconds: 30,
      refresh: async () => {
        n += 1;
        return { accessToken: `t-${n}`, expiresIn: 40 };
      },
    });

    await expect(provider.getAccessToken()).resolves.toBe("t-1");
    // expiresAt = 1_000_000 + 40_000; with skew 30s still valid at +5s
    now += 5_000;
    await expect(provider.getAccessToken()).resolves.toBe("t-1");
    // at +15s remaining is 25s < 30s skew → refresh
    now += 10_000;
    await expect(provider.getAccessToken()).resolves.toBe("t-2");
  });

  it("deduplicates concurrent refresh calls", async () => {
    let resolveRefresh!: (value: { accessToken: string }) => void;
    const refresh = vi.fn(
      () =>
        new Promise<{ accessToken: string }>((resolve) => {
          resolveRefresh = resolve;
        }),
    );
    const provider = new MemoryTokenProvider({ refresh });

    const p1 = provider.getAccessToken();
    const p2 = provider.getAccessToken();
    resolveRefresh({ accessToken: "shared" });
    await expect(Promise.all([p1, p2])).resolves.toEqual(["shared", "shared"]);
    expect(refresh).toHaveBeenCalledTimes(1);
  });
});

describe("StaticTokenProvider", () => {
  it("returns the configured token and rejects forceRefresh", async () => {
    const provider = new StaticTokenProvider("static-token");
    await expect(provider.getAccessToken()).resolves.toBe("static-token");
    await expect(provider.getAccessToken({ forceRefresh: true })).rejects.toThrow(/force-refresh/);
  });
});
