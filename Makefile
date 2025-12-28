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
ORCHESTRATOR_BINARY=bin/orchestrator

all: build

# Build all binaries
build: build-api build-envd build-orchestrator

build-api:
	$(GOBUILD) -o $(API_BINARY) ./cmd/api

build-envd:
	$(GOBUILD) -o $(ENVD_BINARY) ./cmd/envd

build-orchestrator:
	$(GOBUILD) -o $(ORCHESTRATOR_BINARY) ./cmd/orchestrator

# Run the API server (starts orchestrator automatically in local mode)
run-api: build-api
	./$(API_BINARY)

# Run the Orchestrator (Node Daemon) - for BYOC deployments
run-orchestrator: build-orchestrator
	./$(ORCHESTRATOR_BINARY)

# Run envd daemon (runs inside sandboxes)
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

# Build Linux binaries for deployment
build-linux:
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o bin/api-linux-amd64 ./cmd/api
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o bin/orchestrator-linux-amd64 ./cmd/orchestrator
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o bin/envd-linux-amd64 ./cmd/envd
	GOOS=linux GOARCH=arm64 $(GOBUILD) -o bin/orchestrator-linux-arm64 ./cmd/orchestrator
	GOOS=linux GOARCH=arm64 $(GOBUILD) -o bin/envd-linux-arm64 ./cmd/envd
	@echo "✓ Linux binaries built"

# Firecracker deployment
deploy-firecracker: build-linux
	@echo "Deploying Firecracker runtime to AWS..."
	./scripts/deploy-firecracker.sh

# Test Firecracker deployment
test-firecracker:
	@echo "Testing Firecracker deployment..."
	./scripts/test-firecracker.sh

# Quick deploy (stop, copy, start)
quick-deploy:
	@echo "Quick deploying orchestrator..."
	@ssh -i ~/Downloads/akash-dev-key.pem ec2-user@ec2-65-2-148-135.ap-south-1.compute.amazonaws.com "pkill -f orchestrator || true"
	@scp -i ~/Downloads/akash-dev-key.pem bin/orchestrator-linux-amd64 ec2-user@ec2-65-2-148-135.ap-south-1.compute.amazonaws.com:/opt/e2b/bin/orchestrator
	@ssh -i ~/Downloads/akash-dev-key.pem ec2-user@ec2-65-2-148-135.ap-south-1.compute.amazonaws.com "chmod +x /opt/e2b/bin/orchestrator && nohup /opt/e2b/bin/orchestrator --node-id=node-1 --listen=:9001 --runtime=firecracker --artifacts-dir=/opt/e2b/artifacts --sandboxes-dir=/opt/e2b/sandboxes > /opt/e2b/logs/orchestrator-1.log 2>&1 & sleep 2; curl -s localhost:9001/health"

# View remote logs
logs-firecracker:
	@ssh -i ~/Downloads/akash-dev-key.pem ec2-user@ec2-65-2-148-135.ap-south-1.compute.amazonaws.com 'tail -f /opt/e2b/logs/orchestrator-1.log'

# Help
help:
	@echo "E2B Server Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make build              - Build all binaries"
	@echo "  make build-linux        - Build Linux binaries for deployment"
	@echo "  make run-api            - Run API server (starts orchestrator in local mode)"
	@echo "  make run-orchestrator   - Run Orchestrator standalone (for BYOC)"
	@echo "  make run-envd           - Run envd daemon"
	@echo "  make run-api-db         - Run API server (with database if DATABASE_URL set)"
	@echo "  make test               - Run tests"
	@echo "  make migrate            - Run database migrations (requires DATABASE_URL)"
	@echo "  make tidy               - Tidy Go modules"
	@echo "  make clean              - Clean build artifacts"
	@echo ""
	@echo "Firecracker Deployment:"
	@echo "  make deploy-firecracker - Deploy Firecracker runtime to AWS"
	@echo "  make test-firecracker   - Test Firecracker deployment"
	@echo "  make quick-deploy       - Quick redeploy orchestrator"
	@echo "  make logs-firecracker   - View remote orchestrator logs"
	@echo ""
	@echo "Components:"
	@echo "  api            - Control Plane (API Server + Scheduler + Build Service)"
	@echo "  orchestrator   - Node Daemon (manages sandboxes, includes Edge Controller)"
	@echo "  envd           - Sandbox daemon (runs inside containers/VMs)"
	@echo ""
	@echo "Environment Variables:"
	@echo "  DATABASE_URL             - PostgreSQL/Supabase connection string"
	@echo "  SUPABASE_URL             - Supabase project URL"
	@echo "  SUPABASE_SECRET_KEY      - Supabase service role key"
	@echo "  E2B_DOMAIN               - Base domain (default: e2b.local)"
	@echo "  E2B_API_PORT             - API port (default: 3000)"
	@echo "  E2B_PROXY_PORT           - Proxy port (default: 8080)"
	@echo "  E2B_ENVD_PORT            - envd port inside sandboxes (default: 49983)"
	@echo "  E2B_EDGE_CONTROLLER_URL  - Edge Controller URL (default: http://localhost:9001)"
	@echo "  E2B_ORCHESTRATOR_NODES   - Remote orchestrator nodes (format: node-1=http://host:port)"
