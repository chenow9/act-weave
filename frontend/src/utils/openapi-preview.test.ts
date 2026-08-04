import { beforeAll, describe, expect, it } from "vitest";

import { setI18nLocale } from "../i18n";
import { tt } from "../i18n/tt";
import { parseOpenAPIPreview } from "./openapi-preview";

beforeAll(() => {
  setI18nLocale("zh-CN");
});

describe("parseOpenAPIPreview", () => {
  it("parses OpenAPI JSON into live preview rows", () => {
    const ready = tt("openapi.previewStatusReady");
    const preview = parseOpenAPIPreview(`{
      "openapi": "3.0.1",
      "info": { "title": "Inspection Area", "version": "1.0.0" },
      "paths": {
        "/api/inspection-area/route/{routeId}": {
          "get": {
            "summary": "航线详情"
          },
          "put": {
            "operationId": "updateRoute"
          }
        },
        "/api/inspection-area/region/page": {
          "get": {
            "summary": "区域分页查询"
          }
        }
      }
    }`);

    expect(preview.error).toBe("");
    expect(preview.endpointCount).toBe(3);
    expect(preview.readyCount).toBe(3);
    expect(preview.rows).toEqual([
      { method: "GET", path: "/api/inspection-area/region/page", suggestedTool: "区域分页查询", statusText: ready },
      { method: "GET", path: "/api/inspection-area/route/{routeId}", suggestedTool: "航线详情", statusText: ready },
      {
        method: "PUT",
        path: "/api/inspection-area/route/{routeId}",
        suggestedTool: "updateRoute",
        statusText: ready,
      },
    ]);
  });

  it("parses OpenAPI YAML and falls back to a generated tool id", () => {
    const ready = tt("openapi.previewStatusReady");
    const preview = parseOpenAPIPreview(`
openapi: 3.0.3
info:
  title: Inspection Area
  version: 1.0.0
paths:
  /api/inspection-area/route-equipment/list:
    get:
      parameters:
        - name: seriesCode
          in: query
          required: false
          schema:
            type: string
`);

    expect(preview.error).toBe("");
    expect(preview.endpointCount).toBe(1);
    expect(preview.readyCount).toBe(1);
    expect(preview.rows).toEqual([
      {
        method: "GET",
        path: "/api/inspection-area/route-equipment/list",
        suggestedTool: "api.inspection-area.route-equipment.list.get",
        statusText: ready,
      },
    ]);
  });
});
