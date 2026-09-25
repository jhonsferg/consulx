package consulx

import (
	"errors"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/jhonsferg/consulx/health"
	"github.com/jhonsferg/consulx/internal/fakeconsul"
	"github.com/jhonsferg/consulx/internal/serviceid"
)

func fakeAgent(t *testing.T, version string) *fakeconsul.Agent {
	t.Helper()
	a := fakeconsul.New(version)
	t.Cleanup(a.Close)
	return a
}

// fastRetry keeps retry-based tests quick.
var fastRetry = WithRetry(RetryConfig{InitialDelay: 5 * time.Millisecond, MaxDelay: 20 * time.Millisecond})

func agentClient(t *testing.T, a *fakeconsul.Agent, opts ...Option) *Client {
	t.Helper()
	base := []Option{
		WithConsulAddress(a.URL()), WithLogger(nil), fastRetry,
		WithServiceName("orders-api"), WithServiceAddress("10.0.0.20"), WithServicePort(8080),
	}
	c, err := New(append(base, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func stop(t *testing.T, c *Client) {
	t.Helper()
	if err := c.Stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStartRegistersAndStopDeregisters(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	srv := &http.Server{Addr: ":8080", Handler: http.NotFoundHandler()}
	c := agentClient(t, a, WithServer(srv), WithAutoHealth(), WithTags("v1"),
		WithMetadata(map[string]string{"team": "payments", "language": "golang"}),
		Config{Service: ServiceConfig{Version: "1.2.3", Environment: "prod", Weights: &Weights{Passing: 10, Warning: 1}}})

	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	wantID := serviceid.HostnamePort("orders-api", host, 8080)
	reg := c.Registration()
	if !reg.Registered || reg.ServiceID != wantID || reg.CheckID != "service:"+wantID || reg.CheckType != CheckHTTP {
		t.Fatalf("registration %+v", reg)
	}
	svc, ok := a.Service(wantID)
	if !ok {
		t.Fatal("service not registered in agent")
	}
	if svc.Name != "orders-api" || svc.Address != "10.0.0.20" || svc.Port != 8080 || !slices.Equal(svc.Tags, []string{"v1"}) {
		t.Fatalf("definition %+v", svc)
	}
	if svc.Weights == nil || svc.Weights.Passing != 10 {
		t.Fatalf("weights %+v", svc.Weights)
	}
	m := svc.Meta
	if m["team"] != "payments" || m["version"] != "1.2.3" || m["environment"] != "prod" || m["secure"] != "false" || m["go_version"] == "" {
		t.Fatalf("meta %v", m)
	}
	if m["language"] != "golang" {
		t.Fatalf("user metadata must win over automatic metadata: %v", m)
	}
	chk := svc.Check
	if chk == nil || chk.HTTP != "http://10.0.0.20:8080/health/ready" || chk.Interval != "10s" || chk.Timeout != "5s" || chk.DeregisterCriticalServiceAfter != "1m0s" {
		t.Fatalf("check %+v", chk)
	}

	stop(t, c)
	if got := a.Deregisters(); !slices.Equal(got, []string{wantID}) {
		t.Fatalf("deregistered %v", got)
	}
	if c.Registration().Registered {
		t.Fatal("registration must be marked lost after deregistration")
	}
}

func TestTTLCheckAndHeartbeat(t *testing.T) {
	a := fakeAgent(t, "2.0.4")
	c := agentClient(t, a, WithHealth(HealthConfig{TTL: time.Second}))
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer stop(t, c)

	svc, _ := a.Service(c.Registration().ServiceID)
	if svc.Check.TTL != "1s" || svc.Check.Status != api.HealthPassing || svc.Check.DeregisterCriticalServiceAfter != "1m0s" {
		t.Fatalf("ttl check %+v", svc.Check)
	}
	eventually(t, "periodic heartbeat", func() bool { return len(a.TTLUpdates()) > 0 })

	// A pushed status change is sent immediately, not at the next tick.
	c.Health().Set("db", health.Result{Status: health.StatusDown})
	eventually(t, "critical heartbeat", func() bool {
		u := a.TTLUpdates()
		return u[len(u)-1].Status == api.HealthCritical
	})
	c.Health().Set("db", health.Result{Status: health.StatusDegraded})
	eventually(t, "warning heartbeat", func() bool {
		u := a.TTLUpdates()
		return u[len(u)-1].Status == api.HealthWarning
	})
}

func TestReRegistersWhenAgentForgetsService(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	c := agentClient(t, a)
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer stop(t, c)

	a.Forget()
	eventually(t, "re-registration", func() bool {
		_, ok := a.Service(c.Registration().ServiceID)
		return ok && a.Registers() == 2
	})
	eventually(t, "running state", func() bool { return c.State() == StateRunning })
}

func TestFailFastStartFails(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	a.SetFailing(http.StatusServiceUnavailable)
	c := agentClient(t, a, WithFailFast(true), Config{Lifecycle: LifecycleConfig{StartTimeout: 200 * time.Millisecond}})

	start := time.Now()
	err := c.Start(t.Context())
	if !errors.Is(err, ErrRegistrationFailed) {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("StartTimeout not honoured")
	}
	if c.State() != StateStopped {
		t.Fatalf("state %s", c.State())
	}
}

func TestFailFastStopsOnPermanentError(t *testing.T) {
	a := fakeAgent(t, "1.21.5")
	// Built without agentClient: its WithServicePort conflicts with Ports.
	c, err := New(WithConsulAddress(a.URL()), WithLogger(nil), fastRetry, WithFailFast(true),
		WithServiceName("mp"), WithServiceAddress("10.0.0.20"),
		Config{Service: ServiceConfig{Ports: []ServicePort{{Name: "http", Port: 8080, Default: true}}}})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Start(t.Context())
	var ufe *UnsupportedFeatureError
	if !errors.As(err, &ufe) || ufe.Agent != "1.21.5" {
		t.Fatalf("got %v", err)
	}
	if a.Registers() != 0 {
		t.Fatal("an unsupported definition must never be sent")
	}
}

func TestUnsupportedFeatureFailsStartEvenWithoutFailFast(t *testing.T) {
	a := fakeAgent(t, "2.0.4")
	c := agentClient(t, a, WithRegistrationHook(func(r *api.AgentServiceRegistration) {
		r.AI = &api.AgentServiceAI{Role: "mcp-server"}
	}))
	if err := c.Start(t.Context()); !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("got %v", err)
	}
}

func TestMultiPortOnSupportedAgent(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	c, err := New(WithConsulAddress(a.URL()), WithLogger(nil), WithServiceName("mp"), WithServiceAddress("10.0.0.20"),
		Config{Service: ServiceConfig{Ports: []ServicePort{{Name: "grpc", Port: 9090}, {Name: "http", Port: 8080, Default: true}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer stop(t, c)
	svc, _ := a.Service(c.Registration().ServiceID)
	if len(svc.Ports) != 2 || svc.Port != 0 || c.Registration().Port != 8080 {
		t.Fatalf("ports %+v port %d", svc.Ports, svc.Port)
	}
}

func TestBackgroundRegistrationRecovers(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	a.SetFailing(http.StatusServiceUnavailable)
	c := agentClient(t, a)
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("FailFast=false must not fail Start: %v", err)
	}
	defer stop(t, c)
	if c.State() != StateDegraded || c.Registration().Registered {
		t.Fatalf("state %s reg %+v", c.State(), c.Registration())
	}
	a.SetFailing(0)
	eventually(t, "registration after recovery", func() bool {
		return c.State() == StateRunning && c.Registration().Registered
	})
}

func TestDegradedWhileAgentDownThenRecovers(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	c := agentClient(t, a)
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer stop(t, c)

	a.SetFailing(http.StatusInternalServerError)
	eventually(t, "degraded", func() bool { return c.State() == StateDegraded })
	select {
	case err := <-c.Errors():
		if err == nil {
			t.Fatal("nil error reported")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("outage not reported on Errors()")
	}

	a.Forget() // the agent also lost its state while down
	a.SetFailing(0)
	eventually(t, "re-registered and running", func() bool {
		_, ok := a.Service(c.Registration().ServiceID)
		return ok && c.State() == StateRunning
	})
}

func TestTransientRegisterFailuresAreRetried(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	a.FailNextRegisters(3)
	c := agentClient(t, a, WithFailFast(true))
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer stop(t, c)
	if a.Registers() != 1 {
		t.Fatalf("registers %d", a.Registers())
	}
}

func TestUnknownVersionIsPermissive(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	a.ForbidSelf()
	c := agentClient(t, a, WithToken("svc-token"))
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer stop(t, c)
	if !c.Registration().Registered {
		t.Fatal("registration must proceed when the version is unreadable")
	}
	for _, tok := range a.Tokens() {
		if tok != "svc-token" {
			t.Fatalf("request without token: %q", tok)
		}
	}
}

func TestMaintenance(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	c := agentClient(t, a)
	if err := c.EnableMaintenance(t.Context(), "x"); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("before registration: %v", err)
	}
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer stop(t, c)
	id := c.Registration().ServiceID
	if err := c.EnableMaintenance(t.Context(), "deploying"); err != nil {
		t.Fatal(err)
	}
	if r, ok := a.Maintenance(id); !ok || r != "deploying" {
		t.Fatalf("maintenance %q %v", r, ok)
	}
	if err := c.DisableMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Maintenance(id); ok {
		t.Fatal("maintenance still enabled")
	}
}

func TestNoDeregistrationWhenDisabled(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	c := agentClient(t, a, Config{Lifecycle: LifecycleConfig{DeregisterOnShutdown: Bool(false)}})
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	stop(t, c)
	if len(a.Deregisters()) != 0 {
		t.Fatal("service deregistered although disabled")
	}
}

func TestStopReportsDeregistrationFailure(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	c := agentClient(t, a)
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	a.SetFailing(http.StatusInternalServerError)
	if err := c.Stop(t.Context()); !errors.Is(err, ErrDeregistrationFailed) {
		t.Fatalf("got %v", err)
	}
	<-c.Done()
}

func TestExplicitRegisterDeregister(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	c := agentClient(t, a, WithAutoRegister(false))
	if err := c.Deregister(t.Context()); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("got %v", err)
	}
	if err := c.Register(t.Context()); err != nil {
		t.Fatal(err)
	}
	if a.Registers() != 1 {
		t.Fatal("not registered")
	}
	if err := c.Deregister(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(a.Deregisters()) != 1 {
		t.Fatal("not deregistered")
	}
}

func TestCheckVariants(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	tests := []struct {
		name  string
		h     HealthConfig
		check func(*api.AgentServiceCheck) bool
	}{
		{"tcp", HealthConfig{Check: CheckTCP, UseTLS: true}, func(c *api.AgentServiceCheck) bool {
			return c.TCP == "10.0.0.20:8080" && c.TCPUseTLS && c.Interval == "10s"
		}},
		{"grpc", HealthConfig{Check: CheckGRPC, GRPCService: "orders.v1"}, func(c *api.AgentServiceCheck) bool {
			return c.GRPC == "10.0.0.20:8080/orders.v1"
		}},
		{"http custom", HealthConfig{Check: CheckHTTP, CheckPath: "/ping", Method: "HEAD", Header: map[string][]string{"X-Probe": {"consul"}}, DeregisterCriticalServiceAfter: -1}, func(c *api.AgentServiceCheck) bool {
			return c.HTTP == "http://10.0.0.20:8080/ping" && c.Method == "HEAD" && c.Header["X-Probe"][0] == "consul" && c.DeregisterCriticalServiceAfter == ""
		}},
		{"none", HealthConfig{Check: CheckNone}, func(c *api.AgentServiceCheck) bool { return c == nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := agentClient(t, a, WithHealth(tt.h), WithServiceID("svc-"+tt.name[:2]))
			reg, err := c.buildRegistration(t.Context(), endpoint{Address: "10.0.0.20", Port: 8080, Scheme: "http"})
			if err != nil {
				t.Fatal(err)
			}
			if !tt.check(reg.Check) {
				t.Fatalf("check %+v", reg.Check)
			}
		})
	}
}

func TestIPv6ServiceAddressGate(t *testing.T) {
	a := fakeAgent(t, "1.21.5")
	c := agentClient(t, a, WithServiceAddress("fd00::20"))
	if err := c.Start(t.Context()); !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("got %v", err)
	}
	a2 := fakeAgent(t, "1.22.7")
	c2 := agentClient(t, a2, WithServiceAddress("fd00::20"), WithAutoHealth())
	if err := c2.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer stop(t, c2)
	svc, _ := a2.Service(c2.Registration().ServiceID)
	if svc.Check.HTTP != "http://[fd00::20]:8080/health/ready" {
		t.Fatalf("IPv6 check URL %q", svc.Check.HTTP)
	}
}

func TestDisableAutoMetaAndRandomID(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	c := agentClient(t, a, Config{Service: ServiceConfig{DisableAutoMeta: true, IDStrategy: IDRandom}})
	reg, err := c.buildRegistration(t.Context(), endpoint{Address: "10.0.0.20", Port: 8080, Scheme: "https"})
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Meta) != 1 || reg.Meta["secure"] != "true" {
		t.Fatalf("meta %v", reg.Meta)
	}
	if !strings.HasPrefix(reg.ID, "orders-api-") || len(reg.ID) != len("orders-api-")+36 {
		t.Fatalf("id %q", reg.ID)
	}
	again, _ := c.buildRegistration(t.Context(), endpoint{Address: "10.0.0.20", Port: 8080})
	if again.ID != reg.ID {
		t.Fatal("generated ID must be stable for the Client's lifetime")
	}
}

func TestMetaValidation(t *testing.T) {
	for name, meta := range map[string]map[string]string{
		"reserved prefix": {"consul-x": "1"},
		"bad char":        {"a.b": "1"},
		"long value":      {"k": strings.Repeat("v", 513)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New(WithServiceName("a"), WithMetadata(meta))
			if !errors.Is(err, ErrInvalidConfiguration) || !strings.Contains(err.Error(), "Service.Meta") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestIsPermanent(t *testing.T) {
	cases := map[error]bool{
		api.StatusError{Code: 400}:       true,
		api.StatusError{Code: 403}:       true,
		api.StatusError{Code: 429}:       false,
		api.StatusError{Code: 500}:       false,
		errors.New("connection refused"): false,
		&ConfigError{Field: "x"}:         true,
		&UnsupportedFeatureError{}:       true,
	}
	for err, want := range cases {
		if got := isPermanent(err); got != want {
			t.Errorf("isPermanent(%v) = %v", err, got)
		}
	}
}

// TestRegistrationFieldsAreClassified fails when an upgrade of the official
// client adds a field to AgentServiceRegistration. Agents reject unknown
// fields (docs/compatibility.md), so every new field must be classified:
// safe on every supported agent, or gated in checkFeatures.
func TestRegistrationFieldsAreClassified(t *testing.T) {
	safe := map[string]bool{
		"Kind": true, "ID": true, "Name": true, "Tags": true, "Port": true,
		"Address": true, "SocketPath": true, "TaggedAddresses": true,
		"EnableTagOverride": true, "Meta": true, "Weights": true, "Check": true,
		"Checks": true, "Proxy": true, "Connect": true, "Locality": true,
	}
	gated := map[string]bool{"Ports": true, "AI": true, "Namespace": true, "Partition": true}
	typ := reflect.TypeFor[api.AgentServiceRegistration]()
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		if !safe[name] && !gated[name] {
			t.Errorf("unclassified registration field %q: verify which agents accept it and gate it if needed", name)
		}
	}
}
