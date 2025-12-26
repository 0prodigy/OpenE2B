package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// FirecrackerRuntime implements SandboxRuntime using Firecracker microVMs
// This is the production runtime for running on Linux hosts with KVM support
type FirecrackerRuntime struct {
	artifactsDir string
	sandboxesDir string
	sandboxes    map[string]*firecrackerSandbox
	mu           sync.RWMutex
	kernelPath   string
}

type firecrackerSandbox struct {
	spec       *SandboxSpec
	state      string
	socketPath string
	rootfsPath string
	startedAt  time.Time
	expiresAt  time.Time
	envdPort   int32
	pid        int
}

// NewFirecrackerRuntime creates a new Firecracker-based runtime
func NewFirecrackerRuntime(artifactsDir, sandboxesDir string) *FirecrackerRuntime {
	return &FirecrackerRuntime{
		artifactsDir: artifactsDir,
		sandboxesDir: sandboxesDir,
		sandboxes:    make(map[string]*firecrackerSandbox),
		kernelPath:   "/opt/e2b/vmlinux", // Default kernel path
	}
}

func (r *FirecrackerRuntime) CreateSandbox(ctx context.Context, spec *SandboxSpec) error {
	// Check if Firecracker is available
	if _, err := exec.LookPath("firecracker"); err != nil {
		return fmt.Errorf("firecracker not found in PATH - are you on a Linux host with KVM?")
	}

	// Check KVM availability
	if _, err := os.Stat("/dev/kvm"); os.IsNotExist(err) {
		return fmt.Errorf("/dev/kvm not available - KVM is required for Firecracker")
	}

	// Create sandbox directory
	sandboxDir := filepath.Join(r.sandboxesDir, spec.SandboxID)
	if err := os.MkdirAll(sandboxDir, 0755); err != nil {
		return fmt.Errorf("failed to create sandbox directory: %w", err)
	}

	// Locate the rootfs artifact
	rootfsPath := filepath.Join(r.artifactsDir, spec.TemplateID, spec.BuildID, "rootfs.ext4")
	if _, err := os.Stat(rootfsPath); os.IsNotExist(err) {
		// Try to convert from rootfs.tar if available
		tarPath := filepath.Join(r.artifactsDir, spec.TemplateID, spec.BuildID, "rootfs.tar")
		if _, err := os.Stat(tarPath); os.IsNotExist(err) {
			return fmt.Errorf("rootfs artifact not found for template %s build %s", spec.TemplateID, spec.BuildID)
		}
		
		// Convert tar to ext4
		if err := r.convertTarToExt4(ctx, tarPath, rootfsPath, int(spec.DiskSizeMB)); err != nil {
			return fmt.Errorf("failed to convert rootfs: %w", err)
		}
	}

	// Copy rootfs to sandbox directory (each VM needs its own copy for writes)
	vmRootfs := filepath.Join(sandboxDir, "rootfs.ext4")
	cpCmd := exec.CommandContext(ctx, "cp", "--reflink=auto", rootfsPath, vmRootfs)
	if out, err := cpCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to copy rootfs: %s: %w", string(out), err)
	}

	// Create Firecracker socket
	socketPath := filepath.Join(sandboxDir, "firecracker.sock")

	// Create Firecracker config
	configPath := filepath.Join(sandboxDir, "config.json")
	config := r.generateConfig(spec, vmRootfs)
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	// Start Firecracker
	cmd := exec.CommandContext(ctx, "firecracker",
		"--api-sock", socketPath,
		"--config-file", configPath,
	)
	cmd.Dir = sandboxDir

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start firecracker: %w", err)
	}

	// Track the sandbox
	now := time.Now()
	r.mu.Lock()
	r.sandboxes[spec.SandboxID] = &firecrackerSandbox{
		spec:       spec,
		state:      "running",
		socketPath: socketPath,
		rootfsPath: vmRootfs,
		startedAt:  now,
		expiresAt:  now.Add(spec.Timeout),
		envdPort:   49983,
		pid:        cmd.Process.Pid,
	}
	r.mu.Unlock()

	log.Printf("Firecracker sandbox %s started (PID: %d)", spec.SandboxID, cmd.Process.Pid)
	return nil
}

func (r *FirecrackerRuntime) convertTarToExt4(ctx context.Context, tarPath, ext4Path string, sizeMB int) error {
	if sizeMB <= 0 {
		sizeMB = 2048 // Default 2GB
	}

	// Create sparse file
	ddCmd := exec.CommandContext(ctx, "dd", "if=/dev/zero", "of="+ext4Path, "bs=1M", "count=0", fmt.Sprintf("seek=%d", sizeMB))
	if out, err := ddCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("dd failed: %s: %w", string(out), err)
	}

	// Format as ext4
	mkfsCmd := exec.CommandContext(ctx, "mkfs.ext4", "-F", ext4Path)
	if out, err := mkfsCmd.CombinedOutput(); err != nil {
		os.Remove(ext4Path)
		return fmt.Errorf("mkfs.ext4 failed: %s: %w", string(out), err)
	}

	// Mount and extract
	mountPoint := ext4Path + ".mount"
	os.MkdirAll(mountPoint, 0755)
	defer os.RemoveAll(mountPoint)

	mountCmd := exec.CommandContext(ctx, "mount", "-o", "loop", ext4Path, mountPoint)
	if out, err := mountCmd.CombinedOutput(); err != nil {
		os.Remove(ext4Path)
		return fmt.Errorf("mount failed: %s: %w", string(out), err)
	}
	defer exec.CommandContext(ctx, "umount", mountPoint).Run()

	// Extract tar
	tarCmd := exec.CommandContext(ctx, "tar", "-xf", tarPath, "-C", mountPoint)
	if out, err := tarCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tar extract failed: %s: %w", string(out), err)
	}

	return nil
}

func (r *FirecrackerRuntime) generateConfig(spec *SandboxSpec, rootfsPath string) string {
	// Generate Firecracker JSON config
	return fmt.Sprintf(`{
  "boot-source": {
    "kernel_image_path": "%s",
    "boot_args": "console=ttyS0 reboot=k panic=1 pci=off init=/sbin/init"
  },
  "drives": [
    {
      "drive_id": "rootfs",
      "path_on_host": "%s",
      "is_root_device": true,
      "is_read_only": false
    }
  ],
  "machine-config": {
    "vcpu_count": %d,
    "mem_size_mib": %d
  },
  "network-interfaces": [
    {
      "iface_id": "eth0",
      "guest_mac": "AA:FC:00:00:00:01",
      "host_dev_name": "tap0"
    }
  ]
}`, r.kernelPath, rootfsPath, spec.CPUCount, spec.MemoryMB)
}

func (r *FirecrackerRuntime) StopSandbox(ctx context.Context, sandboxID string, force bool) error {
	r.mu.Lock()
	sb, exists := r.sandboxes[sandboxID]
	if exists {
		delete(r.sandboxes, sandboxID)
	}
	r.mu.Unlock()

	if !exists {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	// Kill the Firecracker process
	if sb.pid > 0 {
		if force {
			exec.CommandContext(ctx, "kill", "-9", fmt.Sprintf("%d", sb.pid)).Run()
		} else {
			exec.CommandContext(ctx, "kill", fmt.Sprintf("%d", sb.pid)).Run()
		}
	}

	// Clean up sandbox directory
	sandboxDir := filepath.Join(r.sandboxesDir, sandboxID)
	os.RemoveAll(sandboxDir)

	return nil
}

func (r *FirecrackerRuntime) PauseSandbox(ctx context.Context, sandboxID string) (string, error) {
	r.mu.RLock()
	sb, exists := r.sandboxes[sandboxID]
	r.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	// Use Firecracker's snapshot API via socket
	// For now, we just send a pause request to the API
	snapshotID := fmt.Sprintf("%s-snapshot-%d", sandboxID, time.Now().Unix())
	
	// TODO: Implement actual Firecracker snapshot via API socket
	log.Printf("PauseSandbox: Would snapshot to %s (socket: %s)", snapshotID, sb.socketPath)

	r.mu.Lock()
	sb.state = "paused"
	r.mu.Unlock()

	return snapshotID, nil
}

func (r *FirecrackerRuntime) ResumeSandbox(ctx context.Context, sandboxID, snapshotID string) error {
	r.mu.Lock()
	sb, exists := r.sandboxes[sandboxID]
	if exists {
		sb.state = "running"
	}
	r.mu.Unlock()

	if !exists {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	// TODO: Implement actual Firecracker resume from snapshot
	log.Printf("ResumeSandbox: Would resume from %s", snapshotID)

	return nil
}

func (r *FirecrackerRuntime) GetSandboxStatus(ctx context.Context, sandboxID string) (*SandboxInfo, error) {
	r.mu.RLock()
	sb, exists := r.sandboxes[sandboxID]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	return &SandboxInfo{
		SandboxID:   sandboxID,
		TemplateID:  sb.spec.TemplateID,
		BuildID:     sb.spec.BuildID,
		State:       sb.state,
		EnvdAddress: fmt.Sprintf("192.168.0.2:%d", sb.envdPort), // TODO: Get actual IP from TAP
		EnvdPort:    sb.envdPort,
		StartedAt:   sb.startedAt.Unix(),
		ExpiresAt:   sb.expiresAt.Unix(),
		CPUCount:    sb.spec.CPUCount,
		MemoryMB:    sb.spec.MemoryMB,
	}, nil
}

func (r *FirecrackerRuntime) ListSandboxes(ctx context.Context) ([]*SandboxInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var sandboxes []*SandboxInfo
	for id, sb := range r.sandboxes {
		sandboxes = append(sandboxes, &SandboxInfo{
			SandboxID:   id,
			TemplateID:  sb.spec.TemplateID,
			BuildID:     sb.spec.BuildID,
			State:       sb.state,
			EnvdAddress: fmt.Sprintf("192.168.0.2:%d", sb.envdPort),
			EnvdPort:    sb.envdPort,
			StartedAt:   sb.startedAt.Unix(),
			ExpiresAt:   sb.expiresAt.Unix(),
			CPUCount:    sb.spec.CPUCount,
			MemoryMB:    sb.spec.MemoryMB,
		})
	}

	return sandboxes, nil
}

func (r *FirecrackerRuntime) ExecCommand(ctx context.Context, sandboxID, command string, args []string) (<-chan ExecOutput, error) {
	// For Firecracker, commands need to go through the envd inside the VM
	// This would require connecting to envd via the network
	outputCh := make(chan ExecOutput, 1)
	
	go func() {
		defer close(outputCh)
		
		r.mu.RLock()
		sb, exists := r.sandboxes[sandboxID]
		r.mu.RUnlock()
		
		if !exists {
			errMsg := fmt.Sprintf("sandbox not found: %s", sandboxID)
			outputCh <- ExecOutput{Stderr: []byte(errMsg)}
			return
		}

		// TODO: Connect to envd gRPC and execute command
		log.Printf("ExecCommand: Would execute '%s %v' via envd at %s:%d", command, args, "192.168.0.2", sb.envdPort)
		
		exitCode := int32(0)
		outputCh <- ExecOutput{Stdout: []byte(fmt.Sprintf("[stub] Would execute: %s %v\n", command, args))}
		outputCh <- ExecOutput{ExitCode: &exitCode}
	}()

	return outputCh, nil
}
