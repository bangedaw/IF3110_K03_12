import { NavLink, Outlet, useParams } from "react-router-dom"
import { cn } from "@/lib/utils"
import { Badge } from "@/components/ui/form"
import { ErrorState, Loading } from "@/components/ErrorState"
import { useDatabase } from "./api"
import { levelLabel } from "./format"
import { SchemaTree } from "./SchemaTree"

function SectionLink({ to, end, children }: { to: string; end?: boolean; children: string }) {
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) =>
        cn(
          "-mb-px border-b-2 px-3 py-1.5 text-sm",
          isActive ? "border-primary font-medium" : "border-transparent text-muted-foreground hover:text-foreground",
        )
      }
    >
      {children}
    </NavLink>
  )
}

export function DatabaseLayout() {
  const params = useParams()
  const databaseId = Number(params.databaseId)
  const database = useDatabase(databaseId)

  if (database.isLoading) return <Loading />
  if (database.error) return <ErrorState error={database.error} onRetry={() => void database.refetch()} />
  const db = database.data
  if (!db) return null

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-xl font-semibold">{db.name}</h1>
        <Badge>{db.kind === "postgres" ? "PostgreSQL" : "MySQL"}</Badge>
        <Badge tone={db.level === "edit" ? "success" : "neutral"}>{levelLabel(db.level)}</Badge>
        <span className="font-mono text-xs text-muted-foreground">
          {db.host}:{db.port}/{db.databaseName}
        </span>
      </div>

      <nav aria-label="Bagian database" className="flex border-b">
        <SectionLink to={`/databases/${databaseId}`} end>
          Explorer
        </SectionLink>
        <SectionLink to={`/databases/${databaseId}/diff`}>Schema diff</SectionLink>
      </nav>

      <div className="grid gap-6 lg:grid-cols-[260px_minmax(0,1fr)]">
        <aside className="lg:sticky lg:top-4 lg:max-h-[calc(100vh-8rem)] lg:self-start lg:overflow-y-auto">
          <SchemaTree databaseId={databaseId} activeSchema={params.schema} />
        </aside>
        <section className="min-w-0">
          <Outlet />
        </section>
      </div>
    </div>
  )
}

export function ExplorerHome() {
  return (
    <p className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">
      Pilih tabel di sebelah kiri untuk melihat kolom, constraint, index, dan datanya.
    </p>
  )
}
