//go:build bench

// Benchmarks of the discovery package, excluded from normal builds by the
// bench build tag. See docs/benchmarks.md.
//
// Benchmarks that query the in-process fake agent report allocations of the
// whole process, including the fake agent's HTTP server; their "raw-api"
// variants do the same request with the official client, so the difference
// is what ConsulX adds.
package discovery

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/jhonsferg/consulx/internal/fakeconsul"
)

// sizes are the instance counts used across benchmarks: a small service, a
// typical one and a large one.
var sizes = []int{3, 20, 100}

// entries returns n passing health entries of the payments service. version
// is written to the metadata, so different versions are different states.
func entries(n, version int) []*api.ServiceEntry {
	out := make([]*api.ServiceEntry, n)
	for i := range out {
		id := fmt.Sprintf("p%03d", i)
		out[i] = entry(id, "10.0.0."+strconv.Itoa(i%250+1), 8080, api.HealthPassing, "v1", "blue")
		out[i].Service.Meta["build"] = strconv.Itoa(version)
	}
	return out
}

// instances converts entries like a query does.
func instances(n, version int) []ServiceInstance {
	out := make([]ServiceInstance, 0, n)
	for _, e := range entries(n, version) {
		out = append(out, fromEntry(e))
	}
	sortInstances(out)
	return out
}

// benchClient starts a fake agent with n instances and returns a client with
// no pacing, so watch benchmarks measure work and not the rate limiter.
func benchClient(b *testing.B, n int) (*fakeconsul.Agent, *api.Client, *Client) {
	b.Helper()
	a := fakeconsul.New("1.22.7")
	b.Cleanup(a.Close)
	a.SetHealth("payments", entries(n, 0)...)
	raw, err := api.NewClient(&api.Config{Address: a.URL()})
	if err != nil {
		b.Fatal(err)
	}
	return a, raw, New(raw, Config{MinInterval: time.Nanosecond, WaitTime: time.Minute})
}

// BenchmarkConvert measures turning a health entry into an instance, done
// for every instance of every response.
func BenchmarkConvert(b *testing.B) {
	for _, tc := range []struct {
		name string
		e    *api.ServiceEntry
	}{
		{"minimal", &api.ServiceEntry{
			Node:    &api.Node{Node: "n", Address: "10.0.0.1"},
			Service: &api.AgentService{ID: "p1", Service: "payments", Port: 80},
			Checks:  api.HealthChecks{{Status: api.HealthPassing}},
		}},
		{"typical", entries(1, 0)[0]},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = fromEntry(tc.e)
			}
		})
	}
}

// BenchmarkSort measures ordering a response, done on every fetch.
func BenchmarkSort(b *testing.B) {
	for _, n := range sizes {
		src := instances(n, 0)
		// Reverse so every run sorts real work, not an ordered list.
		rev := make([]ServiceInstance, n)
		for i := range src {
			rev[n-1-i] = src[i]
		}
		list := make([]ServiceInstance, n)
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				copy(list, rev)
				sortInstances(list)
			}
		})
	}
}

// BenchmarkDiff measures building a watch event, done on every change.
func BenchmarkDiff(b *testing.B) {
	for _, n := range sizes {
		prev := instances(n, 0)
		same := instances(n, 0)
		changed := instances(n, 0)
		changed[0].Meta = map[string]string{"build": "1"}
		b.Run(fmt.Sprintf("%d-one-changed", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = diff(prev, changed)
			}
		})
		b.Run(fmt.Sprintf("%d-initial", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = diff(nil, same)
			}
		})
	}
}

// BenchmarkClone measures the copies that keep watch snapshots immutable.
func BenchmarkClone(b *testing.B) {
	for _, n := range sizes {
		list := instances(n, 0)
		b.Run(fmt.Sprintf("all-%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = cloneAll(list)
			}
		})
	}
	one := instances(1, 0)[0]
	b.Run("one", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = one.clone()
		}
	})
}

// BenchmarkQueryBuild measures building the request options of a query,
// done on every request of every query and watch.
func BenchmarkQueryBuild(b *testing.B) {
	c := New(nil, Config{})
	ctx := context.Background()
	cases := []struct {
		name string
		q    Query
	}{
		{"plain", c.Service("payments")},
		{"tag", c.Service("payments").Tag("v1")},
		{"meta", c.Service("payments").Meta("version", "2")},
		{"meta-filter", c.Service("payments").Meta("version", "2").Meta("zone", "a").Filter(`Service.Port == 8080`)},
		{"stale-cached", c.Service("payments").Consistency(Stale).Cached()},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				optionsSink = tc.q.options(ctx) // escapes, as when passed to the official client
			}
		})
	}
	b.Run("chain", func(b *testing.B) {
		base := c.Service("payments")
		b.ReportAllocs()
		for b.Loop() {
			_ = base.Tag("v1").Passing().Datacenter("dc1")
		}
	})
}

// BenchmarkQuery measures a complete one-shot query (Query.All) against the
// fake agent, next to the same request through the official client.
func BenchmarkQuery(b *testing.B) {
	ctx := context.Background()
	for _, n := range sizes {
		_, raw, c := benchClient(b, n)
		b.Run(fmt.Sprintf("consulx-%d", n), func(b *testing.B) {
			q := c.Service("payments")
			b.ReportAllocs()
			for b.Loop() {
				if _, err := q.All(ctx); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("raw-api-%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, _, err := raw.Health().Service("payments", "", true, (&api.QueryOptions{}).WithContext(ctx)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkWatchUpdate measures the complete propagation of one change: the
// agent wakes the blocking query, the response is decoded, converted,
// compared, stored and delivered as an event. The raw-api variant is the
// blocking query alone, as a hand-written watch loop would issue it.
func BenchmarkWatchUpdate(b *testing.B) {
	ctx := context.Background()
	for _, n := range sizes {
		b.Run(fmt.Sprintf("consulx-%d", n), func(b *testing.B) {
			a, _, c := benchClient(b, n)
			w, err := c.Watch(ctx, "payments")
			if err != nil {
				b.Fatal(err)
			}
			defer w.Close()
			<-w.Events() // initial state
			versions := [2][]*api.ServiceEntry{entries(n, 1), entries(n, 2)}
			i := 0
			b.ReportAllocs()
			for b.Loop() {
				i++
				a.SetHealth("payments", versions[i%2]...)
				<-w.Events()
			}
		})
		b.Run(fmt.Sprintf("raw-api-%d", n), func(b *testing.B) {
			a, raw, _ := benchClient(b, n)
			_, meta, err := raw.Health().Service("payments", "", true, nil)
			if err != nil {
				b.Fatal(err)
			}
			index := meta.LastIndex
			versions := [2][]*api.ServiceEntry{entries(n, 1), entries(n, 2)}
			i := 0
			b.ReportAllocs()
			for b.Loop() {
				i++
				a.SetHealth("payments", versions[i%2]...)
				_, meta, err := raw.Health().Service("payments", "", true,
					(&api.QueryOptions{WaitIndex: index, WaitTime: time.Minute}).WithContext(ctx))
				if err != nil {
					b.Fatal(err)
				}
				index = meta.LastIndex
			}
		})
	}
}

// BenchmarkWatchRead measures reading a running watch: Pick is the zero-copy
// path the balancer uses, Instances copies the whole list for the caller.
func BenchmarkWatchRead(b *testing.B) {
	ctx := context.Background()
	first := func(l []ServiceInstance) (ServiceInstance, bool) {
		if len(l) == 0 {
			return ServiceInstance{}, false
		}
		return l[0], true
	}
	for _, n := range sizes {
		_, _, c := benchClient(b, n)
		w, err := c.Watch(ctx, "payments")
		if err != nil {
			b.Fatal(err)
		}
		<-w.Ready()
		b.Run(fmt.Sprintf("Pick-%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _ = w.Pick(first)
			}
		})
		b.Run(fmt.Sprintf("Pick-parallel-%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_, _ = w.Pick(first)
				}
			})
		})
		b.Run(fmt.Sprintf("PickEndpoint-%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _ = w.Current().PickEndpoint(func([]ServiceInstance) (int, bool) { return 0, true })
			}
		})
		b.Run(fmt.Sprintf("Instances-%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = w.Instances()
			}
		})
		_ = w.Close()
	}
}

// BenchmarkInstanceHelpers measures the accessors applications call per
// request to build URLs and pick ports.
func BenchmarkInstanceHelpers(b *testing.B) {
	i := instances(1, 0)[0]
	i.Ports = []Port{{Name: "http", Port: 8080, Default: true}, {Name: "grpc", Port: 9090}}
	b.Run("HostPort", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = i.HostPort()
		}
	})
	b.Run("URL", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = i.URL()
		}
	})
	b.Run("PortNamed", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = i.PortNamed("grpc")
		}
	})
	b.Run("Weight", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = i.Weight()
		}
	})
}

// optionsSink keeps request options on the heap, as the official client
// does with them, so the benchmark does not measure a stack allocation.
var optionsSink *api.QueryOptions
