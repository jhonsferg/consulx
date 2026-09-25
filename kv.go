package consulx

import (
	"sync"

	"github.com/jhonsferg/consulx/kvconfig"
)

// WithKVConfig overlays the non-zero fields of k on the KV configuration.
func WithKVConfig(k KVConfig) Option {
	return optionFunc(func(s *settings) error {
		s.cfg.KV.overlay(k)
		return nil
	})
}

type kvState struct {
	once   sync.Once
	loader *kvconfig.Loader
}

// Config returns the loader for distributed configuration stored in Consul
// KV, laid out as described by KVConfig and the kvconfig package.
//
//	var cfg AppConfig
//	err := consul.Config().LoadInto(ctx, &cfg)
//
// For live updates use kvconfig.Watch:
//
//	w, err := kvconfig.Watch[AppConfig](ctx, consul.Config())
//
// Raw KV access (Get, Put, CAS, locks, transactions) is available through
// Raw().KV() with QueryOptions.WithContext.
func (c *Client) Config() *kvconfig.Loader {
	c.kv.once.Do(func() {
		k := c.cfg.KV
		name := k.Name
		if name == "" {
			name = c.cfg.Service.Name
		}
		profiles := k.Profiles
		if len(profiles) == 0 && c.cfg.Service.Environment != "" {
			profiles = []string{c.cfg.Service.Environment}
		}
		format := kvconfig.FormatKeyValue
		switch k.Format {
		case "yaml":
			format = kvconfig.FormatYAML
		case "json":
			format = kvconfig.FormatJSON
		}
		c.kv.loader = kvconfig.New(c.api, kvconfig.Config{
			Name:             name,
			Profiles:         profiles,
			Prefix:           k.Prefix,
			DefaultContext:   k.DefaultContext,
			ProfileSeparator: k.ProfileSeparator,
			Format:           format,
			DataKey:          k.DataKey,
			ErrorUnused:      k.ErrorUnused,
			RequestTimeout:   c.cfg.Consul.RequestTimeout,
			WaitTime:         c.cfg.Consul.WaitTime,
			Retry:            c.retry,
			Logger:           c.log,
			Observe: func(event string, err error) {
				switch {
				case event == "reject" || err != nil:
					c.metrics.IncCounter(MetricConfigReloadErrorsTotal)
				default:
					c.metrics.IncCounter(MetricConfigReloadTotal)
				}
			},
		})
	})
	return c.kv.loader
}
