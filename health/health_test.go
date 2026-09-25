package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func up(context.Context) Result   { return Result{Status: StatusUp} }
func down(context.Context) Result { return Result{Status: StatusDown, Error: "db unreachable"} }

func TestEmptyRegistryIsUp(t *testing.T) {
	r := NewRegistry(0)
	for name, rep := range map[string]Report{"ready": r.Ready(t.Context()), "live": r.Live(t.Context()), "health": r.Health(t.Context())} {
		if rep.Status != StatusUp {
			t.Errorf("%s: %s", name, rep.Status)
		}
	}
}

func TestAggregationTakesWorstStatus(t *testing.T) {
	r := NewRegistry(0)
	r.Register("a", CheckerFunc(up))
	r.Set("b", Result{Status: StatusDegraded})
	if got := r.Ready(t.Context()).Status; got != StatusDegraded {
		t.Fatalf("got %s", got)
	}
	r.Register("c", CheckerFunc(down))
	rep := r.Ready(t.Context())
	if rep.Status != StatusDown || rep.Components["c"].Error != "db unreachable" || len(rep.Components) != 3 {
		t.Fatalf("report %+v", rep)
	}
}

func TestScopes(t *testing.T) {
	r := NewRegistry(0)
	r.Register("db", CheckerFunc(down))                      // readiness only
	r.Register("deadlock", CheckerFunc(up), Liveness)        // liveness only
	r.Register("both", CheckerFunc(up), Readiness, Liveness) // both
	if got := r.Live(t.Context()); got.Status != StatusUp || len(got.Components) != 2 {
		t.Fatalf("liveness must ignore readiness components: %+v", got)
	}
	if got := r.Ready(t.Context()); got.Status != StatusDown || len(got.Components) != 2 {
		t.Fatalf("readiness %+v", got)
	}
	if got := r.Health(t.Context()); len(got.Components) != 3 {
		t.Fatalf("health must include all: %+v", got)
	}
}

func TestSlowCheckerTimesOut(t *testing.T) {
	r := NewRegistry(20 * time.Millisecond)
	release := make(chan struct{})
	defer close(release)
	r.Register("slow", CheckerFunc(func(ctx context.Context) Result {
		select {
		case <-ctx.Done():
		case <-release:
		}
		return Result{Status: StatusUp}
	}))
	start := time.Now()
	rep := r.Ready(t.Context())
	if rep.Status != StatusDown || rep.Components["slow"].Error != "health check timed out" {
		t.Fatalf("report %+v", rep)
	}
	if time.Since(start) > time.Second {
		t.Fatal("probe waited for the slow checker")
	}
}

func TestPanickingCheckerIsDown(t *testing.T) {
	r := NewRegistry(0)
	r.Register("p", CheckerFunc(func(context.Context) Result { panic("boom") }))
	if rep := r.Ready(t.Context()); rep.Status != StatusDown {
		t.Fatalf("report %+v", rep)
	}
}

func TestEmptyStatusCountsAsDown(t *testing.T) {
	r := NewRegistry(0)
	r.Set("x", Result{})
	if rep := r.Ready(t.Context()); rep.Status != StatusDown {
		t.Fatalf("report %+v", rep)
	}
}

func TestDrainingAffectsReadinessOnly(t *testing.T) {
	r := NewRegistry(0)
	r.SetDraining(true)
	if r.Ready(t.Context()).Status != StatusDown || r.Health(t.Context()).Status != StatusDown {
		t.Fatal("draining must make readiness and health DOWN")
	}
	if r.Live(t.Context()).Status != StatusUp {
		t.Fatal("draining must not affect liveness")
	}
	r.SetDraining(false)
	if r.Ready(t.Context()).Status != StatusUp {
		t.Fatal("draining must be reversible")
	}
}

func TestSetKeepsScopesAndNotifies(t *testing.T) {
	r := NewRegistry(0)
	var got []Status
	var mu sync.Mutex
	r.OnPush(func(s Status) { mu.Lock(); got = append(got, s); mu.Unlock() })
	r.Set("broker", Result{Status: StatusUp}, Liveness)
	r.Set("broker", Result{Status: StatusDown})
	if rep := r.Live(t.Context()); rep.Status != StatusDown {
		t.Fatalf("scope must be kept on update: %+v", rep)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[1] != StatusDown {
		t.Fatalf("notifications %v", got)
	}
}

func TestSetCopiesDetails(t *testing.T) {
	r := NewRegistry(0)
	d := map[string]any{"lag": 1}
	r.Set("c", Result{Status: StatusUp, Details: d})
	d["lag"] = 99
	if got := r.Ready(t.Context()).Components["c"].Details["lag"]; got != 1 {
		t.Fatalf("details aliased: %v", got)
	}
}

func TestRemove(t *testing.T) {
	r := NewRegistry(0)
	r.Register("x", CheckerFunc(down))
	r.Remove("x")
	if r.Ready(t.Context()).Status != StatusUp {
		t.Fatal("removed component still reported")
	}
}

func TestConcurrentUse(t *testing.T) {
	r := NewRegistry(0)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			r.Set("c", Result{Status: StatusUp})
			r.Register("k", CheckerFunc(up))
			_ = r.Ready(t.Context())
			r.SetDraining(i%2 == 0)
		})
	}
	wg.Wait()
}

func TestStatusCode(t *testing.T) {
	cases := []struct {
		s        Status
		degraded int
		want     int
	}{
		{StatusUp, 0, 200},
		{StatusDegraded, 0, 429},
		{StatusDegraded, 200, 200},
		{StatusDown, 0, 503},
		{"weird", 0, 503},
	}
	for _, c := range cases {
		if got := StatusCode(c.s, c.degraded); got != c.want {
			t.Errorf("%s/%d: got %d want %d", c.s, c.degraded, got, c.want)
		}
	}
}

func TestHandler(t *testing.T) {
	r := NewRegistry(0)
	r.Register("db", CheckerFunc(down))
	h := Handler(r.Ready, HandlerOptions{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/json" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %v", rec.Header())
	}
	var rep Report
	if err := json.NewDecoder(rec.Body).Decode(&rep); err != nil {
		t.Fatal(err)
	}
	if rep.Components["db"].Status != StatusDown {
		t.Fatalf("body %+v", rep)
	}
}

func TestHandlerHideDetailsAndMethods(t *testing.T) {
	r := NewRegistry(0)
	r.Set("secret-ish", Result{Status: StatusDegraded, Details: map[string]any{"host": "db-1"}})
	h := Handler(r.Health, HandlerOptions{HideDetails: true, DegradedStatusCode: 200})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 || rec.Body.String() != "{\"status\":\"DEGRADED\"}\n" {
		t.Fatalf("code %d body %q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/", nil))
	if rec.Code != 200 || rec.Body.Len() != 0 {
		t.Fatalf("HEAD: code %d body %q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST: code %d", rec.Code)
	}
}
