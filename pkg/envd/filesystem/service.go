package filesystem

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// FileType enumeration
type FileType int32

const (
	FileTypeUnspecified FileType = 0
	FileTypeFile        FileType = 1
	FileTypeDirectory   FileType = 2
)

// EventType enumeration
type EventType int32

const (
	EventTypeUnspecified EventType = 0
	EventTypeCreate      EventType = 1
	EventTypeWrite       EventType = 2
	EventTypeRemove      EventType = 3
	EventTypeRename      EventType = 4
	EventTypeChmod       EventType = 5
)

// EntryInfo represents file/directory metadata
type EntryInfo struct {
	Name          string
	Type          FileType
	Path          string
	Size          int64
	Mode          uint32
	Permissions   string
	Owner         string
	Group         string
	ModifiedTime  time.Time
	SymlinkTarget *string
}

// FilesystemEvent represents a filesystem change event
type FilesystemEvent struct {
	Name string
	Type EventType
}

// Watcher tracks filesystem changes
type Watcher struct {
	ID        string
	Path      string
	Recursive bool
	Events    []FilesystemEvent
	mu        sync.Mutex
}

// Service implements the filesystem operations
type Service struct {
	watchers map[string]*Watcher
	mu       sync.RWMutex
}

// NewService creates a new filesystem service
func NewService() *Service {
	return &Service{
		watchers: make(map[string]*Watcher),
	}
}

// Stat returns file/directory information
func (s *Service) Stat(ctx context.Context, path string) (*EntryInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	return toEntryInfo(path, info), nil
}

// MakeDir creates a directory
func (s *Service) MakeDir(ctx context.Context, path string) (*EntryInfo, error) {
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return toEntryInfo(path, info), nil
}

// Move moves/renames a file or directory
func (s *Service) Move(ctx context.Context, source, destination string) (*EntryInfo, error) {
	if err := os.Rename(source, destination); err != nil {
		return nil, err
	}
	info, err := os.Stat(destination)
	if err != nil {
		return nil, err
	}
	return toEntryInfo(destination, info), nil
}

// ListDir lists directory contents
func (s *Service) ListDir(ctx context.Context, path string, depth uint32) ([]EntryInfo, error) {
	var entries []EntryInfo

	err := walkDir(path, depth, 0, func(p string, info fs.FileInfo) error {
		entries = append(entries, *toEntryInfo(p, info))
		return nil
	})

	return entries, err
}

// Remove deletes a file or directory
func (s *Service) Remove(ctx context.Context, path string) error {
	return os.RemoveAll(path)
}

// CreateWatcher creates a new filesystem watcher
func (s *Service) CreateWatcher(ctx context.Context, path string, recursive bool) (string, error) {
	watcherID := uuid.New().String()

	watcher := &Watcher{
		ID:        watcherID,
		Path:      path,
		Recursive: recursive,
		Events:    []FilesystemEvent{},
	}

	s.mu.Lock()
	s.watchers[watcherID] = watcher
	s.mu.Unlock()

	// In a real implementation, we would use fsnotify here
	// For now, this is a placeholder that tracks watchers

	return watcherID, nil
}

// GetWatcherEvents returns pending events for a watcher
func (s *Service) GetWatcherEvents(ctx context.Context, watcherID string) ([]FilesystemEvent, error) {
	s.mu.RLock()
	watcher, ok := s.watchers[watcherID]
	s.mu.RUnlock()

	if !ok {
		return nil, os.ErrNotExist
	}

	watcher.mu.Lock()
	events := watcher.Events
	watcher.Events = []FilesystemEvent{}
	watcher.mu.Unlock()

	return events, nil
}

// RemoveWatcher removes a filesystem watcher
func (s *Service) RemoveWatcher(ctx context.Context, watcherID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.watchers[watcherID]; !ok {
		return os.ErrNotExist
	}

	delete(s.watchers, watcherID)
	return nil
}

// Helper functions

func toEntryInfo(path string, info fs.FileInfo) *EntryInfo {
	entry := &EntryInfo{
		Name:         info.Name(),
		Path:         path,
		Size:         info.Size(),
		Mode:         uint32(info.Mode()),
		Permissions:  info.Mode().String(),
		ModifiedTime: info.ModTime(),
	}

	if info.IsDir() {
		entry.Type = FileTypeDirectory
	} else {
		entry.Type = FileTypeFile
	}

	// Try to get owner/group from syscall
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		entry.Owner = uidToName(stat.Uid)
		entry.Group = gidToName(stat.Gid)
	}

	// Check if symlink
	if info.Mode()&os.ModeSymlink != 0 {
		if target, err := os.Readlink(path); err == nil {
			entry.SymlinkTarget = &target
		}
	}

	return entry
}

func walkDir(root string, maxDepth, currentDepth uint32, fn func(string, fs.FileInfo) error) error {
	if maxDepth > 0 && currentDepth >= maxDepth {
		return nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		if err := fn(path, info); err != nil {
			return err
		}

		if entry.IsDir() && (maxDepth == 0 || currentDepth < maxDepth-1) {
			if err := walkDir(path, maxDepth, currentDepth+1, fn); err != nil {
				return err
			}
		}
	}

	return nil
}

func uidToName(uid uint32) string {
	// Simplified - would normally look up in /etc/passwd
	if uid == 0 {
		return "root"
	}
	return "user"
}

func gidToName(gid uint32) string {
	// Simplified - would normally look up in /etc/group
	if gid == 0 {
		return "root"
	}
	return "user"
}
