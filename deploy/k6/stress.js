import http from "k6/http"
import { check, fail, sleep } from "k6"
import { Counter, Rate, Trend } from "k6/metrics"

const BASE = __ENV.BASE_URL || "http://localhost:8080"
const PASSWORD = __ENV.DEMO_PASSWORD || "demo12345"
const STEP = __ENV.STEP || "60s"

const levels = [
  { name: "normal", api: 10, jobs: 2 },
  { name: "tinggi", api: 25, jobs: 5 },
  { name: "stress", api: 50, jobs: 10 },
  { name: "pulih", api: 10, jobs: 2 },
]

const queueWait = new Trend("job_queue_wait", true)
const jobDuration = new Trend("job_duration", true)
const jobTotal = new Trend("job_total", true)
const jobsDone = new Counter("jobs_completed")
const jobSuccess = new Rate("job_success")

function stepSeconds() {
  return parseInt(STEP, 10)
}

const scenarios = {}
const thresholds = {
  "http_req_duration{type:api}": ["p(95)<500"],
  "http_req_failed{type:api}": ["rate<0.01"],
  "http_req_failed{type:job}": ["rate<0.01"],
  job_success: ["rate>0.99"],
}
levels.forEach((l, i) => {
  const startTime = `${i * stepSeconds()}s`
  scenarios[`api_${l.name}`] = { executor: "constant-vus", exec: "api", vus: l.api, duration: STEP, startTime, tags: { level: l.name } }
  scenarios[`jobs_${l.name}`] = { executor: "constant-vus", exec: "jobs", vus: l.jobs, duration: STEP, startTime, tags: { level: l.name }, gracefulStop: "60s" }
  thresholds[`http_req_duration{type:api,level:${l.name}}`] = ["p(95)<500"]
  thresholds[`http_req_failed{type:api,level:${l.name}}`] = ["rate<0.01"]
  thresholds[`http_reqs{type:api,level:${l.name}}`] = ["count>0"]
  thresholds[`job_queue_wait{level:${l.name}}`] = ["p(95)<600000"]
  thresholds[`job_total{level:${l.name}}`] = ["p(95)<600000"]
  thresholds[`job_duration{level:${l.name}}`] = ["p(95)<600000"]
  thresholds[`jobs_completed{level:${l.name}}`] = ["count>=0"]
})

export const options = { scenarios, thresholds, summaryTrendStats: ["avg", "med", "p(95)", "max"] }

function login(username, jar) {
  const res = http.post(`${BASE}/api/auth/login`, JSON.stringify({ username, password: PASSWORD }), {
    headers: { "Content-Type": "application/json" },
    jar,
    tags: { type: "login" },
  })
  if (res.status !== 200) fail(`login ${username}: ${res.status} ${res.body}`)
  return { jar, csrf: res.json("csrfToken") }
}

function call(session, method, path, body, tags, expected) {
  const params = {
    headers: { "Content-Type": "application/json", "X-CSRF-Token": session.csrf },
    jar: session.jar,
    tags,
  }
  if (expected) params.responseCallback = http.expectedStatuses(...expected)
  return http.request(method, `${BASE}${path}`, body === undefined ? null : JSON.stringify(body), params)
}

export function setup() {
  const admin = login("admin", new http.CookieJar())
  const groups = call(admin, "GET", "/api/admin/groups?limit=200", undefined, { type: "setup" }).json("items")
  const databases = call(admin, "GET", "/api/admin/databases?limit=200", undefined, { type: "setup" }).json("items")
  const team = groups.find((g) => g.name === "backend-team")
  const targets = databases.filter((d) => d.kind === "postgres").slice(0, 2)
  if (!team || targets.length < 2) fail("jalankan `dbmpctl seed-demo` lebih dulu")
  for (const d of targets) {
    const res = call(admin, "PUT", `/api/admin/groups/${team.id}/grants/${d.id}`, { level: "edit" }, { type: "setup" })
    if (res.status >= 300) fail(`grant ${d.name}: ${res.status} ${res.body}`)
  }
  return { databases: targets.map((d) => d.id) }
}

let reader
let alice
let bob

export function api(data) {
  if (!reader) reader = login("carol", new http.CookieJar())
  const db = data.databases[0]
  const tags = { type: "api" }
  const responses = [
    call(reader, "GET", "/api/databases", undefined, { ...tags, name: "databases" }),
    call(reader, "GET", `/api/databases/${db}/migrations`, undefined, { ...tags, name: "migrations" }),
    call(reader, "GET", `/api/databases/${db}/migration-history?hideRollback=true`, undefined, { ...tags, name: "history" }),
    call(reader, "GET", `/api/databases/${db}/schemas/inventory/tables/products/rows?limit=50`, undefined, { ...tags, name: "rows" }),
  ]
  check(responses, { "API 200": (rs) => rs.every((r) => r.status === 200) })
  sleep(0.5)
}

export function jobs(data) {
  if (!alice) {
    alice = login("alice", new http.CookieJar())
    bob = login("bob", new http.CookieJar())
  }
  const db = data.databases[(__VU + __ITER) % data.databases.length]
  const tags = { type: "job" }
  const created = call(alice, "POST", `/api/databases/${db}/migrations`, {
    title: `k6 vu${__VU} iter${__ITER}`,
    description: "stress test k6",
    upSql: "SELECT pg_sleep(0.2)",
    downSql: "SELECT 1",
    irreversible: false,
    irreversibleReason: "",
  }, { ...tags, name: "create" })
  if (!check(created, { "migration dibuat": (r) => r.status === 201 })) return
  const id = created.json("id")
  const submitted = call(alice, "POST", `/api/migrations/${id}/submit`, undefined, { ...tags, name: "submit" })
  if (!check(submitted, { "diajukan": (r) => r.status === 201 })) return
  const requestId = submitted.json("request.id")
  for (const leader of [alice, bob]) {
    const res = call(leader, "POST", `/api/migrations/${id}/approvals`, { requestId, decision: "approved", comment: "" }, { ...tags, name: "approve" }, [200, 403])
    if (res.status === 403) continue
    check(res, { "disetujui": (r) => r.status === 200 })
  }
  const executed = call(alice, "POST", `/api/migrations/${id}/execute`, { requestId }, { ...tags, name: "execute" })
  if (!check(executed, { "masuk antrean": (r) => r.status === 202 })) return
  const jobId = executed.json("jobId")

  let job
  const deadline = Date.now() + 300000
  while (Date.now() < deadline) {
    sleep(0.5)
    const res = call(alice, "GET", `/api/databases/${db}/jobs/${jobId}`, undefined, { type: "poll", name: "job" })
    if (res.status !== 200) continue
    job = res.json()
    if (["succeeded", "failed", "partial"].includes(job.status)) break
  }
  if (!job || !job.finishedAt) {
    jobSuccess.add(false)
    return
  }
  const created_ = Date.parse(job.createdAt)
  const started = Date.parse(job.startedAt)
  const finished = Date.parse(job.finishedAt)
  queueWait.add(started - created_)
  jobDuration.add(finished - started)
  jobTotal.add(finished - created_)
  jobsDone.add(1)
  jobSuccess.add(job.status === "succeeded")
}

export function handleSummary(data) {
  const out = { stdout: textSummary(data) }
  if (__ENV.SUMMARY) out[__ENV.SUMMARY] = JSON.stringify(data, null, 2)
  return out
}

function textSummary(data) {
  const m = data.metrics
  const v = (name, stat) => (m[name] && m[name].values[stat] !== undefined ? m[name].values[stat] : NaN)
  const lines = ["level   | req API | RPS API | p50 ms | p95 ms | error | job selesai | antre p95 ms | eksekusi p95 ms | total p95 ms"]
  for (const l of levels) {
    const reqs = v(`http_reqs{type:api,level:${l.name}}`, "count")
    lines.push([
      l.name.padEnd(7),
      String(reqs).padStart(7),
      (reqs / stepSeconds()).toFixed(1).padStart(7),
      v(`http_req_duration{type:api,level:${l.name}}`, "med").toFixed(1).padStart(6),
      v(`http_req_duration{type:api,level:${l.name}}`, "p(95)").toFixed(1).padStart(6),
      (v(`http_req_failed{type:api,level:${l.name}}`, "rate") * 100).toFixed(2).padStart(4) + "%",
      String(v(`jobs_completed{level:${l.name}}`, "count")).padStart(11),
      v(`job_queue_wait{level:${l.name}}`, "p(95)").toFixed(0).padStart(12),
      v(`job_duration{level:${l.name}}`, "p(95)").toFixed(0).padStart(15),
      v(`job_total{level:${l.name}}`, "p(95)").toFixed(0).padStart(12),
    ].join(" | "))
  }
  const failed = Object.entries(m).filter(([, x]) => x.thresholds && Object.values(x.thresholds).some((t) => !t.ok)).map(([k]) => k)
  lines.push("", `threshold gagal: ${failed.length ? failed.join(", ") : "tidak ada"}`)
  lines.push(`total job: ${v("jobs_completed", "count")}, sukses ${(v("job_success", "rate") * 100).toFixed(1)}%`)
  return lines.join("\n") + "\n"
}
