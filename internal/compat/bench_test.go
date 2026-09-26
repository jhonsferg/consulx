//go:build bench

// Benchmarks of version handling, excluded from normal builds by the bench
// build tag. Versions are parsed on every (re)connection and checked on
// every registration. See docs/benchmarks.md.
package compat

import "testing"

func BenchmarkParse(b *testing.B) {
	for _, v := range []string{"1.22.7", "2.0.4+ent", "v1.21.0-rc1+ent.fips1402"} {
		b.Run(v, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Parse(v); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSupports(b *testing.B) {
	v, _ := Parse("1.22.7")
	b.ReportAllocs()
	for b.Loop() {
		_ = v.Supports(MultiPort)
		_ = v.Supports(Namespaces)
	}
}
