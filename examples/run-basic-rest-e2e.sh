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
SUT_PORT="${SUT_PORT:-$(pick_unique_port "${USED_PORTS}")}"
USED_PORTS="${USED_PORTS} ${SUT_PORT}"
DEPENDENCY_PORT="${DEPENDENCY_PORT:-$(pick_unique_port "${USED_PORTS}")}"

BUILD_DIR="${BUILD_DIR:-$(mktemp -d)}"

cleanup() {
  if [[ -n "${SUT_PID:-}" ]]; then
    kill "${SUT_PID}" 2>/dev/null || true
    wait "${SUT_PID}" 2>/dev/null || true
  fi
  if [[ -n "${PHROND_PID:-}" ]]; then
    kill "${PHROND_PID}" 2>/dev/null || true
    wait "${PHROND_PID}" 2>/dev/null || true
  fi
  if [[ -n "${DEPENDENCY_PID:-}" ]]; then
    kill "${DEPENDENCY_PID}" 2>/dev/null || true
    wait "${DEPENDENCY_PID}" 2>/dev/null || true
  fi
  if [[ -n "${BUILD_DIR:-}" && -d "${BUILD_DIR}" ]]; then
    rm -rf "${BUILD_DIR}" || true
  fi
}
trap cleanup EXIT

mkdir -p "$ARTIFACT_DIR"

export PHROND_HTTP_LISTEN="127.0.0.1:${PHROND_PORT}"
export PHROND_PROXY_LISTEN="127.0.0.1:${PROXY_PORT}"
export PHROND_ARTIFACT_DIR="$ARTIFACT_DIR"
export PHROND_BASE_URL="http://127.0.0.1:${PHROND_PORT}"
if [[ -n "${PHROND_GENERATE_SCHEMA:-}" ]]; then
  export PHROND_GENERATED_SCHEMA_PATH="${PHROND_GENERATED_SCHEMA_PATH:-${ARTIFACT_DIR}/basic-rest.generated.openapi.yaml}"
fi
# Keep generated example artifacts intentionally small.
export PHROND_MAX_CAPTURE_BYTES="${PHROND_MAX_CAPTURE_BYTES:-64}"

export SUT_LISTEN="127.0.0.1:${SUT_PORT}"
export SUT_BASE_URL="http://${SUT_LISTEN}"
export DEPENDENCY_LISTEN="127.0.0.1:${DEPENDENCY_PORT}"
export DEPENDENCY_UPSTREAM_URL="http://${DEPENDENCY_LISTEN}"
export DEPENDENCY_BASE_URL="http://127.0.0.1:${PROXY_PORT}/__phrond/dep/inventory-service"

pushd "$ROOT_DIR" >/dev/null

GOFLAGS=${GOFLAGS:-}
go build ${GOFLAGS} -o "${BUILD_DIR}/phrond-core" ./cmd/phrond
GOFLAGS=${GOFLAGS:-}
go build ${GOFLAGS} -o "${BUILD_DIR}/basic-rest-sut" ./examples/demo-apps/basic-rest/sut
GOFLAGS=${GOFLAGS:-}
go build ${GOFLAGS} -o "${BUILD_DIR}/basic-rest-dependency" ./examples/demo-apps/basic-rest/dependency

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

# The runner owns this dependency exactly as a consumer owns WireMock or
# another existing mock server. Phrond never starts or configures it.
nohup "${BUILD_DIR}/basic-rest-dependency" > /tmp/phrond-basic-rest-dependency.log 2>&1 &
DEPENDENCY_PID=$!

if ! wait_for "${DEPENDENCY_UPSTREAM_URL}/healthz"; then
  echo "Basic REST dependency failed to start. See /tmp/phrond-basic-rest-dependency.log"
  tail -n 50 /tmp/phrond-basic-rest-dependency.log || true
  exit 1
fi

# The runner owns the SUT lifecycle and configuration. The test suite only
# connects Phrond's capture proxy to this already-running target and dependency.
nohup "${BUILD_DIR}/basic-rest-sut" > /tmp/phrond-basic-rest-sut.log 2>&1 &
SUT_PID=$!

if ! wait_for "${SUT_BASE_URL}/healthz"; then
  echo "Basic REST SUT failed to start. See /tmp/phrond-basic-rest-sut.log"
  tail -n 50 /tmp/phrond-basic-rest-sut.log || true
  exit 1
fi

node --test ./examples/basic-rest/basic-test.js

go run ./cmd/phrond-coverage \
  --artifacts "${ARTIFACT_DIR}" \
  --sut-name "Basic REST SUT" \
  --format workflow \
  --output "${ARTIFACT_DIR}/coverage.workflow.yaml"

go run ./cmd/phrond-coverage \
  --artifacts "${ARTIFACT_DIR}" \
  --sut-name "Basic REST SUT" \
  --format postman \
  --output "${ARTIFACT_DIR}/coverage.postman_collection.json"

go run ./cmd/phrond-coverage \
  --artifacts "${ARTIFACT_DIR}" \
  --sut-name "Basic REST SUT" \
  --format html \
  --output "${ARTIFACT_DIR}/coverage.html"

popd >/dev/null

echo "Artifacts written to ${ARTIFACT_DIR}"
echo "Coverage report (Workflow): ${ARTIFACT_DIR}/coverage.workflow.yaml"
echo "Coverage report (Postman): ${ARTIFACT_DIR}/coverage.postman_collection.json"
echo "Coverage report (HTML): ${ARTIFACT_DIR}/coverage.html"
if [[ -n "${PHROND_GENERATE_SCHEMA:-}" ]]; then
  echo "Generated OpenAPI: ${PHROND_GENERATED_SCHEMA_PATH}"
fi
