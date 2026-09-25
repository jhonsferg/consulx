// Command gin integrates a Gin router. A *gin.Engine is an http.Handler,
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

	"github.com/gin-gonic/gin"

	"github.com/jhonsferg/consulx"
)

func main() {
	router := gin.New()
	router.GET("/orders", func(c *gin.Context) { c.JSON(http.StatusOK, []string{}) })
	server := &http.Server{Addr: ":8080", Handler: router, ReadHeaderTimeout: 5 * time.Second}

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
