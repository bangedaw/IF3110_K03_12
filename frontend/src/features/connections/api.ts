import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { client, unwrap } from "@/features/shared/client"
import type { ConnectionInput, ConnectionPatch } from "@/features/shared/types"

export const connectionKeys = {
  all: ["connections"] as const,
}

export function useConnections() {
  return useQuery({
    queryKey: connectionKeys.all,
    queryFn: ({ signal }) => unwrap(client.GET("/api/admin/databases", { params: { query: { limit: 200 } }, signal })),
  })
}

function useInvalidate() {
  const queryClient = useQueryClient()
  return async () => {
    await queryClient.invalidateQueries({ queryKey: connectionKeys.all })
    await queryClient.invalidateQueries({ queryKey: ["databases"] })
  }
}

export function useCreateConnection() {
  const invalidate = useInvalidate()
  return useMutation({
    mutationFn: (input: ConnectionInput) => unwrap(client.POST("/api/admin/databases", { body: input })),
    onSuccess: invalidate,
  })
}

export function useUpdateConnection() {
  const invalidate = useInvalidate()
  return useMutation({
    mutationFn: ({ id, input }: { id: number; input: ConnectionPatch }) =>
      unwrap(client.PATCH("/api/admin/databases/{databaseId}", { params: { path: { databaseId: id } }, body: input })),
    onSuccess: invalidate,
  })
}

export function useDeleteConnection() {
  const invalidate = useInvalidate()
  return useMutation({
    mutationFn: (id: number) => unwrap(client.DELETE("/api/admin/databases/{databaseId}", { params: { path: { databaseId: id } } })),
    onSuccess: invalidate,
  })
}

export function useTestConnection() {
  return useMutation({
    mutationFn: ({ input, databaseId }: { input: ConnectionInput; databaseId?: number }) =>
      unwrap(client.POST("/api/admin/databases/test", { body: databaseId === undefined ? input : { ...input, databaseId } })),
  })
}

export function useTestSavedConnection() {
  return useMutation({
    mutationFn: (id: number) => unwrap(client.POST("/api/admin/databases/{databaseId}/test", { params: { path: { databaseId: id } } })),
  })
}
