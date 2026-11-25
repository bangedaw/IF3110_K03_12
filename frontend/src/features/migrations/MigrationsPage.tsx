import { Link, useNavigate, useParams } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/form"
import { ErrorState, Loading } from "@/components/ErrorState"
import { useDatabase } from "@/features/explorer/api"
import { useCreateMigration, useMigrations } from "./api"
import { statusLabel, statusTone } from "./format"
import { MigrationForm } from "./MigrationForm"

export function MigrationsPage() {
  const databaseId = Number(useParams().databaseId)
  const migrations = useMigrations(databaseId)
  const database = useDatabase(databaseId)
  const canEdit = database.data?.level === "edit"

  if (migrations.isLoading) return <Loading />
  if (migrations.error) return <ErrorState error={migrations.error} onRetry={() => void migrations.refetch()} />
  const items = migrations.data ?? []

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-lg font-semibold">Migrations</h2>
        <Link className="text-sm underline" to={`/databases/${databaseId}/history`}>
          Riwayat eksekusi
        </Link>
        {canEdit && (
          <Link className="ml-auto" to={`/databases/${databaseId}/migrations/new`}>
            <Button size="sm">Migration baru</Button>
          </Link>
        )}
      </div>
      {items.length === 0 ? (
        <p className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">Belum ada migration.</p>
      ) : (
        <ul className="divide-y rounded-xl border">
          {items.map((m) => (
            <li key={m.id} className="flex flex-wrap items-center gap-2 p-3">
              <span className="font-mono text-xs text-muted-foreground">#{m.seq}</span>
              <Link className="min-w-0 flex-1 truncate font-medium underline-offset-2 hover:underline" to={`/databases/${databaseId}/migrations/${m.id}`}>
                {m.title}
              </Link>
              <Badge tone={statusTone(m.status)}>{statusLabel[m.status]}</Badge>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

export function MigrationNewPage() {
  const databaseId = Number(useParams().databaseId)
  const navigate = useNavigate()
  const create = useCreateMigration(databaseId)
  return (
    <div className="space-y-4">
      <h2 className="text-lg font-semibold">Migration baru</h2>
      <MigrationForm
        submitLabel="Simpan draft"
        onSubmit={async (body) => {
          const m = await create.mutateAsync(body)
          navigate(`/databases/${databaseId}/migrations/${m.id}`)
        }}
      />
    </div>
  )
}
