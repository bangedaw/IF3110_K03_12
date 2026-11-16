export type MigrationStatus = "draft" | "applied";
export interface Migration {
  id: number; databaseId: number; title: string; description: string; sql: string;
  checksum: string; status: MigrationStatus; version?: number | null;
  baseVersion: number; revision: number; createdAt: string; updatedAt: string;
}
export interface MigrationRun {
  id: number; migrationId: number; databaseId: number; status: "running"|"succeeded"|"failed";
  startedAt: string; finishedAt?: string; errorCode?: string; errorMessage?: string;
  failedStatementIndex?: number;
}
const request = async <T>(url: string, init?: RequestInit): Promise<T> => {
  const res = await fetch(url, { credentials: "same-origin", ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) } });
  if (!res.ok) {
    const problem = await res.json().catch(() => ({}));
    throw new Error(problem.detail ?? `Request failed (${res.status})`);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
};
const base = (db: number) => `/api/databases/${db}`;
export const migrationApi = {
  list: (db: number, status?: MigrationStatus) =>
    request<{items: Migration[]; limit: number; offset: number}>(`${base(db)}/migrations${status ? `?status=${status}` : ""}`),
  get: (db: number, id: number) => request<Migration>(`${base(db)}/migrations/${id}`),
  create: (db: number, body: Pick<Migration,"title"|"description"|"sql">) =>
    request<Migration>(`${base(db)}/migrations`, {method:"POST",body:JSON.stringify(body)}),
  update: (db: number, id: number, body: Pick<Migration,"title"|"description"|"sql"> & {revision:number}) =>
    request<Migration>(`${base(db)}/migrations/${id}`, {method:"PATCH",body:JSON.stringify(body)}),
  remove: (db: number,id:number,revision:number) =>
    request<void>(`${base(db)}/migrations/${id}?revision=${revision}`,{method:"DELETE"}),
  execute: (db:number,id:number,expectedRevision:number,acknowledgeHeadChanged=false) =>
    request<MigrationRun>(`${base(db)}/migrations/${id}/execute`,{method:"POST",body:JSON.stringify({expectedRevision,acknowledgeHeadChanged})}),
  runs: (db:number,id:number) => request<MigrationRun[]>(`${base(db)}/migrations/${id}/runs`),
  history: (db:number) => request<{items:MigrationRun[];limit:number;offset:number}>(`${base(db)}/history`),
  explain: (db:number,id:number,analyze:boolean) =>
    request<unknown>(`${base(db)}/migrations/${id}/explain`,{method:"POST",body:JSON.stringify({analyze})}),
};
