package principal

import (
	"errors"
	"strings"

	"github.com/google/uuid"
)

type Type string

const (
	TypeUser             Type = "USER"
	TypeServicePrincipal Type = "SERVICE_PRINCIPAL"
	TypeExternalSubject  Type = "EXTERNAL_SUBJECT"
	TypeSystem           Type = "SYSTEM"
)

var ErrInvalid = errors.New("Principal Ref is invalid")

// Ref is the stable, Workspace-scoped identity reference persisted by domain
// records. ID remains the source identity UUID; Type prevents collisions
// between User, Service Principal, External Subject, and System namespaces.
type Ref struct {
	WorkspaceID string
	Type        Type
	ID          string
}

func (ref Ref) Validate() error {
	if !canonicalUUID(ref.WorkspaceID) || !canonicalUUID(ref.ID) || !knownType(ref.Type) {
		return ErrInvalid
	}
	return nil
}

func (ref Ref) LegacyPair() (string, string) {
	return string(ref.Type), ref.ID
}

func RefFromLegacy(workspaceID, principalType, principalID string) (Ref, error) {
	ref := Ref{
		WorkspaceID: strings.TrimSpace(workspaceID),
		Type:        Type(strings.ToUpper(strings.TrimSpace(principalType))),
		ID:          strings.TrimSpace(principalID),
	}
	return ref, ref.Validate()
}

// InvocationIdentity expresses the caller (Actor) separately from the
// represented resource owner (Subject). A pure Service Principal or System
// call has no Subject; an internal User can be both Actor and Subject.
type InvocationIdentity struct {
	Actor   Ref
	Subject *Ref
}

func NewInvocationIdentity(actor Ref, subject *Ref) (InvocationIdentity, error) {
	if actor.Validate() != nil || actor.Type == TypeExternalSubject {
		return InvocationIdentity{}, ErrInvalid
	}
	result := InvocationIdentity{Actor: actor}
	if subject == nil {
		return result, nil
	}
	if subject.Validate() != nil || subject.WorkspaceID != actor.WorkspaceID ||
		subject.Type == TypeSystem {
		return InvocationIdentity{}, ErrInvalid
	}
	copyValue := *subject
	result.Subject = &copyValue
	return result, nil
}

func (identity InvocationIdentity) Validate() error {
	_, err := NewInvocationIdentity(identity.Actor, identity.Subject)
	return err
}

func knownType(value Type) bool {
	switch value {
	case TypeUser, TypeServicePrincipal, TypeExternalSubject, TypeSystem:
		return true
	default:
		return false
	}
}

func canonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}
