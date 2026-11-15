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

// ctxKeyIngestKey marks a request authorized by a shared ingest key.
type ctxKeyIngestKey struct{}

// WithIngestKeyAuth marks the request as authorized by a shared ingest key, which
// lets the handler attribute its spans by paperclip.agent.id when no actor token
// identified the sender (ADR-0029). Unauthenticated requests never get this mark,
// so an anonymous sender cannot claim an agent's identity through an attribute.
func WithIngestKeyAuth(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeyIngestKey{}, true)
}

// IngestKeyAuthFromContext reports whether WithIngestKeyAuth marked the request.
func IngestKeyAuthFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyIngestKey{}).(bool)
	return v
}
