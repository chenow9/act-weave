package workflowcompiler

import "actweave/backend/internal/domain"

func newIssue(code string, message string, severity string, stage domain.WorkflowIssueStage, nodeID string, edgeID string, portKey string, fieldPath string, suggestion ...string) domain.WorkflowCompilationIssue {
	issue := domain.WorkflowCompilationIssue{
		Code:        code,
		Message:     message,
		Severity:    severity,
		SourceStage: stage,
		NodeID:      nodeID,
		EdgeID:      edgeID,
		PortKey:     portKey,
		FieldPath:   fieldPath,
	}
	if len(suggestion) > 0 {
		issue.Suggestion = suggestion[0]
	}
	return issue
}

func graphIssue(code string, message string, nodeID string, edgeID string, portKey string, fieldPath string, suggestion ...string) domain.WorkflowCompilationIssue {
	return newIssue(code, message, "error", domain.WorkflowIssueStageGraph, nodeID, edgeID, portKey, fieldPath, suggestion...)
}

func specIssue(code string, message string, nodeID string, fieldPath string, suggestion ...string) domain.WorkflowCompilationIssue {
	return newIssue(code, message, "error", domain.WorkflowIssueStageSpec, nodeID, "", "", fieldPath, suggestion...)
}
