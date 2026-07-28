import { describe, expect, it } from "vitest";

import { normalizeServiceBaseURL } from "./normalize-service-base-url";

describe("normalizeServiceBaseURL", () => {
  it("keeps absolute URL port without doubling", () => {
    expect(normalizeServiceBaseURL({ domain: "http://localhost:18080/api" })).toBe("http://localhost:18080/api");
  });

  it("does not append port when domain already includes it", () => {
    expect(
      normalizeServiceBaseURL({
        domain: "http://localhost:18080",
        port: 18080,
        basePath: "/v1",
      }),
    ).toBe("http://localhost:18080");
  });

  it("constructs host-only historical values once", () => {
    expect(
      normalizeServiceBaseURL({
        host: "api.example.com",
        protocol: "https",
        port: 443,
        basePath: "billing",
      }),
    ).toMatch(/^https:\/\/api\.example\.com/);
  });

  it("returns empty for illegal scheme", () => {
    expect(normalizeServiceBaseURL({ domain: "ftp://x" })).toBe("");
  });

  it("reproduces DEF-01 OpenAPI detail case without double port", () => {
    // domain already absolute with :18080; port field still 18080 (historical dual storage).
    expect(
      normalizeServiceBaseURL({
        domain: "http://127.0.0.1:18080",
        port: "18080",
        basePath: "",
      }),
    ).toBe("http://127.0.0.1:18080");
    expect(
      normalizeServiceBaseURL({
        domain: "http://127.0.0.1:18080",
        port: "18080",
        basePath: "",
      }),
    ).not.toMatch(/:\d+:\d+/);
  });
});
