---
date: 2025-12-26T14:29:46+05:30
researcher: Claude
git_commit: initial (not yet committed)
branch: main
repository: E2B-server
topic: "E2B Server Implementation - Go + Connect-RPC"
tags:
  [
    implementation,
    go,
    connect-rpc,
    e2b,
    sandbox,
    orchestration,
    supabase,
    database,
    build-pipeline,
  ]
status: in_progress
last_updated: 2025-12-26T17:00:00+05:30
last_updated_by: Claude
type: implementation_strategy
---

# Handoff: E2B Server Implementation (Phases 1-4 + Database Complete)

## Task(s)

Building a self-hosted E2B-compatible sandbox server in Go with Connect-RPC, following the implementation plan in `.cursor/plans/e2b_server_implementation_cd74680f.plan.md`.

| Phase    | Status       | Description                                              |
| -------- | ------------ | -------------------------------------------------------- |
| Phase 1  | ✅ Completed | Public API (Connect + REST parity)                       |
| Phase 2  | ✅ Completed | envd daemon with REST + Connect services                 |
| Phase 3  | ✅ Completed | Orchestration skeleton (scheduler, image manager, proxy) |
| Database | ✅ Completed | Supabase integration with REST API + PostgreSQL support  |
| Phase 4  | ✅ Completed | Template build pipeline (API + service skeleton)         |
| Phase 5  | 🔲 Pending   | Firecracker VMM integration                              |

## Critical References

1. **Implementation Plan**: `.cursor/plans/e2b_server_implementation_cd74680f.plan.md` (1-172) - Master plan with architecture, phases, and data contracts
2. **OpenAPI Spec**: `/Users/prodigy/prodigy/E2B/spec/openapi.yml` - API contract for public endpoints
3. **envd Spec**: `/Users/prodigy/prodigy/E2B/spec/envd/envd.yaml` - REST API for sandbox daemon
4. **Supabase Project**: `https://hxbsemesfciwgdhqzcpc.supabase.co` - Production database

## Recent Changes (Phase 4 Build Pipeline - 2025-12-26)

### New Build Service (`internal/build/`)

- `internal/build/service.go` - Build service with worker pool and state machine
  - BuildStatus enum: `waiting → building → ready/error`
  - Worker pool for concurrent builds (configurable, default 2 workers)
  - Log collection and structured log entries
  - Async build processing with database persistence

### New API Endpoints

- `POST /templates/{templateID}/builds/{buildID}` - Start build (legacy)
- `POST /v2/templates/{templateID}/builds/{buildID}` - Start build v2 with full config
- `POST /templates/{templateID}` - Trigger template rebuild

### New Models (`internal/api/models.go`)

- `TemplateBuildStartV2` - V2 build start request with fromImage, fromTemplate, steps, startCmd, readyCmd
- `TemplateStep` - Build step with name, command, force flag
- `FromImageRegistry` - Registry credentials for image pulls
- `BuildConfig` - Internal build configuration

### Updated Files

- `internal/api/handlers.go` - Added StartBuild, StartBuildV2, RebuildTemplate handlers
- `internal/api/router.go` - Added routes for build endpoints and v2 templates
- `internal/api/store.go` - Added CreateBuild, UpdateBuildStatus methods
- `internal/db/store.go` - Added CreateBuild to Store interface
- `internal/db/memory.go` - Implemented CreateBuild
- `internal/db/supabase.go` - Implemented CreateBuild
- `internal/db/postgres.go` - Implemented CreateBuild
- `cmd/api/main.go` - Initialize build service on startup

### New Tests

- `TestGetBuildStatus` - Verify build status retrieval
- `TestRebuildTemplate` - Verify template rebuild creates new build
- `TestStartBuild` - Verify legacy build start
- `TestStartBuildV2` - Verify v2 build start with configuration

## Previous Changes (Database Integration - 2025-12-26)

### New Database Layer (`internal/db/`)

- `internal/db/store.go` - Store interface defining all CRUD operations
- `internal/db/supabase.go` - Supabase REST API implementation (works with IPv4)
- `internal/db/postgres.go` - Direct PostgreSQL implementation (requires IPv6 or pooler)
- `internal/db/memory.go` - In-memory implementation for development
- `internal/db/config.go` - Database configuration loader
- `internal/db/migrations/001_initial_schema.sql` - Full database schema

### Database Schema (8 tables)

```sql
-- Core tables created in Supabase:
teams           -- Team/organization management
team_users      -- Users belonging to teams
templates       -- Sandbox templates with build configurations
builds          -- Template build history and logs
sandboxes       -- Running sandbox instances
nodes           -- Compute node registry
api_keys        -- Team API keys for authentication
access_tokens   -- User access tokens
```

### Updated Files

- `cmd/api/main.go` - Updated to support Supabase/PostgreSQL/Memory store selection
- `internal/api/store.go` - Refactored to use `db.Store` interface
- `internal/api/handlers_test.go` - Updated tests to use memory store
- `go.mod` - Added `github.com/jackc/pgx/v5` for PostgreSQL
- `Makefile` - Added `migrate` target
- `README.md` - Updated with database setup instructions
- `scripts/migrate.sh` - Migration runner script
- `scripts/setup_supabase.md` - Supabase setup guide

### Supabase Connection Issue

- **Problem**: Direct PostgreSQL connection fails due to IPv4/IPv6 incompatibility
- **Error**: `dial tcp [IPv6]:5432: connect: no route to host`
- **Solution**: Implemented Supabase REST API store which works over HTTP/IPv4
- **Alternative**: Session Pooler connection string needed (user hasn't provided yet)

## All Implementation Files

### Core API (Phase 1)

- `cmd/api/main.go:1-182` - API server entrypoint with database initialization
- `internal/api/models.go:1-446` - All API request/response models per OpenAPI spec
- `internal/api/handlers.go:1-645` - HTTP handlers for all endpoints
- `internal/api/router.go:1-193` - Request routing
- `internal/api/store.go:1-571` - Store wrapper with DB interface
- `internal/auth/middleware.go:1-100` - Allow-all auth middleware

### Database Layer (NEW)

- `internal/db/store.go:1-220` - Store interface definition
- `internal/db/supabase.go:1-750` - Supabase REST API implementation
- `internal/db/postgres.go:1-700` - PostgreSQL direct connection
- `internal/db/memory.go:1-483` - In-memory store
- `internal/db/config.go:1-69` - Configuration
- `internal/db/migrations/001_initial_schema.sql:1-182` - Schema

### envd Daemon (Phase 2)

- `cmd/envd/main.go:1-80` - envd server entrypoint
- `pkg/envd/models.go:1-39` - envd data models
- `pkg/envd/api/handlers.go:1-180` - REST handlers
- `pkg/envd/api/router.go:1-35` - REST routing
- `pkg/envd/api/auth.go:1-55` - Access token middleware
- `pkg/envd/filesystem/service.go:1-200` - Filesystem operations
- `pkg/envd/process/service.go:1-170` - Process management
- `pkg/envd/connect/filesystem.go:1-220` - Connect-RPC filesystem
- `pkg/envd/connect/process.go:1-260` - Connect-RPC process with streaming

### Orchestration (Phase 3)

- `internal/orchestrator/types.go:1-95` - Sandbox, Node, and Image types
- `internal/orchestrator/scheduler.go:1-230` - Sandbox scheduling
- `internal/orchestrator/image.go:1-90` - Image manager skeleton
- `internal/proxy/router.go:1-100` - Host-based routing

### Build Service (Phase 4)

- `internal/build/service.go:1-320` - Build service with worker pool and state machine

### Scripts & Config

- `cmd/migrate/main.go` - Database migration tool
- `scripts/migrate.sh` - Migration shell script
- `scripts/setup_supabase.md` - Setup documentation

### Tests

- `internal/api/handlers_test.go:1-300` - API handler tests (11 tests, all passing)
- `internal/orchestrator/scheduler_test.go:1-150` - Scheduler tests (all passing)

## Learnings

1. **Supabase IPv4/IPv6**: Supabase free tier only provides IPv6 for direct PostgreSQL connections. For IPv4 networks, use:

   - Session Pooler (requires correct connection string)
   - REST API (implemented and working)
   - IPv4 add-on (paid)

2. **Store Interface Pattern**: Using an interface allows swapping between Supabase REST, PostgreSQL, and in-memory stores without changing API code.

3. **Template ID vs Alias**: When creating sandboxes, must use the actual `template_id` from database, not the alias. The store resolves aliases but stores the real ID.

4. **Supabase REST API**: Full CRUD operations work via PostgREST. Filters use query params like `?column=eq.value`.

## Environment Variables

| Variable              | Default     | Description                                            |
| --------------------- | ----------- | ------------------------------------------------------ |
| `SUPABASE_URL`        | -           | Supabase project URL (e.g., `https://xxx.supabase.co`) |
| `SUPABASE_SECRET_KEY` | -           | Supabase service role key                              |
| `DATABASE_URL`        | -           | Direct PostgreSQL connection string                    |
| `USE_IN_MEMORY_STORE` | `false`     | Force in-memory storage                                |
| `E2B_DOMAIN`          | `e2b.local` | Base domain for sandbox URLs                           |
| `E2B_API_PORT`        | `3000`      | API server port                                        |
| `E2B_ENVD_PORT`       | `49983`     | envd daemon port                                       |

## Running the Server

```bash
# Build
cd /Users/prodigy/prodigy/E2B-server
make build

# Run with Supabase (production)
export SUPABASE_URL="https://hxbsemesfciwgdhqzcpc.supabase.co"
export SUPABASE_SECRET_KEY="sb_secret_JnZKETkRPpiZneewyKbhCw_FZeiIy95"
./bin/api

# Run with PostgreSQL (if IPv6 available or using pooler)
export DATABASE_URL="postgresql://..."
./bin/api

# Run in development (in-memory)
./bin/api

# Run tests
go test ./...
```

## Action Items & Next Steps

### Immediate Priority

#### 1. Fix PostgreSQL Direct Connection (Optional)

- Get Session Pooler connection string from Supabase Dashboard
- Test connection: `Settings → Database → Connection String → Method: Session pooler`
- Update `internal/db/postgres.go` if needed for pooler compatibility

#### 2. Complete Build Pipeline (Phase 4 Enhancement)

The build API skeleton is complete. To make it functional:

1. Integrate with Docker/containerd for actual image building:
   - Pull base images from registry
   - Execute build steps in container
   - Commit and push built images
2. Add build log streaming endpoint (SSE or WebSocket)
3. Implement Dockerfile parsing in `internal/build/`

### Phase 5 - Firecracker Integration

1. Add Firecracker VMM bindings in `internal/firecracker/`:

   - VM lifecycle (create, start, pause, resume, stop)
   - Drive/rootfs attachment
   - Network configuration

2. Implement image conversion in `internal/orchestrator/image.go`:

   - Pull OCI images via containerd
   - Convert to ext4 rootfs for Firecracker
   - Cache management

3. Complete proxy in `internal/proxy/`:
   - Actual TCP/HTTP proxying to sandbox VMs
   - Traffic token validation

### Future Enhancements

1. **Authentication**: Implement real API key validation in `internal/auth/`
2. **Rate Limiting**: Add per-team rate limits
3. **Metrics**: Prometheus metrics endpoint
4. **Pagination**: Complete pagination with `x-next-token` header
5. **Row Level Security**: Enable RLS in Supabase for multi-tenant isolation

## Verified Working Features

✅ Health endpoint (`GET /health`)
✅ List templates (`GET /templates`) - reads from Supabase
✅ Create template (`POST /v3/templates`) - writes to Supabase
✅ Get template by ID or alias
✅ Create sandbox (`POST /sandboxes`) - writes to Supabase
✅ List sandboxes (`GET /sandboxes`) - reads from Supabase
✅ Get sandbox details (`GET /sandboxes/{id}`)
✅ Delete sandbox (`DELETE /sandboxes/{id}`) - deletes from Supabase
✅ List teams (`GET /teams`)
✅ Template alias resolution (e.g., "base" → actual template ID)
✅ Auto-seeding default "base" template on startup
✅ Get build status (`GET /templates/{id}/builds/{buildID}/status`)
✅ Start build (`POST /templates/{id}/builds/{buildID}`) - legacy
✅ Start build v2 (`POST /v2/templates/{id}/builds/{buildID}`) - with config
✅ Rebuild template (`POST /templates/{id}`) - creates new build
✅ Build service with worker pool (simulated builds)

## SDK Compatibility

The server is designed to be compatible with the official E2B SDKs:

- JS/TS SDK: `packages/js-sdk/` in E2B repo
- Python SDK: `packages/python-sdk/` in E2B repo

The SDKs use `openapi-fetch` for the public API and Connect-RPC for envd communication.
