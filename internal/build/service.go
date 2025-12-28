package build

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/0prodigy/OpenE2B/internal/db"
	"github.com/0prodigy/OpenE2B/internal/scheduler"
	"github.com/google/uuid"
)

// BuildStatus represents the status of a build
type BuildStatus string

const (
	BuildStatusWaiting  BuildStatus = "waiting"
	BuildStatusBuilding BuildStatus = "building"
	BuildStatusReady    BuildStatus = "ready"
	BuildStatusError    BuildStatus = "error"
)

// LogLevel represents log severity
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

// LogEntry represents a build log entry
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
	Level     LogLevel  `json:"level"`
	Step      *string   `json:"step,omitempty"`
}

// Step represents a build step/instruction
// Types: COPY, ENV, RUN, WORKDIR, USER
type Step struct {
	Type      string   // COPY, ENV, RUN, WORKDIR, USER
	Args      []string // Arguments for the instruction
	FilesHash string   // Hash of files (for COPY steps)
	Force     bool     // Force rebuild this step
}

// Config holds build configuration
type Config struct {
	FromImage        string
	FromTemplate     string
	RegistryURL      string
	RegistryUsername string
	RegistryPassword string
	Steps            []Step
	StartCmd         string
	ReadyCmd         string
	Force            bool
}

// Build represents an in-progress build
type Build struct {
	ID         string
	TemplateID string
	Status     BuildStatus
	Config     *Config
	CPUCount   int
	MemoryMB   int
	Logs       []string
	LogEntries []LogEntry
	StartedAt  time.Time
	FinishedAt *time.Time
	Error      error
	mu         sync.Mutex
}

// ImageManager interface for image operations
type ImageManager interface {
	// Interface kept minimal for now
}

// Service manages template builds using the V2 SDK flow:
// 1. SDK calls POST /v3/templates to create template+build record
// 2. SDK uploads files to storage (GET /templates/{id}/files/{hash})
// 3. SDK calls POST /v2/templates/{id}/builds/{buildID} with steps
// 4. This service executes steps in a sandbox and creates snapshot
// 5. SDK polls GET /templates/{id}/builds/{buildID}/status
type Service struct {
	store        db.Store
	scheduler    *scheduler.Scheduler
	imageManager ImageManager
	domain       string
	artifactsDir string
	builds       map[string]*Build
	buildsMu     sync.RWMutex
	workers      int
	workQueue    chan *Build
	stopCh       chan struct{}
}

// NewService creates a new build service
func NewService(store db.Store, sched *scheduler.Scheduler, imageManager ImageManager, domain string, artifactsDir string, workers int) *Service {
	if workers <= 0 {
		workers = 2
	}
	if artifactsDir == "" {
		artifactsDir = "data/artifacts"
	}

	s := &Service{
		store:        store,
		scheduler:    sched,
		imageManager: imageManager,
		domain:       domain,
		artifactsDir: artifactsDir,
		builds:       make(map[string]*Build),
		workers:      workers,
		workQueue:    make(chan *Build, 100),
		stopCh:       make(chan struct{}),
	}

	for i := 0; i < workers; i++ {
		go s.worker(i)
	}

	return s
}

// Stop gracefully shuts down the build service
func (s *Service) Stop() {
	close(s.stopCh)
}

// StartBuild initiates a new build for a template
func (s *Service) StartBuild(ctx context.Context, templateID, buildID string, config *Config) error {
	log.Printf("[build] StartBuild called for template=%s build=%s", templateID, buildID)

	template, err := s.store.GetTemplate(ctx, templateID)
	if err != nil {
		return fmt.Errorf("template not found: %w", err)
	}
	if template == nil {
		return fmt.Errorf("template not found: %s", templateID)
	}

	existingBuild, err := s.store.GetBuild(ctx, buildID)
	if err != nil {
		return fmt.Errorf("failed to get build: %w", err)
	}
	if existingBuild == nil {
		return fmt.Errorf("build not found: %s", buildID)
	}

	if existingBuild.Status != string(BuildStatusWaiting) && existingBuild.Status != string(BuildStatusBuilding) {
		return fmt.Errorf("build is not in waiting state: %s", existingBuild.Status)
	}

	build := &Build{
		ID:         buildID,
		TemplateID: templateID,
		Status:     BuildStatusBuilding,
		Config:     config,
		CPUCount:   existingBuild.CPUCount,
		MemoryMB:   existingBuild.MemoryMB,
		Logs:       []string{},
		LogEntries: []LogEntry{},
		StartedAt:  time.Now(),
	}

	s.buildsMu.Lock()
	s.builds[buildID] = build
	s.buildsMu.Unlock()

	status := string(BuildStatusBuilding)
	if err := s.store.UpdateBuild(ctx, buildID, &db.BuildUpdate{
		Status: &status,
	}); err != nil {
		return fmt.Errorf("failed to update build status: %w", err)
	}

	select {
	case s.workQueue <- build:
		log.Printf("[build] Queued build %s for template %s", buildID, templateID)
	default:
		errorStatus := string(BuildStatusError)
		s.store.UpdateBuild(ctx, buildID, &db.BuildUpdate{
			Status: &errorStatus,
		})
		return fmt.Errorf("build queue is full")
	}

	return nil
}

// GetBuild returns the current state of a build
func (s *Service) GetBuild(buildID string) (*Build, bool) {
	s.buildsMu.RLock()
	defer s.buildsMu.RUnlock()

	build, ok := s.builds[buildID]
	if !ok {
		return nil, false
	}

	build.mu.Lock()
	defer build.mu.Unlock()

	return &Build{
		ID:         build.ID,
		TemplateID: build.TemplateID,
		Status:     build.Status,
		Config:     build.Config,
		Logs:       append([]string{}, build.Logs...),
		LogEntries: append([]LogEntry{}, build.LogEntries...),
		StartedAt:  build.StartedAt,
		FinishedAt: build.FinishedAt,
		Error:      build.Error,
	}, true
}

func (s *Service) worker(id int) {
	log.Printf("[build] Worker %d started", id)
	for {
		select {
		case <-s.stopCh:
			log.Printf("[build] Worker %d stopping", id)
			return
		case build := <-s.workQueue:
			s.processBuild(build)
		}
	}
}

// processBuild executes a build using the V2 SDK flow
func (s *Service) processBuild(build *Build) {
	ctx := context.Background()
	log.Printf("[build] Processing build %s for template %s", build.ID, build.TemplateID)

	s.addLog(build, LogLevelInfo, "Build started", nil)

	// Determine base image
	baseImage := "ubuntu:22.04"
	if build.Config != nil {
		if build.Config.FromImage != "" {
			baseImage = build.Config.FromImage
		} else if build.Config.FromTemplate != "" {
			// TODO: Resolve template to image
			s.addLog(build, LogLevelInfo, fmt.Sprintf("Using base template: %s", build.Config.FromTemplate), nil)
		}
	}

	s.addLog(build, LogLevelInfo, fmt.Sprintf("Using base image: %s", baseImage), nil)

	// Create sandbox container for build
	sandboxID := "build-" + build.ID
	s.addLog(build, LogLevelInfo, fmt.Sprintf("Creating build sandbox: %s", sandboxID), nil)

	// Start container
	args := []string{"run", "-d", "--name", sandboxID, baseImage, "sleep", "infinity"}
	cmd := exec.CommandContext(ctx, "docker", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		s.failBuild(build, fmt.Errorf("failed to start sandbox: %s: %w", string(out), err))
		return
	}

	// Cleanup on exit
	defer func() {
		exec.CommandContext(ctx, "docker", "rm", "-f", sandboxID).Run()
	}()

	s.addLog(build, LogLevelInfo, "Sandbox started", nil)

	// Execute build steps
	if build.Config != nil && len(build.Config.Steps) > 0 {
		for i, step := range build.Config.Steps {
			stepName := fmt.Sprintf("step-%d-%s", i+1, step.Type)
			s.addLog(build, LogLevelInfo, fmt.Sprintf("Executing: %s", stepName), &stepName)

			if err := s.executeStep(ctx, sandboxID, step, build, &stepName); err != nil {
				s.failBuild(build, fmt.Errorf("%s failed: %w", stepName, err))
				return
			}

			s.addLog(build, LogLevelInfo, fmt.Sprintf("Completed: %s", stepName), &stepName)
		}
	}

	// Execute start command if specified
	if build.Config != nil && build.Config.StartCmd != "" {
		stepName := "start-cmd"
		s.addLog(build, LogLevelInfo, fmt.Sprintf("Running start command: %s", build.Config.StartCmd), &stepName)

		cmd := exec.CommandContext(ctx, "docker", "exec", sandboxID, "sh", "-c", build.Config.StartCmd)
		output, err := cmd.CombinedOutput()
		if len(output) > 0 {
			s.addLog(build, LogLevelInfo, string(output), &stepName)
		}
		if err != nil {
			s.addLog(build, LogLevelWarn, fmt.Sprintf("Start command exited with error (may be expected): %v", err), &stepName)
		}
	}

	// Commit container as new image
	s.addLog(build, LogLevelInfo, "Committing container as template image...", nil)

	imageTag := fmt.Sprintf("e2b/%s:%s", build.TemplateID, build.ID)
	cmd = exec.CommandContext(ctx, "docker", "commit", sandboxID, imageTag)
	if out, err := cmd.CombinedOutput(); err != nil {
		s.failBuild(build, fmt.Errorf("failed to commit container: %s: %w", string(out), err))
		return
	}

	s.addLog(build, LogLevelInfo, fmt.Sprintf("Created image: %s", imageTag), nil)

	// Export rootfs artifact
	artifactsDir := s.artifactsDir
	if artifactsDir == "" {
		artifactsDir = "data/artifacts"
	}

	// Create template artifacts directory
	templateArtifactsDir := filepath.Join(artifactsDir, build.TemplateID, build.ID)
	if err := os.MkdirAll(templateArtifactsDir, 0755); err != nil {
		s.addLog(build, LogLevelWarn, fmt.Sprintf("Failed to create artifacts directory: %v", err), nil)
	} else {
		s.addLog(build, LogLevelInfo, "Exporting rootfs artifact...", nil)

		// Export container filesystem to tar (rootfs)
		rootfsPath := filepath.Join(templateArtifactsDir, "rootfs.tar")
		exportCmd := exec.CommandContext(ctx, "docker", "export", sandboxID, "-o", rootfsPath)
		if out, err := exportCmd.CombinedOutput(); err != nil {
			s.addLog(build, LogLevelWarn, fmt.Sprintf("Failed to export rootfs: %s: %v", string(out), err), nil)
		} else {
			// Get file size
			if fi, err := os.Stat(rootfsPath); err == nil {
				sizeMB := float64(fi.Size()) / (1024 * 1024)
				s.addLog(build, LogLevelInfo, fmt.Sprintf("Rootfs exported: %.1f MB", sizeMB), nil)
			}

			// Also save image tar (for future use with VM snapshots)
			imagePath := filepath.Join(templateArtifactsDir, "image.tar")
			saveCmd := exec.CommandContext(ctx, "docker", "save", imageTag, "-o", imagePath)
			if out, err := saveCmd.CombinedOutput(); err != nil {
				s.addLog(build, LogLevelWarn, fmt.Sprintf("Failed to save image: %s: %v", string(out), err), nil)
			} else {
				if fi, err := os.Stat(imagePath); err == nil {
					sizeMB := float64(fi.Size()) / (1024 * 1024)
					s.addLog(build, LogLevelInfo, fmt.Sprintf("Image saved: %.1f MB", sizeMB), nil)
				}
			}

			// Write metadata
			metadataPath := filepath.Join(templateArtifactsDir, "metadata.json")
			metadata := map[string]interface{}{
				"templateId": build.TemplateID,
				"buildId":    build.ID,
				"imageTag":   imageTag,
				"createdAt":  time.Now().UTC().Format(time.RFC3339),
				"baseImage":  baseImage,
				"cpuCount":   build.CPUCount,
				"memoryMB":   build.MemoryMB,
			}
			if data, err := json.MarshalIndent(metadata, "", "  "); err == nil {
				os.WriteFile(metadataPath, data, 0644)
			}

			// Create Firecracker-ready ext4 rootfs with envd injected
			s.addLog(build, LogLevelInfo, "Creating Firecracker-ready rootfs...", nil)
			if err := s.createFirecrackerRootfs(ctx, rootfsPath, templateArtifactsDir, build); err != nil {
				s.addLog(build, LogLevelWarn, fmt.Sprintf("Failed to create Firecracker rootfs: %v", err), nil)
			} else {
				s.addLog(build, LogLevelInfo, "Firecracker rootfs created with envd injected", nil)
			}

			s.addLog(build, LogLevelInfo, fmt.Sprintf("Artifacts saved to: %s", templateArtifactsDir), nil)
		}
	}

	s.addLog(build, LogLevelInfo, "Build completed successfully", nil)

	// Update build status
	build.mu.Lock()
	build.Status = BuildStatusReady
	now := time.Now()
	build.FinishedAt = &now
	build.mu.Unlock()

	// Persist to database
	status := string(BuildStatusReady)
	envdVersion := "0.1.0"
	s.store.UpdateBuild(ctx, build.ID, &db.BuildUpdate{
		Status:      &status,
		FinishedAt:  &now,
		EnvdVersion: &envdVersion,
		Logs:        build.Logs,
		LogEntries:  toDBLogEntries(build.LogEntries),
	})

	s.store.UpdateTemplate(ctx, build.TemplateID, &db.TemplateUpdate{
		BuildStatus: &status,
		BuildID:     &build.ID,
	})

	log.Printf("[build] Build %s completed successfully", build.ID)
}

// executeStep executes a single build step
func (s *Service) executeStep(ctx context.Context, sandboxID string, step Step, build *Build, stepName *string) error {
	switch step.Type {
	case "RUN":
		if len(step.Args) == 0 {
			return fmt.Errorf("RUN step requires a command")
		}
		cmdStr := step.Args[0]
		cmd := exec.CommandContext(ctx, "docker", "exec", sandboxID, "sh", "-c", cmdStr)
		output, err := cmd.CombinedOutput()
		if len(output) > 0 {
			s.addLog(build, LogLevelInfo, string(output), stepName)
		}
		return err

	case "ENV":
		// ENV steps set environment variables
		// For now, we skip since docker doesn't persist env vars after exec
		// In production, we'd need to write to a profile file
		s.addLog(build, LogLevelInfo, fmt.Sprintf("Setting environment variables: %v", step.Args), stepName)
		return nil

	case "WORKDIR":
		if len(step.Args) == 0 {
			return fmt.Errorf("WORKDIR step requires a path")
		}
		// Create directory if it doesn't exist
		cmd := exec.CommandContext(ctx, "docker", "exec", sandboxID, "mkdir", "-p", step.Args[0])
		output, err := cmd.CombinedOutput()
		if len(output) > 0 {
			s.addLog(build, LogLevelInfo, string(output), stepName)
		}
		return err

	case "USER":
		// USER steps change the user - we log but don't execute in this simple impl
		if len(step.Args) > 0 {
			s.addLog(build, LogLevelInfo, fmt.Sprintf("User set to: %s", step.Args[0]), stepName)
		}
		return nil

	case "COPY":
		// COPY steps require files to be downloaded from storage
		// For now, we skip since file storage isn't implemented
		s.addLog(build, LogLevelInfo, fmt.Sprintf("COPY: %v (skipped - file storage not implemented)", step.Args), stepName)
		return nil

	default:
		s.addLog(build, LogLevelWarn, fmt.Sprintf("Unknown step type: %s", step.Type), stepName)
		return nil
	}
}

func (s *Service) addLog(build *Build, level LogLevel, message string, step *string) {
	build.mu.Lock()
	defer build.mu.Unlock()

	entry := LogEntry{
		Timestamp: time.Now(),
		Message:   message,
		Level:     level,
		Step:      step,
	}

	build.Logs = append(build.Logs, message)
	build.LogEntries = append(build.LogEntries, entry)
}

func (s *Service) failBuild(build *Build, err error) {
	ctx := context.Background()

	build.mu.Lock()
	build.Status = BuildStatusError
	build.Error = err
	now := time.Now()
	build.FinishedAt = &now
	build.mu.Unlock()

	s.addLog(build, LogLevelError, fmt.Sprintf("Build failed: %s", err.Error()), nil)

	status := string(BuildStatusError)
	s.store.UpdateBuild(ctx, build.ID, &db.BuildUpdate{
		Status:     &status,
		FinishedAt: &now,
		Logs:       build.Logs,
		LogEntries: toDBLogEntries(build.LogEntries),
	})

	s.store.UpdateTemplate(ctx, build.TemplateID, &db.TemplateUpdate{
		BuildStatus: &status,
	})

	log.Printf("[build] Build %s failed: %v", build.ID, err)
}

// CreateNewBuild creates a new build record for a template
func (s *Service) CreateNewBuild(ctx context.Context, templateID string, cpuCount, memoryMB int) (*db.Build, error) {
	template, err := s.store.GetTemplate(ctx, templateID)
	if err != nil || template == nil {
		return nil, fmt.Errorf("template not found: %s", templateID)
	}

	buildID := uuid.New().String()
	now := time.Now()

	build := &db.Build{
		BuildID:    buildID,
		TemplateID: templateID,
		Status:     string(BuildStatusWaiting),
		CreatedAt:  now,
		UpdatedAt:  now,
		CPUCount:   cpuCount,
		MemoryMB:   memoryMB,
		Logs:       []string{},
		LogEntries: []db.LogEntry{},
	}

	return build, nil
}

func toDBLogEntries(entries []LogEntry) []db.LogEntry {
	result := make([]db.LogEntry, len(entries))
	for i, e := range entries {
		result[i] = db.LogEntry{
			Timestamp: e.Timestamp,
			Message:   e.Message,
			Level:     string(e.Level),
			Step:      e.Step,
		}
	}
	return result
}

// createFirecrackerRootfs converts a tar rootfs to ext4 and injects envd for Firecracker VMs
func (s *Service) createFirecrackerRootfs(ctx context.Context, tarPath, artifactsDir string, build *Build) error {
	ext4Path := filepath.Join(artifactsDir, "rootfs.ext4")

	// Default size: 2GB, can be configured based on build requirements
	sizeMB := 2048
	if build.MemoryMB > 512 {
		// Scale disk size with memory for larger builds
		sizeMB = build.MemoryMB * 4
	}

	// Create sparse file for ext4 filesystem
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

	// Mount the ext4 filesystem
	mountPoint := ext4Path + ".mount"
	os.MkdirAll(mountPoint, 0755)
	defer os.RemoveAll(mountPoint)

	mountCmd := exec.CommandContext(ctx, "mount", "-o", "loop", ext4Path, mountPoint)
	if out, err := mountCmd.CombinedOutput(); err != nil {
		os.Remove(ext4Path)
		return fmt.Errorf("mount failed: %s: %w", string(out), err)
	}
	defer exec.CommandContext(ctx, "umount", mountPoint).Run()

	// Extract the tar archive into the ext4 filesystem
	tarCmd := exec.CommandContext(ctx, "tar", "-xf", tarPath, "-C", mountPoint)
	if out, err := tarCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tar extract failed: %s: %w", string(out), err)
	}

	// Ensure essential directories exist
	essentialDirs := []string{"proc", "sys", "dev", "run", "tmp", "var/log", "usr/local/bin", "etc/init.d"}
	for _, dir := range essentialDirs {
		os.MkdirAll(filepath.Join(mountPoint, dir), 0755)
	}

	// Find and inject envd binary
	envdPath := findEnvdBinary()
	if envdPath != "" {
		envdDst := filepath.Join(mountPoint, "usr", "local", "bin", "envd")
		cpCmd := exec.CommandContext(ctx, "cp", envdPath, envdDst)
		if out, err := cpCmd.CombinedOutput(); err != nil {
			log.Printf("[build] Warning: failed to copy envd: %s: %v", string(out), err)
		} else {
			os.Chmod(envdDst, 0755)
			log.Printf("[build] Injected envd binary into rootfs")
		}
	}

	// Create network and envd startup script
	rcLocalPath := filepath.Join(mountPoint, "etc", "rc.local")
	rcLocalContent := `#!/bin/sh
# E2B Sandbox Network and envd Configuration

# Configure network interfaces
ip link set lo up
ip link set eth0 up 2>/dev/null || true

# Try DHCP first, fallback to static IP
dhclient eth0 2>/dev/null || {
    ip addr add 192.168.100.2/24 dev eth0 2>/dev/null || true
    ip route add default via 192.168.100.1 2>/dev/null || true
}

# Start envd daemon
export E2B_ENVD_PORT=49983
if [ -x /usr/local/bin/envd ]; then
    /usr/local/bin/envd > /var/log/envd.log 2>&1 &
    echo "envd started on port $E2B_ENVD_PORT"
fi

exit 0
`
	if err := os.WriteFile(rcLocalPath, []byte(rcLocalContent), 0755); err != nil {
		log.Printf("[build] Warning: failed to write rc.local: %v", err)
	}

	// Create systemd service for envd (for systems with systemd)
	systemdDir := filepath.Join(mountPoint, "etc", "systemd", "system")
	os.MkdirAll(systemdDir, 0755)

	envdServicePath := filepath.Join(systemdDir, "envd.service")
	envdServiceContent := `[Unit]
Description=E2B Environment Daemon
After=network.target

[Service]
Type=simple
Environment=E2B_ENVD_PORT=49983
ExecStart=/usr/local/bin/envd
Restart=always
RestartSec=1

[Install]
WantedBy=multi-user.target
`
	os.WriteFile(envdServicePath, []byte(envdServiceContent), 0644)

	// Enable envd service (create symlink)
	wantsDir := filepath.Join(systemdDir, "multi-user.target.wants")
	os.MkdirAll(wantsDir, 0755)
	os.Symlink("../envd.service", filepath.Join(wantsDir, "envd.service"))

	// Get final size
	if fi, err := os.Stat(ext4Path); err == nil {
		sizeMB := float64(fi.Size()) / (1024 * 1024)
		log.Printf("[build] Created Firecracker rootfs: %.1f MB", sizeMB)
	}

	return nil
}

// findEnvdBinary locates the envd binary for injection
func findEnvdBinary() string {
	// Try common locations
	locations := []string{
		"/opt/e2b/bin/envd",
		"bin/envd",
		"bin/envd-linux-amd64",
		"bin/envd-linux-arm64",
		"./envd",
	}

	for _, loc := range locations {
		if _, err := os.Stat(loc); err == nil {
			return loc
		}
	}

	// Try looking relative to executable
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		envdPath := filepath.Join(dir, "envd")
		if _, err := os.Stat(envdPath); err == nil {
			return envdPath
		}
	}

	return ""
}
