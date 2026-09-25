// Package serviceid generates Consul service instance IDs.
package serviceid

import (
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"
)

// maxLen keeps IDs readable and well below any Consul or DNS limit.
const maxLen = 128

// Sanitize lower-cases s and replaces every character outside
// [a-z0-9._-] with '-', collapsing repeats and trimming '-' at both ends.
// The result is safe in URLs (IDs appear in /v1/agent/service/:id) and in
// DNS labels once dots are handled by the caller.
func Sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	dash := false
	for _, r := range strings.ToLower(s) {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if !ok || r == '-' {
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = true
			continue
		}
		b.WriteRune(r)
		dash = false
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > maxLen {
		out = strings.TrimRight(out[:maxLen], "-")
	}
	return out
}

// HostnamePort returns "<name>-<hostname>-<port>", sanitised. It is unique
// per instance (a host cannot bind one port twice; containers and pods have
// unique host names) and stable across restarts, so a restarted process
// replaces its own registration. When hostname is empty a random suffix is
// used instead, keeping IDs unique.
func HostnamePort(name, hostname string, port int) string {
	host := Sanitize(hostname)
	if host == "" {
		host = randomSuffix()
	}
	return withSuffix(Sanitize(name+"-"+host), strconv.Itoa(port))
}

// Random returns "<name>-<uuid>", unique on every call.
func Random(name string) string {
	return withSuffix(Sanitize(name), uuid())
}

// withSuffix joins base and suffix, truncating base (never the suffix,
// which carries the uniqueness) to respect maxLen.
func withSuffix(base, suffix string) string {
	if room := maxLen - len(suffix) - 1; len(base) > room {
		base = strings.TrimRight(base[:room], "-")
	}
	if base == "" {
		return suffix
	}
	return base + "-" + suffix
}

// uuid returns a random RFC 4122 version 4 UUID.
func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go >= 1.24)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func randomSuffix() string { return uuid()[:8] }
