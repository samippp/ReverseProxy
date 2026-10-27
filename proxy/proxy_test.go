package proxy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/samippp/ReverseProxy/config"
)

func TestRetryTransport_RetriesAfterResponseHeaderTimeout(t *testing.T) {
	release := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer hung.Close()
	defer close(release)
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("alive"))
	}))
	defer alive.Close()

	balancer := NewRoundRobinBalancer([]config.Backend{
		{Name: "hung", URL: hung.URL},
		{Name: "alive", URL: alive.URL},
	})
	transport := &http.Transport{ResponseHeaderTimeout: 50 * time.Millisecond}
	defer transport.CloseIdleConnections()
	rt := &retryTransport{next: transport, balancer: balancer, maxAttempts: 3}

	// a hung backend never errors on its own, so only the header
	// timeout can get these requests retried and the backend ejected
	for i := range fallThreshold {
		req := httptest.NewRequest(http.MethodGet, hung.URL+"/secret", nil)
		req.RequestURI = ""
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("request %d: expected retry to succeed, got %v", i, err)
		}
		resp.Body.Close()
		if resp.Request.URL.Host != alive.Listener.Addr().String() {
			t.Fatalf("request %d: served by %s, want the alive backend", i, resp.Request.URL.Host)
		}
	}
	if inRotation(balancer, hung.URL) {
		t.Errorf("hung backend still in rotation after %d header timeouts", fallThreshold)
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
	rt := &retryTransport{next: &http.Transport{}, balancer: balancer, maxAttempts: 3}

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
