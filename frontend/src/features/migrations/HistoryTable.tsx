import { Link } from "react-router-dom"
import { Badge } from "@/components/ui/form"
import type { MigrationExecution } from "@/features/shared/types"
import { formatTime, kindLabel, statusLabel, statusTone } from "./format"

export function HistoryTable({ databaseId, items }: { databaseId: number; items: MigrationExecution[] }) {
  if (items.length === 0) {
    return <p className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">Belum ada eksekusi.</p>
  }
  return (
    <div className="max-w-full overflow-x-auto rounded-xl border">
      <table className="w-full min-w-4xl text-left text-sm">
        <thead className="bg-muted/50 text-xs text-muted-foreground">
          <tr>
            {["Execution", "Migration", "Versi", "Revisi", "Jenis", "Status", "Pelaksana", "Mulai", "Selesai", "SQL dan catatan"].map((h) => (
              <th key={h} className="p-2">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y">
          {items.map((e) => (
            <tr key={e.id} className="align-top">
              <td className="p-2 font-mono">#{e.id}</td>
              <td className="p-2">
                <Link className="underline" to={`/databases/${databaseId}/migrations/${e.migrationId}`}>
                  {e.migrationTitle}
                </Link>
              </td>
              <td className="p-2">{e.version ?? "—"}</td>
              <td className="p-2">{e.revision}</td>
              <td className="p-2">{kindLabel[e.kind]}</td>
              <td className="p-2">
                <Badge tone={statusTone(e.status)}>{statusLabel[e.status]}</Badge>
                {e.attempt > 1 && <span className="block text-xs text-muted-foreground">attempt {e.attempt}</span>}
              </td>
              <td className="p-2">{e.executedByName}</td>
              <td className="whitespace-nowrap p-2">{formatTime(e.startedAt)}</td>
              <td className="whitespace-nowrap p-2">{formatTime(e.finishedAt)}</td>
              <td className="space-y-1 p-2 text-xs">
                {e.revertsExecutionId !== undefined && <p>Membatalkan #{e.revertsExecutionId}</p>}
                {e.errorMessage && (
                  <p className="text-destructive">
                    {e.errorMessage}
                    {e.errorCode && ` (${e.errorCode})`}
                  </p>
                )}
                <details>
                  <summary className="cursor-pointer">Lihat SQL</summary>
                  <pre className="mt-1 max-w-md overflow-x-auto rounded border bg-muted/30 p-2">{e.executedSql}</pre>
                </details>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
