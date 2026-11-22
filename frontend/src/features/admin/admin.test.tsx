import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { AuthProvider } from "@/features/auth/AuthProvider"
import { server } from "@/mocks/server"
import { renderRoute } from "@/test/utils"
import { GroupDetailPage } from "./GroupDetailPage"
import { GroupsPage } from "./GroupsPage"
import { UsersPage } from "./UsersPage"

const base = { displayName: "", isActive: true, isPlatformAdmin: false, createdAt: "2026-10-24T00:00:00Z", updatedAt: "2026-10-24T00:00:00Z" }
const admin = { ...base, id: 1, username: "admin", isPlatformAdmin: true }
const alice = { ...base, id: 2, username: "alice" }
const bob = { ...base, id: 3, username: "bob" }

interface Call {
  method: string
  path: string
  body: unknown
}

function record(calls: Call[]) {
  return async ({ request }: { request: Request }) => {
    const text = await request.text()
    calls.push({ method: request.method, path: new URL(request.url).pathname, body: text ? JSON.parse(text) : null })
  }
}

function withAuth(element: React.ReactElement) {
  server.use(http.get("/api/auth/me", () => HttpResponse.json({ user: admin, csrfToken: "t" })))
  return <AuthProvider>{element}</AuthProvider>
}

describe("UsersPage", () => {
  it("menampilkan error validasi per field dari backend", async () => {
    const user = userEvent.setup()
    server.use(
      http.get("/api/admin/users", () => HttpResponse.json({ items: [admin, alice], limit: 200, offset: 0, total: 2 })),
      http.post("/api/admin/users", () =>
        HttpResponse.json(
          { type: "about:blank", title: "x", status: 422, code: "VALIDATION_FAILED", detail: "input tidak valid", errors: { password: "8-256 karakter" } },
          { status: 422 },
        ),
      ),
    )
    renderRoute(withAuth(<UsersPage />), { path: "/admin/users", url: "/admin/users" })

    await user.click(await screen.findByRole("button", { name: "Tambah user" }))
    await user.type(screen.getByLabelText("Username"), "bob")
    await user.type(screen.getByLabelText("Password"), "pendek")
    await user.click(screen.getByRole("button", { name: "Simpan" }))

    expect(await screen.findByText("8-256 karakter")).toBeInTheDocument()
  })

  it("tidak mengizinkan admin menonaktifkan atau menghapus dirinya sendiri", async () => {
    server.use(http.get("/api/admin/users", () => HttpResponse.json({ items: [admin, alice], limit: 200, offset: 0, total: 2 })))
    renderRoute(withAuth(<UsersPage />), { path: "/admin/users", url: "/admin/users" })

    const self = (await screen.findByText("admin")).closest("tr")!
    await waitFor(() => expect(within(self).getByRole("button", { name: "Nonaktifkan" })).toBeDisabled())
    expect(within(self).getByRole("button", { name: "Hapus" })).toBeDisabled()
    const other = screen.getByText("alice").closest("tr")!
    expect(within(other).getByRole("button", { name: "Nonaktifkan" })).toBeEnabled()
  })

  it("mereset password dan menonaktifkan user", async () => {
    const user = userEvent.setup()
    const calls: Call[] = []
    server.use(
      http.get("/api/admin/users", () => HttpResponse.json({ items: [admin, alice], limit: 200, offset: 0, total: 2 })),
      http.post("/api/admin/users/:id/password", async (ctx) => {
        await record(calls)(ctx)
        return new HttpResponse(null, { status: 204 })
      }),
      http.patch("/api/admin/users/:id", async (ctx) => {
        await record(calls)(ctx)
        return HttpResponse.json({ ...alice, isActive: false })
      }),
    )
    renderRoute(withAuth(<UsersPage />), { path: "/admin/users", url: "/admin/users" })

    const row = (await screen.findByText("alice")).closest("tr")!
    await user.click(within(row).getByRole("button", { name: "Reset password" }))
    await user.type(screen.getByLabelText("Password baru untuk alice"), "passwordbaru")
    await user.click(within(row).getByRole("button", { name: "Simpan" }))
    expect(await screen.findByRole("status")).toHaveTextContent("Password alice direset")

    await user.click(within(row).getByRole("button", { name: "Nonaktifkan" }))
    await waitFor(() => expect(calls).toHaveLength(2))
    expect(calls).toEqual([
      { method: "POST", path: "/api/admin/users/2/password", body: { password: "passwordbaru" } },
      { method: "PATCH", path: "/api/admin/users/2", body: { isActive: false } },
    ])
  })
})

describe("GroupDetailPage", () => {
  it("menambah anggota dan mengatur level akses per database", async () => {
    const user = userEvent.setup()
    const calls: Call[] = []
    server.use(
      http.get("/api/admin/groups/7", () =>
        HttpResponse.json({ id: 7, name: "analyst", description: "", createdAt: base.createdAt, leaderId: 2, leaderUsername: "alice", members: [alice] }),
      ),
      http.get("/api/admin/groups/7/grants", () => HttpResponse.json({ items: [{ databaseId: 1, databaseName: "inventory", level: "view" }] })),
      http.get("/api/admin/users", () => HttpResponse.json({ items: [admin, alice, bob], limit: 200, offset: 0, total: 3 })),
      http.put("/api/admin/groups/7/members/:userId", async (ctx) => {
        await record(calls)(ctx)
        return new HttpResponse(null, { status: 204 })
      }),
      http.put("/api/admin/groups/7/grants/:databaseId", async (ctx) => {
        await record(calls)(ctx)
        return HttpResponse.json({ databaseId: 1, databaseName: "inventory", level: "edit" })
      }),
      http.delete("/api/admin/groups/7/grants/:databaseId", async (ctx) => {
        await record(calls)(ctx)
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderRoute(<GroupDetailPage />, { path: "/admin/groups/:groupId", url: "/admin/groups/7" })

    expect(await screen.findByRole("heading", { name: "analyst" })).toBeInTheDocument()
    const picker = screen.getByLabelText("Pilih user")
    await waitFor(() => expect(within(picker).getAllByRole("option").map((o) => o.textContent)).toEqual(["Pilih user…", "admin", "bob"]))
    await user.selectOptions(picker, "3")
    await user.click(screen.getByRole("button", { name: "Tambah anggota" }))

    const inventory = await screen.findByLabelText("Akses inventory")
    expect(inventory).toHaveValue("view")
    await user.selectOptions(inventory, "edit")
    await user.selectOptions(screen.getByLabelText("Akses inventory"), "")

    await waitFor(() => expect(calls).toHaveLength(3))
    expect(calls).toEqual([
      { method: "PUT", path: "/api/admin/groups/7/members/3", body: null },
      { method: "PUT", path: "/api/admin/groups/7/grants/1", body: { level: "edit" } },
      { method: "DELETE", path: "/api/admin/groups/7/grants/1", body: null },
    ])
    expect(screen.getByLabelText("Akses sales")).toHaveValue("")
  })
})

describe("leader group", () => {
  it("mewajibkan leader dari user aktif saat membuat group dan menampilkan leader di daftar", async () => {
    const user = userEvent.setup()
    const calls: Call[] = []
    const carol = { ...base, id: 4, username: "carol", isActive: false }
    server.use(
      http.get("/api/admin/users", () => HttpResponse.json({ items: [admin, alice, carol], limit: 200, offset: 0, total: 3 })),
      http.get("/api/admin/groups", () =>
        HttpResponse.json({
          items: [
            { id: 7, name: "analyst", description: "", createdAt: base.createdAt, leaderId: 2, leaderUsername: "alice" },
            { id: 8, name: "lama", description: "", createdAt: base.createdAt, leaderId: null, leaderUsername: null },
          ],
          limit: 200,
          offset: 0,
          total: 2,
        }),
      ),
      http.post("/api/admin/groups", async (ctx) => {
        await record(calls)(ctx)
        return HttpResponse.json({ id: 9, name: "ops", description: "", createdAt: base.createdAt, leaderId: 2, leaderUsername: "alice" }, { status: 201 })
      }),
    )
    renderRoute(<GroupsPage />, { path: "/admin/groups", url: "/admin/groups" })

    expect(await screen.findByText("Leader: alice")).toBeInTheDocument()
    expect(screen.getByText("Belum ada leader")).toBeInTheDocument()
    const leader = screen.getByLabelText("Leader")
    await waitFor(() => expect(within(leader).getAllByRole("option").map((o) => o.textContent)).toEqual(["Pilih leader…", "admin", "alice"]))

    await user.type(screen.getByLabelText("Nama group"), "ops")
    await user.selectOptions(leader, "2")
    await user.click(screen.getByRole("button", { name: "Tambah group" }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toEqual({ method: "POST", path: "/api/admin/groups", body: { name: "ops", description: "", leaderId: 2 } })
  })

  it("menunjuk anggota aktif sebagai leader dan tidak membiarkan leader dikeluarkan", async () => {
    const user = userEvent.setup()
    const calls: Call[] = []
    const carol = { ...base, id: 4, username: "carol", isActive: false }
    server.use(
      http.get("/api/admin/groups/7", () =>
        HttpResponse.json({ id: 7, name: "analyst", description: "", createdAt: base.createdAt, leaderId: 2, leaderUsername: "alice", members: [alice, bob, carol] }),
      ),
      http.get("/api/admin/groups/7/grants", () => HttpResponse.json({ items: [] })),
      http.get("/api/admin/users", () => HttpResponse.json({ items: [admin, alice, bob, carol], limit: 200, offset: 0, total: 4 })),
      http.put("/api/admin/groups/7/leader", async (ctx) => {
        await record(calls)(ctx)
        return HttpResponse.json({ id: 7, name: "analyst", description: "", createdAt: base.createdAt, leaderId: 3, leaderUsername: "bob" })
      }),
    )
    renderRoute(<GroupDetailPage />, { path: "/admin/groups/:groupId", url: "/admin/groups/7" })

    const members = await screen.findByRole("list")
    const row = (name: string) => within(members).getByText(name).closest("li")!
    const aliceRow = row("alice")
    expect(within(aliceRow).getByText("Leader")).toBeInTheDocument()
    expect(within(aliceRow).getByRole("button", { name: "Keluarkan" })).toBeDisabled()
    expect(within(aliceRow).queryByRole("button", { name: "Jadikan leader" })).not.toBeInTheDocument()
    const carolRow = row("carol")
    expect(within(carolRow).queryByRole("button", { name: "Jadikan leader" })).not.toBeInTheDocument()

    await user.click(within(row("bob")).getByRole("button", { name: "Jadikan leader" }))

    await waitFor(() => expect(calls).toEqual([{ method: "PUT", path: "/api/admin/groups/7/leader", body: { userId: 3 } }]))
  })

  it("memberi peringatan untuk group lama yang belum punya leader", async () => {
    server.use(
      http.get("/api/admin/groups/8", () =>
        HttpResponse.json({ id: 8, name: "lama", description: "", createdAt: base.createdAt, leaderId: null, leaderUsername: null, members: [alice] }),
      ),
      http.get("/api/admin/groups/8/grants", () => HttpResponse.json({ items: [] })),
      http.get("/api/admin/users", () => HttpResponse.json({ items: [admin, alice], limit: 200, offset: 0, total: 2 })),
    )
    renderRoute(<GroupDetailPage />, { path: "/admin/groups/:groupId", url: "/admin/groups/8" })

    expect(await screen.findByText(/belum punya leader/)).toHaveAttribute("role", "status")
  })

  it("menampilkan penolakan backend saat menambah akses untuk group tanpa leader", async () => {
    const user = userEvent.setup()
    server.use(
      http.get("/api/admin/groups/8", () =>
        HttpResponse.json({ id: 8, name: "lama", description: "", createdAt: base.createdAt, leaderId: null, leaderUsername: null, members: [alice] }),
      ),
      http.get("/api/admin/groups/8/grants", () => HttpResponse.json({ items: [] })),
      http.get("/api/admin/users", () => HttpResponse.json({ items: [admin, alice], limit: 200, offset: 0, total: 2 })),
      http.put("/api/admin/groups/8/grants/:databaseId", () =>
        HttpResponse.json(
          { type: "about:blank", title: "Conflict", status: 409, code: "CONFLICT", detail: "group belum punya leader; tunjuk leader sebelum menambah akses" },
          { status: 409 },
        ),
      ),
    )
    renderRoute(<GroupDetailPage />, { path: "/admin/groups/:groupId", url: "/admin/groups/8" })

    await user.selectOptions(await screen.findByLabelText("Akses inventory"), "view")

    expect(await screen.findByRole("alert")).toHaveTextContent("tunjuk leader sebelum menambah akses")
  })
})

describe("aksi admin M1 yang dilengkapi", () => {
  it("mengubah nama tampilan user", async () => {
    const user = userEvent.setup()
    const calls: Call[] = []
    server.use(
      http.get("/api/admin/users", () => HttpResponse.json({ items: [admin, alice], limit: 200, offset: 0, total: 2 })),
      http.patch("/api/admin/users/:id", async (ctx) => {
        await record(calls)(ctx)
        return HttpResponse.json({ ...alice, displayName: "Alice Backend" })
      }),
    )
    renderRoute(withAuth(<UsersPage />), { path: "/admin/users", url: "/admin/users" })

    const row = (await screen.findByText("alice")).closest("tr")!
    await user.click(within(row).getByRole("button", { name: "Ubah nama" }))
    await user.type(screen.getByLabelText("Nama tampilan alice"), "Alice Backend")
    await user.click(within(row).getByRole("button", { name: "Simpan" }))

    expect(await screen.findByRole("status")).toHaveTextContent("Nama alice diperbarui")
    expect(calls).toEqual([{ method: "PATCH", path: "/api/admin/users/2", body: { displayName: "Alice Backend" } }])
  })

  it("mengubah nama dan deskripsi group", async () => {
    const user = userEvent.setup()
    const calls: Call[] = []
    server.use(
      http.get("/api/admin/groups/7", () =>
        HttpResponse.json({ id: 7, name: "analyst", description: "lama", createdAt: base.createdAt, leaderId: 2, leaderUsername: "alice", members: [alice] }),
      ),
      http.get("/api/admin/groups/7/grants", () => HttpResponse.json({ items: [] })),
      http.get("/api/admin/users", () => HttpResponse.json({ items: [admin, alice], limit: 200, offset: 0, total: 2 })),
      http.patch("/api/admin/groups/7", async (ctx) => {
        await record(calls)(ctx)
        return HttpResponse.json({ id: 7, name: "data-team", description: "baru", createdAt: base.createdAt, leaderId: 2, leaderUsername: "alice" })
      }),
    )
    renderRoute(<GroupDetailPage />, { path: "/admin/groups/:groupId", url: "/admin/groups/7" })

    await user.click(await screen.findByRole("button", { name: "Ubah nama" }))
    const name = screen.getByLabelText("Nama group")
    await user.clear(name)
    await user.type(name, "data-team")
    const description = screen.getByLabelText("Deskripsi")
    await user.clear(description)
    await user.type(description, "baru")
    await user.click(screen.getByRole("button", { name: "Simpan" }))

    await waitFor(() => expect(calls).toEqual([{ method: "PATCH", path: "/api/admin/groups/7", body: { name: "data-team", description: "baru" } }]))
    await waitFor(() => expect(screen.queryByLabelText("Nama group")).not.toBeInTheDocument())
  })
})
