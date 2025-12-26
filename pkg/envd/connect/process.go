package connect

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/0prodigy/OpenE2B/pkg/envd/process"
)

// ProcessHandler provides HTTP/Connect handlers for process operations
type ProcessHandler struct {
	service *process.Service
}

// NewProcessHandler creates a new process handler
func NewProcessHandler() *ProcessHandler {
	return &ProcessHandler{
		service: process.NewService(),
	}
}

// Request/Response types matching the proto definitions

type ProcessConfig struct {
	Cmd  string            `json:"cmd"`
	Args []string          `json:"args"`
	Envs map[string]string `json:"envs"`
	Cwd  *string           `json:"cwd,omitempty"`
}

type PTY struct {
	Size *PTYSize `json:"size,omitempty"`
}

type PTYSize struct {
	Cols uint32 `json:"cols"`
	Rows uint32 `json:"rows"`
}

type ListRequest struct{}

type ListResponse struct {
	Processes []ProcessInfo `json:"processes"`
}

type ProcessInfo struct {
	Config ProcessConfig `json:"config"`
	PID    uint32        `json:"pid"`
	Tag    *string       `json:"tag,omitempty"`
}

type StartRequest struct {
	Process ProcessConfig `json:"process"`
	PTY     *PTY          `json:"pty,omitempty"`
	Tag     *string       `json:"tag,omitempty"`
	Stdin   *bool         `json:"stdin,omitempty"`
}

type ProcessSelector struct {
	PID *uint32 `json:"pid,omitempty"`
	Tag *string `json:"tag,omitempty"`
}

type SendInputRequest struct {
	Process ProcessSelector `json:"process"`
	Input   ProcessInput    `json:"input"`
}

type ProcessInput struct {
	Stdin []byte `json:"stdin,omitempty"`
	PTY   []byte `json:"pty,omitempty"`
}

type SendSignalRequest struct {
	Process ProcessSelector `json:"process"`
	Signal  int32           `json:"signal"`
}

type ConnectRequest struct {
	Process ProcessSelector `json:"process"`
}

type ProcessEvent struct {
	Start     *StartEvent `json:"start,omitempty"`
	Data      *DataEvent  `json:"data,omitempty"`
	End       *EndEvent   `json:"end,omitempty"`
	Keepalive *struct{}   `json:"keepalive,omitempty"`
}

type StartEvent struct {
	PID uint32 `json:"pid"`
}

type DataEvent struct {
	Stdout []byte `json:"stdout,omitempty"`
	Stderr []byte `json:"stderr,omitempty"`
	PTY    []byte `json:"pty,omitempty"`
}

type EndEvent struct {
	ExitCode int32   `json:"exit_code"`
	Exited   bool    `json:"exited"`
	Status   string  `json:"status"`
	Error    *string `json:"error,omitempty"`
}

// Register registers all process routes
func (h *ProcessHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /process.Process/List", h.List)
	mux.HandleFunc("POST /process.Process/Start", h.Start)
	mux.HandleFunc("POST /process.Process/Connect", h.Connect)
	mux.HandleFunc("POST /process.Process/SendInput", h.SendInput)
	mux.HandleFunc("POST /process.Process/SendSignal", h.SendSignal)
}

// List handles process listing
func (h *ProcessHandler) List(w http.ResponseWriter, r *http.Request) {
	processes := h.service.List(r.Context())

	var result []ProcessInfo
	for _, p := range processes {
		result = append(result, toConnectProcessInfo(&p))
	}

	writeJSON(w, http.StatusOK, ListResponse{Processes: result})
}

// Start handles process start with streaming output
func (h *ProcessHandler) Start(w http.ResponseWriter, r *http.Request) {
	var req StartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	enableStdin := req.Stdin != nil && *req.Stdin

	config := process.ProcessConfig{
		Cmd:  req.Process.Cmd,
		Args: req.Process.Args,
		Envs: req.Process.Envs,
		Cwd:  req.Process.Cwd,
	}

	proc, err := h.service.Start(r.Context(), config, req.Tag, enableStdin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Stream response using SSE-like format
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming not supported")
		return
	}

	// Send start event
	startEvent := ProcessEvent{Start: &StartEvent{PID: proc.Info.PID}}
	json.NewEncoder(w).Encode(startEvent)
	flusher.Flush()

	// Stream stdout
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := bufio.NewReader(proc.Stdout)
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				dataEvent := ProcessEvent{Data: &DataEvent{Stdout: buf[:n]}}
				json.NewEncoder(w).Encode(dataEvent)
				flusher.Flush()
			}
			if err != nil {
				break
			}
		}
	}()

	// Stream stderr in parallel
	go func() {
		reader := bufio.NewReader(proc.Stderr)
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				dataEvent := ProcessEvent{Data: &DataEvent{Stderr: buf[:n]}}
				json.NewEncoder(w).Encode(dataEvent)
				flusher.Flush()
			}
			if err != nil {
				break
			}
		}
	}()

	// Wait for process to finish
	<-proc.Done
	<-done

	// Send end event
	exitCode := 0
	if proc.Cmd.ProcessState != nil {
		exitCode = proc.Cmd.ProcessState.ExitCode()
	}
	endEvent := ProcessEvent{End: &EndEvent{
		ExitCode: int32(exitCode),
		Exited:   true,
		Status:   "exited",
	}}
	json.NewEncoder(w).Encode(endEvent)
	flusher.Flush()
}

// Connect handles connecting to an existing process
func (h *ProcessHandler) Connect(w http.ResponseWriter, r *http.Request) {
	var req ConnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	proc, err := h.service.Connect(r.Context(), req.Process.PID, req.Process.Tag)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Stream response
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	// Send start event with current PID
	startEvent := ProcessEvent{Start: &StartEvent{PID: proc.Info.PID}}
	json.NewEncoder(w).Encode(startEvent)
	flusher.Flush()

	// Note: In a real implementation, we'd stream existing buffered output
	// For now, just wait for the process to end
	<-proc.Done

	exitCode := 0
	if proc.Cmd.ProcessState != nil {
		exitCode = proc.Cmd.ProcessState.ExitCode()
	}
	endEvent := ProcessEvent{End: &EndEvent{
		ExitCode: int32(exitCode),
		Exited:   true,
		Status:   "exited",
	}}
	json.NewEncoder(w).Encode(endEvent)
	flusher.Flush()
}

// SendInput handles sending input to a process
func (h *ProcessHandler) SendInput(w http.ResponseWriter, r *http.Request) {
	var req SendInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var data []byte
	if len(req.Input.Stdin) > 0 {
		data = req.Input.Stdin
	} else if len(req.Input.PTY) > 0 {
		data = req.Input.PTY
	}

	if err := h.service.SendInput(r.Context(), req.Process.PID, req.Process.Tag, data); err != nil {
		if err == io.EOF {
			writeError(w, http.StatusNotFound, "Process not found")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, struct{}{})
}

// SendSignal handles sending a signal to a process
func (h *ProcessHandler) SendSignal(w http.ResponseWriter, r *http.Request) {
	var req SendSignalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	signal := process.Signal(req.Signal)
	if err := h.service.SendSignal(r.Context(), req.Process.PID, req.Process.Tag, signal); err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Process not found: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, struct{}{})
}

func toConnectProcessInfo(p *process.ProcessInfo) ProcessInfo {
	return ProcessInfo{
		Config: ProcessConfig{
			Cmd:  p.Config.Cmd,
			Args: p.Config.Args,
			Envs: p.Config.Envs,
			Cwd:  p.Config.Cwd,
		},
		PID: p.PID,
		Tag: p.Tag,
	}
}
