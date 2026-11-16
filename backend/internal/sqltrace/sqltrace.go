package sqltrace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
)

const (
	scope       = "github.com/bangedaw/IF3110_K03_12/backend/internal/sqltrace"
	maxQueryLen = 4000
	maxArgLen   = 200
)

var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?i)(\bpassword\s+)'(?:[^']|'')*'`), "${1}'***'"},
	{regexp.MustCompile(`(?i)(\bpassword\s*=\s*)(''[^']*''|'(?:[^']|'')*'|[^\s']+)`), "${1}***"},
}

type Tracer struct {
	Target  string
	LogArgs bool
	Logger  *slog.Logger
}

type attrsKey struct{}

type queryKey struct{}

type query struct {
	start time.Time
	event string
	text  string
	span  trace.Span
}

func With(ctx context.Context, attrs ...slog.Attr) context.Context {
	prev, _ := ctx.Value(attrsKey{}).([]slog.Attr)
	merged := make([]slog.Attr, 0, len(prev)+len(attrs))
	merged = append(merged, prev...)
	merged = append(merged, attrs...)
	return context.WithValue(ctx, attrsKey{}, merged)
}

func Mask(sql string) string {
	for _, p := range secretPatterns {
		sql = p.re.ReplaceAllString(sql, p.repl)
	}
	return truncate(strings.TrimSpace(sql), maxQueryLen)
}

func Event(sql string) string {
	fields := strings.Fields(strings.ToLower(sql))
	if len(fields) == 0 {
		return "sql.query"
	}
	switch strings.TrimSuffix(fields[0], ";") {
	case "begin", "start":
		return "sql.begin"
	case "commit", "end":
		return "sql.commit"
	case "rollback", "abort":
		return "sql.rollback"
	}
	return "sql.query"
}

func (t Tracer) logger() *slog.Logger {
	if t.Logger != nil {
		return t.Logger
	}
	return slog.Default()
}

func (t Tracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	event := Event(data.SQL)
	text := Mask(data.SQL)
	database, server := endpoint(conn)
	ctx, span := otel.Tracer(scope).Start(ctx, event,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system.name", "postgresql"),
			attribute.String("db.namespace", database),
			attribute.String("server.address", server),
			attribute.String("db.query.text", text),
			attribute.String("dbmp.target", t.Target),
		))
	attrs := t.base(ctx, event, database, server)
	attrs = append(attrs, slog.String("query", text))
	if t.LogArgs {
		attrs = append(attrs, slog.Any("params", formatArgs(data.Args)))
	} else {
		attrs = append(attrs, slog.Int("param_count", len(data.Args)))
	}
	t.logger().LogAttrs(ctx, slog.LevelInfo, event+".start", attrs...)
	return context.WithValue(ctx, queryKey{}, &query{start: time.Now(), event: event, text: text, span: span})
}

func (t Tracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	q, ok := ctx.Value(queryKey{}).(*query)
	if !ok {
		return
	}
	elapsed := time.Since(q.start)
	rows := data.CommandTag.RowsAffected()
	database, server := endpoint(conn)
	attrs := t.base(ctx, q.event, database, server)
	attrs = append(attrs,
		slog.String("query", q.text),
		slog.Float64("duration_ms", float64(elapsed.Microseconds())/1000),
		slog.Int64("rows", rows),
	)
	q.span.SetAttributes(attribute.Int64("db.response.returned_rows", rows))
	level, status := slog.LevelInfo, "ok"
	if data.Err != nil {
		level, status = slog.LevelError, "error"
		attrs = append(attrs, slog.String("error", errorMessage(data.Err)))
		var pe *pgconn.PgError
		if errors.As(data.Err, &pe) {
			attrs = append(attrs, slog.String("sqlstate", pe.Code))
			q.span.SetAttributes(attribute.String("db.response.status_code", pe.Code))
		}
		q.span.RecordError(data.Err)
		q.span.SetStatus(codes.Error, errorMessage(data.Err))
	}
	attrs = append(attrs, slog.String("status", status))
	t.logger().LogAttrs(ctx, level, q.event+".end", attrs...)
	q.span.End()
}

func (t Tracer) base(ctx context.Context, event, database, server string) []slog.Attr {
	attrs := []slog.Attr{
		slog.String("event", event),
		slog.String("target", t.Target),
		slog.String("database", database),
		slog.String("server", server),
	}
	if p, ok := auth.PrincipalFrom(ctx); ok {
		attrs = append(attrs, slog.Int64("actor", p.UserID))
	}
	if id := middleware.GetReqID(ctx); id != "" {
		attrs = append(attrs, slog.String("request_id", id))
	}
	if extra, ok := ctx.Value(attrsKey{}).([]slog.Attr); ok {
		attrs = append(attrs, extra...)
	}
	return attrs
}

func endpoint(conn *pgx.Conn) (string, string) {
	if conn == nil {
		return "", ""
	}
	cfg := conn.Config()
	return cfg.Database, cfg.Host + ":" + strconv.Itoa(int(cfg.Port))
}

func errorMessage(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Message
	}
	return err.Error()
}

func formatArgs(args []any) []string {
	out := make([]string, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case nil:
			out[i] = "NULL"
		case []byte:
			out[i] = fmt.Sprintf("<%d bytes>", len(v))
		case string:
			out[i] = truncate(v, maxArgLen)
		default:
			out[i] = truncate(fmt.Sprint(v), maxArgLen)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
