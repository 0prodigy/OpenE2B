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
EDGE_CONTROLLER_BINARY=bin/edge-controller

all: build

# Build all binaries
build: build-api build-envd build-orchestrator build-edge-controller

build-api:
	$(GOBUILD) -o $(API_BINARY) ./cmd/api

build-envd:
	$(GOBUILD) -o $(ENVD_BINARY) ./cmd/envd

build-orchestrator:
	$(GOBUILD) -o $(ORCHESTRATOR_BINARY) ./cmd/orchestrator

build-edge-controller:
	$(GOBUILD) -o $(EDGE_CONTROLLER_BINARY) ./cmd/edge-controller

# Run the API server (includes embedded Edge Controller)
run-api: build-api
	./$(API_BINARY)

# Run the Orchestrator (Node Daemon)
run-orchestrator: build-orchestrator
	./$(ORCHESTRATOR_BINARY)

# Run the Edge Controller (standalone proxy)
run-edge-controller: build-edge-controller
	./$(EDGE_CONTROLLER_BINARY)

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

# Build Linux binaries for deployment
build-linux:
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o bin/orchestrator-linux-amd64 ./cmd/orchestrator
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o bin/envd-linux-amd64 ./cmd/envd
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o bin/api-linux-amd64 ./cmd/api
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o bin/edge-controller-linux-amd64 ./cmd/edge-controller
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
	@echo "  make run-api            - Run API server with embedded Edge Controller"
	@echo "  make run-orchestrator   - Run Orchestrator (Node Daemon)"
	@echo "  make run-edge-controller - Run Edge Controller (standalone proxy)"
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
	@echo "  api            - Control Plane (API Server + Scheduler + embedded Edge Controller)"
	@echo "  orchestrator   - Node Daemon (manages sandboxes on compute nodes)"
	@echo "  edge-controller - Standalone proxy for SDK traffic routing"
	@echo "  envd           - Sandbox daemon (runs inside containers/VMs)"
	@echo ""
	@echo "Environment Variables:"
	@echo "  DATABASE_URL      - PostgreSQL/Supabase connection string"
	@echo "  E2B_DOMAIN        - Base domain (default: e2b.local)"
	@echo "  E2B_API_PORT      - API port (default: 3000)"
	@echo "  E2B_PROXY_PORT    - Edge Controller proxy port (default: 8080)"
	@echo "  E2B_ENVD_PORT     - envd port inside sandboxes (default: 49983)"
	@echo "  SSH_KEY           - SSH key for AWS deployment"
	@echo "  EC2_HOST          - EC2 instance hostname"
