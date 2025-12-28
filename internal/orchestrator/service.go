package orchestrator

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"

	"connectrpc.com/connect"
	pb "github.com/0prodigy/OpenE2B/pkg/proto/node"
)

// Config holds agent configuration
type Config struct {
	NodeID       string
	ArtifactsDir string
	SandboxesDir string
	RuntimeMode  string // "docker" or "firecracker"
	ControlPlane string // URL for control plane registration
}

// SandboxRuntime is the interface for different runtimes
type SandboxRuntime interface {
	CreateSandbox(ctx context.Context, spec *SandboxSpec) error
	StopSandbox(ctx context.Context, sandboxID string, force bool) error
	PauseSandbox(ctx context.Context, sandboxID string) (string, error) // returns snapshot ID
	ResumeSandbox(ctx context.Context, sandboxID, snapshotID string) error
	GetSandboxStatus(ctx context.Context, sandboxID string) (*SandboxInfo, error)
	ListSandboxes(ctx context.Context) ([]*SandboxInfo, error)
	ExecCommand(ctx context.Context, sandboxID, command string, args []string) (<-chan ExecOutput, error)
}

// SandboxSpec defines sandbox configuration
type SandboxSpec struct {
	SandboxID   string
	TemplateID  string
	BuildID     string
	CPUCount    int32
	MemoryMB    int32
	DiskSizeMB  int32
	EnvVars     map[string]string
	Metadata    map[string]string
	Timeout     time.Duration
	AutoPause   bool
	EnvdToken   string
	ArtifactURL string
}

// SandboxInfo represents sandbox status
type SandboxInfo struct {
	SandboxID   string
	TemplateID  string
	BuildID     string
	State       string
	EnvdAddress string
	EnvdPort    int32
	StartedAt   int64
	ExpiresAt   int64
	CPUCount    int32
	MemoryMB    int32
	Error       string
}

// ExecOutput represents command output
type ExecOutput struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode *int32
}

// Agent implements the NodeAgent gRPC service
type Agent struct {
	config  Config
	runtime SandboxRuntime
	mu      sync.RWMutex
	stopCh  chan struct{}
}

// New creates a new node agent
func New(config Config) (*Agent, error) {
	var runtime SandboxRuntime

	switch config.RuntimeMode {
	case "docker":
		runtime = NewDockerRuntime(config.ArtifactsDir, config.SandboxesDir)
	case "firecracker":
		runtime = NewFirecrackerRuntime(config.ArtifactsDir, config.SandboxesDir)
	case "docker-sdk":
		runtime = NewDockerRuntimeSDK(config.ArtifactsDir, config.SandboxesDir)
	default:
		return nil, fmt.Errorf("unknown runtime mode: %s", config.RuntimeMode)
	}

	return &Agent{
		config:  config,
		runtime: runtime,
		stopCh:  make(chan struct{}),
	}, nil
}

// Shutdown stops the agent
func (a *Agent) Shutdown() {
	close(a.stopCh)
}

// GetRuntime returns the sandbox runtime for use by the edge controller
func (a *Agent) GetRuntime() SandboxRuntime {
	return a.runtime
}

// StartHeartbeat sends periodic heartbeats to the control plane
func (a *Agent) StartHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-ticker.C:
			// TODO: Send heartbeat to control plane
			log.Printf("Heartbeat: Node %s is alive", a.config.NodeID)
		}
	}
}

// Heartbeat implements the gRPC Heartbeat method
func (a *Agent) Heartbeat(ctx context.Context, req *connect.Request[pb.HeartbeatRequest]) (*connect.Response[pb.HeartbeatResponse], error) {
	log.Printf("Received heartbeat from node: %s", req.Msg.NodeId)

	return connect.NewResponse(&pb.HeartbeatResponse{
		Acknowledged:   true,
		PendingActions: []string{},
	}), nil
}

// CreateSandbox implements the gRPC CreateSandbox method
func (a *Agent) CreateSandbox(ctx context.Context, req *connect.Request[pb.CreateSandboxRequest]) (*connect.Response[pb.CreateSandboxResponse], error) {
	log.Printf("CreateSandbox: %s (template=%s, build=%s)", req.Msg.SandboxId, req.Msg.TemplateId, req.Msg.BuildId)

	spec := &SandboxSpec{
		SandboxID:   req.Msg.SandboxId,
		TemplateID:  req.Msg.TemplateId,
		BuildID:     req.Msg.BuildId,
		CPUCount:    req.Msg.CpuCount,
		MemoryMB:    req.Msg.MemoryMb,
		DiskSizeMB:  req.Msg.DiskSizeMb,
		EnvVars:     req.Msg.EnvVars,
		Metadata:    req.Msg.Metadata,
		Timeout:     time.Duration(req.Msg.TimeoutSeconds) * time.Second,
		AutoPause:   req.Msg.AutoPause,
		EnvdToken:   req.Msg.EnvdToken,
		ArtifactURL: req.Msg.ArtifactUrl,
	}

	if err := a.runtime.CreateSandbox(ctx, spec); err != nil {
		log.Printf("CreateSandbox failed: %v", err)
		return connect.NewResponse(&pb.CreateSandboxResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	// Get sandbox info to return envd address
	info, err := a.runtime.GetSandboxStatus(ctx, req.Msg.SandboxId)
	if err != nil {
		return connect.NewResponse(&pb.CreateSandboxResponse{
			Success: false,
			Error:   fmt.Sprintf("sandbox created but status unavailable: %v", err),
		}), nil
	}

	return connect.NewResponse(&pb.CreateSandboxResponse{
		Success:     true,
		EnvdPort:    info.EnvdPort,
	}), nil
}

// StopSandbox implements the gRPC StopSandbox method
func (a *Agent) StopSandbox(ctx context.Context, req *connect.Request[pb.StopSandboxRequest]) (*connect.Response[pb.StopSandboxResponse], error) {
	log.Printf("StopSandbox: %s (force=%v)", req.Msg.SandboxId, req.Msg.Force)

	if err := a.runtime.StopSandbox(ctx, req.Msg.SandboxId, req.Msg.Force); err != nil {
		return connect.NewResponse(&pb.StopSandboxResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	return connect.NewResponse(&pb.StopSandboxResponse{
		Success: true,
	}), nil
}

// PauseSandbox implements the gRPC PauseSandbox method
func (a *Agent) PauseSandbox(ctx context.Context, req *connect.Request[pb.PauseSandboxRequest]) (*connect.Response[pb.PauseSandboxResponse], error) {
	log.Printf("PauseSandbox: %s", req.Msg.SandboxId)

	snapshotID, err := a.runtime.PauseSandbox(ctx, req.Msg.SandboxId)
	if err != nil {
		return connect.NewResponse(&pb.PauseSandboxResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	return connect.NewResponse(&pb.PauseSandboxResponse{
		Success:    true,
		SnapshotId: snapshotID,
	}), nil
}

// ResumeSandbox implements the gRPC ResumeSandbox method
func (a *Agent) ResumeSandbox(ctx context.Context, req *connect.Request[pb.ResumeSandboxRequest]) (*connect.Response[pb.ResumeSandboxResponse], error) {
	log.Printf("ResumeSandbox: %s (snapshot=%s)", req.Msg.SandboxId, req.Msg.SnapshotId)

	if err := a.runtime.ResumeSandbox(ctx, req.Msg.SandboxId, req.Msg.SnapshotId); err != nil {
		return connect.NewResponse(&pb.ResumeSandboxResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	info, _ := a.runtime.GetSandboxStatus(ctx, req.Msg.SandboxId)
	envdAddr := ""
	if info != nil {
		envdAddr = info.EnvdAddress
	}

	return connect.NewResponse(&pb.ResumeSandboxResponse{
		Success:     true,
		EnvdAddress: envdAddr,
	}), nil
}

// GetSandboxStatus implements the gRPC GetSandboxStatus method
func (a *Agent) GetSandboxStatus(ctx context.Context, req *connect.Request[pb.GetSandboxStatusRequest]) (*connect.Response[pb.GetSandboxStatusResponse], error) {
	info, err := a.runtime.GetSandboxStatus(ctx, req.Msg.SandboxId)
	if err != nil {
		return connect.NewResponse(&pb.GetSandboxStatusResponse{
			Exists: false,
		}), nil
	}

	return connect.NewResponse(&pb.GetSandboxStatusResponse{
		Exists: true,
		Sandbox: &pb.SandboxInfo{
			SandboxId:   info.SandboxID,
			TemplateId:  info.TemplateID,
			BuildId:     info.BuildID,
			State:       info.State,
			EnvdAddress: info.EnvdAddress,
			StartedAt:   info.StartedAt,
			ExpiresAt:   info.ExpiresAt,
			CpuCount:    info.CPUCount,
			MemoryMb:    info.MemoryMB,
			Error:       info.Error,
		},
	}), nil
}

// ListSandboxes implements the gRPC ListSandboxes method
func (a *Agent) ListSandboxes(ctx context.Context, req *connect.Request[pb.ListSandboxesRequest]) (*connect.Response[pb.ListSandboxesResponse], error) {
	sandboxes, err := a.runtime.ListSandboxes(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	pbSandboxes := make([]*pb.SandboxInfo, len(sandboxes))
	for i, sb := range sandboxes {
		pbSandboxes[i] = &pb.SandboxInfo{
			SandboxId:   sb.SandboxID,
			TemplateId:  sb.TemplateID,
			BuildId:     sb.BuildID,
			State:       sb.State,
			EnvdAddress: sb.EnvdAddress,
			StartedAt:   sb.StartedAt,
			ExpiresAt:   sb.ExpiresAt,
			CpuCount:    sb.CPUCount,
			MemoryMb:    sb.MemoryMB,
			Error:       sb.Error,
		}
	}

	return connect.NewResponse(&pb.ListSandboxesResponse{
		Sandboxes: pbSandboxes,
	}), nil
}

// PullArtifact implements the gRPC PullArtifact method
func (a *Agent) PullArtifact(ctx context.Context, req *connect.Request[pb.PullArtifactRequest]) (*connect.Response[pb.PullArtifactResponse], error) {
	log.Printf("PullArtifact: template=%s build=%s", req.Msg.TemplateId, req.Msg.BuildId)

	// For now, just try to pull the docker image
	// In production, this would download the rootfs artifact
	imageRef := fmt.Sprintf("e2b/%s:%s", req.Msg.TemplateId, req.Msg.BuildId)

	cmd := exec.CommandContext(ctx, "docker", "pull", imageRef)
	if out, err := cmd.CombinedOutput(); err != nil {
		// If pull fails, the image might be local-only
		log.Printf("PullArtifact: docker pull failed (may be local): %s", string(out))
	}

	return connect.NewResponse(&pb.PullArtifactResponse{
		Success:   true,
		LocalPath: a.config.ArtifactsDir + "/" + req.Msg.TemplateId + "/" + req.Msg.BuildId,
	}), nil
}

// ExecCommand implements the gRPC ExecCommand streaming method
func (a *Agent) ExecCommand(ctx context.Context, req *connect.Request[pb.ExecCommandRequest], stream *connect.ServerStream[pb.ExecCommandResponse]) error {
	log.Printf("ExecCommand: sandbox=%s cmd=%s", req.Msg.SandboxId, req.Msg.Command)

	outputCh, err := a.runtime.ExecCommand(ctx, req.Msg.SandboxId, req.Msg.Command, req.Msg.Args)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}

	for output := range outputCh {
		resp := &pb.ExecCommandResponse{}
		if output.Stdout != nil {
			resp.Output = &pb.ExecCommandResponse_Stdout{Stdout: output.Stdout}
		} else if output.Stderr != nil {
			resp.Output = &pb.ExecCommandResponse_Stderr{Stderr: output.Stderr}
		} else if output.ExitCode != nil {
			resp.Output = &pb.ExecCommandResponse_ExitCode{ExitCode: *output.ExitCode}
		}

		if err := stream.Send(resp); err != nil {
			return err
		}
	}

	return nil
}
