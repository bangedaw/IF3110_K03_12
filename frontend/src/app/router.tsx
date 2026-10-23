import { Navigate, type RouteObject } from "react-router-dom"
import { adminRoutes } from "@/features/admin/routes"
import { AdminRoute, ProtectedRoute } from "@/features/auth/guards"
import { LoginPage } from "@/features/auth/LoginPage"
import { connectionRoutes } from "@/features/connections/routes"
import { explorerRoutes } from "@/features/explorer/routes"
import { AppLayout } from "./AppLayout"

export const routes: RouteObject[] = [
  { path: "/login", element: <LoginPage /> },
  {
    element: <ProtectedRoute />,
    children: [
      {
        element: <AppLayout />,
        children: [
          { index: true, element: <Navigate to="/databases" replace /> },
          ...explorerRoutes,
          { element: <AdminRoute />, children: [...connectionRoutes, ...adminRoutes] },
          { path: "*", element: <Navigate to="/" replace /> },
        ],
      },
    ],
  },
]
