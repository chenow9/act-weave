package httptransport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"actweave/backend/internal/aapfile"

	"github.com/gin-gonic/gin"
)

// AAPFileCallbackService is the domain surface for processor callbacks (IC-05).
type AAPFileCallbackService interface {
	LookupDeliveryForCallback(ctx context.Context, deliveryID string) (
		aapfile.ProcessingJob, aapfile.WorkspaceFileProcessor, aapfile.File, error,
	)
	HandleProcessorCallback(ctx context.Context, input aapfile.HandleProcessorCallbackInput) (
		aapfile.HandleProcessorCallbackResult, error,
	)
}

// AAPFileSecretResolver resolves processor HMAC secrets.
type AAPFileSecretResolver interface {
	Resolve(ctx context.Context, workspaceID, secretRef string) (string, error)
}

// ConfigureProcessorCallback wires the HMAC callback path (no Bearer).
// Must be called before RegisterAgentAccessV1 when callbacks are enabled.
func (routes *AAPFileRoutes) ConfigureProcessorCallback(
	domain AAPFileCallbackService,
	secrets AAPFileSecretResolver,
) error {
	if routes == nil || domain == nil {
		return aapfile.ErrInvalid
	}
	if secrets == nil {
		secrets = aapfile.InlineSecretResolver{}
	}
	routes.callback = domain
	routes.secrets = secrets
	return nil
}

// Register callback route on the Public group (no aapAuthenticationMiddleware).
func (routes *AAPFileRoutes) registerProcessorCallback(v1 AgentAccessV1Routes) {
	if routes == nil || routes.callback == nil {
		return
	}
	v1.Public.POST(
		"/internal/file-processor/callbacks/:deliveryId",
		routes.processorCallback,
	)
}

func (routes *AAPFileRoutes) processorCallback(c *gin.Context) {
	if routes == nil || routes.callback == nil {
		RespondError(c, aapfile.ErrInvalid)
		return
	}
	// Feature gate: when files disabled, conceal as not found.
	if routes.gate == nil || !routes.gate.Enabled {
		RespondError(c, ErrAAPFilesFeatureDisabled)
		return
	}
	deliveryID := strings.TrimSpace(c.Param("deliveryId"))
	if deliveryID == "" {
		RespondError(c, aapfile.ErrInvalid)
		return
	}

	// Body limit 384 KiB (design §5.5.5).
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, aapfile.CallbackBodyMaxBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			RespondError(c, aapfile.ErrInvalid)
			return
		}
		RespondError(c, aapfile.ErrInvalid)
		return
	}

	job, proc, _, err := routes.callback.LookupDeliveryForCallback(c.Request.Context(), deliveryID)
	if err != nil {
		if errors.Is(err, aapfile.ErrNotFound) {
			RespondError(c, aapfile.ErrNotFound)
			return
		}
		if errors.Is(err, aapfile.ErrCallbackLate) {
			RespondError(c, aapfile.ErrCallbackLate)
			return
		}
		RespondError(c, err)
		return
	}
	// Late check before HMAC (delivery may already be TIMED_OUT).
	if job.Status == aapfile.JobTimedOut {
		RespondError(c, aapfile.ErrCallbackLate)
		return
	}
	if job.Status == aapfile.JobDelivered && job.DeadlineAt != nil &&
		!job.DeadlineAt.After(time.Now().UTC()) {
		RespondError(c, aapfile.ErrCallbackLate)
		return
	}

	secret, err := routes.secrets.Resolve(c.Request.Context(), proc.WorkspaceID, proc.SecretRef)
	if err != nil {
		RespondError(c, aapfile.ErrCallbackUnauthorized)
		return
	}
	sig := c.GetHeader(aapfile.SignatureHeaderName)
	if err := aapfile.VerifySignature(secret, sig, body, time.Now().UTC(), aapfile.CallbackSignatureSkew); err != nil {
		RespondError(c, aapfile.ErrCallbackUnauthorized)
		return
	}

	// Optional: processorId in body should match stage when present.
	result, err := routes.callback.HandleProcessorCallback(c.Request.Context(), aapfile.HandleProcessorCallbackInput{
		DeliveryID: deliveryID,
		Body:       body,
	})
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"deliveryId": deliveryID,
		"status":     strings.ToLower(result.Job.Status),
		"fileStatus": publicAAPFileStatus(result.File.Status),
	})
}
