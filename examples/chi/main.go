// Command chi integrates a Chi router, reads the service address from the
// environment (as injected by Kubernetes) and uses a TTL check instead of
// injected HTTP endpoints.
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jhonsferg/consulx"
)

func main() {
	router := chi.NewRouter()
	router.Get("/orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "[]") })
	server := &http.Server{Addr: ":8080", Handler: router, ReadHeaderTimeout: 5 * time.Second}

	consul, err := consulx.New(
		consulx.WithServer(server),
		consulx.WithServiceName("orders-api"),
		// POD_IP is set from status.podIP by the Kubernetes Downward API.
		consulx.Config{Service: consulx.ServiceConfig{AddressEnv: "POD_IP"}},
		consulx.WithHealth(consulx.HealthConfig{Check: consulx.CheckTTL, TTL: 15 * time.Second}),
		consulx.WithTags("chi"),
	)
	if err != nil {
		slog.Error("invalid consulx configuration", "error", err)
		os.Exit(1)
	}

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
