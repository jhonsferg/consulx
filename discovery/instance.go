package discovery

import (
	"cmp"
	"maps"
	"net"
	"slices"
	"strconv"

	"github.com/hashicorp/consul/api"
)

// Status is the aggregated health of an instance, as reported by Consul.
type Status string

const (
	StatusPassing     Status = api.HealthPassing
	StatusWarning     Status = api.HealthWarning
	StatusCritical    Status = api.HealthCritical
	StatusMaintenance Status = api.HealthMaint
)

// ServiceInstance is one healthy (or, with AnyStatus, any) instance of a
// service. Values are independent copies: modifying one never affects
// another or ConsulX's caches.
type ServiceInstance struct {
	ID   string
	Name string
	// Address is the service address, or the node address when the service
	// registered none (Consul's own fallback rule).
	Address string
	Port    int
	// Ports lists named ports of multi-port services (Consul >= 1.22).
	Ports []Port
	// Scheme is "https" when the instance published secure=true metadata
	// (ConsulX and Spring Cloud Consul both do), otherwise "http".
	Scheme          string
	Tags            []string
	Meta            map[string]string
	TaggedAddresses map[string]TaggedAddress
	Weights         Weights
	Datacenter      string
	Namespace       string
	Partition       string
	Node            Node
	Status          Status
	Checks          []Check
}

// Port is a named port.
type Port struct {
	Name    string
	Port    int
	Default bool
}

// TaggedAddress is an alternative address such as "lan" or "wan".
type TaggedAddress struct {
	Address string
	Port    int
}

// Weights are the instance weights while passing and while warning.
type Weights struct {
	Passing int
	Warning int
}

// Node describes the node hosting the instance.
type Node struct {
	ID              string
	Name            string
	Address         string
	Datacenter      string
	TaggedAddresses map[string]string
	Meta            map[string]string
}

// Check is one health check of the instance or its node.
type Check struct {
	ID        string
	Name      string
	Status    Status
	Output    string
	ServiceID string
	Type      string
}

// Endpoint is where an instance is reached. It holds only strings and
// numbers, so copies are independent without copying anything: the
// balancer and watches hand out endpoints computed once per state change,
// and returning one costs no allocation.
type Endpoint struct {
	// ID is the instance ID and Node the name of its node; together they
	// identify the instance.
	ID   string
	Node string
	// Address, Port and Scheme are the fields of the instance.
	Address string
	Port    int
	Scheme  string
	// HostPort is "address:port", with IPv6 addresses bracketed.
	HostPort string
	// URL is "scheme://address:port".
	URL string
}

// Endpoint returns the endpoint of the instance. It formats HostPort and
// URL on every call; Watch.Current and Balancer.NextEndpoint return
// endpoints formatted in advance.
func (i ServiceInstance) Endpoint() Endpoint {
	hp := i.HostPort()
	return Endpoint{
		ID: i.ID, Node: i.Node.Name, Address: i.Address, Port: i.Port, Scheme: i.Scheme,
		HostPort: hp, URL: i.Scheme + "://" + hp,
	}
}

// HostPort returns "address:port", bracketing IPv6 addresses.
func (i ServiceInstance) HostPort() string {
	return net.JoinHostPort(i.Address, strconv.Itoa(i.Port))
}

// URL returns "scheme://address:port".
func (i ServiceInstance) URL() string { return i.Scheme + "://" + i.HostPort() }

// PortNamed returns the named port of a multi-port instance.
func (i ServiceInstance) PortNamed(name string) (int, bool) {
	for _, p := range i.Ports {
		if p.Name == name {
			return p.Port, true
		}
	}
	return 0, false
}

// Weight returns the weight matching the instance status: Weights.Warning
// while warning, Weights.Passing otherwise. It is at least 1 for passing
// instances so an unset weight never excludes an instance.
func (i ServiceInstance) Weight() int {
	if i.Status == StatusWarning {
		return i.Weights.Warning
	}
	return max(i.Weights.Passing, 1)
}

// fromEntry converts a health entry into an instance.
//
// The entry comes straight out of the official client, which decodes every
// response into fresh objects and shares them with nobody, so its slices and
// maps are adopted instead of copied: the instance owns them from here, and
// the caller still receives an instance nobody else can observe.
func fromEntry(e *api.ServiceEntry) ServiceInstance {
	s, n := e.Service, e.Node
	inst := ServiceInstance{
		ID:         s.ID,
		Name:       s.Service,
		Address:    cmp.Or(s.Address, n.Address),
		Port:       s.Port,
		Scheme:     "http",
		Tags:       s.Tags,
		Meta:       s.Meta,
		Weights:    Weights{Passing: s.Weights.Passing, Warning: s.Weights.Warning},
		Datacenter: cmp.Or(s.Datacenter, n.Datacenter),
		Namespace:  s.Namespace,
		Partition:  s.Partition,
		Node: Node{
			ID: n.ID, Name: n.Node, Address: n.Address, Datacenter: n.Datacenter,
			TaggedAddresses: n.TaggedAddresses, Meta: n.Meta,
		},
		Status: Status(e.Checks.AggregatedStatus()),
	}
	if s.Meta["secure"] == "true" {
		inst.Scheme = "https"
	}
	if len(s.Ports) > 0 {
		inst.Ports = make([]Port, 0, len(s.Ports))
		for _, p := range s.Ports {
			inst.Ports = append(inst.Ports, Port{Name: p.Name, Port: p.Port, Default: p.Default})
		}
	}
	if inst.Port == 0 {
		for _, p := range s.Ports {
			if p.Default {
				inst.Port = p.Port
			}
		}
	}
	if len(s.TaggedAddresses) > 0 {
		inst.TaggedAddresses = make(map[string]TaggedAddress, len(s.TaggedAddresses))
		for k, v := range s.TaggedAddresses {
			inst.TaggedAddresses[k] = TaggedAddress{Address: v.Address, Port: v.Port}
		}
	}
	if len(e.Checks) > 0 {
		inst.Checks = make([]Check, 0, len(e.Checks))
		for _, c := range e.Checks {
			inst.Checks = append(inst.Checks, Check{
				ID: c.CheckID, Name: c.Name, Status: Status(c.Status), Output: c.Output,
				ServiceID: c.ServiceID, Type: c.Type,
			})
		}
	}
	return inst
}

// sortInstances orders instances by ID for stable snapshots and diffs. The
// comparison never builds a key string: it runs inside pdqsort on every
// fetch, where copying both keys per comparison is pure overhead.
func sortInstances(list []ServiceInstance) {
	slices.SortFunc(list, func(a, b ServiceInstance) int {
		if c := cmp.Compare(a.ID, b.ID); c != 0 {
			return c
		}
		return cmp.Compare(a.Node.Name, b.Node.Name)
	})
}

// equalInstance reports whether a and b hold the same values. It replaces
// reflect.DeepEqual on the watch path, which runs for every instance of
// every response: the typed comparison allocates nothing and treats nil
// and empty collections alike, so they never produce a spurious event.
// TestEqualInstanceCoversEveryField fails when a field is added to
// ServiceInstance or Node without being compared here.
func equalInstance(a, b ServiceInstance) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Address == b.Address && a.Port == b.Port &&
		a.Scheme == b.Scheme && a.Weights == b.Weights && a.Datacenter == b.Datacenter &&
		a.Namespace == b.Namespace && a.Partition == b.Partition && a.Status == b.Status &&
		a.Node.ID == b.Node.ID && a.Node.Name == b.Node.Name && a.Node.Address == b.Node.Address &&
		a.Node.Datacenter == b.Node.Datacenter &&
		slices.Equal(a.Ports, b.Ports) && slices.Equal(a.Tags, b.Tags) && slices.Equal(a.Checks, b.Checks) &&
		maps.Equal(a.Meta, b.Meta) && maps.Equal(a.TaggedAddresses, b.TaggedAddresses) &&
		maps.Equal(a.Node.TaggedAddresses, b.Node.TaggedAddresses) && maps.Equal(a.Node.Meta, b.Node.Meta)
}

// clone returns a deep copy, so callers can never mutate shared snapshots.
func (i ServiceInstance) clone() ServiceInstance {
	c := i
	c.Tags = slices.Clone(i.Tags)
	c.Meta = maps.Clone(i.Meta)
	c.Ports = slices.Clone(i.Ports)
	c.TaggedAddresses = maps.Clone(i.TaggedAddresses)
	c.Node.TaggedAddresses = maps.Clone(i.Node.TaggedAddresses)
	c.Node.Meta = maps.Clone(i.Node.Meta)
	c.Checks = slices.Clone(i.Checks)
	return c
}

func cloneAll(list []ServiceInstance) []ServiceInstance {
	if list == nil {
		return nil
	}
	out := make([]ServiceInstance, len(list))
	for k, i := range list {
		out[k] = i.clone()
	}
	return out
}
