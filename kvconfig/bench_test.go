//go:build bench

// Benchmarks of the kvconfig package, excluded from normal builds by the
// bench build tag. See docs/benchmarks.md.
//
// Benchmarks that read from the in-process fake agent report allocations of
// the whole process, including the fake agent's HTTP server; the "raw-api"
// variant of Load reads the same folders with the official client, so the
// difference is what ConsulX adds.
package kvconfig

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/jhonsferg/consulx/internal/fakeconsul"
)

const benchYAML = `
database:
  host: db.internal
  port: 5432
  max-conns: 20
  timeout: 2s
features:
  checkout: true
  search: false
log-level: debug
`

const benchJSON = `{"database":{"host":"db.internal","port":5432,"max-conns":20,"timeout":"2s"},
"features":{"checkout":true,"search":false},"log-level":"debug"}`

// kvPairs returns the benchmark configuration as individual keys under
// prefix, plus extra keys to vary the size.
func kvPairs(prefix string, extra int) api.KVPairs {
	pairs := api.KVPairs{
		{Key: prefix + "database/host", Value: []byte("db.internal")},
		{Key: prefix + "database/port", Value: []byte("5432")},
		{Key: prefix + "database/max-conns", Value: []byte("20")},
		{Key: prefix + "database/timeout", Value: []byte("2s")},
		{Key: prefix + "features/checkout", Value: []byte("true")},
		{Key: prefix + "features/search", Value: []byte("false")},
		{Key: prefix + "log-level", Value: []byte("debug")},
	}
	for i := range extra {
		pairs = append(pairs, &api.KVPair{Key: fmt.Sprintf("%sextra/group-%d/key-%d", prefix, i%10, i), Value: []byte(strconv.Itoa(i))})
	}
	return pairs
}

// benchLoader starts a fake agent holding the configuration in the
// application and orders-api folders and returns a loader without pacing.
func benchLoader(b *testing.B, cfg Config) (*fakeconsul.Agent, *api.Client, *Loader) {
	b.Helper()
	a := fakeconsul.New("1.22.7")
	b.Cleanup(a.Close)
	a.PutKV("config/application/log-level", "info")
	for _, p := range kvPairs("config/orders-api/", 0) {
		a.PutKV(p.Key, string(p.Value))
	}
	raw, err := api.NewClient(&api.Config{Address: a.URL()})
	if err != nil {
		b.Fatal(err)
	}
	cfg.Name = "orders-api"
	cfg.MinInterval = time.Nanosecond
	cfg.WaitTime = time.Minute
	return a, raw, New(raw, cfg)
}

// BenchmarkDecode measures turning the pairs of one folder into a tree, for
// every format, done for every folder on every load and reload.
func BenchmarkDecode(b *testing.B) {
	const prefix = "config/orders-api/"
	cases := []struct {
		name   string
		format Format
		pairs  api.KVPairs
	}{
		{"kv-7", FormatKeyValue, kvPairs(prefix, 0)},
		{"kv-107", FormatKeyValue, kvPairs(prefix, 100)},
		{"yaml", FormatYAML, api.KVPairs{{Key: prefix + "data", Value: []byte(benchYAML)}}},
		{"json", FormatJSON, api.KVPairs{{Key: prefix + "data", Value: []byte(benchJSON)}}},
	}
	for _, tc := range cases {
		l := New(&api.Client{}, Config{Format: tc.format})
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := l.decode(prefix, tc.pairs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkMerge measures layering folders by precedence.
func BenchmarkMerge(b *testing.B) {
	l := New(&api.Client{}, Config{})
	base, _ := l.decode("p/", kvPairs("p/", 100))
	over, _ := l.decode("p/", kvPairs("p/", 0))
	b.ReportAllocs()
	for b.Loop() {
		dst := map[string]any{}
		merge(dst, base)
		merge(dst, over)
	}
}

// BenchmarkLoad measures reading and merging every folder of a service
// (application, orders-api) from the fake agent.
func BenchmarkLoad(b *testing.B) {
	ctx := context.Background()
	_, raw, l := benchLoader(b, Config{})
	b.Run("consulx", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := l.Load(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("raw-api", func(b *testing.B) {
		contexts := l.Contexts()
		b.ReportAllocs()
		for b.Loop() {
			for _, prefix := range contexts {
				if _, _, err := raw.KV().List(prefix, (&api.QueryOptions{}).WithContext(ctx)); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}

// BenchmarkLoadInto measures a complete typed load: read, merge, bind,
// validate.
func BenchmarkLoadInto(b *testing.B) {
	ctx := context.Background()
	_, _, l := benchLoader(b, Config{})
	b.Run("all", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var cfg AppConfig
			if err := l.LoadInto(ctx, &cfg); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("subtree", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var db DB
			if err := l.Bind(ctx, "database", &db); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkBindTree measures binding an already loaded tree, the part of a
// reload that does not depend on the network.
func BenchmarkBindTree(b *testing.B) {
	l := New(&api.Client{}, Config{})
	tree, _ := l.decode("p/", kvPairs("p/", 0))
	for _, unused := range []bool{false, true} {
		l.cfg.ErrorUnused = unused
		b.Run(fmt.Sprintf("error-unused-%t", unused), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var cfg AppConfig
				if err := l.bindTree(tree, "", &cfg); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkWatcherCurrent measures reading the watched value, which
// applications do on every request that needs configuration.
func BenchmarkWatcherCurrent(b *testing.B) {
	_, _, l := benchLoader(b, Config{})
	w, err := Watch[AppConfig](context.Background(), l)
	if err != nil {
		b.Fatal(err)
	}
	defer w.Close()
	b.Run("sequential", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = w.Current()
		}
	})
	b.Run("parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			var n int
			for pb.Next() {
				n += w.Current().Database.Port // keep the read from being optimised away
			}
			_ = n
		})
	})
}

// BenchmarkWatchReload measures the complete propagation of one change:
// every folder watch wakes, the configuration is read again, bound,
// validated, compared and published.
func BenchmarkWatchReload(b *testing.B) {
	a, _, l := benchLoader(b, Config{})
	w, err := Watch[AppConfig](context.Background(), l)
	if err != nil {
		b.Fatal(err)
	}
	defer w.Close()
	hosts := [2]string{"db-a.internal", "db-b.internal"}
	i := 0
	b.ReportAllocs()
	for b.Loop() {
		i++
		a.PutKV("config/orders-api/database/host", hosts[i%2])
		<-w.Changes()
	}
}
