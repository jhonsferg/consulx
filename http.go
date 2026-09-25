package consulx

import (
	"net"
	"net/http"

	"github.com/jhonsferg/consulx/health"
)

// WithServer integrates the application's HTTP server. ConsulX never
// starts, stops or replaces it. The registered port is taken from
// srv.Addr, and, when health endpoints are enabled, srv.Handler is wrapped
// so the health paths are served in front of the original handler.
//
// Call New before the server starts serving: net/http reads srv.Handler on
// every request, so it cannot be changed safely afterwards.
func WithServer(srv *http.Server) Option {
	return optionFunc(func(s *settings) error {
		if srv == nil {
			return &ConfigError{Field: "Server", Reason: "must not be nil"}
		}
		s.server = srv
		return nil
	})
}

// WithListener provides the listener the server uses. The registered port
// is taken from it, which is required when the server listens on port 0.
// ConsulX never accepts on or closes the listener.
func WithListener(ln net.Listener) Option {
	return optionFunc(func(s *settings) error {
		if ln == nil {
			return &ConfigError{Field: "Listener", Reason: "must not be nil"}
		}
		s.listener = ln
		return nil
	})
}

// WithAddressResolver replaces the automatic address resolution chain. An
// explicit Service.Address still takes precedence.
func WithAddressResolver(r AddressResolver) Option {
	return optionFunc(func(s *settings) error {
		if r == nil {
			return &ConfigError{Field: "AddressResolver", Reason: "must not be nil"}
		}
		s.resolver = r
		return nil
	})
}

// Health returns the registry the application uses to report the health of
// its components. The injected endpoints and TTL heartbeats read it.
func (c *Client) Health() *health.Registry { return c.health }

// HealthHandler returns a handler that serves the configured health
// endpoints and answers 404 for any other path. Use it to mount the
// endpoints manually, for example on a separate management server, when
// WithServer is not used.
func (c *Client) HealthHandler() http.Handler {
	return c.healthMux(http.NotFoundHandler())
}

// healthMux serves exact health paths and delegates everything else to next.
type healthMux struct {
	routes map[string]http.Handler
	next   http.Handler
}

func (m *healthMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h, ok := m.routes[r.URL.Path]; ok {
		h.ServeHTTP(w, r)
		return
	}
	m.next.ServeHTTP(w, r)
}

func (c *Client) healthMux(next http.Handler) *healthMux {
	h := c.cfg.Health
	opts := health.HandlerOptions{HideDetails: h.HideDetails, DegradedStatusCode: h.DegradedStatusCode}
	routes := map[string]http.Handler{}
	if p := h.Endpoints.Health; p != "" {
		routes[p] = health.Handler(c.health.Health, opts)
	}
	if p := h.Endpoints.Live; p != "" {
		routes[p] = health.Handler(c.health.Live, opts)
	}
	if p := h.Endpoints.Ready; p != "" {
		routes[p] = health.Handler(c.health.Ready, opts)
	}
	return &healthMux{routes: routes, next: next}
}

// detectScheme decides the registered scheme. It runs in New, before the
// server starts: http.Server.Serve initialises TLSConfig itself (HTTP/2
// setup), so reading it later would both race with Serve and wrongly report
// https for a plain HTTP server.
func (c *Client) detectScheme() string {
	if s := c.cfg.Service.Scheme; s != "" {
		return s
	}
	if c.server != nil && c.server.TLSConfig != nil {
		return "https"
	}
	return "http"
}

// injectHealth wraps the server handler when health endpoints are enabled.
// A nil handler means http.DefaultServeMux, exactly as net/http does; the
// mux itself is captured, so routes registered on it later still work.
func (c *Client) injectHealth() {
	if c.server == nil || !c.cfg.Health.Enabled {
		return
	}
	next := c.server.Handler
	if next == nil {
		next = http.DefaultServeMux
	}
	c.server.Handler = c.healthMux(next)
}
