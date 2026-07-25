package audit

import "errors"

var (
	ErrInvalid         = errors.New("invalid audit event")
	ErrConflict        = errors.New("audit event conflict")
	ErrNotFound        = errors.New("audit event not found")
	ErrPayloadRequired = errors.New("audit detail exceeds inline limit and requires a payload object")
)
