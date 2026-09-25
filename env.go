package consulx

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment variables read by ConfigFromEnv.
//
// Consul connection settings use the official CONSUL_* names understood by
// the consul CLI and the official Go client, so existing deployments work
// unchanged. Service settings use the CONSULX_ prefix.
const (
	EnvConsulAddr      = "CONSUL_HTTP_ADDR"
	EnvConsulToken     = "CONSUL_HTTP_TOKEN"      // #nosec G101 -- variable name, not a credential
	EnvConsulTokenFile = "CONSUL_HTTP_TOKEN_FILE" // #nosec G101 -- variable name, not a credential
	EnvConsulAuth      = "CONSUL_HTTP_AUTH"
	EnvConsulSSL       = "CONSUL_HTTP_SSL"
	EnvConsulSSLVerify = "CONSUL_HTTP_SSL_VERIFY"
	EnvConsulCACert    = "CONSUL_CACERT"
	EnvConsulCAPath    = "CONSUL_CAPATH"
	EnvConsulCert      = "CONSUL_CLIENT_CERT"
	EnvConsulKey       = "CONSUL_CLIENT_KEY"
	EnvConsulTLSServer = "CONSUL_TLS_SERVER_NAME"
	EnvConsulNamespace = "CONSUL_NAMESPACE"
	EnvConsulPartition = "CONSUL_PARTITION"

	EnvDatacenter      = "CONSULX_DATACENTER"
	EnvServiceName     = "CONSULX_SERVICE_NAME"
	EnvServiceID       = "CONSULX_SERVICE_ID"
	EnvServiceAddress  = "CONSULX_SERVICE_ADDRESS"
	EnvServicePort     = "CONSULX_SERVICE_PORT"
	EnvServiceTags     = "CONSULX_SERVICE_TAGS"
	EnvServiceMeta     = "CONSULX_SERVICE_META"
	EnvServiceVersion  = "CONSULX_SERVICE_VERSION"
	EnvEnvironment     = "CONSULX_ENVIRONMENT"
	EnvZone            = "CONSULX_ZONE"
	EnvHealthEnabled   = "CONSULX_HEALTH_ENABLED"
	EnvHealthInterval  = "CONSULX_HEALTH_INTERVAL"
	EnvDeregisterAfter = "CONSULX_DEREGISTER_CRITICAL_AFTER"
	EnvFailFast        = "CONSULX_FAIL_FAST"
	EnvProfiles        = "CONSULX_PROFILES"
)

// ConfigFromEnv builds a Config layer from the process environment. Unset
// variables leave fields at their zero value, so the layer only overrides
// what the environment defines. Malformed values are reported, never ignored.
//
// New always applies this layer; call it directly only to inspect it.
func ConfigFromEnv() (Config, error) {
	return configFromLookup(os.LookupEnv)
}

func configFromLookup(lookup func(string) (string, bool)) (Config, error) {
	var (
		c    Config
		errs configErrors
	)
	get := func(k string) string {
		v, _ := lookup(k)
		return strings.TrimSpace(v)
	}
	parseBool := func(k string) (value, ok bool) {
		v := get(k)
		if v == "" {
			return false, false
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, &ConfigError{Field: k, Reason: "must be a boolean"})
			return false, false
		}
		return b, true
	}
	parseDuration := func(k string) time.Duration {
		v := get(k)
		if v == "" {
			return 0
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			errs = append(errs, &ConfigError{Field: k, Reason: "must be a duration such as 10s"})
		}
		return d
	}

	cc := &c.Consul
	cc.Address = get(EnvConsulAddr)
	cc.Token = Secret(get(EnvConsulToken))
	cc.TokenFile = get(EnvConsulTokenFile)
	cc.HTTPAuth = Secret(get(EnvConsulAuth))
	if b, ok := parseBool(EnvConsulSSL); ok && b {
		cc.Scheme = "https"
	}
	if b, ok := parseBool(EnvConsulSSLVerify); ok && !b {
		cc.TLS.InsecureSkipVerify = true
	}
	cc.TLS.CAFile = get(EnvConsulCACert)
	cc.TLS.CAPath = get(EnvConsulCAPath)
	cc.TLS.CertFile = get(EnvConsulCert)
	cc.TLS.KeyFile = get(EnvConsulKey)
	cc.TLS.ServerName = get(EnvConsulTLSServer)
	cc.Namespace = get(EnvConsulNamespace)
	cc.Partition = get(EnvConsulPartition)
	cc.Datacenter = get(EnvDatacenter)

	s := &c.Service
	s.Name = get(EnvServiceName)
	s.ID = get(EnvServiceID)
	s.Address = get(EnvServiceAddress)
	if v := get(EnvServicePort); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			errs = append(errs, &ConfigError{Field: EnvServicePort, Reason: "must be an integer"})
		}
		s.Port = p
	}
	if v := get(EnvServiceTags); v != "" {
		s.Tags = splitList(v)
	}
	if v := get(EnvServiceMeta); v != "" {
		m, err := parseMeta(v)
		if err != nil {
			errs = append(errs, &ConfigError{Field: EnvServiceMeta, Reason: err.Error()})
		}
		s.Meta = m
	}
	s.Version = get(EnvServiceVersion)
	s.Environment = get(EnvEnvironment)
	s.Zone = get(EnvZone)

	if b, ok := parseBool(EnvHealthEnabled); ok {
		c.Health.Enabled = b
	}
	c.Health.Interval = parseDuration(EnvHealthInterval)
	c.Health.DeregisterCriticalServiceAfter = parseDuration(EnvDeregisterAfter)
	if b, ok := parseBool(EnvFailFast); ok {
		c.Lifecycle.FailFast = b
	}
	if v := get(EnvProfiles); v != "" {
		c.KV.Profiles = splitList(v)
	}
	return c, errs.errOrNil()
}

// splitList splits a comma separated list, dropping empty items.
func splitList(v string) []string {
	var out []string
	for item := range strings.SplitSeq(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// parseMeta parses "k1=v1,k2=v2".
func parseMeta(v string) (map[string]string, error) {
	m := map[string]string{}
	for _, item := range splitList(v) {
		k, val, ok := strings.Cut(item, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, &metaFormatError{}
		}
		m[k] = strings.TrimSpace(val)
	}
	return m, nil
}

type metaFormatError struct{}

func (*metaFormatError) Error() string { return `must have the form "key=value,key=value"` }
