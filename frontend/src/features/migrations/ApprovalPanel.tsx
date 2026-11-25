import { useState } from "react"
import { Button } from "@/components/ui/button"
import { Badge, Field, inputClass } from "@/components/ui/form"
import { ErrorState, Loading } from "@/components/ErrorState"
import { errorMessage } from "@/features/shared/client"
import { useApprovals, useDecide } from "./api"
import { formatTime, kindLabel, statusLabel, statusTone } from "./format"

const decisionLabel = { pending: "Menunggu", approved: "Setuju", rejected: "Ditolak" } as const
const decisionTone = { pending: "neutral", approved: "success", rejected: "danger" } as const

export function ApprovalPanel({ databaseId, migrationId, requestId }: { databaseId: number; migrationId: number; requestId: number }) {
  const approvals = useApprovals(migrationId, requestId)
  const decide = useDecide(databaseId, migrationId)
  const [comment, setComment] = useState("")

  if (approvals.isLoading) return <Loading />
  if (approvals.error) return <ErrorState error={approvals.error} onRetry={() => void approvals.refetch()} />
  if (!approvals.data) return null
  const { request, groups, canDecide } = approvals.data

  return (
    <section aria-label="Approval" className="space-y-3 rounded-xl border p-4 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="font-medium">
          Request {kindLabel[request.kind].toLowerCase()} #{request.id}
        </h3>
        <Badge tone={statusTone(request.status)}>{statusLabel[request.status]}</Badge>
        <span className="text-xs text-muted-foreground">
          oleh {request.requestedByName}, {formatTime(request.createdAt)}, revisi {request.revision}
        </span>
      </div>
      <ul className="space-y-2">
        {groups.map((g) => (
          <li key={g.groupId} className="flex flex-wrap items-center gap-2">
            <span className="font-medium">{g.groupName}</span>
            <span className="text-muted-foreground">{g.leaderName ? `leader ${g.leaderName}` : "belum punya leader"}</span>
            <Badge tone={decisionTone[g.decision]}>{decisionLabel[g.decision]}</Badge>
            {g.decidedByName && (
              <span className="text-xs text-muted-foreground">
                {g.decidedByName}, {formatTime(g.decidedAt)}
              </span>
            )}
            {g.comment && <span className="w-full break-words text-xs text-muted-foreground">“{g.comment}”</span>}
          </li>
        ))}
      </ul>
      {canDecide && (
        <div className="space-y-2">
          <Field label="Komentar" htmlFor="approval-comment">
            <input id="approval-comment" className={inputClass} maxLength={2000} value={comment} onChange={(e) => setComment(e.target.value)} />
          </Field>
          <div className="flex flex-wrap gap-2">
            <Button disabled={decide.isPending} onClick={() => decide.mutate({ requestId: request.id, decision: "approved", comment })}>
              Setujui
            </Button>
            <Button variant="destructive" disabled={decide.isPending} onClick={() => decide.mutate({ requestId: request.id, decision: "rejected", comment })}>
              Tolak
            </Button>
          </div>
        </div>
      )}
      {decide.error && (
        <p role="alert" className="text-destructive">
          {errorMessage(decide.error)}
        </p>
      )}
    </section>
  )
}
