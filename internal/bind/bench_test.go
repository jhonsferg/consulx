//go:build bench

// Benchmarks of the binder, excluded from normal builds by the bench build
// tag. Binding runs on every configuration load and reload. See
// docs/benchmarks.md.
package bind

import (
	"fmt"
	"testing"
	"time"
)

type flatConfig struct {
	Name     string        `consul:"name"`
	Port     int           `consul:"port"`
	Debug    bool          `consul:"debug"`
	Ratio    float64       `consul:"ratio"`
	Timeout  time.Duration `consul:"timeout"`
	LogLevel string        `consul:"log-level" default:"info"`
}

// BenchmarkBindShapes measures binding for the shapes a configuration
// usually has.
func BenchmarkBindShapes(b *testing.B) {
	cases := []struct {
		name string
		tree map[string]any
		dst  func() any
		opts Options
	}{
		{
			name: "flat",
			tree: map[string]any{"name": "orders", "port": "8080", "debug": "true", "ratio": "0.5", "timeout": "2s"},
			dst:  func() any { return new(flatConfig) },
		},
		{
			name: "flat-typed-values", // YAML and JSON documents carry typed values
			tree: map[string]any{"name": "orders", "port": 8080, "debug": true, "ratio": 0.5, "timeout": "2s"},
			dst:  func() any { return new(flatConfig) },
		},
		{
			name: "fuzzy-keys", // keys matched through normalisation, not exactly
			tree: map[string]any{"NAME": "orders", "Port": "8080", "DEBUG": "true", "Ratio": "0.5", "Time_out": "2s", "log_level": "warn"},
			dst:  func() any { return new(flatConfig) },
		},
		{
			name: "nested",
			tree: map[string]any{
				"name":     "orders",
				"database": map[string]any{"host": "db", "port": "5432", "max-conns": "20", "timeout": "2s", "tls": "true"},
			},
			dst: func() any { return new(AppConfig) },
		},
		{
			name: "collections",
			tree: map[string]any{
				"database": map[string]any{"host": "db"},
				"features": map[string]any{"a": "true", "b": "false", "c": "true"},
				"limits":   map[string]any{"rps": "100", "burst": "20"},
				"hosts":    "a,b,c,d",
				"ports":    map[string]any{"0": "80", "1": "81"},
				"replicas": map[string]any{"0": map[string]any{"host": "r1"}, "1": map[string]any{"host": "r2"}},
				"labels":   map[string]any{"team": "payments", "tier": "1"},
			},
			dst: func() any { return new(AppConfig) },
		},
		{
			name: "error-unused",
			tree: map[string]any{"name": "orders", "port": "8080", "debug": "true", "ratio": "0.5", "timeout": "2s"},
			dst:  func() any { return new(flatConfig) },
			opts: Options{ErrorUnused: true},
		},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := Bind(tc.tree, tc.dst(), tc.opts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkLookup measures finding the key of a field, exact and through
// normalisation, in trees of different sizes.
func BenchmarkLookup(b *testing.B) {
	for _, n := range []int{5, 50} {
		tree := map[string]any{}
		for i := range n {
			tree[fmt.Sprintf("key-%d", i)] = "v"
		}
		tree["max-conns"] = "20"
		b.Run(fmt.Sprintf("exact-%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _, _ = lookup(tree, "max-conns")
			}
		})
		b.Run(fmt.Sprintf("normalised-%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _, _ = lookup(tree, "MaxConns")
			}
		})
	}
}

// BenchmarkSameKey measures the key comparison run for every field and key.
func BenchmarkSameKey(b *testing.B) {
	b.Run("equal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = sameKey("MaxConnections", "max-connections")
		}
	})
	b.Run("different", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = sameKey("MaxConnections", "min-connections")
		}
	})
}
