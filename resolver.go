package consulx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/jhonsferg/consulx/internal/netaddr"
)

// AddressResolver finds the address registered in Consul for this instance.
// The address must be reachable by other hosts and by the Consul agent that
// runs the health checks. It may be an IP or a DNS name.
type AddressResolver interface {
	Resolve(ctx context.Context) (string, error)
}

// AddressResolverFunc adapts a function to AddressResolver.
type AddressResolverFunc func(ctx context.Context) (string, error)

// Resolve implements AddressResolver.
func (f AddressResolverFunc) Resolve(ctx context.Context) (string, error) { return f(ctx) }

// ErrAddressNotFound is returned by resolvers that found no usable address.
var ErrAddressNotFound = errors.New("consulx: no usable service address")

// StaticAddress always returns addr.
func StaticAddress(addr string) AddressResolver {
	return AddressResolverFunc(func(context.Context) (string, error) {
		if addr == "" {
			return "", ErrAddressNotFound
		}
		return addr, nil
	})
}

// EnvAddress returns the value of the environment variable name. It suits
// platforms that inject the instance address, such as Kubernetes with
// "status.podIP" mapped through the Downward API.
func EnvAddress(name string) AddressResolver {
	return AddressResolverFunc(func(context.Context) (string, error) {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("%w: environment variable %s is empty", ErrAddressNotFound, name)
	})
}

// RouteAddress returns the local IP address the operating system uses to
// reach target ("host:port"), without sending any packet. Pointed at the
// Consul agent, it returns the address on the network the agent can reach,
// which is correct in containers, pods and multi-homed hosts.
func RouteAddress(target string) AddressResolver {
	return AddressResolverFunc(func(ctx context.Context) (string, error) {
		a, err := netaddr.RouteIP(ctx, target)
		if err != nil {
			return "", fmt.Errorf("%w: no route to %s: %w", ErrAddressNotFound, target, err)
		}
		return a.String(), nil
	})
}

// InterfaceFilter selects an interface address for InterfaceAddress.
type InterfaceFilter struct {
	// Name restricts the search to one interface, e.g. "eth0". Without a
	// name, virtual bridges (docker0, veth*, cni*, ...) are skipped.
	Name string
	// IPv6 selects IPv6 addresses instead of IPv4.
	IPv6 bool
	// Public selects globally routable addresses instead of private ones.
	Public bool
}

// InterfaceAddress returns the first address of the host interfaces that
// matches f. Loopback, link-local and down interfaces are never selected.
func InterfaceAddress(f InterfaceFilter) AddressResolver {
	return AddressResolverFunc(func(context.Context) (string, error) {
		ifs, err := netaddr.Interfaces()
		if err != nil {
			return "", fmt.Errorf("%w: list interfaces: %w", ErrAddressNotFound, err)
		}
		a, err := netaddr.Select(ifs, netaddr.Filter{Name: f.Name, IPv6: f.IPv6, Public: f.Public})
		if err != nil {
			return "", fmt.Errorf("%w: no interface address matches %+v", ErrAddressNotFound, f)
		}
		return a.String(), nil
	})
}

// HostnameAddress resolves the host name through DNS and returns the first
// non-loopback address. In Docker the container host name resolves to the
// container IP.
func HostnameAddress() AddressResolver {
	return AddressResolverFunc(func(ctx context.Context) (string, error) {
		host, err := os.Hostname()
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrAddressNotFound, err)
		}
		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return "", fmt.Errorf("%w: resolve host name %s: %w", ErrAddressNotFound, host, err)
		}
		for _, a := range addrs {
			if !netaddr.IsLoopback(a) && !netaddr.IsWildcard(a) {
				return a, nil
			}
		}
		return "", fmt.Errorf("%w: host name %s resolves only to loopback", ErrAddressNotFound, host)
	})
}

// FirstAddress tries each resolver in order and returns the first address
// that is usable: not a wildcard and not loopback. It returns the errors of
// every resolver when none succeeds.
func FirstAddress(resolvers ...AddressResolver) AddressResolver {
	return AddressResolverFunc(func(ctx context.Context) (string, error) {
		errs := make([]error, 0, len(resolvers))
		for _, r := range resolvers {
			addr, err := r.Resolve(ctx)
			if err == nil {
				err = checkServiceAddress(addr, false)
			}
			if err == nil {
				return addr, nil
			}
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			errs = append(errs, err)
		}
		return "", errors.Join(append([]error{ErrAddressNotFound}, errs...)...)
	})
}

// checkServiceAddress rejects addresses other hosts cannot use.
func checkServiceAddress(addr string, allowLoopback bool) error {
	switch {
	case netaddr.IsWildcard(addr):
		return fmt.Errorf("%w: %q is a wildcard address", ErrAddressNotFound, addr)
	case !allowLoopback && netaddr.IsLoopback(addr):
		return fmt.Errorf("%w: %q is a loopback address (set AllowLoopback if every consumer runs on this host)", ErrAddressNotFound, addr)
	case strings.ContainsAny(addr, " /\t\r\n"):
		return fmt.Errorf("%w: %q is not a host", ErrAddressNotFound, addr)
	}
	return nil
}

// consulTarget returns "host:port" of the Consul agent for route detection,
// or "" for unix sockets.
func consulTarget(cc ConsulConfig) string {
	scheme, rest, found := strings.Cut(cc.Address, "://")
	if !found {
		rest = cc.Address
		scheme = consulScheme(cc)
	}
	if scheme == "unix" {
		return ""
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	if _, _, err := net.SplitHostPort(rest); err == nil {
		return rest
	}
	port := "8500"
	if scheme == "https" || consulScheme(cc) == "https" {
		port = "8501"
	}
	return net.JoinHostPort(strings.Trim(rest, "[]"), port)
}
