import { useMutation, useQuery, useQueryClient, type QueryKey } from "@tanstack/react-query"
import { client, unwrap } from "@/features/shared/client"
import type { components } from "@/lib/api/schema"

type Schemas = components["schemas"]
export type UserCreate = Schemas["UserCreate"]
export type UserUpdate = Schemas["UserUpdate"]
export type AccessLevel = Schemas["AccessLevel"]

export const adminKeys = {
  users: ["admin", "users"] as const,
  groups: ["admin", "groups"] as const,
  group: (id: number) => ["admin", "groups", id] as const,
  grants: (id: number) => ["admin", "groups", id, "grants"] as const,
}

function useInvalidating<T>(key: QueryKey, fn: (input: T) => Promise<unknown>) {
  const queryClient = useQueryClient()
  return useMutation({ mutationFn: fn, onSuccess: () => queryClient.invalidateQueries({ queryKey: key }) })
}

export function useUsers() {
  return useQuery({
    queryKey: adminKeys.users,
    queryFn: ({ signal }) => unwrap(client.GET("/api/admin/users", { params: { query: { limit: 200 } }, signal })),
  })
}

export function useCreateUser() {
  return useInvalidating(adminKeys.users, (body: UserCreate) => unwrap(client.POST("/api/admin/users", { body })))
}

export function useUpdateUser() {
  return useInvalidating(adminKeys.users, ({ id, body }: { id: number; body: UserUpdate }) =>
    unwrap(client.PATCH("/api/admin/users/{userId}", { params: { path: { userId: id } }, body })),
  )
}

export function useDeleteUser() {
  return useInvalidating(adminKeys.users, (id: number) => unwrap(client.DELETE("/api/admin/users/{userId}", { params: { path: { userId: id } } })))
}

export function useResetPassword() {
  return useMutation({
    mutationFn: ({ id, password }: { id: number; password: string }) =>
      unwrap(client.POST("/api/admin/users/{userId}/password", { params: { path: { userId: id } }, body: { password } })),
  })
}

export function useRevokeSessions() {
  return useMutation({
    mutationFn: (id: number) => unwrap(client.POST("/api/admin/users/{userId}/sessions/revoke", { params: { path: { userId: id } } })),
  })
}

export function useGroups() {
  return useQuery({
    queryKey: adminKeys.groups,
    queryFn: ({ signal }) => unwrap(client.GET("/api/admin/groups", { params: { query: { limit: 200 } }, signal })),
  })
}

export function useCreateGroup() {
  return useInvalidating(adminKeys.groups, (body: Schemas["GroupCreate"]) => unwrap(client.POST("/api/admin/groups", { body })))
}

export function useDeleteGroup() {
  return useInvalidating(adminKeys.groups, (id: number) => unwrap(client.DELETE("/api/admin/groups/{groupId}", { params: { path: { groupId: id } } })))
}

export function useGroup(id: number) {
  return useQuery({
    queryKey: adminKeys.group(id),
    queryFn: ({ signal }) => unwrap(client.GET("/api/admin/groups/{groupId}", { params: { path: { groupId: id } }, signal })),
  })
}

export function useGrants(id: number) {
  return useQuery({
    queryKey: adminKeys.grants(id),
    queryFn: ({ signal }) => unwrap(client.GET("/api/admin/groups/{groupId}/grants", { params: { path: { groupId: id } }, signal })),
  })
}

export function useSetMember(groupId: number) {
  return useInvalidating(adminKeys.group(groupId), ({ userId, member }: { userId: number; member: boolean }) => {
    const params = { params: { path: { groupId, userId } } }
    return unwrap(member ? client.PUT("/api/admin/groups/{groupId}/members/{userId}", params) : client.DELETE("/api/admin/groups/{groupId}/members/{userId}", params))
  })
}

export function useSetGrant(groupId: number) {
  return useInvalidating(adminKeys.grants(groupId), ({ databaseId, level }: { databaseId: number; level: AccessLevel | "" }) => {
    const path = { params: { path: { groupId, databaseId } } }
    return unwrap(
      level
        ? client.PUT("/api/admin/groups/{groupId}/grants/{databaseId}", { ...path, body: { level } })
        : client.DELETE("/api/admin/groups/{groupId}/grants/{databaseId}", path),
    )
  })
}
