import { useQuery } from "@tanstack/react-query"
import { client, unwrap } from "@/features/shared/client"
import type { components } from "@/lib/api/schema"

export type Job = components["schemas"]["Job"]

const finished: ReadonlySet<Job["status"]> = new Set(["succeeded", "failed", "partial"])

export function isFinished(job: Job | undefined): boolean {
  return job !== undefined && finished.has(job.status)
}

export const jobKeys = {
  job: (databaseId: number, jobId: number) => ["jobs", databaseId, jobId] as const,
}

// useJob mem-polling status job selama job masih pending atau running, lalu berhenti sendiri saat job selesai
// atau saat request gagal (misalnya 403 atau 404), supaya tidak terus membebani API.
export function useJob(databaseId: number, jobId: number, pollMs = 2000) {
  return useQuery({
    queryKey: jobKeys.job(databaseId, jobId),
    queryFn: ({ signal }) => unwrap(client.GET("/api/databases/{databaseId}/jobs/{jobId}", { params: { path: { databaseId, jobId } }, signal })),
    refetchInterval: (query) => (query.state.status === "error" || isFinished(query.state.data) ? false : pollMs),
  })
}
