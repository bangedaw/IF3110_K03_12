import { useState, type FormEvent } from "react"
import { Button } from "@/components/ui/button"
import { Badge, Field, inputClass } from "@/components/ui/form"
import { ErrorState, Loading } from "@/components/ErrorState"
import { ApiError, errorMessage } from "@/features/shared/client"
import { useAuth, type User } from "@/features/auth/AuthProvider"
import { useCreateUser, useDeleteUser, useResetPassword, useRevokeSessions, useUpdateUser, useUsers } from "./api"

export function UsersPage() {
  const { user: me } = useAuth()
  const { data, error, isLoading, refetch } = useUsers()
  const update = useUpdateUser()
  const remove = useDeleteUser()
  const revoke = useRevokeSessions()
  const [creating, setCreating] = useState(false)
  const [resetting, setResetting] = useState<number | null>(null)
  const [notice, setNotice] = useState("")
  const [actionError, setActionError] = useState("")

  async function run(action: () => Promise<unknown>, success: string) {
    setNotice("")
    setActionError("")
    try {
      await action()
      setNotice(success)
    } catch (e) {
      setActionError(errorMessage(e))
    }
  }

  function confirmDelete(u: User) {
    if (!window.confirm(`Hapus user "${u.username}"?`)) return
    void run(() => remove.mutateAsync(u.id), `User ${u.username} dihapus`)
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold">Users</h1>
          <p className="text-sm text-muted-foreground">Akses database diberikan lewat group, bukan langsung ke user.</p>
        </div>
        {!creating && <Button onClick={() => setCreating(true)}>Tambah user</Button>}
      </div>

      {creating && <CreateUserForm onDone={() => setCreating(false)} />}

      {notice && (
        <p role="status" className="text-sm text-emerald-600">
          {notice}
        </p>
      )}
      {actionError && (
        <p role="alert" className="text-sm text-destructive">
          {actionError}
        </p>
      )}

      {isLoading && <Loading />}
      {error && <ErrorState error={error} onRetry={() => void refetch()} />}
      {data && (
        <div className="overflow-x-auto rounded-xl border">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-left">
              <tr>
                <th className="px-3 py-2 font-medium">Username</th>
                <th className="px-3 py-2 font-medium">Nama</th>
                <th className="px-3 py-2 font-medium">Status</th>
                <th className="px-3 py-2 text-right font-medium">Aksi</th>
              </tr>
            </thead>
            <tbody>
              {data.items.map((u) => {
                const self = u.id === me?.id
                return (
                  <tr key={u.id} className="border-t align-top">
                    <td className="px-3 py-2 font-medium">{u.username}</td>
                    <td className="px-3 py-2">{u.displayName}</td>
                    <td className="px-3 py-2">
                      <div className="flex gap-1">
                        <Badge tone={u.isActive ? "success" : "neutral"}>{u.isActive ? "Aktif" : "Nonaktif"}</Badge>
                        {u.isPlatformAdmin && <Badge tone="warning">Admin</Badge>}
                      </div>
                    </td>
                    <td className="px-3 py-2">
                      <div className="flex flex-wrap justify-end gap-1">
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={self}
                          onClick={() => void run(() => update.mutateAsync({ id: u.id, body: { isActive: !u.isActive } }), `Status ${u.username} diperbarui`)}
                        >
                          {u.isActive ? "Nonaktifkan" : "Aktifkan"}
                        </Button>
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={self}
                          onClick={() =>
                            void run(() => update.mutateAsync({ id: u.id, body: { isPlatformAdmin: !u.isPlatformAdmin } }), `Peran ${u.username} diperbarui`)
                          }
                        >
                          {u.isPlatformAdmin ? "Cabut admin" : "Jadikan admin"}
                        </Button>
                        <Button size="sm" variant="outline" onClick={() => setResetting(resetting === u.id ? null : u.id)}>
                          Reset password
                        </Button>
                        <Button size="sm" variant="outline" onClick={() => void run(() => revoke.mutateAsync(u.id), `Semua sesi ${u.username} dicabut`)}>
                          Cabut sesi
                        </Button>
                        <Button size="sm" variant="destructive" disabled={self} onClick={() => confirmDelete(u)}>
                          Hapus
                        </Button>
                      </div>
                      {resetting === u.id && (
                        <ResetPasswordForm
                          user={u}
                          onDone={(msg) => {
                            setResetting(null)
                            setNotice(msg)
                          }}
                        />
                      )}
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

function CreateUserForm({ onDone }: { onDone: () => void }) {
  const create = useCreateUser()
  const [username, setUsername] = useState("")
  const [displayName, setDisplayName] = useState("")
  const [password, setPassword] = useState("")
  const [isPlatformAdmin, setAdmin] = useState(false)
  const [fields, setFields] = useState<Record<string, string>>({})
  const [error, setError] = useState("")

  async function submit(e: FormEvent) {
    e.preventDefault()
    setFields({})
    setError("")
    try {
      await create.mutateAsync({ username, displayName, password, isPlatformAdmin })
      onDone()
    } catch (err) {
      if (err instanceof ApiError) setFields(err.fields)
      setError(errorMessage(err))
    }
  }

  return (
    <form onSubmit={(e) => void submit(e)} className="grid gap-3 rounded-xl border p-4 sm:grid-cols-2">
      <Field label="Username" htmlFor="new-username" error={fields.username}>
        <input id="new-username" className={inputClass} value={username} onChange={(e) => setUsername(e.target.value)} required />
      </Field>
      <Field label="Nama tampilan" htmlFor="new-display-name" error={fields.displayName}>
        <input id="new-display-name" className={inputClass} value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
      </Field>
      <Field label="Password" htmlFor="new-password" error={fields.password} hint="Minimal 8 karakter">
        <input
          id="new-password"
          type="password"
          autoComplete="new-password"
          className={inputClass}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
        />
      </Field>
      <label className="flex items-center gap-2 self-end text-sm">
        <input type="checkbox" checked={isPlatformAdmin} onChange={(e) => setAdmin(e.target.checked)} />
        Platform Admin
      </label>
      {error && (
        <p role="alert" className="text-sm text-destructive sm:col-span-2">
          {error}
        </p>
      )}
      <div className="flex gap-2 sm:col-span-2">
        <Button type="submit" disabled={create.isPending}>
          Simpan
        </Button>
        <Button type="button" variant="outline" onClick={onDone}>
          Batal
        </Button>
      </div>
    </form>
  )
}

function ResetPasswordForm({ user, onDone }: { user: User; onDone: (msg: string) => void }) {
  const reset = useResetPassword()
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError("")
    try {
      await reset.mutateAsync({ id: user.id, password })
      onDone(`Password ${user.username} direset dan semua sesinya dicabut`)
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <form onSubmit={(e) => void submit(e)} className="mt-2 flex items-start justify-end gap-2">
      <Field label={`Password baru untuk ${user.username}`} htmlFor={`reset-${user.id}`} error={error}>
        <input
          id={`reset-${user.id}`}
          type="password"
          autoComplete="new-password"
          className={inputClass}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
        />
      </Field>
      <Button type="submit" size="sm" className="mt-6" disabled={reset.isPending}>
        Simpan
      </Button>
    </form>
  )
}
