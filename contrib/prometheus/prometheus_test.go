package prometheus

import (
	"strings"
	"sync"
	"testing"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jhonsferg/consulx"
)

func TestMetricsAreExported(t *testing.T) {
	reg := prom.NewRegistry()
	m := New(reg)
	m.IncCounter(consulx.MetricRegisterTotal)
	m.IncCounter(consulx.MetricRegisterTotal)
	m.IncCounter(consulx.MetricDiscoveryRequestsTotal, consulx.Label{Key: "operation", Value: "query"})
	m.SetGauge(consulx.MetricRuntimeState, 2)
	m.ObserveDuration(consulx.MetricConsulRequestDuration, 20*time.Millisecond, consulx.Label{Key: "operation", Value: "register"})

	expected := `
# HELP consulx_register_total Successful service registrations.
# TYPE consulx_register_total counter
consulx_register_total 2
# HELP consulx_runtime_state Runtime state: 0 idle, 1 starting, 2 running, 3 degraded, 4 stopping, 5 stopped.
# TYPE consulx_runtime_state gauge
consulx_runtime_state 2
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), consulx.MetricRegisterTotal, consulx.MetricRuntimeState); err != nil {
		t.Fatal(err)
	}
	if n := testutil.CollectAndCount(m.histograms[consulx.MetricConsulRequestDuration]); n != 1 {
		t.Fatalf("histogram series %d", n)
	}
	if v := testutil.ToFloat64(m.counters[consulx.MetricDiscoveryRequestsTotal].WithLabelValues("query")); v != 1 {
		t.Fatalf("labelled counter %v", v)
	}
}

func TestSharedRegistryDoesNotPanic(t *testing.T) {
	reg := prom.NewRegistry()
	a, b := New(reg), New(reg)
	a.IncCounter(consulx.MetricRegisterTotal)
	b.IncCounter(consulx.MetricRegisterTotal) // reuses the existing collector
	if v := testutil.ToFloat64(b.counters[consulx.MetricRegisterTotal].WithLabelValues()); v != 2 {
		t.Fatalf("shared counter %v", v)
	}
	// Mismatched labels are ignored instead of panicking.
	a.IncCounter(consulx.MetricRegisterTotal, consulx.Label{Key: "x", Value: "y"})
}

func TestConcurrentUse(t *testing.T) {
	m := New(prom.NewRegistry())
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				m.IncCounter(consulx.MetricReconnectTotal)
				m.SetGauge(consulx.MetricHealthStatus, 1)
				m.ObserveDuration(consulx.MetricConsulRequestDuration, time.Millisecond, consulx.Label{Key: "operation", Value: "x"})
			}
		})
	}
	wg.Wait()
}
