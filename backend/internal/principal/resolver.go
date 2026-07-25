package principal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

var (
	ErrNotFound = errors.New("Principal Ref was not found")
	ErrConflict = errors.New("Principal Ref conflicts with existing identity")
)

type Resolved struct {
	Ref
	DisplayRef     string
	SystemKey      string
	Active         bool
	TargetResolved bool
	Legacy         bool
}

type Resolver struct{ db *sql.DB }

func NewResolver(db *sql.DB) (*Resolver, error) {
	if db == nil {
		return nil, errors.New("Principal Resolver database is required")
	}
	return &Resolver{db: db}, nil
}

func (resolver *Resolver) Resolve(ctx context.Context, ref Ref) (Resolved, error) {
	if resolver == nil || resolver.db == nil || ctx == nil || ref.Validate() != nil {
		return Resolved{}, ErrInvalid
	}
	var value Resolved
	var systemKey sql.NullString
	var origin string
	err := resolver.db.QueryRowContext(ctx, `
		SELECT p.workspace_id,p.principal_type,p.principal_id,p.system_key,p.origin,
		       CASE p.principal_type
		         WHEN 'USER' THEN u.id IS NOT NULL
		         WHEN 'SERVICE_PRINCIPAL' THEN sp.id IS NOT NULL
		         WHEN 'EXTERNAL_SUBJECT' THEN es.id IS NOT NULL
		         WHEN 'SYSTEM' THEN TRUE ELSE FALSE
		       END AS target_resolved,
		       w.status='ACTIVE' AND w.deleted_at IS NULL AND coalesce(
		       CASE p.principal_type
		         WHEN 'USER' THEN u.status='ACTIVE' AND (
		           w.owner_user_id=p.principal_id
		           OR (wm.user_id IS NOT NULL AND wm.disabled_at IS NULL)
		         )
		         WHEN 'SERVICE_PRINCIPAL' THEN sp.status='ACTIVE'
		         WHEN 'EXTERNAL_SUBJECT' THEN es.status='ACTIVE'
		         WHEN 'SYSTEM' THEN TRUE ELSE FALSE
		       END, FALSE) AS active,
		       CASE p.principal_type
		         WHEN 'USER' THEN coalesce(u.display_name,'')
		         WHEN 'SERVICE_PRINCIPAL' THEN coalesce(sp.name,'')
		         WHEN 'EXTERNAL_SUBJECT' THEN coalesce(es.display_ref,'')
		         WHEN 'SYSTEM' THEN coalesce(p.system_key,'') ELSE ''
		       END AS display_ref
		FROM principal_refs p
		JOIN workspaces w ON w.id=p.workspace_id
		LEFT JOIN workspace_members wm
		  ON p.principal_type='USER' AND wm.workspace_id=p.workspace_id
		 AND wm.user_id=p.principal_id
		LEFT JOIN users u ON p.principal_type='USER' AND u.id=p.principal_id
		LEFT JOIN service_principals sp
		  ON p.principal_type='SERVICE_PRINCIPAL' AND sp.workspace_id=p.workspace_id
		 AND sp.id=p.principal_id
		LEFT JOIN external_subjects es
		  ON p.principal_type='EXTERNAL_SUBJECT' AND es.workspace_id=p.workspace_id
		 AND es.id=p.principal_id
		WHERE p.workspace_id=$1 AND p.principal_type=$2 AND p.principal_id=$3
	`, ref.WorkspaceID, ref.Type, ref.ID).Scan(
		&value.WorkspaceID, &value.Type, &value.ID, &systemKey, &origin,
		&value.TargetResolved, &value.Active, &value.DisplayRef,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Resolved{}, ErrNotFound
	}
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve Principal Ref: %w", err)
	}
	value.SystemKey = systemKey.String
	value.Legacy = origin == "LEGACY_EXECUTION"
	return value, nil
}

func (resolver *Resolver) ResolveInvocation(
	ctx context.Context,
	identity InvocationIdentity,
) (Resolved, *Resolved, error) {
	if identity.Validate() != nil {
		return Resolved{}, nil, ErrInvalid
	}
	actor, err := resolver.Resolve(ctx, identity.Actor)
	if err != nil {
		return Resolved{}, nil, err
	}
	if identity.Subject == nil {
		return actor, nil, nil
	}
	subject, err := resolver.Resolve(ctx, *identity.Subject)
	if err != nil {
		return Resolved{}, nil, err
	}
	return actor, &subject, nil
}

func (resolver *Resolver) RegisterSystem(
	ctx context.Context,
	ref Ref,
	systemKey string,
) (Resolved, error) {
	systemKey = strings.TrimSpace(systemKey)
	if resolver == nil || resolver.db == nil || ctx == nil || ref.Validate() != nil ||
		ref.Type != TypeSystem || !validSystemKey(systemKey) {
		return Resolved{}, ErrInvalid
	}
	_, err := resolver.db.ExecContext(ctx, `
		INSERT INTO principal_refs(
		 workspace_id,principal_type,principal_id,system_key,origin
		) VALUES($1,'SYSTEM',$2,$3,'SYSTEM')
		ON CONFLICT (workspace_id,principal_type,principal_id) DO NOTHING
	`, ref.WorkspaceID, ref.ID, systemKey)
	if err != nil {
		return Resolved{}, mapWrite(err)
	}
	value, err := resolver.Resolve(ctx, ref)
	if err != nil {
		return Resolved{}, err
	}
	if value.SystemKey != systemKey || value.Type != TypeSystem {
		return Resolved{}, ErrConflict
	}
	return value, nil
}

func validSystemKey(value string) bool {
	if len(value) < 1 || len(value) > 120 || value != strings.ToLower(value) {
		return false
	}
	for index, character := range value {
		letter := character >= 'a' && character <= 'z'
		digit := character >= '0' && character <= '9'
		if index == 0 && !letter {
			return false
		}
		if !letter && !digit && !strings.ContainsRune("._:-", character) {
			return false
		}
	}
	return true
}

func mapWrite(err error) error {
	var pqError *pq.Error
	if errors.As(err, &pqError) {
		switch pqError.Code {
		case "23505":
			return ErrConflict
		case "23503", "23514", "22P02":
			return ErrInvalid
		}
	}
	return fmt.Errorf("write Principal Ref: %w", err)
}
