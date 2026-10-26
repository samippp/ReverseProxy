// Package proxy handles all http contents
package proxy

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// sharedTransport is reused across all reverse proxies this package
// creates. It raises the default per-host connection limits so that
// under concurrent load, connections to a backend are pooled and
// reused instead of being torn down and recreated for every request —
// avoiding rapid ephemeral port churn (and exhaustion) under load.
var sharedTransport = &http.Transport{
	MaxIdleConns:        200,
	MaxIdleConnsPerHost: 100,
	MaxConnsPerHost:     100,
	IdleConnTimeout:     90 * time.Second,
}

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

// NewReverseProxy builds a reverse proxy that forwards each incoming
// request to whichever backend the given Balancer selects for that
// request, stripping the given path prefix so the backend sees its
// own native path rather than the gateway-facing one. Logs which
// backend served each request and the response status code.
func NewReverseProxy(balancer Balancer, stripPrefix string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport:  sharedTransport,
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
			log.Printf("proxy error backend=%s path=%s err=%v",
				r.URL.Host, r.URL.Path, err)
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
