#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="${BUILD_DIR:-$(mktemp -d)}"
LOG_FILE="${LOG_FILE:-/tmp/phrond-standalone-petstore.log}"
KEEP_RUNNING="${KEEP_RUNNING:-0}"

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
MOCK_PORT="${MOCK_PORT:-$(pick_unique_port "${USED_PORTS}")}"

cleanup() {
  if [[ "${KEEP_RUNNING}" == "1" ]]; then
    return
  fi
  if [[ -n "${PHROND_PID:-}" ]]; then
    kill "${PHROND_PID}" 2>/dev/null || true
  fi
  if [[ -n "${BUILD_DIR:-}" && -d "${BUILD_DIR}" ]]; then
    rm -rf "${BUILD_DIR}" || true
  fi
}
trap cleanup EXIT

export PHROND_MOCK_LISTEN="127.0.0.1:${MOCK_PORT}"

MOCK_BASE_URL="http://127.0.0.1:${MOCK_PORT}"

pushd "$ROOT_DIR" >/dev/null

GOFLAGS=${GOFLAGS:-}
go build ${GOFLAGS} -o "${BUILD_DIR}/phrond-core" ./cmd/phrond

nohup "${BUILD_DIR}/phrond-core" \
  --standalone \
  --standalone-templates "examples/standalone-petstore/templates" \
  --standalone-scenario-header "X-Phrond-Scenario" \
  > "${LOG_FILE}" 2>&1 &
PHROND_PID=$!

wait_for() {
  local url="$1"
  for _ in {1..50}; do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
  done
  return 1
}

if ! wait_for "${MOCK_BASE_URL}/pet/101"; then
  echo "Standalone Petstore failed to start. See ${LOG_FILE}"
  tail -n 80 "${LOG_FILE}" || true
  exit 1
fi

echo "Standalone Petstore is running:"
echo "  mock API: ${MOCK_BASE_URL}"

default_pet="$(curl -sS "${MOCK_BASE_URL}/pet/101")"
if ! printf '%s' "${default_pet}" | grep -Eq '"status"[[:space:]]*:[[:space:]]*"available"'; then
  echo "Unexpected default /pet/101 response:"
  echo "${default_pet}"
  exit 1
fi

not_found_body="$(mktemp)"
not_found_code="$(curl -sS -H "X-Phrond-Scenario: pet-not-found" \
  -o "${not_found_body}" -w "%{http_code}" "${MOCK_BASE_URL}/pet/101")"
if [[ "${not_found_code}" != "404" ]]; then
  echo "Expected 404 for scenario pet-not-found, got ${not_found_code}"
  cat "${not_found_body}"
  rm -f "${not_found_body}"
  exit 1
fi
if ! grep -q 'pet 101 not found' "${not_found_body}"; then
  echo "Unexpected not-found body:"
  cat "${not_found_body}"
  rm -f "${not_found_body}"
  exit 1
fi
rm -f "${not_found_body}"

available_list="$(curl -sS "${MOCK_BASE_URL}/pet/findByStatus?status=available")"
if ! printf '%s' "${available_list}" | grep -Eq '"status"[[:space:]]*:[[:space:]]*"available"'; then
  echo "Unexpected available list response:"
  echo "${available_list}"
  exit 1
fi

sold_list="$(curl -sS -H "X-Phrond-Scenario: pet-sold" \
  "${MOCK_BASE_URL}/pet/findByStatus?status=sold")"
if ! printf '%s' "${sold_list}" | grep -Eq '"status"[[:space:]]*:[[:space:]]*"sold"'; then
  echo "Unexpected sold list response:"
  echo "${sold_list}"
  exit 1
fi

inventory_default="$(curl -sS "${MOCK_BASE_URL}/store/inventory")"
if ! printf '%s' "${inventory_default}" | grep -Eq '"available"[[:space:]]*:[[:space:]]*21'; then
  echo "Unexpected default inventory response:"
  echo "${inventory_default}"
  exit 1
fi

inventory_low="$(curl -sS -H "X-Phrond-Scenario: inventory-low" \
  "${MOCK_BASE_URL}/store/inventory")"
if ! printf '%s' "${inventory_low}" | grep -Eq '"available"[[:space:]]*:[[:space:]]*2'; then
  echo "Unexpected low inventory response:"
  echo "${inventory_low}"
  exit 1
fi

echo "Standalone Petstore smoke checks passed."

if [[ "${KEEP_RUNNING}" == "1" ]]; then
  echo "KEEP_RUNNING=1 set, leaving phrond running (pid=${PHROND_PID})."
  echo "Press Ctrl+C to stop this script; phrond will continue running."
  while true; do
    sleep 3600
  done
fi

popd >/dev/null
