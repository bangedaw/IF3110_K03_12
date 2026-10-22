package secret_test

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/secret"
)

func key(t *testing.T, b byte) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32))
}

func TestRoundTrip(t *testing.T) {
	c, err := secret.New(key(t, 1))
	require.NoError(t, err)

	plain := []byte("rahasia-supplier-123")
	blob := c.Encrypt(plain)

	require.NotEqual(t, plain, blob, "ciphertext tidak boleh sama dengan plaintext")
	got, err := c.Decrypt(blob)
	require.NoError(t, err)
	require.Equal(t, plain, got)
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	c, err := secret.New(key(t, 1))
	require.NoError(t, err)

	plain := []byte("sama")
	require.NotEqual(t, c.Encrypt(plain), c.Encrypt(plain), "nonce harus acak tiap encrypt")
}

func TestEmptyPlaintext(t *testing.T) {
	c, err := secret.New(key(t, 1))
	require.NoError(t, err)

	got, err := c.Decrypt(c.Encrypt(nil))
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestWrongKeyFails(t *testing.T) {
	a, err := secret.New(key(t, 1))
	require.NoError(t, err)
	b, err := secret.New(key(t, 2))
	require.NoError(t, err)

	_, err = b.Decrypt(a.Encrypt([]byte("x")))
	require.ErrorIs(t, err, secret.ErrDecrypt)
}

func TestTamperDetection(t *testing.T) {
	c, err := secret.New(key(t, 1))
	require.NoError(t, err)

	blob := c.Encrypt([]byte("penting"))
	blob[len(blob)-1] ^= 0xFF

	_, err = c.Decrypt(blob)
	require.ErrorIs(t, err, secret.ErrDecrypt)
}

func TestTruncatedBlobFails(t *testing.T) {
	c, err := secret.New(key(t, 1))
	require.NoError(t, err)

	_, err = c.Decrypt([]byte{1, 2, 3})
	require.ErrorIs(t, err, secret.ErrDecrypt)
}

func TestNewRejectsBadKey(t *testing.T) {
	for name, k := range map[string]string{
		"bukan base64": "!!bukan-base64!!",
		"terlalu pendek": base64.StdEncoding.EncodeToString([]byte("pendek")),
		"terlalu panjang": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 64)),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := secret.New(k)
			require.ErrorIs(t, err, secret.ErrInvalidKey)
		})
	}
}
