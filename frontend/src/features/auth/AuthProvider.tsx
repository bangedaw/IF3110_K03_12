import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { client, unwrap } from "@/features/shared/client"
import { installFetchInterceptor, setCsrfToken } from "@/lib/api/session"
import type { components } from "@/lib/api/schema"

export type User = components["schemas"]["User"]

interface AuthContextValue {
  user: User | null
  loading: boolean
  expired: boolean
  login: (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const [user, setUser] = useState<User | null>(null)
  const [loading, setLoading] = useState(true)
  const [expired, setExpired] = useState(false)

  const clear = useCallback(() => {
    setCsrfToken("")
    setUser(null)
    queryClient.clear()
  }, [queryClient])

  useEffect(() => {
    const uninstall = installFetchInterceptor(() => {
      setExpired(true)
      clear()
    })
    client
      .GET("/api/auth/me")
      .then(({ data }) => {
        if (data) {
          setCsrfToken(data.csrfToken)
          setUser(data.user)
        }
      })
      .catch(() => undefined)
      .finally(() => setLoading(false))
    return uninstall
  }, [clear])

  const login = useCallback(async (username: string, password: string) => {
    const session = await unwrap(client.POST("/api/auth/login", { body: { username, password } }))
    setCsrfToken(session.csrfToken)
    setExpired(false)
    setUser(session.user)
  }, [])

  const logout = useCallback(async () => {
    await client.POST("/api/auth/logout").catch(() => undefined)
    setExpired(false)
    clear()
  }, [clear])

  return <AuthContext.Provider value={{ user, loading, expired, login, logout }}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error("useAuth harus dipakai di dalam AuthProvider")
  return ctx
}
