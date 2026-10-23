import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render, screen } from "@testing-library/react"
import { RouterProvider, createMemoryRouter } from "react-router-dom"
import { describe, expect, it } from "vitest"
import { connectionRoutes } from "@/features/connections/routes"
import { explorerRoutes } from "./routes"

function renderAt(url: string) {
  const router = createMemoryRouter([...explorerRoutes, ...connectionRoutes], { initialEntries: [url] })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

describe("feature routes", () => {
  it("memasang daftar database", async () => {
    renderAt("/databases")

    expect(await screen.findByRole("link", { name: /inventory/ })).toBeInTheDocument()
  })

  it("memasang detail tabel di bawah layout database", async () => {
    renderAt("/databases/1/schemas/inventory/tables/warehouses")

    expect(await screen.findByRole("heading", { name: "inventory.warehouses" })).toBeInTheDocument()
  })

  it("memasang halaman schema diff", async () => {
    renderAt("/databases/1/diff")

    expect(await screen.findByRole("heading", { name: "Schema diff" })).toBeInTheDocument()
  })

  it("memasang halaman koneksi admin", async () => {
    renderAt("/admin/connections")

    expect(await screen.findByText("Koneksi database")).toBeInTheDocument()
  })
})
