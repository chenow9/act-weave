import { describe, expect, it } from "vitest";

import { normalizeServiceBaseURL } from "./normalize-service-base-url";

describe("normalizeServiceBaseURL", () => {
  it("keeps absolute URL port without doubling", () => {
    expect(normalizeServiceBaseURL({ domain: "http://localhost:18080/api" })).toBe(
      "http://localhost:18080/api",
    );
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
});
