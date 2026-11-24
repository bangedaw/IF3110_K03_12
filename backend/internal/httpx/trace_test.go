package httpx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/telemetry"
)

func tracedRouter(t *testing.T) (http.Handler, *tracetest.SpanRecorder, *bytes.Buffer) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	logs := &bytes.Buffer{}
	r := httpx.NewRouter(slog.New(telemetry.NewHandler(slog.NewJSONHandler(logs, nil))))
	r.Route("/api", func(api chi.Router) {
		api.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		api.Get("/databases/{databaseId}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		api.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
		api.Post("/auth/login", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusUnauthorized)
		})
	})
	r.Get("/*", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return r, recorder, logs
}

func serve(h http.Handler, path string, header http.Header) {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
}

func attrs(span sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	out := map[attribute.Key]attribute.Value{}
	for _, a := range span.Attributes() {
		out[a.Key] = a.Value
	}
	return out
}

func TestTraceNamesSpanAfterRoutePattern(t *testing.T) {
	h, recorder, _ := tracedRouter(t)
	serve(h, "/api/databases/42?q=rahasia", nil)

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	span := spans[0]
	assert.Equal(t, "GET /api/databases/{databaseId}", span.Name())
	assert.Equal(t, trace.SpanKindServer, span.SpanKind())
	a := attrs(span)
	assert.Equal(t, "/api/databases/{databaseId}", a["http.route"].AsString())
	assert.Equal(t, "/api/databases/42", a["url.path"].AsString())
	assert.EqualValues(t, 200, a["http.response.status_code"].AsInt64())
	assert.NotEmpty(t, a["dbmp.request_id"].AsString())
	for _, v := range a {
		assert.NotContains(t, v.String(), "rahasia", "query string tidak masuk span")
	}
}

func TestTraceContinuesIncomingTraceparent(t *testing.T) {
	h, recorder, _ := tracedRouter(t)
	serve(h, "/api/databases/1", http.Header{"Traceparent": {"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}})

	span := recorder.Ended()[0]
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", span.SpanContext().TraceID().String())
	assert.Equal(t, "00f067aa0ba902b7", span.Parent().SpanID().String())
}

func TestTraceMarksServerErrors(t *testing.T) {
	h, recorder, _ := tracedRouter(t)
	serve(h, "/api/boom", nil)

	span := recorder.Ended()[0]
	assert.Equal(t, codes.Error, span.Status().Code, "panic yang dipulihkan menjadi 500 dan span ditandai error")
	assert.EqualValues(t, 500, attrs(span)["http.response.status_code"].AsInt64())
}

func TestTraceSkipsHealthCheckAndStaticFiles(t *testing.T) {
	h, recorder, _ := tracedRouter(t)
	serve(h, "/api/healthz", nil)
	serve(h, "/assets/app.js", nil)

	assert.Empty(t, recorder.Ended())
}

func TestRequestLogCarriesTraceIDAndRoute(t *testing.T) {
	h, recorder, logs := tracedRouter(t)
	serve(h, "/api/databases/7", nil)

	var line map[string]any
	require.NoError(t, json.Unmarshal(logs.Bytes(), &line), logs.String())
	span := recorder.Ended()[0]
	assert.Equal(t, span.SpanContext().TraceID().String(), line["trace_id"])
	assert.Equal(t, span.SpanContext().SpanID().String(), line["span_id"])
	assert.Equal(t, "/api/databases/{databaseId}", line["route"])
}

func TestTraceAndRequestLogNeverRecordCredentials(t *testing.T) {
	h, recorder, logs := tracedRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login?next=LEAK-query",
		strings.NewReader(`{"username":"alice","password":"LEAK-password"}`))
	req.AddCookie(&http.Cookie{Name: "dbmp_session", Value: "LEAK-cookie"})
	req.Header.Set("X-CSRF-Token", "LEAK-csrf")
	req.Header.Set("Authorization", "Bearer LEAK-bearer")
	h.ServeHTTP(httptest.NewRecorder(), req)

	// Span diekspor dengan exporter yang sama seperti di production, jadi yang diperiksa adalah teks yang
	// benar-benar keluar: nama, atribut, event, dan status.
	var exported bytes.Buffer
	exp, err := stdouttrace.New(stdouttrace.WithWriter(&exported))
	require.NoError(t, err)
	require.NoError(t, exp.ExportSpans(context.Background(), recorder.Ended()))
	require.Contains(t, exported.String(), "POST /api/auth/login")
	require.Contains(t, logs.String(), "/api/auth/login")

	for _, secret := range []string{"LEAK-query", "LEAK-password", "LEAK-cookie", "LEAK-csrf", "LEAK-bearer"} {
		assert.NotContains(t, exported.String(), secret, "span tidak boleh memuat %q", secret)
		assert.NotContains(t, logs.String(), secret, "log tidak boleh memuat %q", secret)
	}
}
