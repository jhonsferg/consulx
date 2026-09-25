package consulx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/jhonsferg/consulx/health"
	"github.com/jhonsferg/consulx/internal/compat"
	"github.com/jhonsferg/consulx/internal/netaddr"
	"github.com/jhonsferg/consulx/internal/serviceid"
)

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

// Registration returns the current registration. Registered is false before
// the first successful registration, after deregistration and while the
// service is known to be missing from the agent.
func (c *Client) Registration() Registration { return c.reg.get() }

// WithRegistrationHook lets the application adjust the service definition
// right before it is sent, for settings ConsulX does not model (Connect
// sidecars, proxy configuration, Kind, Locality, extra checks). The hook
// runs on every (re)registration and must be deterministic. Feature checks
// run after the hook.
func WithRegistrationHook(fn func(*api.AgentServiceRegistration)) Option {
	return optionFunc(func(s *settings) error {
		if fn == nil {
			return &ConfigError{Field: "RegistrationHook", Reason: "must not be nil"}
		}
		s.regHook = fn
		return nil
	})
}

// serviceID returns the configured or generated instance ID.
func (c *Client) serviceID(port int) string {
	s := c.cfg.Service
	if s.ID != "" {
		return s.ID
	}
	c.idOnce.Do(func() {
		if s.IDStrategy == IDRandom {
			c.generatedID = serviceid.Random(s.Name)
			return
		}
		host, _ := os.Hostname()
		c.generatedID = serviceid.HostnamePort(s.Name, host, port)
	})
	return c.generatedID
}

// checkID is deterministic so TTL updates can address the check.
func checkID(serviceID string) string { return "service:" + serviceID }

// buildRegistration turns the configuration and the resolved endpoint into
// the definition sent to the agent.
func (c *Client) buildRegistration(ctx context.Context, ep endpoint) (*api.AgentServiceRegistration, error) {
	s := c.cfg.Service
	id := c.serviceID(ep.Port)
	reg := &api.AgentServiceRegistration{
		ID:                id,
		Name:              s.Name,
		Tags:              slices.Clone(s.Tags),
		Address:           ep.Address,
		Meta:              c.buildMeta(ep),
		EnableTagOverride: s.EnableTagOverride,
		Namespace:         s.Namespace,
		Partition:         s.Partition,
	}
	if len(s.Ports) > 0 {
		for _, p := range s.Ports {
			reg.Ports = append(reg.Ports, api.ServicePort{Name: p.Name, Port: p.Port, Default: p.Default})
		}
	} else {
		reg.Port = ep.Port
	}
	if s.Weights != nil {
		reg.Weights = &api.AgentWeights{Passing: s.Weights.Passing, Warning: s.Weights.Warning}
	}
	if len(s.TaggedAddresses) > 0 {
		reg.TaggedAddresses = make(map[string]api.ServiceAddress, len(s.TaggedAddresses))
		for k, v := range s.TaggedAddresses {
			reg.TaggedAddresses[k] = api.ServiceAddress{Address: v.Address, Port: v.Port}
		}
	}
	reg.Check = c.buildCheck(ctx, id, ep)

	if c.regHook != nil {
		c.regHook(reg)
	}
	if err := c.checkFeatures(reg); err != nil {
		return nil, err
	}
	return reg, nil
}

// buildMeta merges automatic metadata with the user's. User keys always win.
func (c *Client) buildMeta(ep endpoint) map[string]string {
	s := c.cfg.Service
	meta := map[string]string{"secure": strconv.FormatBool(ep.Scheme == "https")}
	if !s.DisableAutoMeta {
		meta["language"] = "go"
		meta["go_version"] = runtime.Version()
		meta["consulx_version"] = libraryVersion()
		if host, err := os.Hostname(); err == nil {
			meta["hostname"] = host
		}
	}
	for k, v := range map[string]string{"version": s.Version, "environment": s.Environment, "zone": s.Zone} {
		if v != "" {
			meta[k] = v
		}
	}
	maps.Copy(meta, s.Meta)
	return meta
}

// buildCheck returns the check definition, or nil for CheckNone.
func (c *Client) buildCheck(ctx context.Context, serviceID string, ep endpoint) *api.AgentServiceCheck {
	h := c.cfg.Health
	if h.Check == CheckNone {
		return nil
	}
	chk := &api.AgentServiceCheck{
		CheckID:                checkID(serviceID),
		Name:                   c.cfg.Service.Name + " " + string(h.Check) + " check",
		SuccessBeforePassing:   h.SuccessBeforePassing,
		FailuresBeforeWarning:  h.FailuresBeforeWarning,
		FailuresBeforeCritical: h.FailuresBeforeCritical,
	}
	if d := h.DeregisterCriticalServiceAfter; d > 0 {
		chk.DeregisterCriticalServiceAfter = d.String()
	}
	hostPort := net.JoinHostPort(ep.Address, strconv.Itoa(ep.Port))
	switch h.Check {
	case CheckHTTP:
		chk.HTTP = ep.Scheme + "://" + hostPort + h.CheckPath
		chk.Method = h.Method
		chk.Header = maps.Clone(h.Header)
		chk.Body = h.Body
		chk.TLSServerName = h.TLSServerName
		chk.TLSSkipVerify = h.TLSSkipVerify
	case CheckTCP:
		chk.TCP = hostPort
		chk.TCPUseTLS = h.UseTLS
		chk.TLSServerName = h.TLSServerName
		chk.TLSSkipVerify = h.TLSSkipVerify
	case CheckGRPC:
		chk.GRPC = hostPort
		if h.GRPCService != "" {
			chk.GRPC += "/" + h.GRPCService
		}
		chk.GRPCUseTLS = h.UseTLS
		chk.TLSServerName = h.TLSServerName
		chk.TLSSkipVerify = h.TLSSkipVerify
	case CheckTTL:
		chk.TTL = h.TTL.String()
		// Start with the current readiness instead of Consul's default
		// (critical), so a ready instance is routable immediately.
		chk.Status = ttlStatus(c.health.Ready(ctx).Status)
		return chk
	}
	chk.Interval = h.Interval.String()
	chk.Timeout = h.Timeout.String()
	return chk
}

// checkFeatures rejects fields the connected agent does not support.
func (c *Client) checkFeatures(reg *api.AgentServiceRegistration) error {
	need := func(cond bool, f compat.Feature) error {
		if !cond {
			return nil
		}
		return c.requireFeature(f)
	}
	ipv6 := netaddr.IsIPv6(reg.Address)
	for _, ta := range reg.TaggedAddresses {
		ipv6 = ipv6 || netaddr.IsIPv6(ta.Address)
	}
	return errors.Join(
		need(len(reg.Ports) > 0, compat.MultiPort),
		need(ipv6, compat.IPv6Address),
		need(reg.Namespace != "" || c.cfg.Consul.Namespace != "", compat.Namespaces),
		need(reg.Partition != "" || c.cfg.Consul.Partition != "", compat.Partitions),
		need(reg.AI != nil, compat.AIService),
	)
}

// register performs one registration attempt: detect the agent, resolve the
// endpoint, build the definition and send it.
func (c *Client) register(ctx context.Context) error {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()

	if _, err := c.detectAgent(ctx); err != nil {
		// A token without agent:read cannot read the version; the gate then
		// stays permissive and the agent validates the request itself.
		if !isStatus(err, http.StatusForbidden) {
			return c.opError(ErrRegistrationFailed, err)
		}
		c.log.Debug("agent version not readable, feature checks relaxed", slog.Any("error", err))
	}
	ep, err := c.resolveEndpoint(ctx)
	if err != nil {
		return err
	}
	reg, err := c.buildRegistration(ctx, ep)
	if err != nil {
		return err
	}

	c.log.Debug("service registration started", slog.String("service_id", reg.ID))
	c.metrics.IncCounter(MetricConsulRequestsTotal, Label{"operation", "register"})
	start := time.Now()
	opts := api.ServiceRegisterOpts{ReplaceExistingChecks: true}.WithContext(ctx)
	err = c.api.Agent().ServiceRegisterOpts(reg, opts)
	c.metrics.ObserveDuration(MetricConsulRequestDuration, time.Since(start), Label{"operation", "register"})
	if err != nil {
		c.metrics.IncCounter(MetricRegisterErrorsTotal)
		c.metrics.IncCounter(MetricConsulRequestErrorsTotal, Label{"operation", "register"})
		return c.opError(ErrRegistrationFailed, err)
	}
	c.metrics.IncCounter(MetricRegisterTotal)

	current := Registration{
		ServiceID: reg.ID, ServiceName: reg.Name, Address: ep.Address, Port: ep.Port,
		Scheme: ep.Scheme, CheckType: c.cfg.Health.Check, Registered: true,
	}
	if reg.Check != nil {
		current.CheckID = reg.Check.CheckID
	}
	c.reg.set(current)
	c.log.Info("service registered",
		slog.String("service", reg.Name), slog.String("service_id", reg.ID),
		slog.String("address", ep.Address), slog.String("address_source", ep.AddressSource),
		slog.Int("port", ep.Port), slog.String("check", string(c.cfg.Health.Check)))
	if reg.Check != nil {
		c.log.Debug("health check configured", slog.String("check_id", reg.Check.CheckID),
			slog.String("type", string(c.cfg.Health.Check)))
	}
	return nil
}

// deregister removes the service from the agent.
func (c *Client) deregister(ctx context.Context) error {
	cur := c.reg.get()
	if cur.ServiceID == "" {
		return nil
	}
	c.log.Info("service deregistration started", slog.String("service_id", cur.ServiceID))
	q := (&api.QueryOptions{}).WithContext(ctx)
	c.metrics.IncCounter(MetricConsulRequestsTotal, Label{"operation", "deregister"})
	if err := c.api.Agent().ServiceDeregisterOpts(cur.ServiceID, q); err != nil && !isStatus(err, http.StatusNotFound) {
		c.metrics.IncCounter(MetricDeregisterErrorsTotal)
		c.metrics.IncCounter(MetricConsulRequestErrorsTotal, Label{"operation", "deregister"})
		return c.opError(ErrDeregistrationFailed, err)
	}
	c.reg.markLost()
	c.metrics.IncCounter(MetricDeregisterTotal)
	c.log.Info("service deregistered", slog.String("service_id", cur.ServiceID))
	return nil
}

// Register registers the service once, without starting the runtime. Use
// it only with WithAutoRegister(false) when the application manages
// registration itself; nothing re-registers the service if the agent loses
// it. Prefer Start or Run for the managed lifecycle.
func (c *Client) Register(ctx context.Context) error { return c.register(ctx) }

// Deregister removes the service registered by this Client. Stop does it
// automatically unless DeregisterOnShutdown is false.
func (c *Client) Deregister(ctx context.Context) error {
	if c.reg.get().ServiceID == "" {
		return ErrNotRegistered
	}
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	return c.deregister(ctx)
}

// EnableMaintenance puts the service in maintenance mode: Consul marks it
// critical and discovery stops returning it, while it stays registered.
// reason is shown in Consul.
func (c *Client) EnableMaintenance(ctx context.Context, reason string) error {
	id := c.reg.get().ServiceID
	if id == "" {
		return ErrNotRegistered
	}
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	q := (&api.QueryOptions{}).WithContext(ctx)
	if err := c.api.Agent().EnableServiceMaintenanceOpts(id, reason, q); err != nil {
		return c.opError(nil, err)
	}
	c.log.Info("service maintenance enabled", slog.String("service_id", id), slog.String("reason", reason))
	return nil
}

// DisableMaintenance takes the service out of maintenance mode.
func (c *Client) DisableMaintenance(ctx context.Context) error {
	id := c.reg.get().ServiceID
	if id == "" {
		return ErrNotRegistered
	}
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	q := (&api.QueryOptions{}).WithContext(ctx)
	if err := c.api.Agent().DisableServiceMaintenanceOpts(id, q); err != nil {
		return c.opError(nil, err)
	}
	c.log.Info("service maintenance disabled", slog.String("service_id", id))
	return nil
}

// opError wraps a Consul error with op (may be nil) and, for transport
// failures, ErrConsulUnavailable, keeping the original error in the chain.
func (c *Client) opError(op, err error) error {
	var se api.StatusError
	switch {
	case errors.As(err, &se):
		if op == nil {
			return fmt.Errorf("consulx: consul returned %d: %w", se.Code, err)
		}
		return fmt.Errorf("%w: %w", op, err)
	case errors.Is(err, ErrConsulUnavailable):
		if op == nil {
			return err
		}
		return fmt.Errorf("%w: %w", op, err)
	case op == nil:
		return fmt.Errorf("%w: %w", ErrConsulUnavailable, err)
	default:
		return fmt.Errorf("%w: %w: %w", op, ErrConsulUnavailable, err)
	}
}

// isStatus reports whether err carries the HTTP status code from Consul.
func isStatus(err error, code int) bool {
	var se api.StatusError
	return errors.As(err, &se) && se.Code == code
}

// isPermanent reports errors that retrying cannot fix: invalid
// configuration, unsupported features, and 4xx responses other than 408
// and 429.
func isPermanent(err error) bool {
	if errors.Is(err, ErrInvalidConfiguration) || errors.Is(err, ErrUnsupportedFeature) {
		return true
	}
	var se api.StatusError
	if errors.As(err, &se) {
		return se.Code >= 400 && se.Code < 500 && se.Code != http.StatusRequestTimeout && se.Code != http.StatusTooManyRequests
	}
	return false
}

// ttlStatus maps a health status to a Consul check status.
func ttlStatus(s health.Status) string {
	switch s {
	case health.StatusUp:
		return api.HealthPassing
	case health.StatusDegraded:
		return api.HealthWarning
	default:
		return api.HealthCritical
	}
}

var libVersion = sync.OnceValue(func() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	const path = "github.com/jhonsferg/consulx"
	if info.Main.Path == path {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	for _, dep := range info.Deps {
		if dep.Path == path {
			return strings.TrimPrefix(dep.Version, "v")
		}
	}
	return "unknown"
})

// libraryVersion returns the ConsulX module version from build info.
func libraryVersion() string { return libVersion() }
