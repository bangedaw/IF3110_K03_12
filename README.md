# IF3110_K03_12 — Database Management Platform

Platform untuk mengatur akses ke database bersama, menjelajahi schema dan data, serta menjalankan
migration secara terkontrol. Backend Go (modular monolith) yang melayani JSON API di `/api` dan frontend
**Single-Page Application** (React, Vite, TypeScript) di `frontend/`. Hasil build SPA di-embed ke binary
server.

- Keputusan desain (session, CSRF, akses multi-group, Platform Admin, dll.): [docs/decisions.md](docs/decisions.md)
- Arsitektur dan pembagian paket: [docs/architecture.md](docs/architecture.md)
- Kontrak JSON API: [api/openapi.yaml](api/openapi.yaml)

## Prasyarat

- Go ≥ 1.25, Docker
- Node.js ≥ 24 dan npm (frontend)
- GNU make (Windows: jalankan dari Git Bash; `winget install ezwinports.make`)
- gcc/cgo hanya untuk `make gen` (sqlc dijalankan via `go run`)

## Menjalankan untuk development

```bash
cp .env.example .env
make dev                                   # platform DB + target PostgreSQL ×2 + MySQL, dengan data seed
make run                                   # API di :8080 (migration platform DB otomatis)
make run-worker                            # worker yang menjalankan migration dari antrean
make web-dev                               # SPA di :5173 dengan hot reload, /api diteruskan ke :8080
cd backend && go run ./cmd/dbmpctl create-admin -username admin   # sekali saja
```

Buka http://localhost:5173 dan masuk sebagai admin. Jalankan `cd frontend && npm ci` sekali sebelum
`make web-dev`. Bila SPA belum pernah di-build, `make run` hanya melayani `/api`; jalankan `make web`
agar http://localhost:8080 juga melayani SPA.

Test frontend: `cd frontend && npm test` (lint: `npm run lint`, tipe: `npm run typecheck`). Tipe API di
`frontend/src/lib/api/schema.d.ts` di-generate dari `api/openapi.yaml` oleh `make gen`.

`create-admin` meminta password tanpa menampilkannya (atau membaca stdin bila di-pipe). Menjalankan ulang
untuk username yang sama akan mereset password, mengaktifkan akun, dan me-revoke semua session-nya.

Setelah login sebagai admin, daftarkan database target di menu **Koneksi** (server berjalan di host,
jadi pakai `localhost`):

| Nama | DBMS | Port | Database | User / password |
|---|---|---|---|---|
| inventory | PostgreSQL | 5433 | `inventory` | `dbmp` / `dbmp` |
| inventory-staging | PostgreSQL | 5434 | `inventory` | `dbmp` / `dbmp` |
| sales | MySQL | 3307 | `sales` | `dbmp` / `dbmp` |

Lalu buat user dan group di menu **Pengguna** / **Group**, dan beri group akses View Only atau View + Edit
per database. Platform Admin sendiri tidak otomatis punya akses data; tambahkan ke group bila perlu.

### Data demo

Untuk mengisi skenario demo sekaligus (hanya untuk development, ditolak bila `APP_ENV=production`):

```bash
cd backend && go run ./cmd/dbmpctl seed-demo            # opsional: -password <sandi> -host <host>
```

Perintah ini idempotent dan membuat akun berikut dengan password `demo12345`, tiga koneksi di atas,
serta dua group (`backend-team`: alice sebagai leader, carol; `analyst`: bob sebagai leader, carol).
Migration pada `inventory` butuh persetujuan alice dan bob karena kedua group punya akses ke sana; carol
anggota keduanya tetapi bukan leader, jadi tidak ikut menyetujui.

| User | Akses efektif |
|---|---|
| admin | tidak punya akses data (hanya kelola pengguna, group, koneksi) |
| alice | inventory View + Edit, inventory-staging View Only |
| bob | inventory View Only, sales View Only |
| carol | inventory View + Edit, inventory-staging View Only, sales View Only |

### Dataset besar untuk uji migration

Dataset ini dipakai untuk membandingkan migration biasa dengan migration per batch. Tidak dimuat otomatis
oleh `make dev` karena butuh waktu dan ruang disk.

```bash
make seed-large                            # 2.000.000 baris di inventory (port 5433), ±8 detik
make seed-large LOADTEST_ROWS=500000       # ukuran lain; menjalankan ulang membuat ulang tabel
make bench-update                          # ukur UPDATE seluruh row tanpa batching (3 kali)
make bench-batch LOADTEST_BATCH=50000      # query yang sama per batch, lalu dibatalkan per batch
```

Tabel `loadtest.transactions` berisi `id` berurutan (kolom cursor batch), `account_id`, `amount`, `status`,
`note`, `created_at`, `updated_at`, dua index sekunder, serta kolom `amount_cents` yang masih kosong
sebagai target backfill. Query yang diukur:

```sql
UPDATE loadtest.transactions SET amount_cents = (amount * 100)::bigint, updated_at = now();
```

`bench-update` menjalankan query di dalam `BEGIN … ROLLBACK` lalu `VACUUM`, sehingga dataset tetap sama
untuk uji migration per batch dengan query yang sama.

| Lingkungan | Baris | Ukuran tabel + index | Durasi UPDATE tanpa batching |
|---|---|---|---|
| Intel Core i7-13650HX, RAM 11 GB, PostgreSQL 17.11 (Docker) | 2.000.000 | 361 MB | 17,3 s; 12,8 s; 12,7 s |

Migration per batch pada dataset dan query yang sama (`… WHERE id > $1 AND id <= $2`), lalu dibatalkan
per batch dengan `SET amount_cents = NULL`, sehingga dataset kembali seperti semula:

| Ukuran batch | Jumlah batch | Durasi up | Rata-rata per transaction | Durasi pembatalan |
|---|---|---|---|---|
| 10.000 | 200 | 13,3 s | 66 ms | 34,7 s |
| 50.000 | 40 | 13,4 s | 335 ms | 19,9 s |
| 200.000 | 10 | 12,6 s | 1,3 s | 15,4 s |

Total durasi hampir sama dengan `UPDATE` tunggal, tetapi tiap transaction hanya menahan lock baris selama
satu batch dan setiap batch langsung committed, sehingga kegagalan di tengah tidak membuang pekerjaan
sebelumnya. Pembatalan lebih lambat karena berjalan tepat setelah up tanpa `VACUUM`, sehingga setiap
`UPDATE` harus mencari ruang di antara versi baris lama.

### Mode migration per batch

Mode batch dijalankan oleh `dbms.BatchMigrator` (implementasi PostgreSQL di
`backend/internal/dbms/postgres/batch.go`). Aturannya:

- SQL up dan down masing-masing satu statement yang memakai `$1` (cursor terakhir, eksklusif) dan `$2`
  (batas batch, inklusif), misalnya `UPDATE t SET … WHERE id > $1 AND id <= $2`.
- Kolom batch wajib bertipe integer dan memiliki unique index satu kolom. Kolom tidak unik ditolak karena
  `LIMIT` dapat memotong nilai duplikat di batas batch sehingga sebagian baris terlewat.
- Cakupan ditentukan sekali di awal (`min(id) - 1` sampai `max(id)`), sehingga baris yang ditambahkan
  setelah eksekusi dimulai tidak ikut diproses. Batas tiap batch dihitung di database dengan
  `ORDER BY id LIMIT n`; data tidak pernah dimuat ke memory aplikasi.
- Setiap batch berjalan dalam transaction sendiri. Checkpoint (`_dbmp_batch_checkpoint`: execution,
  nomor batch, rentang cursor, jumlah row) ditulis di database target dalam transaction yang sama dengan
  perubahan batch, sehingga checkpoint selalu sesuai dengan data yang benar-benar committed.
- Melanjutkan eksekusi yang gagal memakai execution ID yang sama dan mulai dari checkpoint terakhir.
  Batch yang sudah committed tidak diulang, termasuk saat pesan job terkirim dua kali.
- Pembatalan menjalankan SQL down per batch dari nomor batch terbesar ke terkecil memakai rentang yang
  tersimpan. Bila gagal di tengah, statusnya `partially_reverted` dan pembatalan dapat dilanjutkan.
  Eksekusi yang sudah dibatalkan, sebagian atau seluruhnya, tidak dapat dilanjutkan ke depan; forward
  dilakukan sebagai eksekusi baru. Migration tanpa SQL down ditolak.
- Selama eksekusi, koneksi worker memegang advisory lock database target sehingga eksekusi lain pada
  database yang sama ditolak, termasuk di antara commit batch.
- Riwayat dan checkpoint disimpan di tabel `_dbmp_schema_history`, `_dbmp_batch_run`, dan
  `_dbmp_batch_checkpoint` pada database target. Prefix `_dbmp_` dicadangkan untuk platform: tabel
  berawalan itu tidak tampil di explorer, tidak ikut snapshot, dan tidak pernah muncul di SQL schema
  diff, sehingga sinkronisasi schema tidak bisa menghapus bukti eksekusi.

### Log dan span eksekusi SQL

Setiap koneksi PostgreSQL, baik ke database target maupun platform DB, memakai `sqltrace.Tracer`
(`backend/internal/sqltrace`), sehingga setiap query tercatat tanpa perlu instrumentasi di pemanggil. Ini
termasuk query read-only explorer dan schema diff, test koneksi, migration, serta tiap batch.

- Tiap query menghasilkan log `<event>.start` dan `<event>.end` (JSON lewat `log/slog` ke stdout). Event
  `sql.begin`, `sql.query`, `sql.commit`, dan `sql.rollback` tercatat terpisah.
- Atribut: `time`, `level`, `event`, `target` (`target`/`platform`), `database`, `server`, `actor`,
  `request_id`, `query`, `params` (hanya database target), `duration_ms`, `rows`, `status`, serta `error`
  dan `sqlstate` bila gagal. Migration menambahkan `database_id`, `migration_id`, `run_id`, `version`,
  `direction`, dan untuk mode batch `mode`, `batch_size`, `batch`.
- Kegiatan batch dicatat sebagai `migration.batch.committed`, `migration.batch.resume`,
  `migration.batch.completed`, dan `migration.batch.reverted`.
- Penyamaran: `PASSWORD '…'` dan `password=…` di teks SQL diganti `***`; parameter query platform DB
  (hash session dan password) tidak pernah dicatat, hanya jumlahnya; nilai `[]byte` hanya dicatat ukurannya.
- Span OpenTelemetry (`sql.query`, `sql.commit`, …) berisi atribut `db.system.name`, `db.namespace`,
  `db.query.text`, `db.response.returned_rows`, dan `db.response.status_code`; query gagal diberi status
  error. Span ini menjadi anak span request API atau span job di worker (lihat bagian berikut).

Contoh log saat membuka data tabel di explorer:

```json
{"time":"2026-11-17T03:15:53.058+07:00","level":"INFO","msg":"sql.query.end","event":"sql.query","target":"target","database":"inventory","server":"localhost:5433","actor":2,"request_id":"arch/nUR1aEmLhS-000002","query":"SELECT \"id\"::text, … FROM \"inventory\".\"products\" ORDER BY \"inventory\".\"products\".\"id\" ASC LIMIT 2 OFFSET 0","duration_ms":17.774,"rows":2,"status":"ok"}
```

### Trace dari request API sampai query di worker

`backend/internal/telemetry` memasang OpenTelemetry di `cmd/server` (`service.name` `dbmp-server`) dan
`cmd/worker` (`dbmp-worker`). Log JSON keluar ke **stdout**, span ke **stderr** (exporter console, satu
baris JSON per span). `OTEL_TRACES_EXPORTER=none` mematikan span; log tetap jalan.

Satu eksekusi migration berada di satu trace walaupun API dan worker berjalan di proses berbeda. Trace
context (W3C `traceparent`) disimpan di kolom `jobs.trace_context` saat job dipublikasikan dan dibaca lagi
oleh worker saat job diambil.

```
POST /api/migrations/{migrationId}/execute     span server, dbmp-server
└── (jobs.trace_context)
    └── queue.job                              dbmp-worker, induknya remote
        ├── queue.lock                         lock database
        ├── sql.query …                        revalidasi dan riwayat di platform DB
        ├── sql.query CREATE TABLE …           migration biasa: satu transaction
        └── migration.batch                    migration per batch: satu span per transaction batch
            ├── sql.query SELECT max(id) …     batas batch
            ├── sql.query UPDATE … $1 … $2     statement batch
            ├── sql.query INSERT … checkpoint  checkpoint di transaction yang sama
            └── sql.commit
```

- Span HTTP dibuat untuk setiap request `/api` kecuali `/api/healthz`, dinamai dengan pola route
  (`POST /api/migrations/{migrationId}/execute`), dan hanya mencatat path tanpa query string. Status 5xx
  menandai span error.
- `migration.batch` berisi `migration.direction` (`up`/`down`), `migration.batch.number`, rentang cursor,
  dan `migration.batch.rows`; batch yang gagal mencatat error dan status error. Pemulihan worker membuat span
  `queue.recover` di trace asal job.
- Setiap log yang ditulis di dalam request atau job otomatis membawa `trace_id`, `span_id`, `request_id`
  (ID request HTTP), dan `actor`. Log worker juga membawa `job_id`, `migration_request_id`, `database_id`,
  `attempt`, lalu `run_id` dan `migration_id` setelah revalidasi.
- Semua log dan span satu eksekusi bisa dicari dengan satu `trace_id`, misalnya
  `make run-worker 2> spans-worker.jsonl` lalu `grep <trace_id> spans-worker.jsonl`.

Contoh log satu job (diringkas):

```json
{"level":"INFO","msg":"job.start","event":"job.start","status":"running","trace_id":"4468d7c57b9351619a40ad8bf4cc1914","span_id":"e03429965e794401","job_id":10,"migration_request_id":20,"database_id":1,"actor":3,"attempt":1}
{"level":"INFO","msg":"lock.acquire","event":"lock.acquire","status":"acquired","trace_id":"4468d7c57b9351619a40ad8bf4cc1914","span_id":"48a4d1bf9e47f7a7","job_id":10,"migration_request_id":20,"database_id":1,"actor":3,"attempt":1}
{"level":"INFO","msg":"job.end","event":"job.end","status":"succeeded","duration_ms":3,"trace_id":"4468d7c57b9351619a40ad8bf4cc1914","span_id":"e03429965e794401","job_id":10,"migration_request_id":20,"database_id":1,"actor":3,"attempt":1,"run_id":1,"migration_id":7}
```

Contoh span dari alur yang sama (diringkas, satu baris per span). `Parent.SpanID` `queue.job` sama dengan
`SpanID` span HTTP, dan `Remote: true` menandakan induknya datang dari proses lain lewat tabel `jobs`:

```json
{"Name":"POST /api/migrations/{migrationId}/execute","SpanContext":{"TraceID":"4468d7c57b9351619a40ad8bf4cc1914","SpanID":"fcb930beb9612f62"},"Parent":{"SpanID":"0000000000000000","Remote":false},"SpanKind":2,"StartTime":"2026-11-22T12:08:47.8341545+07:00","EndTime":"2026-11-22T12:08:47.8351645+07:00","Attributes":[{"Key":"http.request.method","Value":"POST"},{"Key":"url.path","Value":"/api/migrations/7/execute"},{"Key":"dbmp.request_id","Value":"host/ygqdvzoFXP-000001"},{"Key":"http.route","Value":"/api/migrations/{migrationId}/execute"},{"Key":"http.response.status_code","Value":202}],"Status":{"Code":"Unset"},"Resource":[{"Key":"service.name","Value":"dbmp-server"}]}
{"Name":"queue.job","SpanContext":{"TraceID":"4468d7c57b9351619a40ad8bf4cc1914","SpanID":"e03429965e794401"},"Parent":{"SpanID":"fcb930beb9612f62","Remote":true},"SpanKind":1,"StartTime":"2026-11-22T12:08:47.8351645+07:00","EndTime":"2026-11-22T12:08:47.8351645+07:00","Attributes":[{"Key":"job.id","Value":10},{"Key":"job.migration_request_id","Value":20},{"Key":"job.database_id","Value":1},{"Key":"job.attempt","Value":1}],"Status":{"Code":"Unset"},"Resource":[{"Key":"service.name","Value":"dbmp-worker"}]}
{"Name":"sql.query","SpanContext":{"TraceID":"4468d7c57b9351619a40ad8bf4cc1914","SpanID":"ca4004a1c8cf50d1"},"Parent":{"SpanID":"e03429965e794401","Remote":false},"SpanKind":3,"StartTime":"2026-11-22T12:08:47.8351645+07:00","EndTime":"2026-11-22T12:08:47.8351645+07:00","Attributes":[{"Key":"db.system.name","Value":"postgresql"},{"Key":"db.query.text","Value":"CREATE TABLE t ()"},{"Key":"dbmp.target","Value":"target"},{"Key":"db.response.returned_rows","Value":0}],"Status":{"Code":"Unset"},"Resource":[{"Key":"service.name","Value":"dbmp-worker"}]}
```

Penjelasan lengkap, termasuk atribut log baku dan aturan penyamaran, ada di
[docs/architecture.md](docs/architecture.md#logging-dan-tracing). Test yang membuktikan rantai trace:
`TestOneTraceSpansFromAPIRequestToSQLInWorker` (`internal/queue`) dan
`TestBatchTracesEachTransactionWithItsQueries` (`internal/dbms/postgres`). Test bahwa credential, cookie
session, token CSRF, dan query string tidak masuk log maupun span: `TestLogsAndSpansNeverContainSecrets`
(`cmd/server`).

## Migration: workflow, approval, dan riwayat

Kode: `backend/internal/migration`, UI: `frontend/src/features/migrations`, API: `api/openapi.yaml` (tag
`migrations`). Antrean, lock, retry, dan pemulihan worker dijelaskan di
[docs/architecture.md](docs/architecture.md#kontrak-antar-ruas-milestone-2).

**Definisi.** Migration punya SQL up dan SQL down, atau ditandai *irreversible* beserta alasannya. Mode batch
(opsional) menyimpan schema, tabel, kolom, dan ukuran batch. Setiap perubahan draft menaikkan `revision`, dan
perubahan dari revisi basi ditolak (409). Begitu pernah dieksekusi, termasuk jika gagal, SQL terkunci; perbaikan
dibuat sebagai migration baru.

**Status.** Setiap apply, rollback, dan forward adalah satu *request* (`migration_requests`) dengan alur
Pending Review → Approved → Pending (Queue) → Running → Success / Failed / Partial. Posisi migration di riwayat
linear disimpan terpisah: belum diterapkan, aktif (punya nomor versi), atau sudah di-rollback.

**Approval.** Setiap group yang punya akses ke database, View Only maupun View + Edit, harus disetujui leader-nya.
Pengaju yang juga leader boleh menyetujui, dan leader beberapa group cukup memutuskan sekali. Group yang belum
punya leader membuat request tertahan. Satu penolakan menutup request; draft bisa diubah lalu diajukan lagi.

**Eksekusi.** Setelah disetujui, pengguna View + Edit menekan Execute. API membuat job dan langsung mengembalikan
job ID; SQL dijalankan `cmd/worker`. Sebelum menjalankan SQL, worker memeriksa ulang akses pelaksana, revisi,
kelengkapan approval, dan aturan urutan. Execute ganda memakai job yang sama. Request yang gagal bisa dijalankan
ulang, dan yang partial dilanjutkan dari checkpoint, tanpa approval baru selama approval-nya masih lengkap.

**Rollback dan forward.**
- Rollback hanya untuk migration aktif terakhir yang reversible, atau untuk membatalkan batch yang masih partial.
- Forward hanya untuk migration yang paling akhir di-rollback, satu migration per eksekusi. Setelah ada migration
  baru yang diterapkan, migration yang di-rollback tidak bisa di-forward lagi supaya riwayat tetap linear.
- Eksekusi yang gagal tidak mengubah posisi aktif, dan migration lain boleh dilanjutkan.
- Selama ada migration yang partial, migration lain di database yang sama ditolak.

**Riwayat.** `migration_runs` menyimpan satu baris per attempt, berhasil maupun gagal: execution ID, migration,
versi, revisi, pelaksana, waktu, status, jenis, attempt, dan salinan SQL yang dijalankan. Rollback mencatat
execution yang dibatalkannya. Baris yang sudah final ditolak trigger database jika diubah. Data yang sama keluar
di log aplikasi (`migration.execution.start` dan `migration.execution.end`), dengan credential di SQL disamarkan.
Hide rollback menyembunyikan pasangan eksekusi sukses dan rollback sukses yang membatalkannya berdasarkan
execution ID; attempt gagal tetap tampil.

```json
{"level":"INFO","msg":"migration.execution.start","event":"migration.execution.start","execution_id":1,"kind":"apply","revision":1,"version":1,"executed_by":2,"query":"CREATE TABLE inventory.notes (id int PRIMARY KEY, body text)","migration_id":1,"trace_id":"466ef71290782edf26e61c8412f16684","span_id":"bc03b48cc7a7137d","job_id":1,"migration_request_id":1,"database_id":1,"actor":2,"attempt":1}
```

| Endpoint | Fungsi |
|---|---|
| `GET/POST /api/databases/{id}/migrations` | daftar / buat draft |
| `GET/PUT/DELETE /api/migrations/{id}` | detail / ubah / hapus draft |
| `POST /api/migrations/{id}/submit`, `/rollback`, `/forward` | ajukan request ke review |
| `GET/POST /api/migrations/{id}/approvals` | approval per group / setuju atau tolak |
| `POST /api/migrations/{id}/execute` | masukkan request ke antrean, mengembalikan job ID |
| `POST /api/migrations/{id}/explain` | EXPLAIN SQL up |
| `GET /api/databases/{id}/migration-history` | riwayat eksekusi, `hideRollback=true` untuk menyembunyikan pasangan rollback |

## Stress test dengan k6

Skrip: [`deploy/k6/stress.js`](deploy/k6/stress.js). Jalankan setelah server, worker, dan data demo siap
(`make run`, `make run-worker`, `dbmpctl seed-demo`):

```bash
make loadtest                      # 4 tingkat beban × 60 detik, k6 lewat image Docker grafana/k6
make loadtest LOADTEST_STEP=10s    # versi singkat untuk mencoba skrip
```

**Isi skrip.**
- `setup` login sebagai admin dan memberi group `backend-team` akses View + Edit ke dua database PostgreSQL
  (inventory dan staging), supaya job dikirim ke database yang sama maupun berbeda.
- Skenario `api` (user carol, View Only) terus memanggil daftar database, daftar migration, riwayat eksekusi,
  dan data tabel `inventory.products`.
- Skenario `jobs` menjalankan alur lengkap pada waktu yang sama: alice membuat migration (`SELECT pg_sleep(0.2)`),
  mengajukannya, alice dan bob menyetujui, alice menekan Execute, lalu skrip memantau
  `GET /api/databases/{id}/jobs/{jobId}` sampai job selesai. Database bergantian antara inventory dan staging.
- Respons Execute (202) tidak dihitung sebagai keberhasilan. Job baru dihitung selesai setelah statusnya
  `succeeded`, `failed`, atau `partial`, dan waktunya diambil dari `createdAt`, `startedAt`, dan `finishedAt`.
- Beban normal ditetapkan 10 pengguna API dan 2 pengirim job. Beban naik bertahap ke 25 + 5 dan 50 + 10, lalu
  turun kembali ke 10 + 2 untuk melihat pemulihan.
- Threshold: p95 response time API < 500 ms dan error rate API < 1% (keseluruhan dan per tingkat), error rate
  request alur job < 1%, dan job sukses > 99%.

**Hasil** (laptop Intel Core i7-13650HX 20 thread, RAM 11 GB, server, worker dengan `WORKER_CONCURRENCY=4`,
dan PostgreSQL berjalan di mesin yang sama; exporter span `console`):

| Tingkat | Pengguna API + job | Request API | Throughput API | p50 | p95 | Error | Job selesai | Throughput job | Antre p95 | Eksekusi p95 | Total p95 |
|---|---|---|---|---|---|---|---|---|---|---|---|
| normal | 10 + 2 | 4.640 | 77,3 req/s | 4,1 ms | 8,4 ms | 0% | 97 | 1,6 job/s | 0,8 s | 0,3 s | 1,1 s |
| tinggi | 25 + 5 | 11.308 | 188,5 req/s | 4,5 ms | 25,1 ms | 0% | 108 | 1,8 job/s | 0,9 s | 17,4 s | 18,1 s |
| stress | 50 + 10 | 18.848 | 314,1 req/s | 9,9 ms | 152,6 ms | 0% | 107 | 1,8 job/s | 0,8 s | 34,5 s | 34,9 s |
| pulih | 10 + 2 | 4.140 | 69,0 req/s | 5,1 ms | 88,1 ms | 0% | 59 | 1,0 job/s | 0,8 s | 5,5 s | 5,7 s |

Total 371 job, semuanya sukses, dan tidak ada threshold yang dilanggar. *Antre* adalah waktu dari job dibuat
sampai pertama kali diambil worker. *Eksekusi* dihitung dari pengambilan pertama sampai selesai, termasuk waktu
menunggu lock database. *Total* adalah keduanya.

**Analisis.**
- **Normal.** API stabil di bawah 10 ms. Job menunggu worker kurang dari 1 detik (interval polling worker 1
  detik) dan selesai sekitar 1 detik setelah dibuat.
- **Tinggi.** Throughput API naik 2,4 kali dengan p95 tetap rendah. Throughput job tidak ikut naik dan tertahan di
  sekitar 1,8 job/s, karena setiap database hanya boleh menjalankan satu migration pada satu waktu dan hanya ada
  dua database. Job yang mendapati database sedang dipakai dikembalikan ke antrean dengan jeda 5 detik (tercatat
  162 kali di log worker sebagai `lock.acquire` berstatus `busy`), sehingga waktu eksekusi p95 naik ke 17 detik.
- **Stress.** p95 API naik ke 153 ms karena API, worker, dan PostgreSQL berbagi CPU di mesin yang sama, tetapi
  masih jauh di bawah threshold dan tidak ada request yang gagal. Antrean job memanjang (p95 total 35 detik),
  sementara waktu tunggu worker tetap di bawah 1 detik. Jadi yang menjadi batas adalah lock per database, bukan
  jumlah worker. Tidak ada retry, job gagal, maupun eksekusi ganda.
- **Pulih.** Setelah beban turun, sisa antrean dari tingkat stress masih diselesaikan di awal fase ini, sehingga
  p95 API (88 ms) dan waktu eksekusi job (5,5 detik) belum kembali ke angka normal. Error tetap 0% selama
  pemulihan. Ini sesuai desain: menahan migration berikutnya di database yang sama lebih diutamakan daripada
  throughput.

## SAST dengan CodeQL

Workflow: [`.github/workflows/codeql.yml`](.github/workflows/codeql.yml). CodeQL berjalan pada setiap push ke
`main`, setiap pull request, dan seminggu sekali. Hasilnya diunggah ke tab
[Security → Code scanning](https://github.com/bangedaw/IF3110_K03_12/security/code-scanning) dan disimpan
sebagai artifact SARIF.

**Cakupan.** Suite `security-extended`, untuk tiga bahasa:
- **Go:** seluruh `backend/`, di-build manual (`go build ./...`) supaya `cmd/server`, `cmd/worker`, dan
  `cmd/dbmpctl` ikut dianalisis.
- **JavaScript/TypeScript:** seluruh `frontend/src`.
- **GitHub Actions:** workflow di `.github/workflows`.

`frontend/node_modules`, `frontend/dist`, dan `backend/internal/spa/dist` dikecualikan karena bukan kode
sumber tim. CodeQL yang dipakai versi 2.27.6.

**Hasil scan pertama** (commit `a5a0ba3`): Go 9 temuan, JavaScript/TypeScript 0, Actions 4, total 13.

| # | Rule | Severity | Lokasi | Potensi dampak | Keputusan |
|---|---|---|---|---|---|
| 5, 6 | `go/incorrect-integer-conversion` | high | `backend/internal/dbconn/handler.go:56` | `offset` atau `limit` dari query string dikonversi ke `int32` tanpa batas atas. `offset=4294967296` meluap menjadi 0, sehingga API mengembalikan halaman pertama, bukan halaman kosong | **Valid, diperbaiki.** Nilai diparsing langsung sebagai bilangan 32-bit, dan nilai di atas batas dipotong ke batas maksimum. Test `TestListAndGet` menambahkan kasus overflow, nilai negatif, dan nilai bukan angka |
| 1, 2, 3 | `actions/missing-workflow-permissions` | medium | `.github/workflows/ci.yml` (job `backend`, `contract`, `frontend`) | `GITHUB_TOKEN` mendapat izin bawaan repository. Jika salah satu langkah CI disusupi, token bisa dipakai untuk menulis ke repository | **Valid, diperbaiki** dengan `permissions: contents: read` di tingkat workflow. CI hanya membaca kode |
| 4 | `actions/unpinned-tag` | medium | `.github/workflows/ci.yml:21` | `golangci/golangci-lint-action@v8` mengikuti tag yang bisa dipindahkan pemiliknya, sehingga kode action pihak ketiga bisa berubah tanpa sepengetahuan tim | **Valid, diperbaiki.** Action di-pin ke commit `4afd733a84b1f43292c63897423277bb7f4313a9`, yaitu isi tag `v8` saat ini, jadi perilakunya tidak berubah. Action resmi GitHub (`actions/*`) tidak ditandai CodeQL dan tetap memakai tag |
| 7 | `go/sql-injection` | high | `backend/internal/dbms/mysql/browser.go:69` | Query data viewer MySQL disusun dari nama schema, tabel, dan kolom yang berasal dari URL | **False positive.** Sebelum query disusun, nama tabel dicek keberadaannya dan daftar kolom diambil dari `information_schema` dengan query berparameter. Semua identifier di-escape dengan backtick (`backtick` menggandakan karakter backtick di dalam nama), sedangkan `limit` dan `offset` berupa integer yang dibatasi `normalizePage`. Tidak ada nilai dari pengguna yang masuk ke query sebagai teks SQL. Hak akses tetap diperiksa (View Only) sebelum query dijalankan |
| 8–12 | `go/log-injection` | medium | `backend/internal/httpx/respond.go:42`, `:47` | Path URL dan pesan error dari input pengguna ditulis ke log, sehingga penyerang mungkin bisa memalsukan baris log | **False positive.** Semua log ditulis dengan `slog.JSONHandler`, yang meng-escape baris baru, tanda kutip, dan karakter kontrol, sehingga input selalu menjadi satu nilai string di dalam satu baris JSON. `TestLogsCannotBeForgedThroughRequestValues` (`internal/httpx`) membuktikan path dan error berisi `\n{"msg":"palsu"}` tetap menghasilkan satu baris per log tanpa entri palsu. Data sensitif tetap disaring (lihat `TestLogsAndSpansNeverContainSecrets`) |
| 13 | `go/log-injection` | medium | `backend/internal/httpx/router.go:40` | Sama seperti di atas, untuk log akses HTTP (`path`) | **False positive**, dengan alasan dan test yang sama |

**Perbaikan dan hasil scan ulang.** Temuan valid diperbaiki di commit `12e83b3` (`fix(security): bound pagination ints
and restrict CI token permissions`, PR #22). Scan ulang pada commit `ab8c569`:

| Bahasa | Scan pertama (`a5a0ba3`) | Scan ulang (`ab8c569`) |
|---|---|---|
| Go | 9 | 7 (semuanya false positive di atas) |
| JavaScript/TypeScript | 0 | 0 |
| GitHub Actions | 4 | 0 |

GitHub menandai temuan #1–#6 sebagai *fixed* secara otomatis. Temuan #7–#13 ditandai *dismissed: false positive*
di tab Security dengan rujukan ke bagian ini.

Hasil bersih tidak berarti aplikasi pasti aman. CodeQL tidak memeriksa konfigurasi deployment, dependensi pihak
ketiga, maupun logika otorisasi tingkat bisnis; bagian itu diuji lewat integration test akses.

## Build satu binary (demo/production)

```bash
make build        # build SPA, salin ke backend/internal/spa/dist, embed → bin/dbmp, bin/dbmp-worker, dan bin/dbmpctl
./bin/dbmp        # SPA + JSON API di :8080; deep link seperti /databases/1 dilayani index.html
./bin/dbmp-worker # worker, dijalankan sebagai proses terpisah
```

Di production set `APP_ENV=production` (cookie `Secure`, wajib di belakang HTTPS) dan ganti
`DBMP_SECRET_KEY` serta `CSRF_KEY` dengan nilai acak (`openssl rand -base64 32`).

## Konfigurasi

| Env var | Default | Keterangan |
|---|---|---|
| `APP_ENV` | `dev` | `production` mengaktifkan cookie `Secure` |
| `HTTP_ADDR` | `:8080` | alamat listen |
| `DATABASE_URL` | — (wajib) | platform DB |
| `DBMP_SECRET_KEY` | — (wajib) | 32 byte base64, enkripsi password database target |
| `CSRF_KEY` | — (wajib) | kunci HMAC token CSRF |
| `SESSION_IDLE_TIMEOUT` | `30m` | idle timeout |
| `SESSION_ABSOLUTE_TIMEOUT` | `8h` | absolute expiration |
| `SESSION_ROTATION_INTERVAL` | `15m` | rotasi periodik |
| `SESSION_GRACE_PERIOD` | `30s` | masa berlaku identifier lama setelah rotasi |
| `SESSION_TOUCH_INTERVAL` | `60s` | throttle update aktivitas |
| `OTEL_TRACES_EXPORTER` | `console` | `console` menulis span OpenTelemetry sebagai JSON ke stderr, `none` mematikannya |
| `WORKER_CONCURRENCY` | `4` | jumlah job yang dijalankan paralel oleh satu proses worker |

Untuk demo session, perpendek misalnya `SESSION_IDLE_TIMEOUT=1m SESSION_ROTATION_INTERVAL=20s`.

## Test & lint

```bash
make test                       # unit + integration, termasuk halaman web (butuh Docker untuk testcontainers)
make test-race
make lint
```

Di Windows/Docker Desktop, testcontainers perlu `TESTCONTAINERS_RYUK_DISABLED=true`, dan sebaiknya
dijalankan berurutan (`go test -p 1 ./...`) agar Docker tidak kewalahan. Container test tidak dibersihkan
otomatis tanpa ryuk:

```bash
docker rm -f $(docker ps -aq --filter label=org.testcontainers=true)
```

## Generate kode

Setelah mengubah `api/openapi.yaml` atau `backend/internal/platformdb/queries/*.sql`:

```bash
make gen          # sqlc + oapi-codegen
```

Hasil generate di-commit; CI gagal jika tidak up to date.
