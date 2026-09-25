package integration

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/jhonsferg/consulx"
	"github.com/jhonsferg/consulx/health"
)

// httpService starts an HTTP server on the loopback interface and returns
// it with its listener. The Consul container reaches it through
// host.docker.internal.
func httpService(t *testing.T) (*http.Server, net.Listener) {
	t.Helper()
	ln := listen(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "[]") })
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return srv, ln
}

func serve(t *testing.T, srv *http.Server, ln net.Listener) {
	t.Helper()
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

func quickRetry() consulx.Option {
	return consulx.WithRetry(consulx.RetryConfig{InitialDelay: 200 * time.Millisecond, MaxDelay: time.Second})
}

// Scenario: the application starts, the service is registered, appears in
// Consul with a passing health check, and is deregistered on shutdown.
func TestRegistrationLifecycleWithHTTPCheck(t *testing.T) {
	a := startConsul(t)
	name := uniqueName(t)
	srv, ln := httpService(t)

	c, err := consulx.New(
		consulx.WithConsulAddress(a.addr),
		consulx.WithServer(srv), consulx.WithListener(ln),
		consulx.WithServiceName(name),
		consulx.WithServiceAddress(serviceHost),
		consulx.WithAutoHealth(),
		consulx.WithHealth(consulx.HealthConfig{Interval: time.Second, Timeout: time.Second}),
		consulx.WithTags("it"),
		consulx.WithMetadata(map[string]string{"team": "platform"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	serve(t, srv, ln) // after New, as documented
	stop := runUntil(t, c.Run)

	defer func() {
		if t.Failed() {
			for _, e := range a.instances(t, name, false) {
				for _, ch := range e.Checks {
					t.Logf("check %s %s: %s", ch.CheckID, ch.Status, ch.Output)
				}
			}
		}
	}()
	eventually(t, 30*time.Second, "service passing", func() bool {
		return len(a.instances(t, name, true)) == 1
	})
	e := a.instances(t, name, true)[0]
	if e.Service.ID != c.Registration().ServiceID || e.Service.Meta["team"] != "platform" || e.Service.Tags[0] != "it" {
		t.Fatalf("unexpected entry %+v", e.Service)
	}
	for _, chk := range e.Checks {
		if chk.ServiceID == e.Service.ID && chk.Type != "http" {
			t.Fatalf("check type %q", chk.Type)
		}
	}

	// The original route still works next to the injected endpoints.
	resp, err := http.Get(localURL(ln) + "/orders")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("original route: %v %v", resp, err)
	}
	resp.Body.Close()

	// A failing readiness component turns the Consul check critical.
	c.Health().Set("db", health.Result{Status: health.StatusDown})
	eventually(t, 30*time.Second, "service critical", func() bool {
		return len(a.instances(t, name, true)) == 0 && len(a.instances(t, name, false)) == 1
	})
	c.Health().Remove("db")
	eventually(t, 30*time.Second, "service passing again", func() bool {
		return len(a.instances(t, name, true)) == 1
	})

	if err := stop(); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if n := len(a.instances(t, name, false)); n != 0 {
		t.Fatalf("service still registered after shutdown: %d", n)
	}
}

// TTL check: the heartbeat keeps the service passing and reflects health.
func TestTTLHeartbeat(t *testing.T) {
	a := startConsul(t)
	name := uniqueName(t)
	c, err := consulx.New(
		consulx.WithConsulAddress(a.addr),
		consulx.WithServiceName(name),
		consulx.WithServiceAddress(serviceHost), consulx.WithServicePort(9999),
		consulx.WithHealth(consulx.HealthConfig{TTL: 3 * time.Second}),
	)
	if err != nil {
		t.Fatal(err)
	}
	stop := runUntil(t, c.Run)
	defer func() { _ = stop() }()

	eventually(t, 20*time.Second, "passing", func() bool { return len(a.instances(t, name, true)) == 1 })
	// Longer than the TTL: only heartbeats keep it passing.
	time.Sleep(5 * time.Second)
	if len(a.instances(t, name, true)) != 1 {
		t.Fatal("service expired although heartbeats were sent")
	}
	c.Health().Set("queue", health.Result{Status: health.StatusDegraded})
	eventually(t, 10*time.Second, "warning", func() bool {
		all := a.instances(t, name, false)
		return len(all) == 1 && all[0].Checks.AggregatedStatus() == api.HealthWarning
	})
	c.Health().Set("queue", health.Result{Status: health.StatusDown})
	eventually(t, 10*time.Second, "critical", func() bool {
		all := a.instances(t, name, false)
		return len(all) == 1 && all[0].Checks.AggregatedStatus() == api.HealthCritical
	})
}

// Scenario: Consul unavailable at start-up, in both FailFast modes.
func TestConsulUnavailableAtStartup(t *testing.T) {
	down := "http://127.0.0.1:" + strconv.Itoa(freePort(t))
	base := []consulx.Option{
		consulx.WithConsulAddress(down), consulx.WithServiceName("unavailable"),
		consulx.WithServiceAddress("10.0.0.1"), consulx.WithServicePort(80), quickRetry(),
	}

	ff, err := consulx.New(append(base, consulx.WithFailFast(true),
		consulx.Config{Lifecycle: consulx.LifecycleConfig{StartTimeout: 2 * time.Second}})...)
	if err != nil {
		t.Fatal(err)
	}
	if err := ff.Start(t.Context()); !errors.Is(err, consulx.ErrConsulUnavailable) || !errors.Is(err, consulx.ErrRegistrationFailed) {
		t.Fatalf("FailFast=true: got %v", err)
	}

	lenient, err := consulx.New(base...)
	if err != nil {
		t.Fatal(err)
	}
	if err := lenient.Start(t.Context()); err != nil {
		t.Fatalf("FailFast=false must start: %v", err)
	}
	if lenient.State() != consulx.StateDegraded {
		t.Fatalf("state %s", lenient.State())
	}
	if err := lenient.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// Scenario: Consul goes down while the service runs, ConsulX retries, and
// when the agent comes back without state the service is re-registered.
func TestReconnectAndReRegister(t *testing.T) {
	a := startConsul(t)
	name := uniqueName(t)
	c, err := consulx.New(
		consulx.WithConsulAddress(a.addr),
		consulx.WithServiceName(name),
		consulx.WithServiceAddress(serviceHost), consulx.WithServicePort(9999),
		consulx.WithHealth(consulx.HealthConfig{TTL: 3 * time.Second}),
		consulx.WithRequestTimeout(2*time.Second),
		quickRetry(),
	)
	if err != nil {
		t.Fatal(err)
	}
	stop := runUntil(t, c.Run)
	defer func() { _ = stop() }()
	eventually(t, 20*time.Second, "registered", func() bool { return len(a.instances(t, name, true)) == 1 })

	a.restart(t, 3*time.Second)
	eventually(t, 60*time.Second, "re-registered after agent restart", func() bool {
		return len(a.instances(t, name, true)) == 1
	})
	eventually(t, 10*time.Second, "running", func() bool { return c.State() == consulx.StateRunning })
}

// Scenario: the process dies without deregistering; the check goes
// critical and DeregisterCriticalServiceAfter removes the instance.
func TestCrashProtectionReapsCriticalService(t *testing.T) {
	if testing.Short() {
		t.Skip("takes about two minutes")
	}
	a := startConsul(t)
	name := uniqueName(t)
	c, err := consulx.New(
		consulx.WithConsulAddress(a.addr),
		consulx.WithServiceName(name),
		consulx.WithServiceAddress(serviceHost), consulx.WithServicePort(9999),
		consulx.WithHealth(consulx.HealthConfig{TTL: 2 * time.Second}),
		consulx.WithDeregisterCriticalServiceAfter(time.Minute),
		// Simulates a crash: nothing is deregistered on the way out.
		consulx.Config{Lifecycle: consulx.LifecycleConfig{DeregisterOnShutdown: consulx.Bool(false)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	eventually(t, 20*time.Second, "passing", func() bool { return len(a.instances(t, name, true)) == 1 })
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, "critical", func() bool { return len(a.instances(t, name, true)) == 0 })
	if len(a.instances(t, name, false)) != 1 {
		t.Fatal("service must still be registered while critical")
	}
	eventually(t, 3*time.Minute, "reaped", func() bool { return len(a.instances(t, name, false)) == 0 })
}

func TestMaintenanceMode(t *testing.T) {
	a := startConsul(t)
	name := uniqueName(t)
	c, err := consulx.New(consulx.WithConsulAddress(a.addr), consulx.WithServiceName(name),
		consulx.WithServiceAddress(serviceHost), consulx.WithServicePort(9999))
	if err != nil {
		t.Fatal(err)
	}
	stop := runUntil(t, c.Run)
	defer func() { _ = stop() }()
	eventually(t, 20*time.Second, "passing", func() bool { return len(a.instances(t, name, true)) == 1 })

	if err := c.EnableMaintenance(t.Context(), "deploy"); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "excluded while in maintenance", func() bool { return len(a.instances(t, name, true)) == 0 })
	if err := c.DisableMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "back", func() bool { return len(a.instances(t, name, true)) == 1 })
}

// The feature gate must agree with what the real agent accepts.
func TestFeatureGateMatchesAgent(t *testing.T) {
	a := startConsul(t)
	c, err := consulx.New(consulx.WithConsulAddress(a.addr), consulx.WithServiceName("mp"),
		consulx.WithServiceAddress(serviceHost),
		consulx.Config{Service: consulx.ServiceConfig{Ports: []consulx.ServicePort{
			{Name: "http", Port: 8080, Default: true}, {Name: "grpc", Port: 9090},
		}}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := c.AgentInfo(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("agent version %s", info.Version)

	// What does the real agent say about Ports?
	raw := a.api.Agent().ServiceRegisterOpts(&api.AgentServiceRegistration{
		Name: "probe", Ports: api.ServicePorts{{Name: "http", Port: 1, Default: true}},
	}, api.ServiceRegisterOpts{}.WithContext(t.Context()))
	agentAccepts := raw == nil
	if raw != nil && !strings.Contains(raw.Error(), "unknown field") {
		t.Fatalf("unexpected agent error: %v", raw)
	}

	err = c.Register(t.Context())
	switch {
	case agentAccepts && err != nil:
		t.Fatalf("agent %s accepts Ports but ConsulX refused: %v", info.Version, err)
	case !agentAccepts && !errors.Is(err, consulx.ErrUnsupportedFeature):
		t.Fatalf("agent %s rejects Ports but ConsulX did not gate it: %v", info.Version, err)
	}
	if err == nil {
		_ = c.Deregister(t.Context())
	}
}
