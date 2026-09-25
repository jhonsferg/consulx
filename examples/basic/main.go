// Command basic registers a net/http service in Consul with injected health
// endpoints, and deregisters it on SIGINT/SIGTERM.
//
//	docker run -d -p 8500:8500 hashicorp/consul:1.22 agent -dev -client=0.0.0.0
//	CONSULX_SERVICE_ADDRESS=host.docker.internal go run ./basic
//
// CONSULX_SERVICE_ADDRESS is only needed when Consul runs in Docker Desktop
// and must reach this process on the host.
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

	"github.com/jhonsferg/consulx"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "[]")
	})
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	// New must run before the server starts serving.
	consul, err := consulx.New(
		consulx.WithConsulAddress("http://localhost:8500"),
		consulx.WithServer(server),
		consulx.WithServiceName("orders-api"),
		consulx.WithAutoHealth(),
	)
	if err != nil {
		slog.Error("invalid consulx configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server failed", "error", err)
			stop()
		}
	}()

	// Run registers the service, keeps it registered and deregisters it when
	// ctx is cancelled.
	if err := consul.Run(ctx); err != nil {
		slog.Error("consulx stopped with an error", "error", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}
