package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samippp/ReverseProxy/config"
	"github.com/samippp/ReverseProxy/proxy"
)

func TestRoundRobinBalancer_SkipsUnhealthyBackend(t *testing.T) {
	t.Log("-----------------------Test Balancer Skips Unhealthy Backend--------------------")

	// Two dummy backends, each identifiable by name
	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend2.Close() // backend1 gets closed deliberately mid-test

	balancer := proxy.NewRoundRobinBalancer([]config.Backend{
		{Name: "backend-1", URL: backend1.URL},
		{Name: "backend-2", URL: backend2.URL},
	})

	// Confirm both are in rotation before anything is closed
	seenBefore := map[string]int{}
	for i := 0; i < 10; i++ {
		seenBefore[balancer.Next().URL]++
	}
	if seenBefore[backend1.URL] == 0 || seenBefore[backend2.URL] == 0 {
		t.Fatalf("expected both backends in rotation before closing either, got %+v", seenBefore)
	}

	// Start health checks on a short interval, then kill backend1
	balancer.StartHealthChecks(50 * time.Millisecond)
	backend1.Close()

	// Wait past several check intervals: it takes 3 consecutive
	// failed checks before a backend leaves rotation
	time.Sleep(400 * time.Millisecond)

	// Now backend1 should never be returned
	seenAfter := map[string]int{}
	for i := 0; i < 10; i++ {
		seenAfter[balancer.Next().URL]++
	}
	t.Logf("distribution after closing backend1: %+v", seenAfter)

	if seenAfter[backend1.URL] != 0 {
		t.Errorf("expected backend1 to be skipped after becoming unhealthy, got %d hits", seenAfter[backend1.URL])
	}
	if seenAfter[backend2.URL] != 10 {
		t.Errorf("expected all 10 requests to go to backend2, got %d", seenAfter[backend2.URL])
	}
}

func TestProtectedSecret_RoundRobin_DistributesAcrossBackends(t *testing.T) {
	t.Log("-----------------------Test Round Robin Distribution--------------------")

	// 1. Create user + get a valid token once
	payload, regResp := createTestUser(t, "Robin", "meow", "robin@gmail.com")
	if err := regResp.Body.Close(); err != nil {
		t.Logf("Warning: failed to close setup response body: %v", err)
	}
	_, data := performLoginAndValidateToken(t, payload)
	tokenStr, exists := data["token"]
	if !exists || tokenStr == "" {
		t.Fatalf("TestProtectedSecret_RoundRobin_DistributesAcrossBackends: missing token in login data map")
	}

	// 2. Fire several requests, track which backend answered each one
	seen := map[string]int{}
	const numRequests = 9 // 3 backends × 3 full cycles

	for i := range numRequests {
		req, err := http.NewRequest("GET", testServer.URL+"/protected/secret", nil)
		if err != nil {
			t.Fatalf("failed to build request %d: %v", i, err)
		}
		req.Header.Set("Authorization", "Bearer "+tokenStr)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("failed to read response body for request %d: %v", i, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d: %s", i, resp.StatusCode, body)
		}

		var parsed map[string]string
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Fatalf("request %d: failed to unmarshal response: %v", i, err)
		}
		seen[parsed["backend"]]++
	}

	// 3. Assert all three backends were hit
	t.Logf("distribution: %+v", seen)
	for _, backendID := range []string{"1", "2", "3"} {
		if seen[backendID] == 0 {
			t.Errorf("expected backend %s to receive at least one request, got none", backendID)
		}
	}

	// 4. With round-robin over 9 requests / 3 backends, expect an even 3/3/3 split
	for backendID, count := range seen {
		if count != numRequests/3 {
			t.Errorf("expected backend %s to receive exactly %d requests, got %d", backendID, numRequests/3, count)
		}
	}
}

// deadAndAliveBackends returns the URL of a closed server (connections
// are refused) and a live server that answers 200 with its name.
func deadAndAliveBackends(t *testing.T) (deadURL string, alive *httptest.Server) {
	t.Helper()
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL = dead.URL
	dead.Close()
	alive = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("alive"))
	}))
	t.Cleanup(alive.Close)
	return deadURL, alive
}

func TestReverseProxy_RetriesGetOnDeadBackend(t *testing.T) {
	t.Log("-----------------------Test Retry GET On Dead Backend--------------------")

	deadURL, alive := deadAndAliveBackends(t)
	balancer := proxy.NewRoundRobinBalancer([]config.Backend{
		{Name: "dead", URL: deadURL},
		{Name: "alive", URL: alive.URL},
	})
	front := httptest.NewServer(proxy.NewReverseProxy(balancer, ""))
	defer front.Close()

	// no active health checks: every request that lands on the dead
	// backend must be rescued by a retry, and the failures alone must
	// take it out of rotation
	for i := range 10 {
		resp, err := http.Get(front.URL + "/secret")
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("request %d: expected 200 via retry, got %d", i, resp.StatusCode)
		}
	}

	for range 10 {
		if got := balancer.Next(); got.URL == deadURL {
			t.Fatal("dead backend still in rotation after repeated failed requests")
		}
	}
}

func TestReverseProxy_DoesNotRetryPost(t *testing.T) {
	t.Log("-----------------------Test No Retry For POST--------------------")

	deadURL, alive := deadAndAliveBackends(t)
	balancer := proxy.NewRoundRobinBalancer([]config.Backend{
		{Name: "dead", URL: deadURL},
		{Name: "alive", URL: alive.URL},
	})
	front := httptest.NewServer(proxy.NewReverseProxy(balancer, ""))
	defer front.Close()

	// round robin alternates, so some of these land on the dead backend;
	// a POST may not be safe to repeat, so those must surface as 502
	badGateways := 0
	for i := range 4 {
		resp, err := http.Post(front.URL+"/orders", "application/json", strings.NewReader(`{"n":1}`))
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusBadGateway {
			badGateways++
		}
	}
	if badGateways == 0 {
		t.Error("expected POSTs routed to the dead backend to return 502, not be retried")
	}
}
