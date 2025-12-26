# OpenE2B

A self-hosted E2B-compatible sandbox server implementation in Go. Run secure, isolated cloud sandboxes locally or on your own infrastructure.

[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-blue.svg?style=flat-square)](LICENSE)

## 🏗️ Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Client SDKs                                     │
│                    (JavaScript, Python, CLI)                                 │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                           API Server (:3000)                                 │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐        │
│  │  Sandbox    │  │  Template   │  │   Build     │  │   Admin     │        │
│  │    API      │  │    API      │  │  Service    │  │    API      │        │
│  └─────────────┘  └─────────────┘  └─────────────┘  └─────────────┘        │
│                           │                                                  │
│  ┌────────────────────────┴────────────────────────────────────────┐       │
│  │                     Orchestrator / Scheduler                     │       │
│  │  • Node selection    • Resource tracking    • Sandbox lifecycle  │       │
│  └──────────────────────────────────────────────────────────────────┘       │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                    ┌───────────────┴───────────────┐
                    ▼                               ▼
    ┌───────────────────────────┐   ┌───────────────────────────┐
    │    DockerNode (local)     │   │    GRPCNode (remote)      │
    │    In-process Docker      │   │    → Node Agent (:9000)   │
    └───────────────────────────┘   └───────────────────────────┘
                    │                               │
                    ▼                               ▼
    ┌─────────────────────────────────────────────────────────────┐
    │                    Sandbox Container/VM                      │
    │  ┌─────────────────────────────────────────────────────┐    │
    │  │                 envd Daemon (:49983)                 │    │
    │  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  │    │
    │  │  │ Filesystem  │  │   Process   │  │   REST API  │  │    │
    │  │  │   Service   │  │   Service   │  │  /files     │  │    │
    │  │  └─────────────┘  └─────────────┘  └─────────────┘  │    │
    │  └─────────────────────────────────────────────────────┘    │
    └─────────────────────────────────────────────────────────────┘
```

## 📦 Components

### 1. API Server (`cmd/api`)

The main entry point for all client interactions. Provides REST/JSON APIs compatible with E2B SDKs.

**Key Features:**

- Sandbox lifecycle management (create, pause, resume, delete)
- Template management and build triggering
- Admin endpoints for node management
- Automatic sandbox expiration and cleanup

```go
// internal/api/handlers.go
type Handler struct {
    store        *Store
    scheduler    *orchestrator.Scheduler
    buildService BuildService
}
```

### 2. Orchestrator (`internal/orchestrator`)

Manages sandbox placement and lifecycle across compute nodes.

**Components:**

- **Scheduler** - Selects optimal nodes and manages sandbox state
- **DockerNode** - In-process Docker execution (local development)
- **GRPCNode** - Remote node communication via gRPC

```go
// NodeOperations interface - implement for new runtimes
type NodeOperations interface {
    CreateSandbox(ctx context.Context, node *Node, spec SandboxSpec) error
    StopSandbox(ctx context.Context, node *Node, sandboxID string) error
    PauseSandbox(ctx context.Context, node *Node, sandboxID string) error
    ResumeSandbox(ctx context.Context, node *Node, sandboxID string) error
}
```

### 3. Node Agent (`cmd/node-agent`)

Runs on compute nodes to manage sandboxes. Communicates with API server via gRPC.

**Supported Runtimes:**

- `DockerRuntime` - Docker containers (default, works everywhere)
- `FirecrackerRuntime` - Firecracker microVMs (Linux with KVM only)

```bash
# Run node agent
./node-agent --listen :9000 --runtime docker --artifacts-dir ./data/artifacts
```

### 4. envd Daemon (`cmd/envd`)

Runs inside each sandbox to provide filesystem and process services.

**Endpoints:**
| Endpoint | Description |
|----------|-------------|
| `GET /health` | Health check |
| `GET /metrics` | System metrics (CPU, memory, disk) |
| `POST /init` | Initialize environment |
| `GET /envs` | Get environment variables |
| `GET /files?path=` | Download file |
| `POST /files?path=` | Upload file (multipart) |
| `POST /filesystem.Filesystem/*` | Filesystem operations |
| `POST /process.Process/*` | Process execution |

### 5. Build Service (`internal/build`)

Handles template image building using Docker.

**Build Flow:**

1. SDK sends template definition (base image + steps)
2. Build service creates container from base image
3. Executes steps (RUN, ENV, WORKDIR, COPY)
4. Commits container as new image
5. Exports artifacts (rootfs.tar, image.tar, metadata.json)

---

## 🚀 Quick Start

### Prerequisites

- Go 1.22+
- Docker
- (Optional) PostgreSQL or Supabase for persistent storage

### Build

```bash
# Build all binaries
go build -o ./api ./cmd/api/main.go
go build -o ./node-agent ./cmd/node-agent/main.go
GOOS=linux GOARCH=amd64 go build -o ./bin/envd-linux-amd64 ./cmd/envd/main.go
```

### Run (Development Mode)

```bash
# Start API server with in-memory storage
./api

# Server starts on :3000
# - Sandbox API: http://localhost:3000/sandboxes
# - Template API: http://localhost:3000/templates
# - Admin API: http://localhost:3000/nodes
```

---

## 📖 Usage Examples

### Creating a Sandbox (curl)

```bash
# Create a sandbox
curl -X POST http://localhost:3000/sandboxes \
  -H "Content-Type: application/json" \
  -d '{
    "templateID": "base",
    "timeout": 300,
    "metadata": {"project": "my-app"}
  }'

# Response:
# {
#   "sandboxID": "abc123",
#   "templateID": "base",
#   "envdAccessToken": "...",
#   "domain": "e2b.local"
# }
```

### Using the SDK (JavaScript)

```typescript
import { Sandbox } from "e2b";

// For local development, set environment variables:
// E2B_API_URL=http://localhost:3000
// E2B_SANDBOX_URL=http://localhost:49983

const sandbox = await Sandbox.create({ timeoutMs: 60000 });

// Write a file
await sandbox.files.write("/home/user/hello.txt", "Hello, World!");

// Read it back
const content = await sandbox.files.read("/home/user/hello.txt");
console.log(content); // "Hello, World!"

// List directory
const entries = await sandbox.files.list("/home/user");
console.log(entries);
// [{ name: 'hello.txt', type: 'file' }]

// Create a directory
await sandbox.files.makeDir("/home/user/myproject");

// Clean up
await sandbox.kill();
```

### Using the SDK (Python)

```python
from e2b import Sandbox
import os

# For local development
os.environ['E2B_API_URL'] = 'http://localhost:3000'
os.environ['E2B_SANDBOX_URL'] = 'http://localhost:49983'

sandbox = Sandbox()

# Write file
sandbox.files.write('/home/user/script.py', 'print("Hello from E2B!")')

# List files
files = sandbox.files.list('/home/user')
print(files)

# Clean up
sandbox.kill()
```

### Working with envd Directly

```bash
# List files
curl -X POST http://localhost:49983/filesystem.Filesystem/ListDir \
  -H "Content-Type: application/json" \
  -d '{"path": "/home/user", "depth": 1}'

# Create directory
curl -X POST http://localhost:49983/filesystem.Filesystem/MakeDir \
  -H "Content-Type: application/json" \
  -d '{"path": "/home/user/mydir"}'

# Upload file
curl -X POST "http://localhost:49983/files?path=/home/user/test.txt" \
  -F "file=@myfile.txt"

# Download file
curl "http://localhost:49983/files?path=/home/user/test.txt"

# Run a command
curl -X POST http://localhost:49983/process.Process/Start \
  -H "Content-Type: application/json" \
  -d '{"process": {"cmd": "echo", "args": ["Hello!"]}}'
```

### Building a Template

```typescript
import { Template, defaultBuildLogger } from "e2b";

const template = Template()
  .fromUbuntuImage("22.04")
  .runCmd("apt-get update && apt-get install -y python3 curl")
  .setEnvs({ NODE_ENV: "production" })
  .setWorkdir("/app");

const result = await Template.build(template, {
  alias: "my-template",
  cpuCount: 2,
  memoryMB: 1024,
  onBuildLogs: defaultBuildLogger(),
});

console.log("Template ID:", result.templateId);
console.log("Build ID:", result.buildId);
```

### Pause and Resume

```bash
# Pause a sandbox (freezes all processes)
curl -X POST http://localhost:3000/sandboxes/{sandboxID}/pause

# Resume a paused sandbox
curl -X POST http://localhost:3000/sandboxes/{sandboxID}/resume \
  -H "Content-Type: application/json" \
  -d '{"timeout": 300}'
```

---

## 🗂️ Project Structure

```
E2B-server/
├── cmd/
│   ├── api/              # API server entry point
│   ├── envd/             # envd daemon entry point
│   ├── node-agent/       # Node agent entry point
│   └── migrate/          # Database migration tool
├── internal/
│   ├── api/              # HTTP handlers, models, router
│   ├── auth/             # Authentication middleware
│   ├── build/            # Template build service
│   ├── db/               # Database layer (memory, postgres, supabase)
│   ├── orchestrator/     # Scheduler, node operations, types
│   ├── agent/            # Node agent implementation
│   ├── firecracker/      # Firecracker VM support (WIP)
│   └── proxy/            # Network proxy router
├── pkg/
│   ├── envd/             # envd services (filesystem, process)
│   └── proto/            # Generated protobuf/connect code
├── proto/                # Source .proto files
├── data/
│   ├── artifacts/        # Build artifacts (rootfs, images)
│   └── sandboxes/        # Sandbox data
├── bin/                  # Compiled binaries
├── scripts/              # Setup and migration scripts
└── test-sdk/             # SDK integration tests
```

---

## ⚙️ Configuration

### Environment Variables

| Variable              | Default     | Description                    |
| --------------------- | ----------- | ------------------------------ |
| `E2B_DOMAIN`          | `e2b.local` | Base domain for sandbox URLs   |
| `E2B_API_PORT`        | `3000`      | API server port                |
| `E2B_ENVD_PORT`       | `49983`     | Default envd port in sandboxes |
| `DATABASE_URL`        | -           | PostgreSQL connection string   |
| `SUPABASE_URL`        | -           | Supabase project URL           |
| `SUPABASE_SECRET_KEY` | -           | Supabase service role key      |
| `USE_IN_MEMORY_STORE` | `false`     | Force in-memory storage        |

### SDK Configuration (Local Development)

```bash
# JavaScript/TypeScript
export E2B_API_URL=http://localhost:3000
export E2B_SANDBOX_URL=http://localhost:49983
export E2B_API_KEY=e2b_test_key

# Python
export E2B_API_URL=http://localhost:3000
export E2B_SANDBOX_URL=http://localhost:49983
export E2B_API_KEY=e2b_test_key
```

---

## 🗄️ Database

### Option 1: In-Memory (Development)

Default mode - no setup required, but data is lost on restart.

### Option 2: Supabase (Recommended)

```bash
export SUPABASE_URL="https://[project].supabase.co"
export SUPABASE_SECRET_KEY="[service-role-key]"
./api
```

### Option 3: PostgreSQL

```bash
docker run -d --name e2b-postgres \
  -e POSTGRES_PASSWORD=secret \
  -e POSTGRES_DB=e2b \
  -p 5432:5432 postgres:16

export DATABASE_URL="postgresql://postgres:secret@localhost:5432/e2b"
./scripts/migrate.sh
./api
```

---

## 📋 Development Status

### ✅ Completed

- **API Server** - Full REST API compatible with E2B SDKs
- **Sandbox Lifecycle** - Create, pause, resume, delete with timeout
- **Template Management** - CRUD operations with build support
- **Build Service** - Docker-based template builds with step execution
- **envd Daemon** - Filesystem and process services
- **Docker Runtime** - Full sandbox lifecycle in Docker containers
- **SDK Integration** - Tested with official JavaScript SDK
- **Port Management** - Automatic port allocation and recycling

### 🚧 In Progress

- **Command Execution via SDK** - Requires full Connect-RPC streaming support
- **PTY Support** - Interactive terminal sessions
- **Network Proxy** - Route `{port}-{sandboxID}.{domain}` to sandboxes

### 📋 Planned

- **Firecracker Runtime** - MicroVM support for production
- **Artifact Transfer** - Upload/download between API and nodes
- **Multi-Node Scheduling** - Distribute sandboxes across nodes
- **Real Image Registry** - Pull/push to Docker registries
- **Authentication** - API key and token validation
- **Rate Limiting** - Per-team quotas

---

## 🎯 Future Goals

### Short-term

1. **Full Connect-RPC Support** - Use `connectrpc.com/connect` in envd for SDK streaming compatibility
2. **Network Proxy** - Enable direct access to sandbox services via hostnames
3. **Firecracker Integration** - MicroVM support for better isolation

### Medium-term

1. **Multi-Cloud Support** - Run nodes on AWS, GCP, Azure
2. **Kubernetes Integration** - Deploy as Kubernetes operator
3. **Snapshot/Resume** - Fast sandbox start from snapshots
4. **Persistent Volumes** - Attach storage to sandboxes

### Long-term

1. **WebSocket API** - Real-time streaming support
2. **GPU Support** - GPU passthrough for ML workloads
3. **Custom Runtimes** - Plugin system for new runtimes
4. **Federation** - Connect multiple E2B clusters

---

## 🧪 Testing

```bash
# Run unit tests
go test ./...

# Run with verbose output
go test -v ./internal/api/...

# SDK integration test
cd test-sdk && npx tsx sandbox.ts
```

---

## 📄 License

Apache 2.0 - See [LICENSE](./LICENSE)

---

## 🙏 Acknowledgments

This project is designed to be compatible with [E2B](https://e2b.dev) - the open-source cloud for AI agents. Check out their [official repository](https://github.com/e2b-dev/E2B) for more information.
