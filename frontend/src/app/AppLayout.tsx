import { NavLink, Outlet } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { useAuth } from "@/features/auth/AuthProvider"
import { cn } from "@/lib/utils"

function NavItem({ to, children }: { to: string; children: string }) {
  return (
    <NavLink to={to} className={({ isActive }) => cn("rounded-md px-2 py-1 text-sm", isActive ? "bg-muted font-medium" : "text-muted-foreground hover:text-foreground")}>
      {children}
    </NavLink>
  )
}

export function AppLayout() {
  const { user, logout } = useAuth()
  return (
    <div className="min-h-screen">
      <header className="flex items-center justify-between border-b px-4 py-2">
        <nav className="flex items-center gap-1">
          <span className="mr-3 font-semibold">DBMP</span>
          <NavItem to="/databases">Databases</NavItem>
          {user?.isPlatformAdmin && (
            <>
              <NavItem to="/admin/users">Users</NavItem>
              <NavItem to="/admin/groups">Groups</NavItem>
              <NavItem to="/admin/connections">Koneksi</NavItem>
            </>
          )}
        </nav>
        <div className="flex items-center gap-2 text-sm">
          <span className="text-muted-foreground">{user?.displayName || user?.username}</span>
          <Button size="sm" variant="outline" onClick={() => void logout()}>
            Keluar
          </Button>
        </div>
      </header>
      <main className="p-4">
        <Outlet />
      </main>
    </div>
  )
}
