import { useEffect, useRef } from "react"
import { Badge } from "@/components/ui/form"
import { ErrorState, Loading } from "@/components/ErrorState"
import { isFinished, useJob, type Job } from "./api"

type Tone = "neutral" | "success" | "warning" | "danger"

const kindLabel: Record<Job["kind"], string> = { apply: "Apply", rollback: "Rollback", forward: "Forward" }

function describe(job: Job): { label: string; tone: Tone; note?: string } {
  switch (job.status) {
    case "pending":
      return job.attempts > 0 ? { label: "Menunggu retry", tone: "warning" } : { label: "Dalam antrean", tone: "neutral" }
    case "running":
      return { label: "Berjalan", tone: "neutral" }
    case "succeeded":
      return { label: "Berhasil", tone: "success" }
    case "failed":
      return { label: "Gagal", tone: "danger" }
    case "partial":
      return job.kind === "rollback"
        ? {
            label: "Partially reverted",
            tone: "warning",
            note: "Sebagian batch sudah dibatalkan dan sisanya masih ter-commit. Jalankan rollback lagi untuk menyelesaikannya. Migration lain di database ini tertahan sampai itu selesai.",
          }
        : {
            label: "Partial",
            tone: "warning",
            note: "Sebagian batch sudah ter-commit. Jalankan lagi untuk melanjutkan dari checkpoint, atau rollback untuk membatalkan batch yang sudah ter-commit. Migration lain di database ini tertahan sampai itu selesai.",
          }
  }
}

const number = new Intl.NumberFormat("id-ID")
const time = (iso: string) => new Date(iso).toLocaleTimeString("id-ID")

export interface JobStatusProps {
  databaseId: number
  jobId: number
  // onFinish dipanggil sekali saat job yang sedang dipantau berpindah ke status akhir, misalnya untuk memuat
  // ulang detail migration. Tidak dipanggil jika job sudah selesai saat komponen pertama kali tampil.
  onFinish?: (job: Job) => void
  pollMs?: number
}

// JobStatus menampilkan status satu job eksekusi migration dan memperbarui dirinya sendiri sampai job selesai.
// Pemakaian: <JobStatus databaseId={databaseId} jobId={jobId} />
export function JobStatus({ databaseId, jobId, onFinish, pollMs }: JobStatusProps) {
  const { data: job, error, isLoading, refetch } = useJob(databaseId, jobId, pollMs)
  const watched = useRef(false)

  useEffect(() => {
    if (!job) return
    if (!isFinished(job)) {
      watched.current = true
    } else if (watched.current) {
      watched.current = false
      onFinish?.(job)
    }
  }, [job, onFinish])

  if (isLoading) return <Loading label="Memuat status job…" />
  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />
  if (!job) return null

  const { label, tone, note } = describe(job)
  const batched = job.batchesCommitted > 0 || job.rowsCommitted > 0
  // runAfter hanya dikirim saat pending. Job baru punya runAfter sama dengan createdAt, jadi baru ditampilkan
  // jika ada jeda: retry, menunggu lock database, atau dijalankan ulang setelah partial.
  const nextAttempt = job.runAfter && Date.parse(job.runAfter) > Date.parse(job.createdAt) ? job.runAfter : undefined

  return (
    <section aria-label={`Status job ${job.id}`} className="space-y-2 rounded-xl border p-4 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">
          Job #{job.id} · {kindLabel[job.kind]}
        </span>
        <span aria-live="polite">
          <Badge tone={tone}>{label}</Badge>
        </span>
        {job.attempts > 0 && (
          <span className="text-muted-foreground">
            Attempt {job.attempts} dari {job.maxAttempts}
          </span>
        )}
      </div>

      {batched && (
        <p>
          {number.format(job.batchesCommitted)} batch · {number.format(job.rowsCommitted)} row {job.kind === "rollback" ? "masih ter-commit" : "ter-commit"}
        </p>
      )}
      {note && <p className="text-muted-foreground">{note}</p>}
      {nextAttempt && <p className="text-muted-foreground">Attempt berikutnya paling cepat pukul {time(nextAttempt)}</p>}
      {job.lastError && job.status !== "succeeded" && <p className="break-words text-destructive">Error terakhir: {job.lastError}</p>}

      <p className="text-xs text-muted-foreground">
        Dibuat {time(job.createdAt)}
        {job.startedAt && <> · mulai {time(job.startedAt)}</>}
        {job.finishedAt && <> · selesai {time(job.finishedAt)}</>}
      </p>
    </section>
  )
}
