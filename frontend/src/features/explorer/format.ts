import type { AccessLevel } from "@/features/shared/types"

export function levelLabel(level: AccessLevel): string {
  return level === "edit" ? "View + Edit" : "View Only"
}

export function formatTotal(total: number): string {
  return total > 0 ? `≈ ${total.toLocaleString("id-ID")} baris` : "estimasi belum tersedia"
}
