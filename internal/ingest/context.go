package ingest

import "context"

// ctxKeyUserID is the context key for the authenticated user's display name.
type ctxKeyUserID struct{}

// WithUserName returns a new context carrying the authenticated user's name.
// Used by auth middleware to propagate the user identity to the ingest handler.
func WithUserName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, ctxKeyUserID{}, name)
}

// UserNameFromContext extracts the user name injected by auth middleware, or "".
func UserNameFromContext(ctx context.Context) string {
	if name, ok := ctx.Value(ctxKeyUserID{}).(string); ok {
		return name
	}
	return ""
}
