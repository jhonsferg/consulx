// Command echo integrates an Echo v5 router. *echo.Echo is an http.Handler,
// so no adapter is needed.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/jhonsferg/consulx"
)

func main() {
	e := echo.New()
	e.GET("/orders", func(c *echo.Context) error { return c.JSON(http.StatusOK, []string{}) })
	server := &http.Server{Addr: ":8080", Handler: e, ReadHeaderTimeout: 5 * time.Second}

	consul, err := consulx.New(
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
			stop()
		}
	}()
	if err := consul.Run(ctx); err != nil {
		slog.Error("consulx", "error", err)
	}
	_ = server.Shutdown(context.Background())
}
