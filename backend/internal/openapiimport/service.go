package openapiimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"actweave/backend/internal/domain"
	"github.com/google/uuid"
)

const CurrentParserVersion = "kin-openapi.v1"

type ParseRepository interface {
	CreatePending(context.Context, CreatePendingInput) (Import, error)
	FindLatestByChecksum(context.Context, string, string) (Import, error)
	MarkParsing(context.Context, string, string) (Import, error)
	Complete(context.Context, string, string, CompleteParseInput) (Import, []Endpoint, error)
	Fail(context.Context, string, string) (Import, error)
}

type DocumentParser interface {
	Version() string
	Parse(context.Context, ParseInput) (ParseResult, error)
}

type KinOpenAPIParser struct{}

func (KinOpenAPIParser) Version() string { return CurrentParserVersion }

func (KinOpenAPIParser) Parse(ctx context.Context, input ParseInput) (ParseResult, error) {
	if err := ctx.Err(); err != nil {
		return ParseResult{}, err
	}
	return ParseDocument(input)
}

type IDGenerator func() (string, error)

func UUIDv7Generator() (string, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

type ParseFailure struct{ cause error }

func (*ParseFailure) Error() string         { return ParseErrorCode }
func (failure *ParseFailure) Unwrap() error { return failure.cause }
func (*ParseFailure) Code() string          { return ParseErrorCode }

type ParseService struct {
	repository ParseRepository
	parser     DocumentParser
	newID      IDGenerator
}

func NewParseService(
	repository ParseRepository,
	parser DocumentParser,
	newID IDGenerator,
) (*ParseService, error) {
	if repository == nil {
		return nil, errors.New("openapi parse repository is required")
	}
	if parser == nil || strings.TrimSpace(parser.Version()) == "" {
		return nil, errors.New("versioned openapi parser is required")
	}
	if newID == nil {
		return nil, errors.New("openapi endpoint id generator is required")
	}
	return &ParseService{repository: repository, parser: parser, newID: newID}, nil
}

func (s *ParseService) Parse(ctx context.Context, request ParseRequest) (ParseOutcome, error) {
	request = normalizeParseRequest(request)
	if !validParseRequest(request) {
		return ParseOutcome{}, ErrInvalid
	}
	digest := sha256.Sum256(request.Content)
	checksum := hex.EncodeToString(digest[:])
	var duplicateOfID *string
	duplicate, err := s.repository.FindLatestByChecksum(ctx, request.WorkspaceID, checksum)
	if err == nil {
		duplicateID := duplicate.ID
		duplicateOfID = &duplicateID
	} else if !errors.Is(err, ErrNotFound) {
		return ParseOutcome{}, err
	}

	created, err := s.repository.CreatePending(ctx, CreatePendingInput{
		ID: request.ImportID, WorkspaceID: request.WorkspaceID,
		ProviderID: request.ProviderID, ConnectionID: request.ConnectionID,
		SourceType: request.SourceType, SourceURI: request.SourceURI,
		SourceRevision: request.SourceRevision, FileName: request.FileName, RawObjectID: request.RawObjectID,
		ContentSHA256: checksum, ParserVersion: s.parser.Version(), CreatedBy: request.CreatedBy,
	})
	if err != nil {
		return ParseOutcome{}, err
	}
	parsing, err := s.repository.MarkParsing(ctx, request.WorkspaceID, request.ImportID)
	if err != nil {
		return ParseOutcome{Import: created, DuplicateOfID: duplicateOfID}, err
	}

	parsed, parseErr := s.parser.Parse(ctx, ParseInput{FileName: request.FileName, Content: append([]byte(nil), request.Content...)})
	if parseErr != nil {
		failed, failErr := s.persistFailure(ctx, request.WorkspaceID, request.ImportID)
		if failErr != nil {
			return ParseOutcome{Import: parsing, DuplicateOfID: duplicateOfID}, errors.Join(&ParseFailure{cause: parseErr}, failErr)
		}
		return ParseOutcome{Import: failed, DuplicateOfID: duplicateOfID}, &ParseFailure{cause: parseErr}
	}

	endpoints, conversionErr := s.toPersistedEndpoints(request.WorkspaceID, request.ImportID, parsed.Endpoints)
	if conversionErr != nil {
		failed, failErr := s.persistFailure(ctx, request.WorkspaceID, request.ImportID)
		failure := &ParseFailure{cause: conversionErr}
		if failErr != nil {
			return ParseOutcome{Import: parsing, DuplicateOfID: duplicateOfID}, errors.Join(failure, failErr)
		}
		return ParseOutcome{Import: failed, DuplicateOfID: duplicateOfID}, failure
	}
	completed, persistedEndpoints, err := s.repository.Complete(ctx, request.WorkspaceID, request.ImportID, CompleteParseInput{
		Endpoints: endpoints, ImportIssueCount: len(parsed.Issues),
	})
	if err != nil {
		return ParseOutcome{Import: parsing, DuplicateOfID: duplicateOfID}, err
	}
	return ParseOutcome{Import: completed, Endpoints: persistedEndpoints, DuplicateOfID: duplicateOfID}, nil
}

func (s *ParseService) persistFailure(ctx context.Context, workspaceID, importID string) (Import, error) {
	finalizeContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.repository.Fail(finalizeContext, workspaceID, importID)
}

func (s *ParseService) toPersistedEndpoints(
	workspaceID, importID string,
	parsed []domain.OpenAPIEndpoint,
) ([]Endpoint, error) {
	values := make([]Endpoint, 0, len(parsed))
	for _, parsedEndpoint := range parsed {
		id, err := s.newID()
		if err != nil {
			return nil, fmt.Errorf("generate openapi endpoint id: %w", err)
		}
		if !validUUID(id) {
			return nil, errors.New("generate openapi endpoint id: invalid UUID")
		}
		inputSchema, outputSchema, schemaIssues, err := schemasForEndpoint(parsedEndpoint)
		if err != nil {
			return nil, fmt.Errorf("build endpoint schema: %w", err)
		}
		issues := make([]string, 0, len(parsedEndpoint.Issues)+len(schemaIssues))
		issues = append(issues, parsedEndpoint.Issues...)
		issues = append(issues, schemaIssues...)
		issuesJSON, err := json.Marshal(issues)
		if err != nil {
			return nil, fmt.Errorf("encode endpoint issues: %w", err)
		}
		values = append(values, Endpoint{
			ID: id, WorkspaceID: workspaceID, ImportID: importID,
			Method: strings.ToUpper(strings.TrimSpace(parsedEndpoint.Method)),
			Path:   strings.TrimSpace(parsedEndpoint.Path), OperationID: strings.TrimSpace(parsedEndpoint.OperationID),
			Summary: strings.TrimSpace(parsedEndpoint.Summary), InputSchema: inputSchema,
			OutputSchema: outputSchema, Issues: issuesJSON, Ready: parsedEndpoint.Ready,
		})
	}
	return values, nil
}

func schemasForEndpoint(endpoint domain.OpenAPIEndpoint) (json.RawMessage, json.RawMessage, []string, error) {
	inputProperties := make(map[string]any, len(endpoint.RequestParams))
	required := make([]string, 0)
	issues := make([]string, 0)
	for index, parameter := range endpoint.RequestParams {
		propertyName := strings.TrimSpace(parameter.Name)
		if propertyName == "" {
			propertyName = fmt.Sprintf("parameter_%d", index+1)
			issues = append(issues, "parameter without a name was assigned "+propertyName)
		}
		originalName := propertyName
		if _, exists := inputProperties[propertyName]; exists {
			propertyName = uniquePropertyName(inputProperties, parameter.Location+"_"+originalName)
			issues = append(issues, fmt.Sprintf("duplicate parameter %s was mapped to %s", originalName, propertyName))
		}
		property := parameterSchema(parameter)
		property["x-actweave-location"] = strings.ToLower(strings.TrimSpace(parameter.Location))
		property["x-actweave-parameter-name"] = originalName
		inputProperties[propertyName] = property
		if parameter.Required {
			required = append(required, propertyName)
		}
	}
	sort.Strings(required)
	input := map[string]any{"type": "object", "properties": inputProperties, "additionalProperties": false}
	if len(required) > 0 {
		input["required"] = required
	}

	outputProperties := make(map[string]any, len(endpoint.ResponseFields))
	for index, field := range endpoint.ResponseFields {
		name := strings.TrimSpace(field.Name)
		if name == "" {
			name = fmt.Sprintf("field_%d", index+1)
			issues = append(issues, "response field without a name was assigned "+name)
		}
		name = uniquePropertyName(outputProperties, name)
		outputProperties[name] = responseFieldSchema(field)
	}
	output := map[string]any{"type": "object", "properties": outputProperties}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, nil, nil, err
	}
	outputJSON, err := json.Marshal(output)
	if err != nil {
		return nil, nil, nil, err
	}
	return inputJSON, outputJSON, issues, nil
}

func parameterSchema(parameter domain.ToolParameter) map[string]any {
	value := schemaBase(parameter.Type, parameter.Description)
	if parameter.DefaultValue != nil {
		value["default"] = parameter.DefaultValue
	}
	if len(parameter.Children) > 0 {
		properties, required := make(map[string]any, len(parameter.Children)), make([]string, 0)
		for _, child := range parameter.Children {
			properties[child.Name] = parameterSchema(child)
			if child.Required {
				required = append(required, child.Name)
			}
		}
		value["type"] = "object"
		value["properties"] = properties
		if len(required) > 0 {
			sort.Strings(required)
			value["required"] = required
		}
	}
	if parameter.Item != nil {
		value["type"] = "array"
		value["items"] = parameterSchema(*parameter.Item)
	}
	return value
}

func responseFieldSchema(field domain.ToolResponseField) map[string]any {
	value := schemaBase(field.Type, field.Description)
	if len(field.Children) > 0 {
		properties := make(map[string]any, len(field.Children))
		for _, child := range field.Children {
			properties[child.Name] = responseFieldSchema(child)
		}
		value["type"] = "object"
		value["properties"] = properties
	}
	if field.Item != nil {
		value["type"] = "array"
		value["items"] = responseFieldSchema(*field.Item)
	}
	return value
}

func schemaBase(schemaType, description string) map[string]any {
	value := make(map[string]any)
	if normalized := strings.TrimSpace(schemaType); normalized != "" {
		value["type"] = normalized
	}
	if normalized := strings.TrimSpace(description); normalized != "" {
		value["description"] = normalized
	}
	return value
}

func uniquePropertyName(properties map[string]any, proposed string) string {
	name := strings.TrimSpace(proposed)
	if name == "" {
		name = "field"
	}
	if _, exists := properties[name]; !exists {
		return name
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", name, suffix)
		if _, exists := properties[candidate]; !exists {
			return candidate
		}
	}
}

func normalizeParseRequest(request ParseRequest) ParseRequest {
	request.ImportID = strings.TrimSpace(request.ImportID)
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.ProviderID = normalizeOptional(request.ProviderID)
	request.ConnectionID = normalizeOptional(request.ConnectionID)
	request.SourceType = strings.ToUpper(strings.TrimSpace(request.SourceType))
	request.SourceURI = normalizeOptional(request.SourceURI)
	request.SourceRevision = normalizeOptional(request.SourceRevision)
	request.FileName = strings.TrimSpace(request.FileName)
	request.RawObjectID = strings.TrimSpace(request.RawObjectID)
	request.CreatedBy = strings.TrimSpace(request.CreatedBy)
	request.Content = append([]byte(nil), request.Content...)
	return request
}

func validParseRequest(request ParseRequest) bool {
	return len(request.Content) > 0 && validCreatePending(normalizeCreatePending(CreatePendingInput{
		ID: request.ImportID, WorkspaceID: request.WorkspaceID,
		ProviderID: request.ProviderID, ConnectionID: request.ConnectionID,
		SourceType: request.SourceType, SourceURI: request.SourceURI,
		SourceRevision: request.SourceRevision, FileName: request.FileName, RawObjectID: request.RawObjectID,
		ContentSHA256: strings.Repeat("0", 64), ParserVersion: CurrentParserVersion,
		CreatedBy: request.CreatedBy,
	}))
}
