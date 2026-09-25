# ADR 0010: Observability

* Status: accepted
* Date: 2026-09-25

## Context

Operators must see registration, reconnection and configuration activity.
The core must not force Prometheus, OpenTelemetry or a logging library on
every service.

## Decision

* Logging uses `log/slog`. `WithLogger` injects a logger; the default is
  `slog.Default()`; `WithLogger(nil)` silences ConsulX. No `fmt.Println` or
  `log.Println` in the library.
* Log events follow the brief: client initialized, registration started,
  service registered, health check configured, deregistration started,
  service deregistered, consul unavailable, retry scheduled, reconnection
  successful, service re-registered, configuration changed, configuration
  rejected, runtime stopping, runtime stopped.
* Secrets never reach logs: tokens are `consulx.Secret` values whose fmt,
  slog, JSON and YAML forms are redacted; configuration values read from KV
  are never logged. An integration test asserts that the ACL token does not
  appear in debug logs.
* Metrics go through the small `consulx.Metrics` interface (counter, gauge,
  duration), no-op by default. Metric names are exported constants.
  Labels only take values from small fixed sets (operation names).
* Adapters live in separate modules: `contrib/prometheus` and
  `contrib/otel` (metrics, plus an `otelhttp` client for tracing every
  request to Consul through `WithHTTPClient`).

## Consequences

* The core module depends only on the official Consul client and yaml.v3.
* Adding an exporter means implementing three methods.
