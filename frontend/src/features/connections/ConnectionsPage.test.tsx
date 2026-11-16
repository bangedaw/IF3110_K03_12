import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { describe, expect, it, vi } from "vitest"
import { connectionLog } from "@/mocks/connections"
import { server } from "@/mocks/server"
import { renderRoute } from "@/test/utils"
import { ConnectionsPage } from "./ConnectionsPage"

function renderPage() {
  return renderRoute(<ConnectionsPage />, { path: "/admin/connections", url: "/admin/connections" })
}

async function fillCreateForm(user: ReturnType<typeof userEvent.setup>, overrides: { host?: string; password?: string } = {}) {
  await user.type(screen.getByLabelText("Nama"), "analytics")
  await user.type(screen.getByLabelText("Host"), overrides.host ?? "db.internal")
  await user.type(screen.getByLabelText("Nama database"), "analytics")
  await user.type(screen.getByLabelText("Username"), "reader")
  if (overrides.password !== "") await user.type(screen.getByLabelText("Password"), overrides.password ?? "s3cret")
}

describe("ConnectionsPage", () => {
  it("menampilkan daftar koneksi tanpa pernah menampilkan password", async () => {
    renderPage()

    const row = (await screen.findByText("inventory")).closest("tr")!
    expect(within(row).getByText("dbmp@localhost:5433/inventory")).toBeInTheDocument()
    expect(screen.getByText("Nonaktif")).toBeInTheDocument()
    expect(screen.getByRole("table")).not.toHaveTextContent(/password|ciphertext/i)
    expect(screen.queryByLabelText("Password")).not.toBeInTheDocument()
  })

  it("menampilkan error ketika admin API menolak akses", async () => {
    server.use(http.get("/api/admin/databases", () => HttpResponse.json({ title: "Forbidden", status: 403, code: "FORBIDDEN" }, { status: 403 })))

    renderPage()

    expect(await screen.findByRole("alert")).toHaveTextContent("Anda tidak punya akses untuk aksi ini")
  })

  it("menolak form kosong di sisi klien tanpa mengirim request", async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText("inventory")
    connectionLog.length = 0

    await user.click(screen.getByRole("button", { name: "Tambah koneksi" }))
    await user.click(screen.getByRole("button", { name: "Simpan" }))

    expect(screen.getAllByRole("alert").length).toBeGreaterThanOrEqual(4)
    expect(connectionLog).toHaveLength(0)
  })

  it("membuat koneksi baru dengan password dan memuat ulang daftar", async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText("inventory")

    await user.click(screen.getByRole("button", { name: "Tambah koneksi" }))
    await fillCreateForm(user)
    await user.click(screen.getByRole("button", { name: "Simpan" }))

    expect(await screen.findByText("analytics")).toBeInTheDocument()
    const post = connectionLog.find((r) => r.method === "POST" && r.path === "/api/admin/databases")
    expect(post?.body).toMatchObject({ name: "analytics", kind: "postgres", host: "db.internal", port: 5432, password: "s3cret" })
  })

  it("mengganti port default ketika DBMS diganti, tetapi tidak menimpa port kustom", async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText("inventory")
    await user.click(screen.getByRole("button", { name: "Tambah koneksi" }))

    await user.selectOptions(screen.getByLabelText("DBMS"), "mysql")
    expect(screen.getByLabelText("Port")).toHaveValue("3306")

    await user.clear(screen.getByLabelText("Port"))
    await user.type(screen.getByLabelText("Port"), "3999")
    await user.selectOptions(screen.getByLabelText("DBMS"), "postgres")
    expect(screen.getByLabelText("Port")).toHaveValue("3999")
  })

  it("menampilkan konflik nama dari server", async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText("inventory")

    await user.click(screen.getByRole("button", { name: "Tambah koneksi" }))
    await user.type(screen.getByLabelText("Nama"), "inventory")
    await user.type(screen.getByLabelText("Host"), "h")
    await user.type(screen.getByLabelText("Nama database"), "d")
    await user.type(screen.getByLabelText("Username"), "u")
    await user.type(screen.getByLabelText("Password"), "p")
    await user.click(screen.getByRole("button", { name: "Simpan" }))

    expect(await screen.findByText("nama koneksi sudah dipakai")).toBeInTheDocument()
  })

  it("tidak mengirim field password saat edit dengan password dikosongkan", async () => {
    const user = userEvent.setup()
    renderPage()
    const row = (await screen.findByText("inventory")).closest("tr")!

    await user.click(within(row).getByRole("button", { name: "Ubah" }))
    expect(screen.getByLabelText("Password")).toHaveValue("")
    await user.clear(screen.getByLabelText("Host"))
    await user.type(screen.getByLabelText("Host"), "baru.internal")
    await user.click(screen.getByRole("button", { name: "Simpan" }))

    await waitFor(() => expect(connectionLog.some((r) => r.method === "PATCH")).toBe(true))
    const patch = connectionLog.find((r) => r.method === "PATCH")!
    expect(patch.path).toBe("/api/admin/databases/1")
    expect(patch.body).toMatchObject({ host: "baru.internal" })
    expect(patch.body).not.toHaveProperty("password")
  })

  it("mengirim password baru saat edit jika diisi", async () => {
    const user = userEvent.setup()
    renderPage()
    const row = (await screen.findByText("inventory")).closest("tr")!

    await user.click(within(row).getByRole("button", { name: "Ubah" }))
    await user.type(screen.getByLabelText("Password"), "ganti")
    await user.click(screen.getByRole("button", { name: "Simpan" }))

    await waitFor(() => expect(connectionLog.some((r) => r.method === "PATCH")).toBe(true))
    expect(connectionLog.find((r) => r.method === "PATCH")!.body).toMatchObject({ password: "ganti" })
  })

  it("test koneksi pada form menyertakan databaseId saat edit dan menampilkan hasil", async () => {
    const user = userEvent.setup()
    renderPage()
    const row = (await screen.findByText("inventory")).closest("tr")!

    await user.click(within(row).getByRole("button", { name: "Ubah" }))
    await user.click(screen.getByRole("button", { name: "Test koneksi" }))

    expect(await screen.findByText("Koneksi berhasil (7 ms)")).toBeInTheDocument()
    const call = connectionLog.find((r) => r.path === "/api/admin/databases/test")!
    expect(call.body).toMatchObject({ databaseId: 1 })
  })

  it("menampilkan kegagalan test koneksi ketika host tidak terjangkau", async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText("inventory")

    await user.click(screen.getByRole("button", { name: "Tambah koneksi" }))
    await fillCreateForm(user, { host: "unreachable.invalid" })
    await user.click(screen.getByRole("button", { name: "Test koneksi" }))

    expect(await screen.findByText("Database target tidak terjangkau")).toBeInTheDocument()
  })

  it("menguji koneksi tersimpan dari daftar", async () => {
    const user = userEvent.setup()
    renderPage()
    const row = (await screen.findByText("inventory")).closest("tr")!

    await user.click(within(row).getByRole("button", { name: "Test" }))

    expect(await within(row).findByText("OK (3 ms)")).toBeInTheDocument()
  })

  it("menghapus koneksi hanya setelah konfirmasi", async () => {
    const user = userEvent.setup()
    const confirm = vi.spyOn(window, "confirm")
    renderPage()
    const row = (await screen.findByText("inventory")).closest("tr")!

    confirm.mockReturnValueOnce(false)
    await user.click(within(row).getByRole("button", { name: "Hapus" }))
    expect(connectionLog.some((r) => r.method === "DELETE")).toBe(false)

    confirm.mockReturnValueOnce(true)
    await user.click(within(row).getByRole("button", { name: "Hapus" }))
    await waitFor(() => expect(screen.queryByText("inventory")).not.toBeInTheDocument())
    expect(connectionLog.find((r) => r.method === "DELETE")?.path).toBe("/api/admin/databases/1")

    confirm.mockRestore()
  })
})
