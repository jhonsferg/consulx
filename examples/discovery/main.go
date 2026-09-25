// Command discovery calls another service found in Consul: a one-off
// lookup, a watch that follows changes, and client-side load balancing.
// It registers nothing. It looks up orders-api with the tag "v1", which
// examples/basic registers, so start that example first.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jhonsferg/consulx"
	"github.com/jhonsferg/consulx/balancer"
)

func main() {
	consul, err := consulx.New(consulx.WithAutoRegister(false))
	if err != nil {
		slog.Error("invalid consulx configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start is needed for background resources such as balancer watches;
	// Stop releases them.
	if err := consul.Start(ctx); err != nil {
		slog.Error("start", "error", err)
		os.Exit(1)
	}
	defer func() { _ = consul.Stop(context.Background()) }()

	// One-off lookup of healthy instances with the tag "v1", the tag
	// examples/basic registers on orders-api.
	instances, err := consul.Discovery().Service("orders-api").Tag("v1").All(ctx)
	if err != nil {
		slog.Error("lookup", "error", err)
	}
	fmt.Printf("found %d instances\n", len(instances))

	// Follow changes.
	watch, err := consul.Discovery().Watch(ctx, "orders-api")
	if err != nil {
		slog.Error("watch", "error", err)
		os.Exit(1)
	}
	go func() {
		for ev := range watch.Events() {
			fmt.Printf("orders-api: %d instances (+%d -%d ~%d)\n", len(ev.Instances), len(ev.Added), len(ev.Removed), len(ev.Changed))
		}
	}()

	// Balance requests.
	lb := consul.Balancer(balancer.RoundRobin())
	client := &http.Client{Timeout: 5 * time.Second}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		inst, err := lb.Next(ctx, "orders-api")
		if errors.Is(err, consulx.ErrServiceNotFound) {
			fmt.Println("no healthy orders-api instance")
			continue
		}
		if err != nil {
			slog.Warn("balancer", "error", err)
			continue
		}
		resp, err := client.Get(inst.URL() + "/orders")
		if err != nil {
			slog.Warn("call failed", "instance", inst.ID, "error", err)
			continue
		}
		_ = resp.Body.Close()
		fmt.Printf("%s answered %d\n", inst.ID, resp.StatusCode)
	}
}
