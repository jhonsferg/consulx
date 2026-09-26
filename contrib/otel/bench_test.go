//go:build bench

// Benchmarks of the OpenTelemetry adapter, excluded from normal builds by
// the bench build tag. Every ConsulX measurement goes through these calls.
// See docs/benchmarks.md in the core module.
package otel

import (
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/jhonsferg/consulx"
)

func BenchmarkMetrics(b *testing.B) {
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader()))
	b.Cleanup(func() { _ = provider.Shutdown(b.Context()) })
	m := NewMetrics(provider.Meter(ScopeName))
	op := consulx.Label{Key: "operation", Value: "register"}
	b.Run("IncCounter", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			m.IncCounter(consulx.MetricRegisterTotal)
		}
	})
	b.Run("IncCounter-label", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			m.IncCounter(consulx.MetricConsulRequestsTotal, op)
		}
	})
	b.Run("SetGauge", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			m.SetGauge(consulx.MetricHealthStatus, 1)
		}
	})
	b.Run("ObserveDuration-label", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			m.ObserveDuration(consulx.MetricConsulRequestDuration, 3*time.Millisecond, op)
		}
	})
	b.Run("IncCounter-label-parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				m.IncCounter(consulx.MetricConsulRequestsTotal, op)
			}
		})
	})
}
