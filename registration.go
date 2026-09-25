package consulx

import "sync"

// Registration describes the instance as registered in Consul.
type Registration struct {
	ServiceID   string
	ServiceName string
	Address     string
	Port        int
	Scheme      string
	// CheckID is the ID of the check registered with the service, or "" for
	// CheckNone.
	CheckID   string
	CheckType CheckType
	// Registered reports whether the instance is currently registered, as
	// far as ConsulX knows.
	Registered bool
}

// registration is the mutable registration state shared by the runtime
// tasks and the public accessors.
type registration struct {
	mu      sync.Mutex
	current Registration
}

func (r *registration) get() Registration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

func (r *registration) set(reg Registration) {
	r.mu.Lock()
	r.current = reg
	r.mu.Unlock()
}

func (r *registration) markLost() {
	r.mu.Lock()
	r.current.Registered = false
	r.mu.Unlock()
}
