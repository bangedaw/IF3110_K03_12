package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/explorer"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

const (
	defaultPageSize = 50
	maxPageSize     = dbms.MaxRows
)

var pageSizes = []int{25, 50, 100, 200}

var tableTabs = []struct{ ID, Label string }{
	{"columns", "Columns"},
	{"constraints", "Constraints"},
	{"indexes", "Indexes"},
	{"data", "Data"},
}

type tabLink struct {
	Label   string
	URL     string
	Current bool
}

type dbHeader struct {
	DB   explorer.DatabaseDetail
	Tabs []tabLink
}

type sidebarTable struct {
	Name    string
	URL     string
	Current bool
	IsView  bool
}

type sidebarSchema struct {
	Name   string
	URL    string
	Active bool
	Tables []sidebarTable
}

type databasesView struct {
	Items []explorer.Database
}

type databaseHomeView struct {
	Header  dbHeader
	Schemas []sidebarSchema
	Active  string
	Tables  []tableRow
}

type tableRow struct {
	Name     string
	URL      string
	Kind     string
	Estimate int64
}

type columnRow struct {
	Ordinal  int
	Name     string
	Type     string
	Nullable bool
	Default  string
	Flags    []string
}

type constraintRow struct {
	Name   string
	Type   string
	Detail string
}

type indexRow struct {
	Name    string
	Columns string
	Flags   []string
}

type columnHead struct {
	Name string
	URL  string
	Dir  string
}

type sizeLink struct {
	Size    int
	URL     string
	Current bool
}

type rowsView struct {
	Columns  []columnHead
	Rows     [][]dbms.Cell
	From     int
	To       int
	Estimate string
	PrevURL  string
	NextURL  string
	Sizes    []sizeLink
}

type tableView struct {
	Header      dbHeader
	Schemas     []sidebarSchema
	SchemaName  string
	Name        string
	Kind        string
	Estimate    int64
	Tabs        []tabLink
	Tab         string
	Columns     []columnRow
	Constraints []constraintRow
	Indexes     []indexRow
	Rows        *rowsView
}

func (s *Server) mountExplorer(r chi.Router) {
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/databases", http.StatusSeeOther)
	})
	r.Get("/databases", s.databasesPage)
	r.Get("/databases/{databaseId}", s.databasePage)
	r.Get("/databases/{databaseId}/schemas/{schema}/tables/{table}", s.tablePage)
}

func databasePath(id int64) string { return "/databases/" + strconv.FormatInt(id, 10) }

func tablePath(id int64, schemaName, table string) string {
	return databasePath(id) + "/schemas/" + url.PathEscape(schemaName) + "/tables/" + url.PathEscape(table)
}

func (s *Server) header(db *explorer.DatabaseDetail, active string) dbHeader {
	tabs := []tabLink{
		{Label: "Explorer", URL: databasePath(db.ID), Current: active == "explorer"},
		{Label: "Schema diff", URL: databasePath(db.ID) + "/diff", Current: active == "diff"},
	}
	for _, t := range s.dbTabs {
		tabs = append(tabs, tabLink{Label: t.Label, URL: databasePath(db.ID) + t.Suffix, Current: active == t.Suffix})
	}
	return dbHeader{DB: *db, Tabs: tabs}
}

func (s *Server) databasesPage(w http.ResponseWriter, r *http.Request) {
	p, err := s.principal(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	items, err := s.explorer.Databases(r.Context(), p)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Render(w, r, http.StatusOK, "explorer/databases", databasesView{Items: items})
}

func (s *Server) sidebar(ctx context.Context, p authz.Principal, dbID int64, schemas []string, active, currentTable string) ([]sidebarSchema, []schema.TableSummary, error) {
	out := make([]sidebarSchema, 0, len(schemas))
	var activeTables []schema.TableSummary
	for _, name := range schemas {
		entry := sidebarSchema{Name: name, URL: databasePath(dbID) + "?schema=" + url.QueryEscape(name), Active: name == active}
		if entry.Active {
			tables, err := s.explorer.Tables(ctx, p, dbID, name)
			if err != nil {
				return nil, nil, err
			}
			activeTables = tables
			for _, t := range tables {
				entry.Tables = append(entry.Tables, sidebarTable{
					Name:    t.Name,
					URL:     tablePath(dbID, name, t.Name),
					Current: t.Name == currentTable,
					IsView:  t.Kind == schema.TableKindView,
				})
			}
		}
		out = append(out, entry)
	}
	return out, activeTables, nil
}

func (s *Server) databasePage(w http.ResponseWriter, r *http.Request) {
	p, err := s.principal(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "databaseId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	db, err := s.explorer.Database(r.Context(), p, id)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	schemas, err := s.explorer.Schemas(r.Context(), p, id)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	active := r.URL.Query().Get("schema")
	if active == "" && len(schemas) > 0 {
		active = schemas[0]
	}
	if active != "" && !contains(schemas, active) {
		s.Fail(w, r, apperr.ErrNotFound)
		return
	}
	side, tables, err := s.sidebar(r.Context(), p, id, schemas, active, "")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	rows := make([]tableRow, 0, len(tables))
	for _, t := range tables {
		rows = append(rows, tableRow{Name: t.Name, URL: tablePath(id, active, t.Name), Kind: t.Kind, Estimate: t.RowEstimate})
	}
	s.Render(w, r, http.StatusOK, "explorer/database", databaseHomeView{
		Header:  s.header(db, "explorer"),
		Schemas: side,
		Active:  active,
		Tables:  rows,
	})
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func (s *Server) tablePage(w http.ResponseWriter, r *http.Request) {
	p, err := s.principal(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id, err := idParam(r, "databaseId")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	schemaName, tableName := urlParam(r, "schema"), urlParam(r, "table")

	db, err := s.explorer.Database(r.Context(), p, id)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	table, err := s.explorer.Table(r.Context(), p, id, schemaName, tableName)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	schemas, err := s.explorer.Schemas(r.Context(), p, id)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	side, _, err := s.sidebar(r.Context(), p, id, schemas, schemaName, tableName)
	if err != nil {
		s.Fail(w, r, err)
		return
	}

	tab := r.URL.Query().Get("tab")
	if !validTab(tab) {
		tab = "columns"
	}
	view := tableView{
		Header:      s.header(db, "explorer"),
		Schemas:     side,
		SchemaName:  table.Schema,
		Name:        table.Name,
		Kind:        table.Kind,
		Estimate:    table.RowEstimate,
		Tab:         tab,
		Tabs:        tableTabLinks(id, table.Schema, table.Name, tab),
		Columns:     columnRows(table.Columns),
		Constraints: constraintRows(table.Constraints),
		Indexes:     indexRows(table.Indexes),
	}
	if tab == "data" {
		rows, err := s.rowsView(r, p, id, table)
		if err != nil {
			s.Fail(w, r, err)
			return
		}
		view.Rows = rows
	}
	s.Render(w, r, http.StatusOK, "explorer/table", view)
}

func validTab(tab string) bool {
	for _, t := range tableTabs {
		if t.ID == tab {
			return true
		}
	}
	return false
}

func tableTabLinks(id int64, schemaName, table, current string) []tabLink {
	out := make([]tabLink, 0, len(tableTabs))
	for _, t := range tableTabs {
		out = append(out, tabLink{Label: t.Label, URL: tablePath(id, schemaName, table) + "?tab=" + t.ID, Current: t.ID == current})
	}
	return out
}

func columnRows(cols []schema.Column) []columnRow {
	out := make([]columnRow, 0, len(cols))
	for _, c := range cols {
		typ := c.RawType
		if typ == "" {
			typ = c.DataType
		}
		var flags []string
		if c.IsIdentity {
			flags = append(flags, "identity")
		}
		if c.IsGenerated {
			flags = append(flags, "generated")
		}
		out = append(out, columnRow{Ordinal: c.Ordinal, Name: c.Name, Type: typ, Nullable: c.Nullable, Default: c.Default, Flags: flags})
	}
	return out
}

func constraintRows(items []schema.Constraint) []constraintRow {
	labels := map[string]string{
		schema.ConstraintPrimaryKey: "PRIMARY KEY",
		schema.ConstraintForeignKey: "FOREIGN KEY",
		schema.ConstraintUnique:     "UNIQUE",
		schema.ConstraintCheck:      "CHECK",
	}
	out := make([]constraintRow, 0, len(items))
	for _, c := range items {
		label := labels[c.Type]
		if label == "" {
			label = c.Type
		}
		var detail string
		switch c.Type {
		case schema.ConstraintForeignKey:
			ref := c.RefTable
			if c.RefSchema != "" {
				ref = c.RefSchema + "." + ref
			}
			detail = fmt.Sprintf("(%s) → %s(%s)", strings.Join(c.Columns, ", "), ref, strings.Join(c.RefColumns, ", "))
			if c.OnDelete != "" {
				detail += " · ON DELETE " + c.OnDelete
			}
			if c.OnUpdate != "" {
				detail += " · ON UPDATE " + c.OnUpdate
			}
		case schema.ConstraintCheck:
			detail = c.Definition
		default:
			detail = "(" + strings.Join(c.Columns, ", ") + ")"
		}
		out = append(out, constraintRow{Name: c.Name, Type: label, Detail: detail})
	}
	return out
}

func indexRows(items []schema.Index) []indexRow {
	out := make([]indexRow, 0, len(items))
	for _, i := range items {
		var flags []string
		if i.Primary {
			flags = append(flags, "primary")
		}
		if i.Unique {
			flags = append(flags, "unique")
		}
		if i.Method != "" {
			flags = append(flags, i.Method)
		}
		cols := strings.Join(i.Columns, ", ")
		if cols == "" {
			cols = i.Definition
		}
		out = append(out, indexRow{Name: i.Name, Columns: cols, Flags: flags})
	}
	return out
}

type rowsParams struct {
	limit    int
	offset   int
	orderBy  string
	orderDir string
}

func readRowsParams(r *http.Request) rowsParams {
	limit := queryInt(r, "limit", defaultPageSize)
	if limit < 1 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	offset := queryInt(r, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	q := r.URL.Query()
	params := rowsParams{limit: limit, offset: offset, orderBy: q.Get("orderBy"), orderDir: q.Get("orderDir")}
	if params.orderBy == "" {
		params.orderDir = ""
	} else if params.orderDir != "desc" {
		params.orderDir = "asc"
	}
	return params
}

func (p rowsParams) url(base string, mutate func(*rowsParams)) string {
	c := p
	if mutate != nil {
		mutate(&c)
	}
	q := url.Values{"tab": {"data"}, "limit": {strconv.Itoa(c.limit)}, "offset": {strconv.Itoa(c.offset)}}
	if c.orderBy != "" {
		q.Set("orderBy", c.orderBy)
		q.Set("orderDir", c.orderDir)
	}
	return base + "?" + q.Encode()
}

func (s *Server) rowsView(r *http.Request, p authz.Principal, id int64, table *schema.Table) (*rowsView, error) {
	params := readRowsParams(r)
	page, err := s.explorer.Rows(r.Context(), p, id, dbms.RowsQuery{
		Schema:   table.Schema,
		Table:    table.Name,
		Limit:    params.limit,
		Offset:   params.offset,
		OrderBy:  params.orderBy,
		OrderDir: params.orderDir,
	})
	if err != nil {
		return nil, err
	}
	base := tablePath(id, table.Schema, table.Name)
	view := &rowsView{Rows: page.Rows}
	for _, name := range page.Columns {
		head := columnHead{Name: name}
		switch {
		case params.orderBy == name && params.orderDir == "asc":
			head.Dir = "asc"
			head.URL = params.url(base, func(c *rowsParams) { c.offset, c.orderDir = 0, "desc" })
		case params.orderBy == name:
			head.Dir = "desc"
			head.URL = params.url(base, func(c *rowsParams) { c.offset, c.orderBy, c.orderDir = 0, "", "" })
		default:
			head.URL = params.url(base, func(c *rowsParams) { c.offset, c.orderBy, c.orderDir = 0, name, "asc" })
		}
		view.Columns = append(view.Columns, head)
	}
	if len(page.Rows) > 0 {
		view.From = page.Offset + 1
	}
	view.To = page.Offset + len(page.Rows)
	if page.TotalEstimate > 0 {
		view.Estimate = fmt.Sprintf("≈ %d baris", page.TotalEstimate)
	}
	if page.Offset > 0 {
		prev := max(page.Offset-page.Limit, 0)
		view.PrevURL = params.url(base, func(c *rowsParams) { c.offset = prev })
	}
	if len(page.Rows) == page.Limit {
		next := page.Offset + page.Limit
		view.NextURL = params.url(base, func(c *rowsParams) { c.offset = next })
	}
	for _, size := range pageSizes {
		view.Sizes = append(view.Sizes, sizeLink{
			Size:    size,
			Current: size == params.limit,
			URL:     params.url(base, func(c *rowsParams) { c.limit, c.offset = size, 0 }),
		})
	}
	return view, nil
}
