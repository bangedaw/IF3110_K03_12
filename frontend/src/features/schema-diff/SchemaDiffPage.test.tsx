import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { requestedUrls, sampleDiff } from "@/mocks/explorer"
import { server } from "@/mocks/server"
import { renderRoute } from "@/test/utils"
import { SchemaDiffPage } from "./SchemaDiffPage"

function renderDiff(databaseId = 1) {
  return renderRoute(<SchemaDiffPage />, {
    path: "/databases/:databaseId/diff",
    url: `/databases/${databaseId}/diff`,
  })
}

async function compareWithStaging(user: ReturnType<typeof userEvent.setup>) {
  const select = await screen.findByLabelText("Database pembanding")
  await screen.findByRole("option", { name: "staging" })
  await user.selectOptions(select, "2")
  await user.click(screen.getByRole("button", { name: "Bandingkan" }))
}

describe("SchemaDiffPage", () => {
  it("hanya menawarkan database lain berjenis DBMS yang sama", async () => {
    renderDiff()

    await screen.findByRole("option", { name: "staging" })
    expect(screen.queryByRole("option", { name: "sales" })).not.toBeInTheDocument()
    expect(screen.queryByRole("option", { name: "inventory" })).not.toBeInTheDocument()
  })

  it("menampilkan perubahan, menandai yang destructive, dan menaruh SQL di editor", async () => {
    const user = userEvent.setup()
    const { container } = renderDiff()

    await compareWithStaging(user)

    expect(await screen.findByText("2 perubahan")).toBeInTheDocument()
    expect(screen.getByText("Hapus kolom region pada inventory.warehouses")).toBeInTheDocument()
    expect(screen.getByText("destructive")).toBeInTheDocument()
    expect(screen.getByRole("alert")).toHaveTextContent("perubahan destructive")
    await waitFor(() => expect(container.querySelector(".cm-content")).toHaveTextContent('DROP COLUMN "region"'))

    const call = requestedUrls.find((u) => u.endsWith("/schema-diff"))
    expect(call).toBeDefined()
  })

  it("mengirim against dan direction toTarget ke backend", async () => {
    const user = userEvent.setup()
    let body: unknown = null
    server.use(
      http.post("/api/databases/:id/schema-diff", async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(sampleDiff)
      }),
    )
    renderDiff()

    await compareWithStaging(user)
    await screen.findByText("2 perubahan")

    expect(body).toEqual({ against: { type: "database", id: 2 }, direction: "toTarget" })
  })

  it("menyimpan SQL sebagai draft migration lewat endpoint migrations", async () => {
    const user = userEvent.setup()
    let body: Record<string, unknown> | null = null
    server.use(
      http.post("/api/databases/:id/migrations", async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ id: 9, ...body }, { status: 201 })
      }),
    )
    renderDiff()
    await compareWithStaging(user)
    await screen.findByText("2 perubahan")

    await user.click(screen.getByRole("button", { name: "Simpan sebagai draft migration" }))

    expect(await screen.findByText(/Draft #9 tersimpan/)).toBeInTheDocument()
    expect(body).toMatchObject({ title: "Sinkronkan inventory dengan staging", sql: sampleDiff.sql })
    expect(screen.getByRole("link", { name: "Buka Migrations" })).toHaveAttribute("href", "/databases/1/migrations")
  })

  it("menampilkan error bila penyimpanan draft ditolak", async () => {
    const user = userEvent.setup()
    server.use(
      http.post("/api/databases/:id/migrations", () =>
        HttpResponse.json({ title: "Forbidden", status: 403, code: "FORBIDDEN" }, { status: 403 }),
      ),
    )
    renderDiff()
    await compareWithStaging(user)
    await screen.findByText("2 perubahan")

    await user.click(screen.getByRole("button", { name: "Simpan sebagai draft migration" }))

    expect(await screen.findByText("Anda tidak punya akses untuk aksi ini")).toBeInTheDocument()
  })

  it("menonaktifkan penyimpanan draft untuk akses View Only", async () => {
    const user = userEvent.setup()
    renderDiff(2)

    const select = await screen.findByLabelText("Database pembanding")
    await screen.findByRole("option", { name: "inventory" })
    await user.selectOptions(select, "1")
    await user.click(screen.getByRole("button", { name: "Bandingkan" }))
    await screen.findByText("2 perubahan")

    expect(screen.getByRole("button", { name: "Simpan sebagai draft migration" })).toBeDisabled()
    expect(screen.getByText(/butuh akses View \+ Edit/)).toBeInTheDocument()
  })

  it("menyatakan schema identik bila tidak ada perubahan", async () => {
    const user = userEvent.setup()
    server.use(http.post("/api/databases/:id/schema-diff", () => HttpResponse.json({ changes: [], sql: "", warnings: [] })))
    renderDiff()

    await compareWithStaging(user)

    expect(await screen.findByText("Schema identik. Tidak ada perubahan.")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Simpan sebagai draft migration" })).not.toBeInTheDocument()
  })

  it("mode snapshot memakai snapshot tersimpan sebagai pembanding", async () => {
    const user = userEvent.setup()
    let body: unknown = null
    server.use(
      http.post("/api/databases/:id/schema-diff", async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(sampleDiff)
      }),
    )
    renderDiff()

    await user.click(await screen.findByLabelText("Snapshot"))
    await screen.findByRole("option", { name: /#5/ })
    await user.selectOptions(screen.getByLabelText("Snapshot", { selector: "select" }), "5")
    await user.click(screen.getByRole("button", { name: "Bandingkan" }))
    await screen.findByText("2 perubahan")

    expect(body).toEqual({ against: { type: "snapshot", id: 5 }, direction: "toTarget" })
  })

  it("menampilkan 403 dari backend ketika tidak punya view pada database pembanding", async () => {
    const user = userEvent.setup()
    server.use(
      http.post("/api/databases/:id/schema-diff", () =>
        HttpResponse.json({ title: "Forbidden", status: 403, code: "FORBIDDEN" }, { status: 403 }),
      ),
    )
    renderDiff()

    await compareWithStaging(user)

    expect(await screen.findByRole("alert")).toHaveTextContent("Anda tidak punya akses")
  })
})
