# IF3110_K03_12 — Database Management Platform

Platform untuk mengatur akses ke database bersama, menjelajahi schema dan data, serta menjalankan
migration secara terkontrol. Backend Go (modular monolith) dengan tampilan **server-rendered**
(`html/template`, tanpa SPA).

- Keputusan desain (session, CSRF, akses multi-group, Platform Admin, dll.): [docs/decisions.md](docs/decisions.md)
- Arsitektur dan pembagian paket: [docs/architecture.md](docs/architecture.md)
- Kontrak JSON API: [api/openapi.yaml](api/openapi.yaml)

## Prasyarat

- Go ≥ 1.25, Docker
- GNU make (Windows: jalankan dari Git Bash; `winget install ezwinports.make`)
- gcc/cgo hanya untuk `make gen` (sqlc dijalankan via `go run`)

## Menjalankan untuk development

```bash
cp .env.example .env
make dev                                   # platform DB + target PostgreSQL ×2 + MySQL, dengan data seed
make run                                   # server di :8080 (migration platform DB otomatis)
cd backend && go run ./cmd/dbmpctl create-admin -username admin   # sekali saja
```

Buka http://localhost:8080 dan masuk sebagai admin. Dengan `APP_ENV=dev`, template dibaca langsung dari
`backend/internal/web/templates` sehingga perubahan terlihat tanpa menjalankan ulang server.

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

## Build satu binary (demo/production)

```bash
make build        # template dan aset statis di-embed → bin/dbmp dan bin/dbmpctl
./bin/dbmp        # halaman web + JSON API di :8080
```

Di production set `APP_ENV=production` (cookie `Secure`, wajib di belakang HTTPS) dan ganti
`DBMP_SECRET_KEY` serta `CSRF_KEY` dengan nilai acak (`openssl rand -base64 32`).

## Konfigurasi

| Env var | Default | Keterangan |
|---|---|---|
| `APP_ENV` | `dev` | `production` mengaktifkan cookie `Secure`; `dev` membaca template dari disk |
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
