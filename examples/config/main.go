// Command config loads layered configuration from Consul KV and follows
// changes. Try:
//
//	consul kv put config/application/database/host shared-db
//	consul kv put config/orders-api,prod/database/max-conns 50
//	CONSULX_ENVIRONMENT=prod go run ./config
//	consul kv put config/orders-api/database/max-conns 5000   # rejected
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jhonsferg/consulx"
	"github.com/jhonsferg/consulx/kvconfig"
)

// AppConfig is bound from config/{application,orders-api}[,<profile>]/.
type AppConfig struct {
	Database struct {
		Host     string        `consul:"host,required"`
		Port     int           `consul:"port" default:"5432"`
		MaxConns int           `consul:"max-conns" default:"10"`
		Timeout  time.Duration `consul:"timeout" default:"5s"`
	} `consul:"database"`
	Features map[string]bool `consul:"features"`
}

// Validate rejects unsafe values before they are applied.
func (c AppConfig) Validate() error {
	if c.Database.MaxConns < 1 || c.Database.MaxConns > 1000 {
		return errors.New("database max-conns must be between 1 and 1000")
	}
	return nil
}

func main() {
	consul, err := consulx.New(consulx.WithServiceName("orders-api"), consulx.WithAutoRegister(false))
	if err != nil {
		slog.Error("invalid consulx configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Println("reading", consul.Config().Contexts())
	w, err := kvconfig.Watch[AppConfig](ctx, consul.Config(),
		kvconfig.OnChange(func(old, new AppConfig) {
			fmt.Printf("max-conns %d -> %d\n", old.Database.MaxConns, new.Database.MaxConns)
		}))
	if err != nil {
		slog.Error("configuration unavailable or invalid", "error", err)
		os.Exit(1)
	}
	defer func() { _ = w.Close() }()
	fmt.Printf("initial configuration: %+v\n", w.Current())

	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-w.Errors():
			if ok {
				fmt.Println("change rejected, keeping previous value:", err)
			}
		case <-w.Changes():
		}
	}
}
