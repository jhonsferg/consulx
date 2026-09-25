package otel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/jhonsferg/consulx"
)

func TestMetricsAreRecorded(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	m := NewMetrics(provider.Meter(ScopeName))

	m.IncCounter(consulx.MetricRegisterTotal)
	m.IncCounter(consulx.MetricDiscoveryRequestsTotal, consulx.Label{Key: "operation", Value: "query"})
	m.SetGauge(consulx.MetricRuntimeState, 2)
	m.ObserveDuration(consulx.MetricConsulRequestDuration, 10*time.Millisecond)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, mm := range sm.Metrics {
			names[mm.Name] = true
		}
	}
	for _, want := range []string{"consulx.register", "consulx.discovery.requests", "consulx.runtime.state", "consulx.consul.request.duration"} {
		if !names[want] {
			t.Errorf("missing instrument %s in %v", want, names)
		}
	}
}

func TestHTTPClientCreatesSpans(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))

	hc := HTTPClient(nil, otelhttp.WithTracerProvider(tp))
	if hc.Timeout != 0 {
		t.Fatal("the client must not set a global timeout")
	}
	c, err := consulx.New(consulx.WithConsulAddress(srv.URL), consulx.WithAutoRegister(false), consulx.WithHTTPClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.AgentInfo(t.Context())
	if len(rec.Ended()) == 0 {
		t.Fatal("no span recorded for the Consul request")
	}
}
