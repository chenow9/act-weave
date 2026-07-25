package agentaccessauth

import (
	"context"
	"errors"
	"strings"
	"sync"
)

var ErrSecurityChangesClosed = errors.New("AAP security change source is closed")

type SecurityChange struct {
	WorkspaceID     string
	AgentID         string
	ClientID        string
	GrantID         string
	SecurityVersion int64
}

type SecurityChangeSubscription interface {
	Changes() <-chan SecurityChange
	Close() error
}

type SecurityChangeSource interface {
	Subscribe(context.Context, StreamBinding) (SecurityChangeSubscription, error)
}

type SecurityChangeStats struct {
	Published           uint64
	Delivered           uint64
	Coalesced           uint64
	ActiveSubscriptions int
}

type InProcessSecurityChanges struct {
	mu            sync.Mutex
	subscriptions map[uint64]*securityChangeSubscription
	nextID        uint64
	closed        bool
	stats         SecurityChangeStats
}

func NewInProcessSecurityChanges() *InProcessSecurityChanges {
	return &InProcessSecurityChanges{
		subscriptions: make(map[uint64]*securityChangeSubscription),
	}
}

func (source *InProcessSecurityChanges) Subscribe(
	ctx context.Context,
	binding StreamBinding,
) (SecurityChangeSubscription, error) {
	binding = normalizeStreamBinding(binding)
	if source == nil || ctx == nil || !validStreamBinding(binding) {
		return nil, ErrStreamRevalidationInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source.mu.Lock()
	if source.closed {
		source.mu.Unlock()
		return nil, ErrSecurityChangesClosed
	}
	source.nextID++
	subscription := &securityChangeSubscription{
		source: source, id: source.nextID, binding: binding,
		changes: make(chan SecurityChange, 1),
	}
	source.subscriptions[subscription.id] = subscription
	source.stats.ActiveSubscriptions++
	source.mu.Unlock()

	stop := context.AfterFunc(ctx, func() { _ = subscription.Close() })
	subscription.stopMu.Lock()
	subscription.stopContext = stop
	closed := subscription.closed
	subscription.stopMu.Unlock()
	if closed {
		stop()
	}
	return subscription, nil
}

func (source *InProcessSecurityChanges) Publish(change SecurityChange) error {
	change.WorkspaceID = normalizeIdentity(change.WorkspaceID)
	change.AgentID = normalizeIdentity(change.AgentID)
	change.ClientID = normalizeIdentity(change.ClientID)
	change.GrantID = normalizeIdentity(change.GrantID)
	if source == nil || !validSecurityChange(change) {
		return ErrStreamRevalidationInvalid
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.closed {
		return ErrSecurityChangesClosed
	}
	source.stats.Published++
	for _, subscription := range source.subscriptions {
		if !securityChangeMatches(change, subscription.binding) {
			continue
		}
		delivery := change
		select {
		case subscription.changes <- delivery:
			source.stats.Delivered++
		default:
			queued := <-subscription.changes
			if queued.SecurityVersion > delivery.SecurityVersion {
				delivery = queued
			}
			subscription.changes <- delivery
			source.stats.Coalesced++
		}
	}
	return nil
}

func validSecurityChange(change SecurityChange) bool {
	return validStableIdentity(change.WorkspaceID) &&
		(change.AgentID == "" || validStableIdentity(change.AgentID)) &&
		(change.ClientID == "" || validStableIdentity(change.ClientID)) &&
		(change.GrantID == "" || (validStableIdentity(change.GrantID) && change.ClientID != "")) &&
		change.SecurityVersion > 0
}

func (source *InProcessSecurityChanges) Stats() SecurityChangeStats {
	if source == nil {
		return SecurityChangeStats{}
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.stats
}

func (source *InProcessSecurityChanges) Close() error {
	if source == nil {
		return ErrStreamRevalidationInvalid
	}
	source.mu.Lock()
	if source.closed {
		source.mu.Unlock()
		return nil
	}
	source.closed = true
	subscriptions := make([]*securityChangeSubscription, 0, source.stats.ActiveSubscriptions)
	for _, subscription := range source.subscriptions {
		subscriptions = append(subscriptions, subscription)
	}
	source.mu.Unlock()
	for _, subscription := range subscriptions {
		_ = subscription.Close()
	}
	return nil
}

type securityChangeSubscription struct {
	source      *InProcessSecurityChanges
	id          uint64
	binding     StreamBinding
	changes     chan SecurityChange
	closeOnce   sync.Once
	stopMu      sync.Mutex
	stopContext func() bool
	closed      bool
}

func (subscription *securityChangeSubscription) Changes() <-chan SecurityChange {
	if subscription == nil {
		return nil
	}
	return subscription.changes
}

func (subscription *securityChangeSubscription) Close() error {
	if subscription == nil || subscription.source == nil {
		return ErrStreamRevalidationInvalid
	}
	subscription.closeOnce.Do(func() {
		source := subscription.source
		source.mu.Lock()
		if _, exists := source.subscriptions[subscription.id]; exists {
			delete(source.subscriptions, subscription.id)
			source.stats.ActiveSubscriptions--
		}
		close(subscription.changes)
		source.mu.Unlock()

		subscription.stopMu.Lock()
		subscription.closed = true
		stop := subscription.stopContext
		subscription.stopMu.Unlock()
		if stop != nil {
			stop()
		}
	})
	return nil
}

func securityChangeMatches(change SecurityChange, binding StreamBinding) bool {
	return change.WorkspaceID == binding.WorkspaceID &&
		(change.AgentID == "" || change.AgentID == binding.AgentID) &&
		(change.ClientID == "" || change.ClientID == binding.ClientID) &&
		(change.GrantID == "" || change.GrantID == binding.GrantID)
}

func normalizeIdentity(value string) string {
	return strings.TrimSpace(value)
}
