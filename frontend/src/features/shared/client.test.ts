import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { server } from "@/mocks/server"
import { ApiError, client, errorMessage, postUntyped, unwrap } from "./client"

function listDatabases() {
  return unwrap(client.GET("/api/databases"))
}

describe("unwrap", () => {
  it("mengembalikan data pada respons sukses", async () => {
    server.use(http.get("/api/databases", () => HttpResponse.json([{ id: 1, name: "inventory", kind: "postgres", isActive: true, level: "edit" }])))

    await expect(listDatabases()).resolves.toEqual([{ id: 1, name: "inventory", kind: "postgres", isActive: true, level: "edit" }])
  })

  it("mengubah Problem Details menjadi ApiError", async () => {
    server.use(
      http.get("/api/databases", () =>
        HttpResponse.json(
          { type: "about:blank", title: "Unprocessable", status: 422, code: "VALIDATION_FAILED", detail: "input tidak valid", errors: { name: "wajib" } },
          { status: 422 },
        ),
      ),
    )

    const error = await listDatabases().catch((e: unknown) => e)

    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ status: 422, code: "VALIDATION_FAILED", message: "input tidak valid", fields: { name: "wajib" } })
  })

  it("tetap menghasilkan ApiError ketika body bukan JSON", async () => {
    server.use(http.get("/api/databases", () => new HttpResponse("Bad Gateway", { status: 502 })))

    const error = await listDatabases().catch((e: unknown) => e)

    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ status: 502, code: "UNKNOWN" })
  })

  it("mengembalikan undefined untuk 204", async () => {
    server.use(http.delete("/api/admin/databases/:id", () => new HttpResponse(null, { status: 204 })))

    await expect(unwrap(client.DELETE("/api/admin/databases/{databaseId}", { params: { path: { databaseId: 1 } } }))).resolves.toBeUndefined()
  })

  it("mengirim cookie session (same-origin) pada setiap request", async () => {
    let credentials: string | undefined
    server.use(
      http.get("/api/databases", ({ request }) => {
        credentials = request.credentials
        return HttpResponse.json([])
      }),
    )

    await listDatabases()

    expect(credentials).toBe("same-origin")
  })
})

describe("postUntyped", () => {
  it("mengirim JSON dan mengembalikan body", async () => {
    let received: unknown = null
    server.use(
      http.post("/api/databases/1/migrations", async ({ request }) => {
        received = await request.json()
        return HttpResponse.json({ id: 7 }, { status: 201 })
      }),
    )

    await expect(postUntyped("/api/databases/1/migrations", { title: "t" })).resolves.toEqual({ id: 7 })
    expect(received).toEqual({ title: "t" })
  })

  it("memakai detail dari body non-Problem sebagai pesan error", async () => {
    server.use(http.post("/api/x", () => HttpResponse.json({ detail: "forbidden" }, { status: 403 })))

    const error = await postUntyped("/api/x", {}).catch((e: unknown) => e)

    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ status: 403, message: "forbidden" })
  })

  it("memetakan Problem Details apa adanya", async () => {
    server.use(http.post("/api/x", () => HttpResponse.json({ title: "Conflict", status: 409, code: "CONFLICT" }, { status: 409 })))

    const error = await postUntyped("/api/x", {}).catch((e: unknown) => e)

    expect(error).toMatchObject({ status: 409, code: "CONFLICT" })
  })
})

describe("errorMessage", () => {
  it("memberi pesan khusus untuk database tidak terjangkau dan akses ditolak", () => {
    expect(errorMessage(new ApiError({ title: "x", status: 503, code: "DB_UNREACHABLE" }))).toBe("Database target tidak terjangkau")
    expect(errorMessage(new ApiError({ title: "x", status: 403, code: "FORBIDDEN" }))).toBe("Anda tidak punya akses untuk aksi ini")
  })

  it("meneruskan pesan error biasa", () => {
    expect(errorMessage(new Error("boom"))).toBe("boom")
    expect(errorMessage("string")).toBe("Terjadi kesalahan")
  })
})
