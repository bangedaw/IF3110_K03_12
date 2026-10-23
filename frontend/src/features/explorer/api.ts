import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { client, unwrap } from "@/features/shared/client"

export interface RowsParams {
  limit: number
  offset: number
  orderBy?: string
  orderDir?: "asc" | "desc"
}

export function useDatabases() {
  return useQuery({
    queryKey: ["databases"],
    queryFn: ({ signal }) => unwrap(client.GET("/api/databases", { signal })),
  })
}

export function useDatabase(databaseId: number) {
  return useQuery({
    queryKey: ["databases", databaseId],
    queryFn: ({ signal }) => unwrap(client.GET("/api/databases/{databaseId}", { params: { path: { databaseId } }, signal })),
  })
}

export function useSchemas(databaseId: number) {
  return useQuery({
    queryKey: ["databases", databaseId, "schemas"],
    queryFn: ({ signal }) =>
      unwrap(client.GET("/api/databases/{databaseId}/schemas", { params: { path: { databaseId } }, signal })),
    retry: false,
  })
}

export function useTables(databaseId: number, schemaName: string, enabled = true) {
  return useQuery({
    queryKey: ["databases", databaseId, "schemas", schemaName, "tables"],
    queryFn: ({ signal }) =>
      unwrap(
        client.GET("/api/databases/{databaseId}/schemas/{schemaName}/tables", {
          params: { path: { databaseId, schemaName } },
          signal,
        }),
      ),
    enabled,
    retry: false,
  })
}

export function useTable(databaseId: number, schemaName: string, tableName: string) {
  return useQuery({
    queryKey: ["databases", databaseId, "schemas", schemaName, "tables", tableName],
    queryFn: ({ signal }) =>
      unwrap(
        client.GET("/api/databases/{databaseId}/schemas/{schemaName}/tables/{tableName}", {
          params: { path: { databaseId, schemaName, tableName } },
          signal,
        }),
      ),
    retry: false,
  })
}

export function useRows(databaseId: number, schemaName: string, tableName: string, params: RowsParams) {
  return useQuery({
    queryKey: ["databases", databaseId, "schemas", schemaName, "tables", tableName, "rows", params],
    queryFn: ({ signal }) =>
      unwrap(
        client.GET("/api/databases/{databaseId}/schemas/{schemaName}/tables/{tableName}/rows", {
          params: {
            path: { databaseId, schemaName, tableName },
            query: {
              limit: params.limit,
              offset: params.offset,
              orderBy: params.orderBy,
              orderDir: params.orderBy ? (params.orderDir ?? "asc") : undefined,
            },
          },
          signal,
        }),
      ),
    placeholderData: keepPreviousData,
    retry: false,
  })
}
