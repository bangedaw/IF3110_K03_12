import { useParams, useSearchParams } from "react-router-dom"
import { cn } from "@/lib/utils"
import { Badge } from "@/components/ui/form"
import { ErrorState, Loading } from "@/components/ErrorState"
import type { Constraint, TableDetail } from "@/features/shared/types"
import { useTable } from "./api"
import { DataGrid } from "./DataGrid"
import { formatTotal } from "./format"

const TABS = [
  { id: "columns", label: "Columns" },
  { id: "constraints", label: "Constraints" },
  { id: "indexes", label: "Indexes" },
  { id: "data", label: "Data" },
] as const

type TabId = (typeof TABS)[number]["id"]

const CONSTRAINT_LABEL: Record<Constraint["type"], string> = {
  primary_key: "PRIMARY KEY",
  foreign_key: "FOREIGN KEY",
  unique: "UNIQUE",
  check: "CHECK",
}

function ColumnsTab({ table }: { table: TableDetail }) {
  return (
    <div className="overflow-x-auto rounded-xl border">
      <table className="w-full text-sm">
        <thead className="bg-muted/50 text-left">
          <tr>
            <th className="px-3 py-2 font-medium">#</th>
            <th className="px-3 py-2 font-medium">Nama</th>
            <th className="px-3 py-2 font-medium">Tipe</th>
            <th className="px-3 py-2 font-medium">Nullable</th>
            <th className="px-3 py-2 font-medium">Default</th>
          </tr>
        </thead>
        <tbody>
          {(table.columns ?? []).map((c) => (
            <tr key={c.name} className="border-t">
              <td className="px-3 py-1.5 text-muted-foreground">{c.ordinal}</td>
              <td className="px-3 py-1.5 font-mono text-xs font-medium">{c.name}</td>
              <td className="px-3 py-1.5 font-mono text-xs">{c.rawType || c.dataType}</td>
              <td className="px-3 py-1.5">{c.nullable ? "ya" : <Badge>NOT NULL</Badge>}</td>
              <td className="px-3 py-1.5 font-mono text-xs">
                {c.default ?? ""}
                {c.isIdentity && <Badge>identity</Badge>} {c.isGenerated && <Badge>generated</Badge>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function ConstraintsTab({ table }: { table: TableDetail }) {
  const constraints = table.constraints ?? []
  if (constraints.length === 0) return <p className="text-sm text-muted-foreground">Tidak ada constraint.</p>
  return (
    <div className="overflow-x-auto rounded-xl border">
      <table className="w-full text-sm">
        <thead className="bg-muted/50 text-left">
          <tr>
            <th className="px-3 py-2 font-medium">Nama</th>
            <th className="px-3 py-2 font-medium">Tipe</th>
            <th className="px-3 py-2 font-medium">Detail</th>
          </tr>
        </thead>
        <tbody>
          {constraints.map((c) => (
            <tr key={c.name} className="border-t align-top">
              <td className="px-3 py-1.5 font-mono text-xs font-medium">{c.name}</td>
              <td className="px-3 py-1.5">
                <Badge>{CONSTRAINT_LABEL[c.type] ?? c.type}</Badge>
              </td>
              <td className="px-3 py-1.5 font-mono text-xs">
                {c.type === "foreign_key" ? (
                  <>
                    ({c.columns?.join(", ")}) → {c.refSchema ? `${c.refSchema}.` : ""}
                    {c.refTable}({c.refColumns?.join(", ")})
                    {c.onDelete && <span className="text-muted-foreground"> · ON DELETE {c.onDelete}</span>}
                    {c.onUpdate && <span className="text-muted-foreground"> · ON UPDATE {c.onUpdate}</span>}
                  </>
                ) : c.type === "check" ? (
                  c.definition
                ) : (
                  `(${c.columns?.join(", ")})`
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function IndexesTab({ table }: { table: TableDetail }) {
  const indexes = table.indexes ?? []
  if (indexes.length === 0) return <p className="text-sm text-muted-foreground">Tidak ada index.</p>
  return (
    <div className="overflow-x-auto rounded-xl border">
      <table className="w-full text-sm">
        <thead className="bg-muted/50 text-left">
          <tr>
            <th className="px-3 py-2 font-medium">Nama</th>
            <th className="px-3 py-2 font-medium">Kolom</th>
            <th className="px-3 py-2 font-medium">Properti</th>
          </tr>
        </thead>
        <tbody>
          {indexes.map((i) => (
            <tr key={i.name} className="border-t align-top">
              <td className="px-3 py-1.5 font-mono text-xs font-medium">{i.name}</td>
              <td className="px-3 py-1.5 font-mono text-xs">{i.columns?.join(", ") || i.definition}</td>
              <td className="space-x-1 px-3 py-1.5">
                {i.primary && <Badge tone="success">primary</Badge>}
                {i.unique && <Badge>unique</Badge>}
                {i.method && <Badge>{i.method}</Badge>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function TableView() {
  const params = useParams()
  const databaseId = Number(params.databaseId)
  const schema = params.schema ?? ""
  const tableName = params.table ?? ""
  const [search, setSearch] = useSearchParams()
  const requested = search.get("tab")
  const tab: TabId = TABS.some((t) => t.id === requested) ? (requested as TabId) : "columns"
  const query = useTable(databaseId, schema, tableName)

  if (query.isLoading) return <Loading />
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  const table = query.data
  if (!table) return null

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold">
          {table.schema}.{table.name}
        </h2>
        <p className="text-sm text-muted-foreground">
          {table.kind === "view" ? "View" : "Tabel"} · {formatTotal(table.rowEstimate)}
        </p>
      </div>

      <div role="tablist" aria-label="Detail tabel" className="flex gap-1 border-b">
        {TABS.map((t) => (
          <button
            key={t.id}
            role="tab"
            type="button"
            aria-selected={tab === t.id}
            onClick={() => setSearch({ tab: t.id }, { replace: true })}
            className={cn(
              "-mb-px border-b-2 px-3 py-1.5 text-sm",
              tab === t.id ? "border-primary font-medium" : "border-transparent text-muted-foreground hover:text-foreground",
            )}
          >
            {t.label}
          </button>
        ))}
      </div>

      <div role="tabpanel">
        {tab === "columns" && <ColumnsTab table={table} />}
        {tab === "constraints" && <ConstraintsTab table={table} />}
        {tab === "indexes" && <IndexesTab table={table} />}
        {tab === "data" && <DataGrid key={`${databaseId}.${schema}.${tableName}`} databaseId={databaseId} schema={schema} table={tableName} />}
      </div>
    </div>
  )
}
