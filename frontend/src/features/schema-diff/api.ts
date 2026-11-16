import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { client, postUntyped, unwrap } from "@/features/shared/client"
import type { DiffAgainst } from "@/features/shared/types"

export interface MigrationDraftInput {
  title: string
  description: string
  sql: string
}

export function useSnapshots(databaseId: number) {
  return useQuery({
    queryKey: ["databases", databaseId, "snapshots"],
    queryFn: ({ signal }) =>
      unwrap(client.GET("/api/databases/{databaseId}/snapshots", { params: { path: { databaseId } }, signal })),
  })
}

export function useCreateSnapshot(databaseId: number) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => unwrap(client.POST("/api/databases/{databaseId}/snapshots", { params: { path: { databaseId } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["databases", databaseId, "snapshots"] }),
  })
}

export function useSchemaDiff(databaseId: number) {
  return useMutation({
    mutationFn: (against: DiffAgainst) =>
      unwrap(
        client.POST("/api/databases/{databaseId}/schema-diff", {
          params: { path: { databaseId } },
          body: { against, direction: "toTarget" },
        }),
      ),
  })
}

export function useSaveMigrationDraft(databaseId: number) {
  return useMutation({
    mutationFn: (input: MigrationDraftInput) =>
      postUntyped<{ id: number }>(`/api/databases/${databaseId}/migrations`, input),
  })
}
