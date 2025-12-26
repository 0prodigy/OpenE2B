---
date: 2025-12-26T18:39:44+0530
researcher: AI Assistant
git_commit: N/A (not a git repo)
branch: N/A
repository: E2B-server
topic: "Firecracker VM Infrastructure and gRPC Control Plane Implementation"
tags: [implementation, firecracker, grpc, node-agent, sandbox-runtime, docker]
status: complete
last_updated: 2025-12-26
last_updated_by: AI Assistant
type: implementation_strategy
---

# Handoff: Firecracker VM Infrastructure and gRPC Control Plane

## Task(s)

### Completed
1. **SDK Template Build Testing** - Successfully replicated the E2B SDK template build flow from https://e2b.dev/docs/template/base-image
   - Created test script using local JS SDK
   - Built template with `fromUbuntuImage("22.04")`, `runCmd()`, `setEnvs()`, `setWorkdir()`
   - Verified template image creation and artifact generation

2. **Artifact Generation** - Added rootfs/image export after template builds
   - `rootfs.tar` - Container filesystem export (~169MB)
   - `image.tar` - Docker image archive (~83MB)
   - `metadata.json` - Build metadata (templateId, buildId, cpuCount, memoryMB, baseImage)

3. **gRPC Control Plane** - Implemented Node Agent with Connect/gRPC
   - Created `proto/node/node.proto` with full service definition
   - Generated Go code with buf
   - Implemented NodeAgent service with all sandbox lifecycle methods

4. **Docker Runtime** - Implemented sandbox runtime using Docker containers
   - CreateSandbox, StopSandbox, PauseSandbox, ResumeSandbox
   - ListSandboxes, GetSandboxStatus, ExecCommand
   - Full sandbox lifecycle tested and working

5. **Firecracker Runtime Stub** - Created Firecracker runtime for Linux/KVM environments
   - Rootfs tar to ext4 conversion logic
   - Firecracker config generation
   - VM lifecycle management (requires KVM)

## Critical References
- `E2B/packages/js-sdk/src/template/` - SDK template builder implementation
- `E2B/spec/openapi.yml` - API specification (TemplateBuildStartV2, TemplateStep schemas)
- `E2B-server/plan.md` - Overall implementation plan (Phase 3: Orchestration + Firecracker)

## Recent changes

- `E2B-server/internal/build/service.go:304-360` - Added artifact export (rootfs.tar, image.tar, metadata.json)
- `E2B-server/cmd/api/main.go:102-106` - Added artifactsDir configuration
- `E2B-server/proto/node/node.proto:1-167` - New gRPC service definition for node control
- `E2B-server/cmd/node-agent/main.go:1-98` - New node agent binary entry point
- `E2B-server/internal/agent/agent.go:1-280` - gRPC service implementation
- `E2B-server/internal/agent/runtime_docker.go:1-280` - Docker-based sandbox runtime
- `E2B-server/internal/agent/runtime_firecracker.go:1-280` - Firecracker VM runtime
- `E2B-server/internal/orchestrator/node_grpc.go:1-220` - gRPC client for API → node communication

## Learnings

1. **SDK Template Build Flow (V2)**: The SDK creates templates via `POST /v3/templates`, uploads files via `GET /templates/{id}/files/{hash}`, then triggers builds with steps via `POST /v2/templates/{id}/builds/{buildID}`. Each step has a `Type` (RUN, ENV, WORKDIR, USER, COPY) and `Args`.

2. **Artifact Structure**: Build artifacts are stored in `data/artifacts/{templateID}/{buildID}/` with:
   - `rootfs.tar` - Docker export of container filesystem
   - `image.tar` - Docker save of the committed image
   - `metadata.json` - JSON with templateId, buildId, imageTag, cpuCount, memoryMB

3. **Connect/gRPC**: The Connect protocol allows JSON over HTTP/1.1 which is easy to test with curl:
   ```bash
   curl -X POST http://localhost:9000/node.NodeAgent/CreateSandbox \
     -H "Content-Type: application/json" -d '{"sandboxId": "...", ...}'
   ```

4. **Docker Pause/Unpause**: Docker's pause/unpause freezes container processes in place, which is a good substitute for Firecracker snapshots during development.

5. **Firecracker Requirements**: Firecracker requires Linux with KVM (`/dev/kvm`). On macOS, we use Docker as fallback. The Firecracker runtime includes ext4 conversion logic but needs a Linux host to run.

## Artifacts

### New Proto Definitions
- `E2B-server/proto/node/node.proto` - NodeAgent gRPC service

### Generated Code
- `E2B-server/pkg/proto/node/node.pb.go`
- `E2B-server/pkg/proto/node/nodeconnect/node.connect.go`

### Node Agent
- `E2B-server/cmd/node-agent/main.go` - Binary entry point
- `E2B-server/internal/agent/agent.go` - Service implementation
- `E2B-server/internal/agent/runtime_docker.go` - Docker runtime
- `E2B-server/internal/agent/runtime_firecracker.go` - Firecracker runtime

### Orchestrator Extensions
- `E2B-server/internal/orchestrator/node_grpc.go` - gRPC client for node communication

### Build Service Updates
- `E2B-server/internal/build/service.go` - Added artifact export

### VM Setup
- `E2B-server/scripts/vm-setup/cloud-init.yaml` - Cloud-init for Multipass VMs

### Test Artifacts
- `E2B-server/test-sdk/build.ts` - SDK template build test script
- `E2B-server/test-sdk/package.json` - Test dependencies

## Action Items & Next Steps

1. **Wire API Server to Node Agents** - Update `internal/api/handlers.go` to use `orchestrator.GRPCNode` for sandbox operations instead of `DockerNode`

2. **Integrate envd into Sandboxes** - Run the envd daemon inside containers to provide:
   - Filesystem gRPC service (read/write/watch)
   - Process gRPC service (start/connect/stream)
   - Build and inject envd binary into template images

3. **Network Proxy** - Implement `internal/proxy/router.go` to route `{port}-{sandboxId}.{domain}` to sandbox envd

4. **Test on Linux VM** - Use Multipass to create Ubuntu VM and test Firecracker runtime:
   ```bash
   multipass launch --name e2b-node --cloud-init scripts/vm-setup/cloud-init.yaml
   multipass transfer ./node-agent e2b-node:/opt/e2b/
   ```

5. **Artifact Transfer** - Implement artifact upload/download between API server and node agents (currently assumes local filesystem access)

## Other Notes

### Running Locally

```bash
# Terminal 1: API Server
cd E2B-server && SUPABASE_URL="" SUPABASE_SECRET_KEY="" ./api

# Terminal 2: Node Agent
./node-agent --listen :9000 --runtime docker --artifacts-dir ./data/artifacts

# Terminal 3: Build a template (uses local SDK)
cd E2B-server/test-sdk && npx tsx build.ts

# Terminal 4: Create sandbox via gRPC
curl -X POST http://localhost:9000/node.NodeAgent/CreateSandbox \
  -H "Content-Type: application/json" \
  -d '{"sandboxId": "test", "templateId": "2d84d3c9", "buildId": "be0764f2-..."}'
```

### Architecture Diagram

```
API Server (:3000)        Node Agent (:9000)         Container/VM
┌──────────────┐          ┌──────────────┐          ┌──────────────┐
│ Template API │          │ CreateSandbox│          │ ubuntu:22.04 │
│ Sandbox API  │──gRPC───▶│ StopSandbox  │──docker──▶│ + curl       │
│ Build Svc    │          │ PauseSandbox │          │ + python3    │
└──────────────┘          │ ResumeSandbox│          │ + envd       │
                          └──────────────┘          └──────────────┘
```

### Key Binaries
- `./api` - Main API server
- `./node-agent` - Compute node agent
- `./envd` - Sandbox daemon (to be integrated)

### Template Image Created
- `e2b/2d84d3c9:be0764f2-9b22-464c-8af9-889e173f96b7` - Ubuntu 22.04 with curl and Python 3.10
