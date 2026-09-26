//go:build bench

// Benchmarks of the core package. They are excluded from normal builds by the
// bench build tag, so CI never compiles or runs them; run them locally with
// scripts/bench.sh or:
//
//	go test -tags bench -run '^$' -bench . -benchmem .
//
// Benchmarks that talk to the in-process fake agent report allocations of the
// whole process, which includes the fake agent's HTTP server. Each of them has
// a "raw-api" variant doing the same request with the official client, so the
// difference between the two is exactly what ConsulX adds. See
// docs/benchmarks.md.
package consulx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/jhonsferg/consulx/balancer"
	"github.com/jhonsferg/consulx/health"
	"github.com/jhonsferg/consulx/internal/fakeconsul"
	"github.com/jhonsferg/consulx/kvconfig"
)

// benchAgent starts a fake agent that lives as long as the benchmark.
func benchAgent(b *testing.B) *fakeconsul.Agent {
	b.Helper()
	a := fakeconsul.New("1.22.7")
	b.Cleanup(a.Close)
	return a
}

// benchClient returns a client of a pointing at a registered service.
func benchClient(b *testing.B, a *fakeconsul.Agent, opts ...Option) *Client {
	b.Helper()
	base := []Option{
		WithConsulAddress(a.URL()), WithLogger(nil),
		WithServiceName("orders-api"), WithServiceAddress("10.0.0.20"), WithServicePort(8080),
	}
	c, err := New(append(base, opts...)...)
	if err != nil {
		b.Fatal(err)
	}
	return c
}

// discardWriter is a reusable http.ResponseWriter, so handler benchmarks
// measure the handler and not httptest.ResponseRecorder.
type discardWriter struct {
	h    http.Header
	code int
}

func newDiscardWriter() *discardWriter { return &discardWriter{h: http.Header{}} }

func (w *discardWriter) Header() http.Header         { return w.h }
func (w *discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *discardWriter) WriteHeader(code int)        { w.code = code }
func (w *discardWriter) reset()                      { clear(w.h); w.code = 0 }

var _ io.Writer = (*discardWriter)(nil)

// BenchmarkNew measures building a Client: option processing, defaults,
// validation and the HTTP transport. It runs once per process, so it matters
// for start-up time and for tools that build many clients.
func BenchmarkNew(b *testing.B) {
	b.Run("minimal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := New(WithConsulAddress("http://127.0.0.1:8500"), WithAutoRegister(false), WithLogger(nil)); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("server-health", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			srv := &http.Server{Addr: ":8080", Handler: http.NotFoundHandler()}
			if _, err := New(WithConsulAddress("http://127.0.0.1:8500"), WithServer(srv),
				WithServiceName("orders-api"), WithAutoHealth(), WithLogger(nil),
				WithTags("v1", "blue"), WithMetadata(map[string]string{"team": "payments"})); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkParseConfig measures decoding a configuration file.
func BenchmarkParseConfig(b *testing.B) {
	docs := map[string]string{
		"yaml": `
consul:
  address: http://consul:8500
  token: abc
service:
  name: orders-api
  tags: [v1, blue]
  meta:
    team: payments
health:
  enabled: true
  interval: 15s
  deregisterCriticalServiceAfter: 2m
lifecycle:
  autoRegister: false
`,
		"json": `{"consul":{"address":"http://consul:8500","token":"abc"},
"service":{"name":"orders-api","tags":["v1","blue"],"meta":{"team":"payments"}},
"health":{"enabled":true,"interval":"15s","deregisterCriticalServiceAfter":"2m"},
"lifecycle":{"autoRegister":false}}`,
	}
	for _, name := range []string{"yaml", "json"} {
		doc := []byte(docs[name])
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := ParseConfig(doc); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkConfigFromEnv measures reading the configuration from variables.
func BenchmarkConfigFromEnv(b *testing.B) {
	env := map[string]string{
		EnvConsulAddr: "http://consul:8500", EnvConsulToken: "abc",
		EnvServiceName: "orders-api", EnvServicePort: "8080",
		EnvServiceTags: "v1,blue", EnvServiceMeta: "team=payments,tier=1",
		EnvEnvironment: "prod", EnvHealthEnabled: "true", EnvHealthInterval: "15s",
	}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	b.ReportAllocs()
	for b.Loop() {
		if _, err := configFromLookup(lookup); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMiddlewareOverhead is the cost ConsulX adds to every request of
// the application when health endpoints are injected into its server. The
// "direct" variant calls the application handler without ConsulX; the
// difference to "wrapped" is the whole per-request overhead.
func BenchmarkMiddlewareOverhead(b *testing.B) {
	app := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := &http.Server{Addr: ":8080", Handler: app}
	if _, err := New(WithServer(srv), WithServiceName("a"), WithAutoHealth(), WithLogger(nil)); err != nil {
		b.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/orders/42", nil)
	for _, v := range []struct {
		name string
		h    http.Handler
	}{{"direct", app}, {"wrapped", srv.Handler}} {
		b.Run(v.name, func(b *testing.B) {
			w := newDiscardWriter()
			b.ReportAllocs()
			for b.Loop() {
				v.h.ServeHTTP(w, req)
			}
		})
		b.Run(v.name+"-parallel", func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				w := newDiscardWriter()
				for pb.Next() {
					v.h.ServeHTTP(w, req)
				}
			})
		})
	}
}

// BenchmarkHealthEndpoints measures the injected endpoints as Consul,
// Kubernetes and load balancers poll them: two checkers and one pushed
// component, JSON response.
func BenchmarkHealthEndpoints(b *testing.B) {
	c, err := New(WithConsulAddress("http://127.0.0.1:1"), WithServiceName("a"), WithServicePort(8080),
		WithAutoHealth(), WithLogger(nil))
	if err != nil {
		b.Fatal(err)
	}
	up := health.CheckerFunc(func(context.Context) health.Result { return health.Result{Status: health.StatusUp} })
	c.Health().Register("db", up)
	c.Health().Register("cache", up, health.Readiness, health.Liveness)
	c.Health().Set("broker", health.Result{Status: health.StatusUp})
	h := c.HealthHandler()

	cases := []struct{ name, method, path string }{
		{"ready", http.MethodGet, DefaultReadyPath},
		{"live", http.MethodGet, DefaultLivePath},
		{"health", http.MethodGet, DefaultHealthPath},
		{"ready-head", http.MethodHead, DefaultReadyPath},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		b.Run(tc.name, func(b *testing.B) {
			w := newDiscardWriter()
			b.ReportAllocs()
			for b.Loop() {
				w.reset()
				h.ServeHTTP(w, req)
			}
		})
	}
	b.Run("ready-parallel", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodGet, DefaultReadyPath, nil)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			w := newDiscardWriter()
			for pb.Next() {
				w.reset()
				h.ServeHTTP(w, req)
			}
		})
	})
}

// BenchmarkBuildRegistration measures turning the configuration into the
// service definition, done on every (re-)registration.
func BenchmarkBuildRegistration(b *testing.B) {
	ep := endpoint{Address: "10.0.0.20", Port: 8080, Scheme: "http"}
	for _, check := range []CheckType{CheckHTTP, CheckTTL} {
		b.Run(string(check), func(b *testing.B) {
			c, err := New(WithConsulAddress("http://127.0.0.1:1"), WithLogger(nil),
				WithServiceName("orders-api"), WithServicePort(8080), WithTags("v1", "blue"),
				WithMetadata(map[string]string{"team": "payments"}),
				WithHealth(HealthConfig{Enabled: true, Check: check}))
			if err != nil {
				b.Fatal(err)
			}
			ctx := context.Background()
			b.ReportAllocs()
			for b.Loop() {
				if _, err := c.buildRegistration(ctx, ep); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkResolveEndpoint measures address and port resolution, done on
// every registration attempt.
func BenchmarkResolveEndpoint(b *testing.B) {
	cases := []struct {
		name string
		opts []Option
	}{
		{"configured", []Option{WithServiceAddress("10.0.0.20"), WithServicePort(8080)}},
		{"resolver", []Option{WithAddressResolver(StaticAddress("10.0.0.20")), WithServicePort(8080)}},
		{"route", []Option{WithServicePort(8080)}}, // default chain: route to the agent
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			opts := append([]Option{WithConsulAddress("http://127.0.0.1:8500"), WithLogger(nil), WithServiceName("a")}, tc.opts...)
			if tc.name == "route" {
				opts = append(opts, Config{Service: ServiceConfig{AllowLoopback: true}})
			}
			c, err := New(opts...)
			if err != nil {
				b.Fatal(err)
			}
			ctx := context.Background()
			if _, err := c.resolveEndpoint(ctx); err != nil {
				b.Skip("no usable address on this host:", err)
			}
			b.ReportAllocs()
			for b.Loop() {
				_, _ = c.resolveEndpoint(ctx)
			}
		})
	}
}

// BenchmarkRegister measures one complete registration: agent detection,
// endpoint resolution, definition and the PUT request. It happens at start-up
// and after every agent restart.
func BenchmarkRegister(b *testing.B) {
	a := benchAgent(b)
	ctx := context.Background()
	b.Run("consulx", func(b *testing.B) {
		c := benchClient(b, a)
		b.ReportAllocs()
		for b.Loop() {
			if err := c.register(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("raw-api", func(b *testing.B) {
		raw, _ := api.NewClient(&api.Config{Address: a.URL()})
		reg := &api.AgentServiceRegistration{
			ID: "orders-api-raw", Name: "orders-api", Address: "10.0.0.20", Port: 8080,
			Check: &api.AgentServiceCheck{CheckID: "service:orders-api-raw", TTL: "30s"},
		}
		b.ReportAllocs()
		for b.Loop() {
			if err := raw.Agent().ServiceRegisterOpts(reg, api.ServiceRegisterOpts{ReplaceExistingChecks: true}.WithContext(ctx)); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkHeartbeat measures one TTL update: readiness probe plus the PUT
// request. A running service sends one every TTL/3 (10s by default), and one
// more on every pushed health change.
func BenchmarkHeartbeat(b *testing.B) {
	a := benchAgent(b)
	ctx := context.Background()
	c := benchClient(b, a, WithHealth(HealthConfig{Enabled: true, Check: CheckTTL}))
	if err := c.register(ctx); err != nil {
		b.Fatal(err)
	}
	checkID := c.Registration().CheckID
	b.Run("consulx", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			c.heartbeat(ctx)
		}
	})
	b.Run("raw-api", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := c.api.Agent().UpdateTTLOpts(checkID, "ConsulX heartbeat: UP", api.HealthPassing, (&api.QueryOptions{}).WithContext(ctx)); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkRuntimeAccessors measures the accessors applications call on hot
// paths (state checks in handlers, registration details in logs).
func BenchmarkRuntimeAccessors(b *testing.B) {
	c, err := New(WithConsulAddress("http://127.0.0.1:1"), WithAutoRegister(false), WithLogger(nil))
	if err != nil {
		b.Fatal(err)
	}
	b.Run("State", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = c.State()
		}
	})
	b.Run("Registration", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = c.Registration()
		}
	})
	b.Run("State-parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			var s State
			for pb.Next() {
				s |= c.State() // keep the read from being optimised away
			}
			_ = s
		})
	})
}

// BenchmarkMetricsNoop measures the metric calls ConsulX makes when no
// Metrics implementation is configured. They sit on the registration,
// heartbeat and discovery paths and should cost nothing.
func BenchmarkMetricsNoop(b *testing.B) {
	c, err := New(WithConsulAddress("http://127.0.0.1:1"), WithAutoRegister(false), WithLogger(nil))
	if err != nil {
		b.Fatal(err)
	}
	b.Run("counter", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			c.metrics.IncCounter(MetricRegisterTotal)
		}
	})
	b.Run("counter-label", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			c.metrics.IncCounter(MetricConsulRequestsTotal, labelsRegister...)
		}
	})
	b.Run("duration-label", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			c.metrics.ObserveDuration(MetricConsulRequestDuration, time.Millisecond, labelsRegister...)
		}
	})
}

// BenchmarkSecret measures the redacted token type in the forms logging
// uses.
func BenchmarkSecret(b *testing.B) {
	s := Secret("b1gs3cr3t-token")
	b.Run("String", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = s.String()
		}
	})
	b.Run("LogValue", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = s.LogValue()
		}
	})
	b.Run("Sprintf", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = fmt.Sprintf("%v", s)
		}
	})
}

// BenchmarkLifecycle measures a complete Start and Stop: registration,
// runtime goroutines, deregistration and connection release. It is the
// start-up and shutdown cost of a service instance.
func BenchmarkLifecycle(b *testing.B) {
	a := benchAgent(b)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		c := benchClient(b, a)
		if err := c.Start(ctx); err != nil {
			b.Fatal(err)
		}
		if err := c.Stop(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRuntimeFootprint measures what a running Client keeps from its
// host service, which is the steady-state impact of the library:
//
//   - heap-B: live heap retained after a garbage collection, including the
//     HTTP connections to the agent (and the fake agent's side of them, so
//     it is an upper bound);
//   - goroutines: goroutines running ConsulX code plus the client side of its
//     HTTP connections (two per open connection in net/http).
//
// Each iteration builds, starts and stops one Client; ns/op, B/op and
// allocs/op are that complete lifecycle, while the two custom metrics are
// measured while it runs.
func BenchmarkRuntimeFootprint(b *testing.B) {
	type deps struct {
		lb *balancer.Balancer
		w  io.Closer
	}
	cases := []struct {
		name  string
		opts  []Option
		start func(b *testing.B, c *Client) deps
	}{
		{"registered-http", []Option{WithHealth(HealthConfig{Enabled: true, Check: CheckHTTP})}, nil},
		{"registered-ttl", []Option{WithHealth(HealthConfig{Enabled: true, Check: CheckTTL})}, nil},
		{"full", []Option{WithHealth(HealthConfig{Enabled: true, Check: CheckTTL})}, func(b *testing.B, c *Client) deps {
			lb := c.Balancer(balancer.RoundRobin())
			if _, err := lb.Next(context.Background(), "payments"); err != nil {
				b.Fatal(err)
			}
			type cfg struct {
				Database struct {
					Host string `consul:"host"`
				} `consul:"database"`
			}
			w, err := kvconfig.Watch[cfg](context.Background(), c.Config())
			if err != nil {
				b.Fatal(err)
			}
			return deps{lb: lb, w: w}
		}},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			a := benchAgent(b)
			a.SetHealth("payments", &api.ServiceEntry{
				Node:    &api.Node{Node: "n", Address: "10.0.0.1"},
				Service: &api.AgentService{ID: "p1", Service: "payments", Port: 80},
				Checks:  api.HealthChecks{{Status: api.HealthPassing}},
			})
			a.PutKV("config/orders-api/database/host", "db")
			ctx := context.Background()
			var heap, goroutines float64
			runs := 0
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				base := liveHeapWith(func() {})
				b.StartTimer()

				c := benchClient(b, a, tc.opts...)
				if err := c.Start(ctx); err != nil {
					b.Fatal(err)
				}
				var d deps
				if tc.start != nil {
					d = tc.start(b, c)
				}

				b.StopTimer()
				time.Sleep(50 * time.Millisecond) // let the runtime tasks reach their steady state
				goroutines += float64(clientGoroutines())
				heap += max(liveHeapWith(func() { _ = c.State(); _ = d })-base, 0)
				runs++
				b.StartTimer()

				if d.w != nil {
					_ = d.w.Close()
				}
				if err := c.Stop(ctx); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(heap/float64(runs), "heap-B")
			b.ReportMetric(goroutines/float64(runs), "goroutines")
		})
	}
}

// clientGoroutines counts goroutines running ConsulX code or serving its
// HTTP client connections. The fake agent only runs server-side goroutines,
// so every client connection in the process belongs to ConsulX.
func clientGoroutines() int {
	var sb strings.Builder
	_ = pprof.Lookup("goroutine").WriteTo(&sb, 1)
	n := 0
	for block := range strings.SplitSeq(sb.String(), "\n\n") {
		owned := strings.Contains(block, "github.com/jhonsferg/consulx") &&
			!strings.Contains(block, "internal/fakeconsul") && !strings.Contains(block, "_test.go")
		if !owned && !strings.Contains(block, "net/http.(*persistConn)") {
			continue
		}
		var count int
		if _, err := fmt.Sscanf(block, "%d @", &count); err == nil {
			n += count
		}
	}
	return n
}

// liveHeapWith returns the live heap after a full collection. keep is called
// after measuring, so the objects under test stay reachable until then.
func liveHeapWith(keep func()) float64 {
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	keep()
	return float64(ms.HeapAlloc)
}
