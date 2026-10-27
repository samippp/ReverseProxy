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

// sharedTransport is shared by all proxies so backend connections are pooled.
// MaxIdleConns stays 0 (no global cap): a cap below backends × per-host
// idle conns closes pooled connections after every burst.
var sharedTransport = &http.Transport{
	DialContext: (&net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	MaxIdleConnsPerHost: 100,
	MaxConnsPerHost:     100,
	IdleConnTimeout:     90 * time.Second,
}

// Backend timeouts. The dial timeout fails over from a black-holed
// backend sooner than attemptTimeout would.
const (
	dialTimeout = 2 * time.Second
	// attemptTimeout covers one try, from waiting for a connection to
	// response headers. requestBudget covers all tries; keep it below
	// clients' timeouts so a retry can still answer in time.
	attemptTimeout = 5 * time.Second
	requestBudget  = 10 * time.Second
)

// errAttemptTimeout is the cancel cause when an attempt runs out of time.
var errAttemptTimeout = errors.New("backend attempt timed out")

// sharedBufferPool reuses ReverseProxy's 32KB copy buffers; allocating one
// per response was ~77% of all allocated bytes.
var sharedBufferPool = &bufferPool{
	pool: sync.Pool{New: func() any {
		b := make([]byte, 32*1024)
		return &b
	}},
}

// bufferPool adapts sync.Pool to httputil.BufferPool. It stores *[]byte so
// Get doesn't allocate.
type bufferPool struct {
	pool sync.Pool
}

// Get returns a pooled buffer.
func (p *bufferPool) Get() []byte {
	return *p.pool.Get().(*[]byte)
}

// Put returns a buffer to the pool.
func (p *bufferPool) Put(b []byte) {
	p.pool.Put(&b)
}

// retryTransport reports transport errors (refused, reset, timed out) to
// the balancer and retries bodiless GET/HEAD/OPTIONS on an untried backend.
// A backend that answers, even with a 5xx, isn't retried.
type retryTransport struct {
	next           http.RoundTripper
	balancer       Balancer
	maxAttempts    int
	attemptTimeout time.Duration
	budget         time.Duration
}

// RoundTrip tries backends until one answers, the attempts run out or the
// request budget is spent.
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

// attempt sends req once, canceling it if headers don't arrive within
// timeout. The timer stops at headers so slow bodies aren't cut off.
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
	// a 101 body must stay an io.ReadWriteCloser; ctx ends with the client's
	if resp.StatusCode != http.StatusSwitchingProtocols {
		resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	}
	return resp, nil
}

// cancelOnClose releases an attempt's context when the body is closed.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelCauseFunc
}

// Close closes the body, then releases the context.
func (b *cancelOnClose) Close() error {
	err := b.ReadCloser.Close()
	b.cancel(nil)
	return err
}

// backendError names the backend a failed attempt went to, which differs
// from ErrorHandler's request after a retry.
type backendError struct {
	host string
	err  error
}

// Error returns the underlying error's message.
func (e *backendError) Error() string { return e.err.Error() }

// Unwrap returns the underlying error.
func (e *backendError) Unwrap() error { return e.err }

// untriedBackend returns a backend not in tried, or nil. The search is
// bounded since only tried backends may be left.
func (t *retryTransport) untriedBackend(tried []string) *url.URL {
	for range 2 * t.maxAttempts {
		u, err := url.Parse(t.balancer.Next().URL)
		if err == nil && !slices.Contains(tried, backendKey(u)) {
			return u
		}
	}
	return nil
}

// retryable reports whether req is safe to send again.
func retryable(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return req.Body == nil || req.Body == http.NoBody
	}
	return false
}

// NewReverseProxy proxies each request to the backend balancer picks,
// stripping stripPrefix from the path.
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

// LoggingMiddleware logs method, path, status and duration per request.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		log.Printf("method=%s path=%s status=%d duration=%s",
			r.Method, r.URL.Path, rw.status, time.Since(start))
	})
}

// statusRecorder captures the status code written to a ResponseWriter.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader records status and passes it on.
func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
