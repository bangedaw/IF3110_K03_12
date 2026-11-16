import { useState, type FormEvent } from "react"
import { Button } from "@/components/ui/button"
import { Field, inputClass } from "@/components/ui/form"
import { ApiError, errorMessage } from "@/features/shared/client"
import type { Connection, ConnectionInput, DbKind, TestResult } from "@/features/shared/types"
import { useCreateConnection, useTestConnection, useUpdateConnection } from "./api"

const DEFAULT_PORT: Record<DbKind, number> = { postgres: 5432, mysql: 3306 }
type SslMode = NonNullable<ConnectionInput["sslMode"]>

const SSL_MODES: SslMode[] = ["disable", "require", "verify-ca", "verify-full"]

interface FormState {
  name: string
  kind: DbKind
  host: string
  port: string
  databaseName: string
  username: string
  password: string
  sslMode: SslMode
  isActive: boolean
}

function initialState(existing?: Connection): FormState {
  if (!existing) {
    return {
      name: "",
      kind: "postgres",
      host: "",
      port: String(DEFAULT_PORT.postgres),
      databaseName: "",
      username: "",
      password: "",
      sslMode: "disable",
      isActive: true,
    }
  }
  return {
    name: existing.name,
    kind: existing.kind,
    host: existing.host,
    port: String(existing.port),
    databaseName: existing.databaseName,
    username: existing.username,
    password: "",
    sslMode: existing.sslMode,
    isActive: existing.isActive,
  }
}

function validate(state: FormState, editing: boolean): Record<string, string> {
  const errors: Record<string, string> = {}
  if (!state.name.trim()) errors.name = "Wajib diisi"
  if (!state.host.trim()) errors.host = "Wajib diisi"
  const port = Number(state.port)
  if (!Number.isInteger(port) || port < 1 || port > 65535) errors.port = "Harus antara 1 dan 65535"
  if (!state.databaseName.trim()) errors.databaseName = "Wajib diisi"
  if (!state.username.trim()) errors.username = "Wajib diisi"
  if (!editing && !state.password) errors.password = "Wajib diisi"
  return errors
}

function toInput(state: FormState): ConnectionInput {
  return {
    name: state.name.trim(),
    kind: state.kind,
    host: state.host.trim(),
    port: Number(state.port),
    databaseName: state.databaseName.trim(),
    username: state.username.trim(),
    password: state.password,
    sslMode: state.sslMode,
    isActive: state.isActive,
  }
}

export function ConnectionForm({
  existing,
  onDone,
  onCancel,
}: {
  existing?: Connection
  onDone: () => void
  onCancel: () => void
}) {
  const editing = existing !== undefined
  const [state, setState] = useState(() => initialState(existing))
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [formError, setFormError] = useState("")
  const [testResult, setTestResult] = useState<TestResult | null>(null)
  const create = useCreateConnection()
  const update = useUpdateConnection()
  const test = useTestConnection()

  function set<K extends keyof FormState>(key: K, value: FormState[K]) {
    setState((s) => ({ ...s, [key]: value }))
  }

  function changeKind(kind: DbKind) {
    setState((s) => {
      const portIsDefault = Number(s.port) === DEFAULT_PORT[s.kind]
      return { ...s, kind, port: portIsDefault ? String(DEFAULT_PORT[kind]) : s.port }
    })
  }

  function applyServerError(error: unknown) {
    if (error instanceof ApiError && Object.keys(error.fields).length > 0) {
      setErrors(error.fields)
      setFormError("")
      return
    }
    setFormError(errorMessage(error))
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    setFormError("")
    const found = validate(state, editing)
    setErrors(found)
    if (Object.keys(found).length > 0) return
    const input = toInput(state)
    try {
      if (existing) {
        const patch: Partial<ConnectionInput> = { ...input }
        if (!state.password) delete patch.password
        await update.mutateAsync({ id: existing.id, input: patch })
      } else {
        await create.mutateAsync(input)
      }
      onDone()
    } catch (error) {
      applyServerError(error)
    }
  }

  async function runTest() {
    setFormError("")
    setTestResult(null)
    const found = validate(state, editing)
    setErrors(found)
    if (Object.keys(found).length > 0) return
    try {
      setTestResult(await test.mutateAsync({ input: toInput(state), databaseId: existing?.id }))
    } catch (error) {
      applyServerError(error)
    }
  }

  const busy = create.isPending || update.isPending

  return (
    <form onSubmit={submit} noValidate aria-label={editing ? "Ubah koneksi" : "Tambah koneksi"} className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Nama" htmlFor="conn-name" error={errors.name}>
          <input id="conn-name" className={inputClass} value={state.name} aria-invalid={!!errors.name} onChange={(e) => set("name", e.target.value)} />
        </Field>
        <Field label="DBMS" htmlFor="conn-kind" error={errors.kind}>
          <select id="conn-kind" className={inputClass} value={state.kind} onChange={(e) => changeKind(e.target.value as DbKind)}>
            <option value="postgres">PostgreSQL</option>
            <option value="mysql">MySQL</option>
          </select>
        </Field>
        <Field label="Host" htmlFor="conn-host" error={errors.host}>
          <input id="conn-host" className={inputClass} value={state.host} aria-invalid={!!errors.host} onChange={(e) => set("host", e.target.value)} />
        </Field>
        <Field label="Port" htmlFor="conn-port" error={errors.port}>
          <input id="conn-port" inputMode="numeric" className={inputClass} value={state.port} aria-invalid={!!errors.port} onChange={(e) => set("port", e.target.value)} />
        </Field>
        <Field label="Nama database" htmlFor="conn-db" error={errors.databaseName}>
          <input id="conn-db" className={inputClass} value={state.databaseName} aria-invalid={!!errors.databaseName} onChange={(e) => set("databaseName", e.target.value)} />
        </Field>
        <Field label="Username" htmlFor="conn-user" error={errors.username}>
          <input id="conn-user" autoComplete="off" className={inputClass} value={state.username} aria-invalid={!!errors.username} onChange={(e) => set("username", e.target.value)} />
        </Field>
        <Field
          label="Password"
          htmlFor="conn-password"
          error={errors.password}
          hint={editing ? "Kosongkan jika tidak ingin mengubah password tersimpan" : undefined}
        >
          <input id="conn-password" type="password" autoComplete="new-password" className={inputClass} value={state.password} aria-invalid={!!errors.password} onChange={(e) => set("password", e.target.value)} />
        </Field>
        <Field label="SSL mode" htmlFor="conn-ssl" error={errors.sslMode}>
          <select id="conn-ssl" className={inputClass} value={state.sslMode} onChange={(e) => set("sslMode", e.target.value as SslMode)}>
            {SSL_MODES.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        </Field>
      </div>

      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={state.isActive} onChange={(e) => set("isActive", e.target.checked)} />
        Koneksi aktif
      </label>

      {formError && (
        <p role="alert" className="text-sm text-destructive">
          {formError}
        </p>
      )}
      {testResult && (
        <p role="status" className={testResult.ok ? "text-sm text-emerald-600" : "text-sm text-destructive"}>
          {testResult.ok ? `Koneksi berhasil (${testResult.latencyMs} ms)` : testResult.detail || "Koneksi gagal"}
        </p>
      )}

      <div className="flex flex-wrap gap-2">
        <Button type="submit" disabled={busy}>
          {busy ? "Menyimpan…" : "Simpan"}
        </Button>
        <Button type="button" variant="outline" disabled={test.isPending} onClick={runTest}>
          {test.isPending ? "Menguji…" : "Test koneksi"}
        </Button>
        <Button type="button" variant="ghost" onClick={onCancel}>
          Batal
        </Button>
      </div>
    </form>
  )
}
