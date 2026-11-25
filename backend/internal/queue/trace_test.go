package queue_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/queue"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/sqltrace"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/telemetry"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) lines(t *testing.T) []map[string]any {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.buf.String()), "\n") {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m), line)
		out = append(out, m)
	}
	return out
}

// sqlMigrator menjalankan hook sqltrace yang sama dengan koneksi pgx ke database target, tanpa database
// sungguhan, sehingga span dan log SQL di worker ikut teruji.
type sqlMigrator struct {
	*dbms.FakeMigrator
	tracer sqltrace.Tracer
}

func (m sqlMigrator) Apply(ctx context.Context, in dbms.ApplyInput) (*dbms.ApplyResult, error) {
	qctx := m.tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: in.SQL})
	m.tracer.TraceQueryEnd(qctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("CREATE TABLE")})
	return m.FakeMigrator.Apply(ctx, in)
}

func TestOneTraceSpansFromAPIRequestToSQLInWorker(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	logs := &lockedBuffer{}
	logger := slog.New(telemetry.NewHandler(slog.NewJSONHandler(logs, nil)))

	// 1. Request API mem-publish job. Trace context disimpan dengan cara yang sama seperti Postgres.Publish.
	var stored []byte
	api := httpx.NewRouter(logger)
	api.Post("/api/databases/{databaseId}/migrations/{migrationId}/apply", func(w http.ResponseWriter, r *http.Request) {
		var err error
		stored, err = queue.EncodeTrace(r.Context())
		require.NoError(t, err)
		w.WriteHeader(http.StatusAccepted)
	})
	api.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/databases/1/migrations/7/apply", nil))

	// 2. Worker (proses lain) mengambil job dan menjalankan SQL di database target.
	job := &queue.Job{ID: 10, RequestID: 20, DatabaseID: 1, ActorID: 3, Attempt: 1, MaxAttempts: 3}
	require.NoError(t, queue.WithTrace(job, stored))
	w := &queue.Worker{
		Jobs:      newMemJobs(job),
		Locks:     newMemLocks(),
		Lifecycle: &queue.FakeLifecycle{Execution: queue.Execution{MigrationID: 7, DatabaseID: 1, SQL: "CREATE TABLE t ()", Version: 1}},
		Targets:   targets{m: sqlMigrator{&dbms.FakeMigrator{}, sqltrace.Tracer{Target: "target", Logger: logger}}},
		Logger:    logger,
		LockTTL:   time.Minute,
	}
	_, err := w.Next(context.Background())
	require.NoError(t, err)

	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range recorder.Ended() {
		spans[s.Name()] = s
	}
	request := spans["POST /api/databases/{databaseId}/migrations/{migrationId}/apply"]
	jobSpan, lock, query := spans["queue.job"], spans["queue.lock"], spans["sql.query"]
	require.NotNil(t, request)
	require.NotNil(t, jobSpan)
	require.NotNil(t, lock)
	require.NotNil(t, query)

	traceID := request.SpanContext().TraceID()
	for _, s := range []sdktrace.ReadOnlySpan{jobSpan, lock, query} {
		assert.Equal(t, traceID, s.SpanContext().TraceID(), "%s ada di trace yang sama dengan request API", s.Name())
	}
	assert.Equal(t, request.SpanContext().SpanID(), jobSpan.Parent().SpanID(), "job adalah anak span request API")
	assert.True(t, jobSpan.Parent().IsRemote(), "induknya datang dari proses lain lewat tabel jobs")
	assert.Equal(t, jobSpan.SpanContext().SpanID(), lock.Parent().SpanID())
	assert.Equal(t, jobSpan.SpanContext().SpanID(), query.Parent().SpanID())

	seen := map[string]map[string]any{}
	for _, line := range logs.lines(t) {
		seen[line["msg"].(string)] = line
		assert.Equal(t, traceID.String(), line["trace_id"], "log %q membawa trace_id yang sama", line["msg"])
	}
	for _, msg := range []string{"http", "job.start", "lock.acquire", "sql.query.start", "sql.query.end", "job.end"} {
		assert.Contains(t, seen, msg)
	}
	assert.Equal(t, query.SpanContext().SpanID().String(), seen["sql.query.end"]["span_id"])
	assert.EqualValues(t, 10, seen["job.end"]["job_id"])
	assert.EqualValues(t, 1, seen["job.end"]["run_id"])
	assert.EqualValues(t, 7, seen["job.end"]["migration_id"])
	assert.EqualValues(t, 20, seen["job.end"]["migration_request_id"])
	assert.IsType(t, "", seen["http"]["request_id"], "request_id hanya dipakai untuk ID request HTTP")
	assert.NotContains(t, seen["job.end"], "request_id", "ID migration request tidak memakai key request_id milik HTTP")
}
