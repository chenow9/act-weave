package openapiimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"actweave/backend/internal/tool"
	"github.com/lib/pq"
)

type ToolIDs struct {
	CapabilityID string
	VersionID    string
}

type ToolIDGenerator func() (ToolIDs, error)

type TransactionalToolCreator interface {
	CreateInTransaction(context.Context, *sql.Tx, tool.CreateInput) (tool.Tool, tool.Version, error)
}

type GenerateToolsRequest struct {
	WorkspaceID string
	ImportID    string
	EndpointIDs []string
	CreatedBy   string
}

type GeneratedTool struct {
	EndpointID string
	Tool       tool.Tool
	Draft      tool.Version
}

type GenerationService struct {
	db     *sql.DB
	tools  TransactionalToolCreator
	newIDs ToolIDGenerator
}

func NewGenerationService(
	db *sql.DB,
	tools TransactionalToolCreator,
	newIDs ToolIDGenerator,
) (*GenerationService, error) {
	if db == nil || tools == nil || newIDs == nil {
		return nil, errors.New("openapi tool generation dependencies are required")
	}
	return &GenerationService{db: db, tools: tools, newIDs: newIDs}, nil
}

func (s *GenerationService) Generate(
	ctx context.Context,
	request GenerateToolsRequest,
) ([]GeneratedTool, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.ImportID = strings.TrimSpace(request.ImportID)
	request.CreatedBy = strings.TrimSpace(request.CreatedBy)
	request.EndpointIDs = normalizeEndpointIDs(request.EndpointIDs)
	if !validUUID(request.WorkspaceID) || !validUUID(request.ImportID) ||
		!validUUID(request.CreatedBy) || len(request.EndpointIDs) == 0 {
		return nil, ErrInvalid
	}
	for _, endpointID := range request.EndpointIDs {
		if !validUUID(endpointID) {
			return nil, ErrInvalid
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin openapi tool generation transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1))`, request.WorkspaceID); err != nil {
		return nil, fmt.Errorf("lock workspace tool names: %w", err)
	}
	var providerID string
	var defaultConnectionID *string
	var status, providerKind, providerStatus string
	if err := tx.QueryRowContext(ctx, `
		SELECT i.provider_id,i.connection_id,i.status,p.provider_kind,p.status
		FROM openapi_imports i
		JOIN capability_providers p
		  ON p.workspace_id=i.workspace_id AND p.id=i.provider_id AND p.deleted_at IS NULL
		WHERE i.workspace_id=$1 AND i.id=$2
		FOR UPDATE OF i,p
	`, request.WorkspaceID, request.ImportID).Scan(
		&providerID, &defaultConnectionID, &status, &providerKind, &providerStatus,
	); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("lock openapi import for generation: %w", err)
	}
	if status != ImportStatusSucceeded || providerKind != "HTTP_OPENAPI" || providerStatus != "ACTIVE" {
		return nil, ErrConflict
	}

	endpoints, err := loadGenerationEndpoints(ctx, tx, request.WorkspaceID, request.ImportID, request.EndpointIDs)
	if err != nil {
		return nil, err
	}
	if len(endpoints) != len(request.EndpointIDs) {
		return nil, ErrNotFound
	}
	generated := make([]GeneratedTool, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if !endpoint.Ready || endpoint.GeneratedCapabilityID != nil {
			return nil, ErrConflict
		}
		ids, err := s.newIDs()
		if err != nil {
			return nil, fmt.Errorf("generate tool identifiers: %w", err)
		}
		if !validUUID(ids.CapabilityID) || !validUUID(ids.VersionID) {
			return nil, ErrInvalid
		}
		actionConfig, err := actionConfigForEndpoint(endpoint)
		if err != nil {
			return nil, ErrInvalid
		}
		nameBase := endpointToolName(endpoint)
		name, err := allocateCapabilityName(ctx, tx, request.WorkspaceID, nameBase)
		if err != nil {
			return nil, err
		}
		slug, err := allocateCapabilitySlug(ctx, tx, request.WorkspaceID, endpointToolSlug(endpoint))
		if err != nil {
			return nil, err
		}
		riskLevel, sideEffectLevel := endpointRisk(endpoint.Method)
		createdTool, draft, err := s.tools.CreateInTransaction(ctx, tx, tool.CreateInput{
			CapabilityID: ids.CapabilityID, InitialVersionID: ids.VersionID,
			WorkspaceID: request.WorkspaceID, ProviderID: providerID,
			DefaultConnectionID: defaultConnectionID, SourceEndpointID: &endpoint.ID,
			Name: name, Slug: slug, Description: endpoint.Summary, CreatedBy: request.CreatedBy,
			Draft: tool.DraftSpec{
				DefaultConnectionID: defaultConnectionID,
				ActionSchemaVersion: "http.v1", ActionConfig: actionConfig,
				InputSchema: endpoint.InputSchema, OutputSchema: endpoint.OutputSchema,
				ErrorMappings: json.RawMessage(`{}`),
				RuntimePolicy: json.RawMessage(`{"timeoutMs":10000,"maxResponseBytes":1048576}`),
				RiskLevel:     riskLevel, SideEffectLevel: sideEffectLevel,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("create generated tool draft: %w", err)
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE openapi_endpoints SET generated_capability_id=$4
			WHERE workspace_id=$1 AND import_id=$2 AND id=$3
			  AND ready=TRUE AND generated_capability_id IS NULL
		`, request.WorkspaceID, request.ImportID, endpoint.ID, createdTool.CapabilityID)
		if err != nil {
			return nil, mapWrite("link generated openapi capability", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("read generated endpoint update count: %w", err)
		}
		if rows != 1 {
			return nil, ErrConflict
		}
		generated = append(generated, GeneratedTool{EndpointID: endpoint.ID, Tool: createdTool, Draft: draft})
	}
	if err := tx.Commit(); err != nil {
		return nil, mapWrite("commit generated openapi tools", err)
	}
	return generated, nil
}

func loadGenerationEndpoints(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID, importID string,
	endpointIDs []string,
) ([]Endpoint, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT `+endpointColumns+` FROM openapi_endpoints e
		WHERE e.workspace_id=$1 AND e.import_id=$2 AND e.id=ANY($3)
		ORDER BY e.path,e.method,e.id FOR UPDATE
	`, workspaceID, importID, pq.Array(endpointIDs))
	if err != nil {
		return nil, fmt.Errorf("lock openapi endpoints for generation: %w", err)
	}
	defer rows.Close()
	values := make([]Endpoint, 0, len(endpointIDs))
	for rows.Next() {
		value, err := scanEndpoint(rows)
		if err != nil {
			return nil, fmt.Errorf("scan generation endpoint: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate generation endpoints: %w", err)
	}
	return values, nil
}

func actionConfigForEndpoint(endpoint Endpoint) (json.RawMessage, error) {
	var schema struct {
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
	}
	if json.Unmarshal(endpoint.InputSchema, &schema) != nil || schema.Properties == nil {
		return nil, ErrInvalid
	}
	required := make(map[string]bool, len(schema.Required))
	for _, name := range schema.Required {
		required[name] = true
	}
	propertyNames := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		propertyNames = append(propertyNames, name)
	}
	sort.Strings(propertyNames)
	parameters := make([]map[string]any, 0, len(propertyNames))
	for _, inputName := range propertyNames {
		property := schema.Properties[inputName]
		location, _ := property["x-actweave-location"].(string)
		parameterName, _ := property["x-actweave-parameter-name"].(string)
		location = strings.ToLower(strings.TrimSpace(location))
		parameterName = strings.TrimSpace(parameterName)
		if parameterName == "" || (location != "path" && location != "query" && location != "header" && location != "body") {
			return nil, ErrInvalid
		}
		parameter := map[string]any{"name": parameterName, "in": location, "input": inputName}
		if required[inputName] || location == "path" {
			parameter["required"] = true
		}
		parameters = append(parameters, parameter)
	}
	action := map[string]any{
		"method": strings.ToUpper(strings.TrimSpace(endpoint.Method)),
		"path":   strings.TrimSpace(endpoint.Path), "parameters": parameters,
	}
	encoded, err := json.Marshal(action)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func endpointRisk(method string) (string, string) {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "GET", "HEAD", "OPTIONS":
		return "LOW", "READ"
	default:
		return "MEDIUM", "WRITE"
	}
}

func endpointToolName(endpoint Endpoint) string {
	if summary := strings.TrimSpace(endpoint.Summary); summary != "" {
		return truncateRunes(summary, 100)
	}
	if operationID := strings.TrimSpace(endpoint.OperationID); operationID != "" {
		return truncateRunes(operationID, 100)
	}
	return truncateRunes(strings.ToUpper(endpoint.Method)+" "+endpoint.Path, 100)
}

func endpointToolSlug(endpoint Endpoint) string {
	base := strings.TrimSpace(endpoint.OperationID)
	if base == "" {
		base = strings.ToLower(endpoint.Method) + "-" + endpoint.Path
	}
	var output []rune
	lastHyphen := false
	for _, character := range strings.ToLower(base) {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			output = append(output, character)
			lastHyphen = false
		case len(output) > 0 && !lastHyphen:
			output = append(output, '-')
			lastHyphen = true
		}
	}
	slug := strings.Trim(string(output), "-")
	if slug == "" || slug[0] < 'a' || slug[0] > 'z' {
		slug = "tool-" + slug
	}
	slug = truncateRunes(slug, 63)
	return strings.Trim(slug, "-")
}

func allocateCapabilityName(ctx context.Context, tx *sql.Tx, workspaceID, base string) (string, error) {
	for suffix := 1; suffix <= 1000; suffix++ {
		candidate := base
		if suffix > 1 {
			candidate = truncateRunes(base, 108-len(fmt.Sprintf(" (%d)", suffix))) + fmt.Sprintf(" (%d)", suffix)
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM capabilities
			 WHERE workspace_id=$1 AND name=$2 AND deleted_at IS NULL)
		`, workspaceID, candidate).Scan(&exists); err != nil {
			return "", fmt.Errorf("check generated capability name: %w", err)
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", ErrConflict
}

func allocateCapabilitySlug(ctx context.Context, tx *sql.Tx, workspaceID, base string) (string, error) {
	for suffix := 1; suffix <= 1000; suffix++ {
		candidate := base
		if suffix > 1 {
			tail := fmt.Sprintf("-%d", suffix)
			candidate = strings.Trim(truncateRunes(base, 63-len(tail)), "-") + tail
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM capabilities
			 WHERE workspace_id=$1 AND slug=$2 AND deleted_at IS NULL)
		`, workspaceID, candidate).Scan(&exists); err != nil {
			return "", fmt.Errorf("check generated capability slug: %w", err)
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", ErrConflict
}

func normalizeEndpointIDs(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func truncateRunes(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}
