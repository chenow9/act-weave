package agentaccess

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type RunConcurrencyQuotaConfig struct {
	Workspace  int
	Agent      int
	Client     int
	Subject    int
	RetryAfter time.Duration
}

func DefaultRunConcurrencyQuotaConfig() RunConcurrencyQuotaConfig {
	return RunConcurrencyQuotaConfig{
		Workspace: 1000, Agent: 100, Client: 50, Subject: 20,
		RetryAfter: 5 * time.Second,
	}
}

type PostgresRunConcurrencyQuota struct {
	db     *sql.DB
	config RunConcurrencyQuotaConfig
	now    func() time.Time
}

func NewPostgresRunConcurrencyQuota(
	db *sql.DB,
	config RunConcurrencyQuotaConfig,
) (*PostgresRunConcurrencyQuota, error) {
	if db == nil || config.Workspace < 1 || config.Agent < 1 || config.Client < 1 ||
		config.Subject < 1 || config.RetryAfter <= 0 {
		return nil, ErrDataPlaneQuotaInvalid
	}
	return &PostgresRunConcurrencyQuota{
		db: db, config: config, now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (quota *PostgresRunConcurrencyQuota) Allow(
	ctx context.Context,
	request DataPlaneQuotaRequest,
) (DataPlaneQuotaDecision, error) {
	request = normalizeQuotaRequest(request)
	if quota == nil || quota.db == nil || ctx == nil || ctx.Err() != nil ||
		!validQuotaRequest(request) {
		return DataPlaneQuotaDecision{}, ErrDataPlaneQuotaInvalid
	}
	if request.Operation != QuotaRunCreate {
		return DataPlaneQuotaDecision{}, nil
	}
	var workspaceCount, agentCount, clientCount, subjectCount int
	err := quota.db.QueryRowContext(ctx, `
		SELECT
		 COUNT(*) FILTER (WHERE workspace_id=$1),
		 COUNT(*) FILTER (WHERE workspace_id=$1 AND agent_id=$2),
		 COUNT(*) FILTER (WHERE workspace_id=$1 AND client_id=$3),
		 COUNT(*) FILTER (WHERE workspace_id=$1 AND
		   COALESCE(subject_id,triggered_by_id)=$4)
		FROM agent_runs
		WHERE workspace_id=$1 AND status IN ('PENDING','RUNNING','WAITING_CONFIRMATION')
	`, request.WorkspaceID, request.AgentID, request.ClientID, request.SubjectID).Scan(
		&workspaceCount, &agentCount, &clientCount, &subjectCount,
	)
	if err != nil {
		return DataPlaneQuotaDecision{}, errors.Join(ErrDataPlaneQuotaInvalid, err)
	}
	limits := []int{quota.config.Workspace, quota.config.Agent, quota.config.Client, quota.config.Subject}
	counts := []int{workspaceCount, agentCount, clientCount, subjectCount}
	selectedLimit, remaining := limits[0], limits[0]-counts[0]
	for index := range limits {
		left := limits[index] - counts[index]
		if left < remaining {
			selectedLimit, remaining = limits[index], left
		}
		if counts[index] >= limits[index] {
			now := quota.now().UTC()
			return DataPlaneQuotaDecision{
				Limit: limits[index], Remaining: 0,
				ResetAt: now.Add(quota.config.RetryAfter), RetryAfter: quota.config.RetryAfter,
			}, ErrDataPlaneQuotaExceeded
		}
	}
	return DataPlaneQuotaDecision{Limit: selectedLimit, Remaining: remaining}, nil
}

type CompositeDataPlaneQuota struct {
	quotas []DataPlaneQuota
}

func NewCompositeDataPlaneQuota(quotas ...DataPlaneQuota) (*CompositeDataPlaneQuota, error) {
	if len(quotas) == 0 {
		return nil, ErrDataPlaneQuotaInvalid
	}
	for _, quota := range quotas {
		if quota == nil {
			return nil, ErrDataPlaneQuotaInvalid
		}
	}
	return &CompositeDataPlaneQuota{quotas: append([]DataPlaneQuota(nil), quotas...)}, nil
}

func (quota *CompositeDataPlaneQuota) Allow(
	ctx context.Context,
	request DataPlaneQuotaRequest,
) (DataPlaneQuotaDecision, error) {
	if quota == nil || len(quota.quotas) == 0 {
		return DataPlaneQuotaDecision{}, ErrDataPlaneQuotaInvalid
	}
	var selected DataPlaneQuotaDecision
	for _, delegate := range quota.quotas {
		decision, err := delegate.Allow(ctx, request)
		if err != nil {
			return decision, err
		}
		if decision.Limit > 0 && (selected.Limit == 0 || decision.Remaining < selected.Remaining) {
			selected = decision
		}
	}
	return selected, nil
}
