package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"actweave/backend/internal/domain"

	"github.com/google/uuid"
)

type CompilationCompiler interface {
	Version() string
	Compile(workflowID string, draftVersion string, draft domain.WorkflowGraphDraft) domain.WorkflowCompilation
}

type CompilationService struct {
	repository      *Repository
	compiler        CompilationCompiler
	compilerVersion string
	outbound        *OutboundRequirementsLoader
}

func NewCompilationService(repository *Repository, compiler CompilationCompiler) (*CompilationService, error) {
	if repository == nil {
		return nil, errors.New("workflow compilation repository is required")
	}
	if compiler == nil {
		return nil, errors.New("workflow compiler is required")
	}
	version := strings.TrimSpace(compiler.Version())
	if version == "" {
		return nil, errors.New("workflow compiler version is required")
	}
	return &CompilationService{
		repository:      repository,
		compiler:        compiler,
		compilerVersion: version,
	}, nil
}

// WithOutboundRequirementsLoader attaches dual-mode requirements enrichment.
func (s *CompilationService) WithOutboundRequirementsLoader(loader *OutboundRequirementsLoader) *CompilationService {
	if s != nil {
		s.outbound = loader
	}
	return s
}

func (s *CompilationService) Compile(
	ctx context.Context,
	workspaceID, capabilityID, compiledBy string,
) (Compilation, error) {
	compiledBy = strings.TrimSpace(compiledBy)
	if !validUUID(compiledBy) {
		return Compilation{}, ErrInvalid
	}
	draft, err := s.repository.GetDraft(ctx, workspaceID, capabilityID)
	if err != nil {
		return Compilation{}, err
	}

	result := compileDraft(s.compiler, draft)
	if result.Status == domain.WorkflowCompilationValid && result.Plan != nil && s.outbound != nil {
		if err := s.outbound.EnrichPlan(ctx, workspaceID, result.Plan); err != nil {
			// Fail closed: surface as invalid compilation with stable message.
			result.Status = domain.WorkflowCompilationInvalid
			result.Plan = nil
			result.Spec = nil
			result.Issues = append(result.Issues, domain.WorkflowCompilationIssue{
				Code:        "outbound-identity-requirements-failed",
				Message:     "工作流出站身份要求无法固化: " + err.Error(),
				Severity:    "error",
				SourceStage: domain.WorkflowIssueStagePlan,
				Suggestion:  "Migrate and verify dual-mode ServiceConnections before compiling.",
			})
		}
	}
	input, err := compilationCreate(result, s.compilerVersion, compiledBy)
	if err != nil {
		return Compilation{}, err
	}
	value, err := s.repository.CreateCompilation(ctx, draft, input)
	if err != nil {
		return Compilation{}, err
	}
	return value, nil
}

func compileDraft(compiler CompilationCompiler, draft Draft) domain.WorkflowCompilation {
	var graph domain.WorkflowGraphDraft
	if err := json.Unmarshal(draft.Graph, &graph); err != nil {
		return invalidGraphCompilation(draft.CapabilityID, draft.DraftVersion)
	}
	if strings.TrimSpace(graph.SchemaVersion) == "" {
		graph.SchemaVersion = draft.SchemaVersion
	}
	return compiler.Compile(draft.CapabilityID, strconv.FormatInt(draft.DraftVersion, 10), graph)
}

func invalidGraphCompilation(capabilityID string, draftVersion int64) domain.WorkflowCompilation {
	return domain.WorkflowCompilation{
		WorkflowID:   capabilityID,
		DraftVersion: strconv.FormatInt(draftVersion, 10),
		Status:       domain.WorkflowCompilationInvalid,
		Issues: []domain.WorkflowCompilationIssue{{
			Code:        "workflow-graph-contract-invalid",
			Message:     "工作流图不符合编译器输入契约",
			Severity:    "error",
			SourceStage: domain.WorkflowIssueStageGraph,
			FieldPath:   "graph",
			Suggestion:  "Save a workflow graph that matches the declared schema version.",
		}},
	}
}

func compilationCreate(
	result domain.WorkflowCompilation,
	compilerVersion, compiledBy string,
) (CompilationCreate, error) {
	status := ""
	switch result.Status {
	case domain.WorkflowCompilationValid:
		if result.Spec == nil || result.Plan == nil {
			return failedCompilationCreate(compilerVersion, compiledBy, "workflow-compiler-output-missing")
		}
		status = "VALID"
	case domain.WorkflowCompilationInvalid:
		status = "INVALID"
	default:
		return failedCompilationCreate(compilerVersion, compiledBy, "workflow-compiler-status-invalid")
	}

	var specValue any
	if result.Spec != nil {
		specValue = result.Spec
	}
	spec, _, err := marshalCompilationObject(specValue)
	if err != nil {
		return CompilationCreate{}, fmt.Errorf("marshal workflow compilation spec: %w", err)
	}
	var planValue any
	if result.Plan != nil {
		planValue = result.Plan
	}
	plan, planHash, err := marshalCompilationObject(planValue)
	if err != nil {
		return CompilationCreate{}, fmt.Errorf("marshal workflow compilation plan: %w", err)
	}
	issues, _, err := marshalCompilationArray(result.Issues)
	if err != nil {
		return CompilationCreate{}, fmt.Errorf("marshal workflow compilation issues: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return CompilationCreate{}, fmt.Errorf("create workflow compilation id: %w", err)
	}
	return CompilationCreate{
		ID:              id.String(),
		CompilerVersion: compilerVersion,
		Status:          status,
		Spec:            spec,
		Plan:            plan,
		Issues:          issues,
		PlanHash:        planHash,
		CompiledBy:      compiledBy,
	}, nil
}

func failedCompilationCreate(
	compilerVersion, compiledBy, code string,
) (CompilationCreate, error) {
	issue := []domain.WorkflowCompilationIssue{{
		Code:        code,
		Message:     "工作流编译器未返回可用产物",
		Severity:    "error",
		SourceStage: domain.WorkflowIssueStagePlan,
	}}
	issues, _, err := marshalCompilationArray(issue)
	if err != nil {
		return CompilationCreate{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return CompilationCreate{}, fmt.Errorf("create failed workflow compilation id: %w", err)
	}
	emptyObject, planHash, err := canonicalJSON(json.RawMessage(`{}`), "object")
	if err != nil {
		return CompilationCreate{}, err
	}
	return CompilationCreate{
		ID:              id.String(),
		CompilerVersion: compilerVersion,
		Status:          "FAILED",
		Spec:            emptyObject,
		Plan:            emptyObject,
		Issues:          issues,
		PlanHash:        planHash,
		CompiledBy:      compiledBy,
	}, nil
}

func marshalCompilationObject(value any) (json.RawMessage, string, error) {
	if value == nil {
		return canonicalJSON(json.RawMessage(`{}`), "object")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	return canonicalJSON(payload, "object")
}

func marshalCompilationArray(value any) (json.RawMessage, string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	if string(payload) == "null" {
		payload = []byte(`[]`)
	}
	return canonicalJSON(payload, "array")
}
