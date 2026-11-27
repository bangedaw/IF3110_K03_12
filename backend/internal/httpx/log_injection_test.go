package httpx_test

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
	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
)

func TestLogsCannotBeForgedThroughRequestValues(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	prev := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })

	forged := "\n{\"level\":\"ERROR\",\"msg\":\"palsu\"}\n"
	r := httpx.NewRouter(logger)
	r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
		httpx.Error(w, req, errors.New("gagal"+forged))
	})
	r.Post("/*", func(w http.ResponseWriter, req *http.Request) {
		httpx.Error(w, req, apperr.Wrap(apperr.CodeConflict, "konflik", errors.New("sebab"+forged)))
	})
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/x%0A%7B%22msg%22%3A%22palsu%22%7D", nil))
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 4, "dua log error dan dua log http, tidak ada baris tambahan")
	for _, line := range lines {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), line)
		assert.NotEqual(t, "palsu", entry["msg"])
	}
}
