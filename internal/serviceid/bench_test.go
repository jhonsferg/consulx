//go:build bench

// Benchmarks of service ID generation, excluded from normal builds by the
// bench build tag. See docs/benchmarks.md.
package serviceid

import "testing"

func BenchmarkSanitize(b *testing.B) {
	for _, tc := range []struct{ name, in string }{
		{"clean", "orders-api"},
		{"dirty", "Orders API (EU) / v2"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = Sanitize(tc.in)
			}
		})
	}
}

func BenchmarkGenerate(b *testing.B) {
	b.Run("HostnamePort", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = HostnamePort("orders-api", "orders-api-host-1", 8080)
		}
	})
	b.Run("Random", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = Random("orders-api")
		}
	})
}
