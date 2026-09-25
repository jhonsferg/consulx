package consulx

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestPortResolution(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	lnPort := ln.Addr().(*net.TCPAddr).Port

	tests := []struct {
		name   string
		opts   []Option
		port   int
		source string
	}{
		{"server addr", []Option{WithServer(&http.Server{Addr: ":8080"})}, 8080, "server"},
		{"empty server addr is :http", []Option{WithServer(&http.Server{})}, 80, "server"},
		{"listener wins over server", []Option{WithServer(&http.Server{Addr: ":0"}), WithListener(ln)}, lnPort, "listener"},
		{"explicit wins", []Option{WithServer(&http.Server{Addr: ":8080"}), WithServicePort(9000)}, 9000, "config"},
		{"multi-port default", []Option{Config{Service: ServiceConfig{Ports: []ServicePort{{Name: "grpc", Port: 9090}, {Name: "http", Port: 8081, Default: true}}}}}, 8081, "ports"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, append([]Option{WithServiceName("a")}, tt.opts...)...)
			port, source, err := c.resolvePort(t.Context())
			if err != nil || port != tt.port || source != tt.source {
				t.Fatalf("got %d %s %v", port, source, err)
			}
		})
	}
}

func TestPortResolutionErrors(t *testing.T) {
	for name, opts := range map[string][]Option{
		"port zero":    {WithServer(&http.Server{Addr: ":0"})},
		"nothing":      {},
		"bad addr":     {WithServer(&http.Server{Addr: "nonsense"})},
		"named port 0": {WithServer(&http.Server{Addr: "10.0.0.1:0"})},
	} {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, append([]Option{WithServiceName("a")}, opts...)...)
			if _, _, err := c.resolvePort(t.Context()); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestAddressResolutionOrder(t *testing.T) {
	t.Setenv("CONSULX_TEST_POD_IP", "10.1.2.3")
	custom := AddressResolverFunc(func(context.Context) (string, error) { return "10.9.9.9", nil })
	tests := []struct {
		name   string
		opts   []Option
		addr   string
		source string
	}{
		{"explicit", []Option{WithServiceAddress("10.0.0.20"), WithAddressResolver(custom)}, "10.0.0.20", "config"},
		{"custom resolver", []Option{WithAddressResolver(custom)}, "10.9.9.9", "resolver"},
		{"env", []Option{Config{Service: ServiceConfig{AddressEnv: "CONSULX_TEST_POD_IP"}}, WithServer(&http.Server{Addr: "10.5.5.5:80"})}, "10.1.2.3", "env CONSULX_TEST_POD_IP"},
		{"server host", []Option{WithServer(&http.Server{Addr: "10.5.5.5:80"})}, "10.5.5.5", "server"},
		{"dns name", []Option{WithServiceAddress("orders.internal")}, "orders.internal", "config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, append([]Option{WithServiceName("a"), WithServicePort(80)}, tt.opts...)...)
			ep, err := c.resolveEndpoint(t.Context())
			if err != nil || ep.Address != tt.addr || ep.AddressSource != tt.source {
				t.Fatalf("got %+v %v", ep, err)
			}
		})
	}
}

func TestWildcardAndLoopbackAreRejected(t *testing.T) {
	for _, addr := range []string{"0.0.0.0", "::", "127.0.0.1", "localhost"} {
		c := newTestClient(t, WithServiceName("a"), WithServicePort(80), WithServiceAddress(addr))
		if _, err := c.resolveEndpoint(t.Context()); !errors.Is(err, ErrInvalidConfiguration) {
			t.Errorf("%s: got %v", addr, err)
		}
	}
	c := newTestClient(t, WithServiceName("a"), WithServicePort(80), WithServiceAddress("127.0.0.1"),
		Config{Service: ServiceConfig{AllowLoopback: true}})
	if ep, err := c.resolveEndpoint(t.Context()); err != nil || ep.Address != "127.0.0.1" {
		t.Fatalf("AllowLoopback: %+v %v", ep, err)
	}
}

func TestLoopbackServerHostFallsThrough(t *testing.T) {
	// The server listens on loopback and the agent is on loopback too: route
	// detection yields loopback as well, so only a real interface is usable.
	c := newTestClient(t, WithServiceName("a"), WithServer(&http.Server{Addr: "127.0.0.1:8080"}))
	ep, err := c.resolveEndpoint(t.Context())
	if err == nil && (ep.AddressSource != "interface" || strings.HasPrefix(ep.Address, "127.")) {
		t.Fatalf("loopback must never be registered implicitly: %+v", ep)
	}
	if err != nil && !strings.Contains(err.Error(), "WithServiceAddress") {
		t.Fatalf("error must tell the user how to fix it: %v", err)
	}
}

func TestCustomResolverErrorIsReported(t *testing.T) {
	failing := AddressResolverFunc(func(context.Context) (string, error) { return "", errors.New("metadata service down") })
	c := newTestClient(t, WithServiceName("a"), WithServicePort(80), WithAddressResolver(failing))
	_, err := c.resolveEndpoint(t.Context())
	if !errors.Is(err, ErrInvalidConfiguration) || !strings.Contains(err.Error(), "metadata service down") {
		t.Fatalf("got %v", err)
	}
}

func TestSchemeResolution(t *testing.T) {
	c := newTestClient(t, WithServiceName("a"), WithServiceAddress("10.0.0.1"),
		WithServer(&http.Server{Addr: ":8443", TLSConfig: &tls.Config{}}))
	ep, err := c.resolveEndpoint(t.Context())
	if err != nil || ep.Scheme != "https" {
		t.Fatalf("%+v %v", ep, err)
	}
	c = newTestClient(t, WithServiceName("a"), WithServiceAddress("10.0.0.1"), WithServicePort(80),
		Config{Service: ServiceConfig{Scheme: "https"}})
	if ep, _ := c.resolveEndpoint(t.Context()); ep.Scheme != "https" {
		t.Fatalf("explicit scheme ignored: %+v", ep)
	}
}

// Regression: http.Server.Serve initialises TLSConfig for HTTP/2, which
// made a plain HTTP server register as https (found by integration tests)
// and raced with Serve.
func TestSchemeIsDecidedBeforeServing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.NotFoundHandler()}
	c := newTestClient(t, WithServiceName("a"), WithServiceAddress("10.0.0.1"), WithServer(srv), WithListener(ln))
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	// Let Serve run its HTTP/2 setup.
	if resp, err := http.Get("http://" + ln.Addr().String()); err == nil {
		resp.Body.Close()
	}
	ep, err := c.resolveEndpoint(t.Context())
	if err != nil || ep.Scheme != "http" {
		t.Fatalf("got %+v %v", ep, err)
	}
}

func TestResolverHelpers(t *testing.T) {
	if _, err := StaticAddress("").Resolve(t.Context()); !errors.Is(err, ErrAddressNotFound) {
		t.Errorf("static empty: %v", err)
	}
	if _, err := EnvAddress("CONSULX_TEST_SURELY_UNSET").Resolve(t.Context()); !errors.Is(err, ErrAddressNotFound) {
		t.Errorf("env unset: %v", err)
	}
	if a, err := RouteAddress("127.0.0.1:8500").Resolve(t.Context()); err != nil || a != "127.0.0.1" {
		t.Errorf("route: %s %v", a, err)
	}
	chain := FirstAddress(StaticAddress("0.0.0.0"), StaticAddress("127.0.0.1"), StaticAddress("10.0.0.7"))
	if a, err := chain.Resolve(t.Context()); err != nil || a != "10.0.0.7" {
		t.Errorf("chain must skip wildcard and loopback: %s %v", a, err)
	}
	if _, err := FirstAddress(StaticAddress("0.0.0.0")).Resolve(t.Context()); !errors.Is(err, ErrAddressNotFound) {
		t.Errorf("chain without usable address: %v", err)
	}
}

func TestConsulTarget(t *testing.T) {
	tests := map[string]ConsulConfig{
		"127.0.0.1:8500":    {Address: "127.0.0.1:8500"},
		"consul:8500":       {Address: "http://consul"},
		"consul:8501":       {Address: "https://consul"},
		"consul:9000":       {Address: "https://consul:9000/prefix"},
		"[fd00::1]:8500":    {Address: "http://[fd00::1]"},
		"":                  {Address: "unix:///var/run/consul.sock"},
		"consul.local:8501": {Address: "consul.local", TLS: TLSConfig{Enabled: true}},
	}
	for want, cc := range tests {
		if got := consulTarget(cc); got != want {
			t.Errorf("%s: got %q want %q", cc.Address, got, want)
		}
	}
}
