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
	artifactsDir string
	sandboxesDir string
	sandboxes    map[string]*dockerSandbox
	mu           sync.RWMutex
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
	return &DockerRuntime{
		artifactsDir: artifactsDir,
		sandboxesDir: sandboxesDir,
		sandboxes:    make(map[string]*dockerSandbox),
	}
}

func (r *DockerRuntime) CreateSandbox(ctx context.Context, spec *SandboxSpec) error {
	// Determine the image to use
	image := fmt.Sprintf("e2b/%s:%s", spec.TemplateID, spec.BuildID)
	
	// Check if image exists locally
	checkCmd := exec.CommandContext(ctx, "docker", "image", "inspect", image)
	if err := checkCmd.Run(); err != nil {
		// Image doesn't exist, try using ubuntu:22.04 as fallback
		log.Printf("Image %s not found locally, using ubuntu:22.04", image)
		image = "ubuntu:22.04"
	}

	// Find an available port for envd (49983 is the default, but we need to map it)
	envdPort := int32(49983)
	
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

	// Port mapping for envd
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
		return fmt.Errorf("docker run failed: %s: %w", string(output), err)
	}

	// Track the sandbox
	now := time.Now()
	r.mu.Lock()
	r.sandboxes[spec.SandboxID] = &dockerSandbox{
		spec:      spec,
		state:     "running",
		startedAt: now,
		expiresAt: now.Add(spec.Timeout),
		envdPort:  envdPort,
	}
	r.mu.Unlock()

	log.Printf("Sandbox %s created successfully", spec.SandboxID)
	return nil
}

func (r *DockerRuntime) StopSandbox(ctx context.Context, sandboxID string, force bool) error {
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

	r.mu.Lock()
	delete(r.sandboxes, sandboxID)
	r.mu.Unlock()

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
