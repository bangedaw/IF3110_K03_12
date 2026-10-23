package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

var (
	ErrInvalidKey = errors.New("master key tidak valid")

	ErrDecrypt = errors.New("gagal mendekripsi")
)

const (
	keySize   = 32
	nonceSize = 12
)

type Cipher struct {
	aead cipher.AEAD
}

func New(keyBase64 string) (*Cipher, error) {
	raw, err := base64.StdEncoding.DecodeString(keyBase64)
	if err != nil {
		return nil, fmt.Errorf("%w: bukan base64 yang valid", ErrInvalidKey)
	}
	if len(raw) != keySize {
		return nil, fmt.Errorf("%w: panjang %d byte, harus %d byte", ErrInvalidKey, len(raw), keySize)
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(plaintext []byte) []byte {
	nonce := make([]byte, nonceSize)
	_, _ = io.ReadFull(rand.Reader, nonce)

	return c.aead.Seal(nonce, nonce, plaintext, nil)
}

func (c *Cipher) Decrypt(blob []byte) ([]byte, error) {
	if len(blob) < nonceSize+c.aead.Overhead() {
		return nil, ErrDecrypt
	}
	plaintext, err := c.aead.Open(nil, blob[:nonceSize], blob[nonceSize:], nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}
