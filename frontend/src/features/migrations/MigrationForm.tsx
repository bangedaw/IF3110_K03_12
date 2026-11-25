import { useState } from "react"
import CodeMirror from "@uiw/react-codemirror"
import { PostgreSQL, sql } from "@codemirror/lang-sql"
import { Button } from "@/components/ui/button"
import { Field, inputClass } from "@/components/ui/form"
import { ApiError, errorMessage } from "@/features/shared/client"
import type { Migration, MigrationDefinition } from "@/features/shared/types"

const editorBox = "max-w-full overflow-x-auto rounded-lg border"

export function MigrationForm({
  initial,
  submitLabel,
  onSubmit,
}: {
  initial?: Migration
  submitLabel: string
  onSubmit: (body: MigrationDefinition) => Promise<unknown>
}) {
  const [title, setTitle] = useState(initial?.title ?? "")
  const [description, setDescription] = useState(initial?.description ?? "")
  const [upSql, setUpSql] = useState(initial?.upSql ?? "")
  const [downSql, setDownSql] = useState(initial?.downSql ?? "")
  const [irreversible, setIrreversible] = useState(initial?.irreversible ?? false)
  const [reason, setReason] = useState(initial?.irreversibleReason ?? "")
  const [batch, setBatch] = useState(Boolean(initial?.batch))
  const [column, setColumn] = useState(initial?.batch?.column ?? "")
  const [size, setSize] = useState(String(initial?.batch?.size ?? 1000))
  const [fields, setFields] = useState<Record<string, string>>({})
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)

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
        batch: batch ? { column: column.trim(), size: Number(size) } : undefined,
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
          <CodeMirror id="m-up" value={upSql} height="220px" extensions={[sql({ dialect: PostgreSQL })]} onChange={setUpSql} aria-label="SQL up" />
        </div>
      </Field>

      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={irreversible} onChange={(e) => setIrreversible(e.target.checked)} />
        Irreversible (tidak punya SQL down)
      </label>
      {irreversible ? (
        <Field label="Alasan irreversible" htmlFor="m-reason" error={fields.definition}>
          <input id="m-reason" className={inputClass} value={reason} onChange={(e) => setReason(e.target.value)} />
        </Field>
      ) : (
        <Field label="SQL down" htmlFor="m-down" error={fields.downSql ?? fields.definition}>
          <div className={editorBox}>
            <CodeMirror id="m-down" value={downSql} height="160px" extensions={[sql({ dialect: PostgreSQL })]} onChange={setDownSql} aria-label="SQL down" />
          </div>
        </Field>
      )}

      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={batch} onChange={(e) => setBatch(e.target.checked)} />
        Jalankan per batch
      </label>
      {batch && (
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Kolom batch" htmlFor="m-col">
            <input id="m-col" className={inputClass} value={column} onChange={(e) => setColumn(e.target.value)} />
          </Field>
          <Field label="Ukuran batch" htmlFor="m-size">
            <input id="m-size" type="number" min={1} className={inputClass} value={size} onChange={(e) => setSize(e.target.value)} />
          </Field>
        </div>
      )}

      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <Button type="submit" disabled={busy || !title.trim() || !upSql.trim()}>
        {busy ? "Menyimpan…" : submitLabel}
      </Button>
    </form>
  )
}
