// Package kvconfig loads layered application configuration from Consul KV
// and binds it to Go structs.
//
// The layout follows Spring Cloud Consul, so existing KV trees work
// unchanged. For service "orders-api" with profile "prod", these folders are
// read, lowest precedence first; later folders override earlier ones:
//
//	config/application/
//	config/application,prod/
//	config/orders-api/
//	config/orders-api,prod/
//
// Each folder holds either individual keys (FormatKeyValue, the default),
// e.g. config/orders-api/database/host, or a single document under the data
// key (FormatYAML, FormatJSON), e.g. config/orders-api/data.
//
// Fields are mapped with the `consul` struct tag:
//
//	type AppConfig struct {
//		Database struct {
//			Host    string        `consul:"host,required"`
//			Port    int           `consul:"port" default:"5432"`
//			Timeout time.Duration `consul:"timeout" default:"5s"`
//		} `consul:"database"`
//	}
package kvconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hashicorp/consul/api"
	"gopkg.in/yaml.v3"

	"github.com/jhonsferg/consulx/internal/bind"
)

// Format is how configuration is stored in each folder.
type Format int

const (
	// FormatKeyValue stores one KV entry per setting. Key paths below the
	// folder become nested objects: database/host → {"database":{"host":…}}.
	FormatKeyValue Format = iota
	// FormatYAML stores one YAML document under the data key.
	FormatYAML
	// FormatJSON stores one JSON document under the data key.
	FormatJSON
)

// BindError describes a value that could not be bound to a field.
type BindError = bind.Error

// ErrRequired is wrapped by errors for missing required keys.
var ErrRequired = bind.ErrRequired

// ErrInvalid wraps validation failures: the configuration was read but
// rejected by Validate or a validator function.
var ErrInvalid = errors.New("consulx: invalid configuration value")

// Config configures a Loader. consulx fills it from its own configuration.
type Config struct {
	// Name is the application context, normally the service name.
	Name string
	// Profiles are active profiles, lowest precedence first, e.g. ["prod"].
	Profiles []string
	// Prefix is the root folder. Default "config".
	Prefix string
	// DefaultContext is the folder shared by every application.
	// Default "application".
	DefaultContext string
	// ProfileSeparator joins a context and a profile. Default ",".
	ProfileSeparator string
	// Format of each folder. Default FormatKeyValue.
	Format Format
	// DataKey is the key holding the document for YAML and JSON. Default "data".
	DataKey string
	// ErrorUnused reports keys that match no field, to catch typos.
	ErrorUnused bool

	// RequestTimeout bounds each non-blocking read.
	RequestTimeout time.Duration
	// WaitTime is the blocking query wait for watches. Default 5m.
	WaitTime time.Duration
	// MinInterval paces blocking queries. Default 1s.
	MinInterval time.Duration
	// Retry computes delays after failed watch requests.
	Retry interface {
		NextDelay(attempt int) (time.Duration, bool)
	}
	// Logger receives reload events. nil discards them.
	Logger *slog.Logger
	// Observe is called with "reload" or "reject" and the error, if any.
	Observe func(event string, err error)
}

// Loader reads configuration. It is safe for concurrent use.
type Loader struct {
	kv  *api.KV
	cfg Config
}

// New returns a Loader using the official client.
func New(c *api.Client, cfg Config) *Loader {
	if cfg.Prefix == "" {
		cfg.Prefix = "config"
	}
	cfg.Prefix = strings.Trim(cfg.Prefix, "/")
	if cfg.DefaultContext == "" {
		cfg.DefaultContext = "application"
	}
	if cfg.ProfileSeparator == "" {
		cfg.ProfileSeparator = ","
	}
	if cfg.DataKey == "" {
		cfg.DataKey = "data"
	}
	if cfg.WaitTime <= 0 {
		cfg.WaitTime = 5 * time.Minute
	}
	if cfg.MinInterval <= 0 {
		cfg.MinInterval = time.Second
	}
	if cfg.Retry == nil {
		cfg.Retry = fixed(5 * time.Second)
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Observe == nil {
		cfg.Observe = func(string, error) {}
	}
	return &Loader{kv: c.KV(), cfg: cfg}
}

type fixed time.Duration

func (d fixed) NextDelay(int) (time.Duration, bool) { return time.Duration(d), true }

// Contexts returns the folders read, lowest precedence first, each with a
// trailing slash.
func (l *Loader) Contexts() []string {
	c := l.cfg
	names := []string{c.DefaultContext}
	if c.Name != "" && c.Name != c.DefaultContext {
		names = append(names, c.Name)
	}
	var out []string
	for _, n := range names {
		out = append(out, c.Prefix+"/"+n+"/")
		for _, p := range c.Profiles {
			out = append(out, c.Prefix+"/"+n+c.ProfileSeparator+p+"/")
		}
	}
	return out
}

// Load reads every context and returns the merged configuration tree.
func (l *Loader) Load(ctx context.Context) (map[string]any, error) {
	tree, _, err := l.load(ctx)
	return tree, err
}

// load reads all contexts and returns the merged tree and the highest index.
func (l *Loader) load(ctx context.Context) (map[string]any, uint64, error) {
	merged := map[string]any{}
	var maxIndex uint64
	for _, prefix := range l.Contexts() {
		rctx, cancel := l.requestContext(ctx)
		pairs, meta, err := l.kv.List(prefix, (&api.QueryOptions{}).WithContext(rctx))
		cancel()
		if err != nil {
			return nil, 0, fmt.Errorf("consulx: read config %s: %w", prefix, err)
		}
		maxIndex = max(maxIndex, meta.LastIndex)
		layer, err := l.decode(prefix, pairs)
		if err != nil {
			return nil, 0, err
		}
		merge(merged, layer)
	}
	return merged, maxIndex, nil
}

func (l *Loader) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if l.cfg.RequestTimeout > 0 {
		return context.WithTimeout(ctx, l.cfg.RequestTimeout)
	}
	return context.WithCancel(ctx)
}

// decode turns the pairs of one folder into a tree.
func (l *Loader) decode(prefix string, pairs api.KVPairs) (map[string]any, error) {
	tree := map[string]any{}
	if l.cfg.Format != FormatKeyValue {
		for _, p := range pairs {
			if strings.TrimPrefix(p.Key, prefix) != l.cfg.DataKey {
				continue
			}
			var doc map[string]any
			var err error
			if l.cfg.Format == FormatJSON {
				err = json.Unmarshal(p.Value, &doc)
			} else {
				err = yaml.Unmarshal(p.Value, &doc)
			}
			if err != nil {
				return nil, fmt.Errorf("consulx: decode %s: %w", p.Key, err)
			}
			return normalizeTree(doc), nil
		}
		return tree, nil
	}
	for _, p := range pairs {
		rel := strings.TrimPrefix(p.Key, prefix)
		if rel == "" || strings.HasSuffix(rel, "/") {
			continue // folder markers
		}
		parts := strings.Split(rel, "/")
		node := tree
		conflict := false
		for _, part := range parts[:len(parts)-1] {
			next, ok := node[part].(map[string]any)
			if !ok {
				if _, isLeaf := node[part]; isLeaf {
					conflict = true
					break
				}
				next = map[string]any{}
				node[part] = next
			}
			node = next
		}
		if conflict {
			return nil, fmt.Errorf("consulx: config key %s is both a value and a folder", p.Key)
		}
		leaf := parts[len(parts)-1]
		if _, isFolder := node[leaf].(map[string]any); isFolder {
			return nil, fmt.Errorf("consulx: config key %s is both a value and a folder", p.Key)
		}
		node[leaf] = string(p.Value)
	}
	return tree, nil
}

// normalizeTree converts YAML's map[any]any-free output into plain trees
// (yaml.v3 already yields map[string]any for string keys) and deep-copies.
func normalizeTree(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = normalizeValue(v)
	}
	return out
}

func normalizeValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return normalizeTree(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalizeValue(e)
		}
		return out
	default:
		return v
	}
}

// merge deep-merges src into dst. Objects merge key by key; any other value
// replaces the destination.
func merge(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				merge(dm, sm)
				continue
			}
			cp := map[string]any{}
			merge(cp, sm)
			dst[k] = cp
			continue
		}
		dst[k] = v
	}
}

// LoadInto reads the configuration and binds it into dst, a pointer to a
// struct. If dst implements Validate() error, it is called. On any error
// dst may be partially updated; use Watch for atomic updates.
func (l *Loader) LoadInto(ctx context.Context, dst any) error {
	return l.Bind(ctx, "", dst)
}

// Bind is LoadInto restricted to the subtree at path, e.g. "database".
func (l *Loader) Bind(ctx context.Context, path string, dst any) error {
	tree, err := l.Load(ctx)
	if err != nil {
		return err
	}
	return l.bindTree(tree, path, dst)
}

func (l *Loader) bindTree(tree map[string]any, path string, dst any) error {
	sub := subtree(tree, path)
	if err := bind.Bind(sub, dst, bind.Options{ErrorUnused: l.cfg.ErrorUnused}); err != nil {
		return err
	}
	return validate(dst)
}

// subtree returns the object at a slash separated path, or an empty tree.
func subtree(tree map[string]any, path string) map[string]any {
	path = strings.Trim(path, "/")
	if path == "" {
		return tree
	}
	node := tree
	for part := range strings.SplitSeq(path, "/") {
		next, ok := node[part].(map[string]any)
		if !ok {
			return map[string]any{}
		}
		node = next
	}
	return node
}

// Validator is implemented by configuration types that check themselves.
type Validator interface {
	Validate() error
}

func validate(v any) error {
	if val, ok := v.(Validator); ok {
		if err := val.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	return nil
}
