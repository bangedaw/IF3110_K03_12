import { useMemo, useState } from "react"
import { Link, useParams } from "react-router-dom"
import CodeMirror from "@uiw/react-codemirror"
import { MySQL, PostgreSQL, sql } from "@codemirror/lang-sql"
import { Button } from "@/components/ui/button"
import { Badge, Field, inputClass } from "@/components/ui/form"
import { ErrorState, Loading } from "@/components/ErrorState"
import { errorMessage } from "@/features/shared/client"
import type { DiffAgainst } from "@/features/shared/types"
import { useDatabase, useDatabases } from "@/features/explorer/api"
import { useCreateSnapshot, useSaveMigrationDraft, useSchemaDiff, useSnapshots } from "./api"
import { describeChange } from "./describe"

export function SchemaDiffPage() {
  const databaseId = Number(useParams().databaseId)
  const database = useDatabase(databaseId)
  const databases = useDatabases()
  const snapshots = useSnapshots(databaseId)
  const createSnapshot = useCreateSnapshot(databaseId)
  const diff = useSchemaDiff(databaseId)
  const saveDraftMutation = useSaveMigrationDraft(databaseId)

  const [mode, setMode] = useState<DiffAgainst["type"]>("database")
  const [againstId, setAgainstId] = useState("")
  const [script, setScript] = useState("")
  const [title, setTitle] = useState("")
  const [saved, setSaved] = useState<{ id: number } | null>(null)
  const [saveError, setSaveError] = useState("")
  const [saving, setSaving] = useState(false)

  const candidates = useMemo(
    () => (databases.data ?? []).filter((d) => d.id !== databaseId && d.kind === database.data?.kind),
    [databases.data, databaseId, database.data?.kind],
  )

  if (database.isLoading) return <Loading />
  if (database.error) return <ErrorState error={database.error} />
  const db = database.data
  if (!db) return null

  const canEdit = db.level === "edit"
  const result = diff.data
  const destructive = result?.changes.some((c) => c.destructive) ?? false

  async function compare() {
    setSaved(null)
    setSaveError("")
    const id = Number(againstId)
    if (!id) return
    const outcome = await diff.mutateAsync({ type: mode, id }).catch(() => null)
    if (outcome) {
      setScript(outcome.sql)
      setTitle(`Sinkronkan ${db?.name} dengan ${mode === "database" ? candidates.find((c) => c.id === id)?.name : `snapshot #${id}`}`)
    }
  }

  async function saveDraft() {
    setSaving(true)
    setSaveError("")
    try {
      const draft = await saveDraftMutation.mutateAsync({ title: title.trim(), description: "Dihasilkan dari schema diff", sql: script })
      setSaved({ id: draft.id })
    } catch (error) {
      setSaveError(errorMessage(error))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="space-y-5">
      <div>
        <h2 className="text-lg font-semibold">Schema diff</h2>
        <p className="text-sm text-muted-foreground">
          Bandingkan <strong>{db.name}</strong> dengan pembanding, lalu hasilkan SQL yang membuat <strong>{db.name}</strong> menyamai pembanding. SQL hanya kandidat: tinjau dan ubah sebelum disimpan.
        </p>
      </div>

      <fieldset className="space-y-3 rounded-xl border p-4">
        <legend className="px-1 text-sm font-medium">Pembanding</legend>
        <div className="flex gap-4 text-sm">
          <label className="flex items-center gap-1.5">
            <input
              type="radio"
              name="mode"
              checked={mode === "database"}
              onChange={() => {
                setMode("database")
                setAgainstId("")
              }}
            />
            Database lain
          </label>
          <label className="flex items-center gap-1.5">
            <input
              type="radio"
              name="mode"
              checked={mode === "snapshot"}
              onChange={() => {
                setMode("snapshot")
                setAgainstId("")
              }}
            />
            Snapshot
          </label>
        </div>

        <div className="flex flex-wrap items-end gap-2">
          <Field label={mode === "database" ? "Database pembanding" : "Snapshot"} htmlFor="diff-against" className="min-w-64">
            <select id="diff-against" className={inputClass} value={againstId} onChange={(e) => setAgainstId(e.target.value)}>
              <option value="">Pilih…</option>
              {mode === "database" &&
                candidates.map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.name}
                  </option>
                ))}
              {mode === "snapshot" &&
                snapshots.data?.map((s) => (
                  <option key={s.id} value={s.id}>
                    #{s.id} · {new Date(s.takenAt).toLocaleString()}
                    {s.migrationRunId ? ` · setelah run ${s.migrationRunId}` : ""}
                  </option>
                ))}
            </select>
          </Field>
          <Button onClick={() => void compare()} disabled={!againstId || diff.isPending}>
            {diff.isPending ? "Membandingkan…" : "Bandingkan"}
          </Button>
          {mode === "snapshot" && (
            <Button variant="outline" disabled={createSnapshot.isPending} onClick={() => createSnapshot.mutate()}>
              Ambil snapshot sekarang
            </Button>
          )}
        </div>
        {mode === "database" && candidates.length === 0 && !databases.isLoading && (
          <p className="text-xs text-muted-foreground">Tidak ada database lain berjenis sama yang dapat Anda lihat.</p>
        )}
        {mode === "snapshot" && snapshots.data?.length === 0 && (
          <p className="text-xs text-muted-foreground">Belum ada snapshot. Ambil snapshot untuk menyimpan kondisi schema saat ini.</p>
        )}
        {createSnapshot.error && <p role="alert" className="text-sm text-destructive">{errorMessage(createSnapshot.error)}</p>}
      </fieldset>

      {diff.error && <ErrorState error={diff.error} />}

      {result && (
        <div className="space-y-4">
          {result.changes.length === 0 ? (
            <p role="status" className="rounded-xl border border-dashed p-6 text-center text-sm text-muted-foreground">
              Schema identik. Tidak ada perubahan.
            </p>
          ) : (
            <>
              <section aria-label="Daftar perubahan">
                <h3 className="mb-2 font-medium">{result.changes.length} perubahan</h3>
                <ul className="divide-y rounded-xl border">
                  {result.changes.map((c, i) => (
                    <li key={`${c.kind}-${c.schema}-${c.table}-${c.name}-${i}`} className={c.destructive ? "flex items-center justify-between gap-2 bg-destructive/5 px-3 py-2 text-sm" : "flex items-center justify-between gap-2 px-3 py-2 text-sm"}>
                      <span>{describeChange(c)}</span>
                      {c.destructive && <Badge tone="danger">destructive</Badge>}
                    </li>
                  ))}
                </ul>
              </section>

              {destructive && (
                <p role="alert" className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">
                  Ada perubahan destructive yang dapat menghapus data. Rename tidak terdeteksi otomatis dan muncul sebagai drop + add.
                </p>
              )}
              {result.warnings.length > 0 && (
                <ul aria-label="Peringatan" className="space-y-1 rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-sm">
                  {result.warnings.map((w) => (
                    <li key={w}>{w}</li>
                  ))}
                </ul>
              )}

              <section className="space-y-3">
                <h3 className="font-medium">SQL migration (dapat diubah)</h3>
                <div className="overflow-hidden rounded-lg border">
                  <CodeMirror
                    value={script}
                    height="300px"
                    extensions={[sql({ dialect: db.kind === "mysql" ? MySQL : PostgreSQL })]}
                    onChange={setScript}
                    basicSetup={{ lineNumbers: true }}
                    aria-label="SQL hasil diff"
                  />
                </div>
                <Field label="Judul draft" htmlFor="diff-title">
                  <input id="diff-title" className={inputClass} value={title} onChange={(e) => setTitle(e.target.value)} />
                </Field>
                <div className="flex flex-wrap items-center gap-3">
                  <Button onClick={() => void saveDraft()} disabled={!canEdit || saving || !title.trim() || !script.trim()}>
                    {saving ? "Menyimpan…" : "Simpan sebagai draft migration"}
                  </Button>
                  {!canEdit && <span className="text-xs text-muted-foreground">Menyimpan draft butuh akses View + Edit pada {db.name}.</span>}
                  {saved && (
                    <span role="status" className="text-sm text-emerald-600">
                      Draft #{saved.id} tersimpan. <Link className="underline" to={`/databases/${databaseId}/migrations`}>Buka Migrations</Link>
                    </span>
                  )}
                </div>
                {saveError && <p role="alert" className="text-sm text-destructive">{saveError}</p>}
              </section>
            </>
          )}
        </div>
      )}
    </div>
  )
}
