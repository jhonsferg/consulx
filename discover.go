package consulx

import (
	"sync"

	"github.com/jhonsferg/consulx/balancer"
	"github.com/jhonsferg/consulx/discovery"
)

// services holds the discovery client and the balancers of a Client.
type services struct {
	once sync.Once
	d    *discovery.Client

	mu        sync.Mutex
	balancers []*balancer.Balancer
}
