import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { beforeEach, describe, expect, it, vi } from "vitest"
import type { Migration, MigrationApprovals, MigrationExecution, MigrationRequest } from "@/features/shared/types"
import { server } from "@/mocks/server"
import { renderRoute } from "@/test/utils"
import { HistoryPage } from "./HistoryPage"
import { MigrationDetailPage } from "./MigrationDetailPage"
import { MigrationNewPage, MigrationsPage } from "./MigrationsPage"

const stamp = "2026-11-23T00:00:00Z"
const draft: Migration = {
  id: 5,
  databaseId: 1,
  title: "Buat t",
  description: "",
  status: "draft",
  position: "draft",
  revision: 1,
  upSql: "CREATE TABLE t (id int)",
  downSql: "DROP TABLE t",
  irreversible: false,
  irreversibleReason: "",
  sqlLocked: false,
  createdByName: "alice",
  createdAt: stamp,
  updatedAt: stamp,
  actions: ["delete", "edit", "explain", "submit"],
}
const request: MigrationRequest = { id: 3, kind: "apply", status: "pending_review", revision: 1, requestedByName: "alice", createdAt: stamp }
const pending: Migration = { ...draft, status: "pending_review", request, actions: ["explain"] }
const approvedMigration: Migration = {
  ...draft,
  status: "approved",
  request: { ...request, status: "approved" },
  executableRequestId: 3,
  actions: ["execute", "explain"],
}
const applied: Migration = { ...draft, status: "succeeded", position: "applied", version: 1, sqlLocked: true, actions: ["rollback"] }
const approvals: MigrationApprovals = {
  request,
  canDecide: true,
  groups: [
    { groupId: 1, groupName: "backend", leaderName: "alice", decision: "pending", comment: "" },
    { groupId: 2, groupName: "analyst", leaderName: "bob", decision: "approved", decidedByName: "bob", decidedAt: stamp, comment: "aman" },
    { groupId: 3, groupName: "qa", decision: "pending", comment: "" },
  ],
}

const problem = (status: number, code: string, detail?: string, errors?: Record<string, string>) =>
  HttpResponse.json({ title: code, status, code, detail, errors }, { status })

beforeEach(() => {
  server.use(
    http.get("/api/databases/:id/migration-history", () => HttpResponse.json([])),
    http.get("/api/migrations/:id/approvals", () => HttpResponse.json(approvals)),
  )
})

function detail(m: Migration | (() => Migration)) {
  server.use(http.get("/api/migrations/:id", () => HttpResponse.json(typeof m === "function" ? m() : m)))
  renderRoute(<MigrationDetailPage />, { path: "/databases/:databaseId/migrations/:migrationId", url: "/databases/1/migrations/5" })
}

describe("MigrationsPage", () => {
  const show = (id: number) => renderRoute(<MigrationsPage />, { path: "/databases/:databaseId/migrations", url: `/databases/${id}/migrations` })

  it("menampilkan status dan posisi, dan tombol baru untuk View + Edit", async () => {
    server.use(http.get("/api/databases/:id/migrations", () => HttpResponse.json([draft, { ...applied, id: 6, title: "Kedua" }])))
    show(1)

    expect(await screen.findByRole("link", { name: "Buat t" })).toHaveAttribute("href", "/databases/1/migrations/5")
    expect(screen.getByText("Success")).toBeInTheDocument()
    expect(screen.getByText("versi 1")).toBeInTheDocument()
    expect(await screen.findByRole("link", { name: "Migration baru" })).toHaveAttribute("href", "/databases/1/migrations/new")
  })

  it("menyembunyikan tombol baru untuk View Only", async () => {
    server.use(http.get("/api/databases/:id/migrations", () => HttpResponse.json([])))
    show(2)

    expect(await screen.findByText("Belum ada migration.")).toBeInTheDocument()
    expect(screen.queryByRole("link", { name: "Migration baru" })).not.toBeInTheDocument()
  })

  it("menampilkan error jika akses ditolak", async () => {
    server.use(http.get("/api/databases/:id/migrations", () => problem(403, "FORBIDDEN")))
    show(1)

    expect(await screen.findByText("Anda tidak punya akses untuk aksi ini")).toBeInTheDocument()
  })
})

describe("MigrationNewPage", () => {
  const show = (id: number) => renderRoute(<MigrationNewPage />, { path: "/databases/:databaseId/migrations/new", url: `/databases/${id}/migrations/new` })

  it("tidak bisa disimpan sebelum SQL up diisi", async () => {
    show(1)
    expect(await screen.findByRole("button", { name: "Simpan draft" })).toBeDisabled()
  })

  it("menampilkan input batch dan menyembunyikan SQL down jika irreversible", async () => {
    const user = userEvent.setup()
    show(1)
    await user.click(await screen.findByLabelText("Jalankan per batch"))
    for (const label of ["Schema", "Tabel", "Kolom batch", "Ukuran batch"]) expect(screen.getByLabelText(label)).toBeInTheDocument()
    await user.click(screen.getByLabelText("Irreversible (tidak punya SQL down)"))
    expect(screen.getByLabelText("Alasan irreversible")).toBeInTheDocument()
    expect(screen.queryByText("SQL down")).not.toBeInTheDocument()
  })

  it("ditolak untuk View Only", async () => {
    show(2)
    expect(await screen.findByText("Membuat migration butuh akses View + Edit.")).toBeInTheDocument()
  })
})

describe("MigrationDetailPage", () => {
  it("mengajukan draft ke review", async () => {
    const user = userEvent.setup()
    let submitted = false
    server.use(
      http.post("/api/migrations/:id/submit", () => {
        submitted = true
        return HttpResponse.json(pending, { status: 201 })
      }),
    )
    detail(() => (submitted ? pending : draft))

    await user.click(await screen.findByRole("button", { name: "Ajukan review" }))
    expect((await screen.findAllByText("Pending Review")).length).toBeGreaterThan(0)
    expect(screen.queryByRole("button", { name: "Ajukan review" })).not.toBeInTheDocument()
  })

  it("menampilkan penolakan aturan dari backend", async () => {
    const user = userEvent.setup()
    server.use(http.post("/api/migrations/:id/rollback", () => problem(409, "CONFLICT", "rollback hanya untuk migration aktif terakhir")))
    detail(applied)

    await user.click(await screen.findByRole("button", { name: "Ajukan rollback" }))
    expect(await screen.findByRole("alert")).toHaveTextContent("rollback hanya untuk migration aktif terakhir")
  })

  it("leader menyetujui request dengan komentar", async () => {
    const user = userEvent.setup()
    let body: unknown = null
    server.use(
      http.post("/api/migrations/:id/approvals", async ({ request: r }) => {
        body = await r.json()
        return HttpResponse.json(approvedMigration)
      }),
    )
    detail(pending)

    const panel = await screen.findByRole("region", { name: "Approval" })
    expect(within(panel).getByText("leader alice")).toBeInTheDocument()
    expect(within(panel).getByText("belum punya leader")).toBeInTheDocument()
    expect(within(panel).getByText("“aman”")).toBeInTheDocument()
    await user.type(within(panel).getByLabelText("Komentar"), "ok")
    await user.click(within(panel).getByRole("button", { name: "Setujui" }))
    await waitFor(() => expect(body).toEqual({ requestId: 3, decision: "approved", comment: "ok" }))
  })

  it("keputusan memakai request yang sedang ditampilkan panel", async () => {
    const user = userEvent.setup()
    let body: unknown = null
    server.use(
      http.get("/api/migrations/:id/approvals", () => HttpResponse.json({ ...approvals, request: { ...request, id: 4 } })),
      http.post("/api/migrations/:id/approvals", async ({ request: r }) => {
        body = await r.json()
        return HttpResponse.json(pending)
      }),
    )
    detail(pending)

    await user.click(await screen.findByRole("button", { name: "Setujui" }))
    await waitFor(() => expect(body).toMatchObject({ requestId: 4 }))
  })

  it("menolak request dan menampilkan error akses", async () => {
    const user = userEvent.setup()
    server.use(http.post("/api/migrations/:id/approvals", () => problem(403, "FORBIDDEN")))
    detail(pending)

    await user.click(await screen.findByRole("button", { name: "Tolak" }))
    expect(await screen.findByText("Anda tidak punya akses untuk aksi ini")).toBeInTheDocument()
  })

  it("bukan leader tidak melihat tombol keputusan", async () => {
    server.use(http.get("/api/migrations/:id/approvals", () => HttpResponse.json({ ...approvals, canDecide: false })))
    detail(pending)

    expect(await screen.findByText("leader alice")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Setujui" })).not.toBeInTheDocument()
  })

  it("Execute mengirim request yang disetujui lalu memantau job sampai selesai", async () => {
    const user = userEvent.setup()
    let body: unknown = null
    let executed = false
    let polls = 0
    server.use(
      http.post("/api/migrations/:id/execute", async ({ request: r }) => {
        body = await r.json()
        executed = true
        return HttpResponse.json({ jobId: 9 }, { status: 202 })
      }),
      http.get("/api/databases/1/jobs/9", () => {
        polls++
        return HttpResponse.json({
          id: 9, requestId: 3, databaseId: 1, kind: "apply", status: "pending", attempts: 0, maxAttempts: 3,
          batchesCommitted: 0, rowsCommitted: 0, createdAt: stamp,
        })
      }),
    )
    detail(() => (executed ? { ...approvedMigration, status: "queued", jobId: 9, executableRequestId: undefined, actions: [] } : approvedMigration))

    await user.click(await screen.findByRole("button", { name: "Execute" }))
    expect(await screen.findByText("Dalam antrean")).toBeInTheDocument()
    expect(screen.getAllByText("Pending (Queue)").length).toBeGreaterThan(0)
    expect(body).toEqual({ requestId: 3 })
    expect(polls).toBeGreaterThan(0)
    expect(screen.queryByRole("button", { name: "Execute" })).not.toBeInTheDocument()
  })

  it("status diperbarui selama job berjalan walaupun job selesai sebelum dipantau", async () => {
    let calls = 0
    const job = { id: 9, requestId: 3, databaseId: 1, kind: "apply", status: "succeeded", attempts: 1, maxAttempts: 3, batchesCommitted: 0, rowsCommitted: 0, createdAt: stamp }
    server.use(http.get("/api/databases/1/jobs/9", () => HttpResponse.json(job)))
    detail(() => (calls++ === 0 ? { ...pending, status: "queued", jobId: 9 } : { ...applied, jobId: 9 }))

    expect(await screen.findByText("Pending (Queue)")).toBeInTheDocument()
    expect(await screen.findByText("Aktif", undefined, { timeout: 4000 })).toBeInTheDocument()
  })

  it("migration partial bisa dilanjutkan atau di-rollback", async () => {
    detail({ ...draft, status: "partial", executableRequestId: 3, actions: ["execute", "rollback"] })

    expect(await screen.findByRole("button", { name: "Lanjutkan dari checkpoint" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Ajukan rollback" })).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Ubah" })).not.toBeInTheDocument()
  })

  it("pengguna View Only hanya melihat", async () => {
    detail({ ...draft, actions: [] })

    expect(await screen.findByText("CREATE TABLE t (id int)")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /Ajukan|Execute|Ubah|Hapus/ })).not.toBeInTheDocument()
  })

  it("mengirim revisi saat menyimpan perubahan dan menampilkan error per field", async () => {
    const user = userEvent.setup()
    let body: Record<string, unknown> = {}
    server.use(
      http.put("/api/migrations/:id", async ({ request: r }) => {
        body = (await r.json()) as Record<string, unknown>
        return problem(422, "VALIDATION_FAILED", "input tidak valid", { downSql: "wajib diisi" })
      }),
    )
    detail({ ...draft, revision: 4 })

    await user.click(await screen.findByRole("button", { name: "Ubah" }))
    await user.click(screen.getByRole("button", { name: "Simpan perubahan" }))
    expect(await screen.findByText("wajib diisi")).toBeInTheDocument()
    expect(body.revision).toBe(4)
    await user.click(screen.getByRole("button", { name: "Batal" }))
    expect(screen.getByRole("button", { name: "Ubah" })).toBeInTheDocument()
  })

  it("menghapus draft setelah konfirmasi", async () => {
    const user = userEvent.setup()
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true)
    let removed = false
    server.use(
      http.delete("/api/migrations/:id", () => {
        removed = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    detail(draft)

    await user.click(await screen.findByRole("button", { name: "Hapus" }))
    await waitFor(() => expect(removed).toBe(true))
    confirm.mockRestore()
  })

  it("menampilkan execution plan", async () => {
    const user = userEvent.setup()
    server.use(
      http.post("/api/migrations/:id/explain", () =>
        HttpResponse.json([{ index: 1, sql: "SELECT 1", explainable: true, plan: [{ Plan: { "Node Type": "Result" } }] }]),
      ),
    )
    detail(draft)

    await user.click(await screen.findByRole("button", { name: "Jalankan EXPLAIN" }))
    expect(await screen.findByText(/"Node Type": "Result"/)).toBeInTheDocument()
  })
})

describe("HistoryPage", () => {
  const base: MigrationExecution = {
    id: 1, migrationId: 5, migrationTitle: "Buat t", version: 1, revision: 2, kind: "apply", status: "succeeded", attempt: 1,
    executedBy: 1, executedByName: "alice", executedSql: "CREATE TABLE t ()", startedAt: stamp, finishedAt: stamp,
  }
  const rows: MigrationExecution[] = [
    { ...base, id: 3, kind: "rollback", revertsExecutionId: 1, executedSql: "DROP TABLE t" },
    { ...base, id: 2, status: "failed", errorMessage: "relation exists", errorCode: "42P07" },
    base,
  ]
  const show = () => renderRoute(<HistoryPage />, { path: "/databases/:databaseId/history", url: "/databases/1/history" })

  it("menampilkan semua attempt dan meminta hideRollback ke API saat dicentang", async () => {
    const user = userEvent.setup()
    const seen: (string | null)[] = []
    server.use(
      http.get("/api/databases/:id/migration-history", ({ request: r }) => {
        const hide = new URL(r.url).searchParams.get("hideRollback")
        seen.push(hide)
        return HttpResponse.json(hide === "true" ? [rows[1]] : rows)
      }),
    )
    show()

    expect(await screen.findByText(/relation exists/)).toBeInTheDocument()
    expect(screen.getByText(/42P07/)).toBeInTheDocument()
    expect(screen.getByText("Membatalkan #1")).toBeInTheDocument()
    expect(screen.getByText("DROP TABLE t")).toBeInTheDocument()

    await user.click(screen.getByLabelText("Hide rollback"))
    await waitFor(() => expect(screen.queryByText("Membatalkan #1")).not.toBeInTheDocument())
    expect(seen).toContain("true")
    expect(screen.getByText(/relation exists/)).toBeInTheDocument()
  })

  it("menampilkan keadaan kosong", async () => {
    show()
    expect(await screen.findByText("Belum ada eksekusi.")).toBeInTheDocument()
  })
})
