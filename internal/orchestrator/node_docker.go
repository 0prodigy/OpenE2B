package orchestrator

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DockerNode implements NodeOperations using local Docker CLI.
// This is used for in-process execution when the API and compute run together.
type DockerNode struct {
	// envdPort is the default port for envd in sandboxes
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

// NewDockerNode creates a new Docker-based node operations handler
func NewDockerNode() *DockerNode {
	// Try to find the envd binary
	envdPath := findEnvdBinary()
	if envdPath != "" {
		log.Printf("[docker] Using envd binary: %s", envdPath)
	} else {
		log.Printf("[docker] Warning: envd binary not found, sandboxes will run without envd")
	}

	return &DockerNode{
		envdPort:       49983,
		envdBinaryPath: envdPath,
		sandboxPorts:   make(map[string]int),
		availablePorts: []int{},
		nextNewPort:    49983, // Start from the default envd port
	}
}

// allocatePort returns an available port for a new sandbox
func (n *DockerNode) allocatePort() int {
	// Try to reuse an available port first
	if len(n.availablePorts) > 0 {
		port := n.availablePorts[0]
		n.availablePorts = n.availablePorts[1:]
		return port
	}
	// Otherwise allocate a new port
	port := n.nextNewPort
	n.nextNewPort++
	return port
}

// releasePort returns a port to the available pool
func (n *DockerNode) releasePort(port int) {
	n.availablePorts = append(n.availablePorts, port)
}

// findEnvdBinary looks for the envd binary in common locations
func findEnvdBinary() string {
	// Check common locations
	paths := []string{
		"./bin/envd-linux-amd64",
		"./envd-linux-amd64",
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

func (n *DockerNode) CreateSandbox(ctx context.Context, node *Node, spec SandboxSpec) error {
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

	// Port mapping for envd - use a predictable host port for local development
	// Allocate a port (reusing if available)
	hostPort := n.allocatePort()
	n.sandboxPorts[spec.ID] = hostPort
	args = append(args, "-p", fmt.Sprintf("%d:%d", hostPort, n.envdPort))

	// Environment variables
	for key, value := range spec.EnvVars {
		args = append(args, "-e", fmt.Sprintf("%s=%s", key, value))
	}

	// Add envd configuration
	if spec.EnvdToken != "" {
		args = append(args, "-e", fmt.Sprintf("E2B_ACCESS_TOKEN=%s", spec.EnvdToken))
	}
	args = append(args, "-e", fmt.Sprintf("E2B_ENVD_PORT=%d", n.envdPort))

	// Working directory
	args = append(args, "-w", "/home/user")

	// The image and command - keep container running with a shell that we can use to start envd
	args = append(args, image, "sleep", "infinity")

	log.Printf("[docker] Creating sandbox: docker %s", strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, "docker", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker run failed: %s: %w", string(out), err)
	}

	log.Printf("[docker] Sandbox %s created with image %s", spec.ID, image)

	// Inject and start envd if binary is available
	if n.envdBinaryPath != "" {
		if err := n.injectAndStartEnvd(ctx, spec.ID, spec.EnvdToken); err != nil {
			log.Printf("[docker] Warning: failed to start envd in sandbox %s: %v", spec.ID, err)
			// Don't fail the sandbox creation, envd is optional for basic operations
		}
	}

	return nil
}

// injectAndStartEnvd copies the envd binary into the container and starts it
func (n *DockerNode) injectAndStartEnvd(ctx context.Context, sandboxID, envdToken string) error {
	// Copy envd binary into container
	copyCmd := exec.CommandContext(ctx, "docker", "cp", n.envdBinaryPath, fmt.Sprintf("%s:/usr/local/bin/envd", sandboxID))
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
			envdToken, n.envdPort),
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

	log.Printf("[docker] envd started in sandbox %s on port %d", sandboxID, n.envdPort)
	return nil
}

func (n *DockerNode) StopSandbox(ctx context.Context, node *Node, sandboxID string) error {
	log.Printf("[docker] Stopping sandbox: %s", sandboxID)
	cmd := exec.CommandContext(ctx, "docker", "rm", "-f", sandboxID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker rm failed: %s: %w", string(out), err)
	}
	// Release port back to the pool
	if port, ok := n.sandboxPorts[sandboxID]; ok {
		n.releasePort(port)
		delete(n.sandboxPorts, sandboxID)
	}
	return nil
}

func (n *DockerNode) PauseSandbox(ctx context.Context, node *Node, sandboxID string) error {
	log.Printf("[docker] Pausing sandbox: %s", sandboxID)
	cmd := exec.CommandContext(ctx, "docker", "pause", sandboxID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker pause failed: %s: %w", string(out), err)
	}
	return nil
}

func (n *DockerNode) ResumeSandbox(ctx context.Context, node *Node, sandboxID string) error {
	log.Printf("[docker] Resuming sandbox: %s", sandboxID)
	cmd := exec.CommandContext(ctx, "docker", "unpause", sandboxID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker unpause failed: %s: %w", string(out), err)
	}
	return nil
}

// GetSandboxStatus returns the status of a sandbox by inspecting the container
func (n *DockerNode) GetSandboxStatus(ctx context.Context, node *Node, sandboxID string) (*SandboxStatus, error) {
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

	// Get the mapped envd port
	envdPort := n.getEnvdPort(ctx, sandboxID)

	return &SandboxStatus{
		State:    state,
		NodeID:   node.Spec.ID,
		EnvdPort: envdPort,
	}, nil
}

// getEnvdPort returns the host port mapped to the envd port in the container
func (n *DockerNode) getEnvdPort(ctx context.Context, sandboxID string) int {
	// First check our cache
	if port, ok := n.sandboxPorts[sandboxID]; ok {
		return port
	}

	// Fallback to docker port command
	cmd := exec.CommandContext(ctx, "docker", "port", sandboxID, fmt.Sprintf("%d/tcp", n.envdPort))
	out, err := cmd.Output()
	if err != nil {
		return 0
	}

	// Output format is "0.0.0.0:XXXXX" or "[::]:XXXXX"
	portStr := strings.TrimSpace(string(out))
	parts := strings.Split(portStr, ":")
	if len(parts) >= 2 {
		var port int
		fmt.Sscanf(parts[len(parts)-1], "%d", &port)
		return port
	}

	return 0
}

// GetEnvdHostPort returns the host port for a sandbox (for API responses)
func (n *DockerNode) GetEnvdHostPort(sandboxID string) int {
	if port, ok := n.sandboxPorts[sandboxID]; ok {
		return port
	}
	return n.envdPort
}
