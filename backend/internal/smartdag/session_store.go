package smartdag

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// MemorySessionStore is an in-memory SessionStore for unit tests and light wiring.
type MemorySessionStore struct {
	mu       sync.Mutex
	sessions map[string]GenerateSession // workspaceID/sessionID
	turns    map[string][]GenerateTurn  // workspaceID/sessionID
}

// NewMemorySessionStore constructs an empty memory store.
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{
		sessions: make(map[string]GenerateSession),
		turns:    make(map[string][]GenerateTurn),
	}
}

func sessionKey(workspaceID, sessionID string) string {
	return workspaceID + "/" + sessionID
}

// CreateSession inserts a new session.
func (s *MemorySessionStore) CreateSession(_ context.Context, session GenerateSession) (GenerateSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(session.WorkspaceID, session.ID)
	if _, exists := s.sessions[key]; exists {
		return GenerateSession{}, ErrInvalid
	}
	if session.Constraints == nil {
		session.Constraints = json.RawMessage(`{}`)
	}
	if session.Status == "" {
		session.Status = SessionStatusOpen
	}
	if session.LockVersion == 0 {
		session.LockVersion = 1
	}
	s.sessions[key] = session
	s.turns[key] = nil
	return session, nil
}

// GetSession loads a session by workspace + id.
func (s *MemorySessionStore) GetSession(_ context.Context, workspaceID, sessionID string) (GenerateSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionKey(workspaceID, sessionID)]
	if !ok {
		return GenerateSession{}, ErrSessionNotFound
	}
	return session, nil
}

// CloseSession marks session CLOSED.
func (s *MemorySessionStore) CloseSession(_ context.Context, workspaceID, sessionID string, closedAt time.Time) (GenerateSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(workspaceID, sessionID)
	session, ok := s.sessions[key]
	if !ok {
		return GenerateSession{}, ErrSessionNotFound
	}
	if session.Status == SessionStatusClosed {
		return session, nil
	}
	session.Status = SessionStatusClosed
	session.ClosedAt = &closedAt
	session.UpdatedAt = closedAt
	session.LockVersion++
	s.sessions[key] = session
	return session, nil
}

// SetSessionWorkflow binds the created workflow to the session.
func (s *MemorySessionStore) SetSessionWorkflow(_ context.Context, workspaceID, sessionID, workflowID string) (GenerateSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(workspaceID, sessionID)
	session, ok := s.sessions[key]
	if !ok {
		return GenerateSession{}, ErrSessionNotFound
	}
	wf := workflowID
	session.WorkflowID = &wf
	session.UpdatedAt = time.Now().UTC()
	session.LockVersion++
	s.sessions[key] = session
	return session, nil
}

// CreateTurn appends a turn record.
func (s *MemorySessionStore) CreateTurn(_ context.Context, turn GenerateTurn) (GenerateTurn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(turn.WorkspaceID, turn.SessionID)
	if _, ok := s.sessions[key]; !ok {
		return GenerateTurn{}, ErrSessionNotFound
	}
	s.turns[key] = append(s.turns[key], turn)
	return turn, nil
}

// ListTurns returns turns ordered by turn_index ascending.
func (s *MemorySessionStore) ListTurns(_ context.Context, workspaceID, sessionID string) ([]GenerateTurn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(workspaceID, sessionID)
	if _, ok := s.sessions[key]; !ok {
		return nil, ErrSessionNotFound
	}
	src := s.turns[key]
	out := make([]GenerateTurn, len(src))
	copy(out, src)
	sort.Slice(out, func(i, j int) bool {
		if out[i].TurnIndex == out[j].TurnIndex {
			return out[i].ID < out[j].ID
		}
		return out[i].TurnIndex < out[j].TurnIndex
	})
	return out, nil
}

// NextTurnIndex returns the next 1-based turn index.
func (s *MemorySessionStore) NextTurnIndex(_ context.Context, workspaceID, sessionID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(workspaceID, sessionID)
	if _, ok := s.sessions[key]; !ok {
		return 0, ErrSessionNotFound
	}
	maxIndex := 0
	for _, turn := range s.turns[key] {
		if turn.TurnIndex > maxIndex {
			maxIndex = turn.TurnIndex
		}
	}
	return maxIndex + 1, nil
}

// CountSessions is a test helper.
func (s *MemorySessionStore) CountSessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}
