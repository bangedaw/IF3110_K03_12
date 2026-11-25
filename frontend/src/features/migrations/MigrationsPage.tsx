import { Link, useNavigate, useParams } from "react-router-dom"
import { buttonVariants } from "@/components/ui/button"
import { Badge } from "@/components/ui/form"
import { ErrorState, Loading } from "@/components/ErrorState"
import { useDatabase } from "@/features/explorer/api"
import { useCreateMigration, useMigrations } from "./api"
import { positionLabel, statusLabel, statusTone } from "./format"
import { MigrationForm } from "./MigrationForm"

export function MigrationsPage() {
  const databaseId = Number(useParams().databaseId)
  const migrations = useMigrations(databaseId)
  const database = useDatabase(databaseId)

  if (migrations.isLoading) return <Loading />
  if (migrations.error) return <ErrorState error={migrations.error} onRetry={() => void migrations.refetch()} />
  const items = migrations.data ?? []

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-lg font-semibold">Migrations</h2>
        {database.data?.level === "edit" && (
          <Link className={buttonVariants({ size: "sm", className: "ml-auto" })} to={`/databases/${databaseId}/migrations/new`}>
            Migration baru
          </Link>
        )}
      </div>
      {items.length === 0 ? (
        <p className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">Belum ada migration.</p>
      ) : (
        <ul className="divide-y rounded-xl border">
          {items.map((m) => (
            <li key={m.id} className="flex flex-wrap items-center gap-2 p-3 text-sm">
              <Link className="min-w-0 flex-1 basis-48 font-medium break-words underline-offset-2 hover:underline" to={`/databases/${databaseId}/migrations/${m.id}`}>
                {m.title}
              </Link>
              {m.version !== undefined && <span className="text-xs text-muted-foreground">versi {m.version}</span>}
              <span className="text-xs text-muted-foreground">{positionLabel[m.position]}</span>
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
  const database = useDatabase(databaseId)
  const navigate = useNavigate()
  const create = useCreateMigration(databaseId)

  if (database.isLoading) return <Loading />
  if (database.error) return <ErrorState error={database.error} />
  if (database.data?.level !== "edit") {
    return <p className="text-sm text-muted-foreground">Membuat migration butuh akses View + Edit.</p>
  }
  return (
    <div className="space-y-4">
      <h2 className="text-lg font-semibold">Migration baru</h2>
      <MigrationForm
        kind={database.data.kind}
        submitLabel="Simpan draft"
        onSubmit={async (body) => {
          const m = await create.mutateAsync(body)
          navigate(`/databases/${databaseId}/migrations/${m.id}`)
        }}
      />
    </div>
  )
}
