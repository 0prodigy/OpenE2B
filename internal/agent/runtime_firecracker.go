//go:build linux
// +build linux

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
)

// FirecrackerRuntime implements SandboxRuntime using Firecracker microVMs
// This implementation uses the official firecracker-go-sdk for proper
// VM lifecycle management and CNI-based networking
type FirecrackerRuntime struct {
	artifactsDir   string
	sandboxesDir   string
	sandboxes      map[string]*firecrackerSandbox
	mu             sync.RWMutex
	kernelPath     string
	envdBinaryPath string

	// CNI Configuration
	cniNetworkName string   // Name of CNI network (matches conflist filename)
	cniBinPath     []string // Paths to CNI plugin binaries
	cniConfDir     string   // Directory containing CNI config files
	cniCacheDir    string   // CNI cache directory
}

type firecrackerSandbox struct {
	spec       *SandboxSpec
	state      string
	machine    *firecracker.Machine
	socketPath string
	rootfsPath string
	startedAt  time.Time
	expiresAt  time.Time
	envdPort   int32
	vmIP       string
	logFile    *os.File
}

// NewFirecrackerRuntime creates a new Firecracker-based runtime using the SDK
func NewFirecrackerRuntime(artifactsDir, sandboxesDir string) *FirecrackerRuntime {
	// Try to find the envd binary
	envdPath := findEnvdBinary()
	if envdPath != "" {
		log.Printf("[firecracker-runtime] Using envd binary: %s", envdPath)
	} else {
		log.Printf("[firecracker-runtime] Warning: envd binary not found")
	}

	// Default CNI paths
	cniBinPath := []string{"/opt/cni/bin", "/usr/lib/cni"}
	cniConfDir := "/etc/cni/net.d"
	cniCacheDir := "/var/lib/cni"

	// Check for custom CNI config in our configs directory
	customConfDir := filepath.Join(filepath.Dir(filepath.Dir(artifactsDir)), "configs", "cni")
	if _, err := os.Stat(customConfDir); err == nil {
		cniConfDir = customConfDir
		log.Printf("[firecracker-runtime] Using custom CNI config dir: %s", cniConfDir)
	}

	return &FirecrackerRuntime{
		artifactsDir:   artifactsDir,
		sandboxesDir:   sandboxesDir,
		sandboxes:      make(map[string]*firecrackerSandbox),
		kernelPath:     "/opt/e2b/vmlinux",
		envdBinaryPath: envdPath,
		cniNetworkName: "fcnet",
		cniBinPath:     cniBinPath,
		cniConfDir:     cniConfDir,
		cniCacheDir:    cniCacheDir,
	}
}

func (r *FirecrackerRuntime) CreateSandbox(ctx context.Context, spec *SandboxSpec) error {
	// Check if Firecracker is available
	firecrackerBin, err := exec.LookPath("firecracker")
	if err != nil {
		return fmt.Errorf("firecracker not found in PATH - are you on a Linux host with KVM?")
	}

	// Check KVM availability
	if _, err := os.Stat("/dev/kvm"); os.IsNotExist(err) {
		return fmt.Errorf("/dev/kvm not available - KVM is required for Firecracker")
	}

	// Check kernel exists
	if _, err := os.Stat(r.kernelPath); os.IsNotExist(err) {
		return fmt.Errorf("kernel not found at %s", r.kernelPath)
	}

	// Create sandbox directory
	sandboxDir := filepath.Join(r.sandboxesDir, spec.SandboxID)
	if err := os.MkdirAll(sandboxDir, 0755); err != nil {
		return fmt.Errorf("failed to create sandbox directory: %w", err)
	}

	// Prepare rootfs
	vmRootfs := filepath.Join(sandboxDir, "rootfs.ext4")
	// Use a placeholder IP - CNI will allocate the real one
	if err := r.prepareRootfs(ctx, spec, vmRootfs, "192.168.128.2", "192.168.128.1"); err != nil {
		os.RemoveAll(sandboxDir)
		return fmt.Errorf("failed to prepare rootfs: %w", err)
	}

	// Create Firecracker socket path
	socketPath := filepath.Join(sandboxDir, "firecracker.sock")

	// Create log file
	logFile, err := os.Create(filepath.Join(sandboxDir, "firecracker.log"))
	if err != nil {
		os.RemoveAll(sandboxDir)
		return fmt.Errorf("failed to create log file: %w", err)
	}

	// Configure CPU and memory
	cpuCount := spec.CPUCount
	if cpuCount <= 0 {
		cpuCount = 1
	}
	memoryMB := spec.MemoryMB
	if memoryMB <= 0 {
		memoryMB = 512
	}

	// Build Firecracker configuration using the SDK
	cfg := firecracker.Config{
		SocketPath:      socketPath,
		KernelImagePath: r.kernelPath,
		KernelArgs:      "console=ttyS0 reboot=k panic=1 pci=off init=/sbin/init quiet",
		Drives: []models.Drive{
			{
				DriveID:      firecracker.String("rootfs"),
				PathOnHost:   firecracker.String(vmRootfs),
				IsRootDevice: firecracker.Bool(true),
				IsReadOnly:   firecracker.Bool(false),
			},
		},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(int64(cpuCount)),
			MemSizeMib: firecracker.Int64(int64(memoryMB)),
		},
		NetworkInterfaces: []firecracker.NetworkInterface{
			{
				// Use CNI for automatic TAP creation, bridge attachment, and IP allocation
				// Note: Cannot use StaticConfiguration alongside CNIConfiguration
				CNIConfiguration: &firecracker.CNIConfiguration{
					NetworkName: r.cniNetworkName,
					IfName:      "eth0",
					BinPath:     r.cniBinPath,
					ConfDir:     r.cniConfDir,
					CacheDir:    r.cniCacheDir,
				},
			},
		},
		// MMDS for metadata (optional, can be used for cloud-init style config)
		// MmdsVersion: firecracker.MMDSv2,
	}

	// Create command to run Firecracker
	cmd := firecracker.VMCommandBuilder{}.
		WithBin(firecrackerBin).
		WithSocketPath(socketPath).
		WithStdout(logFile).
		WithStderr(logFile).
		Build(ctx)

	// Create the machine using the SDK
	// Use background context so the VM isn't killed when the request completes
	machineCtx := context.Background()
	machine, err := firecracker.NewMachine(machineCtx, cfg, firecracker.WithProcessRunner(cmd))
	if err != nil {
		logFile.Close()
		os.RemoveAll(sandboxDir)
		return fmt.Errorf("failed to create Firecracker machine: %w", err)
	}

	// Start the VM
	if err := machine.Start(machineCtx); err != nil {
		logFile.Close()
		os.RemoveAll(sandboxDir)
		return fmt.Errorf("failed to start Firecracker VM: %w", err)
	}

	// Get the IP address assigned by CNI
	var vmIP string
	if len(machine.Cfg.NetworkInterfaces) > 0 {
		ni := machine.Cfg.NetworkInterfaces[0]
		if ni.StaticConfiguration != nil && ni.StaticConfiguration.IPConfiguration != nil {
			vmIP = ni.StaticConfiguration.IPConfiguration.IPAddr.IP.String()
		}
	}

	// Fallback: try to get IP from CNI result if not populated
	if vmIP == "" {
		// The SDK should populate this after Start(), but if not, we'll wait for envd
		vmIP = "192.168.128.2" // Default first IP in range
		log.Printf("[firecracker-runtime] Warning: Could not get IP from CNI, using default: %s", vmIP)
	}

	log.Printf("[firecracker-runtime] VM %s started with IP: %s", spec.SandboxID, vmIP)

	// Wait for VM to boot and envd to start
	envdCtx, envdCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer envdCancel()
	if err := r.waitForEnvd(envdCtx, vmIP, 49983, 60*time.Second); err != nil {
		log.Printf("[firecracker-runtime] Warning: envd not responding: %v", err)
		// Don't fail - VM might still be booting
	}

	// Add /etc/hosts entry for easy access by sandbox ID
	if err := r.addHostsEntry(spec.SandboxID, vmIP); err != nil {
		log.Printf("[firecracker-runtime] Warning: failed to add hosts entry: %v", err)
	}

	// Track the sandbox
	now := time.Now()
	r.mu.Lock()
	r.sandboxes[spec.SandboxID] = &firecrackerSandbox{
		spec:       spec,
		state:      "running",
		machine:    machine,
		socketPath: socketPath,
		rootfsPath: vmRootfs,
		startedAt:  now,
		expiresAt:  now.Add(spec.Timeout),
		envdPort:   49983,
		vmIP:       vmIP,
		logFile:    logFile,
	}
	r.mu.Unlock()

	log.Printf("[firecracker-runtime] Sandbox %s created successfully (IP: %s)", spec.SandboxID, vmIP)
	return nil
}

// generateMACAddress creates a unique MAC address from sandbox ID
func generateMACAddress(sandboxID string) string {
	// Use locally administered unicast MAC address (02:xx:xx:xx:xx:xx)
	// Hash the sandbox ID to get consistent MAC addresses
	hash := uint32(0)
	for _, c := range sandboxID {
		hash = hash*31 + uint32(c)
	}
	return fmt.Sprintf("02:FC:%02X:%02X:%02X:%02X",
		(hash>>24)&0xFF,
		(hash>>16)&0xFF,
		(hash>>8)&0xFF,
		hash&0xFF,
	)
}

// addHostsEntry adds an entry to /etc/hosts for the sandbox
func (r *FirecrackerRuntime) addHostsEntry(sandboxID, vmIP string) error {
	entry := fmt.Sprintf("%s\t%s\n", vmIP, sandboxID)

	f, err := os.OpenFile("/etc/hosts", os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open /etc/hosts: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(entry); err != nil {
		return fmt.Errorf("failed to write to /etc/hosts: %w", err)
	}

	log.Printf("[firecracker-runtime] Added /etc/hosts entry: %s -> %s", sandboxID, vmIP)
	return nil
}

// removeHostsEntry removes the sandbox entry from /etc/hosts
func (r *FirecrackerRuntime) removeHostsEntry(sandboxID string) error {
	data, err := os.ReadFile("/etc/hosts")
	if err != nil {
		return fmt.Errorf("failed to read /etc/hosts: %w", err)
	}

	lines := bytes.Split(data, []byte("\n"))
	var newLines [][]byte

	for _, line := range lines {
		// Skip lines containing the sandbox ID
		if !bytes.Contains(line, []byte(sandboxID)) {
			newLines = append(newLines, line)
		}
	}

	newData := bytes.Join(newLines, []byte("\n"))
	if err := os.WriteFile("/etc/hosts", newData, 0644); err != nil {
		return fmt.Errorf("failed to write /etc/hosts: %w", err)
	}

	log.Printf("[firecracker-runtime] Removed /etc/hosts entry for %s", sandboxID)
	return nil
}

// waitForEnvd waits for envd to respond on the given address
func (r *FirecrackerRuntime) waitForEnvd(ctx context.Context, vmIP string, port int32, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("%s:%d", vmIP, port)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			conn.Close()
			log.Printf("[firecracker-runtime] envd responding at %s", addr)
			return nil
		}

		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("envd not responding at %s after %v", addr, timeout)
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

	// Stop the VM using the SDK - this handles CNI cleanup automatically
	if sb.machine != nil {
		if force {
			// Force stop - kill the process
			if err := sb.machine.StopVMM(); err != nil {
				log.Printf("[firecracker-runtime] Warning: StopVMM failed: %v", err)
			}
		} else {
			// Graceful shutdown via Firecracker API
			shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := sb.machine.Shutdown(shutdownCtx); err != nil {
				log.Printf("[firecracker-runtime] Warning: Shutdown failed, forcing stop: %v", err)
				sb.machine.StopVMM()
			}
		}
	}

	// Close log file
	if sb.logFile != nil {
		sb.logFile.Close()
	}

	// Remove /etc/hosts entry
	if err := r.removeHostsEntry(sandboxID); err != nil {
		log.Printf("[firecracker-runtime] Warning: failed to remove hosts entry: %v", err)
	}

	// Clean up sandbox directory
	sandboxDir := filepath.Join(r.sandboxesDir, sandboxID)
	os.RemoveAll(sandboxDir)

	log.Printf("[firecracker-runtime] Sandbox %s stopped", sandboxID)
	return nil
}

func (r *FirecrackerRuntime) PauseSandbox(ctx context.Context, sandboxID string) (string, error) {
	r.mu.RLock()
	sb, exists := r.sandboxes[sandboxID]
	r.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	if sb.machine == nil {
		return "", fmt.Errorf("sandbox machine not initialized")
	}

	snapshotID := fmt.Sprintf("%s-snapshot-%d", sandboxID, time.Now().Unix())
	snapshotDir := filepath.Join(r.sandboxesDir, sandboxID, "snapshots", snapshotID)
	os.MkdirAll(snapshotDir, 0755)

	// Pause the VM using SDK
	if err := sb.machine.PauseVM(ctx); err != nil {
		return "", fmt.Errorf("failed to pause VM: %w", err)
	}

	// Create snapshot using SDK
	if err := sb.machine.CreateSnapshot(ctx, filepath.Join(snapshotDir, "mem"), filepath.Join(snapshotDir, "vmstate")); err != nil {
		return "", fmt.Errorf("failed to create snapshot: %w", err)
	}

	r.mu.Lock()
	sb.state = "paused"
	r.mu.Unlock()

	log.Printf("[firecracker-runtime] Sandbox %s paused, snapshot: %s", sandboxID, snapshotID)
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

	if sb.machine == nil {
		return fmt.Errorf("sandbox machine not initialized")
	}

	// Resume the VM using SDK
	if err := sb.machine.ResumeVM(ctx); err != nil {
		return fmt.Errorf("failed to resume VM: %w", err)
	}

	log.Printf("[firecracker-runtime] Sandbox %s resumed", sandboxID)
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
		EnvdAddress: fmt.Sprintf("%s:%d", sb.vmIP, sb.envdPort),
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
			EnvdAddress: fmt.Sprintf("%s:%d", sb.vmIP, sb.envdPort),
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
	r.mu.RLock()
	sb, exists := r.sandboxes[sandboxID]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	outputCh := make(chan ExecOutput, 10)

	go func() {
		defer close(outputCh)

		// Connect to envd via the VM's IP
		envdAddr := fmt.Sprintf("http://%s:%d", sb.vmIP, sb.envdPort)

		// Build command request
		cmdReq := map[string]interface{}{
			"process": map[string]interface{}{
				"cmd":  command,
				"args": args,
			},
		}
		reqBody, _ := json.Marshal(cmdReq)

		// Make HTTP request to envd process.Process/Start
		req, err := http.NewRequestWithContext(ctx, "POST", envdAddr+"/process.Process/Start", bytes.NewReader(reqBody))
		if err != nil {
			outputCh <- ExecOutput{Stderr: []byte(fmt.Sprintf("failed to create request: %v", err))}
			return
		}
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			outputCh <- ExecOutput{Stderr: []byte(fmt.Sprintf("failed to execute command: %v", err))}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			outputCh <- ExecOutput{Stderr: []byte(fmt.Sprintf("envd returned %d: %s", resp.StatusCode, bytes.TrimSpace(body)))}
			return
		}

		// Stream NDJSON response and forward stdout/stderr/exit_code
		decoder := json.NewDecoder(resp.Body)

		for {
			var event struct {
				Data *struct {
					Stdout []byte `json:"stdout,omitempty"`
					Stderr []byte `json:"stderr,omitempty"`
				} `json:"data,omitempty"`
				End *struct {
					ExitCode int32   `json:"exit_code"`
					Error    *string `json:"error,omitempty"`
				} `json:"end,omitempty"`
			}

			if err := decoder.Decode(&event); err != nil {
				if err == io.EOF {
					return
				}
				outputCh <- ExecOutput{Stderr: []byte(fmt.Sprintf("failed to decode process output: %v", err))}
				return
			}

			if event.Data != nil {
				if len(event.Data.Stdout) > 0 {
					outputCh <- ExecOutput{Stdout: event.Data.Stdout}
				}
				if len(event.Data.Stderr) > 0 {
					outputCh <- ExecOutput{Stderr: event.Data.Stderr}
				}
			}

			if event.End != nil {
				if event.End.Error != nil {
					outputCh <- ExecOutput{Stderr: []byte(*event.End.Error)}
				}
				exitCode := event.End.ExitCode
				outputCh <- ExecOutput{ExitCode: &exitCode}
				return
			}
		}
	}()

	return outputCh, nil
}

// prepareRootfs prepares the rootfs for the VM, including injecting envd
func (r *FirecrackerRuntime) prepareRootfs(ctx context.Context, spec *SandboxSpec, vmRootfs string, vmIP string, gateway string) error {
	// Try to find existing rootfs in order of preference:
	// 1. Template-specific ext4 rootfs (pre-built by build service)
	// 2. Template-specific tar rootfs (needs conversion)
	// 3. Base template rootfs (fallback)
	// 4. Download/create minimal rootfs

	rootfsFound := false

	// 1. Check for template-specific ext4 rootfs
	rootfsPath := filepath.Join(r.artifactsDir, spec.TemplateID, spec.BuildID, "rootfs.ext4")
	if _, err := os.Stat(rootfsPath); err == nil {
		log.Printf("[firecracker-runtime] Using template rootfs: %s", rootfsPath)
		cpCmd := exec.CommandContext(ctx, "cp", "--reflink=auto", rootfsPath, vmRootfs)
		if out, err := cpCmd.CombinedOutput(); err != nil {
			log.Printf("[firecracker-runtime] Failed to copy template rootfs: %s: %v", string(out), err)
		} else {
			rootfsFound = true
		}
	}

	// 2. Check for template-specific tar rootfs (needs conversion)
	if !rootfsFound {
		tarPath := filepath.Join(r.artifactsDir, spec.TemplateID, spec.BuildID, "rootfs.tar")
		if _, err := os.Stat(tarPath); err == nil {
			log.Printf("[firecracker-runtime] Converting tar rootfs: %s", tarPath)
			if err := r.convertTarToExt4(ctx, tarPath, vmRootfs, int(spec.DiskSizeMB)); err != nil {
				log.Printf("[firecracker-runtime] Failed to convert tar rootfs: %v", err)
			} else {
				rootfsFound = true
			}
		}
	}

	// 3. Check for base template rootfs (fallback for all templates)
	if !rootfsFound {
		baseRootfsPaths := []string{
			filepath.Join(r.artifactsDir, "base", "default", "rootfs.ext4"),
			filepath.Join(r.artifactsDir, "base", "rootfs.ext4"),
			"/opt/e2b/base-rootfs.ext4",
		}
		for _, basePath := range baseRootfsPaths {
			if _, err := os.Stat(basePath); err == nil {
				log.Printf("[firecracker-runtime] Using base rootfs: %s", basePath)
				cpCmd := exec.CommandContext(ctx, "cp", "--reflink=auto", basePath, vmRootfs)
				if out, err := cpCmd.CombinedOutput(); err != nil {
					log.Printf("[firecracker-runtime] Failed to copy base rootfs: %s: %v", string(out), err)
				} else {
					rootfsFound = true
					break
				}
			}
		}
	}

	// 4. Download or create minimal rootfs
	if !rootfsFound {
		log.Printf("[firecracker-runtime] No pre-built rootfs found, downloading base image...")
		if err := r.downloadBaseRootfs(ctx, vmRootfs); err != nil {
			log.Printf("[firecracker-runtime] Failed to download base rootfs: %v, creating minimal...", err)
			if err := r.createMinimalRootfs(ctx, vmRootfs, int(spec.DiskSizeMB)); err != nil {
				return fmt.Errorf("failed to create minimal rootfs: %w", err)
			}
		}
	}

	// Always inject envd into the rootfs for proper communication
	if r.envdBinaryPath != "" {
		if err := r.injectEnvdIntoRootfs(ctx, vmRootfs, spec, vmIP, gateway); err != nil {
			log.Printf("[firecracker-runtime] Warning: failed to inject envd: %v", err)
		}
	}

	return nil
}

// downloadBaseRootfs downloads a pre-built base rootfs for Firecracker
func (r *FirecrackerRuntime) downloadBaseRootfs(ctx context.Context, destPath string) error {
	// Determine architecture
	arch := "x86_64"
	if out, err := exec.CommandContext(ctx, "uname", "-m").Output(); err == nil {
		archStr := string(out)
		if contains(archStr, "aarch64") || contains(archStr, "arm64") {
			arch = "aarch64"
		}
	}

	// Download official Firecracker quickstart rootfs
	var url string
	if arch == "aarch64" {
		url = "https://s3.amazonaws.com/spec.ccfc.min/img/quickstart_guide/aarch64/rootfs/bionic.rootfs.ext4"
	} else {
		url = "https://s3.amazonaws.com/spec.ccfc.min/img/quickstart_guide/x86_64/rootfs/bionic.rootfs.ext4"
	}

	log.Printf("[firecracker-runtime] Downloading base rootfs from %s", url)

	// Download to temp file first
	tempPath := destPath + ".download"
	curlCmd := exec.CommandContext(ctx, "curl", "-fsSL", "-o", tempPath, url)
	if out, err := curlCmd.CombinedOutput(); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("curl failed: %s: %w", string(out), err)
	}

	// Move to final destination
	if err := os.Rename(tempPath, destPath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to rename: %w", err)
	}

	// Also save as base rootfs for future use
	baseDir := filepath.Join(r.artifactsDir, "base", "default")
	os.MkdirAll(baseDir, 0755)
	baseRootfs := filepath.Join(baseDir, "rootfs.ext4")
	if _, err := os.Stat(baseRootfs); os.IsNotExist(err) {
		exec.CommandContext(ctx, "cp", "--reflink=auto", destPath, baseRootfs).Run()
		log.Printf("[firecracker-runtime] Saved base rootfs to %s", baseRootfs)
	}

	return nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// createMinimalRootfs creates a minimal rootfs from debootstrap or alpine
func (r *FirecrackerRuntime) createMinimalRootfs(ctx context.Context, rootfsPath string, sizeMB int) error {
	if sizeMB <= 0 {
		sizeMB = 2048
	}

	// Create sparse file
	ddCmd := exec.CommandContext(ctx, "dd", "if=/dev/zero", "of="+rootfsPath, "bs=1M", "count=0", fmt.Sprintf("seek=%d", sizeMB))
	if out, err := ddCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("dd failed: %s: %w", string(out), err)
	}

	// Format as ext4
	mkfsCmd := exec.CommandContext(ctx, "mkfs.ext4", "-F", rootfsPath)
	if out, err := mkfsCmd.CombinedOutput(); err != nil {
		os.Remove(rootfsPath)
		return fmt.Errorf("mkfs.ext4 failed: %s: %w", string(out), err)
	}

	// Mount, bootstrap, and unmount
	mountPoint := rootfsPath + ".mount"
	os.MkdirAll(mountPoint, 0755)
	defer os.RemoveAll(mountPoint)

	mountCmd := exec.CommandContext(ctx, "mount", "-o", "loop", rootfsPath, mountPoint)
	if out, err := mountCmd.CombinedOutput(); err != nil {
		os.Remove(rootfsPath)
		return fmt.Errorf("mount failed: %s: %w", string(out), err)
	}
	defer exec.CommandContext(ctx, "umount", mountPoint).Run()

	// Try debootstrap for a minimal Debian/Ubuntu system
	debCmd := exec.CommandContext(ctx, "debootstrap", "--variant=minbase", "jammy", mountPoint, "http://archive.ubuntu.com/ubuntu")
	if out, err := debCmd.CombinedOutput(); err != nil {
		log.Printf("[firecracker-runtime] debootstrap failed: %s, trying alpine", string(out))

		// Fallback: create minimal structure manually
		dirs := []string{"bin", "etc", "home/user", "lib", "lib64", "proc", "root", "run", "sbin", "sys", "tmp", "usr/bin", "usr/sbin", "var/log"}
		for _, d := range dirs {
			os.MkdirAll(filepath.Join(mountPoint, d), 0755)
		}

		// Create minimal init script
		initScript := `#!/bin/sh
mount -t proc proc /proc
mount -t sysfs sys /sys
mount -t devtmpfs dev /dev
hostname sandbox

# Configure network - CNI handles the host side, we configure inside VM
ip link set lo up
ip link set eth0 up
# DHCP or static IP from CNI metadata
dhclient eth0 2>/dev/null || true

# Start envd if available
if [ -x /usr/local/bin/envd ]; then
    /usr/local/bin/envd &
fi

# Keep running
exec /bin/sh
`
		if err := os.WriteFile(filepath.Join(mountPoint, "init"), []byte(initScript), 0755); err != nil {
			return fmt.Errorf("failed to write init: %w", err)
		}
	}

	return nil
}

// injectEnvdIntoRootfs mounts the rootfs and copies envd into it
func (r *FirecrackerRuntime) injectEnvdIntoRootfs(ctx context.Context, rootfsPath string, spec *SandboxSpec, vmIP string, gateway string) error {
	mountPoint := rootfsPath + ".mount"
	os.MkdirAll(mountPoint, 0755)
	defer os.RemoveAll(mountPoint)

	// Mount rootfs
	mountCmd := exec.CommandContext(ctx, "mount", "-o", "loop", rootfsPath, mountPoint)
	if out, err := mountCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mount failed: %s: %w", string(out), err)
	}
	defer exec.CommandContext(ctx, "umount", mountPoint).Run()

	// Copy envd binary
	envdDst := filepath.Join(mountPoint, "usr", "local", "bin", "envd")
	os.MkdirAll(filepath.Dir(envdDst), 0755)

	cpCmd := exec.CommandContext(ctx, "cp", r.envdBinaryPath, envdDst)
	if out, err := cpCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to copy envd: %s: %w", string(out), err)
	}

	// Make executable
	os.Chmod(envdDst, 0755)

	// Create systemd service for envd - runs early in boot
	// Note: IP is configured by CNI, not hardcoded
	envdService := fmt.Sprintf(`[Unit]
Description=E2B Environment Daemon
After=network-pre.target systemd-sysctl.service
Before=network.target
Wants=network-pre.target

[Service]
Type=simple
Environment=E2B_ACCESS_TOKEN=%s
Environment=E2B_ENVD_PORT=49983
ExecStartPre=/bin/sh -c 'ip link set lo up; ip link set eth0 up 2>/dev/null || true'
ExecStart=/usr/local/bin/envd
Restart=always
RestartSec=1

[Install]
WantedBy=multi-user.target
`, spec.EnvdToken)

	systemdDir := filepath.Join(mountPoint, "etc", "systemd", "system")
	os.MkdirAll(systemdDir, 0755)
	servicePath := filepath.Join(systemdDir, "envd.service")
	if err := os.WriteFile(servicePath, []byte(envdService), 0644); err != nil {
		log.Printf("[firecracker-runtime] Warning: could not write systemd service: %v", err)
	}

	// Enable the service using RELATIVE symlink paths (critical for rootfs)
	wantsDir := filepath.Join(systemdDir, "multi-user.target.wants")
	os.MkdirAll(wantsDir, 0755)
	symlinkPath := filepath.Join(wantsDir, "envd.service")
	os.Remove(symlinkPath)
	os.Symlink("../envd.service", symlinkPath) // Relative path!

	// Also enable in basic.target.wants for early startup
	basicWantsDir := filepath.Join(systemdDir, "basic.target.wants")
	os.MkdirAll(basicWantsDir, 0755)
	os.Remove(filepath.Join(basicWantsDir, "envd.service"))
	os.Symlink("../envd.service", filepath.Join(basicWantsDir, "envd.service"))

	// Create a keep-alive service that prevents the VM from shutting down
	keepAliveService := `[Unit]
Description=E2B Keep Alive Service
After=envd.service
Requires=envd.service

[Service]
Type=simple
ExecStart=/bin/sh -c 'while true; do sleep 3600; done'
Restart=always

[Install]
WantedBy=multi-user.target
`
	keepAlivePath := filepath.Join(systemdDir, "e2b-keepalive.service")
	os.WriteFile(keepAlivePath, []byte(keepAliveService), 0644)
	os.Remove(filepath.Join(wantsDir, "e2b-keepalive.service"))
	os.Symlink("../e2b-keepalive.service", filepath.Join(wantsDir, "e2b-keepalive.service"))

	// Create a network configuration service that gets IP from CNI
	networkService := `[Unit]
Description=E2B Network Configuration
Before=network.target envd.service
Wants=network-pre.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'ip link set lo up; ip link set eth0 up; dhclient -1 eth0 2>/dev/null || ip addr add 192.168.128.2/24 dev eth0; ip route add default via 192.168.128.1 2>/dev/null || true'

[Install]
WantedBy=multi-user.target
`
	networkServicePath := filepath.Join(systemdDir, "e2b-network.service")
	os.WriteFile(networkServicePath, []byte(networkService), 0644)
	os.Remove(filepath.Join(wantsDir, "e2b-network.service"))
	os.Symlink("../e2b-network.service", filepath.Join(wantsDir, "e2b-network.service"))

	// Update envd service to depend on network
	envdServiceUpdated := fmt.Sprintf(`[Unit]
Description=E2B Environment Daemon
After=network.target e2b-network.service
Requires=e2b-network.service
Wants=network.target

[Service]
Type=simple
Environment=E2B_ACCESS_TOKEN=%s
Environment=E2B_ENVD_PORT=49983
ExecStart=/usr/local/bin/envd
Restart=always
RestartSec=1

[Install]
WantedBy=multi-user.target
`, spec.EnvdToken)
	os.WriteFile(servicePath, []byte(envdServiceUpdated), 0644)

	// Also write a simple init.d script as fallback
	initScript := fmt.Sprintf(`#!/bin/sh
### BEGIN INIT INFO
# Provides:          envd
# Required-Start:    $network $local_fs
# Required-Stop:     $network $local_fs
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Description:       E2B Environment Daemon
### END INIT INFO

export E2B_ACCESS_TOKEN=%s
export E2B_ENVD_PORT=49983

case "$1" in
    start)
        ip link set eth0 up 2>/dev/null || true
        dhclient -1 eth0 2>/dev/null || ip addr add 192.168.128.2/24 dev eth0 2>/dev/null || true
        ip route add default via 192.168.128.1 2>/dev/null || true
        /usr/local/bin/envd > /var/log/envd.log 2>&1 &
        ;;
    stop)
        pkill envd || true
        ;;
    *)
        echo "Usage: $0 {start|stop}"
        exit 1
        ;;
esac
exit 0
`, spec.EnvdToken)

	initPath := filepath.Join(mountPoint, "etc", "init.d", "envd")
	os.MkdirAll(filepath.Dir(initPath), 0755)
	os.WriteFile(initPath, []byte(initScript), 0755)

	// Create symlinks for SysV init runlevels
	for _, runlevel := range []string{"2", "3", "4", "5"} {
		rcDir := filepath.Join(mountPoint, "etc", "rc"+runlevel+".d")
		os.MkdirAll(rcDir, 0755)
		os.Remove(filepath.Join(rcDir, "S99envd"))
		os.Symlink("../init.d/envd", filepath.Join(rcDir, "S99envd"))
	}

	// Add to rc.local for systems that use it
	rcLocalPath := filepath.Join(mountPoint, "etc", "rc.local")
	rcLocal := fmt.Sprintf(`#!/bin/sh -e
# E2B Sandbox Initialization - rc.local

# Configure network
ip link set lo up 2>/dev/null || true
ip link set eth0 up 2>/dev/null || true

# Get IP from DHCP or use default
dhclient -1 eth0 2>/dev/null || {
    ip addr add 192.168.128.2/24 dev eth0 2>/dev/null || true
    ip route add default via 192.168.128.1 2>/dev/null || true
}

# Start envd daemon if not already running
if ! pgrep -x envd > /dev/null 2>&1; then
    export E2B_ACCESS_TOKEN=%s
    export E2B_ENVD_PORT=49983
    /usr/local/bin/envd > /var/log/envd.log 2>&1 &
    echo "envd started on port 49983"
fi

exit 0
`, spec.EnvdToken)
	os.WriteFile(rcLocalPath, []byte(rcLocal), 0755)

	// Enable rc.local service for systemd
	rcLocalService := `[Unit]
Description=/etc/rc.local Compatibility
ConditionFileIsExecutable=/etc/rc.local
After=network.target

[Service]
Type=forking
ExecStart=/etc/rc.local start
TimeoutSec=0
RemainAfterExit=yes
GuessMainPID=no

[Install]
WantedBy=multi-user.target
`
	rcLocalServicePath := filepath.Join(systemdDir, "rc-local.service")
	os.WriteFile(rcLocalServicePath, []byte(rcLocalService), 0644)
	os.Remove(filepath.Join(wantsDir, "rc-local.service"))
	os.Symlink("../rc-local.service", filepath.Join(wantsDir, "rc-local.service"))

	log.Printf("[firecracker-runtime] Injected envd and network config into rootfs")
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
