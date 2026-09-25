package consulx

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// noEnv is an empty environment, so tests never depend on the host.
func noEnv(string) (string, bool) { return "", false }

func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestDefaultsAreApplied(t *testing.T) {
	s, err := newSettings(noEnv, []Option{WithServiceName("orders")})
	if err != nil {
		t.Fatal(err)
	}
	c := s.cfg
	checks := map[string]bool{
		"address":      c.Consul.Address == DefaultConsulAddress,
		"request":      c.Consul.RequestTimeout == DefaultRequestTimeout,
		"wait":         c.Consul.WaitTime == DefaultWaitTime,
		"id strategy":  c.Service.IDStrategy == IDHostnamePort,
		"check ttl":    c.Health.Check == CheckTTL, // no endpoints injected
		"interval":     c.Health.Interval == DefaultCheckInterval,
		"timeout":      c.Health.Timeout == DefaultCheckTimeout,
		"dereg":        c.Health.DeregisterCriticalServiceAfter == DefaultDeregisterCriticalAfter,
		"retry":        c.Retry.InitialDelay == DefaultRetryInitialDelay && c.Retry.MaxDelay == DefaultRetryMaxDelay,
		"autoregister": *c.Lifecycle.AutoRegister,
		"dereg on":     *c.Lifecycle.DeregisterOnShutdown,
		"failfast":     !c.Lifecycle.FailFast,
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("default %q not applied: %+v", name, c)
		}
	}
}

func TestAutoHealthSelectsHTTPCheckOnReadyPath(t *testing.T) {
	s, err := newSettings(noEnv, []Option{WithServiceName("a"), WithAutoHealth()})
	if err != nil {
		t.Fatal(err)
	}
	h := s.cfg.Health
	if h.Check != CheckHTTP || h.CheckPath != DefaultReadyPath {
		t.Fatalf("check=%s path=%s", h.Check, h.CheckPath)
	}
	if h.Endpoints != (HealthEndpoints{DefaultHealthPath, DefaultLivePath, DefaultReadyPath}) {
		t.Fatalf("endpoints %+v", h.Endpoints)
	}
}

func TestCustomEndpointsAreServedExactly(t *testing.T) {
	s, err := newSettings(noEnv, []Option{WithServiceName("a"), WithHealthEndpoints(HealthEndpoints{Health: "/status"})})
	if err != nil {
		t.Fatal(err)
	}
	h := s.cfg.Health
	if h.Endpoints.Live != "" || h.Endpoints.Ready != "" || h.CheckPath != "/status" {
		t.Fatalf("unexpected health config %+v", h)
	}
}

func TestTimeoutDefaultNeverExceedsShortInterval(t *testing.T) {
	s, err := newSettings(noEnv, []Option{WithServiceName("a"), WithHealth(HealthConfig{Interval: 2 * time.Second})})
	if err != nil {
		t.Fatal(err)
	}
	if s.cfg.Health.Timeout != 2*time.Second {
		t.Fatalf("timeout %v", s.cfg.Health.Timeout)
	}
}

func TestPrecedenceEnvThenConfigThenOptions(t *testing.T) {
	env := envOf(map[string]string{
		EnvConsulAddr:     "env:8500",
		EnvServiceName:    "from-env",
		EnvDatacenter:     "dc-env",
		EnvServiceVersion: "1.0.0",
	})
	cfg := Config{
		Consul:  ConsulConfig{Address: "struct:8500"},
		Service: ServiceConfig{Name: "from-struct"},
	}
	s, err := newSettings(env, []Option{cfg, WithServiceName("from-option")})
	if err != nil {
		t.Fatal(err)
	}
	c := s.cfg
	if c.Consul.Address != "struct:8500" {
		t.Errorf("struct must override env: %s", c.Consul.Address)
	}
	if c.Service.Name != "from-option" {
		t.Errorf("option must override struct: %s", c.Service.Name)
	}
	if c.Consul.Datacenter != "dc-env" || c.Service.Version != "1.0.0" {
		t.Errorf("env values not overridden must survive: %+v", c)
	}
}

func TestOptionCanTurnOffBooleanSetByEarlierLayer(t *testing.T) {
	env := envOf(map[string]string{EnvFailFast: "true"})
	s, err := newSettings(env, []Option{WithServiceName("a"), WithFailFast(false)})
	if err != nil {
		t.Fatal(err)
	}
	if s.cfg.Lifecycle.FailFast {
		t.Fatal("WithFailFast(false) must win")
	}
}

func TestConfigZeroValuesDoNotOverride(t *testing.T) {
	first := Config{Service: ServiceConfig{Name: "a", Port: 8080}, Lifecycle: LifecycleConfig{AutoRegister: Bool(false)}}
	second := Config{Service: ServiceConfig{Port: 0}}
	s, err := newSettings(noEnv, []Option{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if s.cfg.Service.Port != 8080 || *s.cfg.Lifecycle.AutoRegister {
		t.Fatalf("zero values overrode earlier layer: %+v", s.cfg)
	}
}

func TestMetadataMergesAndDoesNotAlias(t *testing.T) {
	meta := map[string]string{"team": "payments"}
	s, err := newSettings(noEnv, []Option{
		Config{Service: ServiceConfig{Name: "a", Meta: meta}},
		WithMetadata(map[string]string{"version": "1.2.3"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	meta["team"] = "mutated"
	got := s.cfg.Service.Meta
	if got["team"] != "payments" || got["version"] != "1.2.3" {
		t.Fatalf("meta %v", got)
	}
}

func TestValidationReportsEveryProblem(t *testing.T) {
	_, err := newSettings(noEnv, []Option{
		WithHealth(HealthConfig{Interval: time.Second, Timeout: 2 * time.Second, DeregisterCriticalServiceAfter: 10 * time.Second}),
		WithServicePort(70000),
	})
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("want ErrInvalidConfiguration, got %v", err)
	}
	fields := map[string]bool{}
	var target *ConfigError
	if !errors.As(err, &target) {
		t.Fatal("errors.As must find a *ConfigError")
	}
	var all configErrors
	if !errors.As(err, &all) {
		t.Fatal("errors.As must find the aggregated configErrors")
	}
	for _, e := range all {
		fields[e.Field] = true
	}
	for _, f := range []string{"Service.Name", "Service.Port", "Health.Timeout", "Health.DeregisterCriticalServiceAfter"} {
		if !fields[f] {
			t.Errorf("missing problem for %s in: %v", f, err)
		}
	}
}

func TestValidationCases(t *testing.T) {
	base := []Option{WithServiceName("svc")}
	tests := []struct {
		name  string
		opt   Option
		field string
	}{
		{"bad scheme", WithConsulAddress("ftp://x"), "Consul.Address"},
		{"empty host", WithConsulAddress("http://"), "Consul.Address"},
		{"name slash", WithServiceName("a/b"), "Service.Name"},
		{"http check without path", WithHealth(HealthConfig{Check: CheckHTTP}), "Health.CheckPath"},
		{"unknown check", WithHealth(HealthConfig{Check: "icmp"}), "Health.Check"},
		{"relative endpoint", WithHealthEndpoints(HealthEndpoints{Health: "health"}), "Health.Endpoints.Health"},
		{"duplicate endpoint", WithHealthEndpoints(HealthEndpoints{Health: "/h", Ready: "/h"}), "Health.Endpoints.Ready"},
		{"cert without key", WithTLS(TLSConfig{CertFile: "c.pem"}), "Consul.TLS"},
		{"multiplier", WithRetry(RetryConfig{Multiplier: 0.5}), "Retry.Multiplier"},
		{"ports without default", Config{Service: ServiceConfig{Ports: []ServicePort{{Name: "a", Port: 1}}}}, "Service.Ports"},
		{"port and ports", Config{Service: ServiceConfig{Port: 80, Ports: []ServicePort{{Name: "a", Port: 1, Default: true}}}}, "Service.Ports"},
		{"weights", Config{Service: ServiceConfig{Weights: &Weights{Passing: 0}}}, "Service.Weights"},
		{"basic auth", Config{Consul: ConsulConfig{HTTPAuth: "nocolon"}}, "Consul.HTTPAuth"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newSettings(noEnv, append(base, tt.opt))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("error does not mention %s: %v", tt.field, err)
			}
		})
	}
}

func TestDiscoveryOnlyClientNeedsNoName(t *testing.T) {
	if _, err := newSettings(noEnv, []Option{WithAutoRegister(false)}); err != nil {
		t.Fatal(err)
	}
}

func TestNegativeDeregisterDisables(t *testing.T) {
	if _, err := newSettings(noEnv, []Option{WithServiceName("a"), WithDeregisterCriticalServiceAfter(-1)}); err != nil {
		t.Fatal(err)
	}
}

func TestNilOptionErrorsAreJoined(t *testing.T) {
	_, err := newSettings(noEnv, []Option{nil, WithMetrics(nil), WithRetryPolicy(nil)})
	if !errors.Is(err, ErrInvalidConfiguration) || !strings.Contains(err.Error(), "Metrics") || !strings.Contains(err.Error(), "RetryPolicy") {
		t.Fatalf("got %v", err)
	}
}

func TestEnvLayer(t *testing.T) {
	c, err := configFromLookup(envOf(map[string]string{
		EnvConsulAddr:      "consul:8501",
		EnvConsulSSL:       "true",
		EnvConsulSSLVerify: "false",
		EnvConsulToken:     "s3cr3t",
		EnvServicePort:     "9090",
		EnvServiceTags:     "a, b,,c",
		EnvServiceMeta:     "team=payments, tier = gold",
		EnvHealthInterval:  "15s",
		EnvHealthEnabled:   "1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Consul.Scheme != "https" || !c.Consul.TLS.InsecureSkipVerify || c.Consul.Token.Reveal() != "s3cr3t" {
		t.Errorf("consul env not applied: %+v", c.Consul)
	}
	if c.Service.Port != 9090 || strings.Join(c.Service.Tags, "|") != "a|b|c" {
		t.Errorf("service env not applied: %+v", c.Service)
	}
	if c.Service.Meta["tier"] != "gold" || c.Service.Meta["team"] != "payments" {
		t.Errorf("meta %v", c.Service.Meta)
	}
	if c.Health.Interval != 15*time.Second || !c.Health.Enabled {
		t.Errorf("health env not applied: %+v", c.Health)
	}
}

func TestEnvLayerRejectsMalformedValues(t *testing.T) {
	_, err := configFromLookup(envOf(map[string]string{
		EnvServicePort:    "http",
		EnvFailFast:       "maybe",
		EnvHealthInterval: "10",
		EnvServiceMeta:    "novalue",
	}))
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("got %v", err)
	}
	for _, k := range []string{EnvServicePort, EnvFailFast, EnvHealthInterval, EnvServiceMeta} {
		if !strings.Contains(err.Error(), k) {
			t.Errorf("error does not mention %s: %v", k, err)
		}
	}
}

func TestParseConfigYAMLAndJSON(t *testing.T) {
	yamlDoc := `
consul:
  address: http://consul:8500
  token: abc
service:
  name: orders-api
  tags: [v1, blue]
health:
  enabled: true
  interval: 15s
  deregisterCriticalServiceAfter: 2m
lifecycle:
  autoRegister: false
`
	jsonDoc := `{"consul":{"address":"http://consul:8500","token":"abc"},
"service":{"name":"orders-api","tags":["v1","blue"]},
"health":{"enabled":true,"interval":"15s","deregisterCriticalServiceAfter":"2m"},
"lifecycle":{"autoRegister":false}}`

	for name, doc := range map[string]string{"yaml": yamlDoc, "json": jsonDoc} {
		t.Run(name, func(t *testing.T) {
			c, err := ParseConfig([]byte(doc))
			if err != nil {
				t.Fatal(err)
			}
			if c.Consul.Address != "http://consul:8500" || c.Consul.Token.Reveal() != "abc" {
				t.Errorf("consul %+v", c.Consul)
			}
			if c.Service.Name != "orders-api" || len(c.Service.Tags) != 2 {
				t.Errorf("service %+v", c.Service)
			}
			if c.Health.Interval != 15*time.Second || c.Health.DeregisterCriticalServiceAfter != 2*time.Minute {
				t.Errorf("health %+v", c.Health)
			}
			if c.Lifecycle.AutoRegister == nil || *c.Lifecycle.AutoRegister {
				t.Errorf("lifecycle %+v", c.Lifecycle)
			}
		})
	}
}

func TestParseConfigRejectsUnknownKeys(t *testing.T) {
	_, err := ParseConfig([]byte("service:\n  nmme: typo\n"))
	if !errors.Is(err, ErrInvalidConfiguration) || !strings.Contains(err.Error(), "nmme") {
		t.Fatalf("got %v", err)
	}
}

func TestParseConfigEmptyDocument(t *testing.T) {
	c, err := ParseConfig(nil)
	if err != nil || c.Service.Name != "" {
		t.Fatalf("c=%+v err=%v", c, err)
	}
}

func TestLoadConfigOverlaysEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "consulx.yaml")
	if err := os.WriteFile(path, []byte("service:\n  name: from-file\n  version: \"1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvServiceName, "from-env")
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Service.Name != "from-env" || c.Service.Version != "1" {
		t.Fatalf("service %+v", c.Service)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v", err)
	}
}

func FuzzParseConfig(f *testing.F) {
	f.Add([]byte("service:\n  name: a\n"))
	f.Add([]byte(`{"health":{"interval":"10s"}}`))
	f.Add([]byte("consul: [1, 2"))
	f.Fuzz(func(t *testing.T, data []byte) {
		c, err := ParseConfig(data)
		if err != nil {
			return
		}
		// A decoded config must never make validation panic.
		c.applyDefaults()
		_ = c.validate()
	})
}

func TestSecretIsRedactedEverywhere(t *testing.T) {
	s := Secret("super-secret-token")
	cfg := Config{Consul: ConsulConfig{Token: s}}
	outputs := []string{
		fmt.Sprint(s), fmt.Sprintf("%s|%v|%q|%x|%#v", s, s, s, s, s),
		fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg),
	}
	var logBuf strings.Builder
	slog.New(slog.NewJSONHandler(&logBuf, nil)).Info("x", "token", s, "cfg", cfg)
	outputs = append(outputs, logBuf.String())
	text, _ := s.MarshalText()
	outputs = append(outputs, string(text))

	for _, out := range outputs {
		if strings.Contains(out, "super-secret-token") {
			t.Fatalf("secret leaked: %s", out)
		}
	}
	if s.Reveal() != "super-secret-token" {
		t.Fatal("Reveal must return the value")
	}
	if Secret("").String() != "" {
		t.Fatal("empty secret must print empty")
	}
}
