// Package compat detects the Consul agent version and edition and decides
// whether a feature can be used against it.
//
// Consul exposes one HTTP API (/v1) whose request bodies are decoded
// strictly: an agent rejects a field it does not know with HTTP 400. The
// official Go client often declares fields before agents support them, so
// ConsulX checks each optional feature here before sending it. Requirements
// are recorded from release notes and verified against live agents; see
// docs/compatibility.md.
package compat

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/consul/api"
)

// Version is a parsed Consul agent version.
type Version struct {
	Major, Minor, Patch int
	// Pre is the pre-release label, e.g. "rc1". Empty for releases.
	Pre string
	// Enterprise is true for "+ent" builds.
	Enterprise bool
	// Raw is the string reported by the agent.
	Raw string
}

// Unknown is the zero Version, used when the agent version cannot be read
// (for example when the ACL token lacks agent:read). Feature checks against
// Unknown are permissive: the agent remains the final authority.
var Unknown = Version{}

// IsUnknown reports whether v was not detected.
func (v Version) IsUnknown() bool { return v.Raw == "" }

// String returns the raw version, or "unknown".
func (v Version) String() string {
	if v.IsUnknown() {
		return "unknown"
	}
	return v.Raw
}

// AtLeast reports whether v >= major.minor.patch, ignoring pre-release
// labels (a release candidate counts as its release).
func (v Version) AtLeast(major, minor, patch int) bool {
	if v.Major != major {
		return v.Major > major
	}
	if v.Minor != minor {
		return v.Minor > minor
	}
	return v.Patch >= patch
}

// Parse parses versions such as "1.22.7", "2.0.4+ent", "1.21.0-rc1" and
// "v1.22.7+ent.fips1402".
func Parse(s string) (Version, error) {
	raw := strings.TrimSpace(s)
	core := strings.TrimPrefix(raw, "v")
	var build string
	if i := strings.IndexByte(core, '+'); i >= 0 {
		core, build = core[:i], core[i+1:]
	}
	var pre string
	if i := strings.IndexByte(core, '-'); i >= 0 {
		core, pre = core[:i], core[i+1:]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("compat: malformed version %q", s)
	}
	var nums [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("compat: malformed version %q", s)
		}
		nums[i] = n
	}
	return Version{
		Major: nums[0], Minor: nums[1], Patch: nums[2],
		Pre:        pre,
		Enterprise: build == "ent" || strings.HasPrefix(build, "ent."),
		Raw:        raw,
	}, nil
}

// Feature is an optional capability gated by version or edition.
type Feature int

const (
	// MultiPort is the Ports field of a service definition.
	MultiPort Feature = iota + 1
	// IPv6Address is an IPv6 service or tagged address.
	IPv6Address
	// Namespaces is any namespace-scoped request.
	Namespaces
	// Partitions is any admin-partition-scoped request.
	Partitions
	// AIService is the AI block of a service definition. The official client
	// declares it, but no verified agent accepts it yet (2.0.4 rejects it).
	AIService
)

type requirement struct {
	name       string
	major      int
	minor      int
	patch      int
	enterprise bool
	// never marks a feature no verified agent version supports.
	never bool
}

var requirements = map[Feature]requirement{
	MultiPort:   {name: "multi-port services", major: 1, minor: 22},
	IPv6Address: {name: "IPv6 service addresses", major: 1, minor: 22},
	Namespaces:  {name: "namespaces", enterprise: true},
	Partitions:  {name: "admin partitions", enterprise: true},
	AIService:   {name: "AI service definitions", never: true},
}

// Name returns the human readable feature name.
func (f Feature) Name() string { return requirements[f].name }

// Requirement returns a description such as "Consul >= 1.22.0".
func (f Feature) Requirement() string {
	r := requirements[f]
	switch {
	case r.never:
		return "a Consul version not yet verified by ConsulX"
	case r.enterprise:
		return "Consul Enterprise"
	default:
		return fmt.Sprintf("Consul >= %d.%d.%d", r.major, r.minor, r.patch)
	}
}

// Supports reports whether v can handle f. An unknown version is assumed to
// support every feature except those no verified version supports.
func (v Version) Supports(f Feature) bool {
	r, ok := requirements[f]
	if !ok {
		return false
	}
	if r.never {
		return false
	}
	if v.IsUnknown() {
		return true
	}
	if r.enterprise {
		return v.Enterprise
	}
	return v.AtLeast(r.major, r.minor, r.patch)
}

// agentSelf is the subset of GET /v1/agent/self that ConsulX reads.
type agentSelf struct {
	Config struct {
		Version    string
		Datacenter string
		NodeName   string
	}
}

// AgentInfo describes the connected agent.
type AgentInfo struct {
	Version    Version
	Datacenter string
	NodeName   string
}

// Detect reads the agent version with GET /v1/agent/self. It uses the Raw
// client because Agent().Self() does not accept a context.
func Detect(ctx context.Context, c *api.Client) (AgentInfo, error) {
	var self agentSelf
	q := (&api.QueryOptions{}).WithContext(ctx)
	if _, err := c.Raw().Query("/v1/agent/self", &self, q); err != nil {
		return AgentInfo{}, err
	}
	v, err := Parse(self.Config.Version)
	if err != nil {
		return AgentInfo{}, err
	}
	return AgentInfo{Version: v, Datacenter: self.Config.Datacenter, NodeName: self.Config.NodeName}, nil
}
