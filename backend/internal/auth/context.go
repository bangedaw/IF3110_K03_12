package auth

import (
	"context"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
)

type principalKey struct{}

func WithPrincipal(ctx context.Context, p authz.Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func PrincipalFrom(ctx context.Context) (authz.Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(authz.Principal)
	return p, ok
}
