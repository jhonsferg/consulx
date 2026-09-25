package consulx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadConfig reads a YAML or JSON configuration file and overlays the
// environment (ConfigFromEnv) on top of it, so environment variables win
// over the file. The result is meant to be passed to New, where explicit
// Config values and options are applied on top.
//
// Unknown keys are rejected to surface typos. Durations are written as Go
// duration strings ("10s", "1m30s").
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("consulx: read config file: %w", err)
	}
	cfg, err := ParseConfig(data)
	if err != nil {
		return Config{}, fmt.Errorf("%w (file %s)", err, path)
	}
	env, err := ConfigFromEnv()
	if err != nil {
		return Config{}, err
	}
	cfg.overlay(env)
	return cfg, nil
}

// ParseConfig decodes a YAML or JSON document into a Config without reading
// the environment. An empty document yields a zero Config.
func ParseConfig(data []byte) (Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, &ConfigError{Field: "file", Reason: err.Error()}
	}
	return cfg, nil
}
