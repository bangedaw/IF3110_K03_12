import { http, HttpResponse } from "msw"
import type { Cell, RowsPage, SchemaDiffResult, Snapshot } from "@/features/shared/types"
import { databaseDetail, databaseFixtures, warehousesTable } from "./fixtures"

export const requestedUrls: string[] = []

export function resetExplorerLog() {
  requestedUrls.length = 0
}

export const sampleDiff: SchemaDiffResult = {
  changes: [
    { kind: "drop_column", schema: "inventory", table: "warehouses", name: "region", destructive: true },
    { kind: "add_column", schema: "inventory", table: "warehouses", name: "attrs", destructive: false },
  ],
  sql: 'ALTER TABLE "inventory"."warehouses" DROP COLUMN "region";\nALTER TABLE "inventory"."warehouses" ADD COLUMN "attrs" jsonb',
  warnings: [],
}

export const sampleSnapshots: Snapshot[] = [{ id: 5, databaseId: 1, takenAt: "2026-10-22T10:00:00Z", model: { schemas: [] } }]

function cell(value: string): Cell {
  return { value, isNull: false }
}

function rowsFor(limit: number, offset: number): RowsPage {
  const total = 120
  const count = Math.max(0, Math.min(limit, total - offset))
  const rows = Array.from({ length: count }, (_, i) => {
    const id = offset + i + 1
    return [cell(String(id)), cell(`WH-${id}`), id % 5 === 0 ? { value: "", isNull: true } : cell("Jakarta")]
  })
  return { columns: ["id", "code", "city"], rows, totalEstimate: total, limit, offset }
}

export const explorerHandlers = [
  http.get("/api/databases", ({ request }) => {
    requestedUrls.push(request.url)
    return HttpResponse.json(databaseFixtures)
  }),
  http.get("/api/databases/:id", ({ request, params }) => {
    requestedUrls.push(request.url)
    return HttpResponse.json(databaseDetail(Number(params.id)))
  }),
  http.get("/api/databases/:id/schemas", ({ request }) => {
    requestedUrls.push(request.url)
    return HttpResponse.json(["inventory", "reporting"])
  }),
  http.get("/api/databases/:id/schemas/:schema/tables", ({ request, params }) => {
    requestedUrls.push(request.url)
    if (params.schema === "reporting") return HttpResponse.json([])
    return HttpResponse.json([{ schema: "inventory", name: "warehouses", kind: "table", rowEstimate: 120 }])
  }),
  http.get("/api/databases/:id/schemas/:schema/tables/:table/rows", ({ request }) => {
    requestedUrls.push(request.url)
    const url = new URL(request.url)
    return HttpResponse.json(rowsFor(Number(url.searchParams.get("limit")), Number(url.searchParams.get("offset"))))
  }),
  http.get("/api/databases/:id/schemas/:schema/tables/:table", ({ request }) => {
    requestedUrls.push(request.url)
    return HttpResponse.json(warehousesTable)
  }),
  http.get("/api/databases/:id/snapshots", () => HttpResponse.json(sampleSnapshots)),
  http.post("/api/databases/:id/schema-diff", async ({ request }) => {
    requestedUrls.push(request.url)
    return HttpResponse.json(sampleDiff)
  }),
]
