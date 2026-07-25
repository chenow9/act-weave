package provider

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"actweave/backend/internal/serviceendpoint"
	"actweave/backend/internal/tool"

	"github.com/google/uuid"
)

type SyncService struct {
	repository *Repository
	registry   *Registry
}

func NewSyncService(repository *Repository, registry *Registry) (*SyncService, error) {
	if repository == nil || registry == nil {
		return nil, errors.New("provider sync repository and registry are required")
	}
	return &SyncService{repository: repository, registry: registry}, nil
}

// Sync performs driver I/O without an open transaction. Only the initial run
// claim and final asset/result writes use short database transactions.
func (s *SyncService) Sync(ctx context.Context, workspaceID, providerID, actorID string) (SyncRun, error) {
	if !validUUID(workspaceID) || !validUUID(providerID) || !validUUID(actorID) {
		return SyncRun{}, ErrInvalid
	}
	value, err := s.repository.Get(ctx, workspaceID, providerID)
	if err != nil {
		return SyncRun{}, err
	}
	if value.Status == "DISABLED" || value.DiscoveryMode == "MANUAL" {
		return SyncRun{}, ErrConflict
	}
	endpoint, err := serviceendpoint.Parse(value.EndpointConfig)
	if err != nil || !endpoint.HasDiscovery() {
		return SyncRun{}, ErrConflict
	}
	driver, err := s.registry.Resolve(value.Kind)
	if err != nil {
		return SyncRun{}, err
	}
	if err := driver.Validate(ctx, value, nil); err != nil {
		return SyncRun{}, err
	}
	runID, err := uuid.NewV7()
	if err != nil {
		return SyncRun{}, err
	}
	run, err := s.repository.beginSync(ctx, SyncRun{ID: runID.String(), WorkspaceID: workspaceID, ProviderID: providerID, StartedBy: actorID})
	if err != nil {
		return SyncRun{}, err
	}
	assets := make([]Asset, 0)
	cursor := ""
	seen := map[string]struct{}{}
	for {
		page, discoverErr := driver.Discover(ctx, DiscoveryRequest{Provider: value, Cursor: cursor, Limit: 200})
		if discoverErr != nil {
			failed, storeErr := s.repository.failSync(ctx, run, "PROVIDER_DISCOVERY_FAILED")
			if storeErr != nil {
				return SyncRun{}, storeErr
			}
			return failed, nil
		}
		for _, asset := range page.Assets {
			asset.ID, err = newID()
			if err != nil {
				_, _ = s.repository.failSync(ctx, run, "PROVIDER_DISCOVERY_INTERNAL_ERROR")
				return SyncRun{}, err
			}
			asset.WorkspaceID, asset.ProviderID = workspaceID, providerID
			asset.Kind, asset.ExternalID, asset.Name = strings.TrimSpace(asset.Kind), strings.TrimSpace(asset.ExternalID), strings.TrimSpace(asset.Name)
			asset.InputSchema, asset.OutputSchema, asset.Metadata = defaultObject(asset.InputSchema), defaultObject(asset.OutputSchema), defaultObject(asset.Metadata)
			asset.SourceChecksum = strings.TrimSpace(asset.SourceChecksum)
			if !validAsset(asset) {
				failed, storeErr := s.repository.failSync(ctx, run, "PROVIDER_DISCOVERY_INVALID_ASSET")
				if storeErr != nil {
					return SyncRun{}, storeErr
				}
				return failed, nil
			}
			assets = append(assets, asset)
		}
		next := strings.TrimSpace(page.NextCursor)
		if next == "" {
			break
		}
		if next == cursor {
			failed, storeErr := s.repository.failSync(ctx, run, "PROVIDER_DISCOVERY_CURSOR_LOOP")
			if storeErr != nil {
				return SyncRun{}, storeErr
			}
			return failed, nil
		}
		if _, exists := seen[next]; exists {
			failed, storeErr := s.repository.failSync(ctx, run, "PROVIDER_DISCOVERY_CURSOR_LOOP")
			if storeErr != nil {
				return SyncRun{}, storeErr
			}
			return failed, nil
		}
		seen[next] = struct{}{}
		cursor = next
	}
	if cursor != "" {
		run.CursorAfter = &cursor
	}
	completed, err := s.repository.completeSync(ctx, run, assets)
	if err == nil {
		return completed, nil
	}
	if failed, failErr := s.repository.failSync(ctx, run, "PROVIDER_DISCOVERY_STORE_FAILED"); failErr == nil {
		return failed, nil
	}
	return SyncRun{}, err
}

type TransactionalToolCreator interface {
	CreateInTransaction(context.Context, *sql.Tx, tool.CreateInput) (tool.Tool, tool.Version, error)
}

type MaterializationResult struct {
	Asset Asset        `json:"asset"`
	Tool  tool.Tool    `json:"tool"`
	Draft tool.Version `json:"draft"`
}

type MaterializationService struct {
	repository *Repository
	tools      TransactionalToolCreator
}

func NewMaterializationService(repository *Repository, tools TransactionalToolCreator) (*MaterializationService, error) {
	if repository == nil || tools == nil {
		return nil, errors.New("provider materialization repository and tool creator are required")
	}
	return &MaterializationService{repository: repository, tools: tools}, nil
}

func (s *MaterializationService) Materialize(ctx context.Context, workspaceID, providerID, assetID, actorID string, defaultConnectionID *string) (MaterializationResult, error) {
	if !validUUID(workspaceID) || !validUUID(providerID) || !validUUID(assetID) || !validUUID(actorID) ||
		(defaultConnectionID != nil && !validUUID(*defaultConnectionID)) {
		return MaterializationResult{}, ErrInvalid
	}
	tx, err := s.repository.db.BeginTx(ctx, nil)
	if err != nil {
		return MaterializationResult{}, fmt.Errorf("begin provider asset materialization: %w", err)
	}
	defer tx.Rollback()
	asset, providerStatus, providerKind, err := lockMaterializationSource(ctx, tx, workspaceID, providerID, assetID)
	if err != nil {
		return MaterializationResult{}, err
	}
	if providerStatus != "ACTIVE" || providerKind != KindHTTPOpenAPI || asset.Kind != "TOOL" || asset.Status != "ACTIVE" || asset.MaterializedCapabilityID != nil {
		return MaterializationResult{}, ErrConflict
	}
	spec, err := draftFromAsset(asset, defaultConnectionID)
	if err != nil {
		return MaterializationResult{}, err
	}
	capabilityID, err := newID()
	if err != nil {
		return MaterializationResult{}, err
	}
	versionID, err := newID()
	if err != nil {
		return MaterializationResult{}, err
	}
	created, draft, err := s.tools.CreateInTransaction(ctx, tx, tool.CreateInput{
		CapabilityID: capabilityID, InitialVersionID: versionID, WorkspaceID: workspaceID, ProviderID: providerID,
		SourceAssetID: &asset.ID, DefaultConnectionID: defaultConnectionID, Name: asset.Name,
		Slug: materializedSlug(asset), Description: asset.Description, CreatedBy: actorID, Draft: spec,
	})
	if err != nil {
		return MaterializationResult{}, fmt.Errorf("create materialized tool: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE provider_assets SET materialized_capability_id=$4,status='MATERIALIZED'
		WHERE workspace_id=$1 AND provider_id=$2 AND id=$3 AND status='ACTIVE' AND materialized_capability_id IS NULL`, workspaceID, providerID, assetID, capabilityID); err != nil {
		return MaterializationResult{}, mapWrite("link materialized provider asset", err)
	}
	asset.MaterializedCapabilityID = &capabilityID
	asset.Status = "MATERIALIZED"
	if err := tx.Commit(); err != nil {
		return MaterializationResult{}, mapWrite("commit provider asset materialization", err)
	}
	return MaterializationResult{Asset: asset, Tool: created, Draft: draft}, nil
}

func lockMaterializationSource(ctx context.Context, tx *sql.Tx, wid, pid, aid string) (Asset, string, Kind, error) {
	var asset Asset
	var input, output, metadata []byte
	var revision sql.NullString
	var kind string
	var providerStatus string
	err := tx.QueryRowContext(ctx, `SELECT `+assetColumns+`,p.status,p.provider_kind FROM provider_assets a
		JOIN capability_providers p ON p.workspace_id=a.workspace_id AND p.id=a.provider_id
		WHERE a.workspace_id=$1 AND a.provider_id=$2 AND a.id=$3 AND p.deleted_at IS NULL FOR UPDATE OF a,p`, wid, pid, aid).Scan(
		&asset.ID, &asset.WorkspaceID, &asset.ProviderID, &asset.Kind, &asset.ExternalID, &asset.Name, &asset.Description,
		&input, &output, &metadata, &revision, &asset.SourceChecksum, &asset.MaterializedCapabilityID, &asset.Status,
		&asset.DiscoveredAt, &asset.LastSeenAt, &providerStatus, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return Asset{}, "", "", ErrNotFound
	}
	if err != nil {
		return Asset{}, "", "", fmt.Errorf("lock provider asset: %w", err)
	}
	asset.InputSchema = input
	asset.OutputSchema = output
	asset.Metadata = metadata
	if revision.Valid {
		asset.SourceRevision = revision.String
	}
	return asset, providerStatus, Kind(kind), nil
}

func draftFromAsset(asset Asset, connectionID *string) (tool.DraftSpec, error) {
	var metadata struct {
		ActionConfig         json.RawMessage `json:"actionConfig"`
		ErrorMappings        json.RawMessage `json:"errorMappings"`
		RuntimePolicy        json.RawMessage `json:"runtimePolicy"`
		RiskLevel            string          `json:"riskLevel"`
		SideEffectLevel      string          `json:"sideEffectLevel"`
		RequiresConfirmation bool            `json:"requiresConfirmation"`
	}
	if json.Unmarshal(asset.Metadata, &metadata) != nil || !jsonObject(metadata.ActionConfig) {
		return tool.DraftSpec{}, ErrInvalid
	}
	if len(metadata.ActionConfig) == 0 {
		return tool.DraftSpec{}, ErrInvalid
	}
	if len(metadata.ErrorMappings) == 0 {
		metadata.ErrorMappings = json.RawMessage(`{}`)
	}
	if len(metadata.RuntimePolicy) == 0 {
		metadata.RuntimePolicy = json.RawMessage(`{"timeoutMs":10000,"maxResponseBytes":1048576}`)
	}
	if metadata.RiskLevel == "" {
		metadata.RiskLevel = "LOW"
	}
	if metadata.SideEffectLevel == "" {
		metadata.SideEffectLevel = "READ"
	}
	return tool.DraftSpec{ProviderAssetID: &asset.ID, DefaultConnectionID: connectionID, ActionSchemaVersion: "http.v1",
		ActionConfig: metadata.ActionConfig, InputSchema: asset.InputSchema, OutputSchema: asset.OutputSchema,
		ErrorMappings: metadata.ErrorMappings, RuntimePolicy: metadata.RuntimePolicy, RiskLevel: metadata.RiskLevel,
		SideEffectLevel: metadata.SideEffectLevel, RequiresConfirmation: metadata.RequiresConfirmation}, nil
}

func materializedSlug(asset Asset) string {
	base := strings.ToLower(asset.ExternalID)
	var b strings.Builder
	dash := false
	for _, r := range base {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
		} else if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	value := strings.Trim(b.String(), "-")
	if value == "" {
		value = "tool"
	}
	suffix := strings.ReplaceAll(asset.ID, "-", "")
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	return value + "-" + suffix
}

func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate provider identifier: %w", err)
	}
	return id.String(), nil
}
