//go:build bench

// Benchmarks of address handling, excluded from normal builds by the bench
// build tag. They run on every registration attempt. See docs/benchmarks.md.
package netaddr

import (
	"context"
	"net/netip"
	"testing"
)

func BenchmarkParseListenAddress(b *testing.B) {
	ctx := context.Background()
	for _, addr := range []string{":8080", "10.0.0.20:8080", "[::1]:8443"} {
		b.Run(addr, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, _, err := ParseListenAddress(ctx, addr); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkClassify(b *testing.B) {
	b.Run("IsWildcard", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = IsWildcard("0.0.0.0")
		}
	})
	b.Run("IsLoopback", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = IsLoopback("10.0.0.20")
		}
	})
	b.Run("IsIPv6", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = IsIPv6("10.0.0.20")
		}
	})
	b.Run("IsVirtual", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = IsVirtual("eth0")
		}
	})
}

// BenchmarkSelect measures choosing an interface address on a host with
// container bridges, the usual case in Docker and Kubernetes nodes.
func BenchmarkSelect(b *testing.B) {
	ifs := []Interface{
		{Name: "lo", Up: true, Loop: true, Addrs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8")}},
		{Name: "docker0", Up: true, Addrs: []netip.Prefix{netip.MustParsePrefix("172.17.0.1/16")}},
		{Name: "veth12ab", Up: true, Addrs: []netip.Prefix{netip.MustParsePrefix("fe80::1/64")}},
		{Name: "eth0", Up: true, Addrs: []netip.Prefix{netip.MustParsePrefix("fe80::2/64"), netip.MustParsePrefix("10.0.0.20/24")}},
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Select(ifs, Filter{}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRouteIP measures finding the local address that routes to the
// agent, which opens (and closes) a UDP socket without sending packets.
func BenchmarkRouteIP(b *testing.B) {
	ctx := context.Background()
	if _, err := RouteIP(ctx, "127.0.0.1:8500"); err != nil {
		b.Skip("no route on this host:", err)
	}
	b.ReportAllocs()
	for b.Loop() {
		_, _ = RouteIP(ctx, "127.0.0.1:8500")
	}
}
