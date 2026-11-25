package httpx

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const tracerScope = "github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"

// Trace membuat satu span server untuk setiap request /api. Span menjadi induk semua span di bawahnya
// (query SQL, publish job), dan trace context-nya ikut tersimpan di job sehingga worker menyambung trace
// yang sama. Hanya path yang dicatat, tanpa query string, karena query bisa berisi data sensitif.
func Trace(next http.Handler) http.Handler {
	tracer := otel.Tracer(tracerScope)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := tracer.Start(ctx, r.Method,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
			))
		defer span.End()
		if id := middleware.GetReqID(ctx); id != "" {
			span.SetAttributes(attribute.String("dbmp.request_id", id))
		}

		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r.WithContext(ctx))

		// Pola route baru diketahui setelah chi selesai mencocokkan route. Nama span memakai pola, bukan
		// path asli, supaya /api/databases/1 dan /api/databases/2 masuk kelompok yang sama.
		if route := RoutePattern(r); route != "" {
			span.SetName(r.Method + " " + route)
			span.SetAttributes(attribute.String("http.route", route))
		}
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		span.SetAttributes(attribute.Int("http.response.status_code", status))
		if status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(status))
		}
	})
}

// RoutePattern mengembalikan pola route chi yang cocok, misalnya /api/databases/{databaseId}.
func RoutePattern(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		return rctx.RoutePattern()
	}
	return ""
}
