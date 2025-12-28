package orchestrator

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/docker/go-sdk/client"
	"github.com/docker/go-sdk/container"
	"github.com/moby/moby/api/types/network"
	dockerclient "github.com/moby/moby/client"
)

type DockerRuntimeSDK struct {
	artifactsDir   string
	sandboxesDir   string
	sandboxes      map[string]*dockerSandbox
	mu             sync.RWMutex
	nextPort       int32
	availablePorts []int32
	envdBinaryPath string
}

func NewDockerRuntimeSDK(artifactsDir, sandboxesDir string) *DockerRuntimeSDK {
	envdPath := findEnvdBinary()
	if envdPath != "" {
		log.Printf("[docker-runtime] Using envd binary: %s", envdPath)
	} else {
		log.Printf("[docker-runtime] Warning: envd binary not found, sandboxes will run without envd")
	}

	return &DockerRuntimeSDK{
		artifactsDir:   artifactsDir,
		sandboxesDir:   sandboxesDir,
		sandboxes:      make(map[string]*dockerSandbox),
		mu:             sync.RWMutex{},
		nextPort:       49983,
		availablePorts: []int32{},
		envdBinaryPath: envdPath,
	}
}

func (r *DockerRuntimeSDK) CreateSandbox(ctx context.Context, spec *SandboxSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	image := fmt.Sprintf("e2b/%s:%s", spec.TemplateID, spec.BuildID)
	// envdCmd := exec.NewRawCommand(
	// 	[]string{
	// 		"/bin/sh", "-c",
	// 		fmt.Sprintf("E2B_ACCESS_TOKEN=%s E2B_ENVD_PORT=%d /usr/local/bin/envd > /var/log/envd.log 2>&1", spec.EnvdToken, 49983),
	// 	},
	// 	exec.WithUser("user"),
	// 	exec.WithWorkingDir("/home/user"),
	// )

	// check health check 49983/health
	// Recreate with host networking: --network=host
	healthPort, ok := network.PortFrom(49983, network.TCP)
	if !ok {
		return fmt.Errorf("failed to create health port: %v", healthPort)
	}
	// waitStrategy := wait.NewHTTPStrategy("/health").WithPort(healthPort).WithTimeout(time.Second * 5)

	ctr, err := container.Run(
		context.Background(),
		container.WithImage(image),
		container.WithName(spec.SandboxID),
		// container.WithWaitStrategy(wait.ForExec([]string{"envd", "version"}).WithTimeout(time.Second*5)),
		// container.WithAdditionalWaitStrategy(wait.ForHealthCheck().WithTimeout(time.Second*5)),
		container.WithFiles(container.File{
			HostPath:      r.envdBinaryPath,
			ContainerPath: "/usr/local/bin/envd",
			Mode:          0755,
		}),
		container.WithEnv(map[string]string{
			"E2B_ACCESS_TOKEN": spec.EnvdToken,
			"E2B_ENVD_PORT":    fmt.Sprintf("%d", 49983),
		}),
		container.WithEntrypoint([]string{"envd"}...),
		container.WithLabels(map[string]string{
			"e2b.sandbox.id":  spec.SandboxID,
			"e2b.template.id": spec.TemplateID,
			"e2b.build.id":    spec.BuildID,
		}),
		container.WithExposedPorts(fmt.Sprintf("%d/tcp", 49983)),
	)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	// get exposed ports
	mappedPort, err := ctr.MappedPort(ctx, healthPort)
	if err != nil {
		return fmt.Errorf("failed to get host config: %w", err)
	}
	log.Printf("Mapped Port: %d/tcp", mappedPort.Num())

	r.sandboxes[spec.SandboxID] = &dockerSandbox{
		spec:        spec,
		state:       "running",
		startedAt:   time.Now(),
		expiresAt:   time.Now().Add(spec.Timeout),
		envdPort:    int32(mappedPort.Num()),
		envdAddress: "127.0.0.1",
	}
	log.Printf("Sandbox %s created successfully on port %d", spec.SandboxID, mappedPort.Num())
	return nil
}

func (r *DockerRuntimeSDK) StopSandbox(ctx context.Context, sandboxID string, force bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	dockerClient, err := client.New(ctx)
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}

	response, err := dockerClient.ContainerList(ctx, dockerclient.ContainerListOptions{
		All:     true,
		Filters: make(dockerclient.Filters).Add("label", "e2b.sandbox.id="+sandboxID),
	})
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	if len(response.Items) == 0 {
		return fmt.Errorf("container %s not found", sandboxID)
	}

	ctr, err := container.FromResponse(ctx, dockerClient, response.Items[0])
	if err != nil {
		return fmt.Errorf("failed to get container: %w", err)
	}

	err = ctr.Terminate(ctx)
	if err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}
	return nil
}

func (r *DockerRuntimeSDK) PauseSandbox(ctx context.Context, sandboxID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return "", nil
}

func (r *DockerRuntimeSDK) ResumeSandbox(ctx context.Context, sandboxID, snapshotID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return nil
}

func (r *DockerRuntimeSDK) GetSandboxStatus(ctx context.Context, sandboxID string) (*SandboxInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	r.mu.RLock()
	sb, exists := r.sandboxes[sandboxID]
	r.mu.RUnlock()

	if exists {
		return &SandboxInfo{
			SandboxID:   sandboxID,
			TemplateID:  sb.spec.TemplateID,
			BuildID:     sb.spec.BuildID,
			State:       sb.state,
			EnvdAddress: sb.envdAddress,
			EnvdPort:    sb.envdPort,
			StartedAt:   sb.startedAt.Unix(),
			ExpiresAt:   sb.expiresAt.Unix(),
			CPUCount:    sb.spec.CPUCount,
			MemoryMB:    sb.spec.MemoryMB,
		}, nil
	}

	dockerClient, err := client.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	ctr, err := container.FromID(ctx, dockerClient, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("failed to get container: %w", err)
	}

	status := "unknown"

	state, err := ctr.State(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container state: %w", err)
	}

	if state.Running {
		status = "running"
	} else if state.Paused {
		status = "paused"
	} else {
		status = "stopped"
	}
	healthPort, ok := network.PortFrom(49983, network.TCP)
	if !ok {
		return nil, fmt.Errorf("failed to create health port: %v", healthPort.Num())
	}
	mappedPort, err := ctr.MappedPort(ctx, healthPort)
	if err != nil {
		return nil, fmt.Errorf("failed to get mapped port: %w", err)
	}
	return &SandboxInfo{
		SandboxID:   sandboxID,
		TemplateID:  sb.spec.TemplateID,
		BuildID:     sb.spec.BuildID,
		State:       status,
		StartedAt:   time.Now().Unix(),
		ExpiresAt:   time.Now().Add(sb.spec.Timeout).Unix(),
		CPUCount:    sb.spec.CPUCount,
		MemoryMB:    sb.spec.MemoryMB,
		EnvdPort:    int32(mappedPort.Num()),
		EnvdAddress: "127.0.0.1",
	}, nil
}

func (r *DockerRuntimeSDK) ListSandboxes(ctx context.Context) ([]*SandboxInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return nil, nil
}

func (r *DockerRuntimeSDK) ExecCommand(ctx context.Context, sandboxID, command string, args []string) (<-chan ExecOutput, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return nil, nil
}

// allocatePort returns an available port for a new sandbox
func (r *DockerRuntimeSDK) allocatePort() int32 {
	// Try to reuse an available port first
	// get a random port between 49983 and 49999
	port := rand.Intn(49999-49983+1) + 49983
	return int32(port)
}
