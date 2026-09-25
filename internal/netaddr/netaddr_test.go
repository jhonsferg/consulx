package netaddr

import (
	"errors"
	"net/netip"
	"testing"
)

func TestParseListenAddress(t *testing.T) {
	tests := []struct {
		in   string
		host string
		port int
	}{
		{":8080", "", 8080},
		{"0.0.0.0:80", "0.0.0.0", 80},
		{"[::1]:8443", "::1", 8443},
		{"", "", 80},
		{"localhost:http", "localhost", 80},
		{"10.0.0.5:0", "10.0.0.5", 0},
	}
	for _, tt := range tests {
		host, port, err := ParseListenAddress(t.Context(), tt.in)
		if err != nil || host != tt.host || port != tt.port {
			t.Errorf("ParseListenAddress(%q) = %q %d %v", tt.in, host, port, err)
		}
	}
	for _, bad := range []string{"8080", "host:", "host:99999", "host:notaport"} {
		if _, _, err := ParseListenAddress(t.Context(), bad); err == nil {
			t.Errorf("ParseListenAddress(%q) should fail", bad)
		}
	}
}

func FuzzParseListenAddress(f *testing.F) {
	for _, s := range []string{":8080", "[::1]:1", "a:b", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if _, port, err := ParseListenAddress(t.Context(), s); err == nil && (port < 0 || port > 65535) {
			t.Fatalf("port %d out of range for %q", port, s)
		}
	})
}

func TestClassification(t *testing.T) {
	for _, h := range []string{"", "0.0.0.0", "::", "[::]"} {
		if !IsWildcard(h) {
			t.Errorf("%q should be wildcard", h)
		}
	}
	for _, h := range []string{"10.0.0.1", "example.com", "::1"} {
		if IsWildcard(h) {
			t.Errorf("%q should not be wildcard", h)
		}
	}
	for _, h := range []string{"127.0.0.1", "127.8.9.1", "::1", "[::1]", "localhost", "LOCALHOST.", "api.localhost"} {
		if !IsLoopback(h) {
			t.Errorf("%q should be loopback", h)
		}
	}
	for _, h := range []string{"10.0.0.1", "example.com", "::ffff:10.0.0.1"} {
		if IsLoopback(h) {
			t.Errorf("%q should not be loopback", h)
		}
	}
	if !IsIPv6("fd00::1") || IsIPv6("10.0.0.1") || IsIPv6("::ffff:10.0.0.1") || IsIPv6("host") {
		t.Error("IsIPv6 misclassified")
	}
}

func TestRouteIP(t *testing.T) {
	a, err := RouteIP(t.Context(), "127.0.0.1:8500")
	if err != nil {
		t.Fatal(err)
	}
	if !a.IsLoopback() {
		t.Fatalf("route to loopback must use loopback, got %s", a)
	}
}

func ifc(name string, up, loop bool, addrs ...string) Interface {
	x := Interface{Name: name, Up: up, Loop: loop}
	for _, a := range addrs {
		x.Addrs = append(x.Addrs, netip.MustParsePrefix(a))
	}
	return x
}

func TestSelect(t *testing.T) {
	ifs := []Interface{
		ifc("lo", true, true, "127.0.0.1/8", "::1/128"),
		ifc("docker0", true, false, "172.17.0.1/16"),
		ifc("eth-down", false, false, "10.9.9.9/24"),
		ifc("eth0", true, false, "fe80::1/64", "203.0.113.10/24", "10.0.0.20/24", "fd00::20/64", "2001:db8::20/64"),
		ifc("eth1", true, false, "192.168.1.5/24"),
	}
	tests := []struct {
		name string
		f    Filter
		want string
	}{
		{"private ipv4 skips virtual, down, public", Filter{}, "10.0.0.20"},
		{"by name", Filter{Name: "eth1"}, "192.168.1.5"},
		{"virtual by name", Filter{Name: "docker0"}, "172.17.0.1"},
		{"public ipv4", Filter{Public: true}, "203.0.113.10"},
		{"private ipv6 skips link-local", Filter{IPv6: true}, "fd00::20"},
		{"public ipv6", Filter{IPv6: true, Public: true}, "2001:db8::20"},
	}
	for _, tt := range tests {
		got, err := Select(ifs, tt.f)
		if err != nil || got.String() != tt.want {
			t.Errorf("%s: got %s %v, want %s", tt.name, got, err, tt.want)
		}
	}
	if _, err := Select(ifs, Filter{Name: "missing"}); !errors.Is(err, ErrNoAddress) {
		t.Errorf("missing interface: %v", err)
	}
}

func TestInterfacesDoesNotFail(t *testing.T) {
	if _, err := Interfaces(); err != nil {
		t.Fatal(err)
	}
}
