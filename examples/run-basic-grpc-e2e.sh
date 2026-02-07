#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARTIFACT_DIR="${ARTIFACT_DIR:-$ROOT_DIR/artifacts/run-$(date +%s)-$RANDOM}"

pick_port() {
  local base=20000
  local span=30000
  if command -v lsof >/dev/null 2>&1; then
    for _ in {1..60}; do
      local port=$((base + (RANDOM % span)))
      if ! lsof -iTCP:"${port}" -sTCP:LISTEN >/dev/null 2>&1; then
        echo "${port}"
        return
      fi
    done
  fi
  echo $((base + (RANDOM % span)))
}

pick_unique_port() {
  local used="$1"
  local port
  while true; do
    port="$(pick_port)"
    if [[ " ${used} " != *" ${port} "* ]]; then
      echo "${port}"
      return
    fi
  done
}

USED_PORTS=""
PHROND_PORT="${PHROND_PORT:-$(pick_unique_port "${USED_PORTS}")}"
USED_PORTS="${USED_PORTS} ${PHROND_PORT}"
PROXY_PORT="${PROXY_PORT:-$(pick_unique_port "${USED_PORTS}")}"
USED_PORTS="${USED_PORTS} ${PROXY_PORT}"
MOCK_PORT="${MOCK_PORT:-$(pick_unique_port "${USED_PORTS}")}"
USED_PORTS="${USED_PORTS} ${MOCK_PORT}"
GRPC_PROXY_PORT="${GRPC_PROXY_PORT:-$(pick_unique_port "${USED_PORTS}")}"
USED_PORTS="${USED_PORTS} ${GRPC_PROXY_PORT}"
GRPC_MOCK_PORT="${GRPC_MOCK_PORT:-$(pick_unique_port "${USED_PORTS}")}"
USED_PORTS="${USED_PORTS} ${GRPC_MOCK_PORT}"
SUT_PORT="${SUT_PORT:-$(pick_unique_port "${USED_PORTS}")}"

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
export PHROND_PROXY_LISTEN="127.0.0.1:${PROXY_PORT}"
export PHROND_MOCK_LISTEN="127.0.0.1:${MOCK_PORT}"
export PHROND_GRPC_PROXY_LISTEN="127.0.0.1:${GRPC_PROXY_PORT}"
export PHROND_GRPC_PROXY_INSECURE="true"
export PHROND_GRPC_MOCK_LISTEN="127.0.0.1:${GRPC_MOCK_PORT}"
export PHROND_ARTIFACT_DIR="$ARTIFACT_DIR"
export PHROND_BASE_URL="http://127.0.0.1:${PHROND_PORT}"

export SUT_LISTEN="127.0.0.1:${SUT_PORT}"
export DEPENDENCY_ADDR="127.0.0.1:${GRPC_MOCK_PORT}"
export PHROND_GRPC_PROXY_TARGET="${SUT_LISTEN}"

pushd "$ROOT_DIR" >/dev/null

GOFLAGS=${GOFLAGS:-}
go build ${GOFLAGS} -o "${BUILD_DIR}/phrond-core" ./cmd/phrond
GOFLAGS=${GOFLAGS:-}
go build ${GOFLAGS} -o "${BUILD_DIR}/basic-grpc-sut" ./examples/demo-apps/basic-grpc/sut

nohup "${BUILD_DIR}/phrond-core" > /tmp/phrond-core.log 2>&1 &
PHROND_PID=$!

wait_for() {
  local url="$1"
  for _ in {1..40}; do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
  done
  return 1
}

if ! wait_for "http://127.0.0.1:${PHROND_PORT}/healthz"; then
  echo "Core failed to start. See /tmp/phrond-core.log"
  tail -n 50 /tmp/phrond-core.log || true
  exit 1
fi

export SUT_BINARY="${BUILD_DIR}/basic-grpc-sut"
go test ./examples/basic-grpc -run TestBasicGRPC -v

go run ./cmd/phrond-coverage \
  --artifacts "${ARTIFACT_DIR}" \
  --sut-name "Basic gRPC SUT" \
  --format yaml \
  --output "${ARTIFACT_DIR}/coverage.yaml"

go run ./cmd/phrond-coverage \
  --artifacts "${ARTIFACT_DIR}" \
  --sut-name "Basic gRPC SUT" \
  --format html \
  --output "${ARTIFACT_DIR}/coverage.html"

popd >/dev/null

echo "Artifacts written to ${ARTIFACT_DIR}"
echo "Coverage report (YAML): ${ARTIFACT_DIR}/coverage.yaml"
echo "Coverage report (HTML): ${ARTIFACT_DIR}/coverage.html"
