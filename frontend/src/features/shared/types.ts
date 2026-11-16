import type { components } from "@/lib/api/schema"

type Schemas = components["schemas"]

export type DbKind = Schemas["DBMSKind"]
export type AccessLevel = Schemas["AccessLevel"]

export type Connection = Schemas["DatabaseConnection"]
export type ConnectionInput = Schemas["DatabaseConnectionCreate"]
export type ConnectionPatch = Schemas["DatabaseConnectionUpdate"]
export type ConnectionPage = Schemas["DatabaseConnectionPage"]
export type TestResult = Schemas["ConnectionTestResult"]

export type DatabaseSummary = Schemas["ExplorerDatabase"]
export type DatabaseDetail = Schemas["ExplorerDatabaseDetail"]
export type TableSummary = Schemas["ExplorerTableSummary"]
export type TableDetail = Schemas["ExplorerTable"]
export type Column = Schemas["ExplorerColumn"]
export type Constraint = Schemas["ExplorerConstraint"]
export type TableIndex = Schemas["ExplorerIndex"]
export type Cell = Schemas["ExplorerCell"]
export type RowsPage = Schemas["ExplorerRowsPage"]

export type Snapshot = Schemas["SchemaSnapshot"]
export type DiffAgainst = Schemas["SchemaDiffRequest"]["against"]
export type SchemaChange = Schemas["SchemaDiffChange"]
export type SchemaDiffResult = Schemas["SchemaDiffResult"]
