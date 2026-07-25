package httptransport

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"actweave/backend/internal/outboundidentity"

	"github.com/gin-gonic/gin"
)

// MaxOutboundEntryBodyBytes caps top-level request bodies that may carry
// write-only outbound credentials (AAP create run, direct invoke, tool test, trial).
const MaxOutboundEntryBodyBytes = 256 * 1024

// OutboundCredentialsBody is the transport-only split of a top-level request.
// Plaintext Value material lives only in CredentialsRaw and must be zeroed after
// BindingAttacher.Attach (or discard on idempotent replay).
type OutboundCredentialsBody struct {
	// BusinessJSON is the request body with outboundCredentials removed.
	BusinessJSON []byte
	// CredentialsRaw is the outbound-credentials.v1 object (not the wrapper).
	// Nil when the field was absent.
	CredentialsRaw json.RawMessage
}

// ReadOutboundCredentialsBody reads the request body once, extracts the write-only
// envelope, and returns a business body without Token material.
//
// Callers must:
//  1. decode business fields only from BusinessJSON;
//  2. pass CredentialsRaw to BindingAttacher before durable request hashing;
//  3. call Zero() when finished (success or failure).
func ReadOutboundCredentialsBody(c *gin.Context) (OutboundCredentialsBody, error) {
	var zero OutboundCredentialsBody
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return zero, ErrAAPCreateRunInvalid
	}
	limited := io.LimitReader(c.Request.Body, MaxOutboundEntryBodyBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return zero, ErrAAPCreateRunInvalid
	}
	_ = c.Request.Body.Close()
	if len(raw) > MaxOutboundEntryBodyBytes {
		// Overwrite before return.
		for i := range raw {
			raw[i] = 0
		}
		return zero, outboundidentity.ErrCredentialInvalid
	}
	creds, err := outboundidentity.ExtractOutboundCredentialsRaw(raw)
	if err != nil {
		for i := range raw {
			raw[i] = 0
		}
		return zero, err
	}
	business, err := outboundidentity.StripOutboundCredentialsFromBody(raw)
	// Zero original full body (may still hold Value in memory until GC).
	for i := range raw {
		raw[i] = 0
	}
	if err != nil {
		_ = outboundidentity.ZeroCredentialsRaw(creds)
		return zero, err
	}
	// Restore Body for any downstream that still reads it — only business JSON.
	c.Request.Body = io.NopCloser(bytes.NewReader(business))
	return OutboundCredentialsBody{BusinessJSON: business, CredentialsRaw: creds}, nil
}

// Zero wipes credentials raw and business copy (business is non-secret but cleared
// to avoid retaining large payloads longer than needed).
func (b *OutboundCredentialsBody) Zero() {
	if b == nil {
		return
	}
	_ = outboundidentity.ZeroCredentialsRaw(b.CredentialsRaw)
	b.CredentialsRaw = nil
	for i := range b.BusinessJSON {
		b.BusinessJSON[i] = 0
	}
	b.BusinessJSON = nil
}

// DecodeBusinessJSON decodes business fields with DisallowUnknownFields=false
// (business schemas vary). Never pass CredentialsRaw through this path.
func DecodeBusinessJSON(raw []byte, dest any) error {
	if len(raw) == 0 {
		return ErrAAPCreateRunInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(dest); err != nil {
		return err
	}
	return nil
}

// RejectOutboundCredentialsInProductionBody fails closed if a production execute
// body still carries outboundCredentials (production inherits from AgentRun root).
func RejectOutboundCredentialsInProductionBody(body []byte) error {
	creds, err := outboundidentity.ExtractOutboundCredentialsRaw(body)
	if err != nil {
		return err
	}
	if len(creds) > 0 {
		_ = outboundidentity.ZeroCredentialsRaw(creds)
		return outboundidentity.ErrCredentialInvalid
	}
	return nil
}

// ensure compile-time use of http for future Content-Type helpers.
var _ = http.StatusOK
