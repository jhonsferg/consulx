// Package otel integrates ConsulX with OpenTelemetry.
//
// Metrics:
//
//	consul, err := consulx.New(consulx.WithMetrics(otel.NewMetrics(otel.Meter())), ...)
//
// Tracing of every request to Consul:
//
//	consul, err := consulx.New(consulx.WithHTTPClient(otel.HTTPClient(nil)), ...)
package otel

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/jhonsferg/consulx"
)

// ScopeName is the instrumentation scope used by Meter.
const ScopeName = "github.com/jhonsferg/consulx"

// Meter returns the ConsulX meter from the global MeterProvider.
func Meter() metric.Meter { return otel.Meter(ScopeName) }

// Metrics implements consulx.Metrics with OpenTelemetry instruments. It is
// safe for concurrent use.
type Metrics struct {
	meter metric.Meter

	mu         sync.Mutex
	counters   map[string]metric.Int64Counter
	gauges     map[string]metric.Float64Gauge
	histograms map[string]metric.Float64Histogram
}

var _ consulx.Metrics = (*Metrics)(nil)

// NewMetrics returns a Metrics recording on meter.
func NewMetrics(meter metric.Meter) *Metrics {
	return &Metrics{
		meter:      meter,
		counters:   map[string]metric.Int64Counter{},
		gauges:     map[string]metric.Float64Gauge{},
		histograms: map[string]metric.Float64Histogram{},
	}
}

// OpenTelemetry names use dots; the Prometheus-style suffixes are dropped
// because units and types carry that information.
func instrumentName(name string) string {
	name = strings.TrimSuffix(name, "_total")
	name = strings.TrimSuffix(name, "_seconds")
	return strings.ReplaceAll(name, "_", ".")
}

func attrs(labels []consulx.Label) metric.MeasurementOption {
	kv := make([]attribute.KeyValue, len(labels))
	for i, l := range labels {
		kv[i] = attribute.String(l.Key, l.Value)
	}
	return metric.WithAttributes(kv...)
}

// IncCounter implements consulx.Metrics.
func (m *Metrics) IncCounter(name string, labels ...consulx.Label) {
	m.mu.Lock()
	c, ok := m.counters[name]
	if !ok {
		var err error
		if c, err = m.meter.Int64Counter(instrumentName(name)); err != nil {
			m.mu.Unlock()
			return
		}
		m.counters[name] = c
	}
	m.mu.Unlock()
	c.Add(context.Background(), 1, attrs(labels))
}

// SetGauge implements consulx.Metrics.
func (m *Metrics) SetGauge(name string, value float64, labels ...consulx.Label) {
	m.mu.Lock()
	g, ok := m.gauges[name]
	if !ok {
		var err error
		if g, err = m.meter.Float64Gauge(instrumentName(name)); err != nil {
			m.mu.Unlock()
			return
		}
		m.gauges[name] = g
	}
	m.mu.Unlock()
	g.Record(context.Background(), value, attrs(labels))
}

// ObserveDuration implements consulx.Metrics, in seconds.
func (m *Metrics) ObserveDuration(name string, d time.Duration, labels ...consulx.Label) {
	m.mu.Lock()
	h, ok := m.histograms[name]
	if !ok {
		var err error
		if h, err = m.meter.Float64Histogram(instrumentName(name), metric.WithUnit("s")); err != nil {
			m.mu.Unlock()
			return
		}
		m.histograms[name] = h
	}
	m.mu.Unlock()
	h.Record(context.Background(), d.Seconds(), attrs(labels))
}

// HTTPClient returns an http.Client whose transport creates a client span
// for every request to Consul. base defaults to a clone of
// http.DefaultTransport. The client has no Timeout on purpose: ConsulX
// bounds each request with a context, and blocking queries last minutes.
// Pass it with consulx.WithHTTPClient; TLS then must be configured on base.
func HTTPClient(base http.RoundTripper, opts ...otelhttp.Option) *http.Client {
	if base == nil {
		base = http.DefaultTransport.(*http.Transport).Clone()
	}
	return &http.Client{Transport: otelhttp.NewTransport(base, opts...)}
}
