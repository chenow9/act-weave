package audit

import "context"

type RequestContext struct {
	RequestID string
	TraceID   string
	SourceIP  string
	UserAgent string
}

type requestContextKey struct{}

func WithRequestContext(ctx context.Context, value RequestContext) context.Context {
	return context.WithValue(ctx, requestContextKey{}, value)
}

func requestContextFrom(ctx context.Context) RequestContext {
	value, _ := ctx.Value(requestContextKey{}).(RequestContext)
	return value
}
