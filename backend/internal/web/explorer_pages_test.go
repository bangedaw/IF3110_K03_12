package web_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func booksPath(e *env, query string) string {
	path := dbPath(e.liveID, "/schemas/app/tables/books")
	if query != "" {
		path += "?" + query
	}
	return path
}

func TestDatabaseListShowsOnlyAccessibleActiveDatabases(t *testing.T) {
	e := newEnv(t, false)

	rec := e.get("/databases", viewerID)

	require.Equal(t, http.StatusOK, rec.Code)
	html := body(rec)
	assert.Contains(t, html, "<strong>live</strong>")
	assert.Contains(t, html, "<strong>dead</strong>")
	assert.NotContains(t, html, "<strong>inactive</strong>")
	assert.NotContains(t, html, "<strong>nogrant</strong>")
	assert.NotContains(t, html, "<strong>staging</strong>")
	assert.Contains(t, html, "View Only")
	assert.Contains(t, html, fmt.Sprintf(`href="/databases/%d"`, e.liveID))
}

func TestDatabaseListShowsEditLevelAndEmptyState(t *testing.T) {
	e := newEnv(t, false)

	assert.Contains(t, body(e.get("/databases", editorID)), `badge badge-success">View &#43; Edit`)

	empty := e.get("/databases", nobodyID)
	require.Equal(t, http.StatusOK, empty.Code)
	assert.Contains(t, body(empty), "belum memiliki akses")

	admin := e.get("/databases", adminID)
	require.Equal(t, http.StatusOK, admin.Code)
	assert.Contains(t, body(admin), "belum memiliki akses", "Platform Admin tidak otomatis punya akses database")
}

func TestDatabasePageListsSchemasAndTables(t *testing.T) {
	e := newEnv(t, false)

	rec := e.get(dbPath(e.liveID, ""), viewerID)

	require.Equal(t, http.StatusOK, rec.Code, body(rec))
	html := body(rec)
	assert.Contains(t, html, "<h1>live</h1>")
	assert.Contains(t, html, "PostgreSQL")
	assert.Contains(t, html, "Tabel di schema app")
	for _, table := range []string{"authors", "books", "attachments"} {
		assert.Contains(t, html, fmt.Sprintf(`href="/databases/%d/schemas/app/tables/%s"`, e.liveID, table))
	}
	assert.Contains(t, html, `aria-label="Bagian database"`)
	assert.Contains(t, html, "Schema diff")
}

func TestDatabasePageDeniedAndUnreachableAndUnknown(t *testing.T) {
	e := newEnv(t, false)

	denied := e.get(dbPath(e.nograntID, ""), viewerID)
	assert.Equal(t, http.StatusForbidden, denied.Code)
	assert.Contains(t, body(denied), "Akses ditolak")

	dead := e.get(dbPath(e.deadID, ""), viewerID)
	assert.Equal(t, http.StatusServiceUnavailable, dead.Code)
	assert.Contains(t, body(dead), "Database tidak terjangkau")
	assert.NotContains(t, body(dead), "apa-saja")

	assert.Equal(t, http.StatusNotFound, e.get(dbPath(e.liveID, "?schema=tidak_ada"), viewerID).Code)
	assert.Equal(t, http.StatusForbidden, e.get(dbPath(99999, ""), viewerID).Code)
	assert.Equal(t, http.StatusNotFound, e.get("/databases/abc", viewerID).Code)
}

func TestTablePageColumnsTab(t *testing.T) {
	e := newEnv(t, false)

	rec := e.get(booksPath(e, ""), viewerID)

	require.Equal(t, http.StatusOK, rec.Code, body(rec))
	html := body(rec)
	assert.Contains(t, html, "app.books")
	for _, col := range []string{"id", "author_id", "title", "price", "published_on", "tags"} {
		assert.Contains(t, html, "<strong>"+col+"</strong>")
	}
	assert.Contains(t, html, "NOT NULL")
	assert.Contains(t, html, `aria-current="page">Columns</a>`)
}

func TestTablePageConstraintsAndIndexesTabs(t *testing.T) {
	e := newEnv(t, false)

	constraints := body(e.get(booksPath(e, "tab=constraints"), viewerID))
	assert.Contains(t, constraints, "PRIMARY KEY")
	assert.Contains(t, constraints, "FOREIGN KEY")
	assert.Contains(t, constraints, "app.authors(id)")
	assert.Contains(t, constraints, "ON DELETE CASCADE")
	assert.Contains(t, constraints, "CHECK")

	indexes := body(e.get(booksPath(e, "tab=indexes"), viewerID))
	assert.Contains(t, indexes, "books_author_idx")
	assert.Contains(t, indexes, "books_title_key")
	assert.Contains(t, indexes, "primary")
	assert.Contains(t, indexes, "unique")
}

func TestTablePageUnknownTabFallsBackToColumns(t *testing.T) {
	e := newEnv(t, false)

	rec := e.get(booksPath(e, "tab=bukan-tab"), viewerID)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, body(rec), `aria-current="page">Columns</a>`)
}

func TestTablePageUnknownTableAndDeniedAndDead(t *testing.T) {
	e := newEnv(t, false)

	assert.Equal(t, http.StatusNotFound, e.get(dbPath(e.liveID, "/schemas/app/tables/tidak_ada"), viewerID).Code)
	assert.Equal(t, http.StatusForbidden, e.get(booksPath(e, ""), nobodyID).Code)
	assert.Equal(t, http.StatusServiceUnavailable, e.get(dbPath(e.deadID, "/schemas/app/tables/books"), viewerID).Code)
}

func TestTableDataFirstPageAndPagination(t *testing.T) {
	e := newEnv(t, false)

	first := body(e.get(booksPath(e, "tab=data&limit=5"), viewerID))
	assert.Contains(t, first, "Dasar PostgreSQL")
	assert.Contains(t, first, "Baris 1–5")
	assert.Contains(t, first, "≈ 12 baris")
	assert.Contains(t, first, "Concurrency di Go")
	assert.NotContains(t, first, "Data Modeling")
	assert.Contains(t, first, `rel="next"`)
	assert.NotContains(t, first, `rel="prev"`)
	nextHref := regexp.MustCompile(`href="([^"]+)" rel="next"`).FindStringSubmatch(first)
	require.Len(t, nextHref, 2)
	assert.Contains(t, nextHref[1], "offset=5")
	assert.Contains(t, nextHref[1], "limit=5")

	second := body(e.get(booksPath(e, "tab=data&limit=5&offset=5"), viewerID))
	assert.Contains(t, second, "Data Modeling")
	assert.NotContains(t, second, "Dasar PostgreSQL")
	assert.Contains(t, second, "Baris 6–10")
	assert.Contains(t, second, `rel="prev"`)
	assert.Contains(t, second, `rel="next"`)

	last := body(e.get(booksPath(e, "tab=data&limit=5&offset=10"), viewerID))
	assert.Contains(t, last, "Gudang Efektif")
	assert.Contains(t, last, "Inventori Modern")
	assert.Contains(t, last, "Baris 11–12")
	assert.NotContains(t, last, `rel="next"`)
	assert.Contains(t, last, `rel="prev"`)
}

func TestTableDataClampsLimitAndOffset(t *testing.T) {
	e := newEnv(t, false)

	assert.Equal(t, http.StatusOK, e.get(booksPath(e, "tab=data&limit=99999&offset=-5"), viewerID).Code)
	assert.Equal(t, http.StatusOK, e.get(booksPath(e, "tab=data&limit=abc&offset=xyz"), viewerID).Code)
	html := body(e.get(booksPath(e, "tab=data&limit=0"), viewerID))
	assert.Contains(t, html, "Baris 1–12")
}

func TestTableDataSortLinksToggleDirection(t *testing.T) {
	e := newEnv(t, false)

	none := body(e.get(booksPath(e, "tab=data&limit=5"), viewerID))
	assert.Contains(t, none, "orderBy=title&amp;orderDir=asc")

	asc := body(e.get(booksPath(e, "tab=data&limit=5&orderBy=title&orderDir=asc"), viewerID))
	assert.Contains(t, asc, `aria-sort="ascending"`)
	assert.Contains(t, asc, "orderBy=title&amp;orderDir=desc")
	assert.Less(t, indexOf(asc, "Analitik Bisnis"), indexOf(asc, "Belajar Go"))

	desc := body(e.get(booksPath(e, "tab=data&limit=5&orderBy=title&orderDir=desc"), viewerID))
	assert.Contains(t, desc, `aria-sort="descending"`)
	assert.Less(t, indexOf(desc, "Query Lanjutan"), indexOf(desc, "Normalisasi Ringkas"))
}

func indexOf(haystack, needle string) int {
	re := regexp.MustCompile(regexp.QuoteMeta(needle))
	loc := re.FindStringIndex(haystack)
	if loc == nil {
		return 1 << 30
	}
	return loc[0]
}

func TestTableDataRejectsUnknownOrderColumn(t *testing.T) {
	e := newEnv(t, false)

	for _, q := range []string{
		"tab=data&orderBy=kolom_tidak_ada",
		"tab=data&orderBy=" + url.QueryEscape("id; DROP TABLE app.books"),
		"tab=data&orderBy=" + url.QueryEscape(`title" FROM app.authors --`),
	} {
		rec := e.get(booksPath(e, q), viewerID)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, q)
	}
	assert.Equal(t, http.StatusOK, e.get(booksPath(e, "tab=data"), viewerID).Code)
}

func TestTableDataEscapesCellValuesAndMarksNull(t *testing.T) {
	e := newEnv(t, false)
	_, err := e.target.Exec(context.Background(),
		`INSERT INTO app.books (author_id, title, published_on) VALUES (1, '<img src=x onerror=alert(1)>', NULL)`)
	require.NoError(t, err)

	html := body(e.get(booksPath(e, "tab=data&limit=200&orderBy=id&orderDir=desc"), viewerID))

	assert.NotContains(t, html, "<img src=x")
	assert.Contains(t, html, "&lt;img src=x onerror=alert(1)&gt;")
	assert.Contains(t, html, `<span class="null">NULL</span>`)
}

func TestTableDataTruncatedCellsAreMarked(t *testing.T) {
	e := newEnv(t, false)

	html := body(e.get(dbPath(e.liveID, "/schemas/app/tables/attachments?tab=data"), viewerID))

	assert.Contains(t, html, "…")
	assert.NotContains(t, html, "catatan yang cukup panjang untuk memotong tampilan data viewer. catatan yang cukup panjang untuk memotong tampilan data viewer. catatan yang cukup panjang untuk memotong tampilan data viewer. catatan yang cukup panjang untuk memotong tampilan data viewer. catatan")
}

func TestPostToExplorerPagesNotAllowed(t *testing.T) {
	e := newEnv(t, false)

	rec := e.post("/databases", viewerID, url.Values{})

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
