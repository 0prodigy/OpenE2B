package rpc

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"connectrpc.com/connect"
	pb "github.com/0prodigy/OpenE2B/pkg/proto/filesystem"
	"github.com/0prodigy/OpenE2B/pkg/proto/filesystem/filesystemconnect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// FilesystemService implements the Connect-RPC FilesystemHandler interface
type FilesystemService struct {
	filesystemconnect.UnimplementedFilesystemHandler
	rootPath string
}

// NewFilesystemService creates a new filesystem service
func NewFilesystemService(rootPath string) *FilesystemService {
	if rootPath == "" {
		rootPath = "/"
	}
	return &FilesystemService{rootPath: rootPath}
}

// Stat returns file/directory information
func (s *FilesystemService) Stat(ctx context.Context, req *connect.Request[pb.StatRequest]) (*connect.Response[pb.StatResponse], error) {
	path := s.resolvePath(req.Msg.Path)
	log.Printf("[fs] Stat: %s", path)

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("file not found: %s", req.Msg.Path))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	entry := s.toEntryInfo(path, info)
	return connect.NewResponse(&pb.StatResponse{Entry: entry}), nil
}

// MakeDir creates a directory
func (s *FilesystemService) MakeDir(ctx context.Context, req *connect.Request[pb.MakeDirRequest]) (*connect.Response[pb.MakeDirResponse], error) {
	path := s.resolvePath(req.Msg.Path)
	log.Printf("[fs] MakeDir: %s", path)

	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	entry := s.toEntryInfo(path, info)
	return connect.NewResponse(&pb.MakeDirResponse{Entry: entry}), nil
}

// Move renames/moves a file or directory
func (s *FilesystemService) Move(ctx context.Context, req *connect.Request[pb.MoveRequest]) (*connect.Response[pb.MoveResponse], error) {
	source := s.resolvePath(req.Msg.Source)
	dest := s.resolvePath(req.Msg.Destination)
	log.Printf("[fs] Move: %s -> %s", source, dest)

	if err := os.Rename(source, dest); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	info, err := os.Stat(dest)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	entry := s.toEntryInfo(dest, info)
	return connect.NewResponse(&pb.MoveResponse{Entry: entry}), nil
}

// ListDir lists directory contents
func (s *FilesystemService) ListDir(ctx context.Context, req *connect.Request[pb.ListDirRequest]) (*connect.Response[pb.ListDirResponse], error) {
	path := s.resolvePath(req.Msg.Path)
	log.Printf("[fs] ListDir: %s (depth=%d)", path, req.Msg.Depth)

	entries, err := s.listDirRecursive(path, int(req.Msg.Depth))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("directory not found: %s", req.Msg.Path))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&pb.ListDirResponse{Entries: entries}), nil
}

func (s *FilesystemService) listDirRecursive(path string, depth int) ([]*pb.EntryInfo, error) {
	var entries []*pb.EntryInfo

	dirEntries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	for _, de := range dirEntries {
		info, err := de.Info()
		if err != nil {
			continue
		}

		fullPath := filepath.Join(path, de.Name())
		entry := s.toEntryInfo(fullPath, info)
		entries = append(entries, entry)

		// Recurse if directory and depth > 0
		if de.IsDir() && depth > 0 {
			subEntries, err := s.listDirRecursive(fullPath, depth-1)
			if err == nil {
				entries = append(entries, subEntries...)
			}
		}
	}

	return entries, nil
}

// Remove removes a file or directory
func (s *FilesystemService) Remove(ctx context.Context, req *connect.Request[pb.RemoveRequest]) (*connect.Response[pb.RemoveResponse], error) {
	path := s.resolvePath(req.Msg.Path)
	log.Printf("[fs] Remove: %s", path)

	if err := os.RemoveAll(path); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&pb.RemoveResponse{}), nil
}

// WatchDir watches a directory for changes (streaming)
func (s *FilesystemService) WatchDir(ctx context.Context, req *connect.Request[pb.WatchDirRequest], stream *connect.ServerStream[pb.WatchDirResponse]) error {
	// Simplified implementation - just send start event
	// Full implementation would use fsnotify
	log.Printf("[fs] WatchDir: %s (not fully implemented)", req.Msg.Path)

	// Send initial start event
	stream.Send(&pb.WatchDirResponse{
		Event: &pb.WatchDirResponse_Start{
			Start: &pb.WatchDirResponse_StartEvent{},
		},
	})

	// Wait for context cancellation
	<-ctx.Done()
	return nil
}

// CreateWatcher creates a new watcher (non-streaming)
func (s *FilesystemService) CreateWatcher(ctx context.Context, req *connect.Request[pb.CreateWatcherRequest]) (*connect.Response[pb.CreateWatcherResponse], error) {
	log.Printf("[fs] CreateWatcher: %s (not fully implemented)", req.Msg.Path)
	return connect.NewResponse(&pb.CreateWatcherResponse{
		WatcherId: "watcher-1",
	}), nil
}

// GetWatcherEvents gets events from a watcher (non-streaming)
func (s *FilesystemService) GetWatcherEvents(ctx context.Context, req *connect.Request[pb.GetWatcherEventsRequest]) (*connect.Response[pb.GetWatcherEventsResponse], error) {
	return connect.NewResponse(&pb.GetWatcherEventsResponse{
		Events: []*pb.FilesystemEvent{},
	}), nil
}

// RemoveWatcher removes a watcher
func (s *FilesystemService) RemoveWatcher(ctx context.Context, req *connect.Request[pb.RemoveWatcherRequest]) (*connect.Response[pb.RemoveWatcherResponse], error) {
	return connect.NewResponse(&pb.RemoveWatcherResponse{}), nil
}

func (s *FilesystemService) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(s.rootPath, path)
}

func (s *FilesystemService) toEntryInfo(path string, info os.FileInfo) *pb.EntryInfo {
	entryType := pb.FileType_FILE_TYPE_FILE
	if info.IsDir() {
		entryType = pb.FileType_FILE_TYPE_DIRECTORY
	}

	entry := &pb.EntryInfo{
		Name:         info.Name(),
		Type:         entryType,
		Path:         path,
		Size:         info.Size(),
		Mode:         uint32(info.Mode()),
		Permissions:  info.Mode().String(),
		ModifiedTime: timestamppb.New(info.ModTime()),
	}

	// Get owner/group info on Unix
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		entry.Owner = strconv.Itoa(int(stat.Uid))
		entry.Group = strconv.Itoa(int(stat.Gid))

		if u, err := user.LookupId(entry.Owner); err == nil {
			entry.Owner = u.Username
		}
		if g, err := user.LookupGroupId(entry.Group); err == nil {
			entry.Group = g.Name
		}
	}

	// Get symlink target
	if info.Mode()&os.ModeSymlink != 0 {
		if target, err := os.Readlink(path); err == nil {
			entry.SymlinkTarget = &target
		}
	}

	return entry
}

// Read reads file contents (used by HTTP handler)
func (s *FilesystemService) Read(path string) ([]byte, error) {
	fullPath := s.resolvePath(path)
	return os.ReadFile(fullPath)
}

// Write writes file contents (used by HTTP handler)
func (s *FilesystemService) Write(path string, data []byte) error {
	fullPath := s.resolvePath(path)

	// Ensure parent directory exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(fullPath, data, 0644)
}

// WriteStream writes file contents from a reader
func (s *FilesystemService) WriteStream(path string, r io.Reader) (int64, error) {
	fullPath := s.resolvePath(path)

	// Ensure parent directory exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, err
	}

	f, err := os.Create(fullPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	return io.Copy(f, r)
}
