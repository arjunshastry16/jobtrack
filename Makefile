# Common tasks. Run `make help` to list them.
.DEFAULT_GOAL := help

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
