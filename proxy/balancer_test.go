package proxy

import (
	"net/url"
	"testing"
	"time"

	"github.com/samippp/ReverseProxy/config"
)

// fakeClock lets tests move past ejection periods without sleeping.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestBalancer() *RoundRobinBalancer {
	b, _ := newClockedTestBalancer()
	return b
}

func newClockedTestBalancer() (*RoundRobinBalancer, *fakeClock) {
	b := NewRoundRobinBalancer([]config.Backend{
		{Name: "a", URL: "http://a.test:1"},
		{Name: "b", URL: "http://b.test:1"},
	})
	clock := &fakeClock{t: time.Unix(0, 0)}
	b.now = clock.now
	return b, clock
}

func eject(b *RoundRobinBalancer, key string) {
	for range fallThreshold {
		b.record(key, false)
	}
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
	b, clock := newClockedTestBalancer()
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

	clock.advance(baseEjection)
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

func TestBalancer_StaysOutForEjectionPeriod(t *testing.T) {
	b, clock := newClockedTestBalancer()
	key, backendURL := b.keys[0], b.backends[0].URL
	eject(b, key)

	// like a hung backend whose /health still answers
	for range 4 {
		clock.advance(baseEjection / 5)
		b.record(key, true)
		if inRotation(b, backendURL) {
			t.Fatal("back in rotation before its ejection period ended")
		}
	}
	clock.advance(baseEjection / 5)
	b.record(key, true)
	if !inRotation(b, backendURL) {
		t.Fatal("not back after its ejection period with passing checks")
	}
}

func TestBalancer_EjectionDoublesWhenRemovedSoonAfterReturning(t *testing.T) {
	b, clock := newClockedTestBalancer()
	key, backendURL := b.keys[0], b.backends[0].URL

	for _, want := range []time.Duration{baseEjection, 2 * baseEjection, 4 * baseEjection} {
		eject(b, key)
		clock.advance(want - time.Second)
		b.record(key, true)
		b.record(key, true)
		if inRotation(b, backendURL) {
			t.Fatalf("back before its %v ejection ended", want)
		}
		clock.advance(time.Second)
		b.record(key, true)
		if !inRotation(b, backendURL) {
			t.Fatalf("not back after its %v ejection", want)
		}
		clock.advance(5 * time.Second) // fails again shortly after returning
	}
}

func TestBalancer_EjectionResetsAfterStableStretch(t *testing.T) {
	b, clock := newClockedTestBalancer()
	key, backendURL := b.keys[0], b.backends[0].URL

	eject(b, key)
	clock.advance(baseEjection)
	b.record(key, true)
	b.record(key, true)
	clock.advance(stableAfter)

	eject(b, key)
	clock.advance(baseEjection)
	b.record(key, true)
	b.record(key, true)
	if !inRotation(b, backendURL) {
		t.Fatalf("ejection kept growing although the backend stayed up for %v", stableAfter)
	}
}

func TestEjectionPeriod_Capped(t *testing.T) {
	if got := ejectionPeriod(100); got != maxEjection {
		t.Errorf("ejectionPeriod(100) = %v, want %v", got, maxEjection)
	}
}
