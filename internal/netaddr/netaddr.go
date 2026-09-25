// Package netaddr parses listen addresses and discovers the local IP
// address other hosts can use to reach this process.
package netaddr

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// ParseListenAddress parses a listen address such as ":8080", "0.0.0.0:80",
// "[::1]:8443" or "localhost:http". An empty address means ":http", as in
// net/http. Named ports are resolved with net.LookupPort.
func ParseListenAddress(ctx context.Context, addr string) (host string, port int, err error) {
	if addr == "" {
		addr = ":http"
	}
	host, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	if p == "" {
		return "", 0, fmt.Errorf("netaddr: missing port in %q", addr)
	}
	port, err = strconv.Atoi(p)
	if err != nil {
		port, err = net.DefaultResolver.LookupPort(ctx, "tcp", p)
		if err != nil {
			return "", 0, err
		}
	}
	if port < 0 || port > 65535 {
		return "", 0, fmt.Errorf("netaddr: port out of range in %q", addr)
	}
	return host, port, nil
}

// IsWildcard reports whether host is empty or an unspecified address
// (0.0.0.0, ::). Such addresses are valid for listening, never for
// registering: other hosts cannot connect to them.
func IsWildcard(host string) bool {
	if host == "" {
		return true
	}
	a, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err == nil && a.IsUnspecified()
}

// IsLoopback reports whether host is a loopback IP or "localhost".
func IsLoopback(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	a, err := netip.ParseAddr(strings.Trim(h, "[]"))
	return err == nil && a.IsLoopback()
}

// IsIPv6 reports whether host is an IPv6 literal (not an IPv4-mapped one).
func IsIPv6(host string) bool {
	a, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err == nil && a.Is6() && !a.Is4In6()
}

// ErrNoAddress is returned when no suitable address was found.
var ErrNoAddress = errors.New("netaddr: no suitable address")

// RouteIP returns the local IP the operating system would use to reach
// target ("host:port"). It "connects" a UDP socket, which selects a route
// without sending any packet. It follows the real routing table, so it
// picks the right interface in containers, pods and multi-homed hosts.
func RouteIP(ctx context.Context, target string) (netip.Addr, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", target)
	if err != nil {
		return netip.Addr{}, err
	}
	defer func() { _ = conn.Close() }()
	ua, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return netip.Addr{}, ErrNoAddress
	}
	a, ok := netip.AddrFromSlice(ua.IP)
	if !ok {
		return netip.Addr{}, ErrNoAddress
	}
	return a.Unmap(), nil
}

// Interface is the part of net.Interface the selection logic needs.
type Interface struct {
	Name  string
	Up    bool
	Loop  bool
	Addrs []netip.Prefix
}

// Interfaces lists the host interfaces.
func Interfaces() ([]Interface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]Interface, 0, len(ifs))
	for _, ifc := range ifs {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		x := Interface{
			Name: ifc.Name,
			Up:   ifc.Flags&net.FlagUp != 0,
			Loop: ifc.Flags&net.FlagLoopback != 0,
		}
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil {
				x.Addrs = append(x.Addrs, netip.PrefixFrom(p.Addr().Unmap(), p.Bits()))
			}
		}
		out = append(out, x)
	}
	return out, nil
}

// Filter selects interface addresses.
type Filter struct {
	// Name restricts the search to one interface, e.g. "eth0".
	Name string
	// IPv6 selects IPv6 addresses instead of IPv4.
	IPv6 bool
	// Public selects globally routable addresses instead of private ones.
	Public bool
}

// virtualPrefixes name interfaces created by container runtimes, VPNs and
// hypervisors on the host. Their addresses are not reachable from other
// hosts, so they are skipped unless selected explicitly by name.
var virtualPrefixes = []string{
	"docker", "br-", "veth", "virbr", "vmnet", "vboxnet", "cni", "flannel",
	"cali", "weave", "kube-", "lxc", "lxd", "podman", "tailscale", "zt",
	"vethernet", "utun", "awdl", "llw",
}

// IsVirtual reports whether an interface name looks like a virtual bridge.
func IsVirtual(name string) bool {
	n := strings.ToLower(name)
	for _, p := range virtualPrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// Select returns the first address matching f, in interface order. Link
// local, loopback and unspecified addresses are never returned.
func Select(ifs []Interface, f Filter) (netip.Addr, error) {
	for _, ifc := range ifs {
		if !ifc.Up || ifc.Loop {
			continue
		}
		if f.Name != "" {
			if ifc.Name != f.Name {
				continue
			}
		} else if IsVirtual(ifc.Name) {
			continue
		}
		for _, p := range ifc.Addrs {
			a := p.Addr()
			if a.Is6() != f.IPv6 || a.IsLoopback() || a.IsUnspecified() ||
				a.IsLinkLocalUnicast() || a.IsMulticast() {
				continue
			}
			if a.IsPrivate() == !f.Public {
				return a, nil
			}
		}
	}
	return netip.Addr{}, ErrNoAddress
}
