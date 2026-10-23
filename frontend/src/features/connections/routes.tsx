import type { RouteObject } from "react-router-dom"
import { ConnectionsPage } from "./ConnectionsPage"

export const connectionRoutes: RouteObject[] = [{ path: "admin/connections", element: <ConnectionsPage /> }]
