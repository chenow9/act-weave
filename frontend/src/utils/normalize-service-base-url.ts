/**
 * ZKL-56 UX-05: normalize Connection service base URL without double ports.
 *
 * Rules:
 * 1. Absolute HTTP(S) `domain` is the sole source — strip query/fragment, normalize slash,
 *    do not re-append derived port/basePath.
 * 2. Historical host-only values: construct once from protocol/host/port/basePath.
 * 3. Non-HTTP(S) / illegal → empty string (caller shows 配置异常).
 */
export function normalizeServiceBaseURL(input: {
  domain?: string;
  protocol?: string;
  host?: string;
  port?: string | number;
  basePath?: string;
}): string {
  const domain = (input.domain || "").trim();
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(domain)) {
    // Explicit scheme present: only http(s) allowed.
    if (!/^https?:\/\//i.test(domain)) {
      return "";
    }
    try {
      const url = new URL(domain);
      if (url.protocol !== "http:" && url.protocol !== "https:") {
        return "";
      }
      url.search = "";
      url.hash = "";
      let path = url.pathname;
      if (path.length > 1 && path.endsWith("/")) {
        path = path.slice(0, -1);
      }
      url.pathname = path === "/" ? "" : path;
      // URL always emits trailing slash for empty path — strip for stable display.
      const out = url.toString();
      if (url.pathname === "" || url.pathname === "/") {
        return out.replace(/\/$/, "");
      }
      return out;
    } catch {
      return "";
    }
  }

  const host = (input.host || domain || "").trim();
  if (!host) return "";
  const protocol = (input.protocol || "https").replace(/:$/, "");
  if (protocol !== "http" && protocol !== "https") return "";
  const portRaw = input.port === undefined || input.port === null ? "" : String(input.port).trim();
  const basePath = (input.basePath || "").trim();
  const pathPart = basePath ? (basePath.startsWith("/") ? basePath : `/${basePath}`) : "";
  const normalizedPath =
    pathPart.length > 1 && pathPart.endsWith("/") ? pathPart.slice(0, -1) : pathPart;
  // If host already embeds :port, do not append again (fixes :18080:18080).
  const hostHasPort = /:\d+$/.test(host.replace(/^\[|\]$/g, ""));
  const portSuffix = portRaw && !hostHasPort ? `:${portRaw}` : "";
  try {
    const url = new URL(`${protocol}://${host}${portSuffix}${normalizedPath}`);
    const out = url.toString();
    if (!normalizedPath) return out.replace(/\/$/, "");
    return out;
  } catch {
    return "";
  }
}
