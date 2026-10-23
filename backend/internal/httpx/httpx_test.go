package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
)

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   apperr.Code
	}{
		{"forbidden", apperr.ErrForbidden, 403, apperr.CodeForbidden},
		{"not found", apperr.New(apperr.CodeNotFound, "table x"), 404, apperr.CodeNotFound},
		{"validation", apperr.Validation(map[string]string{"username": "wajib"}), 422, apperr.CodeValidation},
		{"db unreachable", apperr.Wrap(apperr.CodeDBUnreachable, "db mati", errors.New("dial tcp")), 503, apperr.CodeDBUnreachable},
		{"unknown", errors.New("boom secret"), 500, "INTERNAL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			Error(rec, httptest.NewRequest(http.MethodGet, "/x", nil), tc.err)

			assert.Equal(t, tc.status, rec.Code)
			assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
			var p Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, tc.code, p.Code)
			assert.Equal(t, tc.status, p.Status)
			assert.NotContains(t, rec.Body.String(), "boom secret")
			assert.NotContains(t, rec.Body.String(), "dial tcp")
		})
	}
}

func TestErrorsIsMatchesByCode(t *testing.T) {
	assert.ErrorIs(t, apperr.New(apperr.CodeNotFound, "detail"), apperr.ErrNotFound)
	assert.NotErrorIs(t, apperr.New(apperr.CodeNotFound, "detail"), apperr.ErrForbidden)
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	var v struct {
		Username string `json:"username"`
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"username":"a","extra":1}`))
	err := Decode(httptest.NewRecorder(), req, &v)
	assert.ErrorIs(t, err, apperr.ErrValidation)
}

func TestLoggerDoesNotLogQueryOrBody(t *testing.T) {
	var buf bytes.Buffer
	r := NewRouter(slog.New(slog.NewJSONHandler(&buf, nil)))
	r.Post("/api/auth/login", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login?token=leak1", strings.NewReader(`{"password":"leak2"}`))
	req.Header.Set("Cookie", "dbmp_session=leak3")
	r.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	assert.Contains(t, out, "/api/auth/login")
	for _, s := range []string{"leak1", "leak2", "leak3"} {
		assert.NotContains(t, out, s)
	}
}

func TestRecoverReturnsProblem(t *testing.T) {
	r := NewRouter(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	r.Get("/panic", func(http.ResponseWriter, *http.Request) { panic("x") })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))
	assert.Equal(t, 500, rec.Code)
	assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
}

func TestCrossOriginRequestRejected(t *testing.T) {
	r := NewRouter(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	r.Post("/api/x", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	r.Get("/api/x", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })

	cases := []struct {
		method, site string
		status       int
	}{
		{http.MethodPost, "cross-site", http.StatusForbidden},
		{http.MethodPost, "same-origin", http.StatusNoContent},
		{http.MethodPost, "", http.StatusNoContent},
		{http.MethodGet, "cross-site", http.StatusNoContent},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, "/api/x", nil)
		if tc.site != "" {
			req.Header.Set("Sec-Fetch-Site", tc.site)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, tc.status, rec.Code, tc)
		if tc.status == http.StatusForbidden {
			assert.Contains(t, rec.Body.String(), `"code":"CSRF_INVALID"`)
		}
	}
}
