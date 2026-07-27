.PHONY: help verify bootstrap build build-bin build-frontend backend frontend frontend-placeholder swagger \
	deps deps-frontend test test-backend test-integration test-frontend test-coverage clean lint security \
	docker-build docker-run docker-compose-up docker-compose-down dev migrate-up migrate-down release pre-commit

# These targets are ordered pipelines, not independent compile units: bootstrap
# must finish before tests, and dependencies must be installed before the build
# that uses them. Nothing here gains from parallelism, and running with -j
# silently breaks the order, so keep the whole makefile serial.
.NOTPARALLEL:

FRONTEND_DIR := booksdump-frontend

# Pinned tool versions — keep in sync with Dockerfile and .github/workflows.
SWAG_VERSION := v1.16.6

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
	go build -o bin/gopds cmd/*

swagger: ## Generate Swagger documentation (pinned CLI version)
	@echo "Generating Swagger docs with swag $(SWAG_VERSION)..."
	go run github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION) init --generalInfo cmd/main.go

bootstrap: swagger frontend-placeholder ## Prepare generated inputs required to compile and test the backend
	@echo "Bootstrap complete."

build: build-frontend swagger backend ## Build everything against a real frontend build

build-bin: build-frontend swagger ## Build test binary (mirrors Dockerfile)
	@echo "Building test binary..."
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o bin/gopds cmd/*

# Testing and Quality
# Short mode skips the tests that talk to a real database — see opds/collections_test.go.
# It needs neither the frontend toolchain nor any running service.
test-backend: bootstrap ## Run Go tests that need no database or frontend toolchain
	@echo "Running backend tests..."
	go test -short -race ./...

test-integration: bootstrap ## Run the full Go suite, including tests that require PostgreSQL
	@echo "Running backend tests including integration tests..."
	go test -race ./...

test-frontend: ## Run frontend tests once, without watch mode
	@echo "Running frontend tests..."
	cd $(FRONTEND_DIR) && CI=true yarn test --watchAll=false

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

lint: ## Run linters
	@echo "Running golangci-lint..."
	golangci-lint run --timeout=5m

security: ## Run security checks
	@echo "Running gosec security scanner..."
	gosec ./...

# Docker
docker-build: ## Build Docker image
	@echo "Building Docker image..."
	docker build -t gopds-api:latest .

docker-run: ## Run Docker container
	@echo "Running Docker container..."
	docker run --rm -p 8085:8085 gopds-api:latest

docker-compose-up: ## Start services with docker-compose
	docker-compose up -d

docker-compose-down: ## Stop services with docker-compose
	docker-compose down

# Development helpers
dev: ## Run in development mode
	@echo "Starting development server..."
	go run cmd/*

clean: ## Clean build artifacts and generated inputs
	@echo "Cleaning..."
	rm -rf bin/
	rm -rf $(FRONTEND_DIR)/build/
	rm -rf docs/
	rm -f coverage.out coverage.html
	go clean

# Database
migrate-up: ## Run database migrations up
	@echo "Running database migrations..."
	# Add your migration command here

migrate-down: ## Run database migrations down
	@echo "Reverting database migrations..."
	# Add your migration rollback command here

# Release
release: clean build test lint security ## Prepare for release
	@echo "Release preparation complete!"

# Quick checks before commit
pre-commit: lint test ## Run pre-commit checks
	@echo "Pre-commit checks passed!"
