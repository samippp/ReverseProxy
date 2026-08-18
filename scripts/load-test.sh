#!/usr/bin/env bash
# Fetches a fresh token, then runs a hey load test against /protected/secret.
# Usage: ./scripts/load-test.sh [requests] [concurrency]
set -euo pipefail

cd "$(dirname "$0")/.."

REQUESTS="${1:-2000}"
CONCURRENCY="${2:-50}"
GATEWAY_URL="http://localhost:8080"

echo "fetching token..." >&2
TOKEN=$(./scripts/get-token.sh)

echo "" >&2
echo "RAW TOKEN BYTES (first 20):" >&2
echo -n "$TOKEN" | head -c 20 | od -c >&2
echo "" >&2

if [ -z "$TOKEN" ]; then
  echo "failed to get a token, aborting." >&2
  exit 1
fi

echo "" >&2
echo "curl sanity check immediately before hey runs:" >&2
curl -s --trace-ascii /tmp/curl_trace.txt -o /tmp/curl_check_body.txt -w "HTTP_STATUS:%{http_code}\n" -H "Authorization: Bearer $TOKEN" "$GATEWAY_URL/protected/secret" >&2
cat /tmp/curl_check_body.txt >&2
echo "" >&2
echo "--- trace ---" >&2
cat /tmp/curl_trace.txt >&2
echo "--- end trace ---" >&2

echo "" >&2
echo "running load test: $REQUESTS requests, $CONCURRENCY concurrency" >&2
echo "" >&2

hey -n "$REQUESTS" -c "$CONCURRENCY" \
  -H "Authorization: Bearer $TOKEN" \
  "$GATEWAY_URL/protected/secret"
