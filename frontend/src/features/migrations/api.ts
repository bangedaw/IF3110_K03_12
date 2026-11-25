import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { client, unwrap } from "@/features/shared/client"
import type { MigrationDefinition, MigrationKind } from "@/features/shared/types"

export const migrationKeys = {
  list: (databaseId: number) => ["databases", databaseId, "migrations"] as const,
  one: (id: number) => ["migrations", id] as const,
  approvals: (id: number, kind: MigrationKind) => ["migrations", id, "approvals", kind] as const,
  history: (databaseId: number, hideRollback: boolean) => ["databases", databaseId, "migration-history", hideRollback] as const,
}

export function useMigrations(databaseId: number) {
  return useQuery({
    queryKey: migrationKeys.list(databaseId),
    queryFn: ({ signal }) =>
      unwrap(client.GET("/api/databases/{databaseId}/migrations", { params: { path: { databaseId } }, signal })),
  })
}

export function useMigration(migrationId: number) {
  return useQuery({
    queryKey: migrationKeys.one(migrationId),
    queryFn: ({ signal }) =>
      unwrap(client.GET("/api/migrations/{migrationId}", { params: { path: { migrationId } }, signal })),
  })
}

export function useApprovals(migrationId: number, kind: MigrationKind, enabled = true) {
  return useQuery({
    queryKey: migrationKeys.approvals(migrationId, kind),
    enabled,
    queryFn: ({ signal }) =>
      unwrap(
        client.GET("/api/migrations/{migrationId}/approvals", {
          params: { path: { migrationId }, query: { kind } },
          signal,
        }),
      ),
  })
}

export function useHistory(databaseId: number, hideRollback: boolean) {
  return useQuery({
    queryKey: migrationKeys.history(databaseId, hideRollback),
    queryFn: ({ signal }) =>
      unwrap(
        client.GET("/api/databases/{databaseId}/migration-history", {
          params: { path: { databaseId }, query: { hideRollback } },
          signal,
        }),
      ),
  })
}

export function useCreateMigration(databaseId: number) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (body: MigrationDefinition) =>
      unwrap(client.POST("/api/databases/{databaseId}/migrations", { params: { path: { databaseId } }, body })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: migrationKeys.list(databaseId) }),
  })
}

// Semua aksi mengubah status migration, riwayat, dan approval: segarkan semuanya.
function useInvalidateAll() {
  const queryClient = useQueryClient()
  return () => queryClient.invalidateQueries()
}

export function useUpdateMigration(migrationId: number) {
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: (body: MigrationDefinition) =>
      unwrap(client.PUT("/api/migrations/{migrationId}", { params: { path: { migrationId } }, body })),
    onSuccess: invalidate,
  })
}

export function useSubmitMigration(migrationId: number) {
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: () => unwrap(client.POST("/api/migrations/{migrationId}/submit", { params: { path: { migrationId } } })),
    onSuccess: invalidate,
  })
}

export function useRequestExecution(migrationId: number, action: "rollback" | "forward") {
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: () =>
      unwrap(
        action === "rollback"
          ? client.POST("/api/migrations/{migrationId}/rollback", { params: { path: { migrationId } } })
          : client.POST("/api/migrations/{migrationId}/forward", { params: { path: { migrationId } } }),
      ),
    onSuccess: invalidate,
  })
}

export function useDecide(migrationId: number) {
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: (body: { kind: MigrationKind; decision: "approved" | "rejected"; comment?: string }) =>
      unwrap(client.POST("/api/migrations/{migrationId}/approvals", { params: { path: { migrationId } }, body })),
    onSuccess: invalidate,
  })
}
