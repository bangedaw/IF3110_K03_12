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
serta dua group (`backend-team`: alice, carol; `analyst`: bob, carol):

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
  Ukuran batch boleh diubah saat melanjutkan. Batch yang sudah committed tidak diulang, termasuk saat
  pesan job terkirim dua kali.
- Pembatalan menjalankan SQL down per batch dari nomor batch terbesar ke terkecil memakai rentang yang
  tersimpan. Bila gagal di tengah, statusnya `partially_reverted` dan pembatalan dapat dilanjutkan.
  Eksekusi yang sudah dibatalkan, sebagian atau seluruhnya, tidak dapat dilanjutkan ke depan; forward
  dilakukan sebagai eksekusi baru. Migration tanpa SQL down ditolak.
- Selama eksekusi, koneksi worker memegang advisory lock database target sehingga eksekusi lain pada
  database yang sama ditolak, termasuk di antara commit batch.

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
- Span OpenTelemetry (`sql.query`, `sql.commit`, …) memakai tracer global dengan atribut `db.system.name`,
  `db.namespace`, `db.query.text`, `db.response.returned_rows`, dan `db.response.status_code`; query gagal
  diberi status error.

Contoh log saat membuka data tabel di explorer:

```json
{"time":"2026-11-17T03:15:53.058+07:00","level":"INFO","msg":"sql.query.end","event":"sql.query","target":"target","database":"inventory","server":"localhost:5433","actor":2,"request_id":"arch/nUR1aEmLhS-000002","query":"SELECT \"id\"::text, … FROM \"inventory\".\"products\" ORDER BY \"inventory\".\"products\".\"id\" ASC LIMIT 2 OFFSET 0","duration_ms":17.774,"rows":2,"status":"ok"}
```

## Migration: workflow, approval, dan riwayat

Kode: `backend/internal/migration`, UI: `frontend/src/features/migrations`, API: `api/openapi.yaml` (tag `migrations`).

**Definisi.** Satu migration punya SQL up dan SQL down, atau ditandai *irreversible* beserta alasannya. Mode batch
(opsional) menyimpan kolom dan ukuran batch. Setiap perubahan draft menaikkan `revision`. Setelah migration pernah
dieksekusi, `sqlLocked` menjadi `true` dan SQL tidak bisa diubah lagi; buat migration baru.

**Status.** `draft` → `pending_review` → `approved` → `pending_queue` → `running` → `success` atau `failed`.
Perpindahan di luar jalur ini ditolak (409). Penolakan dari satu leader mengembalikan migration ke `draft`.

**Approval.** Yang wajib menyetujui adalah leader setiap group yang punya akses ke database (View Only maupun
View + Edit); satu leader yang memimpin beberapa group dihitung satu suara. Hanya leader itu yang boleh memutuskan
(selain itu 403). Job baru dipublikasikan ke queue setelah semua leader setuju. Rollback dan forward memakai putaran
approval sendiri dengan aturan yang sama. Keputusan sementara (belum dijawab di Q&A): approval leader pengaju tetap
dihitung, dan group tanpa leader aktif tidak menghasilkan approver; bila tidak ada approver sama sekali, pengajuan ditolak.

**Aturan eksekusi.** Apply mengikuti urutan linear (`seq`). Rollback hanya untuk migration aktif terakhir dan bukan
irreversible. Forward menjalankan migration berikutnya menurut urutan, satu per pengajuan. Rollback yang gagal tidak
mengubah posisi aktif. Aturan yang sama diperiksa ulang oleh worker sebelum eksekusi (`Revalidate`).

**Riwayat.** `migration_executions` menyimpan satu baris per attempt, berhasil maupun gagal, lengkap dengan revisi,
pengguna, waktu, status, jenis, dan salinan SQL yang dijalankan. Rollback sukses merujuk execution yang dibatalkan.
Baris final tidak bisa diubah (trigger di database) dan tidak pernah dihapus oleh kode. Setiap baris juga ditulis ke
log aplikasi (`migration.execution.*`; SQL hanya dicatat sebagai hash dan panjang). `GET .../migration-history?hideRollback=true`
menyembunyikan pasangan (execution yang dibatalkan, rollback suksesnya); attempt gagal tetap tampil.

| Endpoint | Fungsi |
|---|---|
| `GET/POST /api/databases/{id}/migrations` | daftar / buat draft |
| `GET/PUT /api/migrations/{id}` | detail / ubah draft |
| `POST /api/migrations/{id}/submit` | ajukan review |
| `GET/POST /api/migrations/{id}/approvals` | daftar approver / setuju atau tolak |
| `POST /api/migrations/{id}/rollback`, `/forward` | ajukan rollback / forward |
| `GET /api/databases/{id}/migration-history` | riwayat eksekusi |

## Build satu binary (demo/production)

```bash
make build        # build SPA, salin ke backend/internal/spa/dist, embed → bin/dbmp dan bin/dbmpctl
./bin/dbmp        # SPA + JSON API di :8080; deep link seperti /databases/1 dilayani index.html
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
