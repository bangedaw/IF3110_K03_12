package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashPasswordUsesRandomSalt(t *testing.T) {
	a, err := HashPassword("rahasia123")
	require.NoError(t, err)
	b, err := HashPassword("rahasia123")
	require.NoError(t, err)

	assert.NotEqual(t, a, b)
	assert.True(t, strings.HasPrefix(a, "$argon2id$v=19$m=19456,t=2,p=1$"))
	assert.NotContains(t, a, "rahasia123")
}

func TestVerifyPassword(t *testing.T) {
	h, err := HashPassword("rahasia123")
	require.NoError(t, err)

	ok, err := VerifyPassword(h, "rahasia123")
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = VerifyPassword(h, "salah")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, h := range []string{"", "plaintext", "$argon2i$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", "$argon2id$v=19$m=x$c2FsdA$aGFzaA", "$argon2id$v=19$m=1,t=1,p=1$!!$aGFzaA"} {
		_, err := VerifyPassword(h, "x")
		assert.Error(t, err, h)
	}
}
