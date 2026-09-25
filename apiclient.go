package consulx

import (
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/hashicorp/consul/api"
)

// newAPIClient builds the official client from a validated, defaulted
// configuration.
//
// ConsulX builds the transport itself instead of relying on api.NewClient
// defaults so that the configured dial timeout and TLS settings apply, and
// so each Client owns (and can close) its connection pool. The http.Client
// has no overall Timeout: blocking queries legitimately last up to WaitTime.
// Every request is bounded by a context instead.
func newAPIClient(s *settings) (*api.Client, *http.Transport, error) {
	cc := s.cfg.Consul
	conf := &api.Config{
		Address:    cc.Address,
		Scheme:     consulScheme(cc),
		Datacenter: cc.Datacenter,
		Namespace:  cc.Namespace,
		Partition:  cc.Partition,
		Token:      cc.Token.Reveal(),
		TokenFile:  cc.TokenFile,
		// WaitTime is deliberately not set: the official client would add
		// it to every query, blocking or not. Watches pass it explicitly.
		TLSConfig: api.TLSConfig{
			Address:            cc.TLS.ServerName,
			CAFile:             cc.TLS.CAFile,
			CAPath:             cc.TLS.CAPath,
			CAPem:              cc.TLS.CAPEM,
			CertFile:           cc.TLS.CertFile,
			KeyFile:            cc.TLS.KeyFile,
			CertPEM:            cc.TLS.CertPEM,
			KeyPEM:             cc.TLS.KeyPEM,
			InsecureSkipVerify: cc.TLS.InsecureSkipVerify,
		},
	}
	if auth := cc.HTTPAuth.Reveal(); auth != "" {
		user, pass, _ := strings.Cut(auth, ":")
		conf.HttpAuth = &api.HttpBasicAuth{Username: user, Password: pass}
	}

	var transport *http.Transport
	if s.httpClient != nil {
		conf.HttpClient = s.httpClient
	} else {
		transport = newTransport(cc.DialTimeout)
		hc, err := api.NewHttpClient(transport, conf.TLSConfig)
		if err != nil {
			return nil, nil, &ConfigError{Field: "Consul.TLS", Reason: err.Error()}
		}
		conf.Transport = transport
		conf.HttpClient = hc
	}

	if s.apiHook != nil {
		s.apiHook(conf)
	}
	client, err := api.NewClient(conf)
	if err != nil {
		// Errors from NewClient concern the address, scheme or token file.
		// They never contain the token itself.
		return nil, nil, &ConfigError{Field: "Consul", Reason: err.Error()}
	}
	return client, transport, nil
}

func consulScheme(cc ConsulConfig) string {
	switch {
	case strings.HasPrefix(cc.Address, "https://"), cc.TLS.Enabled:
		return "https"
	case cc.Scheme != "":
		return cc.Scheme
	default:
		return "http"
	}
}

// newTransport returns a pooled transport dedicated to one Client. Values
// other than the dial timeout mirror net/http's DefaultTransport.
func newTransport(dialTimeout time.Duration) *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   runtime.GOMAXPROCS(0) + 1,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}
