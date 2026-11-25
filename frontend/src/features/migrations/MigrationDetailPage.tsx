import { useState } from "react"
import { useNavigate, useParams } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/form"
import { ErrorState, Loading } from "@/components/ErrorState"
import { useDatabase } from "@/features/explorer/api"
import { JobStatus } from "@/features/jobs/JobStatus"
import { errorMessage } from "@/features/shared/client"
import type { MigrationAction } from "@/features/shared/types"
import {
  useDeleteMigration,
  useExecute,
  useHistory,
  useMigration,
  useRefreshMigrations,
  useRequestMigration,
  useUpdateMigration,
} from "./api"
import { ApprovalPanel } from "./ApprovalPanel"
import { ExplainPanel } from "./ExplainPanel"
import { formatTime, positionLabel, statusLabel, statusTone } from "./format"
import { HistoryTable } from "./HistoryTable"
import { MigrationForm } from "./MigrationForm"

function Sql({ label, value }: { label: string; value: string }) {
  return (
    <section>
      <h3 className="mb-1 text-sm font-medium">{label}</h3>
      <pre className="max-w-full overflow-x-auto rounded-lg border bg-muted/30 p-3 text-xs">{value}</pre>
    </section>
  )
}

function MigrationHistory({ databaseId, migrationId }: { databaseId: number; migrationId: number }) {
  const history = useHistory(databaseId, false, migrationId)
  if (history.isLoading) return <Loading />
  if (history.error) return <ErrorState error={history.error} onRetry={() => void history.refetch()} />
  return <HistoryTable databaseId={databaseId} items={history.data ?? []} />
}

export function MigrationDetailPage() {
  const params = useParams()
  const navigate = useNavigate()
  const databaseId = Number(params.databaseId)
  const migrationId = Number(params.migrationId)
  const database = useDatabase(databaseId)
  const migration = useMigration(migrationId)
  const refresh = useRefreshMigrations(databaseId)
  const update = useUpdateMigration(databaseId, migrationId)
  const remove = useDeleteMigration(databaseId, migrationId)
  const request = useRequestMigration(databaseId, migrationId)
  const execute = useExecute(databaseId, migrationId)
  const [editing, setEditing] = useState(false)

  if (migration.isLoading) return <Loading />
  if (migration.error) return <ErrorState error={migration.error} onRetry={() => void migration.refetch()} />
  const m = migration.data
  if (!m) return null

  const can = (action: MigrationAction) => m.actions.includes(action)
  const busy = request.isPending || execute.isPending || remove.isPending
  const actionError = request.error ?? execute.error ?? remove.error
  const rerun = m.status === "failed" || m.status === "partial"
  const executable = m.executableRequestId
  const executeLabel = m.status === "partial" ? "Lanjutkan dari checkpoint" : rerun ? "Jalankan ulang" : "Execute"

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="min-w-0 text-lg font-semibold break-words">{m.title}</h2>
        <Badge tone={statusTone(m.status)}>{statusLabel[m.status]}</Badge>
        <Badge>{positionLabel[m.position]}</Badge>
        {m.version !== undefined && <Badge>versi {m.version}</Badge>}
        {m.sqlLocked && <Badge>SQL terkunci</Badge>}
      </div>
      {m.description && <p className="text-sm break-words text-muted-foreground">{m.description}</p>}
      <p className="text-xs text-muted-foreground">
        Dibuat {m.createdByName}, diubah {formatTime(m.updatedAt)}, revisi {m.revision}
      </p>

      {editing && can("edit") ? (
        <MigrationForm
          kind={database.data?.kind ?? "postgres"}
          initial={m}
          submitLabel="Simpan perubahan"
          onCancel={() => setEditing(false)}
          onSubmit={async (body) => {
            await update.mutateAsync({ ...body, revision: m.revision })
            setEditing(false)
          }}
        />
      ) : (
        <>
          <Sql label="SQL up" value={m.upSql} />
          {m.irreversible ? (
            <p className="text-sm break-words">
              <Badge tone="warning">Irreversible</Badge> {m.irreversibleReason}
            </p>
          ) : (
            <Sql label="SQL down" value={m.downSql} />
          )}
          {m.batch && (
            <p className="text-sm break-words">
              Per batch: {m.batch.size} row berdasarkan{" "}
              <code className="break-all">
                {m.batch.schema}.{m.batch.table}.{m.batch.column}
              </code>
            </p>
          )}
          <div className="flex flex-wrap gap-2">
            {can("execute") && executable !== undefined && (
              <Button disabled={busy} onClick={() => execute.mutate(executable)}>
                {executeLabel}
              </Button>
            )}
            {can("submit") && (
              <Button variant={can("execute") ? "outline" : "default"} disabled={busy} onClick={() => request.mutate("submit")}>
                Ajukan review
              </Button>
            )}
            {can("rollback") && (
              <Button variant="outline" disabled={busy} onClick={() => request.mutate("rollback")}>
                Ajukan rollback
              </Button>
            )}
            {can("forward") && (
              <Button variant="outline" disabled={busy} onClick={() => request.mutate("forward")}>
                Ajukan forward
              </Button>
            )}
            {can("edit") && (
              <Button variant="outline" onClick={() => setEditing(true)}>
                Ubah
              </Button>
            )}
            {can("delete") && (
              <Button
                variant="destructive"
                disabled={busy}
                onClick={() => {
                  if (window.confirm(`Hapus draft "${m.title}"?`)) {
                    remove.mutate(undefined, { onSuccess: () => navigate(`/databases/${databaseId}/migrations`) })
                  }
                }}
              >
                Hapus
              </Button>
            )}
          </div>
        </>
      )}
      {actionError && (
        <p role="alert" className="text-sm break-words text-destructive">
          {errorMessage(actionError)}
        </p>
      )}

      {m.request && <ApprovalPanel key={`${m.request.id}-${m.request.status}`} databaseId={databaseId} migrationId={m.id} requestId={m.request.id} />}
      {m.jobId !== undefined && <JobStatus databaseId={databaseId} jobId={m.jobId} onFinish={() => void refresh()} />}
      {can("explain") && !editing && <ExplainPanel migrationId={m.id} />}

      <section className="space-y-2">
        <h3 className="text-sm font-medium">Riwayat eksekusi migration ini</h3>
        <MigrationHistory key={m.status} databaseId={databaseId} migrationId={m.id} />
      </section>
    </div>
  )
}
