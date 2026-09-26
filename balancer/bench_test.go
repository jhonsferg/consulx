//go:build bench

// Benchmarks of the balancer package, excluded from normal builds by the
// bench build tag. See docs/benchmarks.md.
//
// Next is the one ConsulX call an application makes per outgoing request,
// so it is the hottest path of the library.
package balancer

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/jhonsferg/consulx/discovery"
	"github.com/jhonsferg/consulx/internal/fakeconsul"
)

var sizes = []int{3, 20, 100}

var strategies = []struct {
	name string
	s    Strategy
}{
	{"round-robin", RoundRobin()},
	{"random", Random()},
	{"weighted", Weighted()},
}

// list returns n passing instances with uneven weights.
func list(n int) []discovery.ServiceInstance {
	out := make([]discovery.ServiceInstance, n)
	for i := range out {
		out[i] = inst(fmt.Sprintf("p%03d", i), 1+i%5, 1, discovery.StatusPassing)
	}
	return out
}

// entries returns n health entries shaped like real ones: tags, metadata
// and a check, so the copy made per call has realistic size.
func entries(n int) []*api.ServiceEntry {
	out := make([]*api.ServiceEntry, n)
	for i := range out {
		e := healthEntry(fmt.Sprintf("p%03d", i))
		e.Service.Address = "10.0.0." + strconv.Itoa(i%250+1)
		e.Service.Meta = map[string]string{"version": "2", "zone": "a"}
		e.Service.Tags = []string{"v2", "blue"}
		e.Service.Weights = api.AgentWeights{Passing: 1 + i%5, Warning: 1}
		out[i] = e
	}
	return out
}

// newBench returns a balancer over services services of n instances each,
// with every watch already running.
func newBench(b *testing.B, s Strategy, n, services int, opts ...Option) *Balancer {
	b.Helper()
	a := fakeconsul.New("1.22.7")
	b.Cleanup(a.Close)
	for i := range services {
		a.SetHealth(serviceName(i), entries(n)...)
	}
	raw, err := api.NewClient(&api.Config{Address: a.URL()})
	if err != nil {
		b.Fatal(err)
	}
	lb := New(context.Background(), discovery.New(raw, discovery.Config{}), s, opts...)
	b.Cleanup(func() { _ = lb.Close() })
	for i := range services {
		if _, err := lb.Next(context.Background(), serviceName(i)); err != nil {
			b.Fatal(err)
		}
	}
	return lb
}

func serviceName(i int) string {
	if i == 0 {
		return "payments"
	}
	return "service-" + strconv.Itoa(i)
}

// BenchmarkPicker measures each strategy alone, without the watch.
func BenchmarkPicker(b *testing.B) {
	for _, st := range strategies {
		for _, n := range sizes {
			l := list(n)
			p := st.s.NewPicker()
			b.Run(fmt.Sprintf("%s-%d", st.name, n), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					_ = p.Pick(l)
				}
			})
		}
	}
}

// BenchmarkNextStrategy measures Next end to end for every strategy and
// instance count: service lookup, snapshot read, pick and the copy of the
// chosen instance returned to the caller.
func BenchmarkNextStrategy(b *testing.B) {
	ctx := context.Background()
	for _, st := range strategies {
		for _, n := range sizes {
			b.Run(fmt.Sprintf("%s-%d", st.name, n), func(b *testing.B) {
				lb := newBench(b, st.s, n, 1)
				b.ReportAllocs()
				for b.Loop() {
					if _, err := lb.Next(ctx, "payments"); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkNextOptions measures the cost of each balancer option on Next.
func BenchmarkNextOptions(b *testing.B) {
	ctx := context.Background()
	cases := []struct {
		name string
		opts []Option
	}{
		{"default", nil},
		{"stale-grace", []Option{WithStaleGrace(10 * time.Second)}},
		{"no-idle-release", []Option{WithIdleTimeout(0)}},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			lb := newBench(b, RoundRobin(), 20, 1, tc.opts...)
			b.ReportAllocs()
			for b.Loop() {
				_, _ = lb.Next(ctx, "payments")
			}
		})
	}
}

// BenchmarkNextManyServices measures Next when the balancer tracks many
// services, as a gateway or an aggregator does.
func BenchmarkNextManyServices(b *testing.B) {
	ctx := context.Background()
	for _, services := range []int{1, 10, 50} {
		b.Run(strconv.Itoa(services), func(b *testing.B) {
			lb := newBench(b, RoundRobin(), 3, services)
			names := make([]string, services)
			for i := range names {
				names[i] = serviceName(i)
			}
			i := 0
			b.ReportAllocs()
			for b.Loop() {
				_, _ = lb.Next(ctx, names[i%services])
				i++
			}
		})
	}
}

// BenchmarkNextContention measures Next under concurrent load for each
// strategy, the way HTTP handlers of a busy service call it.
func BenchmarkNextContention(b *testing.B) {
	for _, st := range strategies {
		b.Run(st.name, func(b *testing.B) {
			lb := newBench(b, st.s, 20, 1, WithStaleGrace(10*time.Second))
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				ctx := context.Background()
				for pb.Next() {
					_, _ = lb.Next(ctx, "payments")
				}
			})
		})
	}
	b.Run("many-services", func(b *testing.B) {
		const services = 10
		lb := newBench(b, RoundRobin(), 3, services)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			ctx := context.Background()
			i := 0
			for pb.Next() {
				_, _ = lb.Next(ctx, serviceName(i%services))
				i++
			}
		})
	})
}

// BenchmarkFirstUse measures the first Next for a service: starting its
// watch and waiting for the initial response. Services seen for the first
// time, or again after the idle timeout, pay it once.
func BenchmarkFirstUse(b *testing.B) {
	a := fakeconsul.New("1.22.7")
	b.Cleanup(a.Close)
	a.SetHealth("payments", entries(3)...)
	raw, err := api.NewClient(&api.Config{Address: a.URL()})
	if err != nil {
		b.Fatal(err)
	}
	d := discovery.New(raw, discovery.Config{})
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		lb := New(ctx, d, RoundRobin())
		if _, err := lb.Next(ctx, "payments"); err != nil {
			b.Fatal(err)
		}
		_ = lb.Close()
	}
}

// BenchmarkNextEndpoint measures NextEndpoint, the zero-allocation
// alternative to Next for callers that only need where to connect.
func BenchmarkNextEndpoint(b *testing.B) {
	ctx := context.Background()
	for _, st := range strategies {
		for _, n := range sizes {
			b.Run(fmt.Sprintf("%s-%d", st.name, n), func(b *testing.B) {
				lb := newBench(b, st.s, n, 1, WithStaleGrace(10*time.Second))
				b.ReportAllocs()
				for b.Loop() {
					if _, err := lb.NextEndpoint(ctx, "payments"); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
	b.Run("parallel", func(b *testing.B) {
		lb := newBench(b, RoundRobin(), 20, 1, WithStaleGrace(10*time.Second))
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			ctx := context.Background()
			for pb.Next() {
				_, _ = lb.NextEndpoint(ctx, "payments")
			}
		})
	})
	b.Run("custom-picker", func(b *testing.B) {
		first := StrategyFunc(func() Picker {
			return PickerFunc(func(l []discovery.ServiceInstance) discovery.ServiceInstance { return l[len(l)/2] })
		})
		lb := newBench(b, first, 20, 1)
		b.ReportAllocs()
		for b.Loop() {
			_, _ = lb.NextEndpoint(ctx, "payments")
		}
	})
}
