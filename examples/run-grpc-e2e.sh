#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARTIFACT_DIR="${ARTIFACT_DIR:-$ROOT_DIR/artifacts}"

pick_port() {
  node -e 'const net=require("net");const s=net.createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close();});'
}

PHROND_PORT="${PHROND_PORT:-$(pick_port)}"
SUT_PORT="${SUT_PORT:-$(pick_port)}"
GRPC_PROXY_PORT="${GRPC_PROXY_PORT:-$(pick_port)}"
GRPC_MOCK_PORT="${GRPC_MOCK_PORT:-$(pick_port)}"

BUILD_DIR="${BUILD_DIR:-$(mktemp -d)}"

cleanup() {
  if [[ -n "${PHROND_PID:-}" ]]; then
    kill "${PHROND_PID}" 2>/dev/null || true
  fi
  if [[ -n "${BUILD_DIR:-}" && -d "${BUILD_DIR}" ]]; then
    rm -rf "${BUILD_DIR}" || true
  fi
}
trap cleanup EXIT

mkdir -p "$ARTIFACT_DIR"

export PHROND_HTTP_LISTEN="127.0.0.1:${PHROND_PORT}"
export PHROND_GRPC_PROXY_LISTEN="127.0.0.1:${GRPC_PROXY_PORT}"
export PHROND_GRPC_MOCK_LISTEN="127.0.0.1:${GRPC_MOCK_PORT}"
export PHROND_ARTIFACT_DIR="$ARTIFACT_DIR"
export PHROND_BASE_URL="http://127.0.0.1:${PHROND_PORT}"

export SUT_LISTEN="127.0.0.1:${SUT_PORT}"
export DEPENDENCY_ADDR="127.0.0.1:${GRPC_MOCK_PORT}"

pushd "$ROOT_DIR" >/dev/null

# Build binaries
GOFLAGS=${GOFLAGS:-}
go build ${GOFLAGS} -o "${BUILD_DIR}/phrond-core" ./cmd/phrond
GOFLAGS=${GOFLAGS:-}
go build ${GOFLAGS} -o "${BUILD_DIR}/phrond-grpc-sut" ./examples/grpc/sut

# Start recorder core
nohup "${BUILD_DIR}/phrond-core" > /tmp/phrond-core.log 2>&1 &
PHROND_PID=$!

wait_for_http() {
  local url="$1"
  for i in {1..40}; do
    if curl -kfsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
  done
  return 1
}

if ! wait_for_http "http://127.0.0.1:${PHROND_PORT}/healthz"; then
  echo "Core failed to start. See /tmp/phrond-core.log"
  tail -n 50 /tmp/phrond-core.log || true
  exit 1
fi

# Run Go example (starts its own SUT using the built binary)
export SUT_BINARY="${BUILD_DIR}/phrond-grpc-sut"
go run ./examples/grpc/client

popd >/dev/null

echo "Artifacts written to ${ARTIFACT_DIR}"
