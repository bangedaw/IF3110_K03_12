# IF3110_K03_12 — Database Management Platform

Backend Go (modular monolith) + frontend React SPA. Keputusan desain: [docs/decisions.md](docs/decisions.md).
Kontrak API: [api/openapi.yaml](api/openapi.yaml).

## Prasyarat

- Go ≥ 1.25, Docker, GNU make (Windows: jalankan dari Git Bash; `winget install ezwinports.make`)
- gcc/cgo untuk `make gen` (sqlc dijalankan via `go run`)

## Menjalankan backend

```bash
cp .env.example .env
make run            # menjalankan migration platform DB lalu listen di :8080
curl localhost:8080/api/healthz
```

## Generate kode

Setelah mengubah `api/openapi.yaml` atau `backend/internal/platformdb/queries/*.sql`:

```bash
make gen            # sqlc + oapi-codegen (+ openapi-typescript jika frontend ada)
```

Hasil generate di-commit; CI gagal jika tidak up to date.

## Test & lint

```bash
cd backend && go test ./...
make lint
```
