import { screen, waitFor } from "@testing-library/react"
import { http, HttpResponse } from "msw"
import { describe, expect, it, vi } from "vitest"
import { server } from "@/mocks/server"
import { renderRoute } from "@/test/utils"
import type { Job } from "./api"
import { JobStatus } from "./JobStatus"

const base: Job = {
  id: 7,
  requestId: 3,
  databaseId: 1,
  kind: "apply",
  status: "pending",
  attempts: 0,
  maxAttempts: 3,
  batchesCommitted: 0,
  rowsCommitted: 0,
  createdAt: "2026-11-23T03:00:00Z",
  runAfter: "2026-11-23T03:00:00Z",
}

// serve membalas GET job dengan urutan respons; respons terakhir diulang. Mengembalikan penghitung request.
function serve(...responses: Job[]) {
  const calls = { count: 0 }
  server.use(
    http.get("/api/databases/1/jobs/7", () => {
      const job = responses[Math.min(calls.count, responses.length - 1)]
      calls.count++
      return HttpResponse.json(job)
    }),
  )
  return calls
}

const wait = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

function show(props: Partial<Parameters<typeof JobStatus>[0]> = {}) {
  return renderRoute(<JobStatus databaseId={1} jobId={7} pollMs={20} {...props} />, { path: "/", url: "/" })
}

describe("JobStatus", () => {
  it("memperbarui diri sampai job selesai, lalu berhenti polling dan memanggil onFinish sekali", async () => {
    const running = { ...base, status: "running", attempts: 1, startedAt: "2026-11-23T03:00:01Z", runAfter: undefined } as const
    const partial: Job = { ...running, status: "partial", batchesCommitted: 2, rowsCommitted: 2000, lastError: "batch 3: deadlock detected", finishedAt: "2026-11-23T03:00:09Z" }
    const calls = serve(base, running, partial)
    const onFinish = vi.fn()
    show({ onFinish })

    expect(await screen.findByText("Dalam antrean")).toBeInTheDocument()
    expect(await screen.findByText("Berjalan")).toBeInTheDocument()
    expect(await screen.findByText("Partial")).toBeInTheDocument()
    expect(screen.getByText("2 batch · 2.000 row ter-commit")).toBeInTheDocument()
    expect(screen.getByText(/melanjutkan dari checkpoint/)).toBeInTheDocument()
    expect(screen.getByText("Error terakhir: batch 3: deadlock detected")).toBeInTheDocument()

    await waitFor(() => expect(onFinish).toHaveBeenCalledTimes(1))
    expect(onFinish).toHaveBeenCalledWith(expect.objectContaining({ status: "partial" }))
    const settled = calls.count
    await wait(100)
    expect(calls.count).toBe(settled)
  })

  it("menampilkan partially reverted untuk rollback yang berhenti di tengah", async () => {
    serve({ ...base, kind: "rollback", status: "partial", attempts: 3, batchesCommitted: 3, rowsCommitted: 3000, runAfter: undefined })
    show()

    expect(await screen.findByText("Partially reverted")).toBeInTheDocument()
    expect(screen.getByText("Job #7 · Rollback")).toBeInTheDocument()
    expect(screen.getByText("3 batch · 3.000 row masih ter-commit")).toBeInTheDocument()
    expect(screen.getByText(/Jalankan rollback lagi/)).toBeInTheDocument()
  })

  it("menampilkan jadwal retry dan error attempt sebelumnya", async () => {
    serve({ ...base, attempts: 1, lastError: "deadlock detected", runAfter: "2026-11-23T03:00:10Z" })
    show()

    expect(await screen.findByText("Menunggu retry")).toBeInTheDocument()
    expect(screen.getByText("Attempt 1 dari 3")).toBeInTheDocument()
    expect(screen.getByText(/Attempt berikutnya paling cepat pukul/)).toBeInTheDocument()
    expect(screen.getByText("Error terakhir: deadlock detected")).toBeInTheDocument()
  })

  it("tidak menampilkan jadwal untuk job baru dan tidak memanggil onFinish untuk job yang sudah selesai", async () => {
    const calls = serve({ ...base, status: "succeeded", attempts: 1, runAfter: undefined, finishedAt: "2026-11-23T03:00:05Z" })
    const onFinish = vi.fn()
    show({ onFinish })

    expect(await screen.findByText("Berhasil")).toBeInTheDocument()
    expect(screen.queryByText(/paling cepat/)).not.toBeInTheDocument()
    await wait(100)
    expect(calls.count).toBe(1)
    expect(onFinish).not.toHaveBeenCalled()
  })

  it("menampilkan error dan berhenti polling saat API menolak", async () => {
    const calls = { count: 0 }
    server.use(
      http.get("/api/databases/1/jobs/7", () => {
        calls.count++
        return HttpResponse.json({ type: "about:blank", title: "Forbidden", status: 403, code: "FORBIDDEN", detail: "tidak punya akses" }, { status: 403 })
      }),
    )
    show()

    expect(await screen.findByRole("alert")).toHaveTextContent("tidak punya akses")
    await wait(100)
    expect(calls.count).toBe(1)
  })
})
