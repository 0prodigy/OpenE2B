package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DockerRuntime implements SandboxRuntime using Docker containers
type DockerRuntime struct {
	artifactsDir   string
	sandboxesDir   string
	sandboxes      map[string]*dockerSandbox
	mu             sync.RWMutex
	nextPort       int32
	availablePorts []int32
	envdBinaryPath string
}

type dockerSandbox struct {
	spec      *SandboxSpec
	state     string
	startedAt time.Time
	expiresAt time.Time
	envdPort  int32
}

// NewDockerRuntime creates a new Docker-based runtime
func NewDockerRuntime(artifactsDir, sandboxesDir string) *DockerRuntime {
	// Try to find the envd binary
	envdPath := findEnvdBinary()
	if envdPath != "" {
		log.Printf("[docker-runtime] Using envd binary: %s", envdPath)
	} else {
		log.Printf("[docker-runtime] Warning: envd binary not found, sandboxes will run without envd")
	}

	return &DockerRuntime{
		artifactsDir:   artifactsDir,
		sandboxesDir:   sandboxesDir,
		sandboxes:      make(map[string]*dockerSandbox),
		nextPort:       49983, // Start port allocation from default envd port
		availablePorts: []int32{},
		envdBinaryPath: envdPath,
	}
}

// findEnvdBinary looks for the envd binary in common locations
func findEnvdBinary() string {
	paths := []string{
		"/opt/e2b/bin/envd",
		"./bin/envd-linux-amd64",
		"./bin/envd",
		"./envd-linux-amd64",
		"./envd",
		"/usr/local/bin/envd",
	}

	for _, p := range paths {
		cmd := exec.Command("test", "-f", p)
		if err := cmd.Run(); err == nil {
			return p
		}
	}

	return ""
}

// allocatePort returns an available port for a new sandbox
func (r *DockerRuntime) allocatePort() int32 {
	// Try to reuse an available port first
	if len(r.availablePorts) > 0 {
		port := r.availablePorts[0]
		r.availablePorts = r.availablePorts[1:]
		return port
	}
	// Otherwise allocate a new port
	port := r.nextPort
	r.nextPort++
	return port
}

// releasePort returns a port to the available pool
func (r *DockerRuntime) releasePort(port int32) {
	r.availablePorts = append(r.availablePorts, port)
}

func (r *DockerRuntime) CreateSandbox(ctx context.Context, spec *SandboxSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Determine the image to use
	image := fmt.Sprintf("e2b/%s:%s", spec.TemplateID, spec.BuildID)

	// Check if image exists locally
	checkCmd := exec.CommandContext(ctx, "docker", "image", "inspect", image)
	if err := checkCmd.Run(); err != nil {
		// Image doesn't exist, try using ubuntu:22.04 as fallback
		log.Printf("Image %s not found locally, using ubuntu:22.04", image)
		image = "ubuntu:22.04"
	}

	// Allocate a unique port for envd
	envdPort := r.allocatePort()

	// Build docker run arguments
	args := []string{
		"run", "-d",
		"--name", spec.SandboxID,
		"--label", fmt.Sprintf("e2b.sandbox=%s", spec.SandboxID),
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

	// Port mapping for envd - use allocated port on host, map to 49983 inside container
	args = append(args, "-p", fmt.Sprintf("%d:49983", envdPort))

	// Environment variables
	for key, value := range spec.EnvVars {
		args = append(args, "-e", fmt.Sprintf("%s=%s", key, value))
	}

	// Add envd token
	if spec.EnvdToken != "" {
		args = append(args, "-e", fmt.Sprintf("E2B_ACCESS_TOKEN=%s", spec.EnvdToken))
	}

	// Working directory
	args = append(args, "-w", "/home/user")

	// The image and command
	args = append(args, image, "sleep", "infinity")

	log.Printf("Creating sandbox with: docker %s", strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		r.releasePort(envdPort)
		return fmt.Errorf("docker run failed: %s: %w", string(output), err)
	}

	// Track the sandbox (already holding lock from function start)
	now := time.Now()
	r.sandboxes[spec.SandboxID] = &dockerSandbox{
		spec:      spec,
		state:     "running",
		startedAt: now,
		expiresAt: now.Add(spec.Timeout),
		envdPort:  envdPort,
	}

	log.Printf("Sandbox %s created successfully on port %d", spec.SandboxID, envdPort)

	// Inject and start envd if binary is available
	if r.envdBinaryPath != "" {
		if err := r.injectAndStartEnvd(ctx, spec.SandboxID, spec.EnvdToken); err != nil {
			log.Printf("[docker-runtime] Warning: failed to start envd in sandbox %s: %v", spec.SandboxID, err)
			// Don't fail the sandbox creation, envd is optional for basic operations
		}
	}

	return nil
}

// injectAndStartEnvd copies the envd binary into the container and starts it
func (r *DockerRuntime) injectAndStartEnvd(ctx context.Context, sandboxID, envdToken string) error {
	// Copy envd binary into container
	copyCmd := exec.CommandContext(ctx, "docker", "cp", r.envdBinaryPath, fmt.Sprintf("%s:/usr/local/bin/envd", sandboxID))
	if out, err := copyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to copy envd binary: %s: %w", string(out), err)
	}

	// Make it executable
	chmodCmd := exec.CommandContext(ctx, "docker", "exec", sandboxID, "chmod", "+x", "/usr/local/bin/envd")
	if out, err := chmodCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to chmod envd: %s: %w", string(out), err)
	}

	// Start envd in the background
	startArgs := []string{
		"exec", "-d", sandboxID,
		"/bin/sh", "-c",
		fmt.Sprintf("E2B_ACCESS_TOKEN=%s E2B_ENVD_PORT=49983 /usr/local/bin/envd > /var/log/envd.log 2>&1",
			envdToken),
	}

	startCmd := exec.CommandContext(ctx, "docker", startArgs...)
	if out, err := startCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start envd: %s: %w", string(out), err)
	}

	// Give envd a moment to start
	time.Sleep(500 * time.Millisecond)

	// Verify envd is running by checking the process
	checkCmd := exec.CommandContext(ctx, "docker", "exec", sandboxID, "pgrep", "-f", "envd")
	if err := checkCmd.Run(); err != nil {
		// Try to get the log for debugging
		logCmd := exec.CommandContext(ctx, "docker", "exec", sandboxID, "cat", "/var/log/envd.log")
		logOut, _ := logCmd.Output()
		return fmt.Errorf("envd not running, log: %s", string(logOut))
	}

	log.Printf("[docker-runtime] envd started in sandbox %s on port 49983", sandboxID)
	return nil
}

func (r *DockerRuntime) StopSandbox(ctx context.Context, sandboxID string, force bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	args := []string{"rm"}
	if force {
		args = append(args, "-f")
	}
	args = append(args, sandboxID)

	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rm failed: %s: %w", string(output), err)
	}

	// Release the port back to the pool
	if sb, ok := r.sandboxes[sandboxID]; ok {
		r.releasePort(sb.envdPort)
	}
	delete(r.sandboxes, sandboxID)

	return nil
}

func (r *DockerRuntime) PauseSandbox(ctx context.Context, sandboxID string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "pause", sandboxID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker pause failed: %s: %w", string(output), err)
	}

	r.mu.Lock()
	if sb, ok := r.sandboxes[sandboxID]; ok {
		sb.state = "paused"
	}
	r.mu.Unlock()

	// Docker doesn't have a snapshot concept like Firecracker, so we just return the sandbox ID
	return sandboxID, nil
}

func (r *DockerRuntime) ResumeSandbox(ctx context.Context, sandboxID, snapshotID string) error {
	cmd := exec.CommandContext(ctx, "docker", "unpause", sandboxID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker unpause failed: %s: %w", string(output), err)
	}

	r.mu.Lock()
	if sb, ok := r.sandboxes[sandboxID]; ok {
		sb.state = "running"
	}
	r.mu.Unlock()

	return nil
}

func (r *DockerRuntime) GetSandboxStatus(ctx context.Context, sandboxID string) (*SandboxInfo, error) {
	// First check our local cache
	r.mu.RLock()
	sb, exists := r.sandboxes[sandboxID]
	r.mu.RUnlock()

	if exists {
		return &SandboxInfo{
			SandboxID:   sandboxID,
			TemplateID:  sb.spec.TemplateID,
			BuildID:     sb.spec.BuildID,
			State:       sb.state,
			EnvdAddress: fmt.Sprintf("localhost:%d", sb.envdPort),
			EnvdPort:    sb.envdPort,
			StartedAt:   sb.startedAt.Unix(),
			ExpiresAt:   sb.expiresAt.Unix(),
			CPUCount:    sb.spec.CPUCount,
			MemoryMB:    sb.spec.MemoryMB,
		}, nil
	}

	// Try to get from docker inspect
	cmd := exec.CommandContext(ctx, "docker", "inspect", sandboxID)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	var containers []struct {
		State struct {
			Running bool `json:"Running"`
			Paused  bool `json:"Paused"`
		} `json:"State"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		NetworkSettings struct {
			Ports map[string][]struct {
				HostPort string `json:"HostPort"`
			} `json:"Ports"`
		} `json:"NetworkSettings"`
	}

	if err := json.Unmarshal(output, &containers); err != nil {
		return nil, err
	}

	if len(containers) == 0 {
		return nil, fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	c := containers[0]
	state := "unknown"
	if c.State.Running {
		state = "running"
	} else if c.State.Paused {
		state = "paused"
	} else {
		state = "stopped"
	}

	// Try to get mapped port
	var envdPort int32 = 49983
	if ports, ok := c.NetworkSettings.Ports["49983/tcp"]; ok && len(ports) > 0 {
		if p, err := strconv.Atoi(ports[0].HostPort); err == nil {
			envdPort = int32(p)
		}
	}

	return &SandboxInfo{
		SandboxID:   sandboxID,
		TemplateID:  c.Config.Labels["e2b.template"],
		BuildID:     c.Config.Labels["e2b.build"],
		State:       state,
		EnvdAddress: fmt.Sprintf("localhost:%d", envdPort),
		EnvdPort:    envdPort,
	}, nil
}

func (r *DockerRuntime) ListSandboxes(ctx context.Context) ([]*SandboxInfo, error) {
	// Get all containers with e2b.sandbox label
	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--filter", "label=e2b.sandbox", "--format", "{{.Names}}")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var sandboxes []*SandboxInfo
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, name := range lines {
		if name == "" {
			continue
		}
		info, err := r.GetSandboxStatus(ctx, name)
		if err == nil {
			sandboxes = append(sandboxes, info)
		}
	}

	return sandboxes, nil
}

func (r *DockerRuntime) ExecCommand(ctx context.Context, sandboxID, command string, args []string) (<-chan ExecOutput, error) {
	outputCh := make(chan ExecOutput, 10)

	go func() {
		defer close(outputCh)

		cmdArgs := append([]string{"exec", sandboxID, command}, args...)
		cmd := exec.CommandContext(ctx, "docker", cmdArgs...)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			outputCh <- ExecOutput{Stderr: []byte(err.Error())}
			return
		}

		stderr, err := cmd.StderrPipe()
		if err != nil {
			outputCh <- ExecOutput{Stderr: []byte(err.Error())}
			return
		}

		if err := cmd.Start(); err != nil {
			outputCh <- ExecOutput{Stderr: []byte(err.Error())}
			return
		}

		// Stream stdout
		go func() {
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				outputCh <- ExecOutput{Stdout: scanner.Bytes()}
			}
		}()

		// Stream stderr
		go func() {
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				outputCh <- ExecOutput{Stderr: scanner.Bytes()}
			}
		}()

		// Wait for command to complete
		err = cmd.Wait()
		var exitCode int32 = 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = int32(exitErr.ExitCode())
			} else {
				exitCode = 1
			}
		}
		outputCh <- ExecOutput{ExitCode: &exitCode}
	}()

	return outputCh, nil
}
