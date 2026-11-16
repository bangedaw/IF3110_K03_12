import { NavLink, Outlet } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { useAuth } from "@/features/auth/AuthProvider"
import { cn } from "@/lib/utils"

function NavItem({ to, children }: { to: string; children: string }) {
  return (
    <NavLink to={to} className={({ isActive }) => cn("shrink-0 whitespace-nowrap rounded-md px-2 py-1 text-sm", isActive ? "bg-muted font-medium" : "text-muted-foreground hover:text-foreground")}>
      {children}
    </NavLink>
  )
}

export function AppLayout() {
  const { user, logout } = useAuth()
  return (
    <div className="min-h-screen">
      <header className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b px-4 py-2">
        <span className="font-semibold">DBMP</span>
        <nav className="order-last -mx-1 flex w-full items-center gap-1 overflow-x-auto px-1 sm:order-none sm:mx-0 sm:w-auto sm:flex-1 sm:px-0">
          <NavItem to="/databases">Databases</NavItem>
          {user?.isPlatformAdmin && (
            <>
              <NavItem to="/admin/users">Users</NavItem>
              <NavItem to="/admin/groups">Groups</NavItem>
              <NavItem to="/admin/connections">Koneksi</NavItem>
            </>
          )}
        </nav>
        <div className="ml-auto flex min-w-0 items-center gap-2 text-sm">
          <span className="truncate text-muted-foreground">{user?.displayName || user?.username}</span>
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
