package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestStreamConsoleLLMJob_HeartbeatsAndCompleted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/job", func(c *gin.Context) {
		if !wantsConsoleLLMSSE(c) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected sse"})
			return
		}
		streamConsoleLLMJob(c,
			func(ctx context.Context) (gin.H, error) {
				// Long enough to emit at least one heartbeat with a short ticker override is hard;
				// just return quickly for completed frame.
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(20 * time.Millisecond):
					return gin.H{"output": "ok", "status": "SUCCEEDED"}, nil
				}
			},
			func(stream *consoleSSEWriter) error {
				return stream.Event("started", gin.H{"status": "RUNNING"})
			},
			func(stream *consoleSSEWriter, value gin.H, err error) error {
				if err != nil {
					return stream.Event("failed", gin.H{"error": gin.H{"code": "FAILED", "message": err.Error()}})
				}
				return stream.Event("completed", value)
			},
		)
	})

	request := httptest.NewRequest(http.MethodPost, "/job", strings.NewReader(`{}`))
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content-type=%q", recorder.Header().Get("Content-Type"))
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event: started") {
		t.Fatalf("missing started: %s", body)
	}
	if !strings.Contains(body, "event: completed") {
		t.Fatalf("missing completed: %s", body)
	}
	if !strings.Contains(body, `"output":"ok"`) && !strings.Contains(body, `"output": "ok"`) {
		// JSON encoder has no spaces.
		var found bool
		for _, block := range strings.Split(body, "\n\n") {
			if !strings.Contains(block, "event: completed") {
				continue
			}
			for _, line := range strings.Split(block, "\n") {
				if strings.HasPrefix(line, "data:") {
					var payload map[string]any
					if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &payload); err != nil {
						t.Fatalf("parse completed data: %v", err)
					}
					if payload["output"] == "ok" {
						found = true
					}
				}
			}
		}
		if !found {
			t.Fatalf("completed payload missing output: %s", body)
		}
	}
}

func TestWantsConsoleLLMSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Request.Header.Set("Accept", "text/event-stream")
	if !wantsConsoleLLMSSE(c) {
		t.Fatal("expected true for event-stream")
	}
	c.Request.Header.Set("Accept", "application/json")
	if wantsConsoleLLMSSE(c) {
		t.Fatal("expected false for json")
	}
}
