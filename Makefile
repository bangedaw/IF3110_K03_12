SHELL := bash

SQLC_VERSION         := v1.29.0
OAPI_CODEGEN_VERSION := v2.4.1
GOLANGCI_VERSION     := v2.5.0

COMPOSE := docker compose -f deploy/docker-compose.yml

.PHONY: gen lint run build web web-dev dev down seed reseed seed-large bench-update check test test-race

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

LOADTEST_ROWS ?= 2000000

seed-large: dev
	$(COMPOSE) exec -T target-pg-1 psql -U dbmp -d inventory -v rows=$(LOADTEST_ROWS) < deploy/seed/pg1_loadtest.sql

bench-update:
	bash deploy/bench-update.sh

check:
	bash deploy/check.sh

test:
	cd backend && go test ./...

test-race:
	cd backend && go test -race ./...

gen:
	cd backend && go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) generate
	cd backend && go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) -config oapi-codegen.yaml ../api/openapi.yaml
	cd frontend && npm run gen:api

lint:
	cd backend && go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION) run

run:
	cd backend && go run ./cmd/server

web:
	cd frontend && npm ci && npm run build
	find backend/internal/spa/dist -mindepth 1 ! -name .gitkeep -delete
	cp -r frontend/dist/. backend/internal/spa/dist/

web-dev:
	cd frontend && npm run dev

build: web
	cd backend && go build -o ../bin/dbmp ./cmd/server && go build -o ../bin/dbmpctl ./cmd/dbmpctl
