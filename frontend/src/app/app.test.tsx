import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { createMemoryRouter, RouterProvider } from "react-router-dom"
import { describe, expect, it } from "vitest"
import { AuthProvider } from "@/features/auth/AuthProvider"
import { server } from "@/mocks/server"
import { routes } from "./router"

const admin = { id: 1, username: "admin", displayName: "", isPlatformAdmin: true, isActive: true, createdAt: "2026-10-24T00:00:00Z", updatedAt: "2026-10-24T00:00:00Z" }
const alice = { ...admin, id: 2, username: "alice", isPlatformAdmin: false }

function problem(status: number, code: string, detail: string) {
  return HttpResponse.json({ type: "about:blank", title: detail, status, code, detail }, { status })
}

function loggedInAs(user: typeof admin | null, csrfToken = "csrf-123") {
  server.use(http.get("/api/auth/me", () => (user ? HttpResponse.json({ user, csrfToken }) : problem(401, "UNAUTHENTICATED", "belum login"))))
}

function renderApp(url: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const router = createMemoryRouter(routes, { initialEntries: [url] })
  render(
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <RouterProvider router={router} />
      </AuthProvider>
    </QueryClientProvider>,
  )
  return router
}

const emptyUsers = () => HttpResponse.json({ items: [admin, alice], limit: 200, offset: 0, total: 2 })

describe("app shell", () => {
  it("mengarahkan ke login, lalu kembali ke halaman tujuan setelah login berhasil", async () => {
    const user = userEvent.setup()
    loggedInAs(null)
    server.use(
      http.get("/api/admin/users", emptyUsers),
      http.post("/api/auth/login", async ({ request }) => {
        const body = (await request.json()) as { username: string; password: string }
        if (body.password !== "rahasia123") return problem(401, "UNAUTHENTICATED", "username atau password salah")
        return HttpResponse.json({ user: admin, csrfToken: "csrf-123" })
      }),
    )
    const router = renderApp("/admin/users")

    await user.type(await screen.findByLabelText("Username"), "admin")
    await user.type(screen.getByLabelText("Password"), "salah")
    await user.click(screen.getByRole("button", { name: "Masuk" }))
    expect(await screen.findByRole("alert")).toHaveTextContent("username atau password salah")
    expect(screen.getByLabelText("Password")).toHaveValue("")

    await user.type(screen.getByLabelText("Password"), "rahasia123")
    await user.click(screen.getByRole("button", { name: "Masuk" }))

    expect(await screen.findByRole("heading", { name: "Users" })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe("/admin/users")
  })

  it("mengirim X-CSRF-Token hanya pada request yang mengubah state", async () => {
    const user = userEvent.setup()
    loggedInAs(admin, "csrf-abc")
    const seen: Record<string, string | null> = {}
    server.use(
      http.get("/api/admin/groups", ({ request }) => {
        seen.get = request.headers.get("X-CSRF-Token")
        return HttpResponse.json({ items: [], limit: 200, offset: 0, total: 0 })
      }),
      http.post("/api/admin/groups", ({ request }) => {
        seen.post = request.headers.get("X-CSRF-Token")
        return HttpResponse.json({ id: 9, name: "analyst", description: "", createdAt: admin.createdAt }, { status: 201 })
      }),
    )
    renderApp("/admin/groups")

    await user.type(await screen.findByLabelText("Nama group"), "analyst")
    await user.click(screen.getByRole("button", { name: "Tambah group" }))

    await waitFor(() => expect(seen.post).toBe("csrf-abc"))
    expect(seen.get).toBeNull()
  })

  it("kembali ke login dengan pesan saat API membalas 401", async () => {
    loggedInAs(admin)
    server.use(http.get("/api/admin/users", () => problem(401, "SESSION_EXPIRED", "session berakhir")))
    const router = renderApp("/admin/users")

    expect(await screen.findByText("Sesi Anda berakhir, silakan login kembali.")).toBeInTheDocument()
    expect(router.state.location.pathname).toBe("/login")
  })

  it("menyembunyikan menu admin dan menolak halaman admin untuk user biasa", async () => {
    loggedInAs(alice)
    server.use(http.get("/api/databases", () => HttpResponse.json({ items: [] })))
    renderApp("/admin/users")

    expect(await screen.findByRole("alert")).toHaveTextContent("hanya untuk Platform Admin")
    expect(screen.queryByRole("link", { name: "Users" })).not.toBeInTheDocument()
    expect(screen.getByRole("link", { name: "Databases" })).toBeInTheDocument()
  })

  it("logout me-revoke session di backend lalu kembali ke login", async () => {
    const user = userEvent.setup()
    loggedInAs(admin)
    let loggedOut = false
    server.use(
      http.get("/api/admin/users", emptyUsers),
      http.post("/api/auth/logout", () => {
        loggedOut = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderApp("/admin/users")

    await user.click(await screen.findByRole("button", { name: "Keluar" }))

    expect(await screen.findByRole("button", { name: "Masuk" })).toBeInTheDocument()
    expect(loggedOut).toBe(true)
    expect(screen.queryByText("Sesi Anda berakhir, silakan login kembali.")).not.toBeInTheDocument()
  })
})
