#!/usr/bin/env bash
# Registers (or reuses) a load-test user, logs in, and prints the JWT.
# Usage: ./scripts/get-token.sh
set -euo pipefail

GATEWAY_URL="http://localhost:8080"
USERNAME="loadtest"
PASSWORD="loadtest123"
EMAIL="loadtest@example.com"

echo "registering user (ignore error if already exists)..." >&2
curl -s -m 5 -X POST "$GATEWAY_URL/register" \
  -H "Content-Type: application/json" \
  -d "{\"username\":\"$USERNAME\",\"password\":\"$PASSWORD\",\"email\":\"$EMAIL\"}" \
  >/dev/null || true

echo "logging in..." >&2
LOGIN_RESPONSE=$(curl -s -m 5 -X POST "$GATEWAY_URL/login" \
  -H "Content-Type: application/json" \
  -d "{\"username\":\"$USERNAME\",\"password\":\"$PASSWORD\"}")

TOKEN=$(echo "$LOGIN_RESPONSE" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

if [ -z "$TOKEN" ]; then
  echo "failed to get token. login response was:" >&2
  echo "$LOGIN_RESPONSE" >&2
  exit 1
fi

echo "token acquired." >&2
echo "" >&2
echo "sanity check:" >&2
# bounded and non-fatal, so a stuck backend can't hang or abort token fetching
curl -s -m 5 -H "Authorization: Bearer $TOKEN" "$GATEWAY_URL/protected/secret" >&2 \
  || echo "(sanity check failed; token is still valid)" >&2
echo "" >&2
echo "" >&2

# print just the raw token on stdout, so it can be captured with:
#   TOKEN=$(./scripts/get-token.sh)
echo "$TOKEN"
