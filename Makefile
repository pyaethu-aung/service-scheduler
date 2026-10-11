# Service scheduler developer workflow. Run `make help` for the target list.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

# Local configuration. Copy .env.example to .env (make setup does it once).
-include .env
export

BIN := $(CURDIR)/bin

# Pinned tool versions. Bumping one reinstalls it on the next make call.
GOLANGCI_LINT_VERSION := v2.14.0
SQLC_VERSION          := v1.31.1
OAPI_CODEGEN_VERSION  := v2.8.0
GOOSE_VERSION         := v3.28.0
GOVULNCHECK_VERSION   := v1.9.0

GOLANGCI_LINT := $(BIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)
SQLC          := $(BIN)/sqlc-$(SQLC_VERSION)
OAPI_CODEGEN  := $(BIN)/oapi-codegen-$(OAPI_CODEGEN_VERSION)
GOOSE         := $(BIN)/goose-$(GOOSE_VERSION)
GOVULNCHECK   := $(BIN)/govulncheck-$(GOVULNCHECK_VERSION)

OPENAPI_SPEC  := docs/openapi/service-scheduler.yaml
GENERATED     := internal/api/openapi.gen.go internal/db
COVERAGE_MIN  ?= 85

.PHONY: help
help: ## List targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## --- Setup -----------------------------------------------------------------

.PHONY: setup
setup: tools hooks ## Install tools, enable git hooks, create .env
	@test -f .env || { cp .env.example .env && echo "created .env from .env.example"; }

.PHONY: tools
tools: $(GOLANGCI_LINT) $(SQLC) $(OAPI_CODEGEN) $(GOOSE) $(GOVULNCHECK) ## Install pinned tools into ./bin

.PHONY: hooks
hooks: ## Point git at .githooks
	git config core.hooksPath .githooks

$(GOLANGCI_LINT):
	GOBIN=$(BIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	mv $(BIN)/golangci-lint $@

$(SQLC):
	GOBIN=$(BIN) go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
	mv $(BIN)/sqlc $@

$(OAPI_CODEGEN):
	GOBIN=$(BIN) go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)
	mv $(BIN)/oapi-codegen $@

$(GOOSE):
	GOBIN=$(BIN) go install github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)
	mv $(BIN)/goose $@

$(GOVULNCHECK):
	GOBIN=$(BIN) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	mv $(BIN)/govulncheck $@

## --- Local stack -----------------------------------------------------------

.PHONY: up
up: ## Start api + postgres (COMPOSE_PROFILES=observability adds the o11y stack)
	API_OTLP_ENDPOINT=$(if $(findstring observability,$(COMPOSE_PROFILES)),otel-collector:4317,) \
		docker compose up -d --build --wait

.PHONY: down
down: ## Stop every compose service, including the observability profile
	docker compose --profile observability down

.PHONY: run
run: ## Run the API on the host against DATABASE_URL
	go run ./cmd/server

.PHONY: migrate
migrate: $(GOOSE) ## Apply all migrations to DATABASE_URL
	@if ls migrations/*.sql >/dev/null 2>&1; then \
		$(GOOSE) -dir migrations postgres "$(DATABASE_URL)" up; \
	else echo "no migrations yet"; fi

.PHONY: migrate-status
migrate-status: $(GOOSE) ## Show migration status
	$(GOOSE) -dir migrations postgres "$(DATABASE_URL)" status

.PHONY: seed
seed: ## Load idempotent seed data into DATABASE_URL (safe to re-run)
	go run ./cmd/seed

## --- Code generation -------------------------------------------------------

.PHONY: generate
generate: sqlc-gen openapi-gen ## Run every code generator

.PHONY: sqlc-gen
sqlc-gen: $(SQLC) ## Generate internal/db from migrations and queries
	@if ls internal/db/queries/*.sql >/dev/null 2>&1; then \
		$(SQLC) generate; \
	else echo "no sqlc queries yet"; fi

.PHONY: openapi-gen
openapi-gen: $(OAPI_CODEGEN) ## Generate internal/api from the OpenAPI document
	$(OAPI_CODEGEN) -config oapi-codegen.yaml $(OPENAPI_SPEC)

.PHONY: check-generated
check-generated: generate ## Fail if generated code is out of sync with its sources
	@# Unstaged edits and untracked files under the generated paths, counted
	@# only when they carry the generated-code header: hand-written files
	@# there, such as tests, are not generator output.
	@stale="$$(for f in $$(git diff --name-only -- $(GENERATED); git ls-files --others --exclude-standard -- $(GENERATED)); do \
			[ -f "$$f" ] && grep -q -E '^// Code generated .* DO NOT EDIT\.$$' "$$f" && echo "$$f"; \
		done)"; \
	if [ -n "$$stale" ]; then \
		echo "generated code is out of sync: run 'make generate' and commit the result"; \
		echo "$$stale"; \
		exit 1; \
	fi; true

.PHONY: tidy-check
tidy-check: ## Fail if go.mod or go.sum need go mod tidy
	@go mod tidy -diff || { echo "run 'go mod tidy' and commit the result"; exit 1; }

## --- Quality gates ---------------------------------------------------------

.PHONY: lint
lint: $(GOLANGCI_LINT) ## Run golangci-lint
	$(GOLANGCI_LINT) run

.PHONY: test
test: ## Unit tests with the race detector (no Docker needed)
	go test -race ./...

.PHONY: test-integration
test-integration: ## Unit and integration tests (needs Docker for testcontainers)
	go test -race -tags=integration ./...

.PHONY: test-concurrency
test-concurrency: ## The NFR1 proof: 50 concurrent bookings for one free bay (needs Docker)
	go test -race -tags=integration -count=1 -run '^TestConcurrentBooking' -v ./internal/api/

.PHONY: coverage
coverage: ## All tests with coverage; fails below COVERAGE_MIN% (generated code excluded)
	scripts/coverage.sh $(COVERAGE_MIN)

.PHONY: test-api
test-api: ## Contract tests: handler responses validated against the OpenAPI document
	go test -race -tags=integration -run '^TestContract' ./...

.PHONY: vuln
vuln: $(GOVULNCHECK) ## Scan dependencies for known vulnerabilities
	$(GOVULNCHECK) ./...
