// Package proxy handles all http contents
package proxy

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samippp/ReverseProxy/config"
)

type Balancer interface {
	Next() config.Backend
}

type backendHealth struct {
	backend config.Backend
	healthy bool
}

type RoundRobinBalancer struct {
	mu       sync.RWMutex
	backends []*backendHealth
	counter  atomic.Uint64
}

func NewRoundRobinBalancer(backends []config.Backend) *RoundRobinBalancer {
	if len(backends) == 0 {
		panic("proxy: NewRoundRobinBalancer called with empty backend list")
	}
	wrapped := make([]*backendHealth, len(backends))
	for i, b := range backends {
		wrapped[i] = &backendHealth{backend: b, healthy: true}
	}
	return &RoundRobinBalancer{backends: wrapped}
}

func (b *RoundRobinBalancer) Next() config.Backend {
	b.mu.RLock()
	defer b.mu.RUnlock()

	n := len(b.backends)
	for i := 0; i < n; i++ {
		idx := b.counter.Add(1) % uint64(n)
		if b.backends[idx].healthy {
			return b.backends[idx].backend
		}
	}
	idx := b.counter.Add(1) % uint64(n)
	return b.backends[idx].backend
}

func (b *RoundRobinBalancer) setHealthy(url string, healthy bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, bh := range b.backends {
		if bh.backend.URL == url {
			bh.healthy = healthy
			return
		}
	}
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
			b.checkAll(client)
		}
	}()
}

func (b *RoundRobinBalancer) checkAll(client *http.Client) {
	b.mu.RLock()
	// copy backend to remain consistent if backend were to dynamically change
	backends := make([]config.Backend, len(b.backends))
	for i, bh := range b.backends {
		backends[i] = bh.backend
	}
	b.mu.RUnlock()

	for _, backend := range backends {
		go b.checkOne(client, backend)
	}
}

func (b *RoundRobinBalancer) checkOne(client *http.Client, backend config.Backend) {
	resp, err := client.Get(backend.URL + "/health")
	healthy := err == nil && resp.StatusCode == http.StatusOK
	if resp != nil {
		resp.Body.Close()
	}
	b.setHealthy(backend.URL, healthy)
}
