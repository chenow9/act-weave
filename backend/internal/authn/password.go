// Package authn provides local password authentication primitives and session
// orchestration. HTTP transport concerns belong outside this package.
package authn

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const PasswordAlgorithmArgon2id = "ARGON2ID"

var (
	ErrInvalidPasswordHash = errors.New("invalid Argon2id password hash")
	ErrWeakPasswordConfig  = errors.New("Argon2id configuration is below the security baseline")
)

// Argon2idParams are persisted inside the PHC-style encoded hash so parameters
// can be upgraded without invalidating existing credentials.
type Argon2idParams struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

func DefaultArgon2idParams() Argon2idParams {
	return Argon2idParams{
		MemoryKiB:   64 * 1024,
		Iterations:  3,
		Parallelism: 2,
		SaltLength:  16,
		KeyLength:   32,
	}
}

// PasswordManager hashes and verifies local passwords using bounded Argon2id
// parameters. The reader is injectable only to make salt-generation failures
// testable.
type PasswordManager struct {
	params Argon2idParams
	random io.Reader
}

func NewPasswordManager(params Argon2idParams) (*PasswordManager, error) {
	return newPasswordManager(params, rand.Reader)
}

func newPasswordManager(params Argon2idParams, random io.Reader) (*PasswordManager, error) {
	if err := validateCurrentParams(params); err != nil {
		return nil, err
	}
	if random == nil {
		return nil, errors.New("password salt source is required")
	}
	return &PasswordManager{params: params, random: random}, nil
}

func (m *PasswordManager) Hash(password string) (string, error) {
	if password == "" {
		return "", errors.New("password must not be empty")
	}
	salt := make([]byte, m.params.SaltLength)
	if _, err := io.ReadFull(m.random, salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	return encodePassword(password, salt, m.params), nil
}

// Verify returns whether the password matches and whether the stored hash
// should be replaced with one using the manager's current parameters.
func (m *PasswordManager) Verify(password string, encoded string) (bool, bool, error) {
	parsed, salt, expected, err := parseEncodedPassword(encoded)
	if err != nil {
		return false, false, err
	}
	actual := argon2.IDKey(
		[]byte(password),
		salt,
		parsed.Iterations,
		parsed.MemoryKiB,
		parsed.Parallelism,
		parsed.KeyLength,
	)
	valid := subtle.ConstantTimeCompare(actual, expected) == 1
	needsRehash := valid && parsed != m.params
	return valid, needsRehash, nil
}

func encodePassword(password string, salt []byte, params Argon2idParams) string {
	hash := argon2.IDKey(
		[]byte(password),
		salt,
		params.Iterations,
		params.MemoryKiB,
		params.Parallelism,
		params.KeyLength,
	)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		params.MemoryKiB,
		params.Iterations,
		params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
}

func parseEncodedPassword(encoded string) (Argon2idParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Argon2idParams{}, nil, nil, ErrInvalidPasswordHash
	}
	version, err := parsePrefixedUint(parts[2], "v=")
	if err != nil || version != argon2.Version {
		return Argon2idParams{}, nil, nil, ErrInvalidPasswordHash
	}

	var params Argon2idParams
	parameterParts := strings.Split(parts[3], ",")
	if len(parameterParts) != 3 {
		return Argon2idParams{}, nil, nil, ErrInvalidPasswordHash
	}
	memory, err := parsePrefixedUint(parameterParts[0], "m=")
	if err != nil || memory > uint64(^uint32(0)) {
		return Argon2idParams{}, nil, nil, ErrInvalidPasswordHash
	}
	iterations, err := parsePrefixedUint(parameterParts[1], "t=")
	if err != nil || iterations > uint64(^uint32(0)) {
		return Argon2idParams{}, nil, nil, ErrInvalidPasswordHash
	}
	parallelism, err := parsePrefixedUint(parameterParts[2], "p=")
	if err != nil || parallelism > uint64(^uint8(0)) {
		return Argon2idParams{}, nil, nil, ErrInvalidPasswordHash
	}
	params.MemoryKiB = uint32(memory)
	params.Iterations = uint32(iterations)
	params.Parallelism = uint8(parallelism)

	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return Argon2idParams{}, nil, nil, ErrInvalidPasswordHash
	}
	hash, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return Argon2idParams{}, nil, nil, ErrInvalidPasswordHash
	}
	params.SaltLength = uint32(len(salt))
	params.KeyLength = uint32(len(hash))
	if err := validateStoredParams(params); err != nil {
		return Argon2idParams{}, nil, nil, ErrInvalidPasswordHash
	}
	return params, salt, hash, nil
}

func parsePrefixedUint(value string, prefix string) (uint64, error) {
	if !strings.HasPrefix(value, prefix) {
		return 0, ErrInvalidPasswordHash
	}
	return strconv.ParseUint(strings.TrimPrefix(value, prefix), 10, 64)
}

func validateCurrentParams(params Argon2idParams) error {
	if params.MemoryKiB < 19*1024 || params.MemoryKiB > 256*1024 ||
		params.Iterations < 2 || params.Iterations > 10 ||
		params.Parallelism < 1 || params.Parallelism > 8 ||
		params.SaltLength < 16 || params.SaltLength > 64 ||
		params.KeyLength < 32 || params.KeyLength > 64 {
		return ErrWeakPasswordConfig
	}
	return nil
}

func validateStoredParams(params Argon2idParams) error {
	if params.MemoryKiB < 8*1024 || params.MemoryKiB > 256*1024 ||
		params.Iterations < 1 || params.Iterations > 10 ||
		params.Parallelism < 1 || params.Parallelism > 8 ||
		params.SaltLength < 8 || params.SaltLength > 64 ||
		params.KeyLength < 16 || params.KeyLength > 64 {
		return ErrInvalidPasswordHash
	}
	return nil
}
