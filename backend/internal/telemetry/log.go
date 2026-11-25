package telemetry

import (
	"context"
	"log/slog"

	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/trace"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/sqltrace"
)

// Handler membungkus handler slog lain dan menambahkan ID korelasi dari context ke setiap log:
// trace_id dan span_id dari span aktif, request_id HTTP, actor yang login, dan atribut job atau run yang
// dipasang lewat sqltrace.With. Atribut yang sudah ditulis pemanggil tidak ditimpa dan tidak digandakan.
type Handler struct{ slog.Handler }

func NewHandler(h slog.Handler) Handler { return Handler{h} }

func (h Handler) Handle(ctx context.Context, r slog.Record) error {
	r = r.Clone()
	present := make(map[string]bool, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		present[a.Key] = true
		return true
	})
	add := func(a slog.Attr) {
		if !present[a.Key] {
			present[a.Key] = true
			r.AddAttrs(a)
		}
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		add(slog.String("trace_id", sc.TraceID().String()))
		add(slog.String("span_id", sc.SpanID().String()))
	}
	if id := middleware.GetReqID(ctx); id != "" {
		add(slog.String("request_id", id))
	}
	if p, ok := auth.PrincipalFrom(ctx); ok {
		add(slog.Int64("actor", p.UserID))
	}
	for _, a := range sqltrace.Attrs(ctx) {
		add(a)
	}
	return h.Handler.Handle(ctx, r)
}

func (h Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return Handler{h.Handler.WithAttrs(attrs)}
}

func (h Handler) WithGroup(name string) slog.Handler { return Handler{h.Handler.WithGroup(name)} }
