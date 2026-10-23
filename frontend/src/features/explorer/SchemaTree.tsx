import { useState } from "react"
import { NavLink } from "react-router-dom"
import { ChevronRight } from "lucide-react"
import { cn } from "@/lib/utils"
import { ErrorState, Loading } from "@/components/ErrorState"
import { useSchemas, useTables } from "./api"

function SchemaNode({ databaseId, schema, initiallyOpen }: { databaseId: number; schema: string; initiallyOpen: boolean }) {
  const [open, setOpen] = useState(initiallyOpen)
  const tables = useTables(databaseId, schema, open)

  return (
    <li>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1 rounded-md px-2 py-1 text-left text-sm font-medium hover:bg-muted"
      >
        <ChevronRight className={cn("size-3.5 transition-transform", open && "rotate-90")} />
        {schema}
      </button>
      {open && (
        <div className="ml-4 border-l pl-2">
          {tables.isLoading && <Loading label="Memuat tabel…" />}
          {tables.error && <ErrorState error={tables.error} />}
          {tables.data && tables.data.length === 0 && <p className="px-2 py-1 text-xs text-muted-foreground">Tidak ada tabel</p>}
          <ul>
            {tables.data?.map((t) => (
              <li key={t.name}>
                <NavLink
                  to={`/databases/${databaseId}/schemas/${encodeURIComponent(schema)}/tables/${encodeURIComponent(t.name)}`}
                  className={({ isActive }) =>
                    cn(
                      "flex items-center justify-between gap-2 rounded-md px-2 py-1 text-sm hover:bg-muted",
                      isActive && "bg-muted font-medium",
                    )
                  }
                >
                  <span className="truncate">{t.name}</span>
                  {t.kind === "view" && <span className="text-xs text-muted-foreground">view</span>}
                </NavLink>
              </li>
            ))}
          </ul>
        </div>
      )}
    </li>
  )
}

export function SchemaTree({ databaseId, activeSchema }: { databaseId: number; activeSchema?: string }) {
  const schemas = useSchemas(databaseId)

  if (schemas.isLoading) return <Loading label="Memuat schema…" />
  if (schemas.error) return <ErrorState error={schemas.error} onRetry={() => void schemas.refetch()} />
  return (
    <nav aria-label="Schema">
      <ul className="space-y-1">
        {schemas.data?.map((s, index) => (
          <SchemaNode
            key={s}
            databaseId={databaseId}
            schema={s}
            initiallyOpen={activeSchema ? s === activeSchema : index === 0}
          />
        ))}
      </ul>
    </nav>
  )
}
