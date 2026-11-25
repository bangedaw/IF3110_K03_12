import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { client, unwrap } from "@/features/shared/client"
import type { MigrationDefinition, MigrationUpdate } from "@/features/shared/types"

export const migrationKeys = {
  list: (databaseId: number) => ["databases", databaseId, "migrations"] as const,
  one: (id: number) => ["migrations", id] as const,
  approvals: (id: number, requestId: number) => ["migrations", id, "approvals", requestId] as const,
  history: (databaseId: number, hideRollback: boolean, migrationId?: number) =>
    ["databases", databaseId, "migration-history", hideRollback, migrationId] as const,
}

export type MigrationRequestAction = "submit" | "rollback" | "forward"

export function useMigrations(databaseId: number) {
  return useQuery({
    queryKey: migrationKeys.list(databaseId),
    queryFn: ({ signal }) =>
      unwrap(client.GET("/api/databases/{databaseId}/migrations", { params: { path: { databaseId } }, signal })),
  })
}

export function useMigration(migrationId: number, pollMs = 2000) {
  return useQuery({
    queryKey: migrationKeys.one(migrationId),
    queryFn: ({ signal }) =>
      unwrap(client.GET("/api/migrations/{migrationId}", { params: { path: { migrationId } }, signal })),
    refetchInterval: (query) => (query.state.data?.status === "queued" || query.state.data?.status === "running" ? pollMs : false),
  })
}

export function useApprovals(migrationId: number, requestId: number) {
  return useQuery({
    queryKey: migrationKeys.approvals(migrationId, requestId),
    queryFn: ({ signal }) =>
      unwrap(client.GET("/api/migrations/{migrationId}/approvals", { params: { path: { migrationId } }, signal })),
  })
}

export function useHistory(databaseId: number, hideRollback: boolean, migrationId?: number) {
  return useQuery({
    queryKey: migrationKeys.history(databaseId, hideRollback, migrationId),
    queryFn: ({ signal }) =>
      unwrap(
        client.GET("/api/databases/{databaseId}/migration-history", {
          params: { path: { databaseId }, query: { hideRollback, migrationId } },
          signal,
        }),
      ),
  })
}

export function useRefreshMigrations(databaseId: number) {
  const queryClient = useQueryClient()
  return () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: ["migrations"] }),
      queryClient.invalidateQueries({ queryKey: migrationKeys.list(databaseId) }),
      queryClient.invalidateQueries({ queryKey: ["databases", databaseId, "migration-history"] }),
      queryClient.invalidateQueries({ queryKey: ["jobs", databaseId] }),
    ])
}

export function useCreateMigration(databaseId: number) {
  return useMutation({
    mutationFn: (body: MigrationDefinition) =>
      unwrap(client.POST("/api/databases/{databaseId}/migrations", { params: { path: { databaseId } }, body })),
    onSuccess: useRefreshMigrations(databaseId),
  })
}

export function useUpdateMigration(databaseId: number, migrationId: number) {
  return useMutation({
    mutationFn: (body: MigrationUpdate) =>
      unwrap(client.PUT("/api/migrations/{migrationId}", { params: { path: { migrationId } }, body })),
    onSuccess: useRefreshMigrations(databaseId),
  })
}

export function useDeleteMigration(databaseId: number, migrationId: number) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => unwrap(client.DELETE("/api/migrations/{migrationId}", { params: { path: { migrationId } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: migrationKeys.list(databaseId) }),
  })
}

export function useRequestMigration(databaseId: number, migrationId: number) {
  return useMutation({
    mutationFn: (action: MigrationRequestAction) => {
      const params = { params: { path: { migrationId } } }
      if (action === "submit") return unwrap(client.POST("/api/migrations/{migrationId}/submit", params))
      if (action === "rollback") return unwrap(client.POST("/api/migrations/{migrationId}/rollback", params))
      return unwrap(client.POST("/api/migrations/{migrationId}/forward", params))
    },
    onSuccess: useRefreshMigrations(databaseId),
  })
}

export function useDecide(databaseId: number, migrationId: number) {
  return useMutation({
    mutationFn: (body: { requestId: number; decision: "approved" | "rejected"; comment: string }) =>
      unwrap(client.POST("/api/migrations/{migrationId}/approvals", { params: { path: { migrationId } }, body })),
    onSuccess: useRefreshMigrations(databaseId),
  })
}

export function useExecute(databaseId: number, migrationId: number) {
  return useMutation({
    mutationFn: (requestId: number) =>
      unwrap(client.POST("/api/migrations/{migrationId}/execute", { params: { path: { migrationId } }, body: { requestId } })),
    onSuccess: useRefreshMigrations(databaseId),
  })
}

export function useExplain(migrationId: number) {
  return useMutation({
    mutationFn: (analyze: boolean) =>
      unwrap(client.POST("/api/migrations/{migrationId}/explain", { params: { path: { migrationId } }, body: { analyze } })),
  })
}
