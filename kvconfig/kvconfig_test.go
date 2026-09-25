package kvconfig

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"go.uber.org/goleak"

	"github.com/jhonsferg/consulx/internal/fakeconsul"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type DB struct {
	Host     string        `consul:"host,required"`
	Port     int           `consul:"port" default:"5432"`
	MaxConns int           `consul:"max-conns"`
	Timeout  time.Duration `consul:"timeout" default:"5s"`
}

type AppConfig struct {
	Database DB              `consul:"database"`
	Features map[string]bool `consul:"features"`
	LogLevel string          `consul:"log-level" default:"info"`
}

func (c AppConfig) Validate() error {
	if c.Database.MaxConns < 0 {
		return errors.New("max-conns must not be negative")
	}
	return nil
}

func setup(t *testing.T, cfg Config) (*fakeconsul.Agent, *Loader) {
	t.Helper()
	a := fakeconsul.New("1.22.7")
	t.Cleanup(a.Close)
	raw, err := api.NewClient(&api.Config{Address: a.URL()})
	if err != nil {
		t.Fatal(err)
	}
	cfg.MinInterval = time.Millisecond
	cfg.WaitTime = time.Second
	cfg.Retry = fixed(10 * time.Millisecond)
	return a, New(raw, cfg)
}

func TestContexts(t *testing.T) {
	_, l := setup(t, Config{Name: "orders-api", Profiles: []string{"prod", "eu"}})
	want := []string{
		"config/application/", "config/application,prod/", "config/application,eu/",
		"config/orders-api/", "config/orders-api,prod/", "config/orders-api,eu/",
	}
	if got := l.Contexts(); !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	_, custom := setup(t, Config{Name: "a", Prefix: "/cfg/", DefaultContext: "shared", ProfileSeparator: "::", Profiles: []string{"dev"}})
	if got := custom.Contexts(); !slices.Equal(got, []string{"cfg/shared/", "cfg/shared::dev/", "cfg/a/", "cfg/a::dev/"}) {
		t.Fatalf("custom layout %v", got)
	}
}

func TestLayeredKeyValuePrecedence(t *testing.T) {
	a, l := setup(t, Config{Name: "orders-api", Profiles: []string{"prod"}})
	a.PutKV("config/application/database/host", "shared-db")
	a.PutKV("config/application/database/max-conns", "5")
	a.PutKV("config/application/log-level", "debug")
	a.PutKV("config/application,prod/database/max-conns", "50")
	a.PutKV("config/orders-api/database/host", "orders-db")
	a.PutKV("config/orders-api/features/beta", "true")
	a.PutKV("config/orders-api,prod/log-level", "warn")
	a.PutKV("config/orders-api/", "") // folder marker
	a.PutKV("config/other-app/database/host", "not-mine")

	var cfg AppConfig
	if err := l.LoadInto(t.Context(), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Host != "orders-db" || cfg.Database.MaxConns != 50 || cfg.Database.Port != 5432 ||
		cfg.Database.Timeout != 5*time.Second || !cfg.Features["beta"] || cfg.LogLevel != "warn" {
		t.Fatalf("got %+v", cfg)
	}

	var db DB
	if err := l.Bind(t.Context(), "database", &db); err != nil || db.Host != "orders-db" {
		t.Fatalf("Bind subtree: %+v %v", db, err)
	}
}

func TestYAMLAndJSONFormats(t *testing.T) {
	a, l := setup(t, Config{Name: "orders-api", Format: FormatYAML})
	a.PutKV("config/application/data", "database:\n  host: shared\n  port: 6000\nlog-level: debug\n")
	a.PutKV("config/orders-api/data", "database:\n  host: orders\n  timeout: 2s\nfeatures:\n  beta: true\n")
	var cfg AppConfig
	if err := l.LoadInto(t.Context(), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Host != "orders" || cfg.Database.Port != 6000 || cfg.Database.Timeout != 2*time.Second || !cfg.Features["beta"] || cfg.LogLevel != "debug" {
		t.Fatalf("yaml: %+v", cfg)
	}

	a2, lj := setup(t, Config{Name: "orders-api", Format: FormatJSON, DataKey: "settings"})
	a2.PutKV("config/orders-api/settings", `{"database":{"host":"j","port":7000,"max-conns":3}}`)
	var cj AppConfig
	if err := lj.LoadInto(t.Context(), &cj); err != nil {
		t.Fatal(err)
	}
	if cj.Database.Host != "j" || cj.Database.Port != 7000 || cj.Database.MaxConns != 3 {
		t.Fatalf("json: %+v", cj)
	}

	a2.PutKV("config/orders-api/settings", `{"database":`)
	if err := lj.LoadInto(t.Context(), &cj); err == nil || !strings.Contains(err.Error(), "config/orders-api/settings") {
		t.Fatalf("malformed document must be reported with its key: %v", err)
	}
}

func TestLoadErrors(t *testing.T) {
	a, l := setup(t, Config{Name: "orders-api", ErrorUnused: true})
	var cfg AppConfig
	err := l.LoadInto(t.Context(), &cfg)
	if !errors.Is(err, ErrRequired) {
		t.Fatalf("missing required key: %v", err)
	}

	a.PutKV("config/orders-api/database/host", "h")
	a.PutKV("config/orders-api/database/max-conns", "-1")
	if err := l.LoadInto(t.Context(), &cfg); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate must run: %v", err)
	}

	a.PutKV("config/orders-api/database/max-conns", "1")
	a.PutKV("config/orders-api/databse/host", "typo")
	var be *BindError
	if err := l.LoadInto(t.Context(), &cfg); !errors.As(err, &be) || !strings.Contains(err.Error(), "databse") {
		t.Fatalf("ErrorUnused must report typos: %v", err)
	}

	a.PutKV("config/orders-api/log-level/nested", "x")
	a.PutKV("config/orders-api/log-level", "y")
	if err := l.LoadInto(t.Context(), &cfg); err == nil || !strings.Contains(err.Error(), "both a value and a folder") {
		t.Fatalf("value/folder conflict: %v", err)
	}

	a.SetFailing(500)
	if err := l.LoadInto(t.Context(), &cfg); err == nil {
		t.Fatal("consul errors must be returned")
	}
}

func TestWatchAppliesValidChangesAndRejectsInvalid(t *testing.T) {
	a, l := setup(t, Config{Name: "orders-api"})
	a.PutKV("config/orders-api/database/host", "db1")

	var mu = make(chan [2]AppConfig, 4)
	w, err := Watch(t.Context(), l,
		WithValidator(func(c AppConfig) error {
			if c.Database.Host == "forbidden" {
				return errors.New("host not allowed")
			}
			return nil
		}),
		OnChange(func(old, new AppConfig) { mu <- [2]AppConfig{old, new} }),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.Current().Database.Host != "db1" {
		t.Fatalf("initial %+v", w.Current())
	}

	a.PutKV("config/orders-api/database/host", "db2")
	select {
	case v := <-w.Changes():
		if v.Database.Host != "db2" {
			t.Fatalf("change %+v", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("change not delivered")
	}
	if pair := <-mu; pair[0].Database.Host != "db1" || pair[1].Database.Host != "db2" {
		t.Fatalf("OnChange %+v", pair)
	}

	// Invalid by custom validator, then by Validate: both rejected.
	for _, bad := range []struct{ key, val string }{
		{"config/orders-api/database/host", "forbidden"},
		{"config/orders-api/database/max-conns", "-5"},
	} {
		a.PutKV(bad.key, bad.val)
		select {
		case err := <-w.Errors():
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("rejection must match ErrInvalid: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("rejection not reported")
		}
		if w.Current().Database.Host != "db2" {
			t.Fatalf("invalid configuration applied: %+v", w.Current())
		}
		a.DeleteKV(bad.key)
		if bad.key == "config/orders-api/database/host" {
			a.PutKV(bad.key, "db2")
		}
	}

	// A change in an unrelated application does not publish anything.
	a.PutKV("config/other/x", "1")
	select {
	case v := <-w.Changes():
		t.Fatalf("unexpected change %+v", v)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWatchReportsOneRejectionPerChange(t *testing.T) {
	a, l := setup(t, Config{Name: "orders-api"})
	a.PutKV("config/orders-api/database/host", "db1")

	w, err := Watch(t.Context(), l, WithValidator(func(c AppConfig) error {
		if c.Database.Host == "forbidden" {
			return errors.New("host not allowed")
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	a.PutKV("config/orders-api/database/host", "forbidden")
	select {
	case err := <-w.Errors():
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("rejection must match ErrInvalid: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("rejection not reported")
	}
	// One write wakes every context folder, including the empty
	// config/application/: the same rejected state must be reported once.
	select {
	case err := <-w.Errors():
		t.Fatalf("rejection reported twice: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	if w.Current().Database.Host != "db1" {
		t.Fatalf("invalid configuration applied: %+v", w.Current())
	}

	// A later, different rejection is reported again.
	a.PutKV("config/orders-api/database/max-conns", "-5")
	select {
	case err := <-w.Errors():
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("rejection must match ErrInvalid: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second rejection not reported")
	}
}

func TestWatchInitialFailure(t *testing.T) {
	_, l := setup(t, Config{Name: "orders-api"})
	if _, err := Watch[AppConfig](t.Context(), l); !errors.Is(err, ErrRequired) {
		t.Fatalf("got %v", err)
	}
}

func TestWatchRecoversAfterOutage(t *testing.T) {
	a, l := setup(t, Config{Name: "orders-api"})
	a.PutKV("config/orders-api/database/host", "db1")
	w, err := Watch[AppConfig](t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	a.SetFailing(500)
	select {
	case <-w.Errors():
	case <-time.After(5 * time.Second):
		t.Fatal("outage not reported")
	}
	a.SetFailing(0)
	a.PutKV("config/orders-api/database/host", "db3")
	deadline := time.After(5 * time.Second)
	for w.Current().Database.Host != "db3" {
		select {
		case <-w.Changes():
		case <-deadline:
			t.Fatalf("no recovery, current %+v", w.Current())
		}
	}
}

func TestWatchStopsOnContext(t *testing.T) {
	a, l := setup(t, Config{Name: "orders-api"})
	a.PutKV("config/orders-api/database/host", "db1")
	ctx, cancel := context.WithCancel(t.Context())
	w, err := Watch[AppConfig](ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-w.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not stop")
	}
	if _, ok := <-w.Changes(); ok {
		t.Fatal("changes must be closed")
	}
	_ = w.Close()
}

func TestMerge(t *testing.T) {
	dst := map[string]any{"a": map[string]any{"x": "1", "y": "2"}, "b": "keep", "c": map[string]any{"z": "1"}}
	src := map[string]any{"a": map[string]any{"y": "3"}, "c": "replaced", "d": map[string]any{"n": "1"}}
	merge(dst, src)
	a := dst["a"].(map[string]any)
	if a["x"] != "1" || a["y"] != "3" || dst["b"] != "keep" || dst["c"] != "replaced" || dst["d"].(map[string]any)["n"] != "1" {
		t.Fatalf("merge %v", dst)
	}
	src["d"].(map[string]any)["n"] = "mutated"
	if dst["d"].(map[string]any)["n"] != "1" {
		t.Fatal("merge must copy nested objects")
	}
}

// Two different invalid values can share an error message; each one is a
// new rejected state and must be reported.
func TestWatchReportsDistinctRejectionsWithSameMessage(t *testing.T) {
	a, l := setup(t, Config{Name: "orders-api"})
	a.PutKV("config/orders-api/database/host", "db1")
	w, err := Watch(t.Context(), l, WithValidator(func(c AppConfig) error {
		if strings.HasPrefix(c.Database.Host, "forbidden") {
			return errors.New("host not allowed")
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	for _, host := range []string{"forbidden-1", "forbidden-2"} {
		a.PutKV("config/orders-api/database/host", host)
		select {
		case err := <-w.Errors():
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("%s: rejection must match ErrInvalid: %v", host, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: rejection not reported", host)
		}
	}
}
