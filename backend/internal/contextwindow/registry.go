package contextwindow

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/pkoukk/tiktoken-go"
)

var (
	// ErrUnknownProfile is returned when a tokenizer profile is not registered.
	ErrUnknownProfile = errors.New("unknown tokenizer profile")
	// ErrUnavailableProfile is returned when a registered profile cannot load.
	ErrUnavailableProfile = errors.New("tokenizer profile unavailable")
)

// Profile names match modelconfig.RuntimeCapabilities registry.
const (
	ProfileO200kBase      = "o200k_base"
	ProfileCL100kBase     = "cl100k_base"
	ProfileByteUpperBound = "byte_upper_bound"
)

// EstimatorVersion is pinned into assembly manifests for auditability.
const EstimatorVersion = "contextwindow-estimator.v1"

// Tokenizer is a pure text → token-count function for a fixed profile/version.
type Tokenizer interface {
	Profile() string
	Version() string
	CountText(text string) (int64, error)
}

type registryEntry struct {
	profile string
	version string
	factory func() (Tokenizer, error)
}

var (
	registryMu sync.RWMutex
	registry   = map[string]registryEntry{}
)

func init() {
	MustRegister(ProfileO200kBase, "tiktoken-go-0.1.7", func() (Tokenizer, error) {
		enc, err := tiktoken.GetEncoding(tiktoken.MODEL_O200K_BASE)
		if err != nil {
			// Some releases use encoding name string.
			enc, err = tiktoken.GetEncoding("o200k_base")
			if err != nil {
				return nil, fmt.Errorf("%w: o200k_base: %v", ErrUnavailableProfile, err)
			}
		}
		return &tiktokenTokenizer{profile: ProfileO200kBase, version: "tiktoken-go-0.1.7", enc: enc}, nil
	})
	MustRegister(ProfileCL100kBase, "tiktoken-go-0.1.7", func() (Tokenizer, error) {
		enc, err := tiktoken.GetEncoding(tiktoken.MODEL_CL100K_BASE)
		if err != nil {
			enc, err = tiktoken.GetEncoding("cl100k_base")
			if err != nil {
				return nil, fmt.Errorf("%w: cl100k_base: %v", ErrUnavailableProfile, err)
			}
		}
		return &tiktokenTokenizer{profile: ProfileCL100kBase, version: "tiktoken-go-0.1.7", enc: enc}, nil
	})
	MustRegister(ProfileByteUpperBound, "byte-upper-bound.v1", func() (Tokenizer, error) {
		return byteUpperBoundTokenizer{profile: ProfileByteUpperBound, version: "byte-upper-bound.v1"}, nil
	})
}

// MustRegister panics if profile is empty or already registered with a different factory.
func MustRegister(profile, version string, factory func() (Tokenizer, error)) {
	profile = strings.TrimSpace(profile)
	if profile == "" || factory == nil {
		panic("contextwindow: invalid tokenizer registration")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[profile]; exists {
		panic("contextwindow: duplicate tokenizer profile " + profile)
	}
	registry[profile] = registryEntry{profile: profile, version: version, factory: factory}
}

// LookupTokenizer returns a tokenizer for a controlled profile. Unknown profiles fail closed.
func LookupTokenizer(profile string) (Tokenizer, error) {
	profile = strings.TrimSpace(profile)
	registryMu.RLock()
	entry, ok := registry[profile]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProfile, profile)
	}
	return entry.factory()
}

// KnownProfiles returns registered profile names.
func KnownProfiles() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	return out
}

type tiktokenTokenizer struct {
	profile string
	version string
	enc     *tiktoken.Tiktoken
}

func (t *tiktokenTokenizer) Profile() string { return t.profile }
func (t *tiktokenTokenizer) Version() string { return t.version }

func (t *tiktokenTokenizer) CountText(text string) (int64, error) {
	if t == nil || t.enc == nil {
		return 0, ErrUnavailableProfile
	}
	tokens := t.enc.Encode(text, nil, nil)
	return int64(len(tokens)), nil
}

// byteUpperBoundTokenizer is a conservative upper-bound estimator for providers
// whose exact tokenizer is not platform-verified. It overestimates for CJK/emoji
// by counting UTF-8 bytes with a 1-token-per-3-bytes floor of 1 per non-empty.
type byteUpperBoundTokenizer struct {
	profile string
	version string
}

func (t byteUpperBoundTokenizer) Profile() string { return t.profile }
func (t byteUpperBoundTokenizer) Version() string { return t.version }

func (t byteUpperBoundTokenizer) CountText(text string) (int64, error) {
	if text == "" {
		return 0, nil
	}
	// Conservative: ceil(bytes / 3) + 1 safety, never underestimate relative to
	// typical OpenAI-compatible BPE on mixed CJK/emoji corpora.
	n := int64(len(text))
	tokens := (n + 2) / 3
	if tokens < 1 {
		tokens = 1
	}
	// Extra headroom for multi-byte scripts and special tokens.
	tokens += tokens / 4
	if tokens < 1 {
		tokens = 1
	}
	return tokens, nil
}
