package spa

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const index = "<div id=root></div>"

func build() fstest.MapFS {
	return fstest.MapFS{
		"index.html":      {Data: []byte(index)},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
		"favicon.svg":     {Data: []byte("<svg/>")},
	}
}

func serve(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHandlerServesIndexForClientRoutes(t *testing.T) {
	h, ok := Handler(build())
	require.True(t, ok)

	for _, p := range []string{"/", "/login", "/databases/1/schemas/app/tables/books", "/admin/users", "/tidak-ada"} {
		rec := serve(t, h, http.MethodGet, p)
		assert.Equal(t, http.StatusOK, rec.Code, p)
		assert.Equal(t, index, rec.Body.String(), p)
		assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"), p)
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"), p)
		assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'", p)
	}
}

func TestHandlerServesStaticFiles(t *testing.T) {
	h, ok := Handler(build())
	require.True(t, ok)

	rec := serve(t, h, http.MethodGet, "/assets/app-1.js")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "console.log(1)", rec.Body.String())
	assert.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))

	rec = serve(t, h, http.MethodGet, "/favicon.svg")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Cache-Control"))
}

func TestHandlerRedirectsIndexFileToRoot(t *testing.T) {
	h, ok := Handler(build())
	require.True(t, ok)

	rec := serve(t, h, http.MethodGet, "/index.html")
	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "./", rec.Header().Get("Location"))
}

func TestHandlerMissingAssetIsNotFound(t *testing.T) {
	h, ok := Handler(build())
	require.True(t, ok)

	rec := serve(t, h, http.MethodGet, "/assets/lama-0.js")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), index)
}

func TestHandlerKeepsPathsInsideBuild(t *testing.T) {
	h, ok := Handler(build())
	require.True(t, ok)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.Path = "/../../etc/passwd"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.NotContains(t, rec.Body.String(), "root:")
}

func TestHandlerDisabledWithoutBuild(t *testing.T) {
	_, ok := Handler(fstest.MapFS{".gitkeep": {}})
	assert.False(t, ok)
	_, ok = Handler(nil)
	assert.False(t, ok)
}

func TestDistIsEmbedded(t *testing.T) {
	assert.NotNil(t, Dist())
}
