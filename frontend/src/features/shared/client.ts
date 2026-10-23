import createClient from "openapi-fetch"
import type { paths } from "@/lib/api/schema"

interface ProblemLike {
  title: string
  status: number
  code: string
  detail?: string
  errors?: Record<string, string>
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly fields: Record<string, string>

  constructor(problem: ProblemLike) {
    super(problem.detail || problem.title)
    this.name = "ApiError"
    this.status = problem.status
    this.code = problem.code
    this.fields = problem.errors ?? {}
  }
}

const origin = globalThis.location?.origin ?? ""

export const client = createClient<paths>({
  baseUrl: origin,
  credentials: "same-origin",
  fetch: (request) => globalThis.fetch(request),
})

function isProblem(value: unknown): value is ProblemLike {
  return typeof value === "object" && value !== null && "code" in value && "status" in value
}

function toApiError(payload: unknown, response: Response): ApiError {
  if (isProblem(payload)) return new ApiError(payload)
  const body = (typeof payload === "object" && payload !== null ? payload : {}) as { detail?: string; title?: string }
  return new ApiError({
    title: body.detail || body.title || response.statusText || "Request failed",
    status: response.status,
    code: "UNKNOWN",
  })
}

interface Result<T> {
  data?: T
  error?: unknown
  response: Response
}

export async function unwrap<T = void>(request: Promise<Result<T>>): Promise<T> {
  const { data, error, response } = await request
  if (!response.ok) throw toApiError(error, response)
  return data as T
}

export async function postUntyped<T>(path: string, body: unknown): Promise<T> {
  const response = await fetch(`${origin}${path}`, {
    method: "POST",
    credentials: "same-origin",
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
  const payload: unknown = await response.json().catch(() => null)
  if (!response.ok) throw toApiError(payload, response)
  return payload as T
}

export function errorMessage(error: unknown, fallback = "Terjadi kesalahan"): string {
  if (error instanceof ApiError) {
    if (error.code === "DB_UNREACHABLE") return "Database target tidak terjangkau"
    if (error.code === "FORBIDDEN") return "Anda tidak punya akses untuk aksi ini"
    if (error.code === "UNAUTHENTICATED") return "Sesi tidak valid, silakan login kembali"
    return error.message
  }
  return error instanceof Error ? error.message : fallback
}
