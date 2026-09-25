package consulx

import (
	"slices"
	"testing"

	"github.com/jhonsferg/consulx/kvconfig"
)

func TestConfigLoaderFromClientSettings(t *testing.T) {
	a := fakeAgent(t, "1.22.7")
	var m countingMetrics
	c := agentClient(t, a, WithAutoRegister(false), WithMetrics(&m),
		Config{Service: ServiceConfig{Environment: "prod"}},
		WithKVConfig(KVConfig{Format: "yaml"}))

	want := []string{"config/application/", "config/application,prod/", "config/orders-api/", "config/orders-api,prod/"}
	if got := c.Config().Contexts(); !slices.Equal(got, want) {
		t.Fatalf("contexts %v", got)
	}
	a.PutKV("config/orders-api,prod/data", "database:\n  host: prod-db\n")

	type AppConfig struct {
		Database struct {
			Host string `consul:"host,required"`
		} `consul:"database"`
	}
	var cfg AppConfig
	if err := c.Config().LoadInto(t.Context(), &cfg); err != nil || cfg.Database.Host != "prod-db" {
		t.Fatalf("%+v %v", cfg, err)
	}

	w, err := kvconfig.Watch[AppConfig](t.Context(), c.Config())
	if err != nil {
		t.Fatal(err)
	}
	a.PutKV("config/orders-api,prod/data", "database:\n  host: prod-db-2\n")
	eventually(t, "config reload", func() bool { return w.Current().Database.Host == "prod-db-2" })
	eventually(t, "reload metric", func() bool { return m.count(MetricConfigReloadTotal) > 0 })
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitProfilesWinOverEnvironment(t *testing.T) {
	c := newTestClient(t, WithAutoRegister(false), WithServiceName("svc"),
		Config{Service: ServiceConfig{Environment: "prod"}, KV: KVConfig{Profiles: []string{"eu"}, Name: "billing"}})
	want := []string{"config/application/", "config/application,eu/", "config/billing/", "config/billing,eu/"}
	if got := c.Config().Contexts(); !slices.Equal(got, want) {
		t.Fatalf("contexts %v", got)
	}
}
