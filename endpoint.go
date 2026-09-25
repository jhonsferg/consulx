package consulx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/jhonsferg/consulx/internal/netaddr"
)

// endpoint is where other hosts reach this instance.
type endpoint struct {
	Address       string
	AddressSource string
	Port          int
	PortSource    string
	Scheme        string
}

// resolveEndpoint determines the registered address, port and scheme.
//
// Port, first match wins: Service.Port, the default entry of Service.Ports,
// the listener, the http.Server address.
//
// Address, first match wins: Service.Address; then either the custom
// AddressResolver or the default chain: the variable named by
// Service.AddressEnv, a concrete host in the http.Server address, the local
// IP routing to the Consul agent, and the first private interface address.
func (c *Client) resolveEndpoint(ctx context.Context) (endpoint, error) {
	var ep endpoint
	port, portSource, err := c.resolvePort(ctx)
	if err != nil {
		return ep, err
	}
	ep.Port, ep.PortSource = port, portSource
	ep.Scheme = c.scheme

	allowLoop := c.cfg.Service.AllowLoopback
	if addr := c.cfg.Service.Address; addr != "" {
		if err := checkServiceAddress(addr, allowLoop); err != nil {
			return ep, &ConfigError{Field: "Service.Address", Reason: err.Error()}
		}
		ep.Address, ep.AddressSource = addr, "config"
		return ep, nil
	}

	type source struct {
		name string
		r    AddressResolver
	}
	var chain []source
	if c.resolver != nil {
		chain = append(chain, source{"resolver", c.resolver})
	} else {
		if name := c.cfg.Service.AddressEnv; name != "" {
			chain = append(chain, source{"env " + name, EnvAddress(name)})
		}
		if host := c.serverHost(ctx); host != "" {
			chain = append(chain, source{"server", StaticAddress(host)})
		}
		if target := consulTarget(c.cfg.Consul); target != "" {
			chain = append(chain, source{"route", RouteAddress(target)})
		}
		chain = append(chain, source{"interface", InterfaceAddress(InterfaceFilter{IPv6: c.cfg.Service.PreferIPv6})})
	}

	var errs []error
	for _, s := range chain {
		addr, err := s.r.Resolve(ctx)
		if err == nil {
			err = checkServiceAddress(addr, allowLoop)
		}
		if err == nil {
			ep.Address, ep.AddressSource = addr, s.name
			return ep, nil
		}
		if ctx.Err() != nil {
			return ep, ctx.Err()
		}
		c.log.Debug("address source skipped", "source", s.name, "error", err)
		errs = append(errs, fmt.Errorf("%s: %w", s.name, err))
	}
	return ep, &ConfigError{
		Field:  "Service.Address",
		Reason: "cannot determine the service address; set WithServiceAddress: " + errors.Join(errs...).Error(),
	}
}

// resolvePort returns the registered port and where it came from.
func (c *Client) resolvePort(ctx context.Context) (int, string, error) {
	if p := c.cfg.Service.Port; p != 0 {
		return p, "config", nil
	}
	for _, p := range c.cfg.Service.Ports {
		if p.Default {
			return p.Port, "ports", nil
		}
	}
	if c.listener != nil {
		if ta, ok := c.listener.Addr().(*net.TCPAddr); ok && ta.Port != 0 {
			return ta.Port, "listener", nil
		}
	}
	if c.server != nil {
		_, port, err := netaddr.ParseListenAddress(ctx, c.server.Addr)
		if err != nil {
			return 0, "", &ConfigError{Field: "Server.Addr", Reason: err.Error()}
		}
		if port != 0 {
			return port, "server", nil
		}
		return 0, "", &ConfigError{Field: "Service.Port", Reason: "the server listens on port 0; pass WithListener or WithServicePort"}
	}
	return 0, "", &ConfigError{Field: "Service.Port", Reason: "unknown; pass WithServer, WithListener or WithServicePort"}
}

// serverHost returns the host part of the server address when it is a
// concrete host usable by others, or "".
func (c *Client) serverHost(ctx context.Context) string {
	if c.server == nil {
		return ""
	}
	host, _, err := netaddr.ParseListenAddress(ctx, c.server.Addr)
	if err != nil || netaddr.IsWildcard(host) {
		return ""
	}
	return strings.Trim(host, "[]")
}
