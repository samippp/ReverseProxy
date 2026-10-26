package proxy

import (
	"net/url"
	"testing"

	"github.com/samippp/ReverseProxy/config"
)

func newTestBalancer() *RoundRobinBalancer {
	return NewRoundRobinBalancer([]config.Backend{
		{Name: "a", URL: "http://a.test:1"},
		{Name: "b", URL: "http://b.test:1"},
	})
}

func inRotation(b *RoundRobinBalancer, backendURL string) bool {
	for _, backend := range *b.healthy.Load() {
		if backend.URL == backendURL {
			return true
		}
	}
	return false
}

func TestBalancer_FallAndRiseThresholds(t *testing.T) {
	b := newTestBalancer()
	key, backendURL := b.keys[0], b.backends[0].URL

	for i := 1; i < fallThreshold; i++ {
		b.record(key, false)
		if !inRotation(b, backendURL) {
			t.Fatalf("removed after %d failures, want %d", i, fallThreshold)
		}
	}
	b.record(key, false)
	if inRotation(b, backendURL) {
		t.Fatalf("still in rotation after %d consecutive failures", fallThreshold)
	}

	for i := 1; i < riseThreshold; i++ {
		b.record(key, true)
		if inRotation(b, backendURL) {
			t.Fatalf("back after %d passes, want %d", i, riseThreshold)
		}
	}
	b.record(key, true)
	if !inRotation(b, backendURL) {
		t.Fatalf("not back after %d consecutive passes", riseThreshold)
	}
}

func TestBalancer_AlternatingResultsDontFlipRotation(t *testing.T) {
	b := newTestBalancer()
	key, backendURL := b.keys[0], b.backends[0].URL

	for range 10 {
		b.record(key, false)
		b.record(key, true)
		if !inRotation(b, backendURL) {
			t.Fatal("flaky backend removed without consecutive failures")
		}
	}
}

func TestBalancer_ReportFailureRemovesBackend(t *testing.T) {
	b := newTestBalancer()
	target, _ := url.Parse(b.backends[0].URL + "/secret")

	for range fallThreshold {
		b.ReportFailure(target)
	}
	for range 10 {
		if got := b.Next(); got.URL == b.backends[0].URL {
			t.Fatalf("Next returned %s after it failed %d requests", got.URL, fallThreshold)
		}
	}
}

func TestBalancer_AllDownFailsOpen(t *testing.T) {
	b := newTestBalancer()
	for _, key := range b.keys {
		for range fallThreshold {
			b.record(key, false)
		}
	}
	seen := map[string]bool{}
	for range 4 {
		seen[b.Next().URL] = true
	}
	if len(seen) != 2 {
		t.Errorf("expected Next to rotate over all backends when all are down, got %v", seen)
	}
}
