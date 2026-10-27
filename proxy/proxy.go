// Package proxy handles all http contents
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// sharedTransport is reused across all reverse proxies this package
// creates. It raises the default per-host connection limits so that
// under concurrent load, connections to a backend are pooled and
// reused instead of being torn down and recreated for every request —
// avoiding rapid ephemeral port churn (and exhaustion) under load.
//
// The dial timeout fails over from a black-holed backend sooner than
// retryTransport's per-attempt timeout would on its own.
//
// MaxIdleConns is left at 0 (no global cap): a global cap below
// backends × MaxIdleConnsPerHost closes the excess pooled connections
// after every burst, only for them to be redialed on the next one.
var sharedTransport = &http.Transport{
	DialContext: (&net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	MaxIdleConnsPerHost: 100,
	MaxConnsPerHost:     100,
	IdleConnTimeout:     90 * time.Second,
}

const (
	dialTimeout = 2 * time.Second
	// attemptTimeout bounds one try at a backend, from waiting for a
	// pooled connection until response headers arrive. requestBudget
	// bounds all tries together; it should stay below clients' own
	// timeouts, so a retry can still answer before the client gives up.
	attemptTimeout = 5 * time.Second
	requestBudget  = 10 * time.Second
)

var errAttemptTimeout = errors.New("backend attempt timed out")

// sharedBufferPool recycles the 32KB buffers ReverseProxy copies
// response bodies through. Without it, every proxied response allocates
// a fresh buffer, which was ~77% of all bytes allocated under load and
// the main source of GC pressure.
var sharedBufferPool = &bufferPool{
	pool: sync.Pool{New: func() any {
		b := make([]byte, 32*1024)
		return &b
	}},
}

// bufferPool adapts sync.Pool to httputil.BufferPool. It stores
// *[]byte rather than []byte so Get doesn't allocate a slice header.
type bufferPool struct {
	pool sync.Pool
}

func (p *bufferPool) Get() []byte {
	return *p.pool.Get().(*[]byte)
}

func (p *bufferPool) Put(b []byte) {
	p.pool.Put(&b)
}

// retryTransport reports failed round trips to the balancer, so dead
// backends leave rotation without waiting for a health check, and
// retries idempotent, bodiless requests on a backend not yet tried.
// Only transport errors (refused, reset, timed out) count; a backend
// that answers, even with a 5xx, has responded and isn't retried.
//
// Each attempt runs under its own timer rather than relying on the
// transport's ResponseHeaderTimeout, which only starts once a
// connection is in hand: requests queued behind MaxConnsPerHost stuck
// connections to a hung backend would otherwise wait out a whole
// timeout before even starting theirs.
type retryTransport struct {
	next           http.RoundTripper
	balancer       Balancer
	maxAttempts    int
	attemptTimeout time.Duration
	budget         time.Duration
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	deadline := time.Now().Add(t.budget)
	tried := []string{backendKey(req.URL)}
	for {
		resp, err := t.attempt(req, min(t.attemptTimeout, time.Until(deadline)))
		if err == nil {
			return resp, nil
		}
		err = &backendError{host: req.URL.Host, err: err}
		// a canceled context means the client went away, not the backend
		if req.Context().Err() != nil {
			return nil, err
		}
		t.balancer.ReportFailure(req.URL)
		if !retryable(req) || len(tried) >= t.maxAttempts || time.Until(deadline) <= 0 {
			return nil, err
		}
		target := t.untriedBackend(tried)
		if target == nil {
			return nil, err
		}
		log.Printf("proxy retry from=%s to=%s err=%v", req.URL.Host, target.Host, err)
		req = req.Clone(req.Context())
		req.URL.Scheme, req.URL.Host = target.Scheme, target.Host
		tried = append(tried, backendKey(target))
	}
}

// attempt sends req once, canceling it if response headers don't arrive
// within timeout. The timer stops once they do, so a long-streaming
// body is bounded only by the client's own context.
func (t *retryTransport) attempt(req *http.Request, timeout time.Duration) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(req.Context())
	timer := time.AfterFunc(timeout, func() { cancel(errAttemptTimeout) })
	resp, err := t.next.RoundTrip(req.WithContext(ctx))
	timer.Stop()
	if err != nil {
		if errors.Is(context.Cause(ctx), errAttemptTimeout) {
			err = fmt.Errorf("%w after %v", errAttemptTimeout, timeout)
		}
		cancel(nil)
		return nil, err
	}
	// ReverseProxy needs an upgraded (101) body to stay an
	// io.ReadWriteCloser; ctx is released with the client's request then
	if resp.StatusCode != http.StatusSwitchingProtocols {
		resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	}
	return resp, nil
}

// cancelOnClose releases an attempt's context once ReverseProxy has
// finished copying the response body.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelCauseFunc
}

func (b *cancelOnClose) Close() error {
	err := b.ReadCloser.Close()
	b.cancel(nil)
	return err
}

// backendError records which backend a failed attempt went to. After a
// retry that differs from the request ReverseProxy passes to ErrorHandler.
type backendError struct {
	host string
	err  error
}

func (e *backendError) Error() string { return e.err.Error() }
func (e *backendError) Unwrap() error { return e.err }

// untriedBackend asks the balancer for a backend not in tried. The
// search is bounded because the balancer may only have tried ones left.
func (t *retryTransport) untriedBackend(tried []string) *url.URL {
	for range 2 * t.maxAttempts {
		u, err := url.Parse(t.balancer.Next().URL)
		if err == nil && !slices.Contains(tried, backendKey(u)) {
			return u
		}
	}
	return nil
}

func retryable(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return req.Body == nil || req.Body == http.NoBody
	}
	return false
}

// NewReverseProxy builds a reverse proxy that forwards each incoming
// request to whichever backend the given Balancer selects for that
// request, stripping the given path prefix so the backend sees its
// own native path rather than the gateway-facing one. Logs which
// backend served each request and the response status code.
func NewReverseProxy(balancer Balancer, stripPrefix string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport: &retryTransport{
			next:           sharedTransport,
			balancer:       balancer,
			maxAttempts:    3,
			attemptTimeout: attemptTimeout,
			budget:         requestBudget,
		},
		BufferPool: sharedBufferPool,
		Rewrite: func(r *httputil.ProxyRequest) {
			backend := balancer.Next()
			target, err := url.Parse(backend.URL)
			if err != nil {
				return
			}
			r.SetURL(target)
			r.Out.URL.Path = strings.TrimPrefix(r.In.URL.Path, stripPrefix)
			r.Out.Host = r.In.Host
		},
		ModifyResponse: func(resp *http.Response) error {
			log.Printf("proxied backend=%s status=%d path=%s",
				resp.Request.URL.Host, resp.StatusCode, resp.Request.URL.Path)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			backend := r.URL.Host
			var be *backendError
			if errors.As(err, &be) {
				backend = be.host
			}
			log.Printf("proxy error backend=%s path=%s err=%v",
				backend, r.URL.Path, err)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
}

// LoggingMiddleware wraps an http.Handler and logs the method, path,
// status code, and elapsed time for every request it handles.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		log.Printf("method=%s path=%s status=%d duration=%s",
			r.Method, r.URL.Path, rw.status, time.Since(start))
	})
}

// statusRecorder wraps http.ResponseWriter to capture the status code
// that gets written, since ResponseWriter doesn't expose it directly.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
