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
