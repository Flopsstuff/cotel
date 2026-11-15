package ingest

import "context"

// ctxKeyActorID is the context key for the authenticated actor's display name.
type ctxKeyActorID struct{}

// WithActorName returns a new context carrying the authenticated actor's name.
// Used by auth middleware to propagate the actor identity to the ingest handler.
func WithActorName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, ctxKeyActorID{}, name)
}

// ActorNameFromContext extracts the actor name injected by auth middleware, or "".
func ActorNameFromContext(ctx context.Context) string {
	if name, ok := ctx.Value(ctxKeyActorID{}).(string); ok {
		return name
	}
	return ""
}
