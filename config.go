package consulx

import (
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Config is the complete, serialisable ConsulX configuration.
//
// Config implements Option, so it can be passed to New alongside functional
// options. Layers are applied in this order, later layers overriding only the
// fields they set (non-zero values):
//
//  1. built-in defaults (see the Default* constants),
//  2. configuration file (LoadConfig),
//  3. environment variables (ConfigFromEnv),
//  4. Config values passed to New,
//  5. functional options, in argument order.
//
// Because Go cannot tell "unset" from "zero", fields whose zero value is
// meaningful are pointers (for example LifecycleConfig.AutoRegister) or use a
// negative value to disable a feature (for example
// HealthConfig.DeregisterCriticalServiceAfter).
type Config struct {
	Consul    ConsulConfig    `json:"consul" yaml:"consul"`
	Service   ServiceConfig   `json:"service" yaml:"service"`
	Health    HealthConfig    `json:"health" yaml:"health"`
	Retry     RetryConfig     `json:"retry" yaml:"retry"`
	Lifecycle LifecycleConfig `json:"lifecycle" yaml:"lifecycle"`
	KV        KVConfig        `json:"kv" yaml:"kv"`
}

// ConsulConfig describes how to reach the Consul agent.
type ConsulConfig struct {
	// Address of the agent: "host:port", "http://host:port",
	// "https://host:port" or "unix:///path". Default DefaultConsulAddress.
	Address string `json:"address" yaml:"address"`
	// Scheme is "http" or "https". An https:// Address implies "https".
	Scheme string `json:"scheme" yaml:"scheme"`
	// Datacenter used for every request. Empty means the agent's datacenter.
	Datacenter string `json:"datacenter" yaml:"datacenter"`
	// Namespace is an Enterprise feature. Leave empty on Community Edition.
	Namespace string `json:"namespace" yaml:"namespace"`
	// Partition is an Enterprise feature. Leave empty on Community Edition.
	Partition string `json:"partition" yaml:"partition"`
	// Token is the ACL token sent with every request.
	Token Secret `json:"token" yaml:"token"`
	// TokenFile is read once at construction and wins over Token.
	TokenFile string `json:"tokenFile" yaml:"tokenFile"`
	// HTTPAuth is "user:password" for HTTP basic auth in front of Consul.
	HTTPAuth Secret `json:"httpAuth" yaml:"httpAuth"`
	// TLS configures transport security towards the agent.
	TLS TLSConfig `json:"tls" yaml:"tls"`
	// DialTimeout bounds TCP connection establishment. Default DefaultDialTimeout.
	DialTimeout time.Duration `json:"dialTimeout" yaml:"dialTimeout"`
	// RequestTimeout bounds every non-blocking request made by ConsulX.
	// Default DefaultRequestTimeout.
	RequestTimeout time.Duration `json:"requestTimeout" yaml:"requestTimeout"`
	// WaitTime is the server-side wait of blocking queries used by watches.
	// Consul caps it at 10 minutes. Default DefaultWaitTime.
	WaitTime time.Duration `json:"waitTime" yaml:"waitTime"`
}

// TLSConfig configures TLS towards the Consul agent. Files and PEM values
// are alternatives; PEM values win when both are set.
type TLSConfig struct {
	// Enabled forces https even when Address has no scheme.
	Enabled  bool   `json:"enabled" yaml:"enabled"`
	CAFile   string `json:"caFile" yaml:"caFile"`
	CAPath   string `json:"caPath" yaml:"caPath"`
	CAPEM    []byte `json:"-" yaml:"-"`
	CertFile string `json:"certFile" yaml:"certFile"`
	KeyFile  string `json:"keyFile" yaml:"keyFile"`
	CertPEM  []byte `json:"-" yaml:"-"`
	KeyPEM   []byte `json:"-" yaml:"-"`
	// ServerName overrides the name used for SNI and certificate verification.
	ServerName string `json:"serverName" yaml:"serverName"`
	// InsecureSkipVerify disables certificate verification. It makes the
	// connection vulnerable to interception; use only in local development.
	// ConsulX logs a warning whenever it is enabled.
	InsecureSkipVerify bool `json:"insecureSkipVerify" yaml:"insecureSkipVerify"`
}

// IDStrategy selects how a service ID is generated when none is configured.
type IDStrategy string

const (
	// IDHostnamePort generates "<name>-<hostname>-<port>". It is unique per
	// instance and stable across restarts, so a restarted process replaces
	// its own registration instead of leaving an orphan. Default.
	IDHostnamePort IDStrategy = "hostname-port"
	// IDRandom generates "<name>-<uuid>", unique on every start.
	IDRandom IDStrategy = "random"
)

// KVConfig configures distributed configuration read from Consul KV (see
// the kvconfig package). The layout is compatible with Spring Cloud Consul.
type KVConfig struct {
	// Name is the application context. Default Service.Name.
	Name string `json:"name" yaml:"name"`
	// Profiles are the active profiles, lowest precedence first. Default
	// [Service.Environment] when an environment is set.
	Profiles []string `json:"profiles" yaml:"profiles"`
	// Prefix is the root folder. Default "config".
	Prefix string `json:"prefix" yaml:"prefix"`
	// DefaultContext is the folder shared by every application.
	// Default "application".
	DefaultContext string `json:"defaultContext" yaml:"defaultContext"`
	// ProfileSeparator joins a context and a profile. Default ",".
	ProfileSeparator string `json:"profileSeparator" yaml:"profileSeparator"`
	// Format is "keyvalue" (default), "yaml" or "json".
	Format string `json:"format" yaml:"format"`
	// DataKey holds the document for yaml and json. Default "data".
	DataKey string `json:"dataKey" yaml:"dataKey"`
	// ErrorUnused reports keys that match no field, to catch typos.
	ErrorUnused bool `json:"errorUnused" yaml:"errorUnused"`
}

// ServiceConfig describes the service registered in Consul.
type ServiceConfig struct {
	// Name is the logical service name. Required when auto-registration is on.
	Name string `json:"name" yaml:"name"`
	// ID is the instance ID. Generated with IDStrategy when empty.
	ID         string     `json:"id" yaml:"id"`
	IDStrategy IDStrategy `json:"idStrategy" yaml:"idStrategy"`
	// Address registered for the instance. Resolved automatically when empty;
	// see AddressResolver. Wildcard addresses are always rejected.
	Address string `json:"address" yaml:"address"`
	// AddressEnv names an environment variable holding the address, for
	// example "POD_IP" populated by the Kubernetes Downward API.
	AddressEnv string `json:"addressEnv" yaml:"addressEnv"`
	// AllowLoopback permits registering a loopback address. Useful only when
	// the agent and every consumer run on the same host.
	AllowLoopback bool `json:"allowLoopback" yaml:"allowLoopback"`
	// PreferIPv6 makes automatic resolution pick IPv6 addresses first.
	// IPv6 service addresses require Consul >= 1.22.
	PreferIPv6 bool `json:"preferIPv6" yaml:"preferIPv6"`
	// Port registered for the instance. Taken from the http.Server when empty.
	Port int `json:"port" yaml:"port"`
	// Ports declares named ports (multi-port services, Consul >= 1.22).
	Ports []ServicePort `json:"ports" yaml:"ports"`
	// Scheme is "http" or "https", published as the "secure" metadata key.
	// Defaults to "https" when the http.Server has a TLSConfig.
	Scheme string            `json:"scheme" yaml:"scheme"`
	Tags   []string          `json:"tags" yaml:"tags"`
	Meta   map[string]string `json:"meta" yaml:"meta"`
	// DisableAutoMeta turns off automatically generated metadata.
	DisableAutoMeta bool `json:"disableAutoMeta" yaml:"disableAutoMeta"`
	// Version, Environment and Zone are published as metadata when set.
	Version     string `json:"version" yaml:"version"`
	Environment string `json:"environment" yaml:"environment"`
	Zone        string `json:"zone" yaml:"zone"`
	// TaggedAddresses maps names such as "lan" and "wan" to addresses.
	TaggedAddresses   map[string]TaggedAddress `json:"taggedAddresses" yaml:"taggedAddresses"`
	Weights           *Weights                 `json:"weights" yaml:"weights"`
	EnableTagOverride bool                     `json:"enableTagOverride" yaml:"enableTagOverride"`
	// Namespace and Partition override ConsulConfig for the registration.
	// Enterprise features.
	Namespace string `json:"namespace" yaml:"namespace"`
	Partition string `json:"partition" yaml:"partition"`
}

// ServicePort is one named port of a multi-port service.
type ServicePort struct {
	Name    string `json:"name" yaml:"name"`
	Port    int    `json:"port" yaml:"port"`
	Default bool   `json:"default" yaml:"default"`
}

// TaggedAddress is an alternative address for the service.
type TaggedAddress struct {
	Address string `json:"address" yaml:"address"`
	Port    int    `json:"port" yaml:"port"`
}

// Weights are the DNS SRV weights Consul uses while the instance is passing
// or warning. Client-side weighted load balancing uses them too.
type Weights struct {
	Passing int `json:"passing" yaml:"passing"`
	Warning int `json:"warning" yaml:"warning"`
}

// CheckType selects the Consul health check registered with the service.
type CheckType string

const (
	// CheckAuto registers an HTTP check when a check path is known (health
	// endpoints injected, or CheckPath set) and a TTL check otherwise.
	CheckAuto CheckType = "auto"
	CheckHTTP CheckType = "http"
	CheckTCP  CheckType = "tcp"
	CheckTTL  CheckType = "ttl"
	CheckGRPC CheckType = "grpc"
	// CheckNone registers no check. Consul then considers the instance
	// passing for as long as it is registered, which hides failures.
	CheckNone CheckType = "none"
)

// HealthEndpoints are the paths served by the injected health handler.
// When health endpoints are enabled and all fields are empty, the defaults
// are used; otherwise only the non-empty paths are served.
type HealthEndpoints struct {
	// Health reports the aggregate status of every component.
	Health string `json:"health" yaml:"health"`
	// Live reports only that the process is running. It never depends on
	// external components, so orchestrators do not restart a healthy
	// process because a dependency is down.
	Live string `json:"live" yaml:"live"`
	// Ready reports whether the instance should receive traffic.
	Ready string `json:"ready" yaml:"ready"`
}

// HealthConfig configures the injected health endpoints and the Consul check.
type HealthConfig struct {
	// Enabled injects the health endpoints into the http.Server handler.
	Enabled   bool            `json:"enabled" yaml:"enabled"`
	Endpoints HealthEndpoints `json:"endpoints" yaml:"endpoints"`
	// HideDetails omits per-component details from health responses.
	HideDetails bool `json:"hideDetails" yaml:"hideDetails"`
	// DegradedStatusCode is the HTTP status served for DEGRADED. Default 429,
	// which Consul's HTTP check maps to "warning". Kubernetes probes treat
	// 429 as a failure; use 200 if they share the endpoint.
	DegradedStatusCode int `json:"degradedStatusCode" yaml:"degradedStatusCode"`

	// Check is the Consul check type. Default CheckAuto.
	Check CheckType `json:"check" yaml:"check"`
	// CheckPath is the path Consul probes for HTTP checks. Defaults to the
	// Ready endpoint, then the Health endpoint.
	CheckPath string `json:"checkPath" yaml:"checkPath"`
	// Interval between Consul checks. Default DefaultCheckInterval.
	Interval time.Duration `json:"interval" yaml:"interval"`
	// Timeout of each check. Must not exceed Interval.
	// Default min(DefaultCheckTimeout, Interval).
	Timeout time.Duration `json:"timeout" yaml:"timeout"`
	// TTL of TTL checks. ConsulX heartbeats every TTL/3. Default DefaultCheckTTL.
	TTL time.Duration `json:"ttl" yaml:"ttl"`
	// DeregisterCriticalServiceAfter makes Consul remove the instance after
	// its check has been critical this long; it protects against crashes,
	// SIGKILL and host loss. Consul's minimum is one minute. A negative
	// value disables it. Default DefaultDeregisterCriticalAfter.
	DeregisterCriticalServiceAfter time.Duration `json:"deregisterCriticalServiceAfter" yaml:"deregisterCriticalServiceAfter"`

	// HTTP check options.
	Method        string              `json:"method" yaml:"method"`
	Header        map[string][]string `json:"header" yaml:"header"`
	Body          string              `json:"body" yaml:"body"`
	TLSServerName string              `json:"tlsServerName" yaml:"tlsServerName"`
	// TLSSkipVerify disables certificate verification of HTTPS, gRPC+TLS and
	// TCP+TLS checks performed by the agent.
	TLSSkipVerify bool `json:"tlsSkipVerify" yaml:"tlsSkipVerify"`
	// UseTLS enables TLS for TCP and gRPC checks.
	UseTLS bool `json:"useTLS" yaml:"useTLS"`
	// GRPCService is the service name sent in gRPC health requests.
	GRPCService string `json:"grpcService" yaml:"grpcService"`

	// Flap damping, as defined by Consul.
	SuccessBeforePassing   int `json:"successBeforePassing" yaml:"successBeforePassing"`
	FailuresBeforeWarning  int `json:"failuresBeforeWarning" yaml:"failuresBeforeWarning"`
	FailuresBeforeCritical int `json:"failuresBeforeCritical" yaml:"failuresBeforeCritical"`
}

// RetryConfig configures exponential backoff for every retried operation.
type RetryConfig struct {
	// InitialDelay before the first retry. Default DefaultRetryInitialDelay.
	InitialDelay time.Duration `json:"initialDelay" yaml:"initialDelay"`
	// MaxDelay caps the delay between attempts. Default DefaultRetryMaxDelay.
	MaxDelay time.Duration `json:"maxDelay" yaml:"maxDelay"`
	// Multiplier grows the delay after each attempt. Default DefaultRetryMultiplier.
	Multiplier float64 `json:"multiplier" yaml:"multiplier"`
	// DisableJitter turns off full jitter. Jitter spreads reconnect attempts
	// of many instances and should normally stay on.
	DisableJitter bool `json:"disableJitter" yaml:"disableJitter"`
	// MaxAttempts limits attempts; 0 means unlimited.
	MaxAttempts int `json:"maxAttempts" yaml:"maxAttempts"`
	// MaxElapsed limits the total time spent retrying; 0 means unlimited.
	MaxElapsed time.Duration `json:"maxElapsed" yaml:"maxElapsed"`
}

// LifecycleConfig controls start and shutdown behaviour.
type LifecycleConfig struct {
	// AutoRegister registers the service on Start. Default true.
	AutoRegister *bool `json:"autoRegister" yaml:"autoRegister"`
	// DeregisterOnShutdown deregisters the service on Stop. Default true.
	DeregisterOnShutdown *bool `json:"deregisterOnShutdown" yaml:"deregisterOnShutdown"`
	// FailFast makes Start return an error when the service cannot be
	// registered within StartTimeout. When false (default) Start returns
	// immediately and registration is retried in the background, so a
	// Consul outage does not prevent a healthy service from starting.
	FailFast bool `json:"failFast" yaml:"failFast"`
	// StartTimeout bounds Start when FailFast is true. Default DefaultStartTimeout.
	StartTimeout time.Duration `json:"startTimeout" yaml:"startTimeout"`
	// ShutdownTimeout bounds deregistration during Stop and Run.
	// Default DefaultShutdownTimeout.
	ShutdownTimeout time.Duration `json:"shutdownTimeout" yaml:"shutdownTimeout"`
}

// Defaults. The reason behind each value is documented in
// docs/architecture.md.
const (
	DefaultConsulAddress           = "127.0.0.1:8500"
	DefaultDialTimeout             = 5 * time.Second
	DefaultRequestTimeout          = 10 * time.Second
	DefaultWaitTime                = 5 * time.Minute
	MaxWaitTime                    = 10 * time.Minute
	DefaultHealthPath              = "/health"
	DefaultLivePath                = "/health/live"
	DefaultReadyPath               = "/health/ready"
	DefaultCheckInterval           = 10 * time.Second
	DefaultCheckTimeout            = 5 * time.Second
	DefaultCheckTTL                = 30 * time.Second
	DefaultDeregisterCriticalAfter = time.Minute
	MinDeregisterCriticalAfter     = time.Minute
	DefaultRetryInitialDelay       = 500 * time.Millisecond
	DefaultRetryMaxDelay           = 30 * time.Second
	DefaultRetryMultiplier         = 2.0
	DefaultStartTimeout            = 30 * time.Second
	DefaultShutdownTimeout         = 10 * time.Second
)

// Bool returns a pointer to b, for the optional boolean fields of Config.
func Bool(b bool) *bool { return &b }

// overlay copies every non-zero field of src onto c.
func (c *Config) overlay(src Config) {
	c.Consul.overlay(src.Consul)
	c.Service.overlay(src.Service)
	c.Health.overlay(src.Health)
	c.Retry.overlay(src.Retry)
	c.Lifecycle.overlay(src.Lifecycle)
	c.KV.overlay(src.KV)
}

func (c *ConsulConfig) overlay(s ConsulConfig) {
	set(&c.Address, s.Address)
	set(&c.Scheme, s.Scheme)
	set(&c.Datacenter, s.Datacenter)
	set(&c.Namespace, s.Namespace)
	set(&c.Partition, s.Partition)
	set(&c.Token, s.Token)
	set(&c.TokenFile, s.TokenFile)
	set(&c.HTTPAuth, s.HTTPAuth)
	c.TLS.overlay(s.TLS)
	set(&c.DialTimeout, s.DialTimeout)
	set(&c.RequestTimeout, s.RequestTimeout)
	set(&c.WaitTime, s.WaitTime)
}

func (c *TLSConfig) overlay(s TLSConfig) {
	set(&c.Enabled, s.Enabled)
	set(&c.CAFile, s.CAFile)
	set(&c.CAPath, s.CAPath)
	setSlice(&c.CAPEM, s.CAPEM)
	set(&c.CertFile, s.CertFile)
	set(&c.KeyFile, s.KeyFile)
	setSlice(&c.CertPEM, s.CertPEM)
	setSlice(&c.KeyPEM, s.KeyPEM)
	set(&c.ServerName, s.ServerName)
	set(&c.InsecureSkipVerify, s.InsecureSkipVerify)
}

func (c *ServiceConfig) overlay(s ServiceConfig) {
	set(&c.Name, s.Name)
	set(&c.ID, s.ID)
	set(&c.IDStrategy, s.IDStrategy)
	set(&c.Address, s.Address)
	set(&c.AddressEnv, s.AddressEnv)
	set(&c.AllowLoopback, s.AllowLoopback)
	set(&c.PreferIPv6, s.PreferIPv6)
	set(&c.Port, s.Port)
	setSlice(&c.Ports, s.Ports)
	set(&c.Scheme, s.Scheme)
	setSlice(&c.Tags, s.Tags)
	c.Meta = mergeMap(c.Meta, s.Meta)
	set(&c.DisableAutoMeta, s.DisableAutoMeta)
	set(&c.Version, s.Version)
	set(&c.Environment, s.Environment)
	set(&c.Zone, s.Zone)
	c.TaggedAddresses = mergeMap(c.TaggedAddresses, s.TaggedAddresses)
	if s.Weights != nil {
		w := *s.Weights
		c.Weights = &w
	}
	set(&c.EnableTagOverride, s.EnableTagOverride)
	set(&c.Namespace, s.Namespace)
	set(&c.Partition, s.Partition)
}

func (c *HealthConfig) overlay(s HealthConfig) {
	set(&c.Enabled, s.Enabled)
	if s.Endpoints != (HealthEndpoints{}) {
		c.Endpoints = s.Endpoints
	}
	set(&c.HideDetails, s.HideDetails)
	set(&c.DegradedStatusCode, s.DegradedStatusCode)
	set(&c.Check, s.Check)
	set(&c.CheckPath, s.CheckPath)
	set(&c.Interval, s.Interval)
	set(&c.Timeout, s.Timeout)
	set(&c.TTL, s.TTL)
	set(&c.DeregisterCriticalServiceAfter, s.DeregisterCriticalServiceAfter)
	set(&c.Method, s.Method)
	c.Header = mergeMap(c.Header, s.Header)
	set(&c.Body, s.Body)
	set(&c.TLSServerName, s.TLSServerName)
	set(&c.TLSSkipVerify, s.TLSSkipVerify)
	set(&c.UseTLS, s.UseTLS)
	set(&c.GRPCService, s.GRPCService)
	set(&c.SuccessBeforePassing, s.SuccessBeforePassing)
	set(&c.FailuresBeforeWarning, s.FailuresBeforeWarning)
	set(&c.FailuresBeforeCritical, s.FailuresBeforeCritical)
}

func (c *RetryConfig) overlay(s RetryConfig) {
	set(&c.InitialDelay, s.InitialDelay)
	set(&c.MaxDelay, s.MaxDelay)
	set(&c.Multiplier, s.Multiplier)
	set(&c.DisableJitter, s.DisableJitter)
	set(&c.MaxAttempts, s.MaxAttempts)
	set(&c.MaxElapsed, s.MaxElapsed)
}

func (c *KVConfig) overlay(s KVConfig) {
	set(&c.Name, s.Name)
	setSlice(&c.Profiles, s.Profiles)
	set(&c.Prefix, s.Prefix)
	set(&c.DefaultContext, s.DefaultContext)
	set(&c.ProfileSeparator, s.ProfileSeparator)
	set(&c.Format, s.Format)
	set(&c.DataKey, s.DataKey)
	set(&c.ErrorUnused, s.ErrorUnused)
}

func (c *LifecycleConfig) overlay(s LifecycleConfig) {
	if s.AutoRegister != nil {
		c.AutoRegister = Bool(*s.AutoRegister)
	}
	if s.DeregisterOnShutdown != nil {
		c.DeregisterOnShutdown = Bool(*s.DeregisterOnShutdown)
	}
	set(&c.FailFast, s.FailFast)
	set(&c.StartTimeout, s.StartTimeout)
	set(&c.ShutdownTimeout, s.ShutdownTimeout)
}

// set assigns v to *dst when v is not the zero value.
func set[T comparable](dst *T, v T) {
	var zero T
	if v != zero {
		*dst = v
	}
}

// setSlice replaces *dst with a copy of v when v is not nil.
func setSlice[S ~[]E, E any](dst *S, v S) {
	if v != nil {
		*dst = slices.Clone(v)
	}
}

// mergeMap returns dst with every key of src copied over it. The result never
// aliases src, so later mutations by the caller cannot leak into the Config.
func mergeMap[K comparable, V any](dst, src map[K]V) map[K]V {
	if src == nil {
		return dst
	}
	out := make(map[K]V, len(dst)+len(src))
	maps.Copy(out, dst)
	maps.Copy(out, src)
	return out
}

// applyDefaults fills every unset field with its default. It never
// overrides a value set by any layer.
func (c *Config) applyDefaults() {
	cc := &c.Consul
	if cc.Address == "" {
		cc.Address = DefaultConsulAddress
	}
	if cc.DialTimeout == 0 {
		cc.DialTimeout = DefaultDialTimeout
	}
	if cc.RequestTimeout == 0 {
		cc.RequestTimeout = DefaultRequestTimeout
	}
	if cc.WaitTime == 0 {
		cc.WaitTime = DefaultWaitTime
	}

	if c.Service.IDStrategy == "" {
		c.Service.IDStrategy = IDHostnamePort
	}

	h := &c.Health
	if h.Enabled && h.Endpoints == (HealthEndpoints{}) {
		h.Endpoints = HealthEndpoints{Health: DefaultHealthPath, Live: DefaultLivePath, Ready: DefaultReadyPath}
	}
	if h.Check == "" {
		h.Check = CheckAuto
	}
	if h.CheckPath == "" && h.Enabled {
		h.CheckPath = cmpOr(h.Endpoints.Ready, h.Endpoints.Health)
	}
	if h.Check == CheckAuto {
		if h.CheckPath != "" {
			h.Check = CheckHTTP
		} else {
			h.Check = CheckTTL
		}
	}
	if h.Interval == 0 {
		h.Interval = DefaultCheckInterval
	}
	if h.Timeout == 0 {
		h.Timeout = min(DefaultCheckTimeout, h.Interval)
	}
	if h.TTL == 0 {
		h.TTL = DefaultCheckTTL
	}
	if h.DeregisterCriticalServiceAfter == 0 {
		h.DeregisterCriticalServiceAfter = DefaultDeregisterCriticalAfter
	}

	r := &c.Retry
	if r.InitialDelay == 0 {
		r.InitialDelay = DefaultRetryInitialDelay
	}
	if r.MaxDelay == 0 {
		r.MaxDelay = DefaultRetryMaxDelay
	}
	if r.Multiplier == 0 {
		r.Multiplier = DefaultRetryMultiplier
	}

	l := &c.Lifecycle
	if l.AutoRegister == nil {
		l.AutoRegister = Bool(true)
	}
	if l.DeregisterOnShutdown == nil {
		l.DeregisterOnShutdown = Bool(true)
	}
	if l.StartTimeout == 0 {
		l.StartTimeout = DefaultStartTimeout
	}
	if l.ShutdownTimeout == 0 {
		l.ShutdownTimeout = DefaultShutdownTimeout
	}
}

func cmpOr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// validate reports every problem of a defaulted Config. Checks that need
// runtime information (resolved address and port) happen at Start.
func (c *Config) validate() error {
	var errs configErrors
	add := func(field, reason string) { errs = append(errs, &ConfigError{Field: field, Reason: reason}) }

	cc := c.Consul
	if err := validateConsulAddress(cc.Address); err != "" {
		add("Consul.Address", err)
	}
	if cc.Scheme != "" && cc.Scheme != "http" && cc.Scheme != "https" {
		add("Consul.Scheme", `must be "http" or "https"`)
	}
	if cc.DialTimeout < 0 {
		add("Consul.DialTimeout", "must not be negative")
	}
	if cc.RequestTimeout < 0 {
		add("Consul.RequestTimeout", "must not be negative")
	}
	if cc.WaitTime < 0 || cc.WaitTime > MaxWaitTime {
		add("Consul.WaitTime", "must be between 0 and 10m (Consul's maximum)")
	}
	if (cc.TLS.CertFile == "") != (cc.TLS.KeyFile == "") {
		add("Consul.TLS", "CertFile and KeyFile must be set together")
	}
	if (len(cc.TLS.CertPEM) == 0) != (len(cc.TLS.KeyPEM) == 0) {
		add("Consul.TLS", "CertPEM and KeyPEM must be set together")
	}
	if a := cc.HTTPAuth.Reveal(); a != "" && !strings.Contains(a, ":") {
		add("Consul.HTTPAuth", `must have the form "user:password"`)
	}

	s := c.Service
	if *c.Lifecycle.AutoRegister && s.Name == "" {
		add("Service.Name", "required when auto-registration is enabled; disable it with WithAutoRegister(false) for discovery-only clients")
	}
	if s.Name != "" && strings.ContainsAny(s.Name, "/ \t\r\n") {
		add("Service.Name", "must not contain '/' or whitespace")
	}
	if s.ID != "" && strings.ContainsAny(s.ID, "/ \t\r\n") {
		add("Service.ID", "must not contain '/' or whitespace")
	}
	if s.IDStrategy != IDHostnamePort && s.IDStrategy != IDRandom {
		add("Service.IDStrategy", `must be "hostname-port" or "random"`)
	}
	if s.Port < 0 || s.Port > 65535 {
		add("Service.Port", "must be between 0 and 65535")
	}
	if s.Scheme != "" && s.Scheme != "http" && s.Scheme != "https" {
		add("Service.Scheme", `must be "http" or "https"`)
	}
	validatePorts(s.Ports, add)
	if len(s.Ports) > 0 && s.Port != 0 {
		add("Service.Ports", "Port and Ports are mutually exclusive in Consul; mark the main port as Default instead")
	}
	validateMeta(s.Meta, add)
	if s.Weights != nil && (s.Weights.Passing < 1 || s.Weights.Warning < 0) {
		add("Service.Weights", "Passing must be >= 1 and Warning >= 0")
	}

	h := c.Health
	switch h.Check {
	case CheckHTTP, CheckTCP, CheckTTL, CheckGRPC, CheckNone:
	default:
		add("Health.Check", "must be one of auto, http, tcp, ttl, grpc, none")
	}
	if h.Check == CheckHTTP && h.CheckPath == "" {
		add("Health.CheckPath", "required for HTTP checks when health endpoints are not enabled")
	}
	if h.CheckPath != "" && !strings.HasPrefix(h.CheckPath, "/") {
		add("Health.CheckPath", "must start with '/'")
	}
	validateEndpoints(h, add)
	if d := h.DegradedStatusCode; d != 0 && (d < 200 || d > 599) {
		add("Health.DegradedStatusCode", "must be an HTTP status code between 200 and 599")
	}
	if h.Interval <= 0 {
		add("Health.Interval", "must be positive")
	}
	if h.Timeout <= 0 || h.Timeout > h.Interval {
		add("Health.Timeout", "must be positive and not exceed Interval")
	}
	if h.TTL < time.Second {
		add("Health.TTL", "must be at least 1s")
	}
	if d := h.DeregisterCriticalServiceAfter; d > 0 && d < MinDeregisterCriticalAfter {
		add("Health.DeregisterCriticalServiceAfter", "must be at least 1m (Consul's minimum), or negative to disable")
	}
	if h.SuccessBeforePassing < 0 || h.FailuresBeforeWarning < 0 || h.FailuresBeforeCritical < 0 {
		add("Health", "flap damping counters must not be negative")
	}

	r := c.Retry
	if r.InitialDelay < 0 || r.MaxDelay < r.InitialDelay {
		add("Retry", "InitialDelay must not be negative and MaxDelay must be >= InitialDelay")
	}
	if r.Multiplier < 1 {
		add("Retry.Multiplier", "must be >= 1")
	}
	if r.MaxAttempts < 0 || r.MaxElapsed < 0 {
		add("Retry", "MaxAttempts and MaxElapsed must not be negative")
	}

	switch c.KV.Format {
	case "", "keyvalue", "yaml", "json":
	default:
		add("KV.Format", `must be "keyvalue", "yaml" or "json"`)
	}

	l := c.Lifecycle
	if l.StartTimeout < 0 || l.ShutdownTimeout < 0 {
		add("Lifecycle", "timeouts must not be negative")
	}
	return errs.errOrNil()
}

func validateConsulAddress(addr string) string {
	scheme, rest, found := strings.Cut(addr, "://")
	if !found {
		rest, scheme = addr, ""
	}
	switch scheme {
	case "", "http", "https":
	case "unix":
		if rest == "" {
			return "unix socket path is empty"
		}
		return ""
	default:
		return "unsupported scheme " + strings.ToValidUTF8(scheme, "?")
	}
	host := rest
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		host = rest[:i] // a path prefix is allowed for reverse proxies
	}
	if host == "" {
		return "host is empty"
	}
	if _, err := url.Parse("http://" + host); err != nil {
		return "invalid host"
	}
	return ""
}

func validatePorts(ports []ServicePort, add func(string, string)) {
	if len(ports) == 0 {
		return
	}
	names := make(map[string]bool, len(ports))
	defaults := 0
	for _, p := range ports {
		if p.Name == "" || names[p.Name] {
			add("Service.Ports", "every port needs a unique, non-empty name")
			return
		}
		names[p.Name] = true
		if p.Port < 1 || p.Port > 65535 {
			add("Service.Ports", "port "+p.Name+" must be between 1 and 65535")
		}
		if p.Default {
			defaults++
		}
	}
	if defaults != 1 {
		add("Service.Ports", "exactly one port must be marked Default")
	}
}

// Consul service metadata limits (agent/structs: validateMetaPair).
const (
	metaMaxPairs       = 64
	metaKeyMaxLength   = 128
	metaValueMaxLength = 512
	metaReservedPrefix = "consul-"
)

// autoMetaReserve leaves room for the metadata ConsulX adds itself.
const autoMetaReserve = 8

func validateMeta(meta map[string]string, add func(string, string)) {
	if len(meta) > metaMaxPairs-autoMetaReserve {
		add("Service.Meta", "at most 56 pairs (Consul allows 64; ConsulX reserves 8 for automatic metadata)")
	}
	for k, v := range meta {
		switch {
		case k == "" || len(k) > metaKeyMaxLength:
			add("Service.Meta", "keys must have 1 to 128 characters")
		case strings.HasPrefix(k, metaReservedPrefix):
			add("Service.Meta", "key "+k+` uses the prefix "consul-", reserved by Consul`)
		case strings.IndexFunc(k, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-'
		}) >= 0:
			add("Service.Meta", "key "+k+" may only contain A-Z, a-z, 0-9, '_' and '-'")
		}
		if len(v) > metaValueMaxLength {
			add("Service.Meta", "value of "+k+" exceeds 512 characters")
		}
	}
}

func validateEndpoints(h HealthConfig, add func(string, string)) {
	seen := map[string]bool{}
	for _, p := range []struct{ field, path string }{
		{"Health.Endpoints.Health", h.Endpoints.Health},
		{"Health.Endpoints.Live", h.Endpoints.Live},
		{"Health.Endpoints.Ready", h.Endpoints.Ready},
	} {
		if p.path == "" {
			continue
		}
		if !strings.HasPrefix(p.path, "/") {
			add(p.field, "must start with '/'")
		}
		if seen[p.path] {
			add(p.field, "duplicates another health endpoint")
		}
		seen[p.path] = true
	}
}
