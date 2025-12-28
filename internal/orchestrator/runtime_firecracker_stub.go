//go:build !linux
// +build !linux

package orchestrator

import (
	"context"
	"fmt"
)

// FirecrackerRuntime is a stub for non-Linux platforms
// Firecracker only runs on Linux with KVM support
type FirecrackerRuntime struct{}

// NewFirecrackerRuntime returns a stub runtime that returns errors on non-Linux platforms
func NewFirecrackerRuntime(artifactsDir, sandboxesDir string) *FirecrackerRuntime {
	return &FirecrackerRuntime{}
}

func (r *FirecrackerRuntime) CreateSandbox(ctx context.Context, spec *SandboxSpec) error {
	return fmt.Errorf("Firecracker runtime is only available on Linux with KVM support")
}

func (r *FirecrackerRuntime) StopSandbox(ctx context.Context, sandboxID string, force bool) error {
	return fmt.Errorf("Firecracker runtime is only available on Linux with KVM support")
}

func (r *FirecrackerRuntime) PauseSandbox(ctx context.Context, sandboxID string) (string, error) {
	return "", fmt.Errorf("Firecracker runtime is only available on Linux with KVM support")
}

func (r *FirecrackerRuntime) ResumeSandbox(ctx context.Context, sandboxID, snapshotID string) error {
	return fmt.Errorf("Firecracker runtime is only available on Linux with KVM support")
}

func (r *FirecrackerRuntime) GetSandboxStatus(ctx context.Context, sandboxID string) (*SandboxInfo, error) {
	return nil, fmt.Errorf("Firecracker runtime is only available on Linux with KVM support")
}

func (r *FirecrackerRuntime) ListSandboxes(ctx context.Context) ([]*SandboxInfo, error) {
	return nil, fmt.Errorf("Firecracker runtime is only available on Linux with KVM support")
}

func (r *FirecrackerRuntime) ExecCommand(ctx context.Context, sandboxID, command string, args []string) (<-chan ExecOutput, error) {
	return nil, fmt.Errorf("Firecracker runtime is only available on Linux with KVM support")
}
