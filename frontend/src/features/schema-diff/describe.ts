import type { SchemaChange } from "@/features/shared/types"

const LABELS: Record<SchemaChange["kind"], string> = {
  add_table: "Tambah tabel",
  drop_table: "Hapus tabel",
  add_column: "Tambah kolom",
  drop_column: "Hapus kolom",
  alter_column: "Ubah kolom",
  add_constraint: "Tambah constraint",
  drop_constraint: "Hapus constraint",
  add_index: "Tambah index",
  drop_index: "Hapus index",
}

export function describeChange(change: SchemaChange): string {
  const table = change.schema ? `${change.schema}.${change.table}` : change.table
  const label = LABELS[change.kind] ?? change.kind
  if (change.kind === "add_table" || change.kind === "drop_table") return `${label} ${table}`
  return `${label} ${change.name} pada ${table}`
}
