import { useState, type FormEvent } from "react"
import { Link } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { Field, inputClass } from "@/components/ui/form"
import { ErrorState, Loading } from "@/components/ErrorState"
import { ApiError, errorMessage } from "@/features/shared/client"
import { useCreateGroup, useDeleteGroup, useGroups } from "./api"

export function GroupsPage() {
  const { data, error, isLoading, refetch } = useGroups()
  const create = useCreateGroup()
  const remove = useDeleteGroup()
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [formError, setFormError] = useState("")
  const [fields, setFields] = useState<Record<string, string>>({})

  async function submit(e: FormEvent) {
    e.preventDefault()
    setFormError("")
    setFields({})
    try {
      await create.mutateAsync({ name, description })
      setName("")
      setDescription("")
    } catch (err) {
      if (err instanceof ApiError) setFields(err.fields)
      setFormError(errorMessage(err))
    }
  }

  async function confirmDelete(id: number, groupName: string) {
    if (!window.confirm(`Hapus group "${groupName}"? Anggotanya kehilangan akses yang diberikan group ini.`)) return
    await remove.mutateAsync(id).catch((err) => setFormError(errorMessage(err)))
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-semibold">Groups</h1>
        <p className="text-sm text-muted-foreground">
          Akses efektif user = level tertinggi dari semua group-nya pada database tersebut (edit &gt; view).
        </p>
      </div>

      <form onSubmit={(e) => void submit(e)} className="grid gap-3 rounded-xl border p-4 sm:grid-cols-[1fr_2fr_auto] sm:items-end">
        <Field label="Nama group" htmlFor="group-name" error={fields.name}>
          <input id="group-name" className={inputClass} value={name} onChange={(e) => setName(e.target.value)} required />
        </Field>
        <Field label="Deskripsi" htmlFor="group-description" error={fields.description}>
          <input id="group-description" className={inputClass} value={description} onChange={(e) => setDescription(e.target.value)} />
        </Field>
        <Button type="submit" disabled={create.isPending}>
          Tambah group
        </Button>
        {formError && (
          <p role="alert" className="text-sm text-destructive sm:col-span-3">
            {formError}
          </p>
        )}
      </form>

      {isLoading && <Loading />}
      {error && <ErrorState error={error} onRetry={() => void refetch()} />}
      {data && data.items.length === 0 && <p className="text-sm text-muted-foreground">Belum ada group.</p>}
      {data && data.items.length > 0 && (
        <ul className="divide-y rounded-xl border">
          {data.items.map((g) => (
            <li key={g.id} className="flex items-center justify-between px-3 py-2 text-sm">
              <div>
                <Link to={`/admin/groups/${g.id}`} className="font-medium hover:underline">
                  {g.name}
                </Link>
                {g.description && <p className="text-muted-foreground">{g.description}</p>}
              </div>
              <Button size="sm" variant="destructive" onClick={() => void confirmDelete(g.id, g.name)}>
                Hapus
              </Button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
