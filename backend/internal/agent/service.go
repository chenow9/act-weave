package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	ErrorCodePromptGenerationFailed  = "PROMPT_GENERATION_FAILED"
	ErrorCodePromptObjectWrite       = "PROMPT_OBJECT_WRITE_FAILED"
	ErrorCodePromptOutputInvalid     = "PROMPT_OUTPUT_INVALID"
	ErrorCodePromptGenerationTimeout = "PROMPT_GENERATION_TIMEOUT"
	ErrorCodePromptModelUnavailable  = "PROMPT_MODEL_UNAVAILABLE"
)

var (
	ErrPromptGeneration       = errors.New("prompt generation failed")
	ErrPromptModelUnavailable = errors.New("prompt model unavailable")
	ErrPromptOutputInvalid    = errors.New("prompt output invalid")
)

type PromptObjectStore interface {
	PutPermanent(context.Context, string, string, []byte, string) (string, error)
	GetPermanent(context.Context, string, string, string) ([]byte, error)
	// PutPreview writes an EXPIRING sensitive prompt-preview object that shares
	// retentionUntil with the CREATE_PREVIEW Run (created_at + 30 days).
	PutPreview(ctx context.Context, workspaceID, kind string, content []byte, createdBy string, retentionUntil time.Time) (string, error)
}

type ModelSnapshotSource interface {
	Snapshot(context.Context, string, string) (json.RawMessage, error)
}

// ModelAvailabilitySource resolves a model snapshot only when the config is
// usable for create-preview generation (same workspace, not deleted, VERIFIED,
// credential available). Implementations map failures to ErrPromptModelUnavailable
// or ErrNotFound without leaking cross-workspace existence details.
type ModelAvailabilitySource interface {
	AvailableSnapshot(context.Context, string, string) (json.RawMessage, error)
}

type PromptGenerator interface {
	Generate(context.Context, PromptGenerationRequest) (string, error)
}

type PromptGeneratorFunc func(context.Context, PromptGenerationRequest) (string, error)

func (f PromptGeneratorFunc) Generate(ctx context.Context, request PromptGenerationRequest) (string, error) {
	return f(ctx, request)
}

// PromptGenerationRequest is the shared generator boundary. Prefer explicit
// WorkspaceID/ModelConfigID/AgentID; Agent is retained for existing agent paths.
type PromptGenerationRequest struct {
	WorkspaceID   string
	ModelConfigID string
	AgentID       *string
	Agent         Agent
	OperationType string
	Input         string
	ModelSnapshot json.RawMessage
}

type PromptService struct {
	repository   *Repository
	objects      PromptObjectStore
	snapshots    ModelSnapshotSource
	availability ModelAvailabilitySource
	generator    PromptGenerator
}

func NewPromptService(repository *Repository, objects PromptObjectStore, snapshots ModelSnapshotSource, generator PromptGenerator) (*PromptService, error) {
	if repository == nil || objects == nil || snapshots == nil || generator == nil {
		return nil, errors.New("prompt repository, object store, model snapshot source, and generator are required")
	}
	availability, _ := snapshots.(ModelAvailabilitySource)
	return &PromptService{
		repository: repository, objects: objects, snapshots: snapshots,
		availability: availability, generator: generator,
	}, nil
}

// WithModelAvailability installs the create-preview model gate. Optional for
// tests that inject AvailableSnapshot via the snapshots value itself.
func (s *PromptService) WithModelAvailability(source ModelAvailabilitySource) *PromptService {
	if s != nil {
		s.availability = source
	}
	return s
}

// Run records the input and PromptRun before invoking the model. No database
// transaction spans Generate. Preview is a normal operation type and therefore
// has the same permanent run history even when its result is never accepted.
func (s *PromptService) Run(ctx context.Context, workspaceID, agentID, operationType, input, traceID, createdBy string) (PromptRun, string, error) {
	value, err := s.repository.Get(ctx, workspaceID, agentID)
	if err != nil {
		return PromptRun{}, "", err
	}
	snapshot, err := s.snapshots.Snapshot(ctx, workspaceID, value.ModelConfigID)
	if err != nil {
		return PromptRun{}, "", fmt.Errorf("snapshot prompt model: %w", err)
	}
	inputObjectID, err := s.objects.PutPermanent(ctx, workspaceID, "PROMPT_INPUT", []byte(input), createdBy)
	if err != nil {
		return PromptRun{}, "", fmt.Errorf("store prompt input: %w", err)
	}
	runID, err := uuid.NewV7()
	if err != nil {
		return PromptRun{}, "", err
	}
	inputSHA256 := promptContentHash(input)
	run, err := s.repository.StartPromptRun(ctx, NewPromptRun{
		ID: runID.String(), WorkspaceID: workspaceID, AgentID: &agentID,
		OperationType: operationType, ModelConfigID: value.ModelConfigID,
		ModelSnapshot: snapshot, InputObjectID: inputObjectID, InputSHA256: inputSHA256,
		InputLength: int64(len([]byte(input))), TraceID: traceID, CreatedBy: createdBy,
	})
	if err != nil {
		return PromptRun{}, "", err
	}
	agentIDCopy := agentID
	output, generationErr := s.generator.Generate(ctx, PromptGenerationRequest{
		WorkspaceID: workspaceID, ModelConfigID: value.ModelConfigID, AgentID: &agentIDCopy,
		Agent: value, OperationType: operationType, Input: input,
		ModelSnapshot: append(json.RawMessage(nil), snapshot...),
	})
	if generationErr != nil {
		code := ErrorCodePromptGenerationFailed
		failed, persistErr := s.repository.CompletePromptRun(ctx, workspaceID, run.ID, nil, nil, nil, &code)
		if persistErr != nil {
			return PromptRun{}, "", errors.Join(ErrPromptGeneration, persistErr)
		}
		return failed, "", ErrPromptGeneration
	}
	outputObjectID, err := s.objects.PutPermanent(ctx, workspaceID, "PROMPT_OUTPUT", []byte(output), createdBy)
	if err != nil {
		code := ErrorCodePromptObjectWrite
		failed, persistErr := s.repository.CompletePromptRun(ctx, workspaceID, run.ID, nil, nil, nil, &code)
		if persistErr != nil {
			return PromptRun{}, "", errors.Join(err, persistErr)
		}
		return failed, "", err
	}
	outputSHA256 := promptContentHash(output)
	outputLength := int64(len([]byte(output)))
	completed, err := s.repository.CompletePromptRun(ctx, workspaceID, run.ID,
		&outputObjectID, &outputSHA256, &outputLength, nil)
	if err != nil {
		return PromptRun{}, "", err
	}
	return completed, output, nil
}

// RunCreatePreview generates a create-time prompt enhancement without an Agent
// ID. It never creates Agent/Revision rows; model invocation is outside any DB
// transaction; each call creates a new CREATE_PREVIEW Run.
func (s *PromptService) RunCreatePreview(
	ctx context.Context,
	workspaceID, modelConfigID, input, traceID, createdBy string,
) (PromptRun, string, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	modelConfigID = strings.TrimSpace(modelConfigID)
	input = strings.TrimSpace(input)
	traceID = strings.TrimSpace(traceID)
	createdBy = strings.TrimSpace(createdBy)
	if workspaceID == "" || modelConfigID == "" || input == "" || traceID == "" || createdBy == "" {
		return PromptRun{}, "", ErrInvalid
	}

	var snapshot json.RawMessage
	var err error
	if s.availability != nil {
		snapshot, err = s.availability.AvailableSnapshot(ctx, workspaceID, modelConfigID)
	} else {
		snapshot, err = s.snapshots.Snapshot(ctx, workspaceID, modelConfigID)
	}
	if err != nil {
		return PromptRun{}, "", err
	}

	createdAt, expiresAt, err := s.repository.NextPreviewTimestamps(ctx)
	if err != nil {
		return PromptRun{}, "", err
	}
	inputObjectID, err := s.objects.PutPreview(ctx, workspaceID, "PROMPT_INPUT", []byte(input), createdBy, expiresAt)
	if err != nil {
		return PromptRun{}, "", fmt.Errorf("store preview prompt input: %w", err)
	}
	runID, err := uuid.NewV7()
	if err != nil {
		return PromptRun{}, "", err
	}
	inputSHA256 := promptContentHash(input)
	run, err := s.repository.StartPromptRun(ctx, NewPromptRun{
		ID: runID.String(), WorkspaceID: workspaceID, AgentID: nil,
		OperationType: PromptOperationCreatePreview, ModelConfigID: modelConfigID,
		ModelSnapshot: snapshot, InputObjectID: inputObjectID, InputSHA256: inputSHA256,
		InputLength: int64(len([]byte(input))), TraceID: traceID, CreatedBy: createdBy,
		FixedCreatedAt: &createdAt,
	})
	if err != nil {
		return PromptRun{}, "", err
	}
	if run.ExpiresAt == nil || !run.ExpiresAt.Equal(expiresAt.UTC().Truncate(time.Microsecond)) &&
		!run.ExpiresAt.Truncate(time.Microsecond).Equal(expiresAt.UTC().Truncate(time.Microsecond)) {
		// Allow microsecond DB truncation differences; still require non-nil.
		if run.ExpiresAt == nil {
			return PromptRun{}, "", fmt.Errorf("create preview run missing expires_at")
		}
	}

	output, generationErr := s.generator.Generate(ctx, PromptGenerationRequest{
		WorkspaceID: workspaceID, ModelConfigID: modelConfigID, AgentID: nil,
		OperationType: PromptOperationCreatePreview, Input: input,
		ModelSnapshot: append(json.RawMessage(nil), snapshot...),
	})
	if generationErr != nil {
		code := mapGenerationErrorCode(generationErr)
		failed, persistErr := s.repository.CompletePromptRun(ctx, workspaceID, run.ID, nil, nil, nil, &code)
		if persistErr != nil {
			return PromptRun{}, "", errors.Join(ErrPromptGeneration, persistErr)
		}
		if errors.Is(generationErr, context.DeadlineExceeded) || code == ErrorCodePromptGenerationTimeout {
			return failed, "", fmt.Errorf("%w: %v", ErrPromptGeneration, generationErr)
		}
		return failed, "", ErrPromptGeneration
	}
	output = strings.TrimSpace(output)
	if output == "" {
		code := ErrorCodePromptOutputInvalid
		failed, persistErr := s.repository.CompletePromptRun(ctx, workspaceID, run.ID, nil, nil, nil, &code)
		if persistErr != nil {
			return PromptRun{}, "", errors.Join(ErrPromptOutputInvalid, persistErr)
		}
		return failed, "", ErrPromptOutputInvalid
	}

	outputObjectID, err := s.objects.PutPreview(ctx, workspaceID, "PROMPT_OUTPUT", []byte(output), createdBy, expiresAt)
	if err != nil {
		code := ErrorCodePromptObjectWrite
		failed, persistErr := s.repository.CompletePromptRun(ctx, workspaceID, run.ID, nil, nil, nil, &code)
		if persistErr != nil {
			return PromptRun{}, "", errors.Join(err, persistErr)
		}
		return failed, "", err
	}
	outputSHA256 := promptContentHash(output)
	outputLength := int64(len([]byte(output)))
	completed, err := s.repository.CompletePromptRun(ctx, workspaceID, run.ID,
		&outputObjectID, &outputSHA256, &outputLength, nil)
	if err != nil {
		return PromptRun{}, "", err
	}
	return completed, output, nil
}

func mapGenerationErrorCode(err error) string {
	if err == nil {
		return ErrorCodePromptGenerationFailed
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrorCodePromptGenerationTimeout
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "timeout") || strings.Contains(message, "deadline") {
		return ErrorCodePromptGenerationTimeout
	}
	return ErrorCodePromptGenerationFailed
}

func (s *PromptService) Accept(ctx context.Context, workspaceID, runID, acceptedBy string, expectedAgentLockVersion int64) (PromptRun, PromptRevision, error) {
	run, err := s.repository.GetPromptRun(ctx, workspaceID, runID)
	if err != nil {
		return PromptRun{}, PromptRevision{}, err
	}
	if run.OutputObjectID == nil {
		return PromptRun{}, PromptRevision{}, ErrConflict
	}
	content, err := s.objects.GetPermanent(ctx, workspaceID, *run.OutputObjectID, acceptedBy)
	if err != nil {
		return PromptRun{}, PromptRevision{}, fmt.Errorf("load accepted prompt output: %w", err)
	}
	revisionID, err := uuid.NewV7()
	if err != nil {
		return PromptRun{}, PromptRevision{}, err
	}
	return s.repository.AcceptPromptRun(ctx, workspaceID, runID, revisionID.String(), string(content), acceptedBy, expectedAgentLockVersion)
}

func promptContentHash(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}
