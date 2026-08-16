import type { ToolSchemaNode } from "../types/domain";

export const HTTP_ACTION_SCHEMA_VERSION = "http.v1";

/** Join connection origin + basePath + action path without doubling /api (or similar) prefixes. */
export function joinHttpEndpoint(domain: string, basePath: string, actionPath: string): string {
  const origin = (domain || "").trim().replace(/\/+$/, "");
  const rawBase = (basePath || "").trim();
  const rawPath = (actionPath || "").trim() || "/";
  const path = rawPath.startsWith("/") ? rawPath : `/${rawPath}`;
  const base =
    !rawBase || rawBase === "/"
      ? ""
      : rawBase.startsWith("/")
        ? rawBase.replace(/\/+$/, "")
        : `/${rawBase.replace(/\/+$/, "")}`;

  if (!origin) {
    if (base && path !== "/" && path !== base && !path.startsWith(`${base}/`)) {
      return `${base}${path}`;
    }
    return path;
  }

  let prefix = origin;
  if (base && !originEndsWithPath(origin, base)) {
    prefix = `${origin}${base}`;
  }

  if (path === "/") return prefix;
  if (originEndsWithPath(prefix, path)) return prefix;

  const overlap = trailingPrefixOverlap(prefix, path);
  if (overlap > 0) return `${prefix}${path.slice(overlap)}`;
  return `${prefix}${path}`;
}

function originEndsWithPath(origin: string, suffix: string): boolean {
  if (!suffix || suffix === "/") return false;
  return origin === suffix || origin.endsWith(suffix);
}

function trailingPrefixOverlap(origin: string, path: string): number {
  const parts = path.split("/").filter(Boolean);
  for (let i = parts.length; i >= 1; i -= 1) {
    const prefix = `/${parts.slice(0, i).join("/")}`;
    if (origin.endsWith(prefix)) return prefix.length;
  }
  return 0;
}

export function buildHTTPActionConfig(
  method: string,
  path: string,
  contentType: string,
  requestContract: ToolSchemaNode[],
) {
  const usedInputs = new Set<string>();
  const parameters = requestContract
    .filter((node) => node.name.trim())
    .map((node) => {
      const location = (node.location || "Body").toLocaleLowerCase();
      const wireName = node.name.trim();
      let input = wireName;
      if (usedInputs.has(input)) {
        input = `${location}_${wireName}`;
        let index = 2;
        while (usedInputs.has(input)) {
          input = `${location}_${wireName}_${index}`;
          index += 1;
        }
      }
      usedInputs.add(input);
      return {
        name: wireName,
        in: location,
        input,
        ...(node.required || location === "path" ? { required: true } : {}),
      };
    });
  const hasBodyParameters = parameters.some((parameter) => parameter.in === "body");
  return {
    method: method.toLocaleUpperCase(),
    path: path.trim() || "/",
    parameters,
    ...(hasBodyParameters ? { requestBody: { contentType } } : {}),
  };
}
