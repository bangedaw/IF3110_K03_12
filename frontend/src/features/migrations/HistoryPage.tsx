import { useState } from "react"
import { useParams } from "react-router-dom"
import { ErrorState, Loading } from "@/components/ErrorState"
import { useHistory } from "./api"
import { HistoryTable } from "./HistoryTable"

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
          Hide rollback
        </label>
      </div>
      {history.isLoading && <Loading />}
      {history.error && <ErrorState error={history.error} onRetry={() => void history.refetch()} />}
      {history.data && <HistoryTable databaseId={databaseId} items={history.data} />}
    </div>
  )
}
