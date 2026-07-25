package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

const (
	ErrorCodePromptGenerationFailed = "PROMPT_GENERATION_FAILED"
	ErrorCodePromptObjectWrite      = "PROMPT_OBJECT_WRITE_FAILED"
)

var ErrPromptGeneration = errors.New("prompt generation failed")

type PromptObjectStore interface {
	PutPermanent(context.Context, string, string, []byte, string) (string, error)
	GetPermanent(context.Context, string, string, string) ([]byte, error)
}

type ModelSnapshotSource interface {
	Snapshot(context.Context, string, string) (json.RawMessage, error)
}

type PromptGenerator interface {
	Generate(context.Context, PromptGenerationRequest) (string, error)
}

type PromptGeneratorFunc func(context.Context, PromptGenerationRequest) (string, error)

func (f PromptGeneratorFunc) Generate(ctx context.Context, request PromptGenerationRequest) (string, error) {
	return f(ctx, request)
}

type PromptGenerationRequest struct {
	Agent         Agent
	OperationType string
	Input         string
	ModelSnapshot json.RawMessage
}

type PromptService struct {
	repository *Repository
	objects    PromptObjectStore
	snapshots  ModelSnapshotSource
	generator  PromptGenerator
}

func NewPromptService(repository *Repository, objects PromptObjectStore, snapshots ModelSnapshotSource, generator PromptGenerator) (*PromptService, error) {
	if repository == nil || objects == nil || snapshots == nil || generator == nil {
		return nil, errors.New("prompt repository, object store, model snapshot source, and generator are required")
	}
	return &PromptService{repository: repository, objects: objects, snapshots: snapshots, generator: generator}, nil
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
	output, generationErr := s.generator.Generate(ctx, PromptGenerationRequest{
		Agent: value, OperationType: operationType, Input: input, ModelSnapshot: append(json.RawMessage(nil), snapshot...),
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
