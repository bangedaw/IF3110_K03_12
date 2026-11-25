import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { client, unwrap } from "@/features/shared/client"
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
    // Hasil diff hanya punya SQL up, jadi draft ditandai irreversible sampai SQL down diisi di editor.
    mutationFn: (input: MigrationDraftInput) =>
      unwrap(
        client.POST("/api/databases/{databaseId}/migrations", {
          params: { path: { databaseId } },
          body: {
            title: input.title,
            description: input.description,
            upSql: input.sql,
            downSql: "",
            irreversible: true,
            irreversibleReason: "Dihasilkan dari schema diff; belum ada SQL down",
          },
        }),
      ),
  })
}
