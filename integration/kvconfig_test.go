package integration

import (
	"errors"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/jhonsferg/consulx"
	"github.com/jhonsferg/consulx/kvconfig"
)

type appConfig struct {
	Database struct {
		Host     string        `consul:"host,required"`
		Port     int           `consul:"port" default:"5432"`
		MaxConns int           `consul:"max-conns"`
		Timeout  time.Duration `consul:"timeout" default:"5s"`
	} `consul:"database"`
	Features map[string]bool `consul:"features"`
}

func (c appConfig) Validate() error {
	if c.Database.MaxConns > 1000 {
		return errors.New("max-conns too high")
	}
	return nil
}

func TestDistributedConfiguration(t *testing.T) {
	a := startConsul(t)
	name := uniqueName(t)
	put := func(k, v string) {
		t.Helper()
		if _, err := a.api.KV().Put(&api.KVPair{Key: k, Value: []byte(v)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	put("config/application/database/host", "shared-db")
	put("config/application/database/max-conns", "10")
	put("config/application,prod/database/timeout", "2s")
	put("config/"+name+"/database/host", "orders-db")
	put("config/"+name+",prod/features/beta", "true")

	c, err := consulx.New(consulx.WithConsulAddress(a.addr), consulx.WithServiceName(name),
		consulx.WithAutoRegister(false), consulx.Config{Service: consulx.ServiceConfig{Environment: "prod"}})
	if err != nil {
		t.Fatal(err)
	}

	var cfg appConfig
	if err := c.Config().LoadInto(t.Context(), &cfg); err != nil {
		t.Fatal(err)
	}
	d := cfg.Database
	if d.Host != "orders-db" || d.MaxConns != 10 || d.Timeout != 2*time.Second || d.Port != 5432 || !cfg.Features["beta"] {
		t.Fatalf("layered config %+v", cfg)
	}

	w, err := kvconfig.Watch[appConfig](t.Context(), c.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	put("config/"+name+"/database/max-conns", "50")
	select {
	case v := <-w.Changes():
		if v.Database.MaxConns != 50 {
			t.Fatalf("change %+v", v)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("change not delivered")
	}

	put("config/"+name+"/database/max-conns", "5000") // rejected by Validate
	select {
	case err := <-w.Errors():
		if !errors.Is(err, kvconfig.ErrInvalid) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("invalid change not reported")
	}
	if w.Current().Database.MaxConns != 50 {
		t.Fatalf("invalid change applied: %+v", w.Current())
	}
}

func TestYAMLConfiguration(t *testing.T) {
	a := startConsul(t)
	name := uniqueName(t)
	doc := "database:\n  host: yaml-db\n  port: 6000\nfeatures:\n  beta: true\n"
	if _, err := a.api.KV().Put(&api.KVPair{Key: "config/" + name + "/data", Value: []byte(doc)}, nil); err != nil {
		t.Fatal(err)
	}
	c, err := consulx.New(consulx.WithConsulAddress(a.addr), consulx.WithServiceName(name),
		consulx.WithAutoRegister(false), consulx.WithKVConfig(consulx.KVConfig{Format: "yaml"}))
	if err != nil {
		t.Fatal(err)
	}
	var cfg appConfig
	if err := c.Config().LoadInto(t.Context(), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Host != "yaml-db" || cfg.Database.Port != 6000 || !cfg.Features["beta"] {
		t.Fatalf("%+v", cfg)
	}
}
