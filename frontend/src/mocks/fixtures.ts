import type { Connection, DatabaseDetail, DatabaseSummary, TableDetail } from "@/features/shared/types"

export const connectionFixtures: Connection[] = [
  {
    id: 1,
    name: "inventory",
    kind: "postgres",
    host: "localhost",
    port: 5433,
    databaseName: "inventory",
    username: "dbmp",
    sslMode: "disable",
    isActive: true,
    hasPassword: true,
    createdAt: "2026-10-22T08:00:00Z",
    updatedAt: "2026-10-22T08:00:00Z",
  },
  {
    id: 2,
    name: "sales",
    kind: "mysql",
    host: "127.0.0.1",
    port: 3307,
    databaseName: "sales",
    username: "dbmp",
    sslMode: "disable",
    isActive: false,
    hasPassword: true,
    createdAt: "2026-10-22T08:00:00Z",
    updatedAt: "2026-10-22T08:00:00Z",
  },
]

export const databaseFixtures: DatabaseSummary[] = [
  { id: 1, name: "inventory", kind: "postgres", isActive: true, level: "edit" },
  { id: 2, name: "staging", kind: "postgres", isActive: true, level: "view" },
  { id: 3, name: "sales", kind: "mysql", isActive: true, level: "view" },
]

export function databaseDetail(id: number): DatabaseDetail {
  const base = databaseFixtures.find((d) => d.id === id) ?? databaseFixtures[0]
  return { ...base, host: "localhost", port: 5433, databaseName: base.name, sslMode: "disable" }
}

export const warehousesTable: TableDetail = {
  schema: "inventory",
  name: "warehouses",
  kind: "table",
  rowEstimate: 120,
  columns: [
    { name: "id", dataType: "bigint", rawType: "bigint", nullable: false, default: "nextval('inventory.warehouses_id_seq')", isIdentity: false, ordinal: 1 },
    { name: "code", dataType: "text", rawType: "text", nullable: false, ordinal: 2 },
    { name: "city", dataType: "text", rawType: "text", nullable: true, ordinal: 3 },
  ],
  constraints: [
    { name: "warehouses_pkey", type: "primary_key", columns: ["id"] },
    { name: "warehouses_code_key", type: "unique", columns: ["code"] },
    { name: "warehouses_region_fk", type: "foreign_key", columns: ["city"], refSchema: "inventory", refTable: "cities", refColumns: ["name"], onDelete: "CASCADE" },
  ],
  indexes: [
    { name: "warehouses_pkey", columns: ["id"], unique: true, primary: true, method: "btree" },
    { name: "warehouses_city_idx", columns: ["city"], unique: false, primary: false, method: "btree" },
  ],
}
