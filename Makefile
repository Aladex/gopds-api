.PHONY: help build test clean docker-build docker-run frontend backend swagger deps lint security build-bin

# Default target
help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Development
deps: ## Install dependencies
	@echo "Installing Go dependencies..."
	go mod download
	go mod tidy
	@echo "Installing frontend dependencies..."
	cd booksdump-frontend && yarn install

frontend: ## Build frontend
	@echo "Building frontend..."
	cd booksdump-frontend && yarn build

backend: deps ## Build backend
	@echo "Building backend..."
	go build -o bin/gopds cmd/*

swagger: ## Generate Swagger documentation
	@echo "Generating Swagger docs..."
	swag init --generalInfo cmd/main.go

build: frontend backend swagger ## Build everything

build-bin: frontend swagger ## Build test binary (mirrors Dockerfile)
	@echo "Building test binary..."
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o bin/gopds cmd/*

# Testing and Quality
test: ## Run tests
	@echo "Running tests..."
	go test -v -race -coverprofile=coverage.out ./...

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

clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -rf bin/
	rm -rf booksdump-frontend/build/
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
