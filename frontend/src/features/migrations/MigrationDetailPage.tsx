import { useState } from "react"
import { useParams } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { Badge, Field, inputClass } from "@/components/ui/form"
import { ErrorState, Loading } from "@/components/ErrorState"
import { useAuth } from "@/features/auth/AuthProvider"
import { useDatabase } from "@/features/explorer/api"
import { errorMessage } from "@/features/shared/client"
import type { Migration, MigrationKind } from "@/features/shared/types"
import { useApprovals, useDecide, useMigration, useRequestExecution, useSubmitMigration, useUpdateMigration } from "./api"
import { formatTime, kindLabel, statusLabel, statusTone } from "./format"
import { MigrationForm } from "./MigrationForm"

function Sql({ label, value }: { label: string; value: string }) {
  return (
    <section>
      <h3 className="mb-1 text-sm font-medium">{label}</h3>
      <pre className="max-w-full overflow-x-auto rounded-lg border bg-muted/30 p-3 text-xs">{value}</pre>
    </section>
  )
}

function ApprovalPanel({ migration, kind }: { migration: Migration; kind: MigrationKind }) {
  const { user } = useAuth()
  const approvals = useApprovals(migration.id, kind)
  const decide = useDecide(migration.id)
  const [comment, setComment] = useState("")

  if (approvals.isLoading) return <Loading />
  if (approvals.error) return <ErrorState error={approvals.error} />
  const { required = [], approvals: rows = [] } = approvals.data ?? {}
  const mine = rows.find((a) => a.leaderId === user?.id && a.decision === "pending")
  const rejected = rows.some((a) => a.decision === "rejected")

  return (
    <section aria-label={`Approval ${kindLabel[kind]}`} className="space-y-2 rounded-xl border p-3">
      <h3 className="text-sm font-medium">Approval {kindLabel[kind].toLowerCase()}</h3>
      {required.length === 0 ? (
        <p className="text-sm text-muted-foreground">Belum ada leader yang wajib menyetujui.</p>
      ) : (
        <ul className="space-y-1 text-sm">
          {required.map((r) => {
            const row = rows.find((a) => a.leaderId === r.leaderId)
            const decision = row?.decision ?? "pending"
            return (
              <li key={r.leaderId} className="flex flex-wrap items-center gap-2">
                <span>{r.name || `Leader #${r.leaderId}`}</span>
                <Badge tone={decision === "approved" ? "success" : decision === "rejected" ? "danger" : "neutral"}>{decision}</Badge>
                {row?.comment && <span className="text-xs text-muted-foreground">“{row.comment}”</span>}
              </li>
            )
          })}
        </ul>
      )}
      {mine && !rejected && (
        <div className="space-y-2">
          <Field label="Komentar" htmlFor={`c-${kind}`}>
            <input id={`c-${kind}`} className={inputClass} value={comment} onChange={(e) => setComment(e.target.value)} />
          </Field>
          <div className="flex gap-2">
            <Button size="sm" disabled={decide.isPending} onClick={() => decide.mutate({ kind, decision: "approved", comment })}>
              Setujui
            </Button>
            <Button size="sm" variant="destructive" disabled={decide.isPending} onClick={() => decide.mutate({ kind, decision: "rejected", comment })}>
              Tolak
            </Button>
          </div>
        </div>
      )}
      {decide.error && (
        <p role="alert" className="text-sm text-destructive">
          {errorMessage(decide.error)}
        </p>
      )}
    </section>
  )
}

export function MigrationDetailPage() {
  const params = useParams()
  const migrationId = Number(params.migrationId)
  const migration = useMigration(migrationId)
  const database = useDatabase(Number(params.databaseId))
  const update = useUpdateMigration(migrationId)
  const submit = useSubmitMigration(migrationId)
  const rollback = useRequestExecution(migrationId, "rollback")
  const forward = useRequestExecution(migrationId, "forward")
  const [editing, setEditing] = useState(false)

  if (migration.isLoading) return <Loading />
  if (migration.error) return <ErrorState error={migration.error} onRetry={() => void migration.refetch()} />
  const m = migration.data
  if (!m) return null

  const canEdit = database.data?.level === "edit"
  const actionError = submit.error ?? rollback.error ?? forward.error

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="min-w-0 break-words text-lg font-semibold">
          #{m.seq} {m.title}
        </h2>
        <Badge tone={statusTone(m.status)}>{statusLabel[m.status]}</Badge>
        <span className="text-xs text-muted-foreground">revisi {m.revision}</span>
        {m.sqlLocked && <Badge>SQL terkunci</Badge>}
      </div>
      {m.description && <p className="text-sm text-muted-foreground">{m.description}</p>}
      <p className="text-xs text-muted-foreground">Diubah {formatTime(m.updatedAt)}</p>

      {editing && m.status === "draft" ? (
        <MigrationForm
          initial={m}
          submitLabel="Simpan perubahan"
          onSubmit={async (body) => {
            await update.mutateAsync(body)
            setEditing(false)
          }}
        />
      ) : (
        <>
          <Sql label="SQL up" value={m.upSql} />
          {m.irreversible ? (
            <p className="text-sm">
              <Badge tone="warning">Irreversible</Badge> {m.irreversibleReason}
            </p>
          ) : (
            <Sql label="SQL down" value={m.downSql ?? ""} />
          )}
          {m.batch && (
            <p className="text-sm">
              Mode batch: kolom <code>{m.batch.column}</code>, ukuran {m.batch.size}
            </p>
          )}
        </>
      )}

      {canEdit && m.status === "draft" && !editing && (
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" onClick={() => setEditing(true)}>
            Ubah
          </Button>
          <Button disabled={submit.isPending} onClick={() => submit.mutate()}>
            Ajukan review
          </Button>
        </div>
      )}
      {canEdit && m.status === "success" && (
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" disabled={rollback.isPending || m.irreversible} onClick={() => rollback.mutate()}>
            Ajukan rollback
          </Button>
          <Button variant="outline" disabled={forward.isPending} onClick={() => forward.mutate()}>
            Ajukan forward
          </Button>
        </div>
      )}
      {actionError && (
        <p role="alert" className="text-sm text-destructive">
          {errorMessage(actionError)}
        </p>
      )}

      {m.status === "pending_review" && <ApprovalPanel migration={m} kind="apply" />}
      {m.status === "success" && (
        <>
          <ApprovalPanel migration={m} kind="rollback" />
          <ApprovalPanel migration={m} kind="forward" />
        </>
      )}
    </div>
  )
}
