package consulx

import (
	"context"
	"sync"
)

// State is the lifecycle state of a Client.
type State int32

const (
	// StateIdle: created, not started.
	StateIdle State = iota
	// StateStarting: Start is running.
	StateStarting
	// StateRunning: started and registered (or registration not required).
	StateRunning
	// StateDegraded: started, but Consul is unreachable or the service is
	// not registered; ConsulX is retrying in the background.
	StateDegraded
	// StateStopping: Stop is running.
	StateStopping
	// StateStopped: stopped; Done is closed. Terminal.
	StateStopped
)

var stateNames = [...]string{"idle", "starting", "running", "degraded", "stopping", "stopped"}

func (s State) String() string {
	if s >= 0 && int(s) < len(stateNames) {
		return stateNames[s]
	}
	return "unknown"
}

// errorsBuffer bounds Errors(). Errors beyond it are dropped (and counted)
// rather than blocking the runtime.
const errorsBuffer = 32

// lifecycle holds the runtime state of a Client.
type lifecycle struct {
	// opMu serialises Start and Stop, so Stop waits for an in-flight Start.
	opMu sync.Mutex

	stateMu sync.RWMutex
	state   State

	ctx    context.Context    // runtime context, set by Start
	cancel context.CancelFunc // cancels ctx
	wg     sync.WaitGroup     // runtime goroutines
	done   chan struct{}

	errMu     sync.Mutex
	errs      chan error
	errClosed bool
}

func newLifecycle() lifecycle {
	return lifecycle{done: make(chan struct{}), errs: make(chan error, errorsBuffer)}
}

