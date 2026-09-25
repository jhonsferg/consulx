package bind

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

type DB struct {
	Host     string        `consul:"host,required"`
	Port     int           `consul:"port" default:"5432"`
	MaxConns uint16        // matched as "max-conns"
	Timeout  time.Duration `consul:"timeout" default:"5s"`
	TLS      *bool         `consul:"tls"`
}

type Base struct {
	Name string `consul:"name"`
}

type AppConfig struct {
	Base
	Database DB                `consul:"database"`
	Features map[string]bool   `consul:"features"`
	Limits   map[string]int    `consul:"limits"`
	Hosts    []string          `consul:"hosts"`
	Ports    []int             `consul:"ports"`
	Replicas []DB              `consul:"replicas"`
	Ratio    float64           `consul:"ratio"`
	Bind     net.IP            `consul:"bind"`
	Extra    any               `consul:"extra"`
	Ignored  string            `consul:"-"`
	Labels   map[string]string `consul:"labels"`
	Optional *DB               `consul:"optional"`
	hidden   string            // unexported: never bound
}

func TestBindKVTree(t *testing.T) {
	tree := map[string]any{
		"name": "orders",
		"database": map[string]any{
			"host": "db.internal", "max-conns": "20", "timeout": "2s", "tls": "true",
		},
		"features": map[string]any{"beta": "true", "legacy": "false"},
		"limits":   map[string]any{"rps": "100"},
		"hosts":    "a, b ,c",
		"ports":    map[string]any{"1": "81", "0": "80", "10": "90"},
		"replicas": map[string]any{"0": map[string]any{"host": "r1"}},
		"ratio":    "0.75",
		"bind":     "10.0.0.1",
		"extra":    map[string]any{"k": "v"},
		"-":        "never",
		"labels":   map[string]any{"team": "payments"},
		"unknown":  "ignored",
		"hidden":   "must not be bound",
	}
	var cfg AppConfig
	cfg.Ignored = "keep"
	if err := Bind(tree, &cfg, Options{}); err != nil {
		t.Fatal(err)
	}
	d := cfg.Database
	if cfg.Name != "orders" || d.Host != "db.internal" || d.Port != 5432 || d.MaxConns != 20 || d.Timeout != 2*time.Second || d.TLS == nil || !*d.TLS {
		t.Fatalf("database %+v name %q", d, cfg.Name)
	}
	if !cfg.Features["beta"] || cfg.Features["legacy"] || cfg.Limits["rps"] != 100 || cfg.Labels["team"] != "payments" {
		t.Fatalf("maps %+v %+v", cfg.Features, cfg.Limits)
	}
	if strings.Join(cfg.Hosts, "|") != "a|b|c" {
		t.Fatalf("hosts %q", cfg.Hosts)
	}
	if len(cfg.Ports) != 3 || cfg.Ports[0] != 80 || cfg.Ports[1] != 81 || cfg.Ports[2] != 90 {
		t.Fatalf("numeric folder must become an ordered list: %v", cfg.Ports)
	}
	if len(cfg.Replicas) != 1 || cfg.Replicas[0].Host != "r1" || cfg.Replicas[0].Port != 5432 {
		t.Fatalf("replicas %+v", cfg.Replicas)
	}
	if cfg.Ratio != 0.75 || !cfg.Bind.Equal(net.ParseIP("10.0.0.1")) || cfg.Ignored != "keep" || cfg.Optional != nil || cfg.hidden != "" {
		t.Fatalf("misc %+v", cfg)
	}
	if m, ok := cfg.Extra.(map[string]any); !ok || m["k"] != "v" {
		t.Fatalf("extra %v", cfg.Extra)
	}
}

func TestBindYAMLScalars(t *testing.T) {
	// Values decoded from YAML/JSON are typed, not strings.
	tree := map[string]any{
		"database": map[string]any{"host": "h", "port": 6543, "max-conns": float64(7), "tls": false},
		"ratio":    0.5,
		"hosts":    []any{"x", "y"},
		"ports":    []any{float64(1), 2},
	}
	var cfg AppConfig
	if err := Bind(tree, &cfg, Options{}); err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Port != 6543 || cfg.Database.MaxConns != 7 || *cfg.Database.TLS || cfg.Ratio != 0.5 || len(cfg.Hosts) != 2 || cfg.Ports[1] != 2 {
		t.Fatalf("%+v", cfg)
	}
}

func TestBindReportsEveryError(t *testing.T) {
	tree := map[string]any{
		"database": map[string]any{"port": "abc", "max-conns": "70000", "timeout": "10"},
		"features": map[string]any{"beta": "maybe"},
		"ratio":    "x",
		"bind":     "not-an-ip",
	}
	var cfg AppConfig
	err := Bind(tree, &cfg, Options{})
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, key := range []string{"database/host", "database/port", "database/max-conns", "database/timeout", "features/beta", "ratio", "bind"} {
		if !strings.Contains(err.Error(), `"`+key+`"`) {
			t.Errorf("missing error for %s in:\n%v", key, err)
		}
	}
	if !errors.Is(err, ErrRequired) {
		t.Error("required error must be detectable")
	}
	var be *Error
	if !errors.As(err, &be) {
		t.Error("errors.As must find *Error")
	}
}

func TestBindKeepsExistingValuesForMissingKeys(t *testing.T) {
	cfg := AppConfig{Ratio: 1.5}
	if err := Bind(map[string]any{"database": map[string]any{"host": "h"}}, &cfg, Options{}); err != nil {
		t.Fatal(err)
	}
	if cfg.Ratio != 1.5 {
		t.Fatal("missing keys must keep existing values")
	}
}

func TestBindErrorUnused(t *testing.T) {
	var cfg AppConfig
	err := Bind(map[string]any{"database": map[string]any{"host": "h", "hots": "typo"}, "name": "x"}, &cfg, Options{ErrorUnused: true})
	if err == nil || !strings.Contains(err.Error(), `"database/hots"`) {
		t.Fatalf("got %v", err)
	}
}

func TestBindShapeErrors(t *testing.T) {
	var cfg AppConfig
	for _, tree := range []map[string]any{
		{"database": "scalar-instead-of-object"},
		{"features": "scalar-instead-of-map"},
		{"ports": map[string]any{"a": "1"}},
		{"name": map[string]any{"nested": "x"}},
	} {
		if err := Bind(tree, &cfg, Options{}); err == nil {
			t.Errorf("tree %v should fail", tree)
		}
	}
}

func TestBindRejectsBadDestination(t *testing.T) {
	var cfg AppConfig
	for _, dst := range []any{cfg, nil, new(int), (*AppConfig)(nil)} {
		if err := Bind(map[string]any{}, dst, Options{}); err == nil {
			t.Errorf("destination %T should be rejected", dst)
		}
	}
}

func FuzzBind(f *testing.F) {
	f.Add("database", "host", "x", "ports", "1,2")
	f.Add("ratio", "", "1e400", "hosts", "")
	f.Fuzz(func(t *testing.T, k1, k2, v1, k3, v3 string) {
		tree := map[string]any{k1: map[string]any{k2: v1}, k3: v3, k2: v1}
		var cfg AppConfig
		_ = Bind(tree, &cfg, Options{ErrorUnused: true}) // must never panic
	})
}

func BenchmarkBind(b *testing.B) {
	tree := map[string]any{
		"name":     "orders",
		"database": map[string]any{"host": "db", "port": "5432", "max-conns": "20", "timeout": "2s"},
		"features": map[string]any{"a": "true", "b": "false"},
		"hosts":    "a,b,c",
	}
	b.ReportAllocs()
	for b.Loop() {
		var cfg AppConfig
		if err := Bind(tree, &cfg, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestMissingSectionAppliesDefaultsAndRequired(t *testing.T) {
	var cfg AppConfig
	err := Bind(map[string]any{}, &cfg, Options{})
	if !errors.Is(err, ErrRequired) || !strings.Contains(err.Error(), `"database/host"`) {
		t.Fatalf("required key of a missing section must be reported: %v", err)
	}
	if cfg.Database.Port != 5432 || cfg.Database.Timeout != 5*time.Second {
		t.Fatalf("defaults of a missing section must apply: %+v", cfg.Database)
	}
	if cfg.Optional != nil {
		t.Fatal("missing pointer sections must stay nil")
	}
}

func TestSameKey(t *testing.T) {
	same := [][2]string{{"MaxConns", "max-conns"}, {"MaxConns", "max_conns"}, {"MaxConns", "maxconns"}, {"a.b", "AB"}, {"", "-_."}, {"ÉTÉ", "été"}}
	for _, p := range same {
		if !sameKey(p[0], p[1]) || !sameKey(p[1], p[0]) {
			t.Errorf("%q and %q must match", p[0], p[1])
		}
	}
	diff := [][2]string{{"host", "hosts"}, {"port", "sport"}, {"a", ""}, {"max-conns", "min-conns"}}
	for _, p := range diff {
		if sameKey(p[0], p[1]) || sameKey(p[1], p[0]) {
			t.Errorf("%q and %q must not match", p[0], p[1])
		}
	}
}
