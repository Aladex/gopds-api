.PHONY: help verify bootstrap build build-bin build-frontend backend frontend frontend-placeholder swagger \
	fmt-frontend fmt-frontend-check deps deps-frontend test test-backend test-integration test-frontend test-coverage clean lint lint-new lint-frontend lint-frontend-new fmt staticcheck security \
	docker-build docker-run docker-compose-up docker-compose-down dev migrate-up release pre-commit \
	db-dump db-restore db-seed db-reset migrate-plan search-eval-baseline search-eval-compare

# These targets are ordered pipelines, not independent compile units: bootstrap
# must finish before tests, and dependencies must be installed before the build
# that uses them. Nothing here gains from parallelism, and running with -j
# silently breaks the order, so keep the whole makefile serial.
.NOTPARALLEL:

FRONTEND_DIR := booksdump-frontend

# Pinned tool versions — keep in sync with Dockerfile and .github/workflows.
# versions_test.go guards that these stay aligned.
SWAG_VERSION        := v1.16.6
GOLANGCI_VERSION    := v2.12.2
GOSEC_VERSION       := v2.28.0
STATICCHECK_VERSION := 2026.1

# Base revision the new-code lint gate compares against; CI overrides it per PR.
LINT_BASE           ?= origin/master

# Default target
help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Development
deps: deps-frontend ## Install all dependencies
	@echo "Installing Go dependencies..."
	go mod download
	go mod tidy

deps-frontend: ## Install frontend dependencies from the lockfile
	@echo "Installing frontend dependencies..."
	cd $(FRONTEND_DIR) && yarn install --frozen-lockfile

build-frontend: ## Production frontend build (the real embed input)
	@echo "Building frontend..."
	cd $(FRONTEND_DIR) && yarn build

frontend: build-frontend ## Alias for build-frontend

frontend-placeholder: ## Install the embed placeholder when no real frontend build exists
	@if [ -f $(FRONTEND_DIR)/build/index.html ]; then \
		echo "Frontend build present, keeping it."; \
	else \
		echo "No frontend build found, installing embed placeholder..."; \
		mkdir -p $(FRONTEND_DIR)/build; \
		cp $(FRONTEND_DIR)/placeholder/index.html $(FRONTEND_DIR)/build/index.html; \
	fi

backend: bootstrap ## Build backend
	@echo "Building backend..."
	go build -o bin/gopds ./cmd/gopds

swagger: ## Generate Swagger documentation (pinned CLI version)
	@echo "Generating Swagger docs with swag $(SWAG_VERSION)..."
	go run github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION) init --generalInfo cmd/gopds/main.go --output internal/swaggerdocs

bootstrap: swagger frontend-placeholder ## Prepare generated inputs required to compile and test the backend
	@echo "Bootstrap complete."

build: build-frontend swagger backend ## Build everything against a real frontend build

build-bin: build-frontend swagger ## Build test binary (mirrors Dockerfile)
	@echo "Building test binary..."
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o bin/gopds ./cmd/gopds

# Testing and Quality
# Short mode skips the tests that talk to a real database — see opds/collections_test.go.
# It needs neither the frontend toolchain nor any running service.
test-backend: bootstrap ## Run Go tests that need no database or frontend toolchain
	@echo "Running backend tests..."
	go test -short -race ./...

# Points at the docker-compose database by default; override any of these to run
# against something else. Prepare the data with `make db-reset`.
TEST_DB_HOST ?= 127.0.0.1:5432
TEST_DB_USER ?= gopds
TEST_DB_PASS ?= gopds_password
TEST_DB_NAME ?= gopds

test-integration: bootstrap ## Run the full Go suite, including tests that require PostgreSQL
	@echo "Running backend tests including integration tests against $(TEST_DB_HOST)/$(TEST_DB_NAME)..."
	GOPDS_POSTGRES_DBHOST=$(TEST_DB_HOST) \
	GOPDS_POSTGRES_DBUSER=$(TEST_DB_USER) \
	GOPDS_POSTGRES_DBPASS=$(TEST_DB_PASS) \
	GOPDS_POSTGRES_DBNAME=$(TEST_DB_NAME) \
	go test -race ./...

test-frontend: ## Run frontend tests once (`vitest run` does not watch)
	@echo "Running frontend tests..."
	cd $(FRONTEND_DIR) && CI=true yarn test

test: test-backend ## Alias for test-backend

verify: bootstrap deps-frontend test-frontend build-frontend ## Complete clean-checkout verification
	@echo "Verifying the backend against the real frontend build..."
	go build ./...
	go test -short -race -coverprofile=coverage.out ./...
	@echo "Verification complete."

test-coverage: test ## Run tests with coverage report
	@echo "Generating coverage report..."
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

search-eval-baseline: ## Capture the lexical-search relevance baseline into $(SEARCH_EVAL_OUT)
	@test -n "$(SEARCH_EVAL_OUT)" || { echo "SEARCH_EVAL_OUT is required, e.g. SEARCH_EVAL_OUT=plans/reports/lexical-search-baseline.json"; exit 1; }
	@mkdir -p $(dir $(SEARCH_EVAL_OUT))
	GOPDS_POSTGRES_DBHOST=$(TEST_DB_HOST) \
	GOPDS_POSTGRES_DBUSER=$(TEST_DB_USER) \
	GOPDS_POSTGRES_DBPASS=$(TEST_DB_PASS) \
	GOPDS_POSTGRES_DBNAME=$(TEST_DB_NAME) \
	go run ./cmd/search-eval capture -input database/testdata/search_catalog_queries.json -out $(SEARCH_EVAL_OUT)

search-eval-compare: ## Compare the new search repository against the baseline into $(SEARCH_EVAL_OUT)
	@test -n "$(SEARCH_EVAL_OUT)" || { echo "SEARCH_EVAL_OUT is required, e.g. SEARCH_EVAL_OUT=plans/reports/lexical-search-compare.json"; exit 1; }
	@mkdir -p $(dir $(SEARCH_EVAL_OUT))
	GOPDS_POSTGRES_DBHOST=$(TEST_DB_HOST) \
	GOPDS_POSTGRES_DBUSER=$(TEST_DB_USER) \
	GOPDS_POSTGRES_DBPASS=$(TEST_DB_PASS) \
	GOPDS_POSTGRES_DBNAME=$(TEST_DB_NAME) \
	go run ./cmd/search-eval compare -input database/testdata/search_catalog_queries.json \
		-baseline plans/reports/lexical-search-baseline.json -out $(SEARCH_EVAL_OUT)

lint: ## Run linters over the whole tree (reports the pre-existing backlog too)
	@echo "Running golangci-lint $(GOLANGCI_VERSION)..."
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION) run

lint-new: ## Run linters over new code only — same gate as CI
	@echo "Running golangci-lint $(GOLANGCI_VERSION) on changes since $(LINT_BASE)..."
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION) run \
		--new-from-merge-base=$(LINT_BASE)

lint-frontend: ## Run ESLint over the whole frontend (reports warnings too)
	@echo "Running ESLint over the frontend..."
	cd $(FRONTEND_DIR) && yarn --silent lint

# Fails on errors only. Unlike golangci-lint, ESLint cannot report just the new
# lines, so a changed file is linted whole — gating on warnings would fail on a
# backlog the change did not introduce. Severity carries the distinction
# instead: error blocks, warning is the backlog, visible via lint-frontend.
lint-frontend-new: ## Fail on ESLint errors in frontend files changed since $(LINT_BASE)
	@echo "Running ESLint on frontend changes since $(LINT_BASE)..."
	@files=$$(git diff --name-only --diff-filter=ACMR \
		$$(git merge-base $(LINT_BASE) HEAD) -- \
		'$(FRONTEND_DIR)/**/*.ts' '$(FRONTEND_DIR)/**/*.tsx' \
		| sed 's|^$(FRONTEND_DIR)/||'); \
	if [ -z "$$files" ]; then \
		echo "No frontend files changed."; \
	else \
		cd $(FRONTEND_DIR) && npx eslint --no-warn-ignored --quiet $$files; \
	fi

fmt: fmt-frontend ## Apply formatters (gofmt, goimports, prettier)
	@echo "Applying formatters..."
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION) fmt

fmt-frontend: ## Apply prettier to the frontend
	@echo "Applying prettier to the frontend..."
	cd $(FRONTEND_DIR) && yarn --silent format

# Prettier is pinned in the frontend's package.json. Never run it through a bare
# npx: with no config resolved it falls back to its own defaults, which are not
# this project's, and rewrites whole files.
fmt-frontend-check: ## Fail if any frontend file is unformatted
	@echo "Checking frontend formatting..."
	cd $(FRONTEND_DIR) && yarn --silent format:check

staticcheck: ## Run staticcheck (pinned version)
	@echo "Running staticcheck $(STATICCHECK_VERSION)..."
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

security: ## Run security checks (pinned version)
	@echo "Running gosec $(GOSEC_VERSION)..."
	go run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) \
		-exclude-dir=booksdump-frontend \
		-nosec-require-justification \
		./...

# Docker
docker-build: ## Build Docker image
	@echo "Building Docker image..."
	docker build -t gopds-api:dev .

docker-run: ## Run Docker container
	@echo "Running Docker container..."
	docker run --rm -p 8085:8085 gopds-api:dev

docker-compose-up: ## Start services with docker-compose
	docker-compose up -d

docker-compose-down: ## Stop services with docker-compose
	docker-compose down

# Development helpers
dev: ## Run in development mode
	@echo "Starting development server..."
	go run ./cmd/gopds

clean: ## Clean build artifacts and generated inputs
	@echo "Cleaning..."
	rm -rf bin/
	rm -rf $(FRONTEND_DIR)/build/
	rm -rf internal/swaggerdocs/
	rm -f coverage.out coverage.html
	go clean

# Development dataset
#
# db-dump reads production; everything else only ever touches the local
# database from docker-compose. The dump deliberately excludes auth_user,
# favorite_books and invites — see scripts/dump-prod-catalog.sh.
DUMP_DIR   := .dumps
LOCAL_PG   := PGPASSWORD=gopds_password psql -h 127.0.0.1 -p 5432 -U gopds -d gopds

db-dump: ## Pull the catalog (no user data) out of production into $(DUMP_DIR)
	./scripts/dump-prod-catalog.sh

db-restore: ## Replace the local database with the dumped catalog
	@test -f $(DUMP_DIR)/schema.sql.gz || { echo "No dump found. Run 'make db-dump' first."; exit 1; }
	@echo "Recreating the local schema..."
	$(LOCAL_PG) -q -c 'DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;'
	@echo "Restoring schema..."
	gunzip -c $(DUMP_DIR)/schema.sql.gz | $(LOCAL_PG) -q -v ON_ERROR_STOP=1
	@echo "Restoring catalog data (this takes a few minutes)..."
	gunzip -c $(DUMP_DIR)/catalog.sql.gz | $(LOCAL_PG) -q -v ON_ERROR_STOP=1
	@echo "Restore complete."

db-seed: ## Create synthetic users and reattach imported rows to them
	go run ./scripts/seeddb

db-reset: db-restore db-seed ## Restore the catalog and seed users in one step
	@echo "Local development database ready."

# Database
# There is no migrate-down. Every file here is a forward change to a schema
# holding a live catalog, and none of them was written with a reverse; an
# undo that has never been tested is worse than none, because it invites use.
# To go back, write a new migration.
migrate-up: ## Apply pending database migrations
	@echo "Applying database migrations..."
	go run ./cmd/migrate

migrate-plan: ## Show what migrate-up would do, changing nothing
	go run ./cmd/migrate -dry-run

# Release
release: clean build test lint-new lint-frontend-new fmt-frontend-check security ## Prepare for release
	@echo "Release preparation complete!"

# Quick checks before commit
pre-commit: lint-new lint-frontend-new fmt-frontend-check test ## Run pre-commit checks
	@echo "Pre-commit checks passed!"
