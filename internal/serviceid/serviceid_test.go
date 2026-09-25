package serviceid

import (
	"regexp"
	"strings"
	"testing"
)

var safe = regexp.MustCompile(`^[a-z0-9._-]*$`)

func TestSanitize(t *testing.T) {
	tests := map[string]string{
		"Orders-API":           "orders-api",
		"my service/v1":        "my-service-v1",
		"--a--b--":             "a-b",
		"host.example.com":     "host.example.com",
		"ÜNÍCODE name":         "n-code-name",
		"":                     "",
		"a__b":                 "a__b",
		"pod-7d9f8b6c5d-x2x9z": "pod-7d9f8b6c5d-x2x9z",
	}
	for in, want := range tests {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzSanitize(f *testing.F) {
	for _, s := range []string{"Orders API", "a/b", "---", "ü"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := Sanitize(s)
		if !safe.MatchString(out) || len(out) > maxLen || strings.HasPrefix(out, "-") || strings.HasSuffix(out, "-") || strings.Contains(out, "--") {
			t.Fatalf("Sanitize(%q) = %q", s, out)
		}
		if Sanitize(out) != out {
			t.Fatalf("Sanitize is not idempotent for %q", s)
		}
	})
}

func TestHostnamePort(t *testing.T) {
	if got := HostnamePort("orders-api", "Pod-1.cluster.local", 8080); got != "orders-api-pod-1.cluster.local-8080" {
		t.Fatalf("got %q", got)
	}
	a, b := HostnamePort("x", "", 80), HostnamePort("x", "", 80)
	if a == b || !strings.HasPrefix(a, "x-") || !strings.HasSuffix(a, "-80") {
		t.Fatalf("empty host name must yield unique IDs: %q %q", a, b)
	}
}

func TestRandom(t *testing.T) {
	a, b := Random("Orders"), Random("Orders")
	re := regexp.MustCompile(`^orders-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if a == b || !re.MatchString(a) {
		t.Fatalf("%q %q", a, b)
	}
}

func TestLongNamesAreTruncated(t *testing.T) {
	got := HostnamePort(strings.Repeat("a", 200), "host", 8080)
	if len(got) > maxLen || !strings.HasSuffix(got, "-8080") {
		t.Fatalf("truncation must keep the unique suffix: %q (len %d)", got, len(got))
	}
	if got := Random(strings.Repeat("b", 300)); len(got) > maxLen {
		t.Fatalf("len %d", len(got))
	}
}
