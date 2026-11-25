import { useState } from "react"
import { Link, useParams } from "react-router-dom"
import { Badge } from "@/components/ui/form"
import { ErrorState, Loading } from "@/components/ErrorState"
import { useHistory } from "./api"
import { formatTime, kindLabel, statusTone } from "./format"

export function HistoryPage() {
  const databaseId = Number(useParams().databaseId)
  const [hideRollback, setHideRollback] = useState(false)
  const history = useHistory(databaseId, hideRollback)

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="text-lg font-semibold">Riwayat eksekusi</h2>
        <label className="ml-auto flex items-center gap-2 text-sm">
          <input type="checkbox" checked={hideRollback} onChange={(e) => setHideRollback(e.target.checked)} />
          Sembunyikan rollback
        </label>
      </div>
      {history.isLoading && <Loading />}
      {history.error && <ErrorState error={history.error} onRetry={() => void history.refetch()} />}
      {history.data &&
        (history.data.length === 0 ? (
          <p className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">Belum ada eksekusi.</p>
        ) : (
          <div className="max-w-full overflow-x-auto rounded-xl border">
            <table className="w-full text-left text-sm">
              <thead className="bg-muted/50 text-xs text-muted-foreground">
                <tr>
                  <th className="p-2">Execution</th>
                  <th className="p-2">Migration</th>
                  <th className="p-2">Rev</th>
                  <th className="p-2">Jenis</th>
                  <th className="p-2">Status</th>
                  <th className="p-2">User</th>
                  <th className="p-2">Mulai</th>
                  <th className="p-2">Selesai</th>
                  <th className="p-2">Catatan</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {history.data.map((e) => (
                  <tr key={e.id}>
                    <td className="p-2 font-mono">#{e.id}</td>
                    <td className="p-2">
                      <Link className="underline" to={`/databases/${databaseId}/migrations/${e.migrationId}`}>
                        #{e.migrationId}
                      </Link>
                    </td>
                    <td className="p-2">{e.revision}</td>
                    <td className="p-2">{kindLabel[e.kind]}</td>
                    <td className="p-2">
                      <Badge tone={statusTone(e.status)}>{e.status}</Badge>
                    </td>
                    <td className="p-2">{e.userId}</td>
                    <td className="whitespace-nowrap p-2">{formatTime(e.startedAt)}</td>
                    <td className="whitespace-nowrap p-2">{formatTime(e.finishedAt)}</td>
                    <td className="p-2 text-xs">
                      {e.revertsExecutionId != null && <span>membatalkan #{e.revertsExecutionId}. </span>}
                      {e.status === "failed" && (
                        <span className="text-destructive">
                          {e.errorMessage} {e.sqlState && `(${e.sqlState})`}
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ))}
    </div>
  )
}
