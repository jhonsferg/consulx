package consulx

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jhonsferg/consulx/health"
)

func newTestClient(t *testing.T, opts ...Option) *Client {
	t.Helper()
	base := []Option{WithConsulAddress("http://127.0.0.1:1"), WithLogger(nil)}
	c, err := New(append(base, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body)
}

func TestHealthInjectionKeepsOriginalRouter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "orders") })
	mux.HandleFunc("GET /health/custom", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "mine") })
	srv := &http.Server{Addr: ":8080", Handler: mux}

	newTestClient(t, WithServer(srv), WithServiceName("orders"), WithAutoHealth())

	if code, body := get(t, srv.Handler, "/orders"); code != 200 || body != "orders" {
		t.Fatalf("original route: %d %q", code, body)
	}
	if code, body := get(t, srv.Handler, "/health/custom"); code != 200 || body != "mine" {
		t.Fatalf("user route below the health prefix must be untouched: %d %q", code, body)
	}
	if code, _ := get(t, srv.Handler, "/unknown"); code != http.StatusNotFound {
		t.Fatalf("unknown route must behave as before: %d", code)
	}
	for _, p := range []string{"/health", "/health/live", "/health/ready"} {
		code, body := get(t, srv.Handler, p)
		var rep health.Report
		if code != 200 || json.Unmarshal([]byte(body), &rep) != nil || rep.Status != health.StatusUp {
			t.Fatalf("%s: %d %q", p, code, body)
		}
	}
}

func TestHealthReflectsRegistry(t *testing.T) {
	srv := &http.Server{Addr: ":8080", Handler: http.NotFoundHandler()}
	c := newTestClient(t, WithServer(srv), WithServiceName("a"), WithAutoHealth())

	c.Health().Register("db", health.CheckerFunc(func(context.Context) health.Result {
		return health.Result{Status: health.StatusDown, Error: "timeout"}
	}))
	if code, _ := get(t, srv.Handler, "/health/ready"); code != http.StatusServiceUnavailable {
		t.Fatalf("ready: %d", code)
	}
	if code, _ := get(t, srv.Handler, "/health/live"); code != 200 {
		t.Fatalf("live must not depend on readiness components: %d", code)
	}
	c.Health().Remove("db")
	c.Health().Set("cache", health.Result{Status: health.StatusDegraded})
	if code, _ := get(t, srv.Handler, "/health"); code != http.StatusTooManyRequests {
		t.Fatalf("degraded: %d", code)
	}
}

var registerLateRoute sync.Once

func TestNilHandlerUsesDefaultServeMux(t *testing.T) {
	srv := &http.Server{Addr: ":8080"}
	newTestClient(t, WithServer(srv), WithServiceName("a"), WithAutoHealth())
	// Registered after New: the captured DefaultServeMux must still see it.
	// DefaultServeMux is global, so register once per process (-count=N).
	registerLateRoute.Do(func() {
		http.DefaultServeMux.HandleFunc("/consulx-test-late", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "late") })
	})
	if code, body := get(t, srv.Handler, "/consulx-test-late"); code != 200 || body != "late" {
		t.Fatalf("%d %q", code, body)
	}
}

func TestNoInjectionWhenHealthDisabled(t *testing.T) {
	orig := http.NotFoundHandler()
	srv := &http.Server{Addr: ":8080", Handler: orig}
	newTestClient(t, WithServer(srv), WithServiceName("a"))
	if _, wrapped := srv.Handler.(*healthMux); wrapped {
		t.Fatal("handler must not be wrapped when health endpoints are disabled")
	}
}

func TestCustomEndpointsAndHideDetails(t *testing.T) {
	srv := &http.Server{Addr: ":8080", Handler: http.NotFoundHandler()}
	c := newTestClient(t, WithServer(srv), WithServiceName("a"),
		WithHealthEndpoints(HealthEndpoints{Health: "/status"}),
		WithHealth(HealthConfig{HideDetails: true, DegradedStatusCode: 200}))
	c.Health().Set("x", health.Result{Status: health.StatusDegraded, Details: map[string]any{"k": "v"}})

	if code, body := get(t, srv.Handler, "/status"); code != 200 || body != "{\"status\":\"DEGRADED\"}\n" {
		t.Fatalf("%d %q", code, body)
	}
	if code, _ := get(t, srv.Handler, "/health"); code != http.StatusNotFound {
		t.Fatalf("default path must not be served: %d", code)
	}
}

func TestHealthHandlerForManualMounting(t *testing.T) {
	c := newTestClient(t, WithServiceName("a"), WithAutoHealth())
	h := c.HealthHandler()
	if code, _ := get(t, h, "/health/ready"); code != 200 {
		t.Fatalf("ready: %d", code)
	}
	if code, _ := get(t, h, "/other"); code != http.StatusNotFound {
		t.Fatalf("other: %d", code)
	}
}

func BenchmarkHealthMuxPassThrough(b *testing.B) {
	srv := &http.Server{Addr: ":8080", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})}
	c, err := New(WithServer(srv), WithServiceName("a"), WithAutoHealth(), WithLogger(nil))
	if err != nil {
		b.Fatal(err)
	}
	_ = c
	req := httptest.NewRequest(http.MethodGet, "/orders/42", nil)
	w := httptest.NewRecorder()
	b.ReportAllocs()
	for b.Loop() {
		srv.Handler.ServeHTTP(w, req)
	}
}
