package sqltrace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/sqltrace"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

type entry map[string]any

func fixture(t *testing.T, logArgs bool) (*pgx.Conn, *bytes.Buffer, *tracetest.SpanRecorder) {
	t.Helper()
	pool := testutil.NewPostgresTarget(t)
	t.Cleanup(pool.Close)

	recorder := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	buf := &bytes.Buffer{}
	cfg := pool.Config().ConnConfig.Copy()
	cfg.Tracer = sqltrace.Tracer{Target: "target", LogArgs: logArgs, Logger: slog.New(slog.NewJSONHandler(buf, nil))}
	conn, err := pgx.ConnectConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn, buf, recorder
}

func entries(t *testing.T, buf *bytes.Buffer) []entry {
	t.Helper()
	var out []entry
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var e entry
		require.NoError(t, json.Unmarshal([]byte(line), &e), line)
		out = append(out, e)
	}
	return out
}

func messages(es []entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e["msg"].(string)
	}
	return out
}

func requestContext() context.Context {
	ctx := auth.WithPrincipal(context.Background(), authz.Principal{UserID: 42})
	ctx = context.WithValue(ctx, middleware.RequestIDKey, "req-7")
	return sqltrace.With(ctx, slog.Int64("run_id", 9), slog.Int("batch", 3))
}

func TestTracerLogsStartResultAndCommitSeparately(t *testing.T) {
	conn, buf, recorder := fixture(t, true)
	ctx := requestContext()

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `CREATE TABLE app.traced (id int, label text)`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO app.traced SELECT g, 'x' FROM generate_series(1, $1::int) g`, 3)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	es := entries(t, buf)
	assert.Equal(t, []string{
		"sql.begin.start", "sql.begin.end",
		"sql.query.start", "sql.query.end",
		"sql.query.start", "sql.query.end",
		"sql.commit.start", "sql.commit.end",
	}, messages(es))

	insertStart, insertEnd := es[4], es[5]
	assert.Equal(t, "INFO", insertEnd["level"])
	assert.Equal(t, "sql.query", insertEnd["event"])
	assert.Equal(t, "ok", insertEnd["status"])
	assert.EqualValues(t, 3, insertEnd["rows"])
	assert.Contains(t, insertEnd, "duration_ms")
	assert.Contains(t, insertEnd["query"], "INSERT INTO app.traced")
	assert.Equal(t, []any{"3"}, insertStart["params"])
	for _, e := range es {
		assert.EqualValues(t, 42, e["actor"], e["msg"])
		assert.Equal(t, "req-7", e["request_id"], e["msg"])
		assert.EqualValues(t, 9, e["run_id"], e["msg"])
		assert.EqualValues(t, 3, e["batch"], e["msg"])
		assert.Equal(t, "target", e["target"], e["msg"])
		assert.NotEmpty(t, e["database"], e["msg"])
		assert.Contains(t, e, "time")
	}

	spans := recorder.Ended()
	require.Len(t, spans, 4)
	assert.Equal(t, "sql.begin", spans[0].Name())
	assert.Equal(t, "sql.commit", spans[3].Name())
	attrs := map[string]any{}
	for _, kv := range spans[2].Attributes() {
		attrs[string(kv.Key)] = kv.Value.AsInterface()
	}
	assert.Equal(t, "postgresql", attrs["db.system.name"])
	assert.EqualValues(t, 3, attrs["db.response.returned_rows"])
	assert.Contains(t, attrs["db.query.text"], "INSERT INTO app.traced")
}

func TestTracerRecordsFailureWithSQLStateAndRollback(t *testing.T) {
	conn, buf, recorder := fixture(t, true)
	ctx := context.Background()

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT * FROM app.tidak_ada`)
	require.Error(t, err)
	require.NoError(t, tx.Rollback(ctx))

	es := entries(t, buf)
	assert.Equal(t, []string{"sql.begin.start", "sql.begin.end", "sql.query.start", "sql.query.end", "sql.rollback.start", "sql.rollback.end"}, messages(es))
	failed := es[3]
	assert.Equal(t, "ERROR", failed["level"])
	assert.Equal(t, "error", failed["status"])
	assert.Equal(t, "42P01", failed["sqlstate"])
	assert.Contains(t, failed["error"], "tidak_ada")
	assert.NotContains(t, failed, "actor", "tanpa principal, actor tidak dicatat")

	spans := recorder.Ended()
	require.Len(t, spans, 3)
	assert.Equal(t, codes.Error, spans[1].Status().Code)
	require.NotEmpty(t, spans[1].Events(), "error harus tercatat di span")
	assert.Equal(t, "sql.rollback", spans[2].Name())
}

func TestTracerMasksSecretsAndCanHideParams(t *testing.T) {
	conn, buf, _ := fixture(t, false)
	ctx := context.Background()

	_, err := conn.Exec(ctx, `CREATE ROLE traced_role LOGIN PASSWORD 'sangat-rahasia'`)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `SELECT $1::text`, "token-sesi-rahasia")
	require.NoError(t, err)

	out := buf.String()
	assert.NotContains(t, out, "sangat-rahasia")
	assert.NotContains(t, out, "token-sesi-rahasia")
	assert.Contains(t, out, `PASSWORD '***'`)
	es := entries(t, buf)
	assert.EqualValues(t, 1, es[2]["param_count"])
	assert.NotContains(t, es[2], "params")
}

func TestMask(t *testing.T) {
	cases := map[string]string{
		`ALTER ROLE app PASSWORD 'it''s secret'`:                  `ALTER ROLE app PASSWORD '***'`,
		`create user x with encrypted password 'abc'`:             `create user x with encrypted password '***'`,
		`SELECT dblink_connect('host=db user=u password=abc123')`: `SELECT dblink_connect('host=db user=u password=***')`,
		`SELECT dblink_connect('password = ''x y''')`:             `SELECT dblink_connect('password = ***')`,
		"  SELECT 1  ": "SELECT 1",
	}
	for in, want := range cases {
		assert.Equal(t, want, sqltrace.Mask(in), in)
	}
	long := sqltrace.Mask(strings.Repeat("é", 3000))
	assert.LessOrEqual(t, len(long), 4000+len("…"))
	assert.True(t, strings.HasSuffix(long, "…"))
}

func TestEvent(t *testing.T) {
	cases := map[string]string{
		"begin":                   "sql.begin",
		"BEGIN ISOLATION LEVEL x": "sql.begin",
		"start transaction":       "sql.begin",
		"commit":                  "sql.commit",
		"rollback":                "sql.rollback",
		"  ROLLBACK;":             "sql.rollback",
		"UPDATE t SET x = 1":      "sql.query",
		"":                        "sql.query",
		"SELECT 'commit' AS word": "sql.query",
	}
	for in, want := range cases {
		assert.Equal(t, want, sqltrace.Event(in), in)
	}
}
