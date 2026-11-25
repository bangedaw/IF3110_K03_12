import { useState } from "react"
import { Button } from "@/components/ui/button"
import { errorMessage } from "@/features/shared/client"
import { useExplain } from "./api"

export function ExplainPanel({ migrationId }: { migrationId: number }) {
  const explain = useExplain(migrationId)
  const [analyze, setAnalyze] = useState(false)

  return (
    <section aria-label="EXPLAIN" className="space-y-3 rounded-xl border p-4 text-sm">
      <div className="flex flex-wrap items-center gap-3">
        <h3 className="font-medium">EXPLAIN SQL up</h3>
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={analyze} onChange={(e) => setAnalyze(e.target.checked)} />
          ANALYZE (dijalankan lalu dibatalkan)
        </label>
        <Button size="sm" variant="outline" disabled={explain.isPending} onClick={() => explain.mutate(analyze)}>
          {explain.isPending ? "Menganalisis…" : "Jalankan EXPLAIN"}
        </Button>
      </div>
      {explain.error && (
        <p role="alert" className="break-words text-destructive">
          {errorMessage(explain.error)}
        </p>
      )}
      {explain.data?.map((s) => (
        <div key={s.index} className="space-y-1">
          <pre className="max-w-full overflow-x-auto rounded-lg border bg-muted/30 p-2 text-xs">{s.sql}</pre>
          {s.explainable ? (
            <pre className="max-h-80 max-w-full overflow-auto rounded-lg border p-2 text-xs">{JSON.stringify(s.plan, null, 2)}</pre>
          ) : (
            <p className="text-xs text-muted-foreground">{s.notice ?? "Statement ini tidak punya execution plan."}</p>
          )}
        </div>
      ))}
    </section>
  )
}
