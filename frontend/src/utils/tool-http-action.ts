import type { ToolSchemaNode } from "../types/domain";

export const HTTP_ACTION_SCHEMA_VERSION = "http.v1";

export function buildHTTPActionConfig(
  method: string,
  path: string,
  contentType: string,
  requestContract: ToolSchemaNode[],
) {
  const parameters = requestContract
    .filter((node) => node.name.trim())
    .map((node) => {
      const location = (node.location || "Body").toLocaleLowerCase();
      return {
        name: node.name.trim(),
        in: location,
        input: node.name.trim(),
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
