#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARTIFACT_DIR="${ARTIFACT_DIR:-$ROOT_DIR/artifacts}"

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
WIRETAP_PORT="${WIRETAP_PORT:-$(pick_unique_port "${USED_PORTS}")}"
USED_PORTS="${USED_PORTS} ${WIRETAP_PORT}"
PROXY_PORT="${PROXY_PORT:-$(pick_unique_port "${USED_PORTS}")}"
USED_PORTS="${USED_PORTS} ${PROXY_PORT}"
SUT_PORT="${SUT_PORT:-$(pick_unique_port "${USED_PORTS}")}"
USED_PORTS="${USED_PORTS} ${SUT_PORT}"
DEPENDENCY_PORT="${DEPENDENCY_PORT:-$(pick_unique_port "${USED_PORTS}")}"

BUILD_DIR="${BUILD_DIR:-$(mktemp -d)}"

cleanup() {
  if [[ -n "${WIRETAP_PID:-}" ]]; then
    kill "${WIRETAP_PID}" 2>/dev/null || true
    wait "${WIRETAP_PID}" 2>/dev/null || true
  fi
  if [[ -n "${SUT_PID:-}" ]]; then
    kill "${SUT_PID}" 2>/dev/null || true
    wait "${SUT_PID}" 2>/dev/null || true
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

export WIRETAP_HTTP_LISTEN="127.0.0.1:${WIRETAP_PORT}"
export WIRETAP_PROXY_LISTEN="127.0.0.1:${PROXY_PORT}"
export WIRETAP_OUTPUT_DIR="$ARTIFACT_DIR"
export WIRETAP_BASE_URL="http://127.0.0.1:${WIRETAP_PORT}"
# Keep generated example artifacts intentionally small.
export WIRETAP_MAX_CAPTURE_BYTES="${WIRETAP_MAX_CAPTURE_BYTES:-4096}"
export WIRETAP_RESULT_FILE="${BUILD_DIR}/finish.json"

export SUT_LISTEN="127.0.0.1:${SUT_PORT}"
export SUT_BASE_URL="http://${SUT_LISTEN}"
export DEPENDENCY_LISTEN="127.0.0.1:${DEPENDENCY_PORT}"
export DEPENDENCY_UPSTREAM_URL="http://${DEPENDENCY_LISTEN}"
export DEPENDENCY_BASE_URL="http://127.0.0.1:${PROXY_PORT}/__wiretap/dep/inventory-service"

pushd "$ROOT_DIR" >/dev/null

GOFLAGS=${GOFLAGS:-}
go build ${GOFLAGS} -o "${BUILD_DIR}/wiretap-runtime" ./cmd/wiretap
GOFLAGS=${GOFLAGS:-}
go build ${GOFLAGS} -o "${BUILD_DIR}/basic-rest-sut" ./examples/demo-apps/basic-rest/sut
GOFLAGS=${GOFLAGS:-}
go build ${GOFLAGS} -o "${BUILD_DIR}/basic-rest-dependency" ./examples/demo-apps/basic-rest/dependency

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

# The runner owns this dependency exactly as a consumer owns WireMock or
# another existing mock server. Wiretap never starts or configures it.
nohup "${BUILD_DIR}/basic-rest-dependency" > /tmp/wiretap-basic-rest-dependency.log 2>&1 &
DEPENDENCY_PID=$!

if ! wait_for "${DEPENDENCY_UPSTREAM_URL}/healthz"; then
  echo "Basic REST dependency failed to start. See /tmp/wiretap-basic-rest-dependency.log"
  tail -n 50 /tmp/wiretap-basic-rest-dependency.log || true
  exit 1
fi

# The runner owns the SUT lifecycle and configuration. The test suite only
# connects Wiretap's capture proxy to this already-running target and dependency.
nohup "${BUILD_DIR}/basic-rest-sut" > /tmp/wiretap-basic-rest-sut.log 2>&1 &
SUT_PID=$!

if ! wait_for "${SUT_BASE_URL}/healthz"; then
  echo "Basic REST SUT failed to start. See /tmp/wiretap-basic-rest-sut.log"
  tail -n 50 /tmp/wiretap-basic-rest-sut.log || true
  exit 1
fi

# Wiretap is another service in the existing harness. Test hooks control capture.
nohup "${BUILD_DIR}/wiretap-runtime" --config ./examples/basic-rest/.wiretap.yaml > /tmp/wiretap-basic-rest-runtime.log 2>&1 &
WIRETAP_PID=$!
if ! wait_for "${WIRETAP_BASE_URL}/healthz"; then
  echo "Wiretap failed to start. See /tmp/wiretap-basic-rest-runtime.log" >&2
  tail -n 50 /tmp/wiretap-basic-rest-runtime.log || true
  exit 1
fi

TEST_STATUS=0
if [[ "${WIRETAP_TEST_RUNNER:-node}" == "jest" ]]; then
  ./sdk/js/node_modules/.bin/jest --projects ./examples/basic-rest/jest.config.cjs || TEST_STATUS=$?
else
  node --require ./sdk/js/tracing.js --test ./examples/basic-rest/basic-test.js || TEST_STATUS=$?
fi

if [[ ! -f "$WIRETAP_RESULT_FILE" ]]; then
  if [[ "$TEST_STATUS" -ne 0 ]]; then exit "$TEST_STATUS"; fi
  echo "Suite finalization returned no artifact paths" >&2
  exit 1
fi
BUNDLE_DIR="$(node -e 'console.log(JSON.parse(require("node:fs").readFileSync(process.env.WIRETAP_RESULT_FILE)).bundle_path)')"
if [[ -z "${BUNDLE_DIR}" || ! -f "${BUNDLE_DIR}/bundle.json" ]]; then
  echo "Wiretap did not produce a bundle under ${ARTIFACT_DIR}" >&2
  exit 1
fi
# Suite finalization returns the exact archive and all selected export paths.
node -e 'const fs = require("node:fs"); const run = JSON.parse(fs.readFileSync(process.argv[1])); if (run.run_state !== "complete") throw new Error("test hooks did not finalize capture")' "${BUNDLE_DIR}/bundle.json"

SUMMARY_STATUS=0
"${BUILD_DIR}/wiretap-runtime" report --bundle "${BUNDLE_DIR}" --format text || SUMMARY_STATUS=$?
echo "Raw capture: ${BUNDLE_DIR}"
echo "JSON coverage: ${BUNDLE_DIR}/exports/coverage.json"
echo "Postman collections: ${BUNDLE_DIR}/exports/postman"
if [[ "${TEST_STATUS}" -ne 0 ]]; then
  exit "${TEST_STATUS}"
fi
if [[ "${SUMMARY_STATUS}" -ne 0 ]]; then
  exit "${SUMMARY_STATUS}"
fi

popd >/dev/null
