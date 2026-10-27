# ReverseProxy

A Go-based API gateway with JWT authentication, round-robin load balancing, active and passive health checks with retries, and connection pooling — built and load-tested from scratch to understand how production reverse proxies actually work.

```
Client → [Gateway :8080] → JWT auth → Load balancer → Backend replica (1 of N)
                                            ↓
                                   Health checks every N seconds
```

## Features

- **JWT authentication** (HS256) protecting `/protected/*` routes
- **Round-robin load balancing** across a configurable pool of backend replicas
- **Active health checks:** a background goroutine pings each backend's `/health` endpoint on an interval and routes around backends that fail.
- **Passive health checks and retries:** a failed proxied request counts against its backend immediately, without waiting for the next check. Safe-to-repeat requests (GET, HEAD, OPTIONS without a body) are retried on a different backend, so clients don't see the failure.
- **Flap protection:** a backend leaves rotation only after 3 consecutive failures, and returns only after 2 consecutive passing checks.
- **Path stripping** — the gateway's public routes are decoupled from backend-internal routes (`/protected/secret` → backend sees `/secret`)
- **HTTP connection pooling** — reuses TCP connections to backends instead of opening a new one per request
- **PostgreSQL persistence** via `pgxpool`, with `sqlc`-generated type-safe queries
- **Structured logging** — per-request timing, which backend served each request, and proxy error visibility

## Architecture

Requests flow through the gateway as follows:

1. **Auth middleware** validates the JWT on `/protected/*` routes, rejecting unauthenticated requests with `401` before they reach the proxy layer
2. **Load balancer** (`RoundRobinBalancer`) picks the next backend from an immutable list of the healthy ones, using an atomic counter for the rotation. `Next()` is called by every in-flight request, so it never takes a lock. When a backend's health changes, the balancer builds a new list and swaps it in atomically.
3. **Reverse proxy** rewrites the outbound request (via `httputil.ReverseProxy`'s `Rewrite` hook — the modern replacement for the deprecated `Director` field) to point at the selected backend, stripping the gateway-facing path prefix
4. **Health tracking** combines two signals: a background goroutine pinging every backend's `/health` endpoint, and failures reported by proxied requests. A backend is removed after 3 consecutive failures from either source, and re-added after 2 consecutive passing checks.
5. **Retries:** when a request fails to reach a backend (connection refused, reset or timed out), the proxy retries it on a backend it hasn't tried yet, up to 3 attempts. This only applies to GET, HEAD and OPTIONS requests without a body. A POST might not be safe to send twice, so it fails with a 502. A backend that answers, even with a 5xx error, isn't retried.

```go
type Balancer interface {
    Next() config.Backend
    ReportFailure(target *url.URL)
}
```

Backends are grouped by service name in config, so the same balancer/health-check machinery could support multiple independent backend pools (e.g. one for `/protected/secret`, a separate one for a future `/protected/orders`) without changes to the core logic.

## Load testing

Tested locally with [`hey`](https://github.com/rakyll/hey) against 3 backend replicas.

| Test | Requests | Concurrency | Req/s | p50 | p99 | Success |
|---|---|---|---|---|---|---|
| Baseline | 2,000 | 50 | 12,747 | 2.8ms | 19.4ms | 100% |
| Sustained volume | 100,000 | 300 | 34,217 | 7.2ms | 25.4ms | 100% |
| Failover (live kill) | ~758,000 (30s) | 50 | 25,278 | 1.7ms | 8.4ms | 97.8%* |

*\*Failover test: one backend was killed mid-run. Failures were bounded to a ~2-second window immediately following the kill — the time it took the health checker to detect the dead backend and stop routing to it. The remaining ~28 seconds of the 30-second test ran at 100% success across the two surviving backends, with zero manual intervention. These figures predate passive health checks and retries; see below.*

### Failover with passive health checks and retries

The same failover test was re-run with the newer code, CPU-pinned like the nginx comparison. It's a 30-second load at concurrency 50, with backend-2 killed (`kill -9`) at 8 seconds and restarted at 18:

| | Active checks only | + Passive checks and retries |
|---|---|---|
| Responses | 339,857 | 600,425 |
| 502 errors | 8,147 (2.4%) | **0** |
| Retries | — | 27 |

With retries, the first few requests to hit the dead backend were sent on to a surviving one. After 3 failures, the backend was out of rotation within milliseconds of the kill instead of at the next 5-second health check. When it restarted, it rejoined after 2 consecutive passing checks.

The throughput gap only appears during the failover run, and its cause hasn't been investigated: under normal load, both builds handle about 20k req/s.

### Comparison with nginx

The same workload through nginx 1.30 configured to do the gateway's job: round-robin over the same 3 dummy backends, `/protected` prefix stripping, and 100 keepalive connections per upstream (matching the gateway's `MaxIdleConnsPerHost`). A direct-to-backend run gives the no-proxy baseline.

| Target | Concurrency | Req/s | p50 | p99 | Success |
|---|---|---|---|---|---|
| direct (1 backend) | 50 | 42,518 | 1.0ms | 3.3ms | 100% |
| nginx | 50 | 20,735 | 2.2ms | 5.6ms | 100% |
| **gateway** | 50 | **18,345** | 2.3ms | 7.6ms | 100% |
| direct (1 backend) | 300 | 36,158 | 6.3ms | 30.9ms | 100% |
| nginx | 300 | 22,178 | 10.2ms | 51.0ms | 100% |
| **gateway** | 300 | **18,513** | 14.4ms | 42.1ms | 100% |

The gateway reaches roughly **83–88% of nginx's throughput**. At concurrency 300 its p99 (42ms) is lower than nginx's (51ms). Before the buffer-pool fix described below, it reached only 60–65%: 14,244 req/s at concurrency 50 and 13,366 req/s at 300.

How it was run (`./scripts/bench-nginx.sh`):
- Everything runs natively on one 12-core machine (WSL2, Arch Linux), with each group pinned to its own 4 cores via `taskset`: `hey` on 0–3, the proxy under test on 4–7, and the backends on 8–11.
- nginx runs 4 workers, and the gateway runs with `GOMAXPROCS=4`, so both get the same CPU budget.
- Each row is the median of 3 runs of 100,000 requests, after a 5,000-request warmup.

It isn't fully like-for-like:
- **Only the gateway validates the JWT.** Open-source nginx can't validate JWTs (that's an NGINX Plus feature). Both receive the same `Authorization` header, so requests are the same size, but the gateway parses and HMAC-verifies a token on every request.
- **The gateway does more logging.** It writes two log lines per request (`LoggingMiddleware` and `ModifyResponse`) through Go's mutex-guarded `log` package. nginx writes one access-log line.
- **Results at concurrency 300 are noisier.** Even the direct baseline ranged from 33k to 41k req/s across its three runs.

These numbers aren't comparable with the load-test table above: those runs were on Windows, used `go run` builds and didn't pin CPUs.

### Profiling: the 32KB buffer per response

The gateway can serve `net/http/pprof` on a separate port by setting `PPROF_ADDR=127.0.0.1:6060`. Profiling a 40-second load at concurrency 50 showed where the gateway's CPU and memory went:

| | Before | After `BufferPool` |
|---|---|---|
| Throughput under the profiler | 13.6k req/s | 19.1k req/s |
| Memory allocated per request | ~40 KB | ~9 KB |
| CPU spent on garbage collection | ~11% | ~3% |

By default, `httputil.ReverseProxy` allocates a fresh 32KB buffer for every response it copies. That one allocation accounted for **77% of all bytes allocated**, and the garbage collector had to clean up after it. Setting the proxy's `BufferPool` to a `sync.Pool`-backed pool, so buffers are reused instead of reallocated, took throughput from about 14k to 18.5k req/s with a one-line hookup.

What's left in the profile, as a share of the gateway's CPU time:
- **Socket reads and writes:** ~30%. These are system calls, and nginx makes them too.
- **JWT validation:** ~10%. Most of that is `encoding/json` decoding the token's header and claims.
- **Logging:** ~6%. Two formatted log lines and a `write` system call per request.
- **The round-robin balancer:** under 0.5%. Its lock never showed up as contention.

### The connection pool bug

Early load tests above ~500 concurrent connections failed hard with:
```
dial tcp [::1]:9003: connectex: Only one usage of each socket address
(protocol/network address/port) is normally permitted.
```

This is Windows' wording for **ephemeral port exhaustion**. `httputil.ReverseProxy` uses `http.DefaultTransport` unless configured otherwise, and `DefaultTransport`'s `MaxIdleConnsPerHost` defaults to `2` — meaning under concurrent load, only 2 connections per backend were ever reused; everything beyond that was opened fresh and torn down immediately after use. Each teardown leaves the connection's port in a `TIME_WAIT` state for ~120 seconds (on Windows), and under sustained high-concurrency traffic, new connections were being created faster than old ones could clear — exhausting the available ephemeral port range outright.

**Fix:** a shared `http.Transport` with explicit connection limits. This is its current form:

```go
var sharedTransport = &http.Transport{
    DialContext: (&net.Dialer{
        Timeout:   2 * time.Second,
        KeepAlive: 30 * time.Second,
    }).DialContext,
    MaxIdleConnsPerHost: 100,
    MaxConnsPerHost:     100,
    IdleConnTimeout:     90 * time.Second,
}
```

`MaxConnsPerHost` was the critical addition — `MaxIdleConnsPerHost` alone only bounds how many *idle* connections get reused; it does nothing to cap how many can be opened *concurrently* in the first place. With both set, connections to each backend are capped and reused rather than churned, and load tests up to 300 concurrent connections / 100,000 requests ran without a single connection-related failure afterward.

Two later changes:
- **No global idle cap.** The transport originally also set `MaxIdleConns: 200`. That was below 3 backends × 100, so after every burst the transport closed 100 pooled connections, only to open them again on the next burst. Leaving `MaxIdleConns` at 0 means there's no global cap, and `MaxIdleConnsPerHost` still caps each backend at 100 idle connections.
- **Timeouts.** Stress testing showed that without them, a backend that never answers, or one whose network silently drops connection attempts, makes requests hang until the client gives up. Those requests don't count against the backend, so it stays in rotation. Now the transport gives up on connecting after 2s, and the retry layer gives each attempt 5s to get a response started, with a 10s budget across all attempts. A timeout counts as a failure, which removes the backend and triggers a retry.

  The 5s clock starts before the request has a connection, not when it's sent. An earlier version used the transport's `ResponseHeaderTimeout`, which only starts once a connection is in hand. When all 100 connections to a hung backend were stuck, new requests first waited up to 5s for one to free up, and only then started their own 5s clock. In a 30-second test with a hung backend and clients that give up at 10s, that caused 362 client timeouts. Timing the whole attempt brought it to 0, and the slowest request fell from 10.0s to 5.2s. The clock stops once the response headers arrive, so slow response bodies aren't cut off.

## Project structure

```
cmd/
  gateway/        — main entrypoint
  dummybackend/   — throwaway backend for local load testing (not part of proxy logic)
config/           — YAML + .env config loading
proxy/            — router, reverse proxy, load balancer, health checks
auth/             — JWT generation/validation, auth middleware
api/               — HTTP handlers (register, login, home)
store/            — PostgreSQL data access (sqlc-generated)
tests/            — integration tests (auth flow, round-robin, health-check failover)
scripts/          — dev environment + load testing scripts
bench/            — nginx config for the comparison benchmark
```

## Running locally

Requires Go, PostgreSQL, and [`hey`](https://github.com/rakyll/hey) (for load testing).

```bash
./scripts/dev-up.sh          # starts 3 dummy backends + the gateway
./scripts/get-token.sh       # registers a test user, logs in, prints a JWT
./scripts/load-test.sh                        # 2,000 requests, concurrency 50
./scripts/load-test.sh 100000 300             # custom request count / concurrency
./scripts/load-test.sh --duration 30s 50      # fixed time window (for failover testing)
./scripts/bench-nginx.sh                      # gateway vs nginx vs direct (needs nginx, 12 cores)
```

To test failover: start a duration-based load test, then kill one dummy backend process mid-run (`taskkill //PID <pid> //F` on Windows, `kill <pid>` on Unix) and watch the gateway's logs. The dead backend should be `removed from rotation` after a few retries, without any `502`s, and come `back in rotation` after you restart it.

## Testing

```bash
go test ./...
```

Covers: registration/login, protected-route auth (401/200 paths), round-robin distribution across backends, health-check-driven failover (verified by closing a test backend mid-suite and confirming the balancer stops routing to it), retries of GET requests that hit a dead backend (and no retries for POST), and the fall/rise thresholds that stop flapping.
