import { useState } from "react"
import CodeMirror from "@uiw/react-codemirror"
import { MySQL, PostgreSQL, sql } from "@codemirror/lang-sql"
import { Button } from "@/components/ui/button"
import { Field, inputClass } from "@/components/ui/form"
import { ApiError, errorMessage } from "@/features/shared/client"
import type { DbKind, Migration, MigrationDefinition } from "@/features/shared/types"

const editorBox = "max-w-full overflow-x-auto rounded-lg border"

export function MigrationForm({
  kind,
  initial,
  submitLabel,
  onSubmit,
  onCancel,
}: {
  kind: DbKind
  initial?: Migration
  submitLabel: string
  onSubmit: (body: MigrationDefinition) => Promise<unknown>
  onCancel?: () => void
}) {
  const [title, setTitle] = useState(initial?.title ?? "")
  const [description, setDescription] = useState(initial?.description ?? "")
  const [upSql, setUpSql] = useState(initial?.upSql ?? "")
  const [downSql, setDownSql] = useState(initial?.downSql ?? "")
  const [irreversible, setIrreversible] = useState(initial?.irreversible ?? false)
  const [reason, setReason] = useState(initial?.irreversibleReason ?? "")
  const [batch, setBatch] = useState(Boolean(initial?.batch))
  const [schema, setSchema] = useState(initial?.batch?.schema ?? "public")
  const [table, setTable] = useState(initial?.batch?.table ?? "")
  const [column, setColumn] = useState(initial?.batch?.column ?? "id")
  const [size, setSize] = useState(String(initial?.batch?.size ?? 1000))
  const [fields, setFields] = useState<Record<string, string>>({})
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)

  const extensions = [sql({ dialect: kind === "mysql" ? MySQL : PostgreSQL })]

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError("")
    setFields({})
    try {
      await onSubmit({
        title: title.trim(),
        description,
        upSql,
        downSql: irreversible ? "" : downSql,
        irreversible,
        irreversibleReason: irreversible ? reason : "",
        batch: batch ? { schema: schema.trim(), table: table.trim(), column: column.trim(), size: Number(size) } : undefined,
      })
    } catch (err) {
      if (err instanceof ApiError) setFields(err.fields)
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={(e) => void submit(e)} className="space-y-4">
      <Field label="Judul" htmlFor="m-title" error={fields.title}>
        <input id="m-title" className={inputClass} value={title} onChange={(e) => setTitle(e.target.value)} />
      </Field>
      <Field label="Deskripsi" htmlFor="m-desc" error={fields.description}>
        <input id="m-desc" className={inputClass} value={description} onChange={(e) => setDescription(e.target.value)} />
      </Field>

      <Field label="SQL up" htmlFor="m-up" error={fields.upSql}>
        <div className={editorBox}>
          <CodeMirror id="m-up" value={upSql} height="220px" extensions={extensions} onChange={setUpSql} aria-label="SQL up" />
        </div>
      </Field>

      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={irreversible} onChange={(e) => setIrreversible(e.target.checked)} />
        Irreversible (tidak punya SQL down)
      </label>
      {irreversible ? (
        <Field label="Alasan irreversible" htmlFor="m-reason" error={fields.irreversibleReason}>
          <input id="m-reason" className={inputClass} value={reason} onChange={(e) => setReason(e.target.value)} />
        </Field>
      ) : (
        <Field label="SQL down" htmlFor="m-down" error={fields.downSql}>
          <div className={editorBox}>
            <CodeMirror id="m-down" value={downSql} height="160px" extensions={extensions} onChange={setDownSql} aria-label="SQL down" />
          </div>
        </Field>
      )}

      {kind === "postgres" && (
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={batch} onChange={(e) => setBatch(e.target.checked)} />
          Jalankan per batch
        </label>
      )}
      {batch && (
        <fieldset className="space-y-2">
          <p className="text-xs text-muted-foreground">
            SQL up dan down masing-masing satu statement yang memakai <code>$1</code> (cursor, eksklusif) dan <code>$2</code> (batas, inklusif), misalnya{" "}
            <code className="break-all">UPDATE t SET … WHERE id &gt; $1 AND id &lt;= $2</code>.
          </p>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Schema" htmlFor="m-schema">
              <input id="m-schema" className={inputClass} value={schema} onChange={(e) => setSchema(e.target.value)} />
            </Field>
            <Field label="Tabel" htmlFor="m-table">
              <input id="m-table" className={inputClass} value={table} onChange={(e) => setTable(e.target.value)} />
            </Field>
            <Field label="Kolom batch" htmlFor="m-col">
              <input id="m-col" className={inputClass} value={column} onChange={(e) => setColumn(e.target.value)} />
            </Field>
            <Field label="Ukuran batch" htmlFor="m-size">
              <input id="m-size" type="number" min={1} className={inputClass} value={size} onChange={(e) => setSize(e.target.value)} />
            </Field>
          </div>
          {fields.batch && (
            <p role="alert" className="text-xs text-destructive">
              {fields.batch}
            </p>
          )}
        </fieldset>
      )}

      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <Button type="submit" disabled={busy || !title.trim() || !upSql.trim()}>
          {busy ? "Menyimpan…" : submitLabel}
        </Button>
        {onCancel && (
          <Button type="button" variant="outline" onClick={onCancel}>
            Batal
          </Button>
        )}
      </div>
    </form>
  )
}
