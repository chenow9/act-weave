package openapiimport

import (
	"fmt"
	"testing"

	"actweave/backend/internal/domain"
)

func TestParseDocumentBuildsNormalizedEndpoints(t *testing.T) {
	doc := []byte(`
openapi: 3.0.3
info:
  title: Shipment API
  version: 1.0.0
paths:
  /shipments/{shipmentId}/intercept:
    parameters:
      - $ref: '#/components/parameters/ShipmentID'
    post:
      summary: 拦截发货
      parameters:
        - name: dryRun
          in: query
          required: false
          schema:
            type: boolean
        - name: X-Trace-Id
          in: header
          required: false
          schema:
            type: string
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/InterceptRequest'
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/InterceptResponse'
components:
  parameters:
    ShipmentID:
      name: shipmentId
      in: path
      required: true
      schema:
        type: string
  schemas:
    InterceptRequest:
      type: object
      required: [reason]
      properties:
        reason:
          type: string
    InterceptResponse:
      type: object
      properties:
        accepted:
          type: boolean
`)

	result, err := ParseDocument(ParseInput{
		FileName: "shipment-openapi.yaml",
		Content:  doc,
	})
	if err != nil {
		t.Fatalf("ParseDocument returned error: %v", err)
	}
	if len(result.Endpoints) != 1 {
		t.Fatalf("expected one endpoint, got %d", len(result.Endpoints))
	}

	endpoint := result.Endpoints[0]
	if endpoint.ToolIDCandidate != "shipments.by-shipment-id.intercept.post" {
		t.Fatalf("unexpected tool id candidate: %+v", endpoint)
	}
	if !endpoint.Ready {
		t.Fatalf("expected endpoint to be ready: %+v", endpoint)
	}
	if endpoint.OperationID != "" {
		t.Fatalf("expected raw operationId to remain empty, got %q", endpoint.OperationID)
	}
	if endpoint.Summary != "拦截发货" {
		t.Fatalf("unexpected summary: %+v", endpoint)
	}
	if len(endpoint.RequestParams) != 4 {
		t.Fatalf("expected four request params, got %+v", endpoint.RequestParams)
	}
	if endpoint.RequestParams[0].Name != "shipmentId" || endpoint.RequestParams[0].Location != "path" || endpoint.RequestParams[0].Type != "string" || !endpoint.RequestParams[0].Required {
		t.Fatalf("unexpected path param mapping: %+v", endpoint.RequestParams[0])
	}
	if endpoint.RequestParams[1].Name != "dryRun" || endpoint.RequestParams[1].Location != "query" || endpoint.RequestParams[1].Type != "boolean" || endpoint.RequestParams[1].Required {
		t.Fatalf("unexpected query param mapping: %+v", endpoint.RequestParams[1])
	}
	if endpoint.RequestParams[2].Name != "X-Trace-Id" || endpoint.RequestParams[2].Location != "header" || endpoint.RequestParams[2].Type != "string" || endpoint.RequestParams[2].Required {
		t.Fatalf("unexpected header param mapping: %+v", endpoint.RequestParams[2])
	}
	if endpoint.RequestParams[3].Name != "reason" || endpoint.RequestParams[3].Location != "body" || endpoint.RequestParams[3].Type != "string" || !endpoint.RequestParams[3].Required {
		t.Fatalf("unexpected body field mapping: %+v", endpoint.RequestParams[3])
	}
	if len(endpoint.ResponseFields) != 1 {
		t.Fatalf("expected one response field, got %+v", endpoint.ResponseFields)
	}
	if endpoint.ResponseFields[0].Name != "accepted" || endpoint.ResponseFields[0].Type != "boolean" {
		t.Fatalf("unexpected response field mapping: %+v", endpoint.ResponseFields[0])
	}
}

func TestParseDocumentMarksPaginationParamsAsSystemDefaults(t *testing.T) {
	result, err := ParseDocument(ParseInput{
		FileName: "regions.yaml",
		Content: []byte(`
openapi: 3.0.3
info:
  title: Regions
  version: 1.0.0
paths:
  /regions/page:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [pageNum, pageSize]
              properties:
                pageNum:
                  type: integer
                pageSize:
                  type: integer
                  default: 50
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
`),
	})
	if err != nil {
		t.Fatalf("ParseDocument returned error: %v", err)
	}

	params := result.Endpoints[0].RequestParams
	pageNum := findParam(params, "pageNum")
	if pageNum == nil {
		t.Fatalf("expected pageNum param, got %+v", params)
	}
	if pageNum.ValueSource != "SystemDefault" || fmt.Sprint(pageNum.DefaultValue) != "1" {
		t.Fatalf("expected inferred pageNum system default, got %+v", *pageNum)
	}

	pageSize := findParam(params, "pageSize")
	if pageSize == nil {
		t.Fatalf("expected pageSize param, got %+v", params)
	}
	if pageSize.ValueSource != "SystemDefault" || fmt.Sprint(pageSize.DefaultValue) != "50" {
		t.Fatalf("expected OpenAPI default pageSize to be preserved, got %+v", *pageSize)
	}
}

func findParam(params []domain.ToolParameter, name string) *domain.ToolParameter {
	for index := range params {
		if params[index].Name == name {
			return &params[index]
		}
	}
	return nil
}

func TestParseDocumentFallsBackWhenOperationIDMissing(t *testing.T) {
	result, err := ParseDocument(ParseInput{
		FileName: "orders.yaml",
		Content: []byte(`
openapi: 3.1.0
info: { title: Orders, version: 1.0.0 }
paths:
  /orders/{orderId}:
    get:
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
`),
	})
	if err != nil {
		t.Fatalf("ParseDocument returned error: %v", err)
	}
	if result.Endpoints[0].OperationID != "" {
		t.Fatalf("expected missing raw operationId to remain empty")
	}
	if result.Endpoints[0].ToolIDCandidate != "orders.by-order-id.get" {
		t.Fatalf("expected fallback tool id, got %+v", result.Endpoints[0])
	}
}

func TestParseDocumentIgnoresDefaultErrorResponseWhenSuccessIs204(t *testing.T) {
	result, err := ParseDocument(ParseInput{
		FileName: "jobs.yaml",
		Content: []byte(`
openapi: 3.0.3
info:
  title: Jobs
  version: 1.0.0
paths:
  /jobs/{jobId}/cancel:
    post:
      operationId: cancelJob
      responses:
        "204":
          description: Cancelled
        default:
          description: Error
          content:
            application/json:
              schema:
                type: object
                properties:
                  error:
                    type: string
`),
	})
	if err != nil {
		t.Fatalf("ParseDocument returned error: %v", err)
	}
	if len(result.Endpoints) != 1 {
		t.Fatalf("expected one endpoint, got %d", len(result.Endpoints))
	}

	endpoint := result.Endpoints[0]
	if len(endpoint.ResponseFields) != 0 {
		t.Fatalf("expected no response fields for 204 success response, got %+v", endpoint.ResponseFields)
	}
}

func TestParseDocumentExpandsAllOfAndBoundsRecursiveSchemas(t *testing.T) {
	result, err := ParseDocument(ParseInput{
		FileName: "directory.yaml",
		Content: []byte(`
openapi: 3.0.3
info: { title: Directory, version: 1.0.0 }
paths:
  /departments:
    get:
      operationId: getDepartments
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ResultTree'
components:
  schemas:
    Result:
      type: object
      properties:
        code: { type: string }
        data: { nullable: true }
    Department:
      type: object
      properties:
        name: { type: string }
        children:
          type: array
          items: { $ref: '#/components/schemas/Department' }
    ResultTree:
      allOf:
        - $ref: '#/components/schemas/Result'
        - type: object
          properties:
            data:
              type: array
              items: { $ref: '#/components/schemas/Department' }
`),
	})
	if err != nil {
		t.Fatalf("ParseDocument returned error: %v", err)
	}
	endpoint := result.Endpoints[0]
	if !endpoint.Ready || len(endpoint.Issues) != 0 {
		t.Fatalf("expected allOf endpoint to be ready: %+v", endpoint)
	}
	if len(endpoint.ResponseFields) != 2 || endpoint.ResponseFields[0].Name != "code" || endpoint.ResponseFields[1].Name != "data" {
		t.Fatalf("unexpected merged response fields: %+v", endpoint.ResponseFields)
	}
	data := endpoint.ResponseFields[1]
	if data.Type != "array" || data.Item == nil || data.Item.Type != "object" || len(data.Item.Children) != 2 {
		t.Fatalf("unexpected recursive array mapping: %+v", data)
	}
	children := data.Item.Children[0]
	if children.Name != "children" || children.Type != "array" || children.Item == nil || children.Item.Type != "object" {
		t.Fatalf("recursive child was not bounded as an object leaf: %+v", children)
	}
}

func TestParseDocumentRejectsDocumentWithoutPaths(t *testing.T) {
	_, err := ParseDocument(ParseInput{
		FileName: "empty-paths.yaml",
		Content: []byte(`
openapi: 3.0.3
info:
  title: Empty
  version: 1.0.0
paths: {}
`),
	})
	if err == nil {
		t.Fatal("expected error for document without paths")
	}
}

func TestParseDocumentPreservesNestedObjectAndArrayHierarchy(t *testing.T) {
	result, err := ParseDocument(ParseInput{
		FileName: "nested.yaml",
		Content: []byte(`
openapi: 3.0.3
info:
  title: Nested API
  version: 1.0.0
paths:
  /orders/search:
    post:
      operationId: searchOrders
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [filter, items]
              properties:
                filter:
                  type: object
                  required: [status]
                  properties:
                    status:
                      type: string
                    customer:
                      type: object
                      properties:
                        id:
                          type: string
                items:
                  type: array
                  items:
                    type: object
                    required: [sku]
                    properties:
                      sku:
                        type: string
                      quantity:
                        type: integer
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  meta:
                    type: object
                    properties:
                      total:
                        type: integer
                  records:
                    type: array
                    items:
                      type: object
                      properties:
                        orderId:
                          type: string
`),
	})
	if err != nil {
		t.Fatalf("ParseDocument returned error: %v", err)
	}
	if len(result.Endpoints) != 1 {
		t.Fatalf("expected one endpoint, got %d", len(result.Endpoints))
	}

	endpoint := result.Endpoints[0]
	if len(endpoint.RequestParams) != 2 {
		t.Fatalf("expected top-level request params only, got %+v", endpoint.RequestParams)
	}
	if endpoint.RequestParams[0].Name != "filter" || endpoint.RequestParams[0].Type != "object" || len(endpoint.RequestParams[0].Children) != 2 {
		t.Fatalf("expected nested object request param, got %+v", endpoint.RequestParams[0])
	}
	if endpoint.RequestParams[0].Children[0].Name != "customer" || endpoint.RequestParams[0].Children[0].Type != "object" || len(endpoint.RequestParams[0].Children[0].Children) != 1 {
		t.Fatalf("expected nested customer object, got %+v", endpoint.RequestParams[0].Children[0])
	}
	if endpoint.RequestParams[0].Children[1].Name != "status" || !endpoint.RequestParams[0].Children[1].Required {
		t.Fatalf("expected required nested status field, got %+v", endpoint.RequestParams[0].Children[1])
	}
	if endpoint.RequestParams[1].Name != "items" || endpoint.RequestParams[1].Type != "array" || endpoint.RequestParams[1].Item == nil {
		t.Fatalf("expected array request param item schema, got %+v", endpoint.RequestParams[1])
	}
	if endpoint.RequestParams[1].Item.Type != "object" || len(endpoint.RequestParams[1].Item.Children) != 2 {
		t.Fatalf("expected object array items, got %+v", endpoint.RequestParams[1].Item)
	}
	if endpoint.RequestParams[1].Item.Children[1].Name != "sku" || !endpoint.RequestParams[1].Item.Children[1].Required {
		t.Fatalf("expected required nested sku field, got %+v", endpoint.RequestParams[1].Item.Children[1])
	}

	if len(endpoint.ResponseFields) != 2 {
		t.Fatalf("expected top-level response fields only, got %+v", endpoint.ResponseFields)
	}
	if endpoint.ResponseFields[0].Name != "meta" || endpoint.ResponseFields[0].Type != "object" || len(endpoint.ResponseFields[0].Children) != 1 {
		t.Fatalf("expected nested response object, got %+v", endpoint.ResponseFields[0])
	}
	if endpoint.ResponseFields[1].Name != "records" || endpoint.ResponseFields[1].Type != "array" || endpoint.ResponseFields[1].Item == nil {
		t.Fatalf("expected response array item schema, got %+v", endpoint.ResponseFields[1])
	}
	if endpoint.ResponseFields[1].Item.Type != "object" || len(endpoint.ResponseFields[1].Item.Children) != 1 || endpoint.ResponseFields[1].Item.Children[0].Name != "orderId" {
		t.Fatalf("expected nested response array object, got %+v", endpoint.ResponseFields[1].Item)
	}
}

func TestParseDocumentRejectsExternalRefs(t *testing.T) {
	_, err := ParseDocument(ParseInput{
		FileName: "external-refs.yaml",
		Content: []byte(`
openapi: 3.0.3
info:
  title: External Ref
  version: 1.0.0
paths:
  /widgets:
    get:
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: './schemas.yaml#/components/schemas/Widget'
`),
	})
	if err == nil {
		t.Fatal("expected error for external refs")
	}
}
