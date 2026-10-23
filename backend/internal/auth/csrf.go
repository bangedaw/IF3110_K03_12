package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"

	"github.com/google/uuid"
)

const CSRFHeader = "X-CSRF-Token"

func (s *Service) csrfToken(familyID uuid.UUID) string {
	mac := hmac.New(sha256.New, s.csrfKey)
	mac.Write(familyID[:])
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) validCSRF(familyID uuid.UUID, token string) bool {
	return token != "" && hmac.Equal([]byte(token), []byte(s.csrfToken(familyID)))
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}
