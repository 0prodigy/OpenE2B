package process

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/google/uuid"
)

// Signal enumeration
type Signal int32

const (
	SignalUnspecified Signal = 0
	SignalSIGTERM     Signal = 15
	SignalSIGKILL     Signal = 9
)

// PTYSize represents terminal dimensions
type PTYSize struct {
	Cols uint32
	Rows uint32
}

// ProcessConfig holds process configuration
type ProcessConfig struct {
	Cmd  string
	Args []string
	Envs map[string]string
	Cwd  *string
}

// ProcessInfo holds running process information
type ProcessInfo struct {
	Config ProcessConfig
	PID    uint32
	Tag    *string
}

// Process represents a running process
type Process struct {
	Info   ProcessInfo
	Cmd    *exec.Cmd
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Stderr io.ReadCloser
	Done   chan struct{}
}

// Service implements process management
type Service struct {
	processes map[uint32]*Process
	tagIndex  map[string]*Process
	mu        sync.RWMutex
}

// NewService creates a new process service
func NewService() *Service {
	return &Service{
		processes: make(map[uint32]*Process),
		tagIndex:  make(map[string]*Process),
	}
}

// List returns all running processes
func (s *Service) List(ctx context.Context) []ProcessInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []ProcessInfo
	for _, p := range s.processes {
		result = append(result, p.Info)
	}
	return result
}

// Start starts a new process
func (s *Service) Start(ctx context.Context, config ProcessConfig, tag *string, enableStdin bool) (*Process, error) {
	// Build command
	cmd := exec.CommandContext(ctx, config.Cmd, config.Args...)

	// Set working directory
	if config.Cwd != nil {
		cmd.Dir = *config.Cwd
	}

	// Set environment
	cmd.Env = os.Environ()
	for k, v := range config.Envs {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// Set up pipes
	var stdin io.WriteCloser
	var err error
	if enableStdin {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	// Start process
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Create process record
	proc := &Process{
		Info: ProcessInfo{
			Config: config,
			PID:    uint32(cmd.Process.Pid),
			Tag:    tag,
		},
		Cmd:    cmd,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		Done:   make(chan struct{}),
	}

	// Register process
	s.mu.Lock()
	s.processes[proc.Info.PID] = proc
	if tag != nil {
		s.tagIndex[*tag] = proc
	}
	s.mu.Unlock()

	// Wait for process in background
	go func() {
		cmd.Wait()
		close(proc.Done)
		s.mu.Lock()
		delete(s.processes, proc.Info.PID)
		if tag != nil {
			delete(s.tagIndex, *tag)
		}
		s.mu.Unlock()
	}()

	return proc, nil
}

// Connect returns an existing process by selector
func (s *Service) Connect(ctx context.Context, pid *uint32, tag *string) (*Process, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if pid != nil {
		if p, ok := s.processes[*pid]; ok {
			return p, nil
		}
	}

	if tag != nil {
		if p, ok := s.tagIndex[*tag]; ok {
			return p, nil
		}
	}

	return nil, os.ErrNotExist
}

// SendInput sends input to a process
func (s *Service) SendInput(ctx context.Context, pid *uint32, tag *string, data []byte) error {
	proc, err := s.Connect(ctx, pid, tag)
	if err != nil {
		return err
	}

	if proc.Stdin == nil {
		return os.ErrInvalid
	}

	_, err = proc.Stdin.Write(data)
	return err
}

// SendSignal sends a signal to a process
func (s *Service) SendSignal(ctx context.Context, pid *uint32, tag *string, signal Signal) error {
	proc, err := s.Connect(ctx, pid, tag)
	if err != nil {
		return err
	}

	var sig syscall.Signal
	switch signal {
	case SignalSIGTERM:
		sig = syscall.SIGTERM
	case SignalSIGKILL:
		sig = syscall.SIGKILL
	default:
		sig = syscall.SIGTERM
	}

	return proc.Cmd.Process.Signal(sig)
}

// GenerateTag generates a unique process tag
func GenerateTag() string {
	return uuid.New().String()[:8]
}
