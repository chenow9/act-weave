package httptransport

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"actweave/backend/internal/authn"
	"actweave/backend/internal/authz"
	"actweave/backend/internal/identity"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const refreshCookieName = "actweave_refresh"

type AuthUserService interface {
	Login(context.Context, authn.LoginRequest) (authn.AuthResult, error)
	Refresh(context.Context, string) (authn.AuthResult, error)
	Logout(context.Context, string) error
	ChangePassword(context.Context, string, string, string) error
	ResetPassword(context.Context, string, string) error
	SetUserStatus(context.Context, string, identity.Status, int64) (identity.User, error)
	SetPlatformRole(context.Context, string, identity.PlatformRole, int64) (identity.User, error)
	UnlockUser(context.Context, string, int64) (identity.User, error)
	GetUser(context.Context, string) (identity.User, error)
	ListUsers(context.Context, int) ([]identity.User, error)
	SearchUsers(context.Context, identity.UserListQuery) (identity.UserPage, error)
	ListUserWorkspaceMemberships(context.Context, string, bool) ([]identity.UserWorkspaceMembership, error)
	CreateUser(context.Context, authn.CreateUserRequest) (identity.User, error)
	UpdateUserProfile(context.Context, string, identity.UserProfileUpdate) (identity.User, error)
	AdminCreateUser(context.Context, string, authn.CreateUserRequest) (identity.User, error)
	AdminUpdateUserProfile(context.Context, string, string, identity.UserProfileUpdate) (identity.User, error)
	AdminSetUserStatus(context.Context, string, string, identity.Status, int64) (identity.User, error)
	AdminSetPlatformRole(context.Context, string, string, identity.PlatformRole, int64) (identity.User, error)
	AdminResetPassword(context.Context, string, string, string) error
	AdminUnlockUser(context.Context, string, string, int64) (identity.User, error)
}

type AuthUserRoutes struct{ service AuthUserService }

func NewAuthUserRoutes(service AuthUserService) (*AuthUserRoutes, error) {
	if service == nil {
		return nil, errors.New("auth user service is required")
	}
	return &AuthUserRoutes{service: service}, nil
}

func (routes *AuthUserRoutes) RegisterV1(v1 V1Routes) {
	v1.Public.POST("/auth/login", routes.login)
	v1.Public.POST("/auth/refresh", routes.refresh)
	// Cookie-driven and idempotent: logout must remain possible after the
	// in-memory Access Token has expired or was cleared by the client.
	v1.Public.POST("/auth/logout", routes.logout)
	v1.Protected.GET("/users/me", routes.me)
	v1.Protected.PATCH("/users/me", routes.updateMe)
	v1.Protected.POST("/users/me/__command/change-password", routes.changePassword)
	v1.Protected.GET("/admin/users", routes.listUsers)
	v1.Protected.POST("/admin/users", routes.createUser)
	v1.Protected.GET("/admin/users/:uid/workspaces", routes.listUserWorkspaces)
	v1.Protected.PATCH("/admin/users/:uid", routes.updateUser)
	v1.Protected.POST("/admin/users/:uid/__command/change-platform-role", routes.changePlatformRole)
	v1.Protected.POST("/admin/users/:uid/__command/reset-password", routes.resetPassword)
	v1.Protected.POST("/admin/users/:uid/__command/unlock", routes.unlockUser)
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type tokenResponse struct {
	AccessToken        string    `json:"accessToken"`
	AccessTokenExpires time.Time `json:"accessTokenExpires"`
	SessionID          string    `json:"sessionId"`
	MustChangePassword bool      `json:"mustChangePassword"`
	User               userDTO   `json:"user"`
}

type userDTO struct {
	ID           string                `json:"id"`
	Username     string                `json:"username"`
	Email        *string               `json:"email,omitempty"`
	DisplayName  string                `json:"displayName"`
	AvatarURL    *string               `json:"avatarUrl,omitempty"`
	Status       identity.Status       `json:"status"`
	PlatformRole identity.PlatformRole `json:"platformRole"`
	Locale       string                `json:"locale"`
	Timezone     string                `json:"timezone"`
	LastLoginAt  *time.Time            `json:"lastLoginAt,omitempty"`
	CreatedAt    time.Time             `json:"createdAt"`
	UpdatedAt    time.Time             `json:"updatedAt"`
	LockVersion  int64                 `json:"lockVersion"`
}

func (routes *AuthUserRoutes) login(c *gin.Context) {
	var request loginRequest
	if decodeJSON(c, &request) != nil || strings.TrimSpace(request.Username) == "" || request.Password == "" {
		RespondError(c, identity.ErrInvalid)
		return
	}
	contextValue, _ := RequestContextFrom(c.Request.Context())
	userAgent, sourceIP := optionalString(contextValue.UserAgent), optionalString(contextValue.SourceIP)
	result, err := routes.service.Login(c.Request.Context(), authn.LoginRequest{
		Username: request.Username, Password: request.Password,
		UserAgent: userAgent, IP: sourceIP,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	setRefreshCookie(c, result.RefreshToken, result.RefreshExpiresAt)
	c.JSON(http.StatusOK, tokenResult(result))
}

func (routes *AuthUserRoutes) refresh(c *gin.Context) {
	token, err := c.Cookie(refreshCookieName)
	if err != nil || token == "" {
		RespondError(c, ErrUnauthenticated)
		return
	}
	result, err := routes.service.Refresh(c.Request.Context(), token)
	if err != nil {
		// Do not clear the cookie on refresh failure. A concurrent request may
		// already have rotated it and written the replacement; a stale failure
		// response must not delete the winner's valid cookie.
		RespondError(c, err)
		return
	}
	setRefreshCookie(c, result.RefreshToken, result.RefreshExpiresAt)
	c.JSON(http.StatusOK, tokenResult(result))
}

func (routes *AuthUserRoutes) logout(c *gin.Context) {
	token, err := c.Cookie(refreshCookieName)
	if err != nil || token == "" {
		clearRefreshCookie(c)
		c.Status(http.StatusNoContent)
		return
	}
	if err := routes.service.Logout(c.Request.Context(), token); err != nil {
		if errors.Is(err, authn.ErrRefreshRejected) {
			clearRefreshCookie(c)
			c.Status(http.StatusNoContent)
			return
		}
		RespondError(c, err)
		return
	}
	clearRefreshCookie(c)
	c.Status(http.StatusNoContent)
}

func (routes *AuthUserRoutes) me(c *gin.Context) {
	principal, _ := PrincipalFrom(c.Request.Context())
	user, err := routes.service.GetUser(c.Request.Context(), principal.UserID)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserDTO(user))
}

type updateUserProfileRequest struct {
	DisplayName *string `json:"displayName"`
	Email       *string `json:"email"`
	AvatarURL   *string `json:"avatarUrl"`
	Locale      *string `json:"locale"`
	Timezone    *string `json:"timezone"`
	LockVersion int64   `json:"lockVersion"`
}

func (routes *AuthUserRoutes) updateMe(c *gin.Context) {
	var request updateUserProfileRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	user, err := routes.service.UpdateUserProfile(c.Request.Context(), principal.UserID, profileUpdate(request))
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserDTO(user))
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (routes *AuthUserRoutes) changePassword(c *gin.Context) {
	var request changePasswordRequest
	if decodeJSON(c, &request) != nil || len(request.NewPassword) < 12 || request.CurrentPassword == "" {
		RespondError(c, identity.ErrInvalid)
		return
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	if err := routes.service.ChangePassword(
		c.Request.Context(), principal.UserID, request.CurrentPassword, request.NewPassword,
	); err != nil {
		RespondError(c, err)
		return
	}
	clearRefreshCookie(c)
	c.Status(http.StatusNoContent)
}

func (routes *AuthUserRoutes) listUsers(c *gin.Context) {
	if !platformAdmin(c) {
		RespondError(c, authz.ErrDenied)
		return
	}
	query, page, pageSize, err := parseUserListQuery(c)
	if err != nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	users, err := routes.service.SearchUsers(c.Request.Context(), query)
	if err != nil {
		RespondError(c, err)
		return
	}
	response := make([]userDTO, len(users.Items))
	for index, user := range users.Items {
		response[index] = toUserDTO(user)
	}
	c.JSON(http.StatusOK, gin.H{
		"items":      response,
		"pagination": gin.H{"page": page, "pageSize": pageSize, "total": users.Total},
	})
}

func parseUserListQuery(c *gin.Context) (identity.UserListQuery, int, int, error) {
	page, pageSize := 1, 20
	var err error
	if raw := strings.TrimSpace(c.Query("page")); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil {
			return identity.UserListQuery{}, 0, 0, err
		}
	}
	rawPageSize := strings.TrimSpace(c.Query("pageSize"))
	if rawPageSize == "" {
		rawPageSize = strings.TrimSpace(c.Query("limit"))
	}
	if rawPageSize != "" {
		pageSize, err = strconv.Atoi(rawPageSize)
		if err != nil {
			return identity.UserListQuery{}, 0, 0, err
		}
	}
	search := strings.TrimSpace(c.Query("query"))
	if page < 1 || page > 1_000_000 || pageSize < 1 || pageSize > 100 || len(search) > 200 {
		return identity.UserListQuery{}, 0, 0, identity.ErrInvalid
	}
	query := identity.UserListQuery{
		Query: search, Limit: pageSize, Offset: (page - 1) * pageSize,
	}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		status := identity.Status(raw)
		if status != identity.StatusActive && status != identity.StatusLocked && status != identity.StatusDisabled {
			return identity.UserListQuery{}, 0, 0, identity.ErrInvalid
		}
		query.Status = &status
	}
	if raw := strings.TrimSpace(c.Query("platformRole")); raw != "" {
		role := identity.PlatformRole(raw)
		if role != identity.PlatformRoleUser && role != identity.PlatformRoleAdmin {
			return identity.UserListQuery{}, 0, 0, identity.ErrInvalid
		}
		query.PlatformRole = &role
	}
	return query, page, pageSize, nil
}

type userWorkspaceMembershipDTO struct {
	WorkspaceID          string     `json:"workspaceId"`
	WorkspaceSlug        string     `json:"workspaceSlug"`
	WorkspaceDisplayName string     `json:"workspaceDisplayName"`
	WorkspaceStatus      string     `json:"workspaceStatus"`
	Role                 string     `json:"role"`
	JoinedAt             time.Time  `json:"joinedAt"`
	DisabledAt           *time.Time `json:"disabledAt,omitempty"`
}

func (routes *AuthUserRoutes) listUserWorkspaces(c *gin.Context) {
	if !platformAdmin(c) {
		RespondError(c, authz.ErrDenied)
		return
	}
	if !validPathUUID(c.Param("uid")) {
		RespondError(c, identity.ErrInvalid)
		return
	}
	values, err := routes.service.ListUserWorkspaceMemberships(
		c.Request.Context(), c.Param("uid"), c.Query("includeDisabled") == "true",
	)
	if err != nil {
		RespondError(c, err)
		return
	}
	items := make([]userWorkspaceMembershipDTO, len(values))
	for index, value := range values {
		items[index] = userWorkspaceMembershipDTO{
			WorkspaceID: value.WorkspaceID, WorkspaceSlug: value.WorkspaceSlug,
			WorkspaceDisplayName: value.WorkspaceDisplayName, WorkspaceStatus: value.WorkspaceStatus,
			Role: value.Role, JoinedAt: value.JoinedAt, DisabledAt: value.DisabledAt,
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type createUserRequest struct {
	Username     string                `json:"username"`
	Email        *string               `json:"email"`
	DisplayName  string                `json:"displayName"`
	AvatarURL    *string               `json:"avatarUrl"`
	Password     string                `json:"password"`
	PlatformRole identity.PlatformRole `json:"platformRole"`
	Locale       string                `json:"locale"`
	Timezone     string                `json:"timezone"`
}

func (routes *AuthUserRoutes) createUser(c *gin.Context) {
	if !platformAdmin(c) {
		RespondError(c, authz.ErrDenied)
		return
	}
	var request createUserRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, identity.ErrInvalid)
		return
	}
	id, err := uuid.NewV7()
	if err != nil {
		RespondError(c, err)
		return
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	user, err := routes.service.AdminCreateUser(c.Request.Context(), principal.UserID, authn.CreateUserRequest{
		ID: id.String(), Username: request.Username, Email: request.Email,
		DisplayName: request.DisplayName, AvatarURL: request.AvatarURL, Password: request.Password,
		Status: identity.StatusActive, PlatformRole: request.PlatformRole,
		Locale: request.Locale, Timezone: request.Timezone, MustChangePassword: true,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toUserDTO(user))
}

type updateAdminUserRequest struct {
	DisplayName *string          `json:"displayName"`
	Email       *string          `json:"email"`
	AvatarURL   *string          `json:"avatarUrl"`
	Locale      *string          `json:"locale"`
	Timezone    *string          `json:"timezone"`
	Status      *identity.Status `json:"status"`
	LockVersion int64            `json:"lockVersion"`
}

func (routes *AuthUserRoutes) updateUser(c *gin.Context) {
	if !platformAdmin(c) {
		RespondError(c, authz.ErrDenied)
		return
	}
	var request updateAdminUserRequest
	if decodeJSON(c, &request) != nil || !validPathUUID(c.Param("uid")) {
		RespondError(c, identity.ErrInvalid)
		return
	}
	profileChanged := request.DisplayName != nil || request.Email != nil || request.AvatarURL != nil ||
		request.Locale != nil || request.Timezone != nil
	if request.Status != nil && profileChanged {
		RespondError(c, identity.ErrInvalid)
		return
	}
	var user identity.User
	var err error
	principal, _ := PrincipalFrom(c.Request.Context())
	if request.Status != nil {
		user, err = routes.service.AdminSetUserStatus(
			c.Request.Context(), principal.UserID, c.Param("uid"), *request.Status, request.LockVersion,
		)
	} else {
		user, err = routes.service.AdminUpdateUserProfile(c.Request.Context(), principal.UserID, c.Param("uid"), profileUpdate(
			updateUserProfileRequest{
				DisplayName: request.DisplayName, Email: request.Email, AvatarURL: request.AvatarURL,
				Locale: request.Locale, Timezone: request.Timezone, LockVersion: request.LockVersion,
			},
		))
	}
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserDTO(user))
}

type changePlatformRoleRequest struct {
	PlatformRole identity.PlatformRole `json:"platformRole"`
	LockVersion  int64                 `json:"lockVersion"`
}

func (routes *AuthUserRoutes) changePlatformRole(c *gin.Context) {
	if !platformAdmin(c) {
		RespondError(c, authz.ErrDenied)
		return
	}
	var request changePlatformRoleRequest
	if decodeJSON(c, &request) != nil || !validPathUUID(c.Param("uid")) {
		RespondError(c, identity.ErrInvalid)
		return
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	user, err := routes.service.AdminSetPlatformRole(
		c.Request.Context(), principal.UserID, c.Param("uid"), request.PlatformRole, request.LockVersion,
	)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserDTO(user))
}

type resetPasswordRequest struct {
	TemporaryPassword string `json:"temporaryPassword"`
}

func (routes *AuthUserRoutes) resetPassword(c *gin.Context) {
	if !platformAdmin(c) {
		RespondError(c, authz.ErrDenied)
		return
	}
	var request resetPasswordRequest
	if decodeJSON(c, &request) != nil || len(request.TemporaryPassword) < 12 || !validPathUUID(c.Param("uid")) {
		RespondError(c, identity.ErrInvalid)
		return
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	if err := routes.service.AdminResetPassword(
		c.Request.Context(), principal.UserID, c.Param("uid"), request.TemporaryPassword,
	); err != nil {
		RespondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type unlockUserRequest struct {
	LockVersion int64 `json:"lockVersion"`
}

func (routes *AuthUserRoutes) unlockUser(c *gin.Context) {
	if !platformAdmin(c) {
		RespondError(c, authz.ErrDenied)
		return
	}
	var request unlockUserRequest
	if decodeJSON(c, &request) != nil || !validPathUUID(c.Param("uid")) {
		RespondError(c, identity.ErrInvalid)
		return
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	user, err := routes.service.AdminUnlockUser(
		c.Request.Context(), principal.UserID, c.Param("uid"), request.LockVersion,
	)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserDTO(user))
}

func tokenResult(result authn.AuthResult) tokenResponse {
	return tokenResponse{
		AccessToken: result.AccessToken, AccessTokenExpires: result.AccessTokenExpires,
		SessionID: result.SessionID, MustChangePassword: result.MustChangePassword,
		User: toUserDTO(result.User),
	}
}

func toUserDTO(user identity.User) userDTO {
	return userDTO{
		ID: user.ID, Username: user.Username, Email: user.Email, DisplayName: user.DisplayName,
		AvatarURL: user.AvatarURL, Status: user.Status, PlatformRole: user.PlatformRole,
		Locale: user.Locale, Timezone: user.Timezone, LastLoginAt: user.LastLoginAt,
		CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt, LockVersion: user.LockVersion,
	}
}

func profileUpdate(request updateUserProfileRequest) identity.UserProfileUpdate {
	return identity.UserProfileUpdate{
		DisplayName: request.DisplayName, Email: request.Email, AvatarURL: request.AvatarURL,
		Locale: request.Locale, Timezone: request.Timezone,
		ExpectedLockVersion: request.LockVersion,
	}
}

func setRefreshCookie(c *gin.Context, value string, expiresAt time.Time) {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(c.Writer, refreshCookie(value, expiresAt.UTC(), maxAge, requestIsHTTPS(c.Request)))
}

func clearRefreshCookie(c *gin.Context) {
	http.SetCookie(c.Writer, refreshCookie("", time.Unix(1, 0).UTC(), -1, requestIsHTTPS(c.Request)))
}

func refreshCookie(value string, expiresAt time.Time, maxAge int, secure bool) *http.Cookie {
	return &http.Cookie{
		Name: refreshCookieName, Value: value, Path: "/api/v1/auth",
		Expires: expiresAt, MaxAge: maxAge, HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteStrictMode,
	}
}

// requestIsHTTPS reports the browser-facing scheme. Direct TLS or the first
// X-Forwarded-Proto value (set by the Console nginx) counts as HTTPS so the
// refresh cookie can stay Secure on TLS and still be stored on intranet HTTP.
func requestIsHTTPS(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	proto := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")))
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = strings.TrimSpace(proto[:i])
	}
	return proto == "https"
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	result := value
	return &result
}

func platformAdmin(c *gin.Context) bool {
	principal, exists := PrincipalFrom(c.Request.Context())
	return exists && principal.PlatformRole == string(identity.PlatformRoleAdmin)
}

func validPathUUID(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}
