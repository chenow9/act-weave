package openapiimport

import (
	"encoding/json"
	"time"
)

const (
	ImportStatusPending   = "PENDING"
	ImportStatusParsing   = "PARSING"
	ImportStatusSucceeded = "SUCCEEDED"
	ImportStatusFailed    = "FAILED"

	SourceTypeFile = "FILE"
	SourceTypeURL  = "URL"
	SourceTypeRaw  = "RAW"

	ParseErrorCode = "OPENAPI_PARSE_FAILED"
)

type Import struct {
	ID             string
	WorkspaceID    string
	ProviderID     *string
	ConnectionID   *string
	SourceType     string
	SourceURI      *string
	SourceRevision *string
	FileName       string
	RawObjectID    string
	ContentSHA256  string
	ParserVersion  string
	Status         string
	TotalEndpoints int
	ReadyEndpoints int
	IssueCount     int
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Endpoint struct {
	ID                    string
	WorkspaceID           string
	ImportID              string
	Method                string
	Path                  string
	OperationID           string
	Summary               string
	InputSchema           json.RawMessage
	OutputSchema          json.RawMessage
	Issues                json.RawMessage
	Ready                 bool
	GeneratedCapabilityID *string
}

type CreatePendingInput struct {
	ID             string
	WorkspaceID    string
	ProviderID     *string
	ConnectionID   *string
	SourceType     string
	SourceURI      *string
	SourceRevision *string
	FileName       string
	RawObjectID    string
	ContentSHA256  string
	ParserVersion  string
	CreatedBy      string
}

type CompleteParseInput struct {
	Endpoints        []Endpoint
	ImportIssueCount int
}

type ParseRequest struct {
	ImportID       string
	WorkspaceID    string
	ProviderID     *string
	ConnectionID   *string
	SourceType     string
	SourceURI      *string
	SourceRevision *string
	FileName       string
	RawObjectID    string
	Content        []byte
	CreatedBy      string
}

type ParseOutcome struct {
	Import        Import
	Endpoints     []Endpoint
	DuplicateOfID *string
}
