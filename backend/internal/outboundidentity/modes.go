package outboundidentity

import "strings"

// Frozen outbound modes. No third mode, NONE, or SYSTEM identity path is admitted.
const (
	ModeBrokerOBO          Mode = "BROKER_OBO"
	ModeRequestPassthrough Mode = "REQUEST_PASSTHROUGH"
)

// Subject types permitted for user-scoped outbound identity.
const (
	SubjectTypeUser            SubjectType = "USER"
	SubjectTypeExternalSubject SubjectType = "EXTERNAL_SUBJECT"
)

// Credential types accepted by outbound-credentials.v1 (first release).
const (
	CredentialTypeAccessToken CredentialType = "ACCESS_TOKEN"
)

// Schema versions for the four frozen contracts.
const (
	SchemaIdentity     = "outbound-identity.v1"
	SchemaConnection   = "outbound-connection.v1"
	SchemaRequirements = "outbound-requirements.v1"
	SchemaCredentials  = "outbound-credentials.v1"
)

// Machine authentication method for Broker/OBO (T1=A).
const MachineAuthPrivateKeyJWT = "PRIVATE_KEY_JWT"

// Connection migration / readiness states used by descriptors and later packages.
const (
	MigrationStateNone              = "NONE"
	MigrationStateMigrationRequired = "MIGRATION_REQUIRED"
)

type Mode string
type SubjectType string
type CredentialType string

func (m Mode) Valid() bool {
	switch m {
	case ModeBrokerOBO, ModeRequestPassthrough:
		return true
	default:
		return false
	}
}

func (s SubjectType) Valid() bool {
	switch s {
	case SubjectTypeUser, SubjectTypeExternalSubject:
		return true
	default:
		return false
	}
}

func (c CredentialType) Valid() bool {
	return c == CredentialTypeAccessToken
}

func normalizeMode(raw string) (Mode, bool) {
	mode := Mode(strings.TrimSpace(raw))
	return mode, mode.Valid()
}

func normalizeSubjectType(raw string) (SubjectType, bool) {
	subject := SubjectType(strings.TrimSpace(raw))
	return subject, subject.Valid()
}
