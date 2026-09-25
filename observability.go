package consulx

import "time"

// Metrics receives ConsulX measurements. Implementations must be safe for
// concurrent use and must not block. The core ships only a no-op
// implementation; adapters for Prometheus and OpenTelemetry live in
// separate modules so the core stays free of their dependencies.
type Metrics interface {
	// IncCounter adds one to the counter name.
	IncCounter(name string, labels ...Label)
	// SetGauge sets the gauge name to value.
	SetGauge(name string, value float64, labels ...Label)
	// ObserveDuration records one duration sample for the histogram name.
	ObserveDuration(name string, d time.Duration, labels ...Label)
}

// Label is a metric dimension. ConsulX only uses labels with a small, fixed
// set of values (operation names, states), never IDs or addresses.
type Label struct {
	Key, Value string
}

// Metric names emitted by ConsulX.
const (
	MetricRegisterTotal            = "consulx_register_total"
	MetricRegisterErrorsTotal      = "consulx_register_errors_total"
	MetricDeregisterTotal          = "consulx_deregister_total"
	MetricDeregisterErrorsTotal    = "consulx_deregister_errors_total"
	MetricDiscoveryRequestsTotal   = "consulx_discovery_requests_total"
	MetricDiscoveryErrorsTotal     = "consulx_discovery_errors_total"
	MetricConsulRequestsTotal      = "consulx_consul_requests_total"
	MetricConsulRequestErrorsTotal = "consulx_consul_request_errors_total"
	MetricConsulRequestDuration    = "consulx_consul_request_duration_seconds"
	MetricReconnectTotal           = "consulx_reconnect_total"
	MetricHealthStatus             = "consulx_health_status"
	MetricRuntimeState             = "consulx_runtime_state"
	MetricConfigReloadTotal        = "consulx_config_reload_total"
	MetricConfigReloadErrorsTotal  = "consulx_config_reload_errors_total"
	MetricErrorsDroppedTotal       = "consulx_errors_dropped_total"
)

type noopMetrics struct{}

func (noopMetrics) IncCounter(string, ...Label)                     {}
func (noopMetrics) SetGauge(string, float64, ...Label)              {}
func (noopMetrics) ObserveDuration(string, time.Duration, ...Label) {}
