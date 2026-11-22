import { useState } from "react"
import { Link, useParams } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { inputClass } from "@/components/ui/form"
import { ErrorState, Loading } from "@/components/ErrorState"
import { errorMessage } from "@/features/shared/client"
import { useConnections } from "@/features/connections/api"
import { useGrants, useGroup, useSetGrant, useSetLeader, useSetMember, useUsers, type AccessLevel } from "./api"

export function GroupDetailPage() {
  const groupId = Number(useParams().groupId)
  const group = useGroup(groupId)
  const grants = useGrants(groupId)
  const users = useUsers()
  const connections = useConnections()
  const setMember = useSetMember(groupId)
  const setGrant = useSetGrant(groupId)
  const setLeader = useSetLeader(groupId)
  const [newMember, setNewMember] = useState("")
  const [actionError, setActionError] = useState("")

  async function run(action: () => Promise<unknown>) {
    setActionError("")
    await action().catch((err) => setActionError(errorMessage(err)))
  }

  if (group.isLoading) return <Loading />
  if (group.error) return <ErrorState error={group.error} onRetry={() => void group.refetch()} />
  if (!group.data) return null

  const memberIds = new Set(group.data.members.map((m) => m.id))
  const candidates = users.data?.items.filter((u) => !memberIds.has(u.id)) ?? []
  const levels = new Map(grants.data?.items.map((g) => [g.databaseId, g.level]))

  return (
    <div className="space-y-6">
      <div>
        <Link to="/admin/groups" className="text-sm text-muted-foreground hover:underline">
          ← Groups
        </Link>
        <h1 className="text-xl font-semibold">{group.data.name}</h1>
        {group.data.description && <p className="text-sm text-muted-foreground">{group.data.description}</p>}
        {group.data.leaderUsername ? (
          <p className="text-sm">
            Leader: <span className="font-medium">{group.data.leaderUsername}</span>
          </p>
        ) : (
          <p role="status" className="rounded-lg border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive">
            Group ini belum punya leader. Migration pada database yang bisa diakses group ini tertahan di Pending Review sampai leader
            ditunjuk dari daftar anggota di bawah.
          </p>
        )}
      </div>

      {actionError && (
        <p role="alert" className="text-sm text-destructive">
          {actionError}
        </p>
      )}

      <section className="space-y-2">
        <h2 className="font-medium">Anggota</h2>
        <div className="flex gap-2">
          <select
            aria-label="Pilih user"
            className={inputClass + " max-w-xs"}
            value={newMember}
            onChange={(e) => setNewMember(e.target.value)}
          >
            <option value="">Pilih user…</option>
            {candidates.map((u) => (
              <option key={u.id} value={u.id}>
                {u.username}
              </option>
            ))}
          </select>
          <Button
            disabled={!newMember || setMember.isPending}
            onClick={() => void run(() => setMember.mutateAsync({ userId: Number(newMember), member: true })).then(() => setNewMember(""))}
          >
            Tambah anggota
          </Button>
        </div>
        {group.data.members.length === 0 ? (
          <p className="text-sm text-muted-foreground">Belum ada anggota.</p>
        ) : (
          <ul className="divide-y rounded-xl border">
            {group.data.members.map((m) => {
              const isLeader = m.id === group.data.leaderId
              return (
                <li key={m.id} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2 text-sm">
                  <span>
                    <span className="font-medium">{m.username}</span> {m.displayName && <span className="text-muted-foreground">({m.displayName})</span>}
                    {isLeader && <span className="ml-2 rounded-md bg-primary/10 px-1.5 py-0.5 text-xs font-medium text-primary">Leader</span>}
                    {!m.isActive && <span className="ml-2 text-xs text-muted-foreground">nonaktif</span>}
                  </span>
                  <span className="flex gap-2">
                    {!isLeader && m.isActive && (
                      <Button size="sm" variant="outline" disabled={setLeader.isPending} onClick={() => void run(() => setLeader.mutateAsync(m.id))}>
                        Jadikan leader
                      </Button>
                    )}
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={isLeader}
                      title={isLeader ? "Tunjuk leader lain sebelum mengeluarkan leader" : undefined}
                      onClick={() => void run(() => setMember.mutateAsync({ userId: m.id, member: false }))}
                    >
                      Keluarkan
                    </Button>
                  </span>
                </li>
              )
            })}
          </ul>
        )}
      </section>

      <section className="space-y-2">
        <h2 className="font-medium">Akses database</h2>
        {(grants.isLoading || connections.isLoading) && <Loading />}
        {grants.error && <ErrorState error={grants.error} />}
        {connections.error && <ErrorState error={connections.error} />}
        {connections.data && (
          <table className="w-full rounded-xl border text-sm">
            <thead className="bg-muted/50 text-left">
              <tr>
                <th className="px-3 py-2 font-medium">Database</th>
                <th className="px-3 py-2 font-medium">Akses</th>
              </tr>
            </thead>
            <tbody>
              {connections.data.items.map((c) => (
                <tr key={c.id} className="border-t">
                  <td className="px-3 py-2">{c.name}</td>
                  <td className="px-3 py-2">
                    <select
                      aria-label={`Akses ${c.name}`}
                      className={inputClass + " max-w-40"}
                      value={levels.get(c.id) ?? ""}
                      disabled={setGrant.isPending}
                      onChange={(e) => void run(() => setGrant.mutateAsync({ databaseId: c.id, level: e.target.value as AccessLevel | "" }))}
                    >
                      <option value="">Tidak ada</option>
                      <option value="view">View Only</option>
                      <option value="edit">View + Edit</option>
                    </select>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  )
}
