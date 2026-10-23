import { http, HttpResponse } from "msw"
import type { Connection, ConnectionInput } from "@/features/shared/types"
import { connectionFixtures } from "./fixtures"

export interface RecordedRequest {
  method: string
  path: string
  body: Record<string, unknown> | null
}

export const connectionLog: RecordedRequest[] = []

let store: Connection[] = []
let nextId = 100

export function resetConnections() {
  store = connectionFixtures.map((c) => ({ ...c }))
  nextId = 100
  connectionLog.length = 0
}

resetConnections()

async function record(request: Request) {
  const body = request.method === "GET" || request.method === "DELETE" ? null : ((await request.json().catch(() => null)) as Record<string, unknown> | null)
  connectionLog.push({ method: request.method, path: new URL(request.url).pathname, body })
  return body
}

function problem(status: number, code: string, detail: string, errors?: Record<string, string>) {
  return HttpResponse.json({ type: "about:blank", title: detail, status, code, detail, errors }, { status })
}

export const connectionHandlers = [
  http.get("/api/admin/databases", async ({ request }) => {
    await record(request)
    return HttpResponse.json({ items: store, limit: 200, offset: 0, total: store.length })
  }),
  http.post("/api/admin/databases/test", async ({ request }) => {
    const body = (await record(request)) as unknown as (ConnectionInput & { databaseId?: number }) | null
    if (body?.host === "unreachable.invalid") {
      return problem(503, "DB_UNREACHABLE", "database target tidak terjangkau")
    }
    return HttpResponse.json({ ok: true, latencyMs: 7 })
  }),
  http.post("/api/admin/databases/:id/test", async ({ request }) => {
    await record(request)
    return HttpResponse.json({ ok: true, latencyMs: 3 })
  }),
  http.post("/api/admin/databases", async ({ request }) => {
    const body = (await record(request)) as unknown as ConnectionInput
    if (store.some((c) => c.name === body.name)) {
      return problem(409, "CONFLICT", "nama koneksi sudah dipakai")
    }
    const created: Connection = {
      id: nextId++,
      name: body.name,
      kind: body.kind,
      host: body.host,
      port: body.port,
      databaseName: body.databaseName,
      username: body.username,
      sslMode: body.sslMode ?? "disable",
      isActive: body.isActive,
      hasPassword: true,
      createdAt: "2026-10-23T00:00:00Z",
      updatedAt: "2026-10-23T00:00:00Z",
    }
    store.push(created)
    return HttpResponse.json(created, { status: 201 })
  }),
  http.patch("/api/admin/databases/:id", async ({ request, params }) => {
    const body = (await record(request)) as Partial<Connection> | null
    const index = store.findIndex((c) => c.id === Number(params.id))
    if (index < 0) return problem(404, "NOT_FOUND", "tidak ditemukan")
    store[index] = { ...store[index], ...body }
    return HttpResponse.json(store[index])
  }),
  http.delete("/api/admin/databases/:id", async ({ request, params }) => {
    await record(request)
    store = store.filter((c) => c.id !== Number(params.id))
    return new HttpResponse(null, { status: 204 })
  }),
]
