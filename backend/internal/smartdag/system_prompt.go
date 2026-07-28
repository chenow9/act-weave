package smartdag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
)

// Smart orchestration System Prompt scenario (D16). Terminal users never edit this
// in Console generate UI; only admin-fixed active version is used.

const (
	// DefaultSystemPromptID is the bootstrap active prompt identity.
	DefaultSystemPromptID = "smart-orchestration.default"
	// DefaultSystemPromptVersion is the bootstrap version number.
	DefaultSystemPromptVersion = 1
)

// defaultSystemPromptContent is the platform-default Smart Orchestration system prompt.
// Kept stable so promptHash is deterministic for fixtures and audits.
const defaultSystemPromptContent = `You are the ACTWEAVE Smart Orchestration generator.
Generate only workflow.graph.v1 JSON using published Tool IDs from the provided catalog.
Allowed node types: Start, Tool, Transform, Condition, Approval, End.
Never invent toolId values. Never emit SubWorkflow, Parallel, ForEach, or HTTP nodes.
Every graph must include exactly the required Start and End structure with valid edges.
Respond with structured graph JSON only when producing a draft update.`

// SystemPrompt is a versioned admin-fixed prompt used for generation (D16).
type SystemPrompt struct {
	ID      string
	Version int
	Content string
	// Hash is sha256 hex of Content (lowercase).
	Hash string
}

// GenerationAuditMeta carries prompt identity for turn/session audit records.
type GenerationAuditMeta struct {
	PromptID   string `json:"promptId"`
	PromptHash string `json:"promptHash"`
}

// SystemPromptStore loads the active Smart Orchestration system prompt.
type SystemPromptStore interface {
	Active(ctx context.Context) (SystemPrompt, error)
}

// MemorySystemPromptStore is an in-memory bootstrap store with one active version.
type MemorySystemPromptStore struct {
	mu     sync.RWMutex
	active SystemPrompt
}

// NewMemorySystemPromptStore returns a store seeded with the platform default.
func NewMemorySystemPromptStore() *MemorySystemPromptStore {
	prompt := DefaultSystemPrompt()
	return &MemorySystemPromptStore{active: prompt}
}

// NewMemorySystemPromptStoreWith returns a store with the given active prompt.
// Hash is computed if empty.
func NewMemorySystemPromptStoreWith(prompt SystemPrompt) (*MemorySystemPromptStore, error) {
	prompt.ID = strings.TrimSpace(prompt.ID)
	prompt.Content = strings.TrimSpace(prompt.Content)
	if prompt.ID == "" || prompt.Content == "" || prompt.Version <= 0 {
		return nil, errors.New("system prompt id, version, and content are required")
	}
	if prompt.Hash == "" {
		prompt.Hash = PromptHash(prompt.Content)
	}
	return &MemorySystemPromptStore{active: prompt}, nil
}

// Active returns the current admin-fixed prompt.
func (s *MemorySystemPromptStore) Active(context.Context) (SystemPrompt, error) {
	if s == nil {
		return SystemPrompt{}, errors.New("system prompt store is nil")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.active.ID == "" || s.active.Content == "" {
		return SystemPrompt{}, errors.New("no active smart orchestration system prompt")
	}
	return s.active, nil
}

// SetActive replaces the active prompt (admin path; not Console user generate UI).
func (s *MemorySystemPromptStore) SetActive(prompt SystemPrompt) error {
	if s == nil {
		return errors.New("system prompt store is nil")
	}
	prompt.ID = strings.TrimSpace(prompt.ID)
	prompt.Content = strings.TrimSpace(prompt.Content)
	if prompt.ID == "" || prompt.Content == "" || prompt.Version <= 0 {
		return errors.New("system prompt id, version, and content are required")
	}
	if prompt.Hash == "" {
		prompt.Hash = PromptHash(prompt.Content)
	}
	s.mu.Lock()
	s.active = prompt
	s.mu.Unlock()
	return nil
}

// DefaultSystemPrompt returns the bootstrap active prompt with stable hash.
func DefaultSystemPrompt() SystemPrompt {
	content := defaultSystemPromptContent
	return SystemPrompt{
		ID:      DefaultSystemPromptID,
		Version: DefaultSystemPromptVersion,
		Content: content,
		Hash:    PromptHash(content),
	}
}

// PromptHash returns lowercase sha256 hex of content (D16 audit field).
func PromptHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// AuditMetaFromPrompt builds audit fields from an active prompt.
func AuditMetaFromPrompt(prompt SystemPrompt) GenerationAuditMeta {
	hash := prompt.Hash
	if hash == "" {
		hash = PromptHash(prompt.Content)
	}
	return GenerationAuditMeta{
		PromptID:   prompt.ID,
		PromptHash: hash,
	}
}

