package openapiimport

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"actweave/backend/internal/domain"
	"github.com/getkin/kin-openapi/openapi3"
)

type ParseInput struct {
	FileName string
	Content  []byte
}

type ParseResult struct {
	Endpoints []domain.OpenAPIEndpoint
	Issues    []string
}

func ParseDocument(input ParseInput) (ParseResult, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false

	doc, err := loader.LoadFromData(input.Content)
	if err != nil {
		return ParseResult{}, fmt.Errorf("parse openapi document: %w", err)
	}
	if doc.Paths == nil || len(doc.Paths.Map()) == 0 {
		return ParseResult{}, fmt.Errorf("parse openapi document: no paths defined")
	}

	endpoints := make([]domain.OpenAPIEndpoint, 0)
	for path, item := range doc.Paths.Map() {
		for method, operation := range operationsForPathItem(item) {
			endpoints = append(endpoints, normalizeOperation(method, path, item, operation))
		}
	}

	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].Path == endpoints[j].Path {
			return endpoints[i].Method < endpoints[j].Method
		}
		return endpoints[i].Path < endpoints[j].Path
	})
	markDuplicateOperationIDs(endpoints)

	return ParseResult{Endpoints: endpoints}, nil
}

func operationsForPathItem(item *openapi3.PathItem) map[string]*openapi3.Operation {
	operations := make(map[string]*openapi3.Operation)
	if item == nil {
		return operations
	}
	if item.Connect != nil {
		operations["connect"] = item.Connect
	}
	if item.Delete != nil {
		operations["delete"] = item.Delete
	}
	if item.Get != nil {
		operations["get"] = item.Get
	}
	if item.Head != nil {
		operations["head"] = item.Head
	}
	if item.Options != nil {
		operations["options"] = item.Options
	}
	if item.Patch != nil {
		operations["patch"] = item.Patch
	}
	if item.Post != nil {
		operations["post"] = item.Post
	}
	if item.Put != nil {
		operations["put"] = item.Put
	}
	if item.Trace != nil {
		operations["trace"] = item.Trace
	}
	return operations
}

func normalizeOperation(method string, path string, item *openapi3.PathItem, operation *openapi3.Operation) domain.OpenAPIEndpoint {
	toolID := strings.TrimSpace(operation.OperationID)
	issues := make([]string, 0)
	if toolID == "" {
		toolID = fallbackToolID(method, path)
		issues = append(issues, "operationId missing; generated fallback tool id")
	}

	requestParams, requestIssues := collectRequestParams(item, operation)
	responseFields, responseIssues := collectResponseFields(operation)
	issues = append(issues, requestIssues...)
	issues = append(issues, responseIssues...)
	schemaIssues := validateEndpointSchemas(requestParams, responseFields)
	issues = append(issues, schemaIssues...)

	return domain.OpenAPIEndpoint{
		Method:          strings.ToUpper(method),
		Path:            path,
		OperationID:     operation.OperationID,
		Summary:         operation.Summary,
		ToolIDCandidate: toolID,
		RequestParams:   requestParams,
		ResponseFields:  responseFields,
		Issues:          issues,
		Ready:           toolID != "" && path != "" && method != "" && len(schemaIssues) == 0,
	}
}

func markDuplicateOperationIDs(endpoints []domain.OpenAPIEndpoint) {
	indices := make(map[string][]int)
	for index := range endpoints {
		operationID := strings.TrimSpace(endpoints[index].OperationID)
		if operationID != "" {
			indices[operationID] = append(indices[operationID], index)
		}
	}
	for operationID, duplicates := range indices {
		if len(duplicates) < 2 {
			continue
		}
		for _, index := range duplicates {
			endpoints[index].Ready = false
			endpoints[index].Issues = append(endpoints[index].Issues, "duplicate operationId: "+operationID)
		}
	}
}

func validateEndpointSchemas(
	parameters []domain.ToolParameter,
	responseFields []domain.ToolResponseField,
) []string {
	issues := make([]string, 0)
	for _, parameter := range parameters {
		validateParameterSchema(parameter, "request parameter "+parameter.Name, &issues)
	}
	for _, field := range responseFields {
		validateResponseSchema(field, "response field "+field.Name, &issues)
	}
	return issues
}

func validateParameterSchema(parameter domain.ToolParameter, location string, issues *[]string) {
	if !supportedSchemaType(parameter.Type) {
		*issues = append(*issues, location+" has unsupported schema type: "+parameter.Type)
	}
	if parameter.Type == "array" && parameter.Item == nil {
		*issues = append(*issues, location+" array schema has no items")
	}
	for _, child := range parameter.Children {
		validateParameterSchema(child, location+"."+child.Name, issues)
	}
	if parameter.Item != nil {
		validateParameterSchema(*parameter.Item, location+"[]", issues)
	}
}

func validateResponseSchema(field domain.ToolResponseField, location string, issues *[]string) {
	if !supportedSchemaType(field.Type) {
		*issues = append(*issues, location+" has unsupported schema type: "+field.Type)
	}
	if field.Type == "array" && field.Item == nil {
		*issues = append(*issues, location+" array schema has no items")
	}
	for _, child := range field.Children {
		validateResponseSchema(child, location+"."+child.Name, issues)
	}
	if field.Item != nil {
		validateResponseSchema(*field.Item, location+"[]", issues)
	}
}

func supportedSchemaType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "string", "number", "integer", "boolean", "object", "array", "null":
		return true
	default:
		return false
	}
}

func fallbackToolID(method string, path string) string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return "root." + strings.ToLower(method)
	}

	segments := strings.Split(trimmed, "/")
	parts := make([]string, 0, len(segments)+1)
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			segment = "by-" + segment[1:len(segment)-1]
		}
		parts = append(parts, slugifySegment(segment))
	}
	parts = append(parts, strings.ToLower(method))
	return strings.Join(parts, ".")
}

func slugifySegment(segment string) string {
	var out []rune
	for i, r := range segment {
		switch {
		case unicode.IsUpper(r):
			if i > 0 && len(out) > 0 && out[len(out)-1] != '-' {
				out = append(out, '-')
			}
			out = append(out, unicode.ToLower(r))
		case unicode.IsLower(r), unicode.IsDigit(r):
			out = append(out, r)
		default:
			if len(out) > 0 && out[len(out)-1] != '-' {
				out = append(out, '-')
			}
		}
	}
	return strings.Trim(string(out), "-")
}

func collectRequestParams(item *openapi3.PathItem, operation *openapi3.Operation) ([]domain.ToolParameter, []string) {
	params := make([]domain.ToolParameter, 0)
	issues := make([]string, 0)

	for _, paramRef := range appendPathItemParameters(item, operation) {
		if paramRef == nil || paramRef.Value == nil {
			issues = append(issues, "skipped unresolved parameter reference")
			continue
		}
		param := paramRef.Value
		toolParam := buildToolParameter(param.In, param.Name, param.Schema, param.Required)
		if param.Description != "" {
			toolParam.Description = param.Description
		}
		params = append(params, toolParam)
	}

	if operation.RequestBody == nil || operation.RequestBody.Value == nil {
		return params, issues
	}

	bodyParams, bodyIssues := collectBodyParams(operation.RequestBody.Value)
	params = append(params, bodyParams...)
	issues = append(issues, bodyIssues...)
	return params, issues
}

func appendPathItemParameters(item *openapi3.PathItem, operation *openapi3.Operation) openapi3.Parameters {
	params := make(openapi3.Parameters, 0, len(operation.Parameters))
	if item != nil && len(item.Parameters) > 0 {
		params = append(params, item.Parameters...)
	}
	return append(params, operation.Parameters...)
}

func collectBodyParams(body *openapi3.RequestBody) ([]domain.ToolParameter, []string) {
	mediaType := firstJSONMediaType(body.Content)
	if mediaType == nil || mediaType.Schema == nil || mediaType.Schema.Value == nil {
		return nil, nil
	}

	shape := resolvedSchemaShape(mediaType.Schema)
	if shape.kind != "object" {
		return []domain.ToolParameter{{
			Location: "body",
			Name:     "body",
			Type:     shape.kind,
			Required: body.Required,
		}}, []string{"request body schema is not an object; mapped as a single body parameter"}
	}

	required := requiredSet(shape.required)
	names := sortedSchemaNames(shape.properties)
	params := make([]domain.ToolParameter, 0, len(names))
	for _, name := range names {
		params = append(params, buildToolParameter("body", name, shape.properties[name], required[name]))
	}
	return params, nil
}

func collectResponseFields(operation *openapi3.Operation) ([]domain.ToolResponseField, []string) {
	response := bestResponse(operation.Responses)
	if response == nil || response.Value == nil {
		return nil, nil
	}

	mediaType := firstJSONMediaType(response.Value.Content)
	if mediaType == nil || mediaType.Schema == nil || mediaType.Schema.Value == nil {
		return nil, nil
	}

	shape := resolvedSchemaShape(mediaType.Schema)
	if shape.kind != "object" {
		return []domain.ToolResponseField{{
			Name:        "result",
			Type:        shape.kind,
			Description: shape.description,
		}}, []string{"response schema is not an object; mapped as a single result field"}
	}

	names := sortedSchemaNames(shape.properties)
	fields := make([]domain.ToolResponseField, 0, len(names))
	for _, name := range names {
		fields = append(fields, buildToolResponseField(name, shape.properties[name]))
	}
	return fields, nil
}

func buildToolParameter(location string, name string, schemaRef *openapi3.SchemaRef, required bool) domain.ToolParameter {
	return buildToolParameterDepth(location, name, schemaRef, required, map[*openapi3.Schema]bool{}, 0)
}

func buildToolParameterDepth(location string, name string, schemaRef *openapi3.SchemaRef, required bool, seen map[*openapi3.Schema]bool, depth int) domain.ToolParameter {
	shape := resolvedSchemaShape(schemaRef)
	param := domain.ToolParameter{
		Location:    location,
		Name:        name,
		Type:        shape.kind,
		Required:    required,
		Description: shape.description,
	}
	if schemaRef == nil || schemaRef.Value == nil {
		return withParameterDefault(param)
	}

	schema := schemaRef.Value
	if schema.Default != nil {
		param.DefaultValue = schema.Default
		param.ValueSource = domain.ToolParameterValueSourceSystemDefault
	}
	if depth >= 16 || seen[schema] {
		return withParameterDefault(param)
	}
	seen[schema] = true
	defer delete(seen, schema)
	if param.Type == "object" {
		childRequired := requiredSet(shape.required)
		for _, childName := range sortedSchemaNames(shape.properties) {
			param.Children = append(param.Children, buildToolParameterDepth(location, childName, shape.properties[childName], childRequired[childName], seen, depth+1))
		}
	}
	if param.Type == "array" && shape.items != nil {
		item := buildToolParameterDepth(location, name+"Item", shape.items, false, seen, depth+1)
		item.Location = ""
		item.Name = ""
		item.Required = false
		param.Item = &item
	}
	return withParameterDefault(param)
}

func withParameterDefault(param domain.ToolParameter) domain.ToolParameter {
	if defaultValue, ok := param.SystemDefaultValue(); ok {
		param.ValueSource = domain.ToolParameterValueSourceSystemDefault
		param.DefaultValue = defaultValue
	}
	return param
}

func buildToolResponseField(name string, schemaRef *openapi3.SchemaRef) domain.ToolResponseField {
	return buildToolResponseFieldDepth(name, schemaRef, map[*openapi3.Schema]bool{}, 0)
}

func buildToolResponseFieldDepth(name string, schemaRef *openapi3.SchemaRef, seen map[*openapi3.Schema]bool, depth int) domain.ToolResponseField {
	shape := resolvedSchemaShape(schemaRef)
	field := domain.ToolResponseField{
		Name:        name,
		Type:        shape.kind,
		Description: shape.description,
	}
	if schemaRef == nil || schemaRef.Value == nil {
		return field
	}

	schema := schemaRef.Value
	if depth >= 16 || seen[schema] {
		return field
	}
	seen[schema] = true
	defer delete(seen, schema)
	if field.Type == "object" {
		for _, childName := range sortedSchemaNames(shape.properties) {
			field.Children = append(field.Children, buildToolResponseFieldDepth(childName, shape.properties[childName], seen, depth+1))
		}
	}
	if field.Type == "array" && shape.items != nil {
		item := buildToolResponseFieldDepth("", shape.items, seen, depth+1)
		field.Item = &item
	}
	return field
}

func bestResponse(responses *openapi3.Responses) *openapi3.ResponseRef {
	if responses == nil {
		return nil
	}
	codes := make([]string, 0, len(responses.Map()))
	for code := range responses.Map() {
		if strings.HasPrefix(code, "2") {
			codes = append(codes, code)
		}
	}
	sort.Strings(codes)
	for _, code := range codes {
		return responses.Value(code)
	}
	return nil
}

func firstJSONMediaType(content openapi3.Content) *openapi3.MediaType {
	if content == nil {
		return nil
	}
	for _, key := range []string{"application/json", "application/*+json"} {
		if mediaType := content.Get(key); mediaType != nil {
			return mediaType
		}
	}

	keys := make([]string, 0, len(content))
	for key := range content {
		if strings.Contains(key, "json") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return nil
	}
	return content[keys[0]]
}

func schemaType(schemaRef *openapi3.SchemaRef) string {
	return resolvedSchemaShape(schemaRef).kind
}

func schemaDescription(schemaRef *openapi3.SchemaRef) string {
	if schemaRef == nil || schemaRef.Value == nil {
		return ""
	}
	return schemaRef.Value.Description
}

func requiredSet(required []string) map[string]bool {
	set := make(map[string]bool, len(required))
	for _, name := range required {
		set[name] = true
	}
	return set
}

func sortedSchemaPropertyNames(schema *openapi3.Schema) []string {
	if schema == nil {
		return nil
	}
	return sortedSchemaNames(schema.Properties)
}

func sortedSchemaNames(properties openapi3.Schemas) []string {
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type schemaShape struct {
	kind        string
	description string
	properties  openapi3.Schemas
	required    []string
	items       *openapi3.SchemaRef
}

func resolvedSchemaShape(schemaRef *openapi3.SchemaRef) schemaShape {
	shape := schemaShape{properties: openapi3.Schemas{}}
	if schemaRef == nil || schemaRef.Value == nil {
		return shape
	}
	schema := schemaRef.Value
	for _, part := range schema.AllOf {
		mergeSchemaShape(&shape, resolvedSchemaShape(part))
	}
	direct := schemaShape{
		description: schema.Description,
		properties:  schema.Properties,
		required:    schema.Required,
		items:       schema.Items,
	}
	if schema.Type != nil && len(*schema.Type) > 0 {
		direct.kind = (*schema.Type)[0]
	} else if schema.Items != nil {
		direct.kind = "array"
	} else if len(schema.Properties) > 0 {
		direct.kind = "object"
	}
	mergeSchemaShape(&shape, direct)
	if shape.kind == "" && len(shape.properties) > 0 {
		shape.kind = "object"
	}
	return shape
}

func mergeSchemaShape(target *schemaShape, source schemaShape) {
	if source.kind != "" {
		target.kind = source.kind
	}
	if source.description != "" {
		target.description = source.description
	}
	if target.properties == nil {
		target.properties = openapi3.Schemas{}
	}
	for name, property := range source.properties {
		existing := target.properties[name]
		if existing == nil || schemaType(existing) == "" || schemaType(property) != "" {
			target.properties[name] = property
		}
	}
	if source.items != nil {
		target.items = source.items
	}
	seenRequired := requiredSet(target.required)
	for _, name := range source.required {
		if !seenRequired[name] {
			target.required = append(target.required, name)
			seenRequired[name] = true
		}
	}
}
