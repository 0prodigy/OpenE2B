package scheduler

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DockerRuntime implements NodeOperations using local Docker CLI.
// This is used for in-process execution when the API and compute run together.
type DockerRuntime struct {
	mu sync.RWMutex

	// envdPort is the default port for envd inside containers
	envdPort int
	// envdBinaryPath is the path to the envd binary to inject into containers
	envdBinaryPath string
	// sandboxPorts maps sandbox IDs to their assigned host ports
	sandboxPorts map[string]int
	// availablePorts is a list of ports that can be reused
	availablePorts []int
	// nextNewPort is the next port to use if no available ports
	nextNewPort int
}

// NewDockerRuntime creates a new Docker-based runtime
func NewDockerRuntime() *DockerRuntime {
	// Try to find the envd binary
	envdPath := findEnvdBinary()
	if envdPath != "" {
		log.Printf("[docker] Using envd binary: %s", envdPath)
	} else {
		log.Printf("[docker] Warning: envd binary not found, sandboxes will run without envd")
	}

	return &DockerRuntime{
		envdPort:       49983,
		envdBinaryPath: envdPath,
		sandboxPorts:   make(map[string]int),
		availablePorts: []int{},
		nextNewPort:    49983, // Start from the default envd port
	}
}

// allocatePort returns an available port for a new sandbox
func (d *DockerRuntime) allocatePort() int {
	// Try to reuse an available port first
	if len(d.availablePorts) > 0 {
		port := d.availablePorts[0]
		d.availablePorts = d.availablePorts[1:]
		return port
	}
	// Otherwise allocate a new port
	port := d.nextNewPort
	d.nextNewPort++
	return port
}

// releasePort returns a port to the available pool
func (d *DockerRuntime) releasePort(port int) {
	d.availablePorts = append(d.availablePorts, port)
}

// findEnvdBinary looks for the envd binary in common locations
func findEnvdBinary() string {
	// Check common locations
	paths := []string{
		"./bin/envd-linux-amd64",
		"./bin/envd",
		"./envd-linux-amd64",
		"./envd",
		"/usr/local/bin/envd",
	}

	for _, p := range paths {
		absPath, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if _, err := os.Stat(absPath); err == nil {
			return absPath
		}
	}

	return ""
}

func (d *DockerRuntime) CreateSandbox(ctx context.Context, node *Node, spec SandboxSpec) (*SandboxResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Determine the image to use:
	// 1. If BuildID is set, use e2b/{templateID}:{buildID}
	// 2. Otherwise use the template ID as image name
	// 3. Fallback to ubuntu:22.04
	var image string
	if spec.BuildID != "" {
		image = fmt.Sprintf("e2b/%s:%s", spec.TemplateID, spec.BuildID)
	} else if spec.TemplateID != "" && spec.TemplateID != "base" {
		image = spec.TemplateID
	} else {
		image = "ubuntu:22.04"
	}

	// Check if image exists locally
	checkCmd := exec.CommandContext(ctx, "docker", "image", "inspect", image)
	if err := checkCmd.Run(); err != nil {
		log.Printf("Image %s not found locally, falling back to ubuntu:22.04", image)
		image = "ubuntu:22.04"
	}

	args := []string{
		"run", "-d",
		"--name", spec.ID,
		"--label", fmt.Sprintf("e2b.sandbox=%s", spec.ID),
		"--label", fmt.Sprintf("e2b.template=%s", spec.TemplateID),
		"--label", fmt.Sprintf("e2b.build=%s", spec.BuildID),
	}

	// Resource limits
	if spec.CPUCount > 0 {
		args = append(args, fmt.Sprintf("--cpus=%d", spec.CPUCount))
	}
	if spec.MemoryMB > 0 {
		args = append(args, fmt.Sprintf("-m=%dm", spec.MemoryMB))
	}

	// Port mapping for envd - allocate a unique host port
	hostPort := d.allocatePort()
	d.sandboxPorts[spec.ID] = hostPort
	args = append(args, "-p", fmt.Sprintf("%d:%d", hostPort, d.envdPort))

	// Environment variables
	for key, value := range spec.EnvVars {
		args = append(args, "-e", fmt.Sprintf("%s=%s", key, value))
	}

	// Add envd configuration
	if spec.EnvdToken != "" {
		args = append(args, "-e", fmt.Sprintf("E2B_ACCESS_TOKEN=%s", spec.EnvdToken))
	}
	args = append(args, "-e", fmt.Sprintf("E2B_ENVD_PORT=%d", d.envdPort))

	// Working directory
	args = append(args, "-w", "/home/user")

	// The image and command - keep container running with a shell that we can use to start envd
	args = append(args, image, "sleep", "infinity")

	log.Printf("[docker] Creating sandbox: docker %s", strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, "docker", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		d.releasePort(hostPort)
		delete(d.sandboxPorts, spec.ID)
		return nil, fmt.Errorf("docker run failed: %s: %w", string(out), err)
	}

	log.Printf("[docker] Sandbox %s created with image %s on port %d", spec.ID, image, hostPort)

	// Inject and start envd if binary is available
	if d.envdBinaryPath != "" {
		if err := d.injectAndStartEnvd(ctx, spec.ID, spec.EnvdToken); err != nil {
			log.Printf("[docker] Warning: failed to start envd in sandbox %s: %v", spec.ID, err)
			// Don't fail the sandbox creation, envd is optional for basic operations
		}
	}

	return &SandboxResult{
		HostPort: hostPort,
		EnvdPort: d.envdPort,
	}, nil
}

// injectAndStartEnvd copies the envd binary into the container and starts it
func (d *DockerRuntime) injectAndStartEnvd(ctx context.Context, sandboxID, envdToken string) error {
	// Copy envd binary into container
	copyCmd := exec.CommandContext(ctx, "docker", "cp", d.envdBinaryPath, fmt.Sprintf("%s:/usr/local/bin/envd", sandboxID))
	if out, err := copyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to copy envd binary: %s: %w", string(out), err)
	}

	// Make it executable
	chmodCmd := exec.CommandContext(ctx, "docker", "exec", sandboxID, "chmod", "+x", "/usr/local/bin/envd")
	if out, err := chmodCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to chmod envd: %s: %w", string(out), err)
	}

	// Start envd in the background
	// We use nohup and redirect output to a log file
	startArgs := []string{
		"exec", "-d", sandboxID,
		"/bin/sh", "-c",
		fmt.Sprintf("E2B_ACCESS_TOKEN=%s E2B_ENVD_PORT=%d /usr/local/bin/envd > /var/log/envd.log 2>&1",
			envdToken, d.envdPort),
	}

	startCmd := exec.CommandContext(ctx, "docker", startArgs...)
	if out, err := startCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start envd: %s: %w", string(out), err)
	}

	// Give envd a moment to start
	time.Sleep(100 * time.Millisecond)

	// Verify envd is running by checking the process
	checkCmd := exec.CommandContext(ctx, "docker", "exec", sandboxID, "pgrep", "-f", "envd")
	if err := checkCmd.Run(); err != nil {
		// Try to get the log for debugging
		logCmd := exec.CommandContext(ctx, "docker", "exec", sandboxID, "cat", "/var/log/envd.log")
		logOut, _ := logCmd.Output()
		return fmt.Errorf("envd not running, log: %s", string(logOut))
	}

	log.Printf("[docker] envd started in sandbox %s on port %d", sandboxID, d.envdPort)
	return nil
}

func (d *DockerRuntime) StopSandbox(ctx context.Context, node *Node, sandboxID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	log.Printf("[docker] Stopping sandbox: %s", sandboxID)
	cmd := exec.CommandContext(ctx, "docker", "rm", "-f", sandboxID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker rm failed: %s: %w", string(out), err)
	}
	// Release port back to the pool
	if port, ok := d.sandboxPorts[sandboxID]; ok {
		d.releasePort(port)
		delete(d.sandboxPorts, sandboxID)
	}
	return nil
}

func (d *DockerRuntime) PauseSandbox(ctx context.Context, node *Node, sandboxID string) error {
	log.Printf("[docker] Pausing sandbox: %s", sandboxID)
	cmd := exec.CommandContext(ctx, "docker", "pause", sandboxID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker pause failed: %s: %w", string(out), err)
	}
	return nil
}

func (d *DockerRuntime) ResumeSandbox(ctx context.Context, node *Node, sandboxID string) error {
	log.Printf("[docker] Resuming sandbox: %s", sandboxID)
	cmd := exec.CommandContext(ctx, "docker", "unpause", sandboxID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker unpause failed: %s: %w", string(out), err)
	}
	return nil
}

// GetSandboxHost returns the host and port for a sandbox (for proxy routing)
func (d *DockerRuntime) GetSandboxHost(sandboxID string) (host string, port int, ok bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	port, ok = d.sandboxPorts[sandboxID]
	if !ok {
		return "", 0, false
	}
	return "localhost", port, true
}

// GetSandboxStatus returns the status of a sandbox by inspecting the container
func (d *DockerRuntime) GetSandboxStatus(ctx context.Context, sandboxID string) (*SandboxStatus, error) {
	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Status}}", sandboxID)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("container not found: %s", sandboxID)
	}

	status := strings.TrimSpace(string(out))
	var state SandboxState
	switch status {
	case "running":
		state = SandboxStateRunning
	case "paused":
		state = SandboxStatePaused
	case "exited", "dead":
		state = SandboxStateStopped
	default:
		state = SandboxStateError
	}

	d.mu.RLock()
	envdPort := d.sandboxPorts[sandboxID]
	d.mu.RUnlock()

	return &SandboxStatus{
		State:    state,
		HostPort: envdPort,
		EnvdPort: d.envdPort,
	}, nil
}
