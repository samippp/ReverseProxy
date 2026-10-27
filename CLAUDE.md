# CLAUDE.md

Context for working in this repo. See `README.md` for the user-facing overview and load-test results.

## What this is

A Go (1.26) API gateway / reverse proxy: JWT auth → round-robin load balancer with active health checks → `httputil.ReverseProxy` to backend replicas. Users are stored in PostgreSQL (`pgxpool` + `sqlc`). Module path: `github.com/samippp/ReverseProxy`.

## Request flow

```
Client → gateway (:8080, cmd/gateway)
  ├─ /home, /register, /login     → api.Handler (Postgres via store)
  └─ /protected/*                 → auth.HandleAuthMiddleware
                                    → proxy.NewReverseProxy(balancer, "/protected")
                                    → backend from config `backends.secret` (prefix stripped:
                                       /protected/secret → backend sees /secret)
All routes wrapped in proxy.LoggingMiddleware.
```

The auth middleware adds `X-User-Id` (the JWT's `user_id` claim, overwriting any client-sent value) and `X-Internal-Secret` (= `PROXY_SECRET`) headers before proxying.

## Layout

| Path | Purpose |
|---|---|
| `cmd/gateway` | Main entrypoint. Loads config, opens DB pool, builds router with 5s health-check interval. Set `PPROF_ADDR` (e.g. `127.0.0.1:6060`) to serve `net/http/pprof` on a separate port. |
| `cmd/dummybackend` | Stand-in backend for local load testing (`-port`, `-name`). Serves `/health` and `/secret` only (no catch-all, so path-stripping bugs surface as 404). |
| `cmd/backend` | Older standalone backend on `127.0.0.1:8081` that checks `X-Internal-Secret`; uses `secret/`. Not wired into config or scripts. |
| `config/` | `LoadEnv()` (walks up from cwd to find `.env`, `log.Fatal` if none) and `ImportServerConfig()` (reads YAML at `$SERVER_CONFIG`). |
| `proxy/router.go` | `NewRouter(pool, serverConfig, env, healthCheckInterval)` — wires everything. Only the `"secret"` backend pool is used. |
| `proxy/balancer.go` | `Balancer` interface (`Next`, `ReportFailure`) and `RoundRobinBalancer`. `Next()` is lock-free: it reads an immutable snapshot of healthy backends from an `atomic.Pointer`, which `record()` rebuilds under `mu` only when a backend changes state. Fall/rise thresholds (3 consecutive failures out, 2 consecutive passing checks back in) prevent flapping. Backends are keyed by `scheme://host`. `StartHealthChecks` GETs `<url>/health` (2s timeout, goroutine runs forever). If all backends are down, `Next()` fails open over all of them. |
| `proxy/proxy.go` | `NewReverseProxy` using the `Rewrite` hook (not `Director`); `retryTransport` wrapping the shared transport (reports transport errors to the balancer, and retries bodiless GET/HEAD/OPTIONS on an untried backend, up to 3 attempts; responses, including 5xx, are never retried). Each attempt gets 5s from waiting for a pooled connection until response headers arrive, and all attempts share a 10s budget; the timer stops at headers, so slow bodies aren't cut off. Keep the budget below clients' timeouts, or retries never get a chance; a package-level `sharedTransport` with raised per-host conn limits (see README "connection pool bug") and a 2s dial timeout, and `sharedBufferPool` (`sync.Pool` of 32KB copy buffers; removing it costs ~25% throughput, see README "Profiling"). 502 on proxy error. |
| `auth/` | HS256 JWT generate/validate (24h expiry), Bearer extraction, middleware. |
| `api/` | HTTP handlers: home, register (bcrypt via store), login (returns `{token, expires_in}`). |
| `store/` | Thin wrapper over sqlc `Queries`; handles password hashing/compare. All bcrypt work goes through `withBcryptSlot` (at most GOMAXPROCS/2 at once, so a login flood can't starve proxying; `ErrBusy` → 503 when the caller's 3s timeout runs out). Unknown usernames are compared against `dummyHash` so they take as long as a wrong password. |
| `db/` | `pool.go` (`PostgreSQLConnectionPoll`), `schema.sql`, `queries.sql`, `generated/` (sqlc output). |
| `tests/` | Integration tests (package `tests`) — need a live Postgres. |
| `scripts/` | `dev-up.sh` (3 dummy backends on 9001–9003 + gateway), `get-token.sh`, `load-test.sh` (uses `hey`), `bench-nginx.sh` (gateway vs nginx vs direct, CPU-pinned; needs nginx + 12 cores). |
| `bench/nginx.conf` | Unprivileged nginx config mirroring the gateway's `/protected/*` proxying (listens on 8090), used by `bench-nginx.sh`. |
| `bin/create_psql.py` | Creates the `reverseproxy` DB + `users` table locally (psycopg2, hardcoded creds). |

`pkg/` is empty and `package-lock.json` is a stray empty npm lockfile.

## Configuration

`.env` (gitignored, must exist in cwd or a parent):
```
SERVER_CONFIG=config.yaml   # relative paths resolve against the .env's directory
DATABASE_URL=postgres://...
JWT_SECRET=...
PROXY_SECRET=...
```

`config.yaml`: `server` (hostname/port/endpoint), `routes` (parsed but currently unused by the router), `backends` (map of service name → list of `{name, url}`; only `secret` is consumed).

## Commands

```bash
go build ./...
go vet ./...
gofmt -l .                 # CI fails if this prints anything
go test ./... -v           # requires Postgres with db/schema.sql applied + .env
sqlc generate              # after editing db/schema.sql or db/queries.sql
./scripts/dev-up.sh        # local stack
./scripts/load-test.sh [n] [c] | --duration 30s [c]
```

CI (`.github/workflows/test.yml`): Postgres 16 service, gofmt check, `go vet`, apply schema, write `.env`, `go test ./... -v`.

## Testing notes

- `tests/main_test.go` `TestMain` builds the real router via `proxy.NewRouter` against three `httptest` backends (overriding `cfg.Backends["secret"]`) with a 100ms health-check interval.
- Local dev Postgres is the Windows service `postgresql-x64-18`. WSL reaches it on `localhost:5432` via mirrored networking (`networkingMode=mirrored` in `C:\Users\samip\.wslconfig`), so `go test ./...` works from both WSL and Windows.
- Use the helpers `createTestUser` (registers + `t.Cleanup` deletes the user) and `performLoginAndValidateToken`.
- Tests share one database and reuse usernames (e.g. "John"), relying on cleanup — don't add `t.Parallel()` without unique names.
- `TestProtectedSecret_RoundRobin_DistributesAcrossBackends` expects an exact 3/3/3 split, so anything else hitting the balancer during that test will break it.
- `proxy/balancer_test.go` is an internal (package `proxy`) unit test that drives `record()` directly, so threshold tests don't depend on timing. Prefer that over sleep-based tests for balancer logic.
- Run `go test -race ./proxy/ ./tests/` after touching the balancer or retry code.

## Conventions

- Don't hand-edit `db/generated/`; change SQL and run `sqlc generate`.
- New proxied services: add a pool under `backends` in `config.yaml`, then build a balancer + proxy for it in `NewRouter` (the config is already shaped for multiple pools).
- Keep the shared `http.Transport`; don't create per-proxy transports or fall back to `http.DefaultTransport`.
- Comments are sparse and explain *why*; match that.

## Known quirks / tech debt

Worth knowing before changing related code (not yet fixed):

- `auth.GenerateToken` ignores its `jwtSecret` param; both generate and validate read `os.Getenv("JWT_SECRET")` directly.
- `api.New` calls `config.LoadEnv()` itself rather than receiving the env from the router.
- `api.SecretHandler` is an unused stub.
