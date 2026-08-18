#!/usr/bin/env bash
# Starts the 3 dummy backends + the gateway for local load testing.
# Ctrl+C stops everything cleanly.
set -euo pipefail

cd "$(dirname "$0")/.."

echo "starting dummy backends..."
go run ./cmd/dummybackend -port=9001 -name=backend-1 &
BACKEND1_PID=$!
go run ./cmd/dummybackend -port=9002 -name=backend-2 &
BACKEND2_PID=$!
go run ./cmd/dummybackend -port=9003 -name=backend-3 &
BACKEND3_PID=$!

# give the backends a moment to actually bind their ports before the
# gateway starts routing to them
sleep 1

echo "starting gateway..."
go run ./cmd/gateway &
GATEWAY_PID=$!

cleanup() {
  echo ""
  echo "shutting down..."
  kill "$BACKEND1_PID" "$BACKEND2_PID" "$BACKEND3_PID" "$GATEWAY_PID" 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "all processes started. gateway on :8080, backends on :9001-9003."
echo "press Ctrl+C to stop everything."

wait
