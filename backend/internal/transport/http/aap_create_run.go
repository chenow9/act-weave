package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"actweave/backend/internal/aap"
	"actweave/backend/internal/outboundidentity"
	"actweave/backend/internal/protocolevent"
	"actweave/backend/internal/protocolschema"
	"actweave/backend/internal/transport/sse"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var (
	ErrAAPCreateRunInvalid       = errors.New("AAP create Run request is invalid")
	ErrAAPUnsupportedContentType = errors.New("AAP Run content type is not supported")
	ErrAAPRunIdempotencyConflict = errors.New("AAP Run idempotency key conflicts with another request")
	ErrAAPRunCreationInvalid     = errors.New("AAP Run creator returned an invalid accepted Run")
)

type AAPCreateRunScope struct {
	WorkspaceID string
	AgentID     string
}

type AAPRunContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type AAPRunInputItem struct {
	Type    string              `json:"type"`
	Role    string              `json:"role"`
	Content []AAPRunContentPart `json:"content"`
}

type AAPCreateRunRequest struct {
	ConversationID string            `json:"conversationId,omitempty"`
	Input          []AAPRunInputItem `json:"input"`
	Stream         bool              `json:"stream"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	// outboundCredentials is intentionally absent: transport strips it before
	// business decode and passes write-only raw on AAPCreateRunCommand only.
}

type AAPCreateRunCommand struct {
	Scope          AAPCreateRunScope
	ConversationID string
	Input          []AAPRunInputItem
	Metadata       map[string]string
	IdempotencyKey string
	PrincipalID    string
	TraceID        string
	// OutboundCredentialsRaw is write-only envelope material for BindingAttacher.
	// Never persisted, logged, or included in request hash. Nil when absent.
	OutboundCredentialsRaw json.RawMessage
}

type AAPCreateRunResult struct {
	RunID          string
	ConversationID string
	AcceptedEvent  protocolevent.ProtocolEvent
	Idempotent     bool
}

// AAPRunCreator owns request hashing, idempotency lookup, Run/Conversation
// persistence and the transaction that commits run.accepted. Returning from
// CreateRun means AcceptedEvent is already readable through EventReader.
type AAPRunCreator interface {
	CreateRun(context.Context, AAPCreateRunCommand) (AAPCreateRunResult, error)
}

type AAPCreateRunHandler struct {
	creator  AAPRunCreator
	attacher *AAPEventCatchUp
	encoder  *sse.Encoder
	// bindingAttacher is optional until application wires Vault + requirements.
	// When nil, any non-empty outboundCredentials body is fail-closed (no silent drop).
	bindingAttacher *outboundidentity.BindingAttacher
}

func NewAAPCreateRunHandler(
	creator AAPRunCreator,
	attacher *AAPEventCatchUp,
) (*AAPCreateRunHandler, error) {
	if creator == nil || attacher == nil {
		return nil, ErrAAPCreateRunInvalid
	}
	return &AAPCreateRunHandler{creator: creator, attacher: attacher, encoder: sse.NewEncoder()}, nil
}

// WithBindingAttacher enables Vault attach for REQUEST_PASSTHROUGH create-run envelopes.
func (handler *AAPCreateRunHandler) WithBindingAttacher(a *outboundidentity.BindingAttacher) *AAPCreateRunHandler {
	if handler != nil {
		handler.bindingAttacher = a
	}
	return handler
}

func mapOutboundEntryError(err error) error {
	if err == nil {
		return ErrAAPCreateRunInvalid
	}
	// Preserve stable outbound codes for transport mapping.
	var mapped *outboundidentity.Error
	if errors.As(err, &mapped) {
		return err
	}
	return ErrAAPCreateRunInvalid
}

func (handler *AAPCreateRunHandler) Create(
	c *gin.Context,
	scope AAPCreateRunScope,
	session AAPStreamSession,
) {
	if handler == nil || handler.creator == nil || handler.attacher == nil || c == nil {
		if c != nil {
			RespondError(c, ErrAAPCreateRunInvalid)
		}
		return
	}
	requestContext, ok := RequestContextFrom(c.Request.Context())
	if !ok || !validateCreateRunScope(scope) || !isJSONMediaType(c.GetHeader("Content-Type")) {
		RespondError(c, ErrAAPCreateRunInvalid)
		return
	}
	// Split write-only outboundCredentials before business decode so Token never
	// enters AAPCreateRunRequest / metadata / input maps.
	split, splitErr := ReadOutboundCredentialsBody(c)
	if splitErr != nil {
		RespondError(c, mapOutboundEntryError(splitErr))
		return
	}
	defer split.Zero()

	var request AAPCreateRunRequest
	if err := DecodeBusinessJSON(split.BusinessJSON, &request); err != nil || validateCreateRunRequest(request) != nil {
		RespondError(c, ErrAAPCreateRunInvalid)
		return
	}
	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if _, err := uuid.Parse(idempotencyKey); err != nil {
		RespondError(c, ErrAAPCreateRunInvalid)
		return
	}
	if request.Stream && !acceptsEventStream(c.GetHeader("Accept")) {
		RespondError(c, ErrAAPCreateRunInvalid)
		return
	}
	principalID := strings.TrimSpace(session.Connection.SubjectID)
	if session.Authorization != nil {
		principalID = strings.TrimSpace(session.Authorization.PrincipalID)
	}
	if principalID == "" {
		RespondError(c, ErrAAPCreateRunInvalid)
		return
	}
	// Transfer credentials ownership to the command; split.Zero will not clear
	// the transferred slice once we nil it on the split struct.
	creds := split.CredentialsRaw
	split.CredentialsRaw = nil
	// Fail closed: never silently drop write-only tokens when attacher is not wired.
	// Full requirements-driven attach is performed by BindingAttacher at service boundary
	// once agent requirements are resolved (command carries raw for that step).
	if len(creds) > 0 && handler.bindingAttacher == nil {
		_ = outboundidentity.ZeroCredentialsRaw(creds)
		RespondError(c, outboundidentity.ErrCredentialInvalid)
		return
	}
	result, err := handler.creator.CreateRun(c.Request.Context(), AAPCreateRunCommand{
		Scope: scope, ConversationID: strings.TrimSpace(request.ConversationID),
		Input:    cloneRunInput(request.Input),
		Metadata: cloneRunMetadata(request.Metadata), IdempotencyKey: idempotencyKey,
		PrincipalID: principalID, TraceID: requestContext.TraceID,
		OutboundCredentialsRaw: creds,
	})
	_ = outboundidentity.ZeroCredentialsRaw(creds)
	if err != nil {
		RespondError(c, err)
		return
	}
	if validateCreateRunResult(scope, result, handler.encoder) != nil {
		RespondError(c, ErrAAPRunCreationInvalid)
		return
	}

	if !request.Stream {
		c.Header("ActWeave-Protocol-Version", protocolschema.ProtocolVersion)
		c.JSON(http.StatusAccepted, gin.H{
			"run": gin.H{
				"id": result.RunID, "conversationId": result.ConversationID,
				"agentId": scope.AgentID, "status": "accepted",
			},
			"links":      gin.H{"events": aapRunEventsLink(scope, result.RunID)},
			"idempotent": result.Idempotent,
		})
		return
	}

	handler.attacher.StreamFrom(c, protocolevent.RunScope{
		WorkspaceID: scope.WorkspaceID, AgentID: scope.AgentID,
		ConversationID: result.ConversationID, RunID: result.RunID,
	}, 0, session)
}

func validateCreateRunScope(scope AAPCreateRunScope) bool {
	_, workspaceErr := uuid.Parse(strings.TrimSpace(scope.WorkspaceID))
	_, agentErr := uuid.Parse(strings.TrimSpace(scope.AgentID))
	return workspaceErr == nil && agentErr == nil
}

func validateCreateRunRequest(request AAPCreateRunRequest) error {
	if request.ConversationID != "" {
		if _, err := uuid.Parse(strings.TrimSpace(request.ConversationID)); err != nil {
			return ErrAAPCreateRunInvalid
		}
	}
	if len(request.Input) != 1 || aap.ValidateRunMetadata(request.Metadata) != nil {
		return ErrAAPCreateRunInvalid
	}
	totalText := 0
	for _, item := range request.Input {
		if item.Type != "message" || item.Role != "user" || len(item.Content) == 0 || len(item.Content) > 32 {
			return ErrAAPCreateRunInvalid
		}
		for _, part := range item.Content {
			if part.Type != "text" {
				return ErrAAPUnsupportedContentType
			}
			if strings.TrimSpace(part.Text) == "" {
				return ErrAAPCreateRunInvalid
			}
			totalText += len(part.Text)
			if totalText > 64<<10 {
				return ErrAAPCreateRunInvalid
			}
		}
	}
	return nil
}

func validateCreateRunResult(
	scope AAPCreateRunScope,
	result AAPCreateRunResult,
	encoder *sse.Encoder,
) error {
	event := result.AcceptedEvent
	if encoder == nil || result.RunID == "" || result.ConversationID == "" ||
		event.Type != protocolevent.EventRunAccepted || event.Sequence != 1 ||
		event.WorkspaceID != scope.WorkspaceID || event.AgentID != scope.AgentID ||
		event.ConversationID != result.ConversationID || event.RunID != result.RunID ||
		event.StreamID != "run:"+result.RunID {
		return ErrAAPRunCreationInvalid
	}
	if _, err := uuid.Parse(result.RunID); err != nil {
		return ErrAAPRunCreationInvalid
	}
	if _, err := uuid.Parse(result.ConversationID); err != nil {
		return ErrAAPRunCreationInvalid
	}
	if err := encoder.Encode(io.Discard, event); err != nil {
		return ErrAAPRunCreationInvalid
	}
	return nil
}

func cloneRunInput(input []AAPRunInputItem) []AAPRunInputItem {
	cloned := make([]AAPRunInputItem, len(input))
	for index, item := range input {
		cloned[index] = item
		cloned[index].Content = append([]AAPRunContentPart(nil), item.Content...)
	}
	return cloned
}

func cloneRunMetadata(metadata map[string]string) map[string]string {
	cloned := make(map[string]string, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func acceptsEventStream(value string) bool {
	for _, mediaRange := range strings.Split(value, ",") {
		mediaType := strings.TrimSpace(strings.SplitN(mediaRange, ";", 2)[0])
		if strings.EqualFold(mediaType, "text/event-stream") {
			return true
		}
	}
	return false
}

func isJSONMediaType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

func aapRunEventsLink(scope AAPCreateRunScope, runID string) string {
	return fmt.Sprintf(
		"/api/agent-access/v1/workspaces/%s/agents/%s/runs/%s/events",
		scope.WorkspaceID, scope.AgentID, runID,
	)
}
