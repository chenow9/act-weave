package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

const outboxPublishFailureCode = "OUTBOX_PUBLISH_FAILED"

type OutboxPublisher interface {
	PublishOutbox(context.Context, OutboxEvent) error
}

type OutboxPublisherFunc func(context.Context, OutboxEvent) error

func (function OutboxPublisherFunc) PublishOutbox(ctx context.Context, event OutboxEvent) error {
	return function(ctx, event)
}

type BackoffPolicy interface {
	NextDelay(attempt int) time.Duration
}

type ExponentialBackoff struct {
	Minimum time.Duration
	Maximum time.Duration
}

func (policy ExponentialBackoff) NextDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 30 {
		shift = 30
	}
	delay := float64(policy.Minimum) * math.Pow(2, float64(shift))
	if delay > float64(policy.Maximum) {
		return policy.Maximum
	}
	return time.Duration(delay)
}

type OutboxWorkerConfig struct {
	PollInterval time.Duration
	ClaimLease   time.Duration
	Backoff      BackoffPolicy
}

type OutboxWorker struct {
	repository *OutboxRepository
	publisher  OutboxPublisher
	config     OutboxWorkerConfig
}

func NewOutboxWorker(
	repository *OutboxRepository,
	publisher OutboxPublisher,
	config OutboxWorkerConfig,
) (*OutboxWorker, error) {
	if repository == nil || publisher == nil {
		return nil, errors.New("outbox worker repository and publisher are required")
	}
	if config.PollInterval == 0 {
		config.PollInterval = 250 * time.Millisecond
	}
	if config.ClaimLease == 0 {
		config.ClaimLease = time.Minute
	}
	if config.Backoff == nil {
		config.Backoff = ExponentialBackoff{Minimum: time.Second, Maximum: time.Hour}
	}
	if config.PollInterval < time.Millisecond || config.PollInterval > time.Minute ||
		config.ClaimLease < 100*time.Millisecond || config.ClaimLease > 15*time.Minute ||
		config.Backoff.NextDelay(1) < time.Millisecond || config.Backoff.NextDelay(1) > 24*time.Hour {
		return nil, ErrInvalid
	}
	return &OutboxWorker{repository: repository, publisher: publisher, config: config}, nil
}

func (worker *OutboxWorker) Run(ctx context.Context) error {
	for {
		processed, err := worker.ProcessOne(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) && ctx.Err() != nil {
				return nil
			}
			return err
		}
		if processed {
			continue
		}
		timer := time.NewTimer(worker.config.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func (worker *OutboxWorker) ProcessOne(ctx context.Context) (bool, error) {
	claimID, err := uuid.NewV7()
	if err != nil {
		return false, err
	}
	event, found, err := worker.repository.claimNext(ctx, claimID.String(), worker.config.ClaimLease)
	if err != nil || !found {
		return false, err
	}
	publishErr := worker.publisher.PublishOutbox(ctx, event)
	finalizeContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if publishErr != nil {
		nextAttempt := event.Attempts + 1
		nextAvailable := time.Now().UTC().Add(worker.config.Backoff.NextDelay(nextAttempt))
		if err := worker.repository.markPublishFailed(finalizeContext, event.ID, claimID.String(),
			outboxPublishFailureCode, nextAvailable); err != nil {
			return true, errors.Join(publishErr, err)
		}
		return true, nil
	}
	if err := worker.repository.markPublished(finalizeContext, event.ID, claimID.String()); err != nil {
		return true, err
	}
	return true, nil
}

func (repository *OutboxRepository) claimNext(
	ctx context.Context,
	claimToken string,
	lease time.Duration,
) (OutboxEvent, bool, error) {
	if !validAuditUUID(claimToken) || lease < 100*time.Millisecond || lease > 15*time.Minute {
		return OutboxEvent{}, false, ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return OutboxEvent{}, false, fmt.Errorf("begin claim outbox transaction: %w", err)
	}
	defer tx.Rollback()
	event, err := scanOutboxEvent(tx.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT id FROM outbox_events
			WHERE published_at IS NULL
			  AND available_at <= clock_timestamp()
			  AND (claim_token IS NULL OR claim_expires_at <= clock_timestamp())
			ORDER BY available_at,occurred_at,id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE outbox_events oe
		SET claim_token=$1,claimed_at=clock_timestamp(),
			claim_expires_at=clock_timestamp()+($2 * interval '1 millisecond')
		FROM candidate c WHERE oe.id=c.id
		RETURNING oe.id,oe.workspace_id,oe.aggregate_type,oe.aggregate_id,oe.event_type,
		 oe.payload,oe.schema_version,oe.idempotency_key,oe.occurred_at,oe.available_at,
		 oe.published_at,oe.attempts,oe.last_error,oe.created_at
	`, claimToken, lease.Milliseconds()))
	if errors.Is(err, sql.ErrNoRows) {
		return OutboxEvent{}, false, nil
	}
	if err != nil {
		return OutboxEvent{}, false, mapOutboxWrite("claim outbox event", err)
	}
	if err := tx.Commit(); err != nil {
		return OutboxEvent{}, false, mapOutboxWrite("commit outbox claim", err)
	}
	return event, true, nil
}

func (repository *OutboxRepository) markPublished(
	ctx context.Context,
	eventID, claimToken string,
) error {
	if !validAuditUUID(eventID) || !validAuditUUID(claimToken) {
		return ErrInvalid
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET published_at=clock_timestamp(),attempts=attempts+1,last_error=NULL,
		 claim_token=NULL,claimed_at=NULL,claim_expires_at=NULL
		WHERE id=$1 AND claim_token=$2 AND published_at IS NULL
	`, eventID, claimToken)
	return classifyOutboxClaimUpdate(result, mapOutboxWrite("mark outbox published", err))
}

func (repository *OutboxRepository) markPublishFailed(
	ctx context.Context,
	eventID, claimToken, errorCode string,
	nextAvailable time.Time,
) error {
	errorCode = strings.TrimSpace(errorCode)
	if !validAuditUUID(eventID) || !validAuditUUID(claimToken) ||
		errorCode != outboxPublishFailureCode || nextAvailable.IsZero() {
		return ErrInvalid
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET attempts=attempts+1,last_error=$3,
		 available_at=GREATEST($4,occurred_at),
		 claim_token=NULL,claimed_at=NULL,claim_expires_at=NULL
		WHERE id=$1 AND claim_token=$2 AND published_at IS NULL
	`, eventID, claimToken, errorCode, nextAvailable.UTC())
	return classifyOutboxClaimUpdate(result, mapOutboxWrite("mark outbox publish failed", err))
}

func classifyOutboxClaimUpdate(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrConflict
	}
	return nil
}
