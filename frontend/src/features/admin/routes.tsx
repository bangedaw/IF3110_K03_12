import type { RouteObject } from "react-router-dom"
import { GroupDetailPage } from "./GroupDetailPage"
import { GroupsPage } from "./GroupsPage"
import { UsersPage } from "./UsersPage"

export const adminRoutes: RouteObject[] = [
  { path: "admin/users", element: <UsersPage /> },
  { path: "admin/groups", element: <GroupsPage /> },
  { path: "admin/groups/:groupId", element: <GroupDetailPage /> },
]
