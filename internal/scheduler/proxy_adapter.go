package scheduler

import (
	"github.com/0prodigy/OpenE2B/internal/proxy"
)

// SchedulerProxyAdapter adapts the Scheduler to implement proxy.SandboxLookup
type SchedulerProxyAdapter struct {
	scheduler *Scheduler
}

// NewSchedulerProxyAdapter creates a new proxy adapter for the scheduler
func NewSchedulerProxyAdapter(scheduler *Scheduler) *SchedulerProxyAdapter {
	return &SchedulerProxyAdapter{scheduler: scheduler}
}

// GetSandboxHost implements proxy.SandboxLookup
func (a *SchedulerProxyAdapter) GetSandboxHost(sandboxID string) (host string, port int, state proxy.SandboxState, ok bool) {
	h, p, s, exists := a.scheduler.GetSandboxHost(sandboxID)
	if !exists {
		return "", 0, "", false
	}

	// Convert scheduler state to proxy state
	var proxyState proxy.SandboxState
	switch s {
	case SandboxStateRunning:
		proxyState = proxy.SandboxStateRunning
	case SandboxStatePaused:
		proxyState = proxy.SandboxStatePaused
	case SandboxStateStopped:
		proxyState = proxy.SandboxStateStopped
	default:
		proxyState = proxy.SandboxStateError
	}

	return h, p, proxyState, true
}
