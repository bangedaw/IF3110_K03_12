package schemadiff

import (
	"testing"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

// helpers — tabel di skema "public" kalau tidak dinyatakan lain.

func col(name string, typ string) schema.Column {
	return schema.Column{Name: name, DataType: typ}
}

func tbl(name string, cols ...schema.Column) schema.Table {
	return schema.Table{Name: name, Kind: schema.TableKindTable, Columns: cols}
}

func mdl(tables ...schema.Table) *schema.Model {
	return &schema.Model{Schemas: []schema.Schema{{Name: "public", Tables: tables}}}
}

func kinds(cs []Change) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Kind)
	}
	return out
}

func assertKinds(t *testing.T, got []Change, want ...string) {
	t.Helper()
	g := kinds(got)
	if len(g) != len(want) {
		t.Fatalf("diff = %v, want kinds %v", g, want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("diff[%d] = %s, want %s (full: %v)", i, g[i], want[i], g)
		}
	}
}

func TestDiff_IdenticalIsEmpty(t *testing.T) {
	a := tbl("books", col("id", "bigint"), col("title", "text"))
	b := tbl("books", col("id", "bigint"), col("title", "text"))
	// RowEstimate sengaja dibuat berbeda: estimasi baris bukan perubahan skema.
	a.RowEstimate, b.RowEstimate = 100, 99999

	if cs := Diff(mdl(a), mdl(b)); len(cs) != 0 {
		t.Fatalf("model identik harus menghasilkan diff kosong, dapat %v", cs)
	}
	if cs := Diff(nil, nil); len(cs) != 0 {
		t.Fatalf("Diff(nil,nil) = %v, want kosong", cs)
	}
}

func TestDiff_AddTable(t *testing.T) {
	got := Diff(mdl(tbl("a", col("id", "bigint"))),
		mdl(tbl("a", col("id", "bigint")), tbl("b", col("id", "bigint"))))

	assertKinds(t, got, KindAddTable)
	if got[0].Name != "b" || got[0].Table != "b" || got[0].Schema != "public" {
		t.Fatalf("add_table identity salah: %+v", got[0])
	}
	if got[0].Destructive {
		t.Fatal("add_table bukan destruktif")
	}
	if got[0].TableDef == nil || len(got[0].TableDef.Columns) != 1 {
		t.Fatalf("add_table harus membawa definisi tabel: %+v", got[0].TableDef)
	}
}

func TestDiff_DropTable(t *testing.T) {
	got := Diff(mdl(tbl("a", col("id", "bigint")), tbl("b", col("id", "bigint"))),
		mdl(tbl("a", col("id", "bigint"))))

	assertKinds(t, got, KindDropTable)
	if got[0].Name != "b" || !got[0].Destructive {
		t.Fatalf("drop_table harus destruktif: %+v", got[0])
	}
	// Tabel yang dihapus tidak boleh ikut memuntahkan drop kolom/konstrainnya sendiri.
	if len(got) != 1 {
		t.Fatalf("drop_table tidak boleh diikuti perubahan lain: %v", kinds(got))
	}
}

func TestDiff_TableKindChanged(t *testing.T) {
	partitioned := tbl("books", col("id", "bigint"))
	partitioned.Kind = "partitioned"

	got := Diff(mdl(tbl("books", col("id", "bigint"))), mdl(partitioned))
	assertKinds(t, got, KindDropTable, KindAddTable)
	if !got[0].Destructive {
		t.Fatal("perubahan jenis tabel diawali drop destruktif")
	}
}

func TestDiff_ColumnLifecycle(t *testing.T) {
	before := tbl("books", col("id", "bigint"), col("title", "text"))
	after := tbl("books", col("id", "bigint"), col("isbn", "text"))

	got := Diff(mdl(before), mdl(after))
	assertKinds(t, got, KindDropColumn, KindAddColumn)
	if got[0].Name != "title" || !got[0].Destructive {
		t.Fatalf("drop_column harus destruktif: %+v", got[0])
	}
	if got[1].Name != "isbn" || got[1].Destructive {
		t.Fatalf("add_column tidak destruktif: %+v", got[1])
	}
	if got[1].Column == nil || got[1].Column.DataType != "text" {
		t.Fatalf("add_column harus membawa definisi kolom: %+v", got[1].Column)
	}
}

func TestDiff_AlterColumnType(t *testing.T) {
	got := Diff(mdl(tbl("books", col("price", "numeric"))),
		mdl(tbl("books", col("price", "integer"))))

	assertKinds(t, got, KindAlterColumn)
	c := got[0]
	if c.Name != "price" || c.Destructive {
		t.Fatalf("ganti tipe kolom bukan destruktif menurut spesifikasi: %+v", c)
	}
	if c.Column == nil || c.Column.DataType != "integer" {
		t.Fatalf("alter_column.wajib membawa nilai baru: %+v", c.Column)
	}
	if c.Previous == nil || c.Previous.DataType != "numeric" {
		t.Fatalf("alter_column.wajib membawa nilai lama: %+v", c.Previous)
	}
}

func TestDiff_NullableTightenedIsDestructive(t *testing.T) {
	loose := schema.Column{Name: "title", DataType: "text", Nullable: true}
	tight := col("title", "text")

	got := Diff(mdl(tbl("books", loose)), mdl(tbl("books", tight)))
	assertKinds(t, got, KindAlterColumn)
	if !got[0].Destructive {
		t.Fatal("nullable true->false wajib ditandai destruktif")
	}

	// longgarkan nullable: bukan destruktif
	got = Diff(mdl(tbl("books", tight)), mdl(tbl("books", loose)))
	assertKinds(t, got, KindAlterColumn)
	if got[0].Destructive {
		t.Fatal("nullable false->true bukan destruktif")
	}
}

func TestDiff_ColumnDefaultChanged(t *testing.T) {
	before := schema.Column{Name: "stock", DataType: "integer", Default: "0"}
	after := schema.Column{Name: "stock", DataType: "integer", Default: "42"}

	got := Diff(mdl(tbl("books", before)), mdl(tbl("books", after)))
	assertKinds(t, got, KindAlterColumn)
	if got[0].Destructive {
		t.Fatal("ganti default bukan destruktif")
	}

	// default dihapus -> tetap alter
	got = Diff(mdl(tbl("books", before)), mdl(tbl("books", col("stock", "integer"))))
	assertKinds(t, got, KindAlterColumn)
	if got[0].Previous.Default != "0" || got[0].Column.Default != "" {
		t.Fatalf("previous/current default harus terisi: %+v", got[0])
	}
}

func TestDiff_RawTypeOnlyIsNotAChange(t *testing.T) {
	// RawType hanya untuk tampilan; diff memakai DataType yang sudah dinormalisasi.
	before := schema.Column{Name: "id", DataType: "bigint", RawType: "int8"}
	after := schema.Column{Name: "id", DataType: "bigint", RawType: "bigint"}

	if cs := Diff(mdl(tbl("books", before)), mdl(tbl("books", after))); len(cs) != 0 {
		t.Fatalf("rawtype-only tidak boleh jadi perubahan: %v", kinds(cs))
	}
}

func TestDiff_ConstraintChanged(t *testing.T) {
	pk := schema.Constraint{Name: "books_pkey", Type: schema.ConstraintPrimaryKey, Definition: "PRIMARY KEY (id)"}
	changed := schema.Constraint{Name: "books_pkey", Type: schema.ConstraintPrimaryKey, Definition: "PRIMARY KEY (id, title)"}

	// Model lama tanpa konstrain, model baru dengan konstrain -> add saja.
	got := Diff(mdl(tbl("books", col("id", "bigint"))),
		mdl(withConstraint(tbl("books", col("id", "bigint"), col("title", "text")), changed)))
	assertKinds(t, got, KindAddColumn, KindAddConstraint)

	// Definisi berubah -> drop + add (nama sama, isi beda).
	got = Diff(mdl(withConstraint(tbl("books", col("id", "bigint")), pk)),
		mdl(withConstraint(tbl("books", col("id", "bigint"), col("title", "text")), changed)))
	assertKinds(t, got, KindAddColumn, KindDropConstraint, KindAddConstraint)
	if got[1].Name != "books_pkey" || got[1].Constraint == nil || got[1].Constraint.Definition != pk.Definition {
		t.Fatalf("drop constraint harus membawa definisi lama: %+v", got[1])
	}
}

func TestDiff_ConstraintDropped(t *testing.T) {
	fk := schema.Constraint{
		Name: "books_author_fk", Type: schema.ConstraintForeignKey,
		Definition: "FOREIGN KEY (author_id) REFERENCES authors(id)",
		RefSchema:  "public", RefTable: "authors", OnDelete: "CASCADE",
	}
	got := Diff(mdl(withConstraint(tbl("books", col("author_id", "bigint")), fk)),
		mdl(tbl("books", col("author_id", "bigint"))))

	assertKinds(t, got, KindDropConstraint)
	if got[0].Name != "books_author_fk" || got[0].Constraint == nil {
		t.Fatalf("drop_constraint harus membawa definisi lama: %+v", got[0])
	}
}

func TestDiff_IndexLifecycle(t *testing.T) {
	idx := schema.Index{Name: "books_title_idx", Definition: "CREATE INDEX books_title_idx ON books USING btree (title)"}
	changed := schema.Index{Name: "books_title_idx", Definition: "CREATE UNIQUE INDEX books_title_idx ON books USING btree (title)", Unique: true}

	// tambah index
	got := Diff(mdl(tbl("books", col("title", "text"))),
		mdl(withIndex(tbl("books", col("title", "text")), idx)))
	assertKinds(t, got, KindAddIndex)
	if got[0].Index == nil || got[0].Name != "books_title_idx" {
		t.Fatalf("add_index harus membawa definisi: %+v", got[0])
	}

	// ubah index -> drop + add
	got = Diff(mdl(withIndex(tbl("books", col("title", "text")), idx)),
		mdl(withIndex(tbl("books", col("title", "text")), changed)))
	assertKinds(t, got, KindDropIndex, KindAddIndex)
	if got[0].Index.Unique || !got[1].Index.Unique {
		t.Fatalf("drop harus membawa definisi lama, add definisi baru: %+v / %+v", got[0].Index, got[1].Index)
	}

	// hapus index
	got = Diff(mdl(withIndex(tbl("books", col("title", "text")), idx)),
		mdl(tbl("books", col("title", "text"))))
	assertKinds(t, got, KindDropIndex)
	if got[0].Destructive {
		t.Fatal("drop index bukan destruktif menurut spesifikasi")
	}
}

func TestDiff_SchemaAddedAndRemoved(t *testing.T) {
	withArchive := &schema.Model{Schemas: []schema.Schema{
		{Name: "public", Tables: []schema.Table{tbl("books", col("id", "bigint"))}},
		{Name: "archive", Tables: []schema.Table{tbl("old_books", col("id", "bigint"))}},
	}}
	publicOnly := mdl(tbl("books", col("id", "bigint")))

	got := Diff(publicOnly, withArchive)
	assertKinds(t, got, KindAddTable)
	if got[0].Schema != "archive" || got[0].Name != "old_books" {
		t.Fatalf("tabel di skema baru harus terdeteksi: %+v", got[0])
	}

	got = Diff(withArchive, publicOnly)
	assertKinds(t, got, KindDropTable)
	if got[0].Schema != "archive" || !got[0].Destructive {
		t.Fatalf("tabel di skema yang hilang harus drop destruktif: %+v", got[0])
	}
}

func TestDiff_EmissionOrder(t *testing.T) {
	// Spesifikasi §8.10: urutan global drop table -> drop column -> alter column ->
	// add column -> drop constraint -> add constraint -> drop index -> add index.
	// Urutannya bukan urutan eksekusi SQL; generator (Task 13) yang menata
	// pernyataan agar aman dijalankan per tabel.
	oldIdx := schema.Index{Name: "i_old", Definition: "CREATE INDEX i_old ON books USING btree (a)"}
	newIdx := schema.Index{Name: "i_new", Definition: "CREATE INDEX i_new ON books USING btree (b)"}
	oldC := schema.Constraint{Name: "c_old", Type: schema.ConstraintCheck, Definition: "CHECK (a > 0)"}
	newC := schema.Constraint{Name: "c_new", Type: schema.ConstraintCheck, Definition: "CHECK (b > 0)"}

	before := withIndex(withConstraint(tbl("books", col("a", "integer"), col("gone", "text")), oldC), oldIdx)
	after := withIndex(withConstraint(tbl("books", col("a", "bigint"), col("baru", "text")), newC), newIdx)

	got := Diff(mdl(before), mdl(after))
	assertKinds(t, got,
		KindDropColumn, KindAlterColumn, KindAddColumn,
		KindDropConstraint, KindAddConstraint,
		KindDropIndex, KindAddIndex)
}

func TestDiff_NilModels(t *testing.T) {
	// baseline -> new: semua tambahan; new -> baseline: semua drop destruktif.
	b := mdl(tbl("books", col("id", "bigint")))
	if got := Diff(nil, b); len(got) != 1 || got[0].Kind != KindAddTable {
		t.Fatalf("Diff(nil, model) = %v, want satu add_table", kinds(got))
	}
	if got := Diff(b, nil); len(got) != 1 || got[0].Kind != KindDropTable || !got[0].Destructive {
		t.Fatalf("Diff(model, nil) = %v, want satu drop_table destruktif", kinds(got))
	}
}

// withConstraint / withIndex menempelkan objek ke tabel salinan agar helper
// test tetap ringkas tanpa merusak tabel lain.
func withConstraint(t schema.Table, cs ...schema.Constraint) schema.Table {
	t.Constraints = append(t.Constraints, cs...)
	return t
}

func withIndex(t schema.Table, is ...schema.Index) schema.Table {
	t.Indexes = append(t.Indexes, is...)
	return t
}
