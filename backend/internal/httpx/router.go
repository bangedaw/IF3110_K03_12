package httpx

import (
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
)

func NewRouter(logger *slog.Logger) chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, Logger(logger), Recover(logger))
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		Error(w, r, apperr.New(apperr.CodeNotFound, "endpoint tidak ditemukan"))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, Problem{Status: http.StatusMethodNotAllowed, Code: "METHOD_NOT_ALLOWED"})
	})
	return r
}

func Logger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r)
			logger.InfoContext(r.Context(), "http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", middleware.GetReqID(r.Context()),
			)
		})
	}
}

func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				logger.ErrorContext(r.Context(), "panic", "panic", rec, "stack", string(debug.Stack()),
					"request_id", middleware.GetReqID(r.Context()))
				writeProblem(w, Problem{Status: http.StatusInternalServerError, Code: "INTERNAL", Detail: "terjadi kesalahan internal"})
			}()
			next.ServeHTTP(w, r)
		})
	}
}
