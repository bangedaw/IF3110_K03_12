import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { describe, expect, it } from "vitest"
import { requestedUrls } from "@/mocks/explorer"
import { server } from "@/mocks/server"
import { renderRoute } from "@/test/utils"
import { DatabaseLayout, ExplorerHome } from "./DatabaseLayout"
import { DatabaseListPage } from "./DatabaseListPage"
import { TableView } from "./TableView"

function problem(status: number, code: string) {
  return HttpResponse.json({ title: code, status, code }, { status })
}

function renderExplorer(url: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[url]}>
        <Routes>
          <Route path="/databases/:databaseId" element={<DatabaseLayout />}>
            <Route index element={<ExplorerHome />} />
            <Route path="schemas/:schema/tables/:table" element={<TableView />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe("DatabaseListPage", () => {
  it("menampilkan hanya database yang dikembalikan backend beserta tingkat aksesnya", async () => {
    renderRoute(<DatabaseListPage />, { path: "/databases", url: "/databases" })

    expect(await screen.findByRole("link", { name: /inventory/ })).toHaveAttribute("href", "/databases/1")
    expect(screen.getByRole("link", { name: /inventory/ })).toHaveTextContent("View + Edit")
    expect(screen.getByRole("link", { name: /staging/ })).toHaveTextContent("View Only")
    expect(screen.getByRole("link", { name: /sales/ })).toHaveTextContent("MySQL")
  })

  it("menampilkan keadaan kosong bila user belum punya akses", async () => {
    server.use(http.get("/api/databases", () => HttpResponse.json([])))

    renderRoute(<DatabaseListPage />, { path: "/databases", url: "/databases" })

    expect(await screen.findByText(/belum memiliki akses/i)).toBeInTheDocument()
  })

  it("menampilkan error yang jelas saat backend gagal", async () => {
    server.use(http.get("/api/databases", () => problem(401, "UNAUTHENTICATED")))

    renderRoute(<DatabaseListPage />, { path: "/databases", url: "/databases" })

    expect(await screen.findByRole("alert")).toHaveTextContent("Sesi tidak valid")
  })
})

describe("DatabaseLayout", () => {
  it("menampilkan identitas database, tree schema, dan memuat tabel schema pertama", async () => {
    renderExplorer("/databases/1")

    expect(await screen.findByRole("heading", { name: "inventory" })).toBeInTheDocument()
    expect(await screen.findByRole("link", { name: "warehouses" })).toHaveAttribute(
      "href",
      "/databases/1/schemas/inventory/tables/warehouses",
    )
    expect(screen.getByRole("button", { name: "reporting" })).toHaveAttribute("aria-expanded", "false")
  })

  it("membuka schema lain saat diklik dan menampilkan keadaan kosong", async () => {
    const user = userEvent.setup()
    renderExplorer("/databases/1")
    await screen.findByRole("link", { name: "warehouses" })

    await user.click(screen.getByRole("button", { name: "reporting" }))

    expect(await screen.findByText("Tidak ada tabel")).toBeInTheDocument()
  })

  it("menampilkan error koneksi saat database target mati (503)", async () => {
    server.use(http.get("/api/databases/:id/schemas", () => problem(503, "DB_UNREACHABLE")))

    renderExplorer("/databases/1")

    expect(await screen.findByText("Koneksi ke database gagal")).toBeInTheDocument()
  })

  it("menampilkan penolakan akses untuk database tanpa grant", async () => {
    server.use(http.get("/api/databases/:id", () => problem(403, "FORBIDDEN")))

    renderExplorer("/databases/9")

    expect(await screen.findByRole("alert")).toHaveTextContent("Anda tidak punya akses")
  })
})

describe("TableView", () => {
  const url = "/databases/1/schemas/inventory/tables/warehouses"

  it("menampilkan kolom, lalu constraint dan index lewat tab", async () => {
    const user = userEvent.setup()
    renderExplorer(url)

    expect(await screen.findByRole("heading", { name: "inventory.warehouses" })).toBeInTheDocument()
    expect(screen.getByRole("cell", { name: "code" })).toBeInTheDocument()
    expect(screen.getAllByText("NOT NULL").length).toBeGreaterThan(0)

    await user.click(screen.getByRole("tab", { name: "Constraints" }))
    expect(screen.getByText("PRIMARY KEY")).toBeInTheDocument()
    expect(screen.getByText(/→ inventory\.cities\(name\)/)).toBeInTheDocument()

    await user.click(screen.getByRole("tab", { name: "Indexes" }))
    expect(screen.getByText("warehouses_city_idx")).toBeInTheDocument()
    expect(screen.getByText("primary")).toBeInTheDocument()
  })

  it("menampilkan 404 saat tabel tidak ada", async () => {
    server.use(http.get("/api/databases/:id/schemas/:schema/tables/:table", () => problem(404, "NOT_FOUND")))

    renderExplorer(url)

    expect(await screen.findByRole("alert")).toBeInTheDocument()
  })

  it("data grid: halaman pertama, pagination server-side, dan nilai NULL", async () => {
    const user = userEvent.setup()
    renderExplorer(`${url}?tab=data`)

    const table = await screen.findByRole("table")
    expect(await within(table).findByText("WH-1")).toBeInTheDocument()
    expect(within(table).getAllByText("NULL").length).toBeGreaterThan(0)
    expect(screen.getByText(/Baris 1–50/)).toHaveTextContent("≈ 120 baris")
    expect(screen.getByRole("button", { name: "Sebelumnya" })).toBeDisabled()

    await user.click(screen.getByRole("button", { name: "Berikutnya" }))

    await waitFor(() => expect(screen.getByText(/Baris 51–100/)).toBeInTheDocument())
    const last = requestedUrls.filter((u) => u.includes("/rows")).at(-1)!
    expect(new URL(last).searchParams.get("offset")).toBe("50")
    expect(new URL(last).searchParams.get("limit")).toBe("50")
  })

  it("data grid: halaman terakhir menonaktifkan tombol berikutnya", async () => {
    const user = userEvent.setup()
    renderExplorer(`${url}?tab=data`)
    await screen.findByText("WH-1")

    await user.click(screen.getByRole("button", { name: "Berikutnya" }))
    await screen.findByText("WH-51")
    await user.click(screen.getByRole("button", { name: "Berikutnya" }))

    await waitFor(() => expect(screen.getByText(/Baris 101–120/)).toBeInTheDocument())
    expect(screen.getByRole("button", { name: "Berikutnya" })).toBeDisabled()
  })

  it("data grid: klik header mengurutkan di server, menaik lalu menurun, dan kembali ke halaman pertama", async () => {
    const user = userEvent.setup()
    renderExplorer(`${url}?tab=data`)
    await screen.findByText("WH-1")
    await user.click(screen.getByRole("button", { name: "Berikutnya" }))
    await screen.findByText("WH-51")

    await user.click(screen.getByRole("button", { name: "code" }))
    await waitFor(() => {
      const last = new URL(requestedUrls.filter((u) => u.includes("/rows")).at(-1)!)
      expect(last.searchParams.get("orderBy")).toBe("code")
      expect(last.searchParams.get("orderDir")).toBe("asc")
      expect(last.searchParams.get("offset")).toBe("0")
    })

    await user.click(screen.getByRole("button", { name: "code" }))
    await waitFor(() => {
      const last = new URL(requestedUrls.filter((u) => u.includes("/rows")).at(-1)!)
      expect(last.searchParams.get("orderDir")).toBe("desc")
    })
  })

  it("data grid: mengubah ukuran halaman", async () => {
    const user = userEvent.setup()
    renderExplorer(`${url}?tab=data`)
    await screen.findByText("WH-1")

    await user.selectOptions(screen.getByLabelText("Per halaman"), "25")

    await waitFor(() => expect(screen.getByText(/Baris 1–25/)).toBeInTheDocument())
  })

  it("data grid: query tidak valid (422) tampil sebagai error, bukan crash", async () => {
    server.use(http.get("/api/databases/:id/schemas/:schema/tables/:table/rows", () => problem(422, "VALIDATION_FAILED")))

    renderExplorer(`${url}?tab=data`)

    expect(await screen.findByRole("alert")).toBeInTheDocument()
  })
})
