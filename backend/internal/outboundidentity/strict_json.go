package outboundidentity

import (
	"bytes"
	"encoding/json"
	"io"
)

func decodeStrictJSON(raw []byte, dest any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ErrIdentityPolicyInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	// Reject trailing tokens so `"{}{}"` cannot smuggle a second document.
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ErrIdentityPolicyInvalid
	}
	return nil
}
