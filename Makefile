# Common tasks. Run `make help` to list them.
.DEFAULT_GOAL := help

# Load local settings (JOBTRACK_*) from .env if it exists, and pass them to commands.
-include .env
export

# The race detector needs a 48-bit address space. The Raspberry Pi 5 kernel only
# provides 47 bits, so it is off by default on ARM; CI (x86_64) always runs it.
# Override with `make test RACE=1` or `make test RACE=0`.
ifeq ($(shell uname -m),aarch64)
RACE ?= 0
else
RACE ?= 1
endif
RACE_FLAG := $(if $(filter 1,$(RACE)),-race,)

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

.PHONY: run
run: ## Run the API locally (listens on 127.0.0.1:8090)
	go run ./cmd/api

.PHONY: dev
dev: db-up migrate run ## Start the dev database, migrate it, run the API

COMPOSE_DEV := docker compose -f compose.dev.yml

.PHONY: db-up
db-up: ## Start the dev Postgres and wait until it is healthy
	$(COMPOSE_DEV) up -d --wait

.PHONY: db-down
db-down: ## Stop the dev Postgres (data is kept)
	$(COMPOSE_DEV) down

.PHONY: db-psql
db-psql: ## Open a psql shell on the dev database
	$(COMPOSE_DEV) exec postgres psql -U jobtrack -d jobtrack

.PHONY: migrate
migrate: ## Apply pending migrations
	go run ./cmd/migrate up

.PHONY: migrate-down
migrate-down: ## Roll back the latest migration
	go run ./cmd/migrate down

.PHONY: migrate-status
migrate-status: ## Show which migrations are applied
	go run ./cmd/migrate status

.PHONY: test
test: ## Run all tests (with the race detector where supported)
	go test $(RACE_FLAG) -count=1 ./...

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run ./...

.PHONY: fmt
fmt: ## Format code (gofmt + goimports via golangci-lint)
	golangci-lint fmt ./...

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	go mod tidy
