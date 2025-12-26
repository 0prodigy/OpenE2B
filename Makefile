.PHONY: all build run test clean proto migrate

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt

# Binary names
API_BINARY=bin/api
ENVD_BINARY=bin/envd

all: build

# Build all binaries
build: build-api build-envd

build-api:
	$(GOBUILD) -o $(API_BINARY) ./cmd/api

build-envd:
	$(GOBUILD) -o $(ENVD_BINARY) ./cmd/envd

# Run the API server
run-api: build-api
	./$(API_BINARY)

# Run envd daemon
run-envd: build-envd
	./$(ENVD_BINARY)

# Test all packages
test:
	$(GOTEST) -v ./...

# Format code
fmt:
	$(GOFMT) ./...

# Clean build artifacts
clean:
	rm -rf bin/

# Tidy dependencies
tidy:
	$(GOMOD) tidy

# Generate proto files (requires buf)
proto:
	cd proto && buf generate

# Database migrations
migrate:
	@if [ -z "$(DATABASE_URL)" ]; then \
		echo "Error: DATABASE_URL is required"; \
		echo "Usage: DATABASE_URL=... make migrate"; \
		exit 1; \
	fi
	./scripts/migrate.sh $(DATABASE_URL)

# Run with Supabase (requires DATABASE_URL)
run-api-db: build-api
	@if [ -z "$(DATABASE_URL)" ]; then \
		echo "Warning: DATABASE_URL not set, using in-memory storage"; \
	fi
	./$(API_BINARY)

# Dev: run API with hot reload (requires air)
dev-api:
	air -c .air.api.toml

# Dev: run envd with hot reload (requires air)
dev-envd:
	air -c .air.envd.toml

# Docker build
docker-build:
	docker build -t e2b-server:latest .

# Docker run
docker-run:
	docker run -p 3000:3000 \
		-e DATABASE_URL="$(DATABASE_URL)" \
		-e E2B_DOMAIN="$(E2B_DOMAIN)" \
		e2b-server:latest

# Help
help:
	@echo "E2B Server Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make build        - Build all binaries"
	@echo "  make run-api      - Run API server (in-memory storage)"
	@echo "  make run-api-db   - Run API server (with database if DATABASE_URL set)"
	@echo "  make run-envd     - Run envd daemon"
	@echo "  make test         - Run tests"
	@echo "  make migrate      - Run database migrations (requires DATABASE_URL)"
	@echo "  make tidy         - Tidy Go modules"
	@echo "  make clean        - Clean build artifacts"
	@echo ""
	@echo "Environment Variables:"
	@echo "  DATABASE_URL      - PostgreSQL/Supabase connection string"
	@echo "  E2B_DOMAIN        - Base domain (default: e2b.local)"
	@echo "  E2B_API_PORT      - API port (default: 3000)"
