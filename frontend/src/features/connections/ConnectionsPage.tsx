import { useState } from "react"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/form"
import { ErrorState, Loading } from "@/components/ErrorState"
import { errorMessage } from "@/features/shared/client"
import type { Connection, TestResult } from "@/features/shared/types"
import { ConnectionForm } from "./ConnectionForm"
import { useConnections, useDeleteConnection, useTestSavedConnection } from "./api"

type Editing = { mode: "closed" } | { mode: "create" } | { mode: "edit"; connection: Connection }

export function ConnectionsPage() {
  const { data, error, isLoading, refetch } = useConnections()
  const remove = useDeleteConnection()
  const testSaved = useTestSavedConnection()
  const [editing, setEditing] = useState<Editing>({ mode: "closed" })
  const [results, setResults] = useState<Record<number, TestResult | string>>({})

  async function runTest(connection: Connection) {
    try {
      const result = await testSaved.mutateAsync(connection.id)
      setResults((r) => ({ ...r, [connection.id]: result }))
    } catch (e) {
      setResults((r) => ({ ...r, [connection.id]: errorMessage(e) }))
    }
  }

  async function runDelete(connection: Connection) {
    if (!window.confirm(`Hapus koneksi "${connection.name}"? Grant dan riwayat migration database ini ikut terhapus.`)) return
    await remove.mutateAsync(connection.id).catch(() => undefined)
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold">Koneksi database</h1>
          <p className="text-sm text-muted-foreground">Database target yang dikelola platform. Password tersimpan terenkripsi dan tidak pernah ditampilkan.</p>
        </div>
        {editing.mode === "closed" && <Button onClick={() => setEditing({ mode: "create" })}>Tambah koneksi</Button>}
      </div>

      {editing.mode !== "closed" && (
        <section className="rounded-xl border p-4">
          <h2 className="mb-3 font-medium">{editing.mode === "create" ? "Koneksi baru" : `Ubah ${editing.connection.name}`}</h2>
          <ConnectionForm
            key={editing.mode === "edit" ? editing.connection.id : "new"}
            existing={editing.mode === "edit" ? editing.connection : undefined}
            onDone={() => setEditing({ mode: "closed" })}
            onCancel={() => setEditing({ mode: "closed" })}
          />
        </section>
      )}

      {isLoading && <Loading />}
      {error && <ErrorState error={error} onRetry={() => void refetch()} />}
      {data && data.items.length === 0 && <p className="text-sm text-muted-foreground">Belum ada koneksi.</p>}
      {data && data.items.length > 0 && (
        <div className="overflow-x-auto rounded-xl border">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-left">
              <tr>
                <th className="px-3 py-2 font-medium">Nama</th>
                <th className="px-3 py-2 font-medium">DBMS</th>
                <th className="px-3 py-2 font-medium">Alamat</th>
                <th className="px-3 py-2 font-medium">Status</th>
                <th className="px-3 py-2 text-right font-medium">Aksi</th>
              </tr>
            </thead>
            <tbody>
              {data.items.map((c) => {
                const result = results[c.id]
                return (
                  <tr key={c.id} className="border-t align-top">
                    <td className="px-3 py-2 font-medium">{c.name}</td>
                    <td className="px-3 py-2">{c.kind === "postgres" ? "PostgreSQL" : "MySQL"}</td>
                    <td className="px-3 py-2 font-mono text-xs">
                      {c.username}@{c.host}:{c.port}/{c.databaseName}
                    </td>
                    <td className="px-3 py-2">
                      <div className="flex flex-col items-start gap-1">
                        <Badge tone={c.isActive ? "success" : "neutral"}>{c.isActive ? "Aktif" : "Nonaktif"}</Badge>
                        {result !== undefined && (
                          <span role="status" className={typeof result !== "string" && result.ok ? "text-xs text-emerald-600" : "text-xs text-destructive"}>
                            {typeof result === "string" ? result : result.ok ? `OK (${result.latencyMs} ms)` : result.detail || "Gagal"}
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="px-3 py-2">
                      <div className="flex justify-end gap-1">
                        <Button size="sm" variant="outline" disabled={testSaved.isPending} onClick={() => void runTest(c)}>
                          Test
                        </Button>
                        <Button size="sm" variant="outline" onClick={() => setEditing({ mode: "edit", connection: c })}>
                          Ubah
                        </Button>
                        <Button size="sm" variant="destructive" onClick={() => void runDelete(c)}>
                          Hapus
                        </Button>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
