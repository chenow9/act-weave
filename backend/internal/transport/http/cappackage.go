package httptransport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"actweave/backend/internal/authz"
	"actweave/backend/internal/cappackage"

	"github.com/gin-gonic/gin"
)

type PackageService interface {
	Export(context.Context, cappackage.ExportInput) (cappackage.Document, error)
	Preview(context.Context, cappackage.PreviewInput) (cappackage.Preview, error)
	Import(context.Context, cappackage.ImportInput) (cappackage.ImportResult, error)
}

type PackageRoutes struct {
	authorizer WorkspaceAuthorizer
	packages   PackageService
}

func NewPackageRoutes(authorizer WorkspaceAuthorizer, packages PackageService) (*PackageRoutes, error) {
	if authorizer == nil || packages == nil {
		return nil, errors.New("capability package route dependencies are required")
	}
	return &PackageRoutes{authorizer: authorizer, packages: packages}, nil
}

func (r *PackageRoutes) RegisterV1(v1 V1Routes) {
	group := v1.Protected
	group.GET("/workspaces/:wid/tools/:id/__command/export", r.exportTool)
	group.GET("/workspaces/:wid/workflows/:id/__command/export", r.exportWorkflow)
	group.POST("/workspaces/:wid/packages/__command/export", r.exportPackage)
	group.POST("/workspaces/:wid/packages/__command/preview", r.previewPackage)
	group.POST("/workspaces/:wid/packages/__command/import", r.importPackage)
}

func (r *PackageRoutes) authorize(c *gin.Context, action authz.Action) bool {
	if r == nil || r.authorizer == nil {
		RespondError(c, authz.ErrDenied)
		return false
	}
	principal, _ := PrincipalFrom(c.Request.Context())
	if _, err := r.authorizer.AuthorizeWorkspace(c.Request.Context(), principal.UserID, c.Param("wid"), action); err != nil {
		RespondError(c, err)
		return false
	}
	return true
}

func (r *PackageRoutes) exportTool(c *gin.Context) {
	r.writeExport(c, cappackage.ExportInput{
		WorkspaceID: c.Param("wid"),
		ToolIDs:     []string{c.Param("id")},
		Source:      c.Query("source"),
	})
}

func (r *PackageRoutes) exportWorkflow(c *gin.Context) {
	r.writeExport(c, cappackage.ExportInput{
		WorkspaceID: c.Param("wid"),
		WorkflowIDs: []string{c.Param("id")},
		Source:      c.Query("source"),
	})
}

type exportPackageRequest struct {
	ToolIDs     []string `json:"toolIds"`
	WorkflowIDs []string `json:"workflowIds"`
	Source      string   `json:"source"`
}

func (r *PackageRoutes) exportPackage(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	var request exportPackageRequest
	if decodeJSON(c, &request) != nil {
		RespondError(c, cappackage.ErrInvalid)
		return
	}
	r.writeExportAuthorized(c, cappackage.ExportInput{
		WorkspaceID: c.Param("wid"),
		ToolIDs:     request.ToolIDs,
		WorkflowIDs: request.WorkflowIDs,
		Source:      request.Source,
	})
}

func (r *PackageRoutes) writeExport(c *gin.Context, input cappackage.ExportInput) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	r.writeExportAuthorized(c, input)
}

func (r *PackageRoutes) writeExportAuthorized(c *gin.Context, input cappackage.ExportInput) {
	if r.packages == nil {
		RespondError(c, cappackage.ErrInvalid)
		return
	}
	document, err := r.packages.Export(c.Request.Context(), input)
	if err != nil {
		RespondError(c, err)
		return
	}
	payload, err := cappackage.Marshal(document)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+cappackage.FileName(document)+`"`)
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", payload)
}

type packageBodyRequest struct {
	YAML               string                         `json:"yaml"`
	ConnectionBindings []cappackage.ConnectionBinding `json:"connectionBindings"`
}

func (r *PackageRoutes) previewPackage(c *gin.Context) {
	if !r.authorize(c, authz.ActionView) {
		return
	}
	document, bindings, err := r.readPackageBody(c)
	if err != nil {
		RespondError(c, err)
		return
	}
	preview, err := r.packages.Preview(c.Request.Context(), cappackage.PreviewInput{
		WorkspaceID:        c.Param("wid"),
		Document:           document,
		ConnectionBindings: bindings,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"preview": preview})
}

func (r *PackageRoutes) importPackage(c *gin.Context) {
	if !r.authorize(c, authz.ActionEdit) {
		return
	}
	document, bindings, err := r.readPackageBody(c)
	if err != nil {
		RespondError(c, err)
		return
	}
	result, err := r.packages.Import(c.Request.Context(), cappackage.ImportInput{
		WorkspaceID:        c.Param("wid"),
		ActorID:            actor(c),
		Document:           document,
		ConnectionBindings: bindings,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": result})
}

func (r *PackageRoutes) readPackageBody(c *gin.Context) (cappackage.Document, []cappackage.ConnectionBinding, error) {
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(c.GetHeader("Content-Type"), ";")[0]))
	if contentType == "application/json" {
		var request packageBodyRequest
		if decodeJSON(c, &request) != nil || strings.TrimSpace(request.YAML) == "" {
			return cappackage.Document{}, nil, cappackage.ErrInvalid
		}
		document, err := cappackage.Parse([]byte(request.YAML))
		return document, request.ConnectionBindings, err
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, cappackage.MaxBytes)
	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "too large") {
			return cappackage.Document{}, nil, cappackage.ErrTooLarge
		}
		return cappackage.Document{}, nil, cappackage.ErrInvalid
	}
	document, err := cappackage.Parse(payload)
	return document, nil, err
}
