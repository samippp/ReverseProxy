// Package proxy handles all http contents
package proxy

import (
	"sync/atomic"

	"github.com/samippp/ReverseProxy/config"
)

// Balancer picks one backend from a pool of replicas for a given request.
type Balancer interface {
	Next() config.Backend
}

type RoundRobinBalancer struct {
	backends []config.Backend
	counter  uint64 // incremented atomically on every call to Next()
}

// NewRoundRobinBalancer builds a RoundRobinBalancer from a pool of backends.
// Panics if given an empty pool
func NewRoundRobinBalancer(backends []config.Backend) *RoundRobinBalancer {
	if len(backends) == 0 {
		panic("proxy: NewRoundRobinBalancer called with empty backend list")
	}
	return &RoundRobinBalancer{backends: backends}
}

// Next returns the next backend in rotation. Safe for concurrent use —
// multiple requests can call Next() at the same time since HTTP handlers
// run in separate goroutines.
func (b *RoundRobinBalancer) Next() config.Backend {
	n := atomic.AddUint64(&b.counter, 1)
	idx := (n - 1) % uint64(len(b.backends))
	return b.backends[idx]
}
