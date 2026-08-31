package cappackage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"actweave/backend/internal/connection"
	"actweave/backend/internal/provider"
	"actweave/backend/internal/tool"
	"actweave/backend/internal/workflow"

	"github.com/google/uuid"
)

type ToolStore interface {
	List(context.Context, string) ([]tool.Tool, error)
	Get(context.Context, string, string) (tool.Tool, error)
	Create(context.Context, tool.CreateInput) (tool.Tool, tool.Version, error)
	UpdateMetadata(context.Context, string, string, tool.MetadataUpdate) (tool.Tool, error)
	ListVersions(context.Context, string, string) ([]tool.Version, error)
	UpdateDraft(context.Context, string, string, string, tool.DraftUpdate) (tool.Version, error)
	CreateDraftFromPublished(context.Context, string, string, string, string, string) (tool.Version, error)
}

type WorkflowStore interface {
	List(context.Context, string) ([]workflow.Workflow, error)
	Get(context.Context, string, string) (workflow.Workflow, error)
	Create(context.Context, workflow.CreateInput) (workflow.Workflow, workflow.Draft, error)
	UpdateMetadata(context.Context, string, string, workflow.MetadataUpdate) (workflow.Workflow, error)
	GetDraft(context.Context, string, string) (workflow.Draft, error)
	UpdateDraft(context.Context, string, string, workflow.DraftUpdate) (workflow.Draft, error)
	GetRevision(context.Context, string, string, string) (workflow.Revision, error)
}

type ProviderStore interface {
	List(context.Context, string) ([]provider.Provider, error)
	Get(context.Context, string, string) (provider.Provider, error)
}

type ConnectionStore interface {
	List(context.Context, string, *string) ([]connection.Connection, error)
	Get(context.Context, string, string) (connection.Connection, error)
}

type IDGenerator func() (string, error)

type Service struct {
	tools       ToolStore
	workflows   WorkflowStore
	providers   ProviderStore
	connections ConnectionStore
	newID       IDGenerator
}

func NewService(
	tools ToolStore,
	workflows WorkflowStore,
	providers ProviderStore,
	connections ConnectionStore,
	newID IDGenerator,
) (*Service, error) {
	if tools == nil || workflows == nil || providers == nil || connections == nil {
		return nil, fmt.Errorf("%w: package stores are required", ErrInvalid)
	}
	if newID == nil {
		newID = func() (string, error) {
			id, err := uuid.NewV7()
			if err != nil {
				return "", err
			}
			return id.String(), nil
		}
	}
	return &Service{
		tools: tools, workflows: workflows, providers: providers,
		connections: connections, newID: newID,
	}, nil
}

type ExportInput struct {
	WorkspaceID   string
	ToolIDs       []string
	WorkflowIDs   []string
	Source        string
	WorkspaceHint string
}

type ConnectionBinding struct {
	Provider           string `json:"provider"`
	Connection         string `json:"connection"`
	TargetConnection   string `json:"targetConnection,omitempty"`
	TargetConnectionID string `json:"targetConnectionId,omitempty"`
}

type PreviewInput struct {
	WorkspaceID        string
	Document           Document
	ConnectionBindings []ConnectionBinding
}

type Preview struct {
	Items     []PreviewItem `json:"items"`
	CanImport bool          `json:"canImport"`
	Blocked   int           `json:"blocked"`
	Rejected  int           `json:"rejected"`
}

type PreviewItem struct {
	Kind                 string `json:"kind"`
	Slug                 string `json:"slug"`
	Name                 string `json:"name"`
	Action               string `json:"action"`
	Reason               string `json:"reason,omitempty"`
	Provider             string `json:"provider,omitempty"`
	Connection           string `json:"connection,omitempty"`
	ResolvedProviderID   string `json:"resolvedProviderId,omitempty"`
	ResolvedConnectionID string `json:"resolvedConnectionId,omitempty"`
	ExistingID           string `json:"existingId,omitempty"`
}

type ImportInput struct {
	WorkspaceID        string
	ActorID            string
	Document           Document
	ConnectionBindings []ConnectionBinding
}

type ImportResult struct {
	Items []ImportItem `json:"items"`
}

type ImportItem struct {
	Kind    string `json:"kind"`
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Action  string `json:"action"`
	ID      string `json:"id"`
	DraftID string `json:"draftId,omitempty"`
}

func (s *Service) Export(ctx context.Context, input ExportInput) (Document, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Source = strings.ToLower(strings.TrimSpace(input.Source))
	if input.WorkspaceID == "" || (input.Source != SourceAuto && input.Source != SourceDraft && input.Source != SourcePublished) {
		return Document{}, ErrInvalid
	}
	if len(input.ToolIDs) == 0 && len(input.WorkflowIDs) == 0 {
		return Document{}, fmt.Errorf("%w: export requires tool or workflow ids", ErrInvalid)
	}
	catalog, err := s.loadCatalog(ctx, input.WorkspaceID)
	if err != nil {
		return Document{}, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	document := Document{
		APIVersion: APIVersionV1,
		Kind:       KindPackage,
		Metadata: Metadata{
			ExportedAt: &now,
			Source:     SourceHint{WorkspaceHint: strings.TrimSpace(input.WorkspaceHint), ExportedBy: "console"},
		},
	}
	for _, id := range uniqueIDs(input.ToolIDs) {
		spec, err := s.exportTool(ctx, input.WorkspaceID, id, input.Source, catalog)
		if err != nil {
			return Document{}, err
		}
		document.Spec.Tools = append(document.Spec.Tools, spec)
	}
	for _, id := range uniqueIDs(input.WorkflowIDs) {
		spec, err := s.exportWorkflow(ctx, input.WorkspaceID, id, input.Source, catalog)
		if err != nil {
			return Document{}, err
		}
		document.Spec.Workflows = append(document.Spec.Workflows, spec)
	}
	if len(document.Spec.Tools) == 1 && len(document.Spec.Workflows) == 0 {
		document.Metadata.Name = document.Spec.Tools[0].Slug
	} else if len(document.Spec.Workflows) == 1 && len(document.Spec.Tools) == 0 {
		document.Metadata.Name = document.Spec.Workflows[0].Slug
	} else {
		document.Metadata.Name = "actweave-package"
	}
	if err := validateDocument(document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (s *Service) Preview(ctx context.Context, input PreviewInput) (Preview, error) {
	if err := validateDocument(input.Document); err != nil {
		return Preview{}, err
	}
	catalog, err := s.loadCatalog(ctx, strings.TrimSpace(input.WorkspaceID))
	if err != nil {
		return Preview{}, err
	}
	return s.previewWithCatalog(input.Document, catalog, input.ConnectionBindings), nil
}

func (s *Service) Import(ctx context.Context, input ImportInput) (ImportResult, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	if input.WorkspaceID == "" || input.ActorID == "" {
		return ImportResult{}, ErrInvalid
	}
	if err := validateDocument(input.Document); err != nil {
		return ImportResult{}, err
	}
	catalog, err := s.loadCatalog(ctx, input.WorkspaceID)
	if err != nil {
		return ImportResult{}, err
	}
	preview := s.previewWithCatalog(input.Document, catalog, input.ConnectionBindings)
	if !preview.CanImport {
		return ImportResult{}, ErrBlocked
	}
	result := ImportResult{Items: make([]ImportItem, 0, len(preview.Items))}
	itemBySlug := map[string]PreviewItem{}
	for _, item := range preview.Items {
		itemBySlug[item.Kind+":"+item.Slug] = item
	}
	for _, spec := range input.Document.Spec.Tools {
		item := itemBySlug[KindTool+":"+spec.Slug]
		imported, err := s.importTool(ctx, input.WorkspaceID, input.ActorID, spec, item)
		if err != nil {
			return ImportResult{}, err
		}
		result.Items = append(result.Items, imported)
		catalog.toolBySlug[spec.Slug] = tool.Tool{
			CapabilityID: imported.ID, Slug: spec.Slug, Name: spec.Name,
			ProviderID: item.ResolvedProviderID,
		}
	}
	for _, spec := range input.Document.Spec.Workflows {
		item := itemBySlug[KindWorkflow+":"+spec.Slug]
		imported, err := s.importWorkflow(ctx, input.WorkspaceID, input.ActorID, spec, item, catalog)
		if err != nil {
			return ImportResult{}, err
		}
		result.Items = append(result.Items, imported)
		catalog.workflowBySlug[spec.Slug] = workflow.Workflow{
			CapabilityID: imported.ID, Slug: spec.Slug, Name: spec.Name,
		}
	}
	return result, nil
}

type workspaceCatalog struct {
	providersByName map[string]provider.Provider
	providersByID   map[string]provider.Provider
	connections     []connection.Connection
	toolByID        map[string]tool.Tool
	toolBySlug      map[string]tool.Tool
	workflowByID    map[string]workflow.Workflow
	workflowBySlug  map[string]workflow.Workflow
}

func (s *Service) loadCatalog(ctx context.Context, workspaceID string) (*workspaceCatalog, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return nil, ErrInvalid
	}
	providers, err := s.providers.List(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	connections, err := s.connections.List(ctx, workspaceID, nil)
	if err != nil {
		return nil, err
	}
	tools, err := s.tools.List(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	workflows, err := s.workflows.List(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	catalog := &workspaceCatalog{
		providersByName: make(map[string]provider.Provider, len(providers)),
		providersByID:   make(map[string]provider.Provider, len(providers)),
		connections:     connections,
		toolByID:        make(map[string]tool.Tool, len(tools)),
		toolBySlug:      make(map[string]tool.Tool, len(tools)),
		workflowByID:    make(map[string]workflow.Workflow, len(workflows)),
		workflowBySlug:  make(map[string]workflow.Workflow, len(workflows)),
	}
	for _, value := range providers {
		catalog.providersByName[strings.ToLower(value.Name)] = value
		catalog.providersByID[value.ID] = value
	}
	for _, value := range tools {
		catalog.toolByID[value.CapabilityID] = value
		catalog.toolBySlug[value.Slug] = value
	}
	for _, value := range workflows {
		catalog.workflowByID[value.CapabilityID] = value
		catalog.workflowBySlug[value.Slug] = value
	}
	return catalog, nil
}

func (s *Service) exportTool(
	ctx context.Context,
	workspaceID, capabilityID, source string,
	catalog *workspaceCatalog,
) (ToolSpec, error) {
	value, err := s.tools.Get(ctx, workspaceID, strings.TrimSpace(capabilityID))
	if err != nil {
		return ToolSpec{}, err
	}
	version, err := s.selectToolVersion(ctx, workspaceID, value.CapabilityID, source)
	if err != nil {
		return ToolSpec{}, err
	}
	if version.ExecutorType != "" && version.ExecutorType != "HTTP" {
		return ToolSpec{}, fmt.Errorf("%w: tool %s is not an HTTP tool", ErrInvalid, value.Slug)
	}
	providerName := value.ProviderID
	if known, ok := catalog.providersByID[value.ProviderID]; ok {
		providerName = known.Name
	}
	connectionAlias := ""
	connectionID := firstNonEmpty(deref(version.DefaultConnectionID), deref(value.DefaultConnectionID))
	if connectionID != "" {
		for _, item := range catalog.connections {
			if item.ID == connectionID {
				connectionAlias = item.Alias
				break
			}
		}
	}
	return ToolSpec{
		Slug:                 value.Slug,
		Name:                 value.Name,
		Description:          value.Description,
		Provider:             providerName,
		Connection:           connectionAlias,
		RiskLevel:            version.RiskLevel,
		SideEffectLevel:      version.SideEffectLevel,
		RequiresConfirmation: version.RequiresConfirmation,
		Action: Action{
			SchemaVersion: version.ActionSchemaVersion,
			Config:        objectFromJSON(version.ActionConfig),
		},
		InputSchema:   objectFromJSON(version.InputSchema),
		OutputSchema:  objectFromJSON(version.OutputSchema),
		ErrorMappings: objectFromJSON(version.ErrorMappings),
		RuntimePolicy: objectFromJSON(version.RuntimePolicy),
	}, nil
}

func (s *Service) exportWorkflow(
	ctx context.Context,
	workspaceID, capabilityID, source string,
	catalog *workspaceCatalog,
) (WorkflowSpec, error) {
	value, err := s.workflows.Get(ctx, workspaceID, strings.TrimSpace(capabilityID))
	if err != nil {
		return WorkflowSpec{}, err
	}
	var rawGraph []byte
	var schemaVersion string
	if source == SourcePublished && value.ActiveRevisionID != nil {
		revision, err := s.workflows.GetRevision(ctx, workspaceID, value.CapabilityID, *value.ActiveRevisionID)
		if err != nil {
			return WorkflowSpec{}, err
		}
		rawGraph = revision.DraftSnapshot
		schemaVersion = DefaultGraphSchema
	} else {
		draft, err := s.workflows.GetDraft(ctx, workspaceID, value.CapabilityID)
		if err != nil {
			return WorkflowSpec{}, err
		}
		rawGraph = draft.Graph
		schemaVersion = draft.SchemaVersion
	}
	graph, err := graphFromJSON(rawGraph)
	if err != nil {
		return WorkflowSpec{}, fmt.Errorf("%w: workflow graph: %v", ErrInvalid, err)
	}
	if strings.TrimSpace(graph.SchemaVersion) == "" {
		graph.SchemaVersion = schemaVersion
	}
	toolSlugByID := map[string]string{}
	for id, item := range catalog.toolByID {
		toolSlugByID[id] = item.Slug
	}
	workflowSlugByID := map[string]string{}
	for id, item := range catalog.workflowByID {
		workflowSlugByID[id] = item.Slug
	}
	return WorkflowSpec{
		Slug:        value.Slug,
		Name:        value.Name,
		Description: value.Description,
		Graph:       rewriteGraphForExport(graph, toolSlugByID, workflowSlugByID),
	}, nil
}

func (s *Service) selectToolVersion(ctx context.Context, workspaceID, capabilityID, source string) (tool.Version, error) {
	versions, err := s.tools.ListVersions(ctx, workspaceID, capabilityID)
	if err != nil {
		return tool.Version{}, err
	}
	if len(versions) == 0 {
		return tool.Version{}, ErrNotFound
	}
	var draft, published *tool.Version
	for i := range versions {
		version := versions[i]
		if version.LifecycleStatus != "PUBLISHED" {
			draft = &versions[i]
		} else {
			published = &versions[i]
		}
	}
	switch source {
	case SourcePublished:
		if published == nil {
			return tool.Version{}, fmt.Errorf("%w: tool has no published version", ErrNotFound)
		}
		return *published, nil
	case SourceDraft:
		if draft != nil {
			return *draft, nil
		}
		if published != nil {
			return *published, nil
		}
	default:
		if draft != nil {
			return *draft, nil
		}
		if published != nil {
			return *published, nil
		}
	}
	return versions[len(versions)-1], nil
}

func (s *Service) previewWithCatalog(document Document, catalog *workspaceCatalog, bindings []ConnectionBinding) Preview {
	preview := Preview{Items: make([]PreviewItem, 0, len(document.Spec.Tools)+len(document.Spec.Workflows))}
	packageToolSlugs := map[string]struct{}{}
	packageWorkflowSlugs := map[string]struct{}{}
	for _, spec := range document.Spec.Tools {
		packageToolSlugs[spec.Slug] = struct{}{}
		item := s.previewTool(spec, catalog, bindings)
		preview.Items = append(preview.Items, item)
	}
	for _, spec := range document.Spec.Workflows {
		packageWorkflowSlugs[spec.Slug] = struct{}{}
	}
	for _, spec := range document.Spec.Workflows {
		item := s.previewWorkflow(spec, catalog, packageWorkflowSlugs, packageToolSlugs)
		preview.Items = append(preview.Items, item)
	}
	for _, item := range preview.Items {
		switch item.Action {
		case ActionBlocked:
			preview.Blocked++
		case ActionRejected:
			preview.Rejected++
		}
	}
	preview.CanImport = preview.Blocked == 0 && preview.Rejected == 0
	return preview
}

func (s *Service) previewTool(spec ToolSpec, catalog *workspaceCatalog, bindings []ConnectionBinding) PreviewItem {
	item := PreviewItem{
		Kind: KindTool, Slug: spec.Slug, Name: spec.Name,
		Provider: spec.Provider, Connection: spec.Connection, Action: ActionCreate,
	}
	providerValue, ok := catalog.providersByName[strings.ToLower(strings.TrimSpace(spec.Provider))]
	if !ok {
		item.Action = ActionBlocked
		item.Reason = "provider " + spec.Provider + " was not found in this workspace"
		return item
	}
	item.ResolvedProviderID = providerValue.ID
	connectionID, reason := resolveConnection(spec.Provider, spec.Connection, providerValue.ID, catalog.connections, bindings)
	if reason != "" {
		item.Action = ActionBlocked
		item.Reason = reason
		return item
	}
	item.ResolvedConnectionID = connectionID
	if existing, exists := catalog.toolBySlug[spec.Slug]; exists {
		if existing.ProviderID != providerValue.ID {
			item.Action = ActionBlocked
			item.Reason = "slug already exists on a different provider"
			item.ExistingID = existing.CapabilityID
			return item
		}
		item.Action = ActionUpdateDraft
		item.ExistingID = existing.CapabilityID
	}
	return item
}

func (s *Service) previewWorkflow(
	spec WorkflowSpec,
	catalog *workspaceCatalog,
	packageWorkflowSlugs map[string]struct{},
	packageToolSlugs map[string]struct{},
) PreviewItem {
	item := PreviewItem{Kind: KindWorkflow, Slug: spec.Slug, Name: spec.Name, Action: ActionCreate}
	var unresolved []string
	for _, node := range spec.Graph.Nodes {
		switch node.Type {
		case "Tool":
			slug := graphRef(node.Data, "toolSlug", "toolId")
			if slug == "" {
				unresolved = append(unresolved, "node "+node.ID+": toolSlug is missing")
				continue
			}
			if _, exists := catalog.toolBySlug[slug]; exists {
				continue
			}
			if _, inPackage := packageToolSlugs[slug]; inPackage {
				continue
			}
			unresolved = append(unresolved, "node "+node.ID+": toolSlug "+slug+" was not found in this workspace")
		case "SubWorkflow":
			slug := graphRef(node.Data, "workflowSlug", "workflowId")
			if slug == "" {
				unresolved = append(unresolved, "node "+node.ID+": workflowSlug is missing")
				continue
			}
			if _, exists := catalog.workflowBySlug[slug]; exists {
				continue
			}
			if _, inPackage := packageWorkflowSlugs[slug]; inPackage {
				continue
			}
			unresolved = append(unresolved, "node "+node.ID+": workflowSlug "+slug+" was not found in this workspace")
		}
	}
	if len(unresolved) > 0 {
		item.Action = ActionBlocked
		item.Reason = strings.Join(unresolved, "; ")
		return item
	}
	if existing, exists := catalog.workflowBySlug[spec.Slug]; exists {
		item.Action = ActionUpdateDraft
		item.ExistingID = existing.CapabilityID
	}
	return item
}

func graphRef(data Object, primary, fallback string) string {
	if data == nil {
		return ""
	}
	if value, _ := data[primary].(string); strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	if value, _ := data[fallback].(string); strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return ""
}

func resolveConnection(
	providerName, alias, providerID string,
	connections []connection.Connection,
	bindings []ConnectionBinding,
) (string, string) {
	alias = strings.TrimSpace(alias)
	for _, binding := range bindings {
		if !strings.EqualFold(strings.TrimSpace(binding.Provider), providerName) {
			continue
		}
		if strings.TrimSpace(binding.Connection) != "" && !strings.EqualFold(strings.TrimSpace(binding.Connection), alias) {
			continue
		}
		if id := strings.TrimSpace(binding.TargetConnectionID); id != "" {
			for _, item := range connections {
				if item.ID == id && item.ProviderID == providerID {
					return item.ID, ""
				}
			}
			return "", "connection binding target was not found"
		}
		targetAlias := strings.TrimSpace(binding.TargetConnection)
		if targetAlias == "" {
			continue
		}
		for _, item := range connections {
			if item.ProviderID == providerID && strings.EqualFold(item.Alias, targetAlias) {
				return item.ID, ""
			}
		}
		return "", "connection alias " + targetAlias + " was not found for this provider"
	}
	if alias == "" {
		return "", "connection alias is required"
	}
	for _, item := range connections {
		if item.ProviderID == providerID && strings.EqualFold(item.Alias, alias) {
			return item.ID, ""
		}
	}
	return "", "connection alias " + alias + " was not found for provider " + providerName
}

func (s *Service) importTool(
	ctx context.Context,
	workspaceID, actorID string,
	spec ToolSpec,
	preview PreviewItem,
) (ImportItem, error) {
	connectionID := strings.TrimSpace(preview.ResolvedConnectionID)
	draft := tool.DraftSpec{
		DefaultConnectionID:  optionalString(connectionID),
		ActionSchemaVersion:  spec.Action.SchemaVersion,
		ActionConfig:         spec.Action.Config.JSON(),
		InputSchema:          spec.InputSchema.JSON(),
		OutputSchema:         spec.OutputSchema.JSON(),
		ErrorMappings:        spec.ErrorMappings.JSON(),
		RuntimePolicy:        spec.RuntimePolicy.JSON(),
		RiskLevel:            spec.RiskLevel,
		SideEffectLevel:      spec.SideEffectLevel,
		RequiresConfirmation: spec.RequiresConfirmation,
	}
	if preview.Action == ActionCreate {
		capabilityID, err := s.newID()
		if err != nil {
			return ImportItem{}, err
		}
		versionID, err := s.newID()
		if err != nil {
			return ImportItem{}, err
		}
		created, version, err := s.tools.Create(ctx, tool.CreateInput{
			CapabilityID:        capabilityID,
			InitialVersionID:    versionID,
			WorkspaceID:         workspaceID,
			ProviderID:          preview.ResolvedProviderID,
			DefaultConnectionID: optionalString(connectionID),
			Name:                spec.Name,
			Slug:                spec.Slug,
			Description:         spec.Description,
			Draft:               draft,
			CreatedBy:           actorID,
		})
		if err != nil {
			return ImportItem{}, err
		}
		return ImportItem{
			Kind: KindTool, Slug: spec.Slug, Name: spec.Name, Action: ActionCreate,
			ID: created.CapabilityID, DraftID: version.ID,
		}, nil
	}
	existing, err := s.tools.Get(ctx, workspaceID, preview.ExistingID)
	if err != nil {
		return ImportItem{}, err
	}
	if _, err := s.tools.UpdateMetadata(ctx, workspaceID, existing.CapabilityID, tool.MetadataUpdate{
		Name: spec.Name, Slug: existing.Slug, Description: spec.Description,
		Status: existing.Status, DefaultConnectionID: optionalString(connectionID),
		SourceAssetID: existing.SourceAssetID, UpdatedBy: actorID,
		ExpectedLockVersion: existing.LockVersion,
	}); err != nil {
		return ImportItem{}, err
	}
	version, err := s.ensureMutableToolVersion(ctx, workspaceID, existing.CapabilityID, actorID)
	if err != nil {
		return ImportItem{}, err
	}
	updated, err := s.tools.UpdateDraft(ctx, workspaceID, existing.CapabilityID, version.ID, tool.DraftUpdate{
		Spec: draft, LifecycleStatus: "DRAFT", UpdatedBy: actorID, ExpectedLockVersion: version.LockVersion,
	})
	if err != nil {
		return ImportItem{}, err
	}
	return ImportItem{
		Kind: KindTool, Slug: spec.Slug, Name: spec.Name, Action: ActionUpdateDraft,
		ID: existing.CapabilityID, DraftID: updated.ID,
	}, nil
}

func (s *Service) ensureMutableToolVersion(
	ctx context.Context,
	workspaceID, capabilityID, actorID string,
) (tool.Version, error) {
	versions, err := s.tools.ListVersions(ctx, workspaceID, capabilityID)
	if err != nil {
		return tool.Version{}, err
	}
	var published *tool.Version
	for i := range versions {
		if versions[i].LifecycleStatus != "PUBLISHED" {
			return versions[i], nil
		}
		published = &versions[i]
	}
	if published == nil {
		return tool.Version{}, ErrNotFound
	}
	id, err := s.newID()
	if err != nil {
		return tool.Version{}, err
	}
	return s.tools.CreateDraftFromPublished(ctx, workspaceID, capabilityID, published.ID, id, actorID)
}

func (s *Service) importWorkflow(
	ctx context.Context,
	workspaceID, actorID string,
	spec WorkflowSpec,
	preview PreviewItem,
	catalog *workspaceCatalog,
) (ImportItem, error) {
	toolIDBySlug := map[string]string{}
	for slug, value := range catalog.toolBySlug {
		toolIDBySlug[slug] = value.CapabilityID
	}
	workflowIDBySlug := map[string]string{}
	for slug, value := range catalog.workflowBySlug {
		workflowIDBySlug[slug] = value.CapabilityID
	}
	graph, missing := rewriteGraphForImport(spec.Graph, toolIDBySlug, workflowIDBySlug)
	if len(missing) > 0 {
		return ImportItem{}, fmt.Errorf("%w: %s", ErrBlocked, strings.Join(missing, "; "))
	}
	encoded, err := graphJSON(graph)
	if err != nil {
		return ImportItem{}, fmt.Errorf("%w: encode workflow graph: %v", ErrInvalid, err)
	}
	schemaVersion := graph.SchemaVersion
	if schemaVersion == "" {
		schemaVersion = DefaultGraphSchema
	}
	if preview.Action == ActionCreate {
		capabilityID, err := s.newID()
		if err != nil {
			return ImportItem{}, err
		}
		draftID, err := s.newID()
		if err != nil {
			return ImportItem{}, err
		}
		created, draft, err := s.workflows.Create(ctx, workflow.CreateInput{
			CapabilityID: capabilityID, DraftID: draftID, WorkspaceID: workspaceID,
			Name: spec.Name, Slug: spec.Slug, Description: spec.Description,
			SchemaVersion: schemaVersion, Graph: encoded, CreatedBy: actorID,
		})
		if err != nil {
			return ImportItem{}, err
		}
		return ImportItem{
			Kind: KindWorkflow, Slug: spec.Slug, Name: spec.Name, Action: ActionCreate,
			ID: created.CapabilityID, DraftID: draft.ID,
		}, nil
	}
	existing, err := s.workflows.Get(ctx, workspaceID, preview.ExistingID)
	if err != nil {
		return ImportItem{}, err
	}
	if _, err := s.workflows.UpdateMetadata(ctx, workspaceID, existing.CapabilityID, workflow.MetadataUpdate{
		Name: spec.Name, Slug: existing.Slug, Description: spec.Description,
		Status: existing.Status, UpdatedBy: actorID, ExpectedLockVersion: existing.LockVersion,
	}); err != nil {
		return ImportItem{}, err
	}
	draft, err := s.workflows.GetDraft(ctx, workspaceID, existing.CapabilityID)
	if err != nil {
		return ImportItem{}, err
	}
	updated, err := s.workflows.UpdateDraft(ctx, workspaceID, existing.CapabilityID, workflow.DraftUpdate{
		SchemaVersion: schemaVersion, Graph: encoded, UpdatedBy: actorID,
		ExpectedDraftVersion: draft.DraftVersion, ExpectedLockVersion: draft.LockVersion,
	})
	if err != nil {
		return ImportItem{}, err
	}
	return ImportItem{
		Kind: KindWorkflow, Slug: spec.Slug, Name: spec.Name, Action: ActionUpdateDraft,
		ID: existing.CapabilityID, DraftID: updated.ID,
	}, nil
}

func uniqueIDs(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
