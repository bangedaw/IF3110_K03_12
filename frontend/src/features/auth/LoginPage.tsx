import { useState, type FormEvent } from "react"
import { Navigate, useLocation, useNavigate } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { Field, inputClass } from "@/components/ui/form"
import { ApiError, errorMessage } from "@/features/shared/client"
import { useAuth } from "./AuthProvider"

export function LoginPage() {
  const { user, expired, login } = useAuth()
  const navigate = useNavigate()
  const from = (useLocation().state as { from?: string } | null)?.from ?? "/"
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)

  if (user) return <Navigate to={from} replace />

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError("")
    setBusy(true)
    try {
      await login(username, password)
      navigate(from, { replace: true })
    } catch (err) {
      setError(err instanceof ApiError ? err.message : errorMessage(err))
      setPassword("")
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="flex min-h-screen items-center justify-center p-4">
      <form onSubmit={(e) => void submit(e)} className="w-full max-w-sm space-y-4 rounded-xl border p-6">
        <h1 className="text-xl font-semibold">Database Management Platform</h1>
        {expired && (
          <p role="status" className="rounded-lg bg-amber-500/15 p-2 text-sm text-amber-700">
            Sesi Anda berakhir, silakan login kembali.
          </p>
        )}
        <Field label="Username" htmlFor="username">
          <input id="username" className={inputClass} autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} required />
        </Field>
        <Field label="Password" htmlFor="password">
          <input
            id="password"
            type="password"
            className={inputClass}
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </Field>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        <Button type="submit" className="w-full" disabled={busy}>
          {busy ? "Masuk…" : "Masuk"}
        </Button>
      </form>
    </main>
  )
}
