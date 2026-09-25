package consulx

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jhonsferg/consulx/discovery"
)

// Sentinel errors. Every error returned by ConsulX that belongs to one of
// these categories matches it with errors.Is, while still wrapping the
// original cause.
var (
	// ErrInvalidConfiguration reports a configuration that cannot be used.
	// The concrete error is usually a *ConfigError.
	ErrInvalidConfiguration = errors.New("consulx: invalid configuration")

	// ErrAlreadyStarted is returned by Start when the Client was started before.
	ErrAlreadyStarted = errors.New("consulx: already started")

	// ErrAlreadyStopped is returned by Start when the Client has already been
	// stopped. A Client is single-use. Stop itself is idempotent.
	ErrAlreadyStopped = errors.New("consulx: already stopped")

	// ErrNotStarted is returned by Stop when Start was never called.
	ErrNotStarted = errors.New("consulx: not started")

	// ErrNotRegistered reports an operation that needs the service to be
	// registered in Consul while it is not.
	ErrNotRegistered = errors.New("consulx: service not registered")

	// ErrConsulUnavailable reports that the Consul agent could not be reached.
	ErrConsulUnavailable = errors.New("consulx: consul unavailable")

	// ErrRegistrationFailed reports that the service could not be registered.
	ErrRegistrationFailed = errors.New("consulx: service registration failed")

	// ErrDeregistrationFailed reports that the service could not be deregistered.
	ErrDeregistrationFailed = errors.New("consulx: service deregistration failed")

	// ErrUnsupportedFeature reports a feature the connected Consul agent does
	// not support. The concrete error is a *UnsupportedFeatureError.
	ErrUnsupportedFeature = errors.New("consulx: unsupported feature")

	// ErrServiceNotFound reports that no instance matched a discovery query.
	// It is the same value as discovery.ErrServiceNotFound.
	ErrServiceNotFound = discovery.ErrServiceNotFound
)

// ConfigError describes an invalid configuration field. It matches
// ErrInvalidConfiguration with errors.Is.
type ConfigError struct {
	// Field is the dotted path of the offending field, e.g. "Health.Interval".
	Field string
	// Reason explains what is wrong. It never contains secret values.
	Reason string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("consulx: invalid configuration: %s: %s", e.Field, e.Reason)
}

// Is reports whether target is ErrInvalidConfiguration.
func (e *ConfigError) Is(target error) bool { return target == ErrInvalidConfiguration }

// UnsupportedFeatureError reports that the connected Consul agent cannot
// handle a requested feature. It matches ErrUnsupportedFeature with errors.Is.
type UnsupportedFeatureError struct {
	// Feature is a short name such as "multi-port services".
	Feature string
	// Requirement describes what is needed, e.g. "Consul >= 1.22.0".
	Requirement string
	// Agent is the detected agent version, e.g. "1.21.5".
	Agent string
}

func (e *UnsupportedFeatureError) Error() string {
	return fmt.Sprintf("consulx: unsupported feature: %s requires %s, agent is %s",
		e.Feature, e.Requirement, e.Agent)
}

// Is reports whether target is ErrUnsupportedFeature.
func (e *UnsupportedFeatureError) Is(target error) bool { return target == ErrUnsupportedFeature }

// configErrors aggregates every validation problem so users can fix them in
// one pass instead of one error per run.
type configErrors []*ConfigError

func (es configErrors) Error() string {
	if len(es) == 1 {
		return es[0].Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "consulx: invalid configuration: %d problems:", len(es))
	for _, e := range es {
		fmt.Fprintf(&b, "\n  - %s: %s", e.Field, e.Reason)
	}
	return b.String()
}

// Unwrap exposes every *ConfigError to errors.Is and errors.As.
func (es configErrors) Unwrap() []error {
	out := make([]error, len(es))
	for i, e := range es {
		out[i] = e
	}
	return out
}

// errOrNil returns nil for an empty list so callers can return it directly.
func (es configErrors) errOrNil() error {
	if len(es) == 0 {
		return nil
	}
	return es
}
