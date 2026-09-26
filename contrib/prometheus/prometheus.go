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
	"errors"
	"strings"
	"sync"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"

	"github.com/jhonsferg/consulx"
)

// Metrics implements consulx.Metrics. It is safe for concurrent use.
//
// The series of every metric and label value is resolved once and cached,
// so reporting a measurement costs no allocation: ConsulX reports from its
// registration, heartbeat and discovery paths.
type Metrics struct {
	reg prom.Registerer

	mu         sync.Mutex
	counters   map[string]*prom.CounterVec
	gauges     map[string]*prom.GaugeVec
	histograms map[string]*prom.HistogramVec

	counterSeries   map[seriesKey]prom.Counter
	gaugeSeries     map[seriesKey]prom.Gauge
	histogramSeries map[seriesKey]prom.Observer
}

var _ consulx.Metrics = (*Metrics)(nil)

// New returns a Metrics registering its collectors on reg.
func New(reg prom.Registerer) *Metrics {
	return &Metrics{
		reg:             reg,
		counters:        map[string]*prom.CounterVec{},
		gauges:          map[string]*prom.GaugeVec{},
		histograms:      map[string]*prom.HistogramVec{},
		counterSeries:   map[seriesKey]prom.Counter{},
		gaugeSeries:     map[seriesKey]prom.Gauge{},
		histogramSeries: map[seriesKey]prom.Observer{},
	}
}

// seriesKey identifies a series with at most one label, which covers every
// metric ConsulX reports. Series with more labels are resolved per call.
type seriesKey struct {
	name  string
	label consulx.Label
	n     int // number of labels: 0 or 1
}

// vec is what the three Prometheus vector types have in common.
type vec[S any] interface {
	GetMetricWithLabelValues(lvs ...string) (S, error)
}

// series returns the series of name and labels, creating the vector with
// create on first use. m.mu guards both maps.
func series[V vec[S], S any](m *Metrics, vecs map[string]V, cache map[seriesKey]S,
	name string, labels []consulx.Label, create func(names []string) V,
) (S, bool) {
	key := seriesKey{name: name, n: len(labels)}
	if len(labels) == 1 {
		key.label = labels[0]
	}
	cacheable := len(labels) <= 1
	m.mu.Lock()
	defer m.mu.Unlock()
	if cacheable {
		if s, ok := cache[key]; ok {
			return s, true
		}
	}
	names, values := split(labels)
	v, ok := vecs[name]
	if !ok {
		v = create(names)
		vecs[name] = v
	}
	s, err := v.GetMetricWithLabelValues(values...)
	if err != nil {
		return s, false
	}
	if cacheable {
		cache[key] = s
	}
	return s, true
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
		var are prom.AlreadyRegisteredError
		if errors.As(err, &are) {
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
	c, ok := series(m, m.counters, m.counterSeries, name, labels, func(names []string) *prom.CounterVec {
		return register(m.reg, prom.NewCounterVec(prom.CounterOpts{Name: name, Help: helpFor(name)}, names))
	})
	if ok {
		c.Inc()
	}
}

// SetGauge implements consulx.Metrics.
func (m *Metrics) SetGauge(name string, value float64, labels ...consulx.Label) {
	g, ok := series(m, m.gauges, m.gaugeSeries, name, labels, func(names []string) *prom.GaugeVec {
		return register(m.reg, prom.NewGaugeVec(prom.GaugeOpts{Name: name, Help: helpFor(name)}, names))
	})
	if ok {
		g.Set(value)
	}
}

// ObserveDuration implements consulx.Metrics. Durations are recorded in
// seconds with the default Prometheus buckets.
func (m *Metrics) ObserveDuration(name string, d time.Duration, labels ...consulx.Label) {
	h, ok := series(m, m.histograms, m.histogramSeries, name, labels, func(names []string) *prom.HistogramVec {
		return register(m.reg, prom.NewHistogramVec(prom.HistogramOpts{Name: name, Help: helpFor(name), Buckets: prom.DefBuckets}, names))
	})
	if ok {
		h.Observe(d.Seconds())
	}
}
