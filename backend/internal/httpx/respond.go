package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
)

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type Problem struct {
	Type   string            `json:"type"`
	Title  string            `json:"title"`
	Status int               `json:"status"`
	Detail string            `json:"detail,omitempty"`
	Code   apperr.Code       `json:"code"`
	Errors map[string]string `json:"errors,omitempty"`
}

var statusByCode = map[apperr.Code]int{
	apperr.CodeUnauthenticated: http.StatusUnauthorized,
	apperr.CodeSessionExpired:  http.StatusUnauthorized,
	apperr.CodeForbidden:       http.StatusForbidden,
	apperr.CodeCSRFInvalid:     http.StatusForbidden,
	apperr.CodeNotFound:        http.StatusNotFound,
	apperr.CodeConflict:        http.StatusConflict,
	apperr.CodeValidation:      http.StatusUnprocessableEntity,
	apperr.CodeMigrationFailed: http.StatusUnprocessableEntity,
	apperr.CodeDBUnreachable:   http.StatusServiceUnavailable,
}

func Error(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		slog.ErrorContext(r.Context(), "unhandled error", "err", err, "path", r.URL.Path)
		writeProblem(w, Problem{Status: http.StatusInternalServerError, Code: "INTERNAL", Detail: "terjadi kesalahan internal"})
		return
	}
	if ae.Err != nil {
		slog.WarnContext(r.Context(), "request error", "code", ae.Code, "err", ae.Err, "path", r.URL.Path)
	}
	status, ok := statusByCode[ae.Code]
	if !ok {
		status = http.StatusInternalServerError
	}
	writeProblem(w, Problem{Status: status, Code: ae.Code, Detail: ae.Detail, Errors: ae.Fields})
}

func writeProblem(w http.ResponseWriter, p Problem) {
	p.Type = "about:blank"
	p.Title = http.StatusText(p.Status)
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

const maxBodyBytes = 1 << 20

func Decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return apperr.New(apperr.CodeValidation, "body JSON tidak valid: "+err.Error())
	}
	return nil
}
