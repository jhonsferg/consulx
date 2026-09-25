// Package prometheus exports ConsulX metrics to Prometheus.
//
//	m := prometheus.New(prom.DefaultRegisterer)
//	consul, err := consulx.New(consulx.WithMetrics(m), ...)
//
// Metrics are registered lazily, the first time ConsulX reports them, with
// the label names used at that moment. ConsulX always uses the same label
// names for a given metric, so the set is stable.
package prometheus

import (
	"strings"
	"sync"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"

	"github.com/jhonsferg/consulx"
)

// Metrics implements consulx.Metrics. It is safe for concurrent use.
type Metrics struct {
	reg prom.Registerer

	mu         sync.Mutex
	counters   map[string]*prom.CounterVec
	gauges     map[string]*prom.GaugeVec
	histograms map[string]*prom.HistogramVec
}

var _ consulx.Metrics = (*Metrics)(nil)

// New returns a Metrics registering its collectors on reg.
func New(reg prom.Registerer) *Metrics {
	return &Metrics{
		reg:        reg,
		counters:   map[string]*prom.CounterVec{},
		gauges:     map[string]*prom.GaugeVec{},
		histograms: map[string]*prom.HistogramVec{},
	}
}

var help = map[string]string{
	consulx.MetricRegisterTotal:            "Successful service registrations.",
	consulx.MetricRegisterErrorsTotal:      "Failed service registrations.",
	consulx.MetricDeregisterTotal:          "Successful service deregistrations.",
	consulx.MetricDeregisterErrorsTotal:    "Failed service deregistrations.",
	consulx.MetricDiscoveryRequestsTotal:   "Discovery requests sent to Consul.",
	consulx.MetricDiscoveryErrorsTotal:     "Failed discovery requests.",
	consulx.MetricConsulRequestsTotal:      "Requests sent to Consul by the runtime.",
	consulx.MetricConsulRequestErrorsTotal: "Failed requests sent to Consul by the runtime.",
	consulx.MetricConsulRequestDuration:    "Duration of runtime requests to Consul.",
	consulx.MetricReconnectTotal:           "Recoveries after Consul was unavailable.",
	consulx.MetricHealthStatus:             "Readiness reported to Consul: 1 up, 0.5 degraded, 0 down.",
	consulx.MetricRuntimeState:             "Runtime state: 0 idle, 1 starting, 2 running, 3 degraded, 4 stopping, 5 stopped.",
	consulx.MetricConfigReloadTotal:        "Configuration reloads applied.",
	consulx.MetricConfigReloadErrorsTotal:  "Configuration reloads that failed or were rejected.",
	consulx.MetricErrorsDroppedTotal:       "Runtime errors dropped because Errors() was not read.",
}

func split(labels []consulx.Label) (names, values []string) {
	for _, l := range labels {
		names = append(names, l.Key)
		values = append(values, l.Value)
	}
	return names, values
}

func helpFor(name string) string {
	if h, ok := help[name]; ok {
		return h
	}
	return "ConsulX metric " + strings.TrimPrefix(name, "consulx_") + "."
}

// register registers c, reusing an identical collector registered earlier
// (for example by another Metrics sharing the registry).
func register[C prom.Collector](reg prom.Registerer, c C) C {
	if err := reg.Register(c); err != nil {
		if are, ok := err.(prom.AlreadyRegisteredError); ok {
			if existing, ok := are.ExistingCollector.(C); ok {
				return existing
			}
		}
		// A conflicting definition must not crash the service; drop the
		// metric rather than panic in a monitoring path.
	}
	return c
}

// IncCounter implements consulx.Metrics.
func (m *Metrics) IncCounter(name string, labels ...consulx.Label) {
	names, values := split(labels)
	m.mu.Lock()
	vec, ok := m.counters[name]
	if !ok {
		vec = register(m.reg, prom.NewCounterVec(prom.CounterOpts{Name: name, Help: helpFor(name)}, names))
		m.counters[name] = vec
	}
	m.mu.Unlock()
	if c, err := vec.GetMetricWithLabelValues(values...); err == nil {
		c.Inc()
	}
}

// SetGauge implements consulx.Metrics.
func (m *Metrics) SetGauge(name string, value float64, labels ...consulx.Label) {
	names, values := split(labels)
	m.mu.Lock()
	vec, ok := m.gauges[name]
	if !ok {
		vec = register(m.reg, prom.NewGaugeVec(prom.GaugeOpts{Name: name, Help: helpFor(name)}, names))
		m.gauges[name] = vec
	}
	m.mu.Unlock()
	if g, err := vec.GetMetricWithLabelValues(values...); err == nil {
		g.Set(value)
	}
}

// ObserveDuration implements consulx.Metrics. Durations are recorded in
// seconds with the default Prometheus buckets.
func (m *Metrics) ObserveDuration(name string, d time.Duration, labels ...consulx.Label) {
	names, values := split(labels)
	m.mu.Lock()
	vec, ok := m.histograms[name]
	if !ok {
		vec = register(m.reg, prom.NewHistogramVec(prom.HistogramOpts{Name: name, Help: helpFor(name), Buckets: prom.DefBuckets}, names))
		m.histograms[name] = vec
	}
	m.mu.Unlock()
	if h, err := vec.GetMetricWithLabelValues(values...); err == nil {
		h.Observe(d.Seconds())
	}
}
