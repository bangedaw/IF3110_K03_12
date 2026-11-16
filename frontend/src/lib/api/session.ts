const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"])
export const CSRF_HEADER = "X-CSRF-Token"

let csrfToken = ""

export function setCsrfToken(token: string) {
  csrfToken = token
}

function urlOf(input: RequestInfo | URL): URL {
  const raw = input instanceof Request ? input.url : input.toString()
  return new URL(raw, globalThis.location?.origin ?? "http://localhost")
}

export function installFetchInterceptor(onUnauthorized: () => void): () => void {
  const inner = globalThis.fetch
  globalThis.fetch = async (input, init) => {
    const url = urlOf(input)
    const method = (init?.method ?? (input instanceof Request ? input.method : "GET")).toUpperCase()
    if (csrfToken && !SAFE_METHODS.has(method) && url.origin === globalThis.location?.origin) {
      const headers = new Headers(init?.headers ?? (input instanceof Request ? input.headers : undefined))
      headers.set(CSRF_HEADER, csrfToken)
      init = { ...init, headers }
    }
    const response = await inner(input, init)
    if (response.status === 401 && !url.pathname.startsWith("/api/auth/")) onUnauthorized()
    return response
  }
  return () => {
    globalThis.fetch = inner
  }
}
