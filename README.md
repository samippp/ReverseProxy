# ReverseProxy

A Go-based API gateway with JWT authentication, round-robin load balancing, active health checks, and connection pooling — built and load-tested from scratch to understand how production reverse proxies actually work.

```
Client → [Gateway :8080] → JWT auth → Load balancer → Backend replica (1 of N)
                                            ↓
                                   Health checks every N seconds
```

## Features

- **JWT authentication** (HS256) protecting `/protected/*` routes
- **Round-robin load balancing** across a configurable pool of backend replicas
- **Active health checks** — a background goroutine pings each backend on an interval, marking unhealthy backends unavailable and automatically routing around them
- **Path stripping** — the gateway's public routes are decoupled from backend-internal routes (`/protected/secret` → backend sees `/secret`)
- **HTTP connection pooling** — reuses TCP connections to backends instead of opening a new one per request
- **PostgreSQL persistence** via `pgxpool`, with `sqlc`-generated type-safe queries
- **Structured logging** — per-request timing, which backend served each request, and proxy error visibility

## Architecture

Requests flow through the gateway as follows:

1. **Auth middleware** validates the JWT on `/protected/*` routes, rejecting unauthenticated requests with `401` before they reach the proxy layer
2. **Load balancer** (`RoundRobinBalancer`) selects a healthy backend from the configured pool, using an atomic counter for the rotation and a `sync.RWMutex` to guard health state, since `Next()` is called concurrently by every in-flight request while a background goroutine occasionally updates health status
3. **Reverse proxy** rewrites the outbound request (via `httputil.ReverseProxy`'s `Rewrite` hook — the modern replacement for the deprecated `Director` field) to point at the selected backend, stripping the gateway-facing path prefix
4. **Health checker** runs on a separate goroutine, pinging every backend's `/health` endpoint on a fixed interval and updating shared health state under a write lock

```go
type Balancer interface {
    Next() config.Backend
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

*\*Failover test: one backend was killed mid-run. Failures were bounded to a ~2-second window immediately following the kill — the time it took the health checker to detect the dead backend and stop routing to it. The remaining ~28 seconds of the 30-second test ran at 100% success across the two surviving backends, with zero manual intervention.*

### The connection pool bug

Early load tests above ~500 concurrent connections failed hard with:
```
dial tcp [::1]:9003: connectex: Only one usage of each socket address
(protocol/network address/port) is normally permitted.
```

This is Windows' wording for **ephemeral port exhaustion**. `httputil.ReverseProxy` uses `http.DefaultTransport` unless configured otherwise, and `DefaultTransport`'s `MaxIdleConnsPerHost` defaults to `2` — meaning under concurrent load, only 2 connections per backend were ever reused; everything beyond that was opened fresh and torn down immediately after use. Each teardown leaves the connection's port in a `TIME_WAIT` state for ~120 seconds (on Windows), and under sustained high-concurrency traffic, new connections were being created faster than old ones could clear — exhausting the available ephemeral port range outright.

**Fix:** a shared `http.Transport` with explicit connection limits:

```go
var sharedTransport = &http.Transport{
    MaxIdleConns:        200,
    MaxIdleConnsPerHost: 100,
    MaxConnsPerHost:     100,
    IdleConnTimeout:     90 * time.Second,
}
```

`MaxConnsPerHost` was the critical addition — `MaxIdleConnsPerHost` alone only bounds how many *idle* connections get reused; it does nothing to cap how many can be opened *concurrently* in the first place. With both set, connections to each backend are capped and reused rather than churned, and load tests up to 300 concurrent connections / 100,000 requests ran without a single connection-related failure afterward.

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
```

## Running locally

Requires Go, PostgreSQL, and [`hey`](https://github.com/rakyll/hey) (for load testing).

```bash
./scripts/dev-up.sh          # starts 3 dummy backends + the gateway
./scripts/get-token.sh       # registers a test user, logs in, prints a JWT
./scripts/load-test.sh                        # 2,000 requests, concurrency 50
./scripts/load-test.sh 100000 300             # custom request count / concurrency
./scripts/load-test.sh --duration 30s 50      # fixed time window (for failover testing)
```

To test failover: start a duration-based load test, then kill one dummy backend process mid-run (`taskkill //PID <pid> //F` on Windows, `kill <pid>` on Unix) and watch the gateway's logs show a bounded burst of `502`s before health checks route around it.

## Testing

```bash
go test ./...
```

Covers: registration/login, protected-route auth (401/200 paths), round-robin distribution across backends, and health-check-driven failover (verified by closing a test backend mid-suite and confirming the balancer stops routing to it).
