import { Navigate, Outlet, useLocation } from "react-router-dom"
import { Loading } from "@/components/ErrorState"
import { useAuth } from "./AuthProvider"

export function ProtectedRoute() {
  const { user, loading } = useAuth()
  const location = useLocation()
  if (loading) return <Loading />
  if (!user) return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />
  return <Outlet />
}

export function AdminRoute() {
  const { user } = useAuth()
  if (!user?.isPlatformAdmin) {
    return (
      <p role="alert" className="text-sm text-destructive">
        Halaman ini hanya untuk Platform Admin.
      </p>
    )
  }
  return <Outlet />
}
