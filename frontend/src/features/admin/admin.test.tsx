import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { AuthProvider } from "@/features/auth/AuthProvider"
import { server } from "@/mocks/server"
import { renderRoute } from "@/test/utils"
import { GroupDetailPage } from "./GroupDetailPage"
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
      http.get("/api/admin/groups/7", () => HttpResponse.json({ id: 7, name: "analyst", description: "", createdAt: base.createdAt, members: [alice] })),
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
