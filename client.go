package consulx

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/hashicorp/consul/api"

	"github.com/jhonsferg/consulx/health"
	"github.com/jhonsferg/consulx/internal/backoff"
	"github.com/jhonsferg/consulx/internal/compat"
)

// Client integrates one service instance with Consul. It is safe for
// concurrent use. Create it with New.
type Client struct {
	cfg       Config
	api       *api.Client
	transport *http.Transport // nil when the caller supplied the http.Client
	log       *slog.Logger
	metrics   Metrics
	retry     RetryPolicy

	server   *http.Server
	listener net.Listener
	resolver AddressResolver
	health   *health.Registry
	scheme   string // decided in New, see detectScheme

	lc lifecycle

	reg           registration
	regHook       func(*api.AgentServiceRegistration)
	idOnce        sync.Once
	generatedID   string
	healthChanged chan struct{} // wakes the TTL heartbeat

	agentMu sync.Mutex
	agent   compat.AgentInfo
	hasInfo bool
}

// New validates the configuration and creates a Client. It performs no
// network I/O: connectivity problems surface in Start, Run or the first
// request. Configuration problems are reported together in one error that
// matches ErrInvalidConfiguration.
//
// Layers are applied as documented on Config: defaults, then the
// environment, then every argument in order (Config values and options).
func New(opts ...Option) (*Client, error) {
	s, err := newSettings(os.LookupEnv, opts)
	if err != nil {
		return nil, err
	}
	raw, transport, err := newAPIClient(s)
	if err != nil {
		return nil, err
	}
	c := &Client{
		cfg:       s.cfg,
		api:       raw,
		transport: transport,
		log:       s.logger.With(slog.String("component", "consulx")),
		metrics:   s.metrics,
		retry:     s.retry,
		server:    s.server,
		listener:  s.listener,
		resolver:  s.resolver,
		// Checkers get 80% of the Consul check timeout, so the aggregated
		// response is written before Consul gives up on the request.
		health:        health.NewRegistry(s.cfg.Health.Timeout * 4 / 5),
		lc:            newLifecycle(),
		regHook:       s.regHook,
		healthChanged: make(chan struct{}, 1),
	}
	c.injectHealth()
	if c.retry == nil {
		r := s.cfg.Retry
		c.retry = backoff.Policy{
			Initial:     r.InitialDelay,
			Max:         r.MaxDelay,
			Multiplier:  r.Multiplier,
			Jitter:      !r.DisableJitter,
			MaxAttempts: r.MaxAttempts,
		}
	}

	cc := s.cfg.Consul
	if cc.TLS.InsecureSkipVerify {
		c.log.Warn("TLS certificate verification towards Consul is disabled; connections can be intercepted")
	}
	c.log.Debug("consul client initialized",
		slog.String("address", cc.Address),
		slog.String("scheme", consulScheme(cc)),
		slog.String("datacenter", cc.Datacenter),
		slog.Bool("tls", consulScheme(cc) == "https"),
		slog.Bool("token", cc.Token != "" || cc.TokenFile != ""),
	)
	return c, nil
}

// Raw returns the official Consul client used by ConsulX. Use it for any
// Consul API that ConsulX does not wrap. It shares ConsulX's connection
// settings, token, datacenter and namespace.
func (c *Client) Raw() *api.Client { return c.api }

// EffectiveConfig returns the configuration after every layer, option and
// default was applied. Secrets stay redacted when the result is printed.
func (c *Client) EffectiveConfig() Config {
	cfg := c.cfg
	cfg.Consul.TLS.CAPEM = nil
	cfg.Consul.TLS.CertPEM = nil
	cfg.Consul.TLS.KeyPEM = nil
	cfg.Service.Tags = append([]string(nil), cfg.Service.Tags...)
	cfg.Service.Meta = mergeMap(nil, cfg.Service.Meta)
	cfg.Service.TaggedAddresses = mergeMap(nil, cfg.Service.TaggedAddresses)
	cfg.Service.Ports = append([]ServicePort(nil), cfg.Service.Ports...)
	cfg.Health.Header = mergeMap(nil, cfg.Health.Header)
	return cfg
}

// AgentInfo describes the Consul agent ConsulX talks to.
type AgentInfo struct {
	// Version as reported by the agent, e.g. "1.22.7" or "2.0.4+ent".
	Version    string
	Enterprise bool
	Datacenter string
	NodeName   string
}

// AgentInfo queries the agent (GET /v1/agent/self) and caches the result
// for feature checks. The token needs agent:read; without it, ConsulX falls
// back to letting the agent validate each request.
func (c *Client) AgentInfo(ctx context.Context) (AgentInfo, error) {
	info, err := c.detectAgent(ctx)
	if err != nil {
		return AgentInfo{}, err
	}
	return AgentInfo{
		Version:    info.Version.Raw,
		Enterprise: info.Version.Enterprise,
		Datacenter: info.Datacenter,
		NodeName:   info.NodeName,
	}, nil
}

// detectAgent refreshes the cached agent information. It is called on every
// (re)connection because the agent may have been upgraded in between.
func (c *Client) detectAgent(ctx context.Context) (compat.AgentInfo, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	info, err := compat.Detect(ctx, c.api)
	if err != nil {
		return compat.AgentInfo{}, fmt.Errorf("%w: %w", ErrConsulUnavailable, err)
	}
	c.agentMu.Lock()
	c.agent, c.hasInfo = info, true
	c.agentMu.Unlock()
	return info, nil
}

// agentVersion returns the last detected version, or compat.Unknown.
func (c *Client) agentVersion() compat.Version {
	c.agentMu.Lock()
	defer c.agentMu.Unlock()
	if !c.hasInfo {
		return compat.Unknown
	}
	return c.agent.Version
}

// requireFeature returns an *UnsupportedFeatureError when the connected
// agent is known not to support f.
func (c *Client) requireFeature(f compat.Feature) error {
	v := c.agentVersion()
	if v.Supports(f) {
		return nil
	}
	return &UnsupportedFeatureError{Feature: f.Name(), Requirement: f.Requirement(), Agent: v.String()}
}

// requestContext bounds a non-blocking request with RequestTimeout.
func (c *Client) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if d := c.cfg.Consul.RequestTimeout; d > 0 {
		return context.WithTimeout(ctx, d)
	}
	return context.WithCancel(ctx)
}
