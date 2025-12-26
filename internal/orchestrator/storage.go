package orchestrator

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// ArtifactStorage defines how template artifacts (rootfs, etc.) are stored
type ArtifactStorage interface {
	// Get returns a reader for the artifact
	Get(key string) (io.ReadCloser, error)
	// Put writes data from the reader to the artifact key
	Put(key string, r io.Reader) error
	// Delete removes the artifact
	Delete(key string) error
	// Exists checks if the artifact exists
	Exists(key string) (bool, error)
	// GetPath returns the local filesystem path if available (for performance/direct access)
	// Returns empty string if not locally available (e.g. S3 without cache)
	GetPath(key string) string
}

// LocalStorage implements ArtifactStorage on the local filesystem
type LocalStorage struct {
	baseDir string
	mu      sync.RWMutex
}

// NewLocalStorage creates a new local storage backend
func NewLocalStorage(baseDir string) (*LocalStorage, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage dir: %w", err)
	}
	return &LocalStorage{
		baseDir: baseDir,
	}, nil
}

func (s *LocalStorage) getPath(key string) string {
	return filepath.Join(s.baseDir, key)
}

func (s *LocalStorage) Get(key string) (io.ReadCloser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	f, err := os.Open(s.getPath(key))
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (s *LocalStorage) Put(key string, r io.Reader) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.getPath(key)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create artifact dir: %w", err)
	}

	// Write to temp file first
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(tmp)
		}
	}()

	if _, err = io.Copy(f, r); err != nil {
		return err
	}

	// Atomically rename
	if err = os.Rename(tmp, path); err != nil {
		return err
	}

	return nil
}

func (s *LocalStorage) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.Remove(s.getPath(key))
}

func (s *LocalStorage) Exists(key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, err := os.Stat(s.getPath(key))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (s *LocalStorage) GetPath(key string) string {
	return s.getPath(key)
}
