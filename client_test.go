package consulx

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/consul/api"

	"github.com/jhonsferg/consulx/internal/compat"
)

// agentSelfServer answers GET /v1/agent/self with version and records the
// last ACL token it received.
func agentSelfServer(t *testing.T, version string, token *atomic.Value) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != nil {
			token.Store(r.Header.Get("X-Consul-Token"))
		}
		if r.URL.Path != "/v1/agent/self" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"Config":{"Datacenter":"dc1","NodeName":"node-a","Version":%q}}`, version)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNewDoesNoNetworkIO(t *testing.T) {
	// Port 1 is never listening; New must still succeed.
	c, err := New(WithConsulAddress("http://127.0.0.1:1"), WithAutoRegister(false))
	if err != nil {
		t.Fatal(err)
	}
	if c.Raw() == nil {
		t.Fatal("Raw must return the official client")
	}
}

func TestNewReturnsConfigErrors(t *testing.T) {
	_, err := New() // auto-registration without a name
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("got %v", err)
	}
}

func TestAgentInfoAndFeatureGate(t *testing.T) {
	var token atomic.Value
	srv := agentSelfServer(t, "1.21.5", &token)
	c, err := New(WithConsulAddress(srv.URL), WithAutoRegister(false), WithToken("tkn"))
	if err != nil {
		t.Fatal(err)
	}

	// Before detection the version is unknown and the gate is permissive.
	if err := c.requireFeature(compat.MultiPort); err != nil {
		t.Fatalf("unknown version must be permissive: %v", err)
	}

	info, err := c.AgentInfo(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "1.21.5" || info.NodeName != "node-a" || info.Enterprise {
		t.Fatalf("info %+v", info)
	}
	if got := token.Load(); got != "tkn" {
		t.Fatalf("token header %v", got)
	}

	err = c.requireFeature(compat.MultiPort)
	var ufe *UnsupportedFeatureError
	if !errors.Is(err, ErrUnsupportedFeature) || !errors.As(err, &ufe) {
		t.Fatalf("got %v", err)
	}
	if ufe.Agent != "1.21.5" || ufe.Requirement != "Consul >= 1.22.0" {
		t.Fatalf("details %+v", ufe)
	}
}

func TestAgentInfoUnavailable(t *testing.T) {
	c, err := New(WithConsulAddress("http://127.0.0.1:1"), WithAutoRegister(false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AgentInfo(t.Context()); !errors.Is(err, ErrConsulUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestTokenFileWinsOverToken(t *testing.T) {
	var token atomic.Value
	srv := agentSelfServer(t, "1.22.7", &token)
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := New(WithConsulAddress(srv.URL), WithAutoRegister(false), WithToken("inline"), WithTokenFile(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AgentInfo(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := token.Load(); got != "from-file" {
		t.Fatalf("token header %v", got)
	}
}

func TestMissingTokenFileIsAConfigError(t *testing.T) {
	_, err := New(WithAutoRegister(false), WithTokenFile(filepath.Join(t.TempDir(), "nope")))
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("got %v", err)
	}
}

func TestTLSVerificationIsOnByDefault(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"Config":{"Version":"1.22.7"}}`)
	}))
	defer srv.Close()

	c, err := New(WithConsulAddress(srv.URL), WithAutoRegister(false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AgentInfo(t.Context()); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("self-signed certificate must be rejected, got %v", err)
	}

	insecure, err := New(WithConsulAddress(srv.URL), WithAutoRegister(false), WithTLS(TLSConfig{InsecureSkipVerify: true}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := insecure.AgentInfo(t.Context()); err != nil {
		t.Fatalf("InsecureSkipVerify must connect: %v", err)
	}
}

func TestWithHTTPClientIsUsed(t *testing.T) {
	srv := agentSelfServer(t, "2.0.4", nil)
	var calls atomic.Int32
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return http.DefaultTransport.RoundTrip(r)
	})}
	c, err := New(WithConsulAddress(srv.URL), WithAutoRegister(false), WithHTTPClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AgentInfo(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() == 0 {
		t.Fatal("custom http.Client was not used")
	}
}

func TestWithAPIConfigHook(t *testing.T) {
	c, err := New(WithAutoRegister(false), WithAPIConfig(func(conf *api.Config) { conf.PathPrefix = "/proxy" }))
	if err != nil {
		t.Fatal(err)
	}
	if c.Raw() == nil {
		t.Fatal("nil client")
	}
}

func TestEffectiveConfigIsACopy(t *testing.T) {
	c, err := New(WithServiceName("a"), WithTags("x"), WithMetadata(map[string]string{"k": "v"}), WithToken("t"))
	if err != nil {
		t.Fatal(err)
	}
	e := c.EffectiveConfig()
	e.Service.Tags[0] = "mutated"
	e.Service.Meta["k"] = "mutated"
	again := c.EffectiveConfig()
	if again.Service.Tags[0] != "x" || again.Service.Meta["k"] != "v" {
		t.Fatal("EffectiveConfig must not expose internal state")
	}
	if strings.Contains(fmt.Sprintf("%+v", again), "Token:t ") {
		t.Fatal("token printed in clear")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Regression: api.Config.WaitTime is added to every query by the official
// client, so ConsulX must not set it; watches pass their own wait.
func TestNonBlockingQueriesSendNoWait(t *testing.T) {
	var query atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query.Store(r.URL.RawQuery)
		fmt.Fprint(w, `{"Config":{"Version":"1.22.7"}}`)
	}))
	defer srv.Close()
	c, err := New(WithConsulAddress(srv.URL), WithAutoRegister(false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AgentInfo(t.Context()); err != nil {
		t.Fatal(err)
	}
	if q, _ := query.Load().(string); strings.Contains(q, "wait=") {
		t.Fatalf("non-blocking query carries a wait: %q", q)
	}
}
