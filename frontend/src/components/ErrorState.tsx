import { ApiError, errorMessage } from "@/features/shared/client"
import { Button } from "@/components/ui/button"

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const unreachable = error instanceof ApiError && error.code === "DB_UNREACHABLE"
  return (
    <div role="alert" className="rounded-lg border border-destructive/30 bg-destructive/5 p-4 text-sm">
      <p className="font-medium text-destructive">{unreachable ? "Koneksi ke database gagal" : "Gagal memuat data"}</p>
      <p className="mt-1 text-muted-foreground">{errorMessage(error)}</p>
      {onRetry && (
        <Button variant="outline" size="sm" className="mt-3" onClick={onRetry}>
          Coba lagi
        </Button>
      )}
    </div>
  )
}

export function Loading({ label = "Memuat…" }: { label?: string }) {
  return (
    <p role="status" className="p-4 text-sm text-muted-foreground">
      {label}
    </p>
  )
}
