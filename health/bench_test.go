//go:build bench

// Benchmarks of the health package, excluded from normal builds by the bench
// build tag. See docs/benchmarks.md.
package health

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// discardWriter is a reusable http.ResponseWriter, so handler benchmarks
// measure the handler and not httptest.ResponseRecorder.
type discardWriter struct{ h http.Header }

func (w *discardWriter) Header() http.Header         { return w.h }
func (w *discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *discardWriter) WriteHeader(int)             {}

// registry returns a registry with checkers checker components and pushed
// pushed components, all UP.
func registry(checkers, pushed int) *Registry {
	r := NewRegistry(0)
	for i := range checkers {
		r.Register(fmt.Sprintf("checker-%02d", i), CheckerFunc(up))
	}
	for i := range pushed {
		r.Set(fmt.Sprintf("pushed-%02d", i), Result{Status: StatusUp})
	}
	return r
}

// BenchmarkProbe measures one readiness evaluation for registries of
// different sizes. Checkers run concurrently, each in its own goroutine;
// pushed components are read from memory.
func BenchmarkProbe(b *testing.B) {
	cases := []struct {
		name             string
		checkers, pushed int
	}{
		{"empty", 0, 0},
		{"pushed-1", 0, 1},
		{"pushed-10", 0, 10},
		{"checkers-1", 1, 0},
		{"checkers-3", 3, 0},
		{"checkers-10", 10, 0},
		{"mixed-3+3", 3, 3},
	}
	ctx := context.Background()
	for _, tc := range cases {
		r := registry(tc.checkers, tc.pushed)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = r.Ready(ctx)
			}
		})
	}
}

// BenchmarkProbeParallel measures concurrent probes of the same registry,
// as when Consul, Kubernetes and a load balancer poll at once. Concurrent
// probes share the execution of each checker.
func BenchmarkProbeParallel(b *testing.B) {
	for _, tc := range []struct {
		name             string
		checkers, pushed int
	}{{"pushed-3", 0, 3}, {"checkers-3", 3, 0}} {
		r := registry(tc.checkers, tc.pushed)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				ctx := context.Background()
				for pb.Next() {
					_ = r.Ready(ctx)
				}
			})
		})
	}
}

// BenchmarkSet measures pushing a component state, which applications do
// from their own code paths (a consumer losing its broker, a cache warming
// up), with and without a listener such as the TTL heartbeat.
func BenchmarkSet(b *testing.B) {
	res := Result{Status: StatusUp}
	withDetails := Result{Status: StatusDegraded, Details: map[string]any{"lag": 12}}
	b.Run("plain", func(b *testing.B) {
		r := NewRegistry(0)
		b.ReportAllocs()
		for b.Loop() {
			r.Set("broker", res)
		}
	})
	b.Run("details", func(b *testing.B) {
		r := NewRegistry(0)
		b.ReportAllocs()
		for b.Loop() {
			r.Set("broker", withDetails)
		}
	})
	b.Run("listener", func(b *testing.B) {
		r := NewRegistry(0)
		r.OnPush(func(Status) {})
		b.ReportAllocs()
		for b.Loop() {
			r.Set("broker", res)
		}
	})
	b.Run("parallel", func(b *testing.B) {
		r := NewRegistry(0)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				r.Set("broker", res)
			}
		})
	})
}

// BenchmarkHandler measures the HTTP handler: probe plus JSON encoding.
func BenchmarkHandler(b *testing.B) {
	r := registry(2, 1)
	cases := []struct {
		name   string
		method string
		opts   HandlerOptions
	}{
		{"get", http.MethodGet, HandlerOptions{}},
		{"get-hide-details", http.MethodGet, HandlerOptions{HideDetails: true}},
		{"head", http.MethodHead, HandlerOptions{}},
	}
	for _, tc := range cases {
		h := Handler(r.Ready, tc.opts)
		req := httptest.NewRequest(tc.method, "/health/ready", nil)
		b.Run(tc.name, func(b *testing.B) {
			w := &discardWriter{h: http.Header{}}
			b.ReportAllocs()
			for b.Loop() {
				clear(w.h)
				h.ServeHTTP(w, req)
			}
		})
	}
}

// BenchmarkStatus measures the status helpers used by every probe.
func BenchmarkStatus(b *testing.B) {
	b.Run("Worse", func(b *testing.B) {
		s := StatusUp
		b.ReportAllocs()
		for b.Loop() {
			s = s.Worse(StatusDegraded)
		}
		_ = s
	})
	b.Run("StatusCode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = StatusCode(StatusDegraded, 0)
		}
	})
}
