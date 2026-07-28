package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/workspace"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type WorkspaceService interface {
	Create(context.Context, workspace.NewWorkspace) (workspace.Workspace, error)
	Get(context.Context, string) (workspace.Workspace, error)
	ListAccessible(context.Context, string, int) ([]workspace.Workspace, error)
	ListAccessiblePage(context.Context, string, workspace.WorkspaceListQuery) (workspace.WorkspacePage, error)
	GetAccessible(context.Context, string, string) (workspace.AccessibleWorkspace, error)
	Update(context.Context, string, workspace.UpdateWorkspaceInput) (workspace.Workspace, error)
	SetStatus(context.Context, string, workspace.Status, string, int64) (workspace.Workspace, error)
	SoftDelete(context.Context, string, string, int64) error
	AddMember(context.Context, workspace.NewMember) (workspace.Member, error)
	ChangeMemberRole(context.Context, string, string, workspace.Role, string) (workspace.Member, error)
	RemoveMember(context.Context, string, string, string) error
	ListMembers(context.Context, string, bool) ([]workspace.Member, error)
	SearchMemberCandidates(context.Context, string, string, int) ([]workspace.MemberCandidate, error)
}

type WorkspaceAuthorizer interface {
	AuthorizeWorkspace(context.Context, string, string, authz.Action) (authz.WorkspaceContext, error)
}

type WorkspaceActorResolver interface {
	UsernamesByIDs(context.Context, []string) (map[string]string, error)
}

type WorkspaceRoutes struct {
	service    WorkspaceService
	authorizer WorkspaceAuthorizer
	actors     WorkspaceActorResolver
}

func NewWorkspaceRoutes(
	service WorkspaceService,
	authorizer WorkspaceAuthorizer,
	actors WorkspaceActorResolver,
) (*WorkspaceRoutes, error) {
	if service == nil || authorizer == nil || actors == nil {
		return nil, errors.New("workspace routes dependencies are required")
	}
	return &WorkspaceRoutes{service: service, authorizer: authorizer, actors: actors}, nil
}

func (routes *WorkspaceRoutes) RegisterV1(v1 V1Routes) {
	v1.Protected.GET("/workspaces", routes.listWorkspaces)
	v1.Protected.POST("/workspaces", routes.createWorkspace)
	v1.Protected.GET("/workspaces/:wid", routes.getWorkspace)
	v1.Protected.PATCH("/workspaces/:wid", routes.updateWorkspace)
	v1.Protected.DELETE("/workspaces/:wid", routes.deleteWorkspace)
	v1.Protected.POST("/workspaces/:wid/__command/enable", routes.enableWorkspace)
	v1.Protected.POST("/workspaces/:wid/__command/disable", routes.disableWorkspace)
	v1.Protected.GET("/workspaces/:wid/members", routes.listMembers)
	v1.Protected.GET("/workspaces/:wid/member-candidates", routes.searchMemberCandidates)
	v1.Protected.POST("/workspaces/:wid/members", routes.addMember)
	v1.Protected.PATCH("/workspaces/:wid/members/:uid", routes.changeMemberRole)
	v1.Protected.DELETE("/workspaces/:wid/members/:uid", routes.removeMember)
}

type workspaceDTO struct {
	ID                   string           `json:"id"`
	Slug                 string           `json:"slug"`
	DisplayName          string           `json:"displayName"`
	Mode                 workspace.Mode   `json:"mode"`
	Status               workspace.Status `json:"status"`
	OwnerUserID          string           `json:"ownerUserId"`
	DefaultAgentID       *string          `json:"defaultAgentId,omitempty"`
	DefaultModelConfigID *string          `json:"defaultModelConfigId,omitempty"`
	Settings             json.RawMessage  `json:"settings"`
	CreatedBy            string           `json:"createdBy"`
	CreatedByUsername    string           `json:"createdByUsername,omitempty"`
	UpdatedBy            string           `json:"updatedBy"`
	UpdatedByUsername    string           `json:"updatedByUsername,omitempty"`
	CreatedAt            time.Time        `json:"createdAt"`
	UpdatedAt            time.Time        `json:"updatedAt"`
	LockVersion          int64            `json:"lockVersion"`
	// CurrentUserRole is the caller's effective membership role (ZKL-64 D1-A).
	CurrentUserRole workspace.Role `json:"currentUserRole,omitempty"`
}

type memberDTO struct {
	UserID     string         `json:"userId"`
	Role       workspace.Role `json:"role"`
	InvitedBy  *string        `json:"invitedBy,omitempty"`
	JoinedAt   time.Time      `json:"joinedAt"`
	DisabledAt *time.Time     `json:"disabledAt,omitempty"`
}

type memberCandidateDTO struct {
	UserID       string `json:"userId"`
	Username     string `json:"username"`
	DisplayName  string `json:"displayName"`
	PlatformRole string `json:"platformRole"`
}

func (routes *WorkspaceRoutes) listWorkspaces(c *gin.Context) {
	principal, _ := PrincipalFrom(c.Request.Context())
	query, err := parseWorkspaceListQuery(c)
	if err != nil {
		RespondError(c, err)
		return
	}
	page, err := routes.service.ListAccessiblePage(c.Request.Context(), principal.UserID, query)
	if err != nil {
		RespondError(c, err)
		return
	}
	items, err := routes.toAccessibleWorkspaceDTOs(c.Request.Context(), page.Items)
	if err != nil {
		RespondError(c, err)
		return
	}
	response := gin.H{
		"items": items,
		"pagination": gin.H{
			"page":     page.Page,
			"pageSize": page.PageSize,
			"total":    page.Total,
		},
		"summary": gin.H{
			"total":       page.Summary.Total,
			"active":      page.Summary.Active,
			"production":  page.Summary.Production,
			"boundAgents": page.Summary.BoundAgents,
		},
	}
	c.JSON(http.StatusOK, response)
}

type createWorkspaceRequest struct {
	Slug        string          `json:"slug"`
	DisplayName string          `json:"displayName"`
	Mode        workspace.Mode  `json:"mode"`
	Settings    json.RawMessage `json:"settings"`
}

func (routes *WorkspaceRoutes) createWorkspace(c *gin.Context) {
	var request createWorkspaceRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, workspace.ErrInvalid)
		return
	}
	id, err := uuid.NewV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	value, err := routes.service.Create(c.Request.Context(), workspace.NewWorkspace{
		ID: id.String(), Slug: request.Slug, DisplayName: request.DisplayName,
		Mode: request.Mode, Settings: request.Settings,
		OwnerUserID: principal.UserID, CreatedBy: principal.UserID,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	dto, err := routes.toWorkspaceDTO(c.Request.Context(), value)
	if err != nil {
		RespondError(c, err)
		return
	}
	dto.CurrentUserRole = workspace.RoleOwner
	c.JSON(http.StatusCreated, dto)
}

func (routes *WorkspaceRoutes) getWorkspace(c *gin.Context) {
	if !routes.authorize(c, c.Param("wid"), authz.ActionView) {
		return
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	accessible, err := routes.service.GetAccessible(c.Request.Context(), principal.UserID, c.Param("wid"))
	if err != nil {
		RespondError(c, err)
		return
	}
	dto, err := routes.toAccessibleWorkspaceDTO(c.Request.Context(), accessible)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto)
}

type updateWorkspaceRequest struct {
	DisplayName *string         `json:"displayName"`
	Mode        *workspace.Mode `json:"mode"`
	Settings    json.RawMessage `json:"settings"`
	LockVersion int64           `json:"lockVersion"`
}

func (routes *WorkspaceRoutes) updateWorkspace(c *gin.Context) {
	if !routes.authorize(c, c.Param("wid"), authz.ActionEdit) {
		return
	}
	var request updateWorkspaceRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, workspace.ErrInvalid)
		return
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	value, err := routes.service.Update(c.Request.Context(), c.Param("wid"), workspace.UpdateWorkspaceInput{
		DisplayName: request.DisplayName, Mode: request.Mode, Settings: request.Settings,
		UpdatedBy: principal.UserID, ExpectedLockVersion: request.LockVersion,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	dto, err := routes.toWorkspaceDTOWithRole(c.Request.Context(), principal.UserID, value)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto)
}

func (routes *WorkspaceRoutes) deleteWorkspace(c *gin.Context) {
	if !routes.authorize(c, c.Param("wid"), authz.ActionDelete) {
		return
	}
	lockVersion, err := strconv.ParseInt(c.Query("lockVersion"), 10, 64)
	if err != nil || lockVersion < 1 {
		RespondError(c, workspace.ErrInvalid)
		return
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	if err := routes.service.SoftDelete(
		c.Request.Context(), c.Param("wid"), principal.UserID, lockVersion,
	); err != nil {
		RespondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type workspaceStatusRequest struct {
	LockVersion int64 `json:"lockVersion"`
}

func (routes *WorkspaceRoutes) enableWorkspace(c *gin.Context) {
	routes.setWorkspaceStatus(c, workspace.StatusActive)
}

func (routes *WorkspaceRoutes) disableWorkspace(c *gin.Context) {
	routes.setWorkspaceStatus(c, workspace.StatusDisabled)
}

func (routes *WorkspaceRoutes) setWorkspaceStatus(c *gin.Context, status workspace.Status) {
	workspaceID := c.Param("wid")
	if !routes.authorizeStatus(c, workspaceID) {
		return
	}
	var request workspaceStatusRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, workspace.ErrInvalid)
		return
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	value, err := routes.service.SetStatus(
		c.Request.Context(), workspaceID, status, principal.UserID, request.LockVersion,
	)
	if err != nil {
		RespondError(c, err)
		return
	}
	dto, err := routes.toWorkspaceDTOWithRole(c.Request.Context(), principal.UserID, value)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto)
}

func (routes *WorkspaceRoutes) listMembers(c *gin.Context) {
	includeDisabled := c.Query("includeDisabled") == "true"
	action := authz.ActionView
	if includeDisabled {
		action = authz.ActionManage
	}
	if !routes.authorize(c, c.Param("wid"), action) {
		return
	}
	values, err := routes.service.ListMembers(c.Request.Context(), c.Param("wid"), includeDisabled)
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]memberDTO, len(values))
	for index, value := range values {
		items[index] = toMemberDTO(value)
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (routes *WorkspaceRoutes) searchMemberCandidates(c *gin.Context) {
	workspaceID := c.Param("wid")
	if !routes.authorize(c, workspaceID, authz.ActionManage) {
		return
	}
	limit, err := queryLimit(c, 20)
	if err != nil || limit > 100 {
		RespondError(c, workspace.ErrInvalid)
		return
	}
	values, err := routes.service.SearchMemberCandidates(
		c.Request.Context(), workspaceID, c.Query("query"), limit,
	)
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]memberCandidateDTO, len(values))
	for index, value := range values {
		items[index] = memberCandidateDTO{
			UserID: value.UserID, Username: value.Username,
			DisplayName: value.DisplayName, PlatformRole: value.PlatformRole,
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type addMemberRequest struct {
	UserID string         `json:"userId"`
	Role   workspace.Role `json:"role"`
}

func (routes *WorkspaceRoutes) addMember(c *gin.Context) {
	if !routes.authorize(c, c.Param("wid"), authz.ActionManage) {
		return
	}
	var request addMemberRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, workspace.ErrInvalid)
		return
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	value, err := routes.service.AddMember(c.Request.Context(), workspace.NewMember{
		WorkspaceID: c.Param("wid"), UserID: request.UserID,
		Role: request.Role, InvitedBy: principal.UserID,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toMemberDTO(value))
}

type changeMemberRoleRequest struct {
	Role workspace.Role `json:"role"`
}

func (routes *WorkspaceRoutes) changeMemberRole(c *gin.Context) {
	if !routes.authorize(c, c.Param("wid"), authz.ActionManage) {
		return
	}
	var request changeMemberRoleRequest
	if decodeJSON(c, &request) != nil || !validPathUUID(c.Param("uid")) {
		RespondError(c, workspace.ErrInvalid)
		return
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	value, err := routes.service.ChangeMemberRole(
		c.Request.Context(), c.Param("wid"), c.Param("uid"), request.Role, principal.UserID,
	)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toMemberDTO(value))
}

func (routes *WorkspaceRoutes) removeMember(c *gin.Context) {
	if !routes.authorize(c, c.Param("wid"), authz.ActionManage) {
		return
	}
	if !validPathUUID(c.Param("uid")) {
		RespondError(c, workspace.ErrInvalid)
		return
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	if err := routes.service.RemoveMember(
		c.Request.Context(), c.Param("wid"), c.Param("uid"), principal.UserID,
	); err != nil {
		RespondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (routes *WorkspaceRoutes) authorize(c *gin.Context, workspaceID string, action authz.Action) bool {
	principal, _ := PrincipalFrom(c.Request.Context())
	if _, err := routes.authorizer.AuthorizeWorkspace(
		c.Request.Context(), principal.UserID, workspaceID, action,
	); err != nil {
		RespondError(c, err)
		return false
	}
	return true
}

func (routes *WorkspaceRoutes) authorizeStatus(c *gin.Context, workspaceID string) bool {
	principal, _ := PrincipalFrom(c.Request.Context())
	_, err := routes.authorizer.AuthorizeWorkspace(
		c.Request.Context(), principal.UserID, workspaceID, authz.ActionManage,
	)
	if err == nil {
		return true
	}
	var denial *authz.DenialError
	if errors.As(err, &denial) && denial.Reason == authz.DenialWorkspaceInactive &&
		(denial.Role == workspace.RoleOwner || denial.Role == workspace.RoleAdmin) {
		return true
	}
	RespondError(c, err)
	return false
}

func queryLimit(c *gin.Context, fallback int) (int, error) {
	raw := strings.TrimSpace(c.Query("limit"))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 500 {
		return 0, workspace.ErrInvalid
	}
	return value, nil
}

func parseWorkspaceListQuery(c *gin.Context) (workspace.WorkspaceListQuery, error) {
	rawPage := strings.TrimSpace(c.Query("page"))
	rawPageSize := strings.TrimSpace(c.Query("pageSize"))
	// Legacy bridge: only when neither page nor pageSize is present.
	if rawPage == "" && rawPageSize == "" {
		limit, err := queryLimit(c, 100)
		if err != nil {
			return workspace.WorkspaceListQuery{}, err
		}
		return workspace.WorkspaceListQuery{LegacyLimit: limit}, nil
	}
	page := 1
	if rawPage != "" {
		value, err := strconv.Atoi(rawPage)
		if err != nil || value < 1 {
			return workspace.WorkspaceListQuery{}, workspace.ErrInvalid
		}
		page = value
	}
	pageSize := 20
	if rawPageSize != "" {
		value, err := strconv.Atoi(rawPageSize)
		if err != nil || (value != 10 && value != 20 && value != 50) {
			return workspace.WorkspaceListQuery{}, workspace.ErrInvalid
		}
		pageSize = value
	} else if rawPage != "" {
		// page alone defaults pageSize=20
		pageSize = 20
	}
	query := workspace.WorkspaceListQuery{
		Query:     strings.TrimSpace(c.Query("query")),
		Page:      page,
		PageSize:  pageSize,
		SortBy:    strings.TrimSpace(c.Query("sortBy")),
		SortOrder: strings.ToLower(strings.TrimSpace(c.Query("sortOrder"))),
	}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		status := workspace.Status(strings.ToUpper(raw))
		if status != workspace.StatusActive && status != workspace.StatusDisabled {
			return workspace.WorkspaceListQuery{}, workspace.ErrInvalid
		}
		query.Status = &status
	}
	if raw := strings.TrimSpace(c.Query("mode")); raw != "" {
		mode := workspace.Mode(strings.ToUpper(raw))
		if mode != workspace.ModeProduction && mode != workspace.ModeSandbox {
			return workspace.WorkspaceListQuery{}, workspace.ErrInvalid
		}
		query.Mode = &mode
	}
	if query.SortOrder != "" && query.SortOrder != "asc" && query.SortOrder != "desc" {
		return workspace.WorkspaceListQuery{}, workspace.ErrInvalid
	}
	return query, nil
}

func toWorkspaceDTO(value workspace.Workspace) workspaceDTO {
	return workspaceDTO{
		ID: value.ID, Slug: value.Slug, DisplayName: value.DisplayName,
		Mode: value.Mode, Status: value.Status, OwnerUserID: value.OwnerUserID,
		DefaultAgentID: value.DefaultAgentID, DefaultModelConfigID: value.DefaultModelConfigID,
		Settings: value.Settings, CreatedBy: value.CreatedBy, UpdatedBy: value.UpdatedBy,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, LockVersion: value.LockVersion,
	}
}

func (routes *WorkspaceRoutes) toWorkspaceDTO(ctx context.Context, value workspace.Workspace) (workspaceDTO, error) {
	items, err := routes.toWorkspaceDTOs(ctx, []workspace.Workspace{value})
	if err != nil {
		return workspaceDTO{}, err
	}
	return items[0], nil
}

func (routes *WorkspaceRoutes) toWorkspaceDTOWithRole(
	ctx context.Context,
	userID string,
	value workspace.Workspace,
) (workspaceDTO, error) {
	dto, err := routes.toWorkspaceDTO(ctx, value)
	if err != nil {
		return workspaceDTO{}, err
	}
	accessible, err := routes.service.GetAccessible(ctx, userID, value.ID)
	if err != nil {
		return workspaceDTO{}, err
	}
	dto.CurrentUserRole = accessible.CurrentUserRole
	return dto, nil
}

func (routes *WorkspaceRoutes) toAccessibleWorkspaceDTO(
	ctx context.Context,
	value workspace.AccessibleWorkspace,
) (workspaceDTO, error) {
	dto, err := routes.toWorkspaceDTO(ctx, value.Workspace)
	if err != nil {
		return workspaceDTO{}, err
	}
	dto.CurrentUserRole = value.CurrentUserRole
	return dto, nil
}

func (routes *WorkspaceRoutes) toAccessibleWorkspaceDTOs(
	ctx context.Context,
	values []workspace.AccessibleWorkspace,
) ([]workspaceDTO, error) {
	items := make([]workspaceDTO, len(values))
	for index, value := range values {
		dto, err := routes.toAccessibleWorkspaceDTO(ctx, value)
		if err != nil {
			return nil, err
		}
		items[index] = dto
	}
	return items, nil
}

func (routes *WorkspaceRoutes) toWorkspaceDTOs(
	ctx context.Context,
	values []workspace.Workspace,
) ([]workspaceDTO, error) {
	actorIDs := make([]string, 0, len(values)*2)
	for _, value := range values {
		actorIDs = append(actorIDs, value.CreatedBy, value.UpdatedBy)
	}
	usernames, err := routes.actors.UsernamesByIDs(ctx, actorIDs)
	if err != nil {
		return nil, err
	}
	items := make([]workspaceDTO, len(values))
	for index, value := range values {
		items[index] = toWorkspaceDTO(value)
		items[index].CreatedByUsername = usernames[value.CreatedBy]
		items[index].UpdatedByUsername = usernames[value.UpdatedBy]
	}
	return items, nil
}

func toMemberDTO(value workspace.Member) memberDTO {
	return memberDTO{
		UserID: value.UserID, Role: value.Role, InvitedBy: value.InvitedBy,
		JoinedAt: value.JoinedAt, DisabledAt: value.DisabledAt,
	}
}
