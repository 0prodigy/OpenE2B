package rpc

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"connectrpc.com/connect"
	pb "github.com/0prodigy/OpenE2B/pkg/proto/process"
	"github.com/0prodigy/OpenE2B/pkg/proto/process/processconnect"
)

// ProcessService implements the Connect-RPC ProcessHandler interface
type ProcessService struct {
	processconnect.UnimplementedProcessHandler

	mu        sync.RWMutex
	processes map[uint32]*RunningProcess
	nextPID   uint32
}

// RunningProcess represents a running process
type RunningProcess struct {
	PID     uint32
	Tag     *string
	Cmd     *exec.Cmd
	Stdin   *os.File
	Stdout  *os.File
	Stderr  *os.File
	Done    chan struct{}
	Config  *pb.ProcessConfig
}

// NewProcessService creates a new process service
func NewProcessService() *ProcessService {
	return &ProcessService{
		processes: make(map[uint32]*RunningProcess),
		nextPID:   1,
	}
}

// List returns all running processes
func (s *ProcessService) List(ctx context.Context, req *connect.Request[pb.ListRequest]) (*connect.Response[pb.ListResponse], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var infos []*pb.ProcessInfo
	for _, proc := range s.processes {
		infos = append(infos, &pb.ProcessInfo{
			Config: proc.Config,
			Pid:    proc.PID,
			Tag:    proc.Tag,
		})
	}

	return connect.NewResponse(&pb.ListResponse{Processes: infos}), nil
}

// Start starts a new process and streams output
func (s *ProcessService) Start(ctx context.Context, req *connect.Request[pb.StartRequest], stream *connect.ServerStream[pb.StartResponse]) error {
	procConfig := req.Msg.Process
	if procConfig == nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("process config is required"))
	}

	log.Printf("[process] Starting: %s %v", procConfig.Cmd, procConfig.Args)

	// Build command
	cmd := exec.CommandContext(ctx, procConfig.Cmd, procConfig.Args...)

	// Set working directory
	if procConfig.Cwd != nil && *procConfig.Cwd != "" {
		cmd.Dir = *procConfig.Cwd
	}

	// Set environment
	cmd.Env = os.Environ()
	for k, v := range procConfig.Envs {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Create pipes
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create stdout pipe: %w", err))
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create stderr pipe: %w", err))
	}

	// Handle stdin if requested
	var stdinPipe *os.File
	if req.Msg.Stdin != nil && *req.Msg.Stdin {
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create stdin pipe: %w", err))
		}
		if f, ok := stdin.(*os.File); ok {
			stdinPipe = f
		}
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to start process: %w", err))
	}

	// Allocate PID
	s.mu.Lock()
	pid := s.nextPID
	s.nextPID++

	done := make(chan struct{})
	proc := &RunningProcess{
		PID:    pid,
		Tag:    req.Msg.Tag,
		Cmd:    cmd,
		Stdin:  stdinPipe,
		Done:   done,
		Config: procConfig,
	}
	s.processes[pid] = proc
	s.mu.Unlock()

	log.Printf("[process] Started PID %d: %s", pid, procConfig.Cmd)

	// Send start event
	if err := stream.Send(&pb.StartResponse{
		Event: &pb.ProcessEvent{
			Event: &pb.ProcessEvent_Start{
				Start: &pb.ProcessEvent_StartEvent{Pid: pid},
			},
		},
	}); err != nil {
		return err
	}

	// Stream stdout/stderr in goroutines
	// Use a mutex to protect concurrent stream.Send calls
	var streamMu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(2)

	// Stdout
	go func() {
		defer wg.Done()
		reader := bufio.NewReader(stdout)
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				streamMu.Lock()
				sendErr := stream.Send(&pb.StartResponse{
					Event: &pb.ProcessEvent{
						Event: &pb.ProcessEvent_Data{
							Data: &pb.ProcessEvent_DataEvent{
								Output: &pb.ProcessEvent_DataEvent_Stdout{Stdout: data},
							},
						},
					},
				})
				streamMu.Unlock()
				if sendErr != nil {
					log.Printf("[process] Error sending stdout: %v", sendErr)
					break
				}
			}
			if err != nil {
				break
			}
		}
	}()

	// Stderr
	go func() {
		defer wg.Done()
		reader := bufio.NewReader(stderr)
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				streamMu.Lock()
				sendErr := stream.Send(&pb.StartResponse{
					Event: &pb.ProcessEvent{
						Event: &pb.ProcessEvent_Data{
							Data: &pb.ProcessEvent_DataEvent{
								Output: &pb.ProcessEvent_DataEvent_Stderr{Stderr: data},
							},
						},
					},
				})
				streamMu.Unlock()
				if sendErr != nil {
					log.Printf("[process] Error sending stderr: %v", sendErr)
					break
				}
			}
			if err != nil {
				break
			}
		}
	}()

	// Wait for process to complete
	err = cmd.Wait()
	wg.Wait()
	close(done)

	// Remove from process list
	s.mu.Lock()
	delete(s.processes, pid)
	s.mu.Unlock()

	// Send end event
	exitCode := int32(0)
	exited := true
	status := "exited"
	var errorMsg *string

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = int32(exitErr.ExitCode())
		} else {
			status = "error"
			errStr := err.Error()
			errorMsg = &errStr
		}
	} else if cmd.ProcessState != nil {
		exitCode = int32(cmd.ProcessState.ExitCode())
	}

	log.Printf("[process] PID %d exited with code %d", pid, exitCode)

	streamMu.Lock()
	defer streamMu.Unlock()
	return stream.Send(&pb.StartResponse{
		Event: &pb.ProcessEvent{
			Event: &pb.ProcessEvent_End{
				End: &pb.ProcessEvent_EndEvent{
					ExitCode: exitCode,
					Exited:   exited,
					Status:   status,
					Error:    errorMsg,
				},
			},
		},
	})
}

// Connect connects to an existing process
func (s *ProcessService) Connect(ctx context.Context, req *connect.Request[pb.ConnectRequest], stream *connect.ServerStream[pb.ConnectResponse]) error {
	selector := req.Msg.Process
	if selector == nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("process selector is required"))
	}

	s.mu.RLock()
	var proc *RunningProcess

	switch sel := selector.Selector.(type) {
	case *pb.ProcessSelector_Pid:
		proc = s.processes[sel.Pid]
	case *pb.ProcessSelector_Tag:
		for _, p := range s.processes {
			if p.Tag != nil && *p.Tag == sel.Tag {
				proc = p
				break
			}
		}
	}
	s.mu.RUnlock()

	if proc == nil {
		return connect.NewError(connect.CodeNotFound, fmt.Errorf("process not found"))
	}

	// Send start event with current PID
	if err := stream.Send(&pb.ConnectResponse{
		Event: &pb.ProcessEvent{
			Event: &pb.ProcessEvent_Start{
				Start: &pb.ProcessEvent_StartEvent{Pid: proc.PID},
			},
		},
	}); err != nil {
		return err
	}

	// Wait for process to end
	<-proc.Done

	exitCode := int32(0)
	if proc.Cmd.ProcessState != nil {
		exitCode = int32(proc.Cmd.ProcessState.ExitCode())
	}

	return stream.Send(&pb.ConnectResponse{
		Event: &pb.ProcessEvent{
			Event: &pb.ProcessEvent_End{
				End: &pb.ProcessEvent_EndEvent{
					ExitCode: exitCode,
					Exited:   true,
					Status:   "exited",
				},
			},
		},
	})
}

// Update updates process settings (e.g., PTY size)
func (s *ProcessService) Update(ctx context.Context, req *connect.Request[pb.UpdateRequest]) (*connect.Response[pb.UpdateResponse], error) {
	// PTY resizing not implemented yet
	return connect.NewResponse(&pb.UpdateResponse{}), nil
}

// StreamInput handles streaming input to a process
func (s *ProcessService) StreamInput(ctx context.Context, stream *connect.ClientStream[pb.StreamInputRequest]) (*connect.Response[pb.StreamInputResponse], error) {
	var proc *RunningProcess

	for stream.Receive() {
		msg := stream.Msg()

		switch event := msg.Event.(type) {
		case *pb.StreamInputRequest_Start:
			selector := event.Start.Process
			s.mu.RLock()
			switch sel := selector.Selector.(type) {
			case *pb.ProcessSelector_Pid:
				proc = s.processes[sel.Pid]
			case *pb.ProcessSelector_Tag:
				for _, p := range s.processes {
					if p.Tag != nil && *p.Tag == sel.Tag {
						proc = p
						break
					}
				}
			}
			s.mu.RUnlock()

			if proc == nil {
				return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("process not found"))
			}

		case *pb.StreamInputRequest_Data:
			if proc == nil {
				continue
			}
			if proc.Stdin != nil {
				input := event.Data.Input
				if input != nil {
					switch data := input.Input.(type) {
					case *pb.ProcessInput_Stdin:
						proc.Stdin.Write(data.Stdin)
					case *pb.ProcessInput_Pty:
						proc.Stdin.Write(data.Pty)
					}
				}
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.StreamInputResponse{}), nil
}

// SendInput sends input to a process
func (s *ProcessService) SendInput(ctx context.Context, req *connect.Request[pb.SendInputRequest]) (*connect.Response[pb.SendInputResponse], error) {
	selector := req.Msg.Process
	if selector == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("process selector is required"))
	}

	s.mu.RLock()
	var proc *RunningProcess
	switch sel := selector.Selector.(type) {
	case *pb.ProcessSelector_Pid:
		proc = s.processes[sel.Pid]
	case *pb.ProcessSelector_Tag:
		for _, p := range s.processes {
			if p.Tag != nil && *p.Tag == sel.Tag {
				proc = p
				break
			}
		}
	}
	s.mu.RUnlock()

	if proc == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("process not found"))
	}

	if proc.Stdin == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("stdin not enabled for this process"))
	}

	input := req.Msg.Input
	if input != nil {
		switch data := input.Input.(type) {
		case *pb.ProcessInput_Stdin:
			proc.Stdin.Write(data.Stdin)
		case *pb.ProcessInput_Pty:
			proc.Stdin.Write(data.Pty)
		}
	}

	return connect.NewResponse(&pb.SendInputResponse{}), nil
}

// SendSignal sends a signal to a process
func (s *ProcessService) SendSignal(ctx context.Context, req *connect.Request[pb.SendSignalRequest]) (*connect.Response[pb.SendSignalResponse], error) {
	selector := req.Msg.Process
	if selector == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("process selector is required"))
	}

	s.mu.RLock()
	var proc *RunningProcess
	switch sel := selector.Selector.(type) {
	case *pb.ProcessSelector_Pid:
		proc = s.processes[sel.Pid]
	case *pb.ProcessSelector_Tag:
		for _, p := range s.processes {
			if p.Tag != nil && *p.Tag == sel.Tag {
				proc = p
				break
			}
		}
	}
	s.mu.RUnlock()

	if proc == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("process not found"))
	}

	if proc.Cmd.Process == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("process not running"))
	}

	var sig syscall.Signal
	switch req.Msg.Signal {
	case pb.Signal_SIGNAL_SIGTERM:
		sig = syscall.SIGTERM
	case pb.Signal_SIGNAL_SIGKILL:
		sig = syscall.SIGKILL
	default:
		sig = syscall.SIGTERM
	}

	if err := proc.Cmd.Process.Signal(sig); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to send signal: %w", err))
	}

	return connect.NewResponse(&pb.SendSignalResponse{}), nil
}
