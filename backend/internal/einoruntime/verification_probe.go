package einoruntime

import "context"

type verificationProbeKey struct{}

// WithVerificationProbe marks ctx as a capability probe. Search executors
// skip production search counters on this context.
func WithVerificationProbe(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, verificationProbeKey{}, true)
}

func isVerificationProbe(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	ok, _ := ctx.Value(verificationProbeKey{}).(bool)
	return ok
}
