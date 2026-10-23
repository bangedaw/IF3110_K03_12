package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandler(t *testing.T) {
	h, ok := Handler(fstest.MapFS{
		"index.html":           {Data: []byte("<div id=root></div>")},
		"assets/app-1.js":      {Data: []byte("console.log(1)")},
		"mockServiceWorker.js": {Data: []byte("sw")},
	})
	require.True(t, ok)

	cases := []struct {
		path, body, cache string
	}{
		{"/", "<div id=root></div>", "no-cache"},
		{"/databases/1/schemas/app/tables/books", "<div id=root></div>", "no-cache"},
		{"/admin/users", "<div id=root></div>", "no-cache"},
		{"/assets/app-1.js", "console.log(1)", "public, max-age=31536000, immutable"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		assert.Equal(t, http.StatusOK, rec.Code, tc.path)
		assert.Equal(t, tc.body, rec.Body.String(), tc.path)
		assert.Equal(t, tc.cache, rec.Header().Get("Cache-Control"), tc.path)
	}
}

func TestHandlerDisabledWithoutBuild(t *testing.T) {
	_, ok := Handler(fstest.MapFS{".gitkeep": {}})
	assert.False(t, ok)
}

func TestHandlerRejectsPathTraversal(t *testing.T) {
	h, ok := Handler(fstest.MapFS{"index.html": {Data: []byte("index")}})
	require.True(t, ok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/../../etc/passwd", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
