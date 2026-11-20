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

# Stamped into the binary and reported by /healthz.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-13s\033[0m %s\n", $$1, $$2}'

.PHONY: run
run: ## Run the API locally (listens on 127.0.0.1:8091)
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
	go run ./cmd/api migrate up

.PHONY: migrate-down
migrate-down: ## Roll back the latest migration
	go run ./cmd/api migrate down

.PHONY: migrate-status
migrate-status: ## Show which migrations are applied
	go run ./cmd/api migrate status

SQLC_IMAGE := sqlc/sqlc:1.31.1

.PHONY: generate
generate: ## Regenerate internal/store from SQL (runs sqlc in Docker)
	docker run --rm --user "$$(id -u):$$(id -g)" --volume "$(CURDIR):/src" --workdir /src $(SQLC_IMAGE) generate

.PHONY: build
build: web-build ## Build bin/jobtrack: the API with the web UI embedded (needs web-install once)
	go build -tags webui -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/jobtrack ./cmd/api

.PHONY: image
image: ## Build the container image jobtrack:local for this machine's platform
	docker build --build-arg VERSION=$(VERSION) -t jobtrack:local .

.PHONY: test-deploy
test-deploy: ## Test deploy/deploy.sh (deploy, rollback, skip, lock) on a throwaway stack and registry
	scripts/test-deploy.sh

.PHONY: test
test: ## Run all tests (with the race detector where supported)
	go test $(RACE_FLAG) -count=1 ./...

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run ./...

# Pinned tool images for the ops files: workflows (actionlint, which also shellchecks
# their run: steps) and shell scripts.
ACTIONLINT_IMAGE := rhysd/actionlint:1.7.12@sha256:b1934ee5f1c509618f2508e6eb47ee0d3520686341fec936f3b79331f9315667
SHELLCHECK_IMAGE := koalaman/shellcheck:v0.11.0@sha256:61862eba1fcf09a484ebcc6feea46f1782532571a34ed51fedf90dd25f925a8d

.PHONY: lint-ops
lint-ops: ## Lint GitHub workflows, shell scripts, the production compose files and Prometheus's config (dashboards: make test)
	docker run --rm --volume "$(CURDIR):/repo:ro" --workdir /repo $(ACTIONLINT_IMAGE) -no-color
	docker run --rm --volume "$(CURDIR):/repo:ro" --workdir /repo $(SHELLCHECK_IMAGE) scripts/*.sh deploy/*.sh deploy/observability/*.sh
	JOBTRACK_IMAGE=check JOBTRACK_DB_PASSWORD=check JOBTRACK_ALLOWED_LOGINS=check@example \
		docker compose --file deploy/compose.yml --env-file /dev/null config --quiet
	JOBTRACK_MONITOR_DB_PASSWORD=check GRAFANA_ADMIN_LOGIN=check@example GRAFANA_ADMIN_PASSWORD=check \
	GRAFANA_ROOT_URL=https://pi.example:7443/ \
		docker compose --file deploy/observability/compose.yml --env-file /dev/null config --quiet
	docker run --rm --volume "$(CURDIR)/deploy/observability:/cfg:ro" --entrypoint promtool $(PROMETHEUS_IMAGE) \
		check config /cfg/prometheus.yml

# promtool comes from the image the stack runs, so the check and production never disagree.
PROMETHEUS_IMAGE := $(shell sed -n 's/^ *image: \(prom\/prometheus:.*\)$$/\1/p' deploy/observability/compose.yml)

.PHONY: fmt
fmt: ## Format code (gofmt + goimports via golangci-lint)
	golangci-lint fmt ./...

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	go mod tidy

# Frontend (web/). Needs Node 22.12+ (see web/.nvmrc).

.PHONY: web-install
web-install: ## Install the frontend's exact locked dependencies
	cd web && npm ci

.PHONY: web-dev
web-dev: ## Run the frontend dev server on 127.0.0.1:5173 (proxies the API; run `make run` too)
	cd web && npm run dev

.PHONY: web-generate
web-generate: ## Regenerate the frontend's API types from docs/openapi.yaml
	cd web && npm run generate

.PHONY: web-test
web-test: ## Run the frontend's unit tests (Vitest)
	cd web && npm test

.PHONY: web-build
web-build: ## Type-check and build the frontend into internal/webui/dist
	cd web && npm run build
