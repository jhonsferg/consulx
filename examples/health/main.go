// Command health reports component health: a checker run on every probe
// and a state pushed asynchronously. Readiness drives the Consul check; the
// liveness endpoint stays independent of dependencies.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jhonsferg/consulx"
	"github.com/jhonsferg/consulx/health"
)

func main() {
	server := &http.Server{Addr: ":8080", Handler: http.NewServeMux(), ReadHeaderTimeout: 5 * time.Second}
	consul, err := consulx.New(
		consulx.WithServer(server),
		consulx.WithServiceName("orders-api"),
		consulx.WithAutoHealth(),
		consulx.WithHealth(consulx.HealthConfig{
			Interval: 5 * time.Second,
			Timeout:  2 * time.Second,
			// Kubernetes probes share the endpoints: serve DEGRADED as 200.
			DegradedStatusCode: http.StatusOK,
		}),
	)
	if err != nil {
		slog.Error("invalid consulx configuration", "error", err)
		os.Exit(1)
	}

	var db *sql.DB // your database handle
	consul.Health().Register("database", health.CheckerFunc(func(ctx context.Context) health.Result {
		if db == nil {
			return health.Result{Status: health.StatusDown, Error: "not connected"}
		}
		if err := db.PingContext(ctx); err != nil {
			return health.Result{Status: health.StatusDown, Error: err.Error()}
		}
		return health.Result{Status: health.StatusUp}
	}))

	// A consumer learning about its broker asynchronously pushes its state.
	consul.Health().Set("broker", health.Result{Status: health.StatusDegraded, Details: map[string]any{"lag": 1200}})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			stop()
		}
	}()
	if err := consul.Run(ctx); err != nil {
		slog.Error("consulx", "error", err)
	}
	_ = server.Shutdown(context.Background())
}
