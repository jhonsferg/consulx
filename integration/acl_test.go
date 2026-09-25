package integration

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/jhonsferg/consulx"
)

const rootToken = "11111111-2222-3333-4444-555555555555"

// startACLConsul runs a dev agent with ACLs enforced (default deny).
func startACLConsul(t *testing.T) *agent {
	t.Helper()
	hcl := `acl { enabled = true default_policy = "deny" enable_token_persistence = true tokens { initial_management = "` + rootToken + `" agent = "` + rootToken + `" } }`
	a := startConsul(t, "-hcl", hcl)
	a.api.AddHeader("X-Consul-Token", rootToken)
	return a
}

// serviceToken creates a least-privilege token: write access to one
// service and read access to nodes, but no agent:read.
func serviceToken(t *testing.T, a *agent, service string) string {
	t.Helper()
	rules := `service "` + service + `" { policy = "write" } node_prefix "" { policy = "read" } service_prefix "" { policy = "read" }`
	p, _, err := a.api.ACL().PolicyCreate(&api.ACLPolicy{Name: service + "-policy", Rules: rules}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := a.api.ACL().TokenCreate(&api.ACLToken{Policies: []*api.ACLTokenPolicyLink{{ID: p.ID}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return tok.SecretID
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestACLEnforced(t *testing.T) {
	a := startACLConsul(t)
	name := uniqueName(t)
	base := []consulx.Option{
		consulx.WithConsulAddress(a.addr), consulx.WithServiceName(name),
		consulx.WithServiceAddress(serviceHost), consulx.WithServicePort(9999),
		consulx.WithHealth(consulx.HealthConfig{TTL: 3 * time.Second}),
	}

	// Without a token the agent denies registration: a permanent error that
	// fails a fail-fast start immediately instead of retrying until timeout.
	anon, err := consulx.New(append(base, consulx.WithFailFast(true))...)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = anon.Start(t.Context())
	var se api.StatusError
	if !errors.Is(err, consulx.ErrRegistrationFailed) || !errors.As(err, &se) || se.Code != 403 {
		t.Fatalf("anonymous registration: %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("a permission error must not be retried during a fail-fast start")
	}

	// A least-privilege token works; it cannot read the agent version, so
	// the feature gate stays permissive.
	token := serviceToken(t, a, name)
	var logs syncBuffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c, err := consulx.New(append(base, consulx.WithToken(token), consulx.WithLogger(logger))...)
	if err != nil {
		t.Fatal(err)
	}
	stop := runUntil(t, c.Run)
	eventually(t, 20*time.Second, "registered with ACL token", func() bool {
		return len(a.instances(t, name, true)) == 1
	})
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if n := len(a.instances(t, name, false)); n != 0 {
		t.Fatalf("not deregistered with ACL token: %d", n)
	}
	if strings.Contains(logs.String(), token) {
		t.Fatal("ACL token leaked into logs")
	}
	if !strings.Contains(logs.String(), "service registered") {
		t.Fatalf("expected registration logs, got:\n%s", logs.String())
	}
}
