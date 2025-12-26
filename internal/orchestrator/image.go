package orchestrator

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/0prodigy/OpenE2B/internal/firecracker"
)

// ImageManager handles template image operations
type ImageManager struct {
	storage      ArtifactStorage
	registryHost string
}

// NewImageManager creates a new image manager
func NewImageManager(storage ArtifactStorage, registryHost string) *ImageManager {
	return &ImageManager{
		storage:      storage,
		registryHost: registryHost,
	}
}

// GetImageURI returns the registry URI for a template build
func (m *ImageManager) GetImageURI(templateID, buildID string) string {
	// Format: docker.{domain}/e2b/custom-envs/{templateID}:{buildID}
	return fmt.Sprintf("%s/e2b/custom-envs/%s:%s", m.registryHost, templateID, buildID)
}

// GetRootfsPath returns the path to a cached rootfs, or empty if not locally available
func (m *ImageManager) GetRootfsPath(buildID string) string {
	key := m.getArtifactKey(buildID)
	return m.storage.GetPath(key)
}

func (m *ImageManager) getArtifactKey(buildID string) string {
	return filepath.Join("builds", buildID, "rootfs.ext4")
}

// PullAndConvert pulls an image and converts it to rootfs
func (m *ImageManager) PullAndConvert(ctx context.Context, templateID, buildID string) (*TemplateImage, error) {
	imageURI := m.GetImageURI(templateID, buildID)
	key := m.getArtifactKey(buildID)

	// Check storage first
	if exists, _ := m.storage.Exists(key); exists {
		return &TemplateImage{
			TemplateID: templateID,
			BuildID:    buildID,
			ImageURI:   imageURI,
			RootfsPath: m.storage.GetPath(key),
		}, nil
	}

	outputPath := m.storage.GetPath(key)
	if outputPath == "" {
		return nil, fmt.Errorf("storage does not support direct file access")
	}

	// Trigger conversion using firecracker package
	// Ensure the image exists locally (pull it)
	pullCmd := exec.CommandContext(ctx, "docker", "pull", imageURI)
	if out, err := pullCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("failed to pull image %s: %s: %w", imageURI, string(out), err)
	}

	if err := firecracker.ConvertOCIToRootfs(ctx, imageURI, outputPath, 1024); err != nil {
		return nil, fmt.Errorf("failed to convert image: %w", err)
	}

	return &TemplateImage{
		TemplateID: templateID,
		BuildID:    buildID,
		ImageURI:   imageURI,
		RootfsPath: outputPath,
	}, nil
}

// CommitSandbox commits a running sandbox to a new image
func (m *ImageManager) CommitSandbox(ctx context.Context, sandboxID, templateID, buildID string) error {
	// 1. Commit sandbox container to image
	// docker commit {sandboxID} {registry}/{template}:{build}
	imageURI := m.GetImageURI(templateID, buildID)

	cmd := exec.CommandContext(ctx, "docker", "commit", sandboxID, imageURI)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to commit sandbox: %s: %w", string(out), err)
	}

	// 2. Push to registry (optional for local dev, but good practice)
	// pushCmd := exec.CommandContext(ctx, "docker", "push", imageURI)
	// if out, err := pushCmd.CombinedOutput(); err != nil { ... }

	return nil
}

// CleanupOldImages removes old cached images
func (m *ImageManager) CleanupOldImages(keepBuilds []string) error {
	// TODO: implement storage-aware cleanup
	// key := m.getArtifactKey(entry.Name())
	// m.storage.Delete(key)
	return nil
}
