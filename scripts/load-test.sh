#!/usr/bin/env bash
# Fetches a fresh token, then runs a hey load test against /protected/secret.
#
# Usage:
#   ./scripts/load-test.sh [requests] [concurrency]        (fixed request count)
#   ./scripts/load-test.sh --duration 30s [concurrency]    (fixed time window)
set -euo pipefail

cd "$(dirname "$0")/.."

CONCURRENCY=50
DURATION=""
REQUESTS=2000

if [ "${1:-}" = "--duration" ]; then
  DURATION="${2:-30s}"
  CONCURRENCY="${3:-50}"
else
  REQUESTS="${1:-2000}"
  CONCURRENCY="${2:-50}"
fi

GATEWAY_URL="http://localhost:8080"

echo "fetching token..." >&2
TOKEN=$(./scripts/get-token.sh)

if [ -z "$TOKEN" ]; then
  echo "failed to get a token, aborting." >&2
  exit 1
fi

echo "" >&2
echo "curl sanity check immediately before hey runs:" >&2
curl -s -o /tmp/curl_check_body.txt -w "HTTP_STATUS:%{http_code}\n" -H "Authorization: Bearer $TOKEN" "$GATEWAY_URL/protected/secret" >&2
cat /tmp/curl_check_body.txt >&2
echo "" >&2

echo "" >&2
if [ -n "$DURATION" ]; then
  echo "running load test: $DURATION duration, $CONCURRENCY concurrency" >&2
  echo "" >&2
  hey -z "$DURATION" -c "$CONCURRENCY" -H "Authorization: Bearer $TOKEN" "$GATEWAY_URL/protected/secret"
else
  echo "running load test: $REQUESTS requests, $CONCURRENCY concurrency" >&2
  echo "" >&2
  hey -n "$REQUESTS" -c "$CONCURRENCY" -H "Authorization: Bearer $TOKEN" "$GATEWAY_URL/protected/secret"
fi
