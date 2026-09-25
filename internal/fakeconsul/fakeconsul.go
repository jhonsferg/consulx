// Package fakeconsul is an in-process fake of the Consul agent HTTP API for
// unit tests. It implements only what ConsulX uses, mimics the behaviours
// that matter (strict decoding of version-gated fields, hash-based blocking
// on agent services, 404 for unknown services) and lets tests inject
// failures. Integration tests use a real agent instead.
package fakeconsul

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
)

// TTLUpdate is one received TTL check update.
type TTLUpdate struct {
	CheckID string
	Status  string
	Output  string
}

// Agent is a fake Consul agent. It is safe for concurrent use.
type Agent struct {
	srv *httptest.Server

	mu          sync.Mutex
	version     string
	services    map[string]*api.AgentServiceRegistration
	registers   int
	deregisters []string
	ttl         []TTLUpdate
	maintenance map[string]string
	tokens      []string
	failAll     int           // status code returned for every request; 0 = healthy
	hang        chan struct{} // non-nil: requests block until closed (silent partition)
	failNext    int           // number of register requests to fail with 500
	forbidSelf  bool
	changed     chan struct{} // closed and replaced on every service change
	health      map[string][]*api.ServiceEntry
	kv          map[string][]byte
	index       uint64
	queries     []url.Values
}

// New starts a fake agent reporting version. Close it with t.Cleanup.
func New(version string) *Agent {
	a := &Agent{
		version:     version,
		services:    map[string]*api.AgentServiceRegistration{},
		maintenance: map[string]string{},
		health:      map[string][]*api.ServiceEntry{},
		kv:          map[string][]byte{},
		index:       1,
		changed:     make(chan struct{}),
	}
	a.srv = httptest.NewServer(http.HandlerFunc(a.serve))
	return a
}

// URL is the agent address.
func (a *Agent) URL() string { return a.srv.URL }

// Close stops the server.
func (a *Agent) Close() {
	a.srv.CloseClientConnections()
	a.srv.Close()
}

// SetFailing makes every request answer code (0 restores normal service).
func (a *Agent) SetFailing(code int) {
	a.mu.Lock()
	a.failAll = code
	a.mu.Unlock()
	if code != 0 {
		a.srv.CloseClientConnections() // break pending blocking queries
	}
}

// FailNextRegisters makes the next n registrations answer 500.
func (a *Agent) FailNextRegisters(n int) {
	a.mu.Lock()
	a.failNext = n
	a.mu.Unlock()
}

// ForbidSelf makes GET /v1/agent/self answer 403, like a token without
// agent:read.
func (a *Agent) ForbidSelf() {
	a.mu.Lock()
	a.forbidSelf = true
	a.mu.Unlock()
}

// Forget drops every service, like an agent restarted without state.
func (a *Agent) Forget() {
	a.mu.Lock()
	a.services = map[string]*api.AgentServiceRegistration{}
	a.notifyLocked()
	a.mu.Unlock()
}

// Service returns a registered service definition.
func (a *Agent) Service(id string) (*api.AgentServiceRegistration, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.services[id]
	return s, ok
}

// Registers returns the number of successful registrations.
func (a *Agent) Registers() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.registers
}

// Deregisters returns the deregistered service IDs.
func (a *Agent) Deregisters() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.deregisters...)
}

// TTLUpdates returns the received TTL updates.
func (a *Agent) TTLUpdates() []TTLUpdate {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]TTLUpdate(nil), a.ttl...)
}

// Maintenance returns the maintenance reason of a service and whether it
// is in maintenance.
func (a *Agent) Maintenance(id string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.maintenance[id]
	return r, ok
}

// Tokens returns the ACL tokens seen, in order.
func (a *Agent) Tokens() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.tokens...)
}

func (a *Agent) notifyLocked() {
	close(a.changed)
	a.changed = make(chan struct{})
}

func (a *Agent) serve(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.tokens = append(a.tokens, r.Header.Get("X-Consul-Token"))
	fail := a.failAll
	hang := a.hang
	a.mu.Unlock()
	if hang != nil {
		select {
		case <-hang:
		case <-r.Context().Done():
			return
		}
	}
	if fail != 0 {
		http.Error(w, "injected failure", fail)
		return
	}

	p := r.URL.Path
	switch {
	case r.Method == http.MethodGet && p == "/v1/agent/self":
		a.self(w)
	case r.Method == http.MethodGet && p == "/v1/status/leader":
		writeJSON(w, `"127.0.0.1:8300"`)
	case r.Method == http.MethodPut && p == "/v1/agent/service/register":
		a.register(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(p, "/v1/agent/service/deregister/"):
		a.deregister(w, strings.TrimPrefix(p, "/v1/agent/service/deregister/"))
	case r.Method == http.MethodPut && strings.HasPrefix(p, "/v1/agent/service/maintenance/"):
		a.setMaintenance(w, r, strings.TrimPrefix(p, "/v1/agent/service/maintenance/"))
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v1/agent/service/"):
		a.getService(w, r, strings.TrimPrefix(p, "/v1/agent/service/"))
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v1/kv/"):
		a.kvRead(w, r, strings.TrimPrefix(p, "/v1/kv/"))
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v1/health/service/"):
		a.healthService(w, r, strings.TrimPrefix(p, "/v1/health/service/"))
	case r.Method == http.MethodPut && strings.HasPrefix(p, "/v1/agent/check/update/"):
		a.updateTTL(w, r, strings.TrimPrefix(p, "/v1/agent/check/update/"))
	default:
		http.Error(w, "Invalid URL path: not a recognized HTTP API endpoint", http.StatusNotFound)
	}
}

func (a *Agent) self(w http.ResponseWriter) {
	a.mu.Lock()
	forbid, v := a.forbidSelf, a.version
	a.mu.Unlock()
	if forbid {
		http.Error(w, "Permission denied", http.StatusForbidden)
		return
	}
	writeJSON(w, fmt.Sprintf(`{"Config":{"Datacenter":"dc1","NodeName":"fake","Version":%q}}`, v))
}

func (a *Agent) register(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	if a.failNext > 0 {
		a.failNext--
		a.mu.Unlock()
		http.Error(w, "injected register failure", http.StatusInternalServerError)
		return
	}
	version := a.version
	a.mu.Unlock()

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		http.Error(w, "Request decode failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Mimic strict decoding of fields unknown to older agents (verified
	// against real agents, see docs/compatibility.md).
	if _, ok := raw["Ports"]; ok && versionLess(version, 1, 22) {
		http.Error(w, `Request decode failed: json: unknown field "Ports"`, http.StatusBadRequest)
		return
	}
	if _, ok := raw["AI"]; ok {
		http.Error(w, `Request decode failed: json: unknown field "AI"`, http.StatusBadRequest)
		return
	}
	body, _ := json.Marshal(raw)
	var reg api.AgentServiceRegistration
	if err := json.Unmarshal(body, &reg); err != nil || reg.Name == "" {
		http.Error(w, "Missing service name", http.StatusBadRequest)
		return
	}
	if reg.ID == "" {
		reg.ID = reg.Name
	}
	a.mu.Lock()
	a.services[reg.ID] = &reg
	a.registers++
	a.notifyLocked()
	a.mu.Unlock()
}

func (a *Agent) deregister(w http.ResponseWriter, id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.services[id]; !ok {
		http.Error(w, "Unknown service ID "+id, http.StatusNotFound)
		return
	}
	delete(a.services, id)
	a.deregisters = append(a.deregisters, id)
	a.notifyLocked()
}

func (a *Agent) setMaintenance(w http.ResponseWriter, r *http.Request, id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.services[id]; !ok {
		http.Error(w, "Unknown service ID "+id, http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("enable") == "true" {
		a.maintenance[id] = r.URL.Query().Get("reason")
	} else {
		delete(a.maintenance, id)
	}
}

// getService implements hash-based blocking: when the client's hash matches
// the current one, wait for a change, the wait time or the client leaving.
func (a *Agent) getService(w http.ResponseWriter, r *http.Request, id string) {
	wait := 5 * time.Minute
	if d, err := time.ParseDuration(r.URL.Query().Get("wait")); err == nil {
		wait = d
	}
	clientHash := r.URL.Query().Get("hash")
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		a.mu.Lock()
		svc, ok := a.services[id]
		changed := a.changed
		var body []byte
		if ok {
			body, _ = json.Marshal(api.AgentService{ID: svc.ID, Service: svc.Name, Port: svc.Port, Address: svc.Address, Tags: svc.Tags, Meta: svc.Meta})
		}
		a.mu.Unlock()
		if !ok {
			http.Error(w, "unknown service ID: "+id, http.StatusNotFound)
			return
		}
		sum := sha256.Sum256(body)
		hash := hex.EncodeToString(sum[:8])
		if clientHash == "" || clientHash != hash {
			w.Header().Set("X-Consul-ContentHash", hash)
			writeJSON(w, string(body))
			return
		}
		select {
		case <-changed:
		case <-deadline.C:
			clientHash = "" // answer with the current state
		case <-r.Context().Done():
			return
		}
	}
}

func (a *Agent) updateTTL(w http.ResponseWriter, r *http.Request, checkID string) {
	var body struct{ Status, Output string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	a.mu.Lock()
	defer a.mu.Unlock()
	known := false
	for _, s := range a.services {
		if s.Check != nil && s.Check.CheckID == checkID && s.Check.TTL != "" {
			known = true
		}
	}
	if !known {
		http.Error(w, fmt.Sprintf("Unknown check ID %q", checkID), http.StatusNotFound)
		return
	}
	a.ttl = append(a.ttl, TTLUpdate{CheckID: checkID, Status: body.Status, Output: body.Output})
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func versionLess(v string, major, minor int) bool {
	var ma, mi int
	if _, err := fmt.Sscanf(v, "%d.%d", &ma, &mi); err != nil {
		return false
	}
	return ma < major || (ma == major && mi < minor)
}

// SetHealth replaces the health entries of a service and bumps the index,
// waking blocking queries.
func (a *Agent) SetHealth(service string, entries ...*api.ServiceEntry) {
	a.mu.Lock()
	a.health[service] = entries
	a.index++
	a.notifyLocked()
	a.mu.Unlock()
}

// BumpIndex increases the index without changing data, like an unrelated
// write in the cluster.
func (a *Agent) BumpIndex() {
	a.mu.Lock()
	a.index++
	a.notifyLocked()
	a.mu.Unlock()
}

// HealthQueries returns the query strings received on the health endpoint.
func (a *Agent) HealthQueries() []url.Values {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.queries)
}

// healthService implements GET /v1/health/service/:name with index-based
// blocking and the passing and tag filters.
func (a *Agent) healthService(w http.ResponseWriter, r *http.Request, name string) {
	q := r.URL.Query()
	a.mu.Lock()
	a.queries = append(a.queries, q)
	a.mu.Unlock()
	wait := 5 * time.Minute
	if d, err := time.ParseDuration(q.Get("wait")); err == nil {
		wait = d
	}
	want, _ := strconv.ParseUint(q.Get("index"), 10, 64)
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		a.mu.Lock()
		idx, changed := a.index, a.changed
		entries := a.health[name]
		a.mu.Unlock()
		if want == 0 || idx > want {
			out := make([]*api.ServiceEntry, 0, len(entries))
			for _, e := range entries {
				if q.Has("passing") && e.Checks.AggregatedStatus() != api.HealthPassing {
					continue
				}
				if !hasTags(e.Service.Tags, q["tag"]) {
					continue
				}
				out = append(out, e)
			}
			body, _ := json.Marshal(out)
			w.Header().Set("X-Consul-Index", strconv.FormatUint(idx, 10))
			writeJSON(w, string(body))
			return
		}
		select {
		case <-changed:
		case <-deadline.C:
			want = 0
		case <-r.Context().Done():
			return
		}
	}
}

func hasTags(have, want []string) bool {
	for _, t := range want {
		if !slices.Contains(have, t) {
			return false
		}
	}
	return true
}

// PutKV stores a key and bumps the index.
func (a *Agent) PutKV(key, value string) {
	a.mu.Lock()
	a.kv[key] = []byte(value)
	a.index++
	a.notifyLocked()
	a.mu.Unlock()
}

// DeleteKV removes a key and bumps the index.
func (a *Agent) DeleteKV(key string) {
	a.mu.Lock()
	delete(a.kv, key)
	a.index++
	a.notifyLocked()
	a.mu.Unlock()
}

// kvRead implements GET /v1/kv/:prefix with ?recurse or ?keys and
// index-based blocking. Like Consul, a prefix with no keys answers 404.
func (a *Agent) kvRead(w http.ResponseWriter, r *http.Request, prefix string) {
	q := r.URL.Query()
	wait := 5 * time.Minute
	if d, err := time.ParseDuration(q.Get("wait")); err == nil {
		wait = d
	}
	want, _ := strconv.ParseUint(q.Get("index"), 10, 64)
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		a.mu.Lock()
		idx, changed := a.index, a.changed
		var keys []string
		for k := range a.kv {
			if strings.HasPrefix(k, prefix) {
				keys = append(keys, k)
			}
		}
		slices.Sort(keys)
		pairs := make([]*api.KVPair, 0, len(keys))
		for _, k := range keys {
			pairs = append(pairs, &api.KVPair{Key: k, Value: slices.Clone(a.kv[k]), ModifyIndex: idx})
		}
		a.mu.Unlock()
		if want == 0 || idx > want {
			w.Header().Set("X-Consul-Index", strconv.FormatUint(idx, 10))
			if len(keys) == 0 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			var body []byte
			if q.Has("keys") {
				body, _ = json.Marshal(keys)
			} else {
				body, _ = json.Marshal(pairs)
			}
			writeJSON(w, string(body))
			return
		}
		select {
		case <-changed:
		case <-deadline.C:
			want = 0
		case <-r.Context().Done():
			return
		}
	}
}

// SetHanging makes every request block without answering, like a network
// partition that drops packets instead of resetting connections. Calling it
// with false releases the blocked requests.
func (a *Agent) SetHanging(hang bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case hang && a.hang == nil:
		a.hang = make(chan struct{})
	case !hang && a.hang != nil:
		close(a.hang)
		a.hang = nil
	}
}
