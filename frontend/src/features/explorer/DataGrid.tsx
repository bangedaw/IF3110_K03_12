import { useMemo, useState } from "react"
import { flexRender, getCoreRowModel, useReactTable, type ColumnDef, type SortingState } from "@tanstack/react-table"
import { ArrowDown, ArrowUp } from "lucide-react"
import { Button } from "@/components/ui/button"
import { ErrorState, Loading } from "@/components/ErrorState"
import type { Cell } from "@/features/shared/types"
import { useRows } from "./api"
import { formatTotal } from "./format"

const PAGE_SIZES = [25, 50, 100, 200]

export function DataGrid({ databaseId, schema, table }: { databaseId: number; schema: string; table: string }) {
  const [pageSize, setPageSize] = useState(50)
  const [pageIndex, setPageIndex] = useState(0)
  const [sorting, setSorting] = useState<SortingState>([])

  const sort = sorting[0]
  const query = useRows(databaseId, schema, table, {
    limit: pageSize,
    offset: pageIndex * pageSize,
    orderBy: sort?.id,
    orderDir: sort?.desc ? "desc" : "asc",
  })

  const page = query.data
  const columns = useMemo<ColumnDef<Cell[]>[]>(
    () =>
      (page?.columns ?? []).map((name, index) => ({
        id: name,
        header: name,
        accessorFn: (row) => row[index],
        cell: ({ getValue }) => {
          const cell = getValue<Cell>()
          if (cell.isNull) return <span className="text-xs italic text-muted-foreground">NULL</span>
          return (
            <span title={cell.truncated ? "Nilai dipotong" : undefined}>
              {cell.value}
              {cell.truncated && "…"}
            </span>
          )
        },
      })),
    [page?.columns],
  )

  const grid = useReactTable({
    data: page?.rows ?? [],
    columns,
    state: { sorting },
    manualSorting: true,
    manualPagination: true,
    enableSortingRemoval: true,
    sortDescFirst: false,
    onSortingChange: (updater) => {
      setSorting((current) => (typeof updater === "function" ? updater(current).slice(-1) : updater.slice(-1)))
      setPageIndex(0)
    },
    getCoreRowModel: getCoreRowModel(),
  })

  if (query.isLoading) return <Loading label="Memuat data…" />
  if (query.error && !page) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  if (!page) return null

  const hasNext = page.rows.length === page.limit
  const first = page.rows.length === 0 ? 0 : page.offset + 1
  const last = page.offset + page.rows.length

  return (
    <div className="space-y-3">
      {query.error && <ErrorState error={query.error} onRetry={() => void query.refetch()} />}
      <div className="overflow-auto rounded-xl border" aria-busy={query.isFetching}>
        <table className="w-full text-sm">
          <thead className="bg-muted/50 text-left">
            {grid.getHeaderGroups().map((group) => (
              <tr key={group.id}>
                {group.headers.map((header) => {
                  const direction = header.column.getIsSorted()
                  return (
                    <th
                      key={header.id}
                      scope="col"
                      aria-sort={direction === "asc" ? "ascending" : direction === "desc" ? "descending" : "none"}
                      className="whitespace-nowrap px-3 py-2 font-medium"
                    >
                      <button
                        type="button"
                        className="inline-flex items-center gap-1 hover:underline"
                        onClick={header.column.getToggleSortingHandler()}
                      >
                        {flexRender(header.column.columnDef.header, header.getContext())}
                        {direction === "asc" && <ArrowUp className="size-3" />}
                        {direction === "desc" && <ArrowDown className="size-3" />}
                      </button>
                    </th>
                  )
                })}
              </tr>
            ))}
          </thead>
          <tbody>
            {grid.getRowModel().rows.map((row) => (
              <tr key={row.id} className="border-t">
                {row.getVisibleCells().map((cell) => (
                  <td key={cell.id} className="max-w-xs truncate px-3 py-1.5 font-mono text-xs">
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </td>
                ))}
              </tr>
            ))}
            {page.rows.length === 0 && (
              <tr>
                <td colSpan={Math.max(columns.length, 1)} className="px-3 py-6 text-center text-muted-foreground">
                  Tabel kosong
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3 text-sm">
        <span className="text-muted-foreground">
          Baris {first}–{last} · {formatTotal(page.totalEstimate)}
        </span>
        <div className="flex items-center gap-2">
          <label className="flex items-center gap-1.5 text-muted-foreground">
            Per halaman
            <select
              className="h-7 rounded-md border bg-background px-1"
              value={pageSize}
              onChange={(e) => {
                setPageSize(Number(e.target.value))
                setPageIndex(0)
              }}
            >
              {PAGE_SIZES.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </label>
          <Button variant="outline" size="sm" disabled={pageIndex === 0} onClick={() => setPageIndex((p) => p - 1)}>
            Sebelumnya
          </Button>
          <Button variant="outline" size="sm" disabled={!hasNext} onClick={() => setPageIndex((p) => p + 1)}>
            Berikutnya
          </Button>
        </div>
      </div>
    </div>
  )
}
