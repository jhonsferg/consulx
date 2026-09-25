package compat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want Version
	}{
		{"1.22.7", Version{Major: 1, Minor: 22, Patch: 7, Raw: "1.22.7"}},
		{"2.0.4+ent", Version{Major: 2, Minor: 0, Patch: 4, Enterprise: true, Raw: "2.0.4+ent"}},
		{"v1.22.7+ent.fips1402", Version{Major: 1, Minor: 22, Patch: 7, Enterprise: true, Raw: "v1.22.7+ent.fips1402"}},
		{"1.21.0-rc1", Version{Major: 1, Minor: 21, Pre: "rc1", Raw: "1.21.0-rc1"}},
		{"1.22.0-rc2+ent", Version{Major: 1, Minor: 22, Pre: "rc2", Enterprise: true, Raw: "1.22.0-rc2+ent"}},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Parse(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	for _, in := range []string{"", "1.22", "1.x.3", "1.2.3.4", "-1.0.0", "entirely wrong"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) should fail", in)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"1.22.7", "2.0.4+ent", "1.21.0-rc1", "v1.0.0", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, err := Parse(s)
		if err == nil && (v.Major < 0 || v.Minor < 0 || v.Patch < 0) {
			t.Fatalf("negative component from %q: %+v", s, v)
		}
	})
}

func TestSupportsMatchesVerifiedBehaviour(t *testing.T) {
	// Evidence: docs/compatibility.md section 1.1.
	v1215, _ := Parse("1.21.5")
	v1227, _ := Parse("1.22.7")
	v204, _ := Parse("2.0.4")
	v204ent, _ := Parse("2.0.4+ent")

	cases := []struct {
		v    Version
		f    Feature
		want bool
	}{
		{v1215, MultiPort, false},
		{v1227, MultiPort, true},
		{v204, MultiPort, true},
		{v1227, AIService, false},
		{v204, AIService, false},
		{v204, Namespaces, false},
		{v204ent, Namespaces, true},
		{v204, Partitions, false},
		{v204ent, Partitions, true},
		{v1215, IPv6Address, false},
		{v1227, IPv6Address, true},
		{Unknown, MultiPort, true},
		{Unknown, AIService, false},
	}
	for _, c := range cases {
		if got := c.v.Supports(c.f); got != c.want {
			t.Errorf("%s supports %s = %v, want %v", c.v, c.f.Name(), got, c.want)
		}
	}
}

func TestRequirementDescriptions(t *testing.T) {
	if got := MultiPort.Requirement(); got != "Consul >= 1.22.0" {
		t.Errorf("MultiPort: %q", got)
	}
	if got := Namespaces.Requirement(); got != "Consul Enterprise" {
		t.Errorf("Namespaces: %q", got)
	}
}

func TestDetect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agent/self" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Config":{"Datacenter":"dc2","NodeName":"n1","Version":"1.22.7+ent"}}`))
	}))
	defer srv.Close()

	c, err := api.NewClient(&api.Config{Address: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	info, err := Detect(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if info.Datacenter != "dc2" || info.NodeName != "n1" || !info.Version.Enterprise || info.Version.Minor != 22 {
		t.Fatalf("unexpected info %+v", info)
	}
}

func TestDetectHonoursCancellation(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)

	c, err := api.NewClient(&api.Config{Address: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := Detect(ctx, c); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}
