#!/usr/bin/env bash
# Benchmarks the gateway against nginx doing the same job (round-robin
# over 3 dummy backends with /protected prefix stripping), plus a
# direct-to-backend baseline. Each process group is pinned to its own
# cores so the load generator doesn't steal CPU from the proxy.
#
# Usage: ./scripts/bench-nginx.sh [requests] [runs] [concurrency...]
#   defaults: 100000 requests, 3 runs, concurrency 50 and 300
# Requires: nginx, hey, taskset, Postgres (for the gateway's login).
set -euo pipefail

cd "$(dirname "$0")/.."

REQUESTS="${1:-100000}"
RUNS="${2:-3}"
shift $(( $# > 2 ? 2 : $# ))
if [ $# -gt 0 ]; then CONCURRENCIES=("$@"); else CONCURRENCIES=(50 300); fi

HEY_CPUS="0-3"
PROXY_CPUS="4-7"
BACKEND_CPUS="8-11"

for cmd in nginx hey taskset; do
  command -v "$cmd" >/dev/null || { echo "missing: $cmd" >&2; exit 1; }
done
if [ "$(nproc)" -lt 12 ]; then
  echo "needs 12 cores for CPU pinning, found $(nproc)" >&2
  exit 1
fi

WORK=$(mktemp -d)
mkdir -p "$WORK/nginx/tmp" "$WORK/bin" "$WORK/logs"
PIDS=()
cleanup() {
  kill "${PIDS[@]}" 2>/dev/null || true
  wait 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

echo "building binaries..." >&2
go build -o "$WORK/bin/gateway" ./cmd/gateway
go build -o "$WORK/bin/dummybackend" ./cmd/dummybackend

echo "starting backends..." >&2
for i in 1 2 3; do
  taskset -c "$BACKEND_CPUS" "$WORK/bin/dummybackend" -port="900$i" -name="backend-$i" \
    >"$WORK/logs/backend-$i.log" 2>&1 &
  PIDS+=($!)
done

echo "starting gateway (GOMAXPROCS=4)..." >&2
GOMAXPROCS=4 taskset -c "$PROXY_CPUS" "$WORK/bin/gateway" >"$WORK/logs/gateway.log" 2>&1 &
PIDS+=($!)

echo "starting nginx (4 workers)..." >&2
cp bench/nginx.conf "$WORK/nginx/nginx.conf"
taskset -c "$PROXY_CPUS" nginx -p "$WORK/nginx" -e "$WORK/nginx/error.log" -c "$WORK/nginx/nginx.conf" &
PIDS+=($!)

wait_for() {
  for _ in $(seq 50); do
    curl -s -o /dev/null "$1" && return 0
    sleep 0.1
  done
  echo "timed out waiting for $1" >&2
  exit 1
}
wait_for http://127.0.0.1:9001/health
wait_for http://127.0.0.1:8080/home
wait_for http://127.0.0.1:8090/protected/secret

TOKEN=$(./scripts/get-token.sh 2>/dev/null)
[ -n "$TOKEN" ] || { echo "failed to get a token" >&2; exit 1; }

# label|url — the token is sent to every target so request size matches;
# only the gateway validates it (open-source nginx has no JWT support)
TARGETS=(
  "direct (1 backend)|http://127.0.0.1:9001/secret"
  "nginx|http://127.0.0.1:8090/protected/secret"
  "gateway|http://127.0.0.1:8080/protected/secret"
)

# runs hey once, prints: rps p50_ms p99_ms ok_pct
run_hey() {
  local url=$1 c=$2 n=$3
  taskset -c "$HEY_CPUS" hey -n "$n" -c "$c" -H "Authorization: Bearer $TOKEN" "$url" |
    # hey splits -n evenly across workers, so it sends floor(n/c)*c requests
    awk -v n="$(( n / c * c ))" '
      /Requests\/sec:/ { rps = $2 }
      # hey prints "50%% in" (a format-string bug), so allow either
      / 50%+ in/       { p50 = $3 * 1000 }
      / 99%+ in/       { p99 = $3 * 1000 }
      /\[200\]/        { ok = $2 }
      END { printf "%.0f %.2f %.2f %.2f\n", rps, p50, p99, 100 * ok / n }'
}

RESULTS="| Target | Concurrency | Req/s | p50 | p99 | Success |\n|---|---|---|---|---|---|\n"
for c in "${CONCURRENCIES[@]}"; do
  for target in "${TARGETS[@]}"; do
    label=${target%%|*}
    url=${target#*|}
    echo "== $label, c=$c: warmup + $RUNS runs of $REQUESTS" >&2
    run_hey "$url" "$c" 5000 >/dev/null
    runs=()
    for r in $(seq "$RUNS"); do
      line=$(run_hey "$url" "$c" "$REQUESTS")
      echo "   run $r: $line" >&2
      runs+=("$line")
    done
    # report the run with the median req/s
    median=$(printf '%s\n' "${runs[@]}" | sort -n | sed -n "$(( (RUNS + 1) / 2 ))p")
    read -r rps p50 p99 ok <<< "$median"
    RESULTS+="| $label | $c | $rps | ${p50}ms | ${p99}ms | ${ok}% |\n"
  done
done

echo "" >&2
printf "%b" "$RESULTS"
