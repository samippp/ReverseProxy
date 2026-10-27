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

// Balancer picks backends and tracks their health.
type Balancer interface {
	// Next returns the backend for the next request.
	Next() config.Backend
	// ReportFailure counts a failed request against target's backend.
	ReportFailure(target *url.URL)
}

// Consecutive failures to leave rotation, and passing checks to return.
const (
	fallThreshold = 3
	riseThreshold = 2
)

// A removed backend stays out for an ejection period even if /health
// passes, since it can pass while real requests hang. The period doubles
// when it's removed again within stableAfter of returning.
const (
	baseEjection = 10 * time.Second
	maxEjection  = 5 * time.Minute
	stableAfter  = time.Minute
)

// backendState is one backend's health; guarded by RoundRobinBalancer.mu.
type backendState struct {
	healthy bool
	fails   int
	passes  int

	ejections    int // removals without a stable stretch in between
	ejectedUntil time.Time
	returnedAt   time.Time
}

// RoundRobinBalancer rotates over healthy backends.
type RoundRobinBalancer struct {
	backends []config.Backend
	keys     []string // backendKey of each backend, same order
	counter  atomic.Uint64

	// healthy is an immutable snapshot, swapped on change; Next never locks
	healthy atomic.Pointer[[]config.Backend]

	mu     sync.Mutex // guards states; never taken by Next
	states map[string]*backendState
	now    func() time.Time // replaced in tests
}

// NewRoundRobinBalancer starts with all backends healthy. It panics on an
// empty list or a bad URL.
func NewRoundRobinBalancer(backends []config.Backend) *RoundRobinBalancer {
	if len(backends) == 0 {
		panic("proxy: NewRoundRobinBalancer called with empty backend list")
	}
	b := &RoundRobinBalancer{
		backends: backends,
		keys:     make([]string, len(backends)),
		states:   make(map[string]*backendState, len(backends)),
		now:      time.Now,
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

// backendKey identifies a backend by scheme and host, which outbound
// request URLs keep after path rewriting.
func backendKey(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}

// Next returns the next healthy backend, or any backend if none are.
func (b *RoundRobinBalancer) Next() config.Backend {
	list := *b.healthy.Load()
	if len(list) == 0 {
		// all down: fail open
		list = b.backends
	}
	return list[b.counter.Add(1)%uint64(len(list))]
}

// ReportFailure counts a failed request against target's backend.
func (b *RoundRobinBalancer) ReportFailure(target *url.URL) {
	b.record(backendKey(target), false)
}

// record applies one result to a backend, moving it in or out of rotation
// at the fall/rise thresholds once any ejection period is over.
func (b *RoundRobinBalancer) record(key string, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, found := b.states[key]
	if !found {
		return
	}
	now := b.now()
	if ok {
		s.fails = 0
		s.passes++
		if !s.healthy && s.passes >= riseThreshold && !now.Before(s.ejectedUntil) {
			s.healthy = true
			s.returnedAt = now
			log.Printf("backend=%s back in rotation", key)
			b.publish()
		}
		return
	}
	s.passes = 0
	s.fails++
	if s.healthy && s.fails >= fallThreshold {
		s.healthy = false
		if s.ejections > 0 && now.Sub(s.returnedAt) < stableAfter {
			s.ejections++
		} else {
			s.ejections = 1
		}
		ejection := ejectionPeriod(s.ejections)
		s.ejectedUntil = now.Add(ejection)
		log.Printf("backend=%s removed from rotation for at least %v after %d consecutive failures",
			key, ejection, s.fails)
		b.publish()
	}
}

// ejectionPeriod doubles baseEjection per repeat removal, up to maxEjection.
func ejectionPeriod(ejections int) time.Duration {
	d := baseEjection
	for i := 1; i < ejections && d < maxEjection; i++ {
		d *= 2
	}
	return min(d, maxEjection)
}

// publish stores a new healthy snapshot. Hold b.mu, except in the constructor.
func (b *RoundRobinBalancer) publish() {
	healthy := make([]config.Backend, 0, len(b.backends))
	for i, backend := range b.backends {
		if b.states[b.keys[i]].healthy {
			healthy = append(healthy, backend)
		}
	}
	b.healthy.Store(&healthy)
}

// StartHealthChecks GETs every backend's /health each interval, forever.
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

// checkOne records whether one backend's /health returned 200.
func (b *RoundRobinBalancer) checkOne(client *http.Client, backendURL, key string) {
	resp, err := client.Get(backendURL + "/health")
	healthy := err == nil && resp.StatusCode == http.StatusOK
	if resp != nil {
		resp.Body.Close()
	}
	b.record(key, healthy)
}
