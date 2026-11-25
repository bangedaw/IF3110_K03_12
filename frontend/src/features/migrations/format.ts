import type { Migration, MigrationExecution } from "@/features/shared/types"

type Tone = "neutral" | "success" | "warning" | "danger"

export const statusLabel: Record<Migration["status"], string> = {
  draft: "Draft",
  pending_review: "Pending Review",
  approved: "Approved",
  rejected: "Rejected",
  queued: "Pending (Queue)",
  running: "Running",
  succeeded: "Success",
  failed: "Failed",
  partial: "Partial",
}

export const positionLabel: Record<Migration["position"], string> = {
  draft: "Belum diterapkan",
  applied: "Aktif",
  rolled_back: "Sudah di-rollback",
}

export const kindLabel: Record<MigrationExecution["kind"], string> = {
  apply: "Apply",
  rollback: "Rollback",
  forward: "Forward",
}

export function statusTone(status: Migration["status"]): Tone {
  if (status === "succeeded") return "success"
  if (status === "failed" || status === "rejected") return "danger"
  if (status === "draft") return "neutral"
  return "warning"
}

export function formatTime(value: string | undefined): string {
  return value ? new Date(value).toLocaleString("id-ID") : "—"
}
