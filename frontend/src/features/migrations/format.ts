import type { Migration, MigrationExecution } from "@/features/shared/types"

export const statusLabel: Record<Migration["status"], string> = {
  draft: "Draft",
  pending_review: "Pending Review",
  approved: "Approved",
  pending_queue: "Pending (Queue)",
  running: "Running",
  success: "Success",
  failed: "Failed",
}

export const kindLabel: Record<MigrationExecution["kind"], string> = {
  apply: "Apply",
  rollback: "Rollback",
  forward: "Forward",
}

export function statusTone(status: string): "neutral" | "success" | "warning" | "danger" {
  if (status === "success") return "success"
  if (status === "failed") return "danger"
  if (status === "draft") return "neutral"
  return "warning"
}

// Backend mengirim waktu nol (0001-01-01) selama attempt masih running.
export function formatTime(value: string | null | undefined): string {
  if (!value || value.startsWith("0001")) return "—"
  return new Date(value).toLocaleString("id-ID")
}
