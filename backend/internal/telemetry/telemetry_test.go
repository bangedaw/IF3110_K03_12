package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/sqltrace"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/telemetry"
)

func logLine(t *testing.T, ctx context.Context, log func(*slog.Logger)) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	log(slog.New(telemetry.NewHandler(slog.NewJSONHandler(&buf, nil))))
	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out), buf.String())
	return out
}

func spanContext(t *testing.T) (context.Context, trace.SpanContext) {
	t.Helper()
	tid, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	require.NoError(t, err)
	sid, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	require.NoError(t, err)
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled})
	return trace.ContextWithSpanContext(context.Background(), sc), sc
}

func TestHandlerAddsTraceAndCorrelationIDs(t *testing.T) {
	ctx, sc := spanContext(t)
	ctx = context.WithValue(ctx, middleware.RequestIDKey, "req-1")
	ctx = auth.WithPrincipal(ctx, authz.Principal{UserID: 7})
	ctx = sqltrace.With(ctx, slog.Int64("job_id", 10), slog.Int64("run_id", 5))

	out := logLine(t, ctx, func(l *slog.Logger) { l.InfoContext(ctx, "job.start", "status", "running") })

	assert.Equal(t, sc.TraceID().String(), out["trace_id"])
	assert.Equal(t, sc.SpanID().String(), out["span_id"])
	assert.Equal(t, "req-1", out["request_id"])
	assert.EqualValues(t, 7, out["actor"])
	assert.EqualValues(t, 10, out["job_id"])
	assert.EqualValues(t, 5, out["run_id"])
	assert.Equal(t, "running", out["status"])
}

func TestHandlerKeepsAttributesWrittenByCaller(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.RequestIDKey, "req-1")
	var buf bytes.Buffer
	slog.New(telemetry.NewHandler(slog.NewJSONHandler(&buf, nil))).InfoContext(ctx, "http", "request_id", "req-1")

	assert.Equal(t, 1, strings.Count(buf.String(), `"request_id"`), "tidak digandakan: %s", buf.String())
}

func TestHandlerOmitsTraceWithoutActiveSpan(t *testing.T) {
	out := logLine(t, context.Background(), func(l *slog.Logger) { l.Info("listening") })

	assert.NotContains(t, out, "trace_id")
	assert.NotContains(t, out, "span_id")
}

func TestHandlerKeepsAddingIDsAfterWith(t *testing.T) {
	ctx, sc := spanContext(t)
	out := logLine(t, ctx, func(l *slog.Logger) { l.With("component", "worker").InfoContext(ctx, "job.start") })

	assert.Equal(t, "worker", out["component"])
	assert.Equal(t, sc.TraceID().String(), out["trace_id"])
}

func restoreGlobals(t *testing.T) {
	t.Helper()
	tp, prop := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(tp)
		otel.SetTextMapPropagator(prop)
	})
}

func TestSetupExportsSpansToConsole(t *testing.T) {
	restoreGlobals(t)
	var buf bytes.Buffer
	shutdown, err := telemetry.Setup("dbmp-test", telemetry.ExporterConsole, &buf)
	require.NoError(t, err)

	ctx, span := otel.Tracer("test").Start(context.Background(), "queue.job")
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	span.End()
	require.NoError(t, shutdown(context.Background()))

	var out struct {
		Name        string
		SpanContext struct{ TraceID string }
		Resource    []struct {
			Key   string
			Value struct{ Value any }
		}
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out), buf.String())
	assert.Equal(t, "queue.job", out.Name)
	assert.Equal(t, span.SpanContext().TraceID().String(), out.SpanContext.TraceID)
	assert.Contains(t, out.Resource, struct {
		Key   string
		Value struct{ Value any }
	}{"service.name", struct{ Value any }{"dbmp-test"}})
	assert.Contains(t, carrier.Get("traceparent"), span.SpanContext().TraceID().String(), "propagator global memakai W3C traceparent")
}

func TestSetupWithoutExporterStillPropagates(t *testing.T) {
	restoreGlobals(t)
	shutdown, err := telemetry.Setup("dbmp-test", telemetry.ExporterNone, nil)
	require.NoError(t, err)
	require.NoError(t, shutdown(context.Background()))

	ctx, _ := spanContext(t)
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	assert.NotEmpty(t, carrier.Get("traceparent"))
}

func TestSetupRejectsUnknownExporter(t *testing.T) {
	restoreGlobals(t)
	_, err := telemetry.Setup("dbmp-test", "jaeger", nil)
	assert.ErrorContains(t, err, "OTEL_TRACES_EXPORTER")
}
