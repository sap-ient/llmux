package provider

import "context"

// This file carries an OPTIONAL per-request API-key override through the request
// context. It is the mechanism that lets the gateway dispatch a single request
// against an ACCOUNT'S OWN provider key ("bring your own key" / BYOK) instead of
// the provider's statically-configured central key — without changing any
// adapter's construction.
//
// Adapters consult APIKeyFrom(ctx) when building auth headers and fall back to
// their configured key when it is empty (the default, central path). The value
// is a secret and is never logged.

type credentialCtxKey struct{}

// WithAPIKey returns a context carrying a per-request API key that overrides the
// provider's configured key for this call only. An empty key is a no-op so
// callers can pass through unconditionally.
func WithAPIKey(ctx context.Context, apiKey string) context.Context {
	if apiKey == "" {
		return ctx
	}
	return context.WithValue(ctx, credentialCtxKey{}, apiKey)
}

// APIKeyFrom returns the per-request API-key override from ctx, or "" when none
// is set (the central path: adapters then use their configured key).
func APIKeyFrom(ctx context.Context) string {
	k, _ := ctx.Value(credentialCtxKey{}).(string)
	return k
}

// ResolveKey returns the effective key for a request: the per-request BYOK
// override when present, otherwise the provided configured (central) key. Every
// adapter uses this so BYOK works uniformly.
func ResolveKey(ctx context.Context, configured string) string {
	if k := APIKeyFrom(ctx); k != "" {
		return k
	}
	return configured
}
