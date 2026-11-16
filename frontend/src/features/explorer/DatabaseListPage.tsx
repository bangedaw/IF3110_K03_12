import { Link } from "react-router-dom"
import { Badge } from "@/components/ui/form"
import { ErrorState, Loading } from "@/components/ErrorState"
import { useDatabases } from "./api"
import { levelLabel } from "./format"

export function DatabaseListPage() {
  const { data, error, isLoading, refetch } = useDatabases()

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-semibold">Database</h1>
        <p className="text-sm text-muted-foreground">Database yang dapat Anda akses melalui keanggotaan group.</p>
      </div>
      {isLoading && <Loading />}
      {error && <ErrorState error={error} onRetry={() => void refetch()} />}
      {data && data.length === 0 && (
        <p className="rounded-xl border border-dashed p-6 text-center text-sm text-muted-foreground">
          Anda belum memiliki akses ke database mana pun. Minta Platform Admin menambahkan group Anda ke sebuah database.
        </p>
      )}
      {data && data.length > 0 && (
        <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {data.map((db) => (
            <li key={db.id}>
              <Link
                to={`/databases/${db.id}`}
                className="block rounded-xl border p-4 transition-colors hover:bg-muted/50 focus-visible:ring-3 focus-visible:ring-ring/50 outline-none"
              >
                <div className="flex items-start justify-between gap-2">
                  <span className="font-medium">{db.name}</span>
                  <Badge>{db.kind === "postgres" ? "PostgreSQL" : "MySQL"}</Badge>
                </div>
                <div className="mt-3">
                  <Badge tone={db.level === "edit" ? "success" : "neutral"}>{levelLabel(db.level)}</Badge>
                </div>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
