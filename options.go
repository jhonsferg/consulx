package consulx

import (
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/hashicorp/consul/api"
)

// Option configures a Client. Options are applied in order after the
// environment layer; a later option overrides an earlier one. Unlike Config
// layers, options assign values directly, so WithFailFast(false) does turn
// fail-fast off even when an earlier layer enabled it.
type Option interface {
	apply(*settings) error
}

type optionFunc func(*settings) error

func (f optionFunc) apply(s *settings) error { return f(s) }

// apply makes Config usable as an Option: its non-zero fields are overlaid
// on the configuration built so far.
func (c Config) apply(s *settings) error {
	s.cfg.overlay(c)
	return nil
}

// RetryPolicy decides how long to wait before retry number attempt (1-based).
// ok is false when no further attempt must be made. Implementations must be
// safe for concurrent use. The default policy is built from RetryConfig.
type RetryPolicy interface {
	NextDelay(attempt int) (delay time.Duration, ok bool)
}

// settings is the normalised result of every layer and option.
type settings struct {
	cfg Config

	logger     *slog.Logger
	metrics    Metrics
	retry      RetryPolicy
	httpClient *http.Client
	apiHook    func(*api.Config)
}

// WithConsulAddress sets the Consul agent address, for example
// "http://localhost:8500" or "https://consul.service.consul:8501".
func WithConsulAddress(addr string) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Consul.Address = addr
		return nil
	})
}

// WithDatacenter sets the datacenter used for every request.
func WithDatacenter(dc string) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Consul.Datacenter = dc
		return nil
	})
}

// WithNamespace sets the Consul Enterprise namespace for every request.
func WithNamespace(ns string) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Consul.Namespace = ns
		return nil
	})
}

// WithPartition sets the Consul Enterprise admin partition for every request.
func WithPartition(p string) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Consul.Partition = p
		return nil
	})
}

// WithToken sets the ACL token. The token is never logged.
func WithToken(token string) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Consul.Token = Secret(token)
		return nil
	})
}

// WithTokenFile reads the ACL token from path when the Client is created.
// A token file wins over WithToken.
func WithTokenFile(path string) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Consul.TokenFile = path
		return nil
	})
}

// WithTLS replaces the TLS configuration used to reach Consul and enables
// https.
func WithTLS(tls TLSConfig) Option {
	return optionFunc(func(s *settings) error {
		tls.Enabled = true
		tls.CAPEM = slices.Clone(tls.CAPEM)
		tls.CertPEM = slices.Clone(tls.CertPEM)
		tls.KeyPEM = slices.Clone(tls.KeyPEM)
		s.cfg.Consul.TLS = tls
		return nil
	})
}

// WithRequestTimeout bounds every non-blocking request to Consul.
func WithRequestTimeout(d time.Duration) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Consul.RequestTimeout = d
		return nil
	})
}

// WithHTTPClient makes ConsulX use client for every request to Consul, for
// example to install an OpenTelemetry transport. The caller then owns TLS,
// timeouts and connection pooling; the TLS and DialTimeout settings of
// ConsulConfig are ignored. client must not set a Timeout shorter than the
// blocking query WaitTime.
func WithHTTPClient(client *http.Client) Option {
	return optionFunc(func(s *settings) error {
		if client == nil {
			return &ConfigError{Field: "HTTPClient", Reason: "must not be nil"}
		}
		s.httpClient = client
		return nil
	})
}

// WithAPIConfig registers a function that can adjust the official client
// configuration right before the client is created. It is an escape hatch
// for settings ConsulX does not model; prefer the typed options.
func WithAPIConfig(fn func(*api.Config)) Option {
	return optionFunc(func(s *settings) error {
		s.apiHook = fn
		return nil
	})
}

// WithServiceName sets the logical service name.
func WithServiceName(name string) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Service.Name = name
		return nil
	})
}

// WithServiceID sets the instance ID. See IDStrategy for the generated default.
func WithServiceID(id string) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Service.ID = id
		return nil
	})
}

// WithServiceAddress sets the address registered for the instance, which
// disables automatic address resolution.
func WithServiceAddress(addr string) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Service.Address = addr
		return nil
	})
}

// WithServicePort sets the port registered for the instance.
func WithServicePort(port int) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Service.Port = port
		return nil
	})
}

// WithTags replaces the service tags.
func WithTags(tags ...string) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Service.Tags = slices.Clone(tags)
		return nil
	})
}

// WithMetadata merges meta into the service metadata. Keys set by the user
// always win over automatically generated metadata.
func WithMetadata(meta map[string]string) Option {
	return optionFunc(func(s *settings) error {
		if s.cfg.Service.Meta == nil {
			s.cfg.Service.Meta = make(map[string]string, len(meta))
		}
		maps.Copy(s.cfg.Service.Meta, meta)
		return nil
	})
}

// WithHealth overlays the non-zero fields of h on the health configuration.
func WithHealth(h HealthConfig) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Health.overlay(h)
		return nil
	})
}

// WithAutoHealth injects the default health endpoints (/health,
// /health/live, /health/ready) into the http.Server and makes Consul check
// the readiness endpoint.
func WithAutoHealth() Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Health.Enabled = true
		return nil
	})
}

// WithHealthEndpoints injects exactly the given non-empty health endpoints.
func WithHealthEndpoints(e HealthEndpoints) Option {
	return optionFunc(func(s *settings) error {
		if e == (HealthEndpoints{}) {
			return &ConfigError{Field: "Health.Endpoints", Reason: "at least one path is required"}
		}
		s.cfg.Health.Enabled = true
		s.cfg.Health.Endpoints = e
		return nil
	})
}

// WithDeregisterCriticalServiceAfter sets how long a critical instance stays
// registered before Consul removes it. Negative disables it.
func WithDeregisterCriticalServiceAfter(d time.Duration) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Health.DeregisterCriticalServiceAfter = d
		return nil
	})
}

// WithRetry overlays the non-zero fields of r on the retry configuration.
func WithRetry(r RetryConfig) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Retry.overlay(r)
		return nil
	})
}

// WithRetryPolicy replaces the backoff policy built from RetryConfig.
// RetryConfig.MaxElapsed still applies.
func WithRetryPolicy(p RetryPolicy) Option {
	return optionFunc(func(s *settings) error {
		if p == nil {
			return &ConfigError{Field: "RetryPolicy", Reason: "must not be nil"}
		}
		s.retry = p
		return nil
	})
}

// WithAutoRegister enables or disables registration on Start. Disable it
// for clients used only for discovery, KV or configuration.
func WithAutoRegister(enabled bool) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Lifecycle.AutoRegister = Bool(enabled)
		return nil
	})
}

// WithFailFast makes Start fail when the service cannot be registered
// within the start timeout. See LifecycleConfig.FailFast.
func WithFailFast(enabled bool) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Lifecycle.FailFast = enabled
		return nil
	})
}

// WithShutdownTimeout bounds deregistration during Stop and Run.
func WithShutdownTimeout(d time.Duration) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.Lifecycle.ShutdownTimeout = d
		return nil
	})
}

// WithLogger sets the structured logger. The default is slog.Default().
// A nil logger silences ConsulX.
func WithLogger(l *slog.Logger) Option {
	return optionFunc(func(s *settings) error {
		if l == nil {
			l = slog.New(slog.DiscardHandler)
		}
		s.logger = l
		return nil
	})
}

// WithMetrics sets the metrics sink. The default discards measurements.
func WithMetrics(m Metrics) Option {
	return optionFunc(func(s *settings) error {
		if m == nil {
			return &ConfigError{Field: "Metrics", Reason: "must not be nil"}
		}
		s.metrics = m
		return nil
	})
}

// newSettings applies the environment layer, then every option, then
// defaults, and validates the result.
func newSettings(lookup func(string) (string, bool), opts []Option) (*settings, error) {
	env, err := configFromLookup(lookup)
	if err != nil {
		return nil, err
	}
	s := &settings{cfg: env}
	var errs []error
	for _, o := range opts {
		if o == nil {
			continue
		}
		if err := o.apply(s); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	s.cfg.applyDefaults()
	if err := s.cfg.validate(); err != nil {
		return nil, err
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	if s.metrics == nil {
		s.metrics = noopMetrics{}
	}
	return s, nil
}
