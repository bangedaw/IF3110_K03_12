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

Hasil uji migration per batch pada dataset yang sama dicatat setelah mode batch selesai.

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
