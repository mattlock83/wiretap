#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARTIFACT_DIR="${ARTIFACT_DIR:-$ROOT_DIR/artifacts}"

PHROND_PORT="${PHROND_PORT:-18000}"
SUT_PORT="${SUT_PORT:-18090}"
PROXY_PORT="${PROXY_PORT:-18080}"
MOCK_PORT="${MOCK_PORT:-19090}"

cleanup() {
  if [[ -n "${SUT_PID:-}" ]]; then
    kill "${SUT_PID}" 2>/dev/null || true
  fi
  if [[ -n "${PHROND_PID:-}" ]]; then
    kill "${PHROND_PID}" 2>/dev/null || true
  fi
}
trap cleanup EXIT

mkdir -p "$ARTIFACT_DIR"

export PHROND_HTTP_LISTEN="127.0.0.1:${PHROND_PORT}"
export PHROND_PROXY_LISTEN="127.0.0.1:${PROXY_PORT}"
export PHROND_MOCK_LISTEN="127.0.0.1:${MOCK_PORT}"
export PHROND_ARTIFACT_DIR="$ARTIFACT_DIR"

export SUT_LISTEN="127.0.0.1:${SUT_PORT}"
export DEPENDENCY_BASE_URL="http://127.0.0.1:${MOCK_PORT}"

pushd "$ROOT_DIR" >/dev/null

# Start recorder core
nohup go run ./cmd/phrond > /tmp/phrond-core.log 2>&1 &
PHROND_PID=$!

# Start SUT
nohup go run ./examples/system/sut > /tmp/phrond-sut.log 2>&1 &
SUT_PID=$!

# Wait for SUT health
for i in {1..30}; do
  if curl -fsS "http://127.0.0.1:${SUT_PORT}/healthz" >/dev/null; then
    break
  fi
  sleep 0.2
done

# Run JS example (uses SDK to drive the test case)
node ./examples/js/basic-test.js

popd >/dev/null

echo "Artifacts written to ${ARTIFACT_DIR}"
