package health

import (
	"context"
	"encoding/json"
	"net/http"
)

// ProbeFunc produces a report for one endpoint, for example Registry.Ready.
type ProbeFunc func(ctx context.Context) Report

// HandlerOptions configures Handler.
type HandlerOptions struct {
	// HideDetails omits components from responses. Use it when health
	// endpoints are reachable by untrusted clients.
	HideDetails bool
	// DegradedStatusCode is the HTTP status for DEGRADED. The default, 429,
	// is what Consul's HTTP check maps to "warning". Set it to 200 when the
	// endpoint is also used by Kubernetes probes, which treat 429 as failure.
	DegradedStatusCode int
}

// StatusCode returns the HTTP status code for s: 200 for UP, degraded (or
// 429 when zero) for DEGRADED and 503 for DOWN. Consul's HTTP check reports
// 2xx as passing, 429 as warning and anything else as critical.
func StatusCode(s Status, degraded int) int {
	switch s {
	case StatusUp:
		return http.StatusOK
	case StatusDegraded:
		if degraded == 0 {
			return http.StatusTooManyRequests
		}
		return degraded
	default:
		return http.StatusServiceUnavailable
	}
}

// Handler serves the report of probe as JSON. It answers GET and HEAD;
// other methods get 405. Responses are never cached.
func Handler(probe ProbeFunc, opts HandlerOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		rep := probe(r.Context())
		if opts.HideDetails {
			rep.Components = nil
		}
		h := w.Header()
		h["Content-Type"] = jsonContentType
		h["Cache-Control"] = noStore
		w.WriteHeader(StatusCode(rep.Status, opts.DegradedStatusCode))
		if r.Method == http.MethodHead {
			return
		}
		// Encoding errors mean the client went away or a detail value is not
		// serialisable; the status line has been sent either way.
		_ = json.NewEncoder(w).Encode(rep)
	})
}

// Header values shared by every response. Assigning a prebuilt slice saves
// the allocation Header.Set makes per value; each slice has length equal to
// its capacity, so a later Header.Add copies it instead of writing into it.
var (
	jsonContentType = []string{"application/json"}
	noStore         = []string{"no-store"}
)
