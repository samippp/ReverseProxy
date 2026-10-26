// Package proxy handles all http contents
package proxy

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samippp/ReverseProxy/config"
)

type Balancer interface {
	Next() config.Backend
	// ReportFailure records a failed request to the backend at target,
	// so a dead backend can leave rotation before its next health check.
	ReportFailure(target *url.URL)
}

// A backend leaves rotation after fallThreshold consecutive failures
// (health checks or proxied requests) and only returns after
// riseThreshold consecutive passing health checks, so a flaky backend
// doesn't flip in and out of rotation on every check.
const (
	fallThreshold = 3
	riseThreshold = 2
)

type backendState struct {
	healthy bool
	fails   int
	passes  int
}

type RoundRobinBalancer struct {
	backends []config.Backend
	keys     []string // backendKey of each backend, same order
	counter  atomic.Uint64

	// healthy is an immutable snapshot of the backends in rotation,
	// replaced whenever one changes state, so Next never takes a lock
	healthy atomic.Pointer[[]config.Backend]

	mu     sync.Mutex // guards states; never taken by Next
	states map[string]*backendState
}

func NewRoundRobinBalancer(backends []config.Backend) *RoundRobinBalancer {
	if len(backends) == 0 {
		panic("proxy: NewRoundRobinBalancer called with empty backend list")
	}
	b := &RoundRobinBalancer{
		backends: backends,
		keys:     make([]string, len(backends)),
		states:   make(map[string]*backendState, len(backends)),
	}
	for i, backend := range backends {
		u, err := url.Parse(backend.URL)
		if err != nil {
			panic(fmt.Sprintf("proxy: invalid backend URL %q: %v", backend.URL, err))
		}
		b.keys[i] = backendKey(u)
		b.states[b.keys[i]] = &backendState{healthy: true}
	}
	b.publish()
	return b
}

// backendKey identifies a backend by scheme and host, which is what an
// outbound request's URL still carries after the path is rewritten.
func backendKey(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}

func (b *RoundRobinBalancer) Next() config.Backend {
	list := *b.healthy.Load()
	if len(list) == 0 {
		// every backend is down: fail open rather than refuse outright
		list = b.backends
	}
	return list[b.counter.Add(1)%uint64(len(list))]
}

func (b *RoundRobinBalancer) ReportFailure(target *url.URL) {
	b.record(backendKey(target), false)
}

// record applies one check or request result to a backend, moving it
// in or out of rotation once it crosses the fall/rise threshold.
func (b *RoundRobinBalancer) record(key string, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, found := b.states[key]
	if !found {
		return
	}
	if ok {
		s.fails = 0
		s.passes++
		if !s.healthy && s.passes >= riseThreshold {
			s.healthy = true
			log.Printf("backend=%s back in rotation", key)
			b.publish()
		}
		return
	}
	s.passes = 0
	s.fails++
	if s.healthy && s.fails >= fallThreshold {
		s.healthy = false
		log.Printf("backend=%s removed from rotation after %d consecutive failures", key, s.fails)
		b.publish()
	}
}

// publish swaps in a fresh snapshot of healthy backends. Callers other
// than the constructor must hold b.mu.
func (b *RoundRobinBalancer) publish() {
	healthy := make([]config.Backend, 0, len(b.backends))
	for i, backend := range b.backends {
		if b.states[b.keys[i]].healthy {
			healthy = append(healthy, backend)
		}
	}
	b.healthy.Store(&healthy)
}

// StartHealthChecks launches a background goroutine that periodically
// pings each backend's health endpoint and updates the balancer's
// health status accordingly. It runs until the process exits — for
// this project, there's no need to stop it early.
func (b *RoundRobinBalancer) StartHealthChecks(interval time.Duration) {
	go func() {
		client := &http.Client{Timeout: 2 * time.Second}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			for i, backend := range b.backends {
				go b.checkOne(client, backend.URL, b.keys[i])
			}
		}
	}()
}

func (b *RoundRobinBalancer) checkOne(client *http.Client, backendURL, key string) {
	resp, err := client.Get(backendURL + "/health")
	healthy := err == nil && resp.StatusCode == http.StatusOK
	if resp != nil {
		resp.Body.Close()
	}
	b.record(key, healthy)
}
