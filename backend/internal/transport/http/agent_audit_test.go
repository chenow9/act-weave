package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"actweave/backend/internal/agentaudit"

	"github.com/gin-gonic/gin"
)

type fakeAgentAuditQuery struct {
	list   agentaudit.ListResult
	detail agentaudit.TraceDetail
}

func (f *fakeAgentAuditQuery) DebugMode() bool { return false }

func (f *fakeAgentAuditQuery) ListTraces(context.Context, string, agentaudit.ListFilter) (agentaudit.ListResult, error) {
	return f.list, nil
}

func (f *fakeAgentAuditQuery) GetTrace(_ context.Context, _, _ string, filter agentaudit.DetailFilter) (agentaudit.TraceDetail, error) {
	return agentaudit.PageTimelineSteps(f.detail, filter), nil
}

func TestAgentAuditRoutesRequirePlatformAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	queries := &fakeAgentAuditQuery{
		list: agentaudit.ListResult{
			Items: []agentaudit.TraceListItem{{TraceID: "trace-1", Status: "success"}},
			Stats: agentaudit.Stats{TotalRuns: 1},
		},
		detail: agentaudit.TraceDetail{
			TraceID: "trace-1",
			Steps: []agentaudit.Step{{
				Type: "reasoning", Title: "大模型推理",
				Content: agentaudit.MissingReasoningText, ContentState: agentaudit.ContentMissing,
			}},
		},
	}
	routes, err := NewAgentAuditRoutes(queries)
	if err != nil {
		t.Fatalf("routes: %v", err)
	}
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	v1.Use(func(c *gin.Context) {
		role := c.GetHeader("X-Test-Role")
		principal := Principal{UserID: "user-1", PlatformRole: role}
		ctx := context.WithValue(c.Request.Context(), principalContextKey{}, principal)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	routes.RegisterV1(V1Routes{Protected: v1})

	wid := "018f0000-0000-7000-8000-000000000001"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+wid+"/agent-audit/traces", nil)
	req.Header.Set("X-Test-Role", "PLATFORM_USER")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin list status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+wid+"/agent-audit/traces", nil)
	req.Header.Set("X-Test-Role", "PLATFORM_ADMIN")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list agentaudit.ListResult
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("list body: %s err=%v", rec.Body.String(), err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+wid+"/agent-audit/traces/trace-1", nil)
	req.Header.Set("X-Test-Role", "PLATFORM_ADMIN")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin detail status=%d body=%s", rec.Code, rec.Body.String())
	}
	var detail agentaudit.TraceDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("detail: %v", err)
	}
	if len(detail.Steps) == 0 || detail.Steps[0].Content != agentaudit.MissingReasoningText {
		t.Fatalf("expected 无推理数据 in steps: %+v", detail.Steps)
	}
}
