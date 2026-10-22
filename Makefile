SHELL := bash

SQLC_VERSION         := v1.29.0
OAPI_CODEGEN_VERSION := v2.4.1
GOLANGCI_VERSION     := v2.5.0

COMPOSE := docker compose -f deploy/docker-compose.yml

.PHONY: gen lint run dev down seed reseed check test test-race

dev:
	$(COMPOSE) up -d --wait

down:
	$(COMPOSE) down

# Seed dimuat otomatis saat container pertama dibuat (mount ke
# /docker-entrypoint-initdb.d); target ini memastikan container sehat dan seed ada.
seed: dev
	bash deploy/check.sh

reseed:
	$(COMPOSE) down -v
	$(COMPOSE) up -d --wait
	bash deploy/check.sh

check:
	bash deploy/check.sh

test:
	cd backend && go test ./...

test-race:
	cd backend && go test -race ./...

gen:
	cd backend && go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) generate
	cd backend && go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) -config oapi-codegen.yaml ../api/openapi.yaml
	if [ -f frontend/package.json ]; then cd frontend && pnpm exec openapi-typescript ../api/openapi.yaml -o src/lib/api/schema.d.ts; fi

lint:
	cd backend && go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION) run

run:
	cd backend && go run ./cmd/server
