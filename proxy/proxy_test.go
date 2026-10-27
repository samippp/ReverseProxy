package proxy

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/samippp/ReverseProxy/config"
)

// hungAndAlive returns a backend that never answers until the test ends,
// and one that answers immediately.
func hungAndAlive(t *testing.T) (hung, alive *httptest.Server) {
	t.Helper()
	release := make(chan struct{})
	hung = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	alive = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("alive"))
	}))
	t.Cleanup(func() {
		close(release)
		hung.Close()
		alive.Close()
	})
	return hung, alive
}

func get(t *testing.T, rt http.RoundTripper, target string) (*http.Response, error) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target+"/secret", nil)
	req.RequestURI = ""
	return rt.RoundTrip(req)
}

func TestRetryTransport_RetriesAfterAttemptTimeout(t *testing.T) {
	hung, alive := hungAndAlive(t)
	balancer := NewRoundRobinBalancer([]config.Backend{
		{Name: "hung", URL: hung.URL},
		{Name: "alive", URL: alive.URL},
	})
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	rt := &retryTransport{next: transport, balancer: balancer, maxAttempts: 3,
		attemptTimeout: 50 * time.Millisecond, budget: time.Second}

	// a hung backend never errors on its own, so only the attempt
	// timeout can get these requests retried and the backend ejected
	for i := range fallThreshold {
		resp, err := get(t, rt, hung.URL)
		if err != nil {
			t.Fatalf("request %d: expected retry to succeed, got %v", i, err)
		}
		resp.Body.Close()
		if resp.Request.URL.Host != alive.Listener.Addr().String() {
			t.Fatalf("request %d: served by %s, want the alive backend", i, resp.Request.URL.Host)
		}
	}
	if inRotation(balancer, hung.URL) {
		t.Errorf("hung backend still in rotation after %d attempt timeouts", fallThreshold)
	}
}

func TestRetryTransport_TimeoutCoversWaitForConnection(t *testing.T) {
	hung, alive := hungAndAlive(t)
	balancer := NewRoundRobinBalancer([]config.Backend{
		{Name: "hung", URL: hung.URL},
		{Name: "alive", URL: alive.URL},
	})
	// one connection per host, which the first request ties up
	transport := &http.Transport{MaxConnsPerHost: 1}
	defer transport.CloseIdleConnections()
	go transport.RoundTrip(httptest.NewRequest(http.MethodGet, hung.URL, nil).WithContext(t.Context()))
	time.Sleep(50 * time.Millisecond)

	rt := &retryTransport{next: transport, balancer: balancer, maxAttempts: 3,
		attemptTimeout: 100 * time.Millisecond, budget: time.Second}
	start := time.Now()
	resp, err := get(t, rt, hung.URL)
	if err != nil {
		t.Fatalf("expected a retry once waiting for a connection timed out, got %v", err)
	}
	resp.Body.Close()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("took %v; the wait for a connection isn't bounded by the attempt timeout", elapsed)
	}
}

func TestRetryTransport_StopsRetryingWhenBudgetRunsOut(t *testing.T) {
	var backends []config.Backend
	for range 3 {
		hung, _ := hungAndAlive(t)
		backends = append(backends, config.Backend{Name: "hung", URL: hung.URL})
	}
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	rt := &retryTransport{next: transport, balancer: NewRoundRobinBalancer(backends), maxAttempts: 3,
		attemptTimeout: 100 * time.Millisecond, budget: 150 * time.Millisecond}

	// 100ms, then the 50ms left of the budget, then no third attempt
	start := time.Now()
	_, err := get(t, rt, backends[0].URL)
	elapsed := time.Since(start)
	if !errors.Is(err, errAttemptTimeout) {
		t.Fatalf("expected an attempt timeout, got %v", err)
	}
	if elapsed < 140*time.Millisecond || elapsed > 250*time.Millisecond {
		t.Errorf("took %v, want about the 150ms budget", elapsed)
	}
}

func TestRetryTransport_SlowBodyOutlivesAttemptTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("first "))
		w.(http.Flusher).Flush()
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte("second"))
	}))
	defer slow.Close()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	rt := &retryTransport{next: transport, balancer: NewRoundRobinBalancer([]config.Backend{{Name: "slow", URL: slow.URL}}),
		maxAttempts: 3, attemptTimeout: 50 * time.Millisecond, budget: 100 * time.Millisecond}

	resp, err := get(t, rt, slow.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "first second" {
		t.Errorf("body cut off once headers arrived: %q, %v", body, err)
	}
}

func TestRetryTransport_ErrorNamesLastBackendTried(t *testing.T) {
	deadA := httptest.NewServer(http.NotFoundHandler())
	deadA.Close()
	deadB := httptest.NewServer(http.NotFoundHandler())
	deadB.Close()

	balancer := NewRoundRobinBalancer([]config.Backend{
		{Name: "a", URL: deadA.URL},
		{Name: "b", URL: deadB.URL},
	})
	rt := &retryTransport{next: &http.Transport{}, balancer: balancer, maxAttempts: 3,
		attemptTimeout: time.Second, budget: time.Second}

	// first attempt goes to a, the retry to b; the error must name b, not
	// the backend the original request was addressed to
	req := httptest.NewRequest(http.MethodGet, deadA.URL+"/secret", nil)
	req.RequestURI = ""
	_, err := rt.RoundTrip(req)
	var be *backendError
	if !errors.As(err, &be) {
		t.Fatalf("expected a backendError, got %v", err)
	}
	if want := deadB.Listener.Addr().String(); be.host != want {
		t.Errorf("error names backend %s, want %s", be.host, want)
	}
}
