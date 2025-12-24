.PHONY: help install build-frontend build-backend build dev-frontend dev-backend dev run clean test

# Load .env file if it exists
ifneq (,$(wildcard .env))
    include .env
    export
endif

# Default values
PORT ?= 8080
TAILSCALE_OAUTH_CLIENT_ID ?=
TAILSCALE_OAUTH_CLIENT_SECRET ?=
TAILSCALE_API_KEY ?=
TAILSCALE_TAILNET ?=
TAILSCALE_API_URL ?= https://api.tailscale.com

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

install: ## Install frontend dependencies
	@echo "Installing frontend dependencies..."
	cd frontend && npm install

build-frontend: ## Build the frontend
	@echo "Building frontend..."
	cd frontend && npm run build

build-backend: build-frontend ## Build the backend (includes frontend build)
	@echo "Building backend..."
	cd backend && go build -o ../tsflow main.go

build: build-backend ## Build everything

dev-frontend: ## Run frontend in development mode
	@echo "Starting frontend dev server..."
	cd frontend && DEVELOPMENT_BACKEND_URL=http://localhost:$(PORT) npm run dev

dev-backend: ## Run backend in development mode
	@echo "Starting backend dev server..."
	@echo "Backend will run on http://localhost:$(PORT)"
	@echo ""
	cd backend && go run main.go

run: build ## Build and run the application
	@echo "Starting TSFlow..."
	./tsflow

dev: ## Run in development mode (backend only, use 'make dev-frontend' in another terminal)
	@echo "Starting TSFlow in development mode..."
	@echo "Backend will run on http://localhost:$(PORT)"
	@echo "Run 'make dev-frontend' in another terminal for frontend dev server"
	@echo ""
	cd backend && go run main.go

clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -rf tsflow
	rm -rf frontend/dist
	rm -rf frontend/.svelte-kit
	rm -rf frontend/node_modules
	rm -rf backend/backend

test: ## Run tests
	@echo "Running Go tests..."
	cd backend && go test ./...

tidy: ## Tidy Go modules
	@echo "Tidying Go modules..."
	cd backend && go mod tidy

docker-build: ## Build Docker image
	@echo "Building Docker image..."
	docker build -t tsflow:latest .

docker-run: ## Run Docker container
	@echo "Running Docker container..."
	docker run --rm -it \
		-p $(PORT):8080 \
		-e TAILSCALE_OAUTH_CLIENT_ID=$(TAILSCALE_OAUTH_CLIENT_ID) \
		-e TAILSCALE_OAUTH_CLIENT_SECRET=$(TAILSCALE_OAUTH_CLIENT_SECRET) \
		-e TAILSCALE_API_KEY=$(TAILSCALE_API_KEY) \
		-e TAILSCALE_TAILNET=$(TAILSCALE_TAILNET) \
		-e TAILSCALE_API_URL=$(TAILSCALE_API_URL) \
		-e PORT=8080 \
		-e ENVIRONMENT=production \
		tsflow:latest

.DEFAULT_GOAL := help
