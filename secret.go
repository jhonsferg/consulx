package consulx

import (
	"fmt"
	"log/slog"
)

const redacted = "[REDACTED]"

// Secret holds a sensitive value such as an ACL token. Its String, GoString
// and slog representations are redacted, so a Secret printed with fmt or
// logged with slog never reveals its content. Use Reveal to obtain the value.
type Secret string

// Reveal returns the underlying value. Call it only where the value is sent
// to Consul, never for logging.
func (s Secret) Reveal() string { return string(s) }

// String returns a redacted placeholder, or "" for an empty Secret.
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return redacted
}

// GoString keeps %#v redacted.
func (s Secret) GoString() string { return fmt.Sprintf("consulx.Secret(%q)", s.String()) }

// Format keeps every fmt verb, including %s, %v, %q and %x, redacted.
func (s Secret) Format(f fmt.State, verb rune) {
	switch verb {
	case 'q':
		fmt.Fprintf(f, "%q", s.String())
	case 'v':
		if f.Flag('#') {
			fmt.Fprint(f, s.GoString())
			return
		}
		fmt.Fprint(f, s.String())
	default:
		fmt.Fprint(f, s.String())
	}
}

// LogValue implements slog.LogValuer.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// MarshalText keeps JSON and YAML encodings of a configuration redacted.
// Encoding a Config is therefore lossy by design: secrets do not round-trip.
func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText stores the decoded text as is.
func (s *Secret) UnmarshalText(b []byte) error {
	*s = Secret(b)
	return nil
}
