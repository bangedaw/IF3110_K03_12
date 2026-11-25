import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { AuthProvider } from "@/features/auth/AuthProvider"
import { server } from "@/mocks/server"
import { renderRoute } from "@/test/utils"
import { HistoryPage } from "./HistoryPage"
import { MigrationDetailPage } from "./MigrationDetailPage"
import { MigrationNewPage, MigrationsPage } from "./MigrationsPage"

const stamp = "2026-11-23T00:00:00Z"
const base = { description: "", revision: 1, seq: 1, upSql: "CREATE TABLE t (id int)", downSql: "DROP TABLE t", irreversible: false, irreversibleReason: "", sqlLocked: false, databaseId: 1, createdBy: 1, updatedBy: 1, createdAt: stamp, updatedAt: stamp }
const draft = { ...base, id: 5, title: "Buat t", status: "draft" }
const pending = { ...draft, status: "pending_review" }
const done = { ...draft, status: "success", sqlLocked: true }
const leader = { id: 7, username: "lead", displayName: "Lead", isActive: true, isPlatformAdmin: false, createdAt: stamp, updatedAt: stamp }

const problem = (status: number, code: string, detail?: string) => HttpResponse.json({ title: code, status, code, detail }, { status })

function login() {
  server.use(http.get("/api/auth/me", () => HttpResponse.json({ user: leader, csrfToken: "t" })))
}

function detail(path = "/databases/:databaseId/migrations/:migrationId") {
  login()
  renderRoute(
    <AuthProvider>
      <MigrationDetailPage />
    </AuthProvider>,
    { path, url: "/databases/1/migrations/5" },
  )
}

describe("MigrationsPage", () => {
  it("menampilkan daftar dan status, dan tombol baru hanya untuk View + Edit", async () => {
    server.use(http.get("/api/databases/:id/migrations", () => HttpResponse.json([draft, { ...done, id: 6, seq: 2, title: "Kedua" }])))
    renderRoute(<MigrationsPage />, { path: "/databases/:databaseId/migrations", url: "/databases/1/migrations" })

    expect(await screen.findByRole("link", { name: "Buat t" })).toHaveAttribute("href", "/databases/1/migrations/5")
    expect(screen.getByText("Success")).toBeInTheDocument()
    expect(await screen.findByRole("button", { name: "Migration baru" })).toBeInTheDocument()
  })

  it("menyembunyikan tombol baru untuk View Only", async () => {
    server.use(http.get("/api/databases/:id/migrations", () => HttpResponse.json([])))
    renderRoute(<MigrationsPage />, { path: "/databases/:databaseId/migrations", url: "/databases/2/migrations" })

    expect(await screen.findByText("Belum ada migration.")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Migration baru" })).not.toBeInTheDocument()
  })

  it("menampilkan error bila akses ditolak", async () => {
    server.use(http.get("/api/databases/:id/migrations", () => problem(403, "FORBIDDEN")))
    renderRoute(<MigrationsPage />, { path: "/databases/:databaseId/migrations", url: "/databases/1/migrations" })

    expect(await screen.findByText("Anda tidak punya akses untuk aksi ini")).toBeInTheDocument()
  })
})

describe("MigrationNewPage", () => {
  it("tidak bisa disimpan sebelum SQL up diisi", async () => {
    renderRoute(<MigrationNewPage />, { path: "/databases/:databaseId/migrations/new", url: "/databases/1/migrations/new" })

    expect(await screen.findByRole("button", { name: "Simpan draft" })).toBeDisabled()
  })
})

describe("MigrationDetailPage", () => {
  it("mengajukan draft ke review", async () => {
    const user = userEvent.setup()
    let submitted = false
    server.use(
      http.get("/api/migrations/:id", () => HttpResponse.json(submitted ? pending : draft)),
      http.post("/api/migrations/:id/submit", () => {
        submitted = true
        return HttpResponse.json(pending)
      }),
      http.get("/api/migrations/:id/approvals", () => HttpResponse.json({ required: [], approvals: [] })),
    )
    detail()

    await user.click(await screen.findByRole("button", { name: "Ajukan review" }))
    expect(await screen.findByText("Pending Review")).toBeInTheDocument()
  })

  it("menampilkan penolakan backend saat pengajuan gagal", async () => {
    const user = userEvent.setup()
    server.use(
      http.get("/api/migrations/:id", () => HttpResponse.json(draft)),
      http.post("/api/migrations/:id/submit", () => problem(409, "CONFLICT", "tidak ada leader aktif")),
    )
    detail()

    await user.click(await screen.findByRole("button", { name: "Ajukan review" }))
    expect(await screen.findByRole("alert")).toHaveTextContent("tidak ada leader aktif")
  })

  it("leader menyetujui; leader yang sudah memutuskan tidak melihat tombol", async () => {
    const user = userEvent.setup()
    let body: unknown = null
    server.use(
      http.get("/api/migrations/:id", () => HttpResponse.json(pending)),
      http.get("/api/migrations/:id/approvals", () =>
        HttpResponse.json({
          required: [{ groupId: 1, leaderId: 7, name: "Lead" }, { groupId: 2, leaderId: 8, name: "Dua" }],
          approvals: [
            { migrationId: 5, revision: 1, groupId: 1, leaderId: 7, kind: "apply", decision: "pending", comment: "", decidedAt: null },
            { migrationId: 5, revision: 1, groupId: 2, leaderId: 8, kind: "apply", decision: "approved", comment: "ok", decidedAt: stamp },
          ],
        }),
      ),
      http.post("/api/migrations/:id/approvals", async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(pending)
      }),
    )
    detail()

    await user.click(await screen.findByRole("button", { name: "Setujui" }))
    await waitFor(() => expect(body).toEqual({ kind: "apply", decision: "approved", comment: "" }))
  })

  it("menampilkan error bila bukan leader", async () => {
    const user = userEvent.setup()
    server.use(
      http.get("/api/migrations/:id", () => HttpResponse.json(pending)),
      http.get("/api/migrations/:id/approvals", () =>
        HttpResponse.json({
          required: [{ groupId: 1, leaderId: 7, name: "Lead" }],
          approvals: [{ migrationId: 5, revision: 1, groupId: 1, leaderId: 7, kind: "apply", decision: "pending", comment: "", decidedAt: null }],
        }),
      ),
      http.post("/api/migrations/:id/approvals", () => problem(403, "FORBIDDEN")),
    )
    detail()

    await user.click(await screen.findByRole("button", { name: "Tolak" }))
    expect(await screen.findByText("Anda tidak punya akses untuk aksi ini")).toBeInTheDocument()
  })

  it("migration irreversible tidak bisa di-rollback dari UI", async () => {
    server.use(
      http.get("/api/migrations/:id", () => HttpResponse.json({ ...done, irreversible: true, irreversibleReason: "drop kolom", downSql: undefined })),
      http.get("/api/migrations/:id/approvals", () => HttpResponse.json({ required: [], approvals: [] })),
    )
    detail()

    expect(await screen.findByRole("button", { name: "Ajukan rollback" })).toBeDisabled()
    expect(screen.getByText("drop kolom")).toBeInTheDocument()
  })

  it("mengajukan rollback dan menampilkan penolakan aturan", async () => {
    const user = userEvent.setup()
    server.use(
      http.get("/api/migrations/:id", () => HttpResponse.json(done)),
      http.get("/api/migrations/:id/approvals", () => HttpResponse.json({ required: [], approvals: [] })),
      http.post("/api/migrations/:id/rollback", () => problem(409, "CONFLICT", "bukan migration aktif terakhir")),
    )
    detail()

    await user.click(await screen.findByRole("button", { name: "Ajukan rollback" }))
    expect(await screen.findByRole("alert")).toHaveTextContent("bukan migration aktif terakhir")
  })
})

describe("HistoryPage", () => {
  const rows = [
    { id: 1, migrationId: 5, revision: 1, userId: 1, kind: "apply", status: "success", executedSql: "x", revertsExecutionId: null, errorMessage: "", sqlState: "", startedAt: stamp, finishedAt: stamp },
    { id: 2, migrationId: 5, revision: 1, userId: 1, kind: "apply", status: "failed", executedSql: "x", revertsExecutionId: null, errorMessage: "relation exists", sqlState: "42P07", startedAt: stamp, finishedAt: stamp },
  ]

  it("menampilkan attempt gagal dan meminta hideRollback ke API saat dicentang", async () => {
    const user = userEvent.setup()
    const seen: (string | null)[] = []
    server.use(
      http.get("/api/databases/:id/migration-history", ({ request }) => {
        const hide = new URL(request.url).searchParams.get("hideRollback")
        seen.push(hide)
        return HttpResponse.json(hide === "true" ? [rows[1]] : rows)
      }),
    )
    renderRoute(<HistoryPage />, { path: "/databases/:databaseId/history", url: "/databases/1/history" })

    expect(await screen.findByText(/relation exists/)).toBeInTheDocument()
    expect(screen.getByText(/42P07/)).toBeInTheDocument()

    await user.click(screen.getByLabelText("Sembunyikan rollback"))
    await waitFor(() => expect(seen).toContain("true"))
    await waitFor(() => expect(screen.queryByText("#1")).not.toBeInTheDocument())
  })

  it("menampilkan keadaan kosong", async () => {
    server.use(http.get("/api/databases/:id/migration-history", () => HttpResponse.json([])))
    renderRoute(<HistoryPage />, { path: "/databases/:databaseId/history", url: "/databases/1/history" })

    expect(await screen.findByText("Belum ada eksekusi.")).toBeInTheDocument()
  })
})
