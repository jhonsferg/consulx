// Package integration runs ConsulX against real Consul agents in Docker.
//
//	CONSUL_VERSION=1.22 go test ./...
//
// CONSUL_VERSION selects the hashicorp/consul image tag (default 1.22).
// Tests that take minutes (crash reaping) are skipped with -short.
package integration

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// serviceHost is how the Consul container reaches processes on the test
// host. Docker Desktop provides host.docker.internal; on Linux it is mapped
// to the host gateway below.
const serviceHost = "host.docker.internal"

func consulVersion() string {
	if v := os.Getenv("CONSUL_VERSION"); v != "" {
		return v
	}
	return "1.22"
}

type agent struct {
	ctr  *testcontainers.DockerContainer
	addr string // http://127.0.0.1:<port>
	api  *api.Client
}

// startConsul runs a dev agent bound to a fixed host port, so the address
// survives a stop/start of the container. A restarted dev agent loses its
// state, which is how tests simulate an agent restart.
func startConsul(t *testing.T, extraArgs ...string) *agent {
	t.Helper()
	port := freePort(t)
	cmd := append([]string{"agent", "-dev", "-client=0.0.0.0", "-log-level=warn"}, extraArgs...)
	ctr, err := testcontainers.Run(t.Context(), "hashicorp/consul:"+consulVersion(),
		testcontainers.WithCmd(cmd...),
		testcontainers.WithExposedPorts("8500/tcp"),
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.PortBindings = network.PortMap{
				network.MustParsePort("8500/tcp"): {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: strconv.Itoa(port)}},
			}
			hc.ExtraHosts = append(hc.ExtraHosts, serviceHost+":host-gateway")
		}),
		testcontainers.WithWaitStrategy(leaderElected()),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start consul %s: %v", consulVersion(), err)
	}
	addr := fmt.Sprintf("http://127.0.0.1:%d", port)
	c, err := api.NewClient(&api.Config{Address: addr})
	if err != nil {
		t.Fatal(err)
	}
	return &agent{ctr: ctr, addr: addr, api: c}
}

func leaderElected() wait.Strategy {
	return wait.ForHTTP("/v1/status/leader").WithPort("8500/tcp").
		WithResponseMatcher(func(body io.Reader) bool {
			b, _ := io.ReadAll(body)
			return len(b) > 2 // "" until a leader is elected
		}).
		WithStartupTimeout(time.Minute)
}

// restart stops and starts the container. The dev agent comes back empty.
func (a *agent) restart(t *testing.T, down time.Duration) {
	t.Helper()
	timeout := 5 * time.Second
	if err := a.ctr.Stop(t.Context(), &timeout); err != nil {
		t.Fatal(err)
	}
	time.Sleep(down)
	if err := a.ctr.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// instances returns the health entries of a service.
func (a *agent) instances(t *testing.T, service string, passingOnly bool) []*api.ServiceEntry {
	t.Helper()
	entries, _, err := a.api.Health().Service(service, "", passingOnly, nil)
	if err != nil {
		return nil
	}
	return entries
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func uniqueName(t *testing.T) string {
	n := strings.ToLower(strings.NewReplacer("/", "-", "_", "-").Replace(t.Name()))
	return fmt.Sprintf("%s-%d", n, time.Now().UnixNano()%100000)
}

// runUntil runs fn in a goroutine and returns a function that cancels it and
// waits for its result.
func runUntil(t *testing.T, fn func(ctx context.Context) error) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- fn(ctx) }()
	return func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(30 * time.Second):
			t.Fatal("runtime did not stop")
			return nil
		}
	}
}
