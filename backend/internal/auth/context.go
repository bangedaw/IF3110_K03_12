package auth

import (
	"context"

	"github.com/google/uuid"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
)

type principalKey struct{}

type familyKey struct{}

func WithPrincipal(ctx context.Context, p authz.Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func PrincipalFrom(ctx context.Context) (authz.Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(authz.Principal)
	return p, ok
}

func withFamily(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, familyKey{}, id)
}

func familyFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(familyKey{}).(uuid.UUID)
	return id, ok
}
