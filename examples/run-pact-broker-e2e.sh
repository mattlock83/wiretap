#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARTIFACT_DIR="${ARTIFACT_DIR:-$ROOT_DIR/artifacts/pact-run-$(date +%s)-$RANDOM}"
PACT_CLI_IMAGE="${PACT_CLI_IMAGE:-pactfoundation/pact:0.9.4}"
COMPOSE_PROJECT="phrond-pact-${RANDOM}-${RANDOM}"
BUILD_DIR="$(mktemp -d "${TMPDIR:-/tmp}/phrond-pact.XXXXXXXX")"
COMPOSE=(docker compose --project-name "$COMPOSE_PROJECT" -f "$ROOT_DIR/examples/pact-broker/compose.yaml")

pick_port() {
  local candidate
  for _ in {1..100}; do
    candidate=$((20000 + RANDOM % 30000))
    if [[ " ${USED_PORTS:-} " == *" ${candidate} "* ]]; then
      continue
    fi
    if ! command -v lsof >/dev/null 2>&1 || ! lsof -iTCP:"${candidate}" -sTCP:LISTEN >/dev/null 2>&1; then
      echo "$candidate"
      return
    fi
  done
  echo "could not allocate a free local port" >&2
  return 1
}

USED_PORTS=""
PHROND_PORT="${PHROND_PORT:-$(pick_port)}"
USED_PORTS="${USED_PORTS} ${PHROND_PORT}"
PROXY_PORT="${PROXY_PORT:-$(pick_port)}"
USED_PORTS="${USED_PORTS} ${PROXY_PORT}"
ORDERS_PORT="${ORDERS_PORT:-$(pick_port)}"
USED_PORTS="${USED_PORTS} ${ORDERS_PORT}"
INVENTORY_PORT="${INVENTORY_PORT:-$(pick_port)}"
USED_PORTS="${USED_PORTS} ${INVENTORY_PORT}"
PACT_BROKER_PORT="${PACT_BROKER_PORT:-$(pick_port)}"

cleanup() {
  for pid in "${ORDERS_PID:-}" "${INVENTORY_PID:-}" "${PHROND_PID:-}"; do
    if [[ -n "$pid" ]]; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  if [[ "${BROKER_STARTED:-}" == "1" ]]; then
    "${COMPOSE[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  if [[ "$BUILD_DIR" == "${TMPDIR:-/tmp}/phrond-pact."* && -d "$BUILD_DIR" ]]; then
    rm -rf "$BUILD_DIR"
  fi
}
trap cleanup EXIT

if ! docker info >/dev/null 2>&1; then
  echo "A running Docker daemon is required for the official local Pact Broker and verifier." >&2
  exit 1
fi

mkdir -p "$ARTIFACT_DIR"
export PACT_BROKER_PORT
export PHROND_HTTP_LISTEN="127.0.0.1:${PHROND_PORT}"
export PHROND_PROXY_LISTEN="127.0.0.1:${PROXY_PORT}"
export PHROND_ARTIFACT_DIR="$ARTIFACT_DIR"
export PHROND_MAX_CAPTURE_BYTES=0
export PHROND_BASE_URL="http://${PHROND_HTTP_LISTEN}"
export ORDERS_LISTEN="127.0.0.1:${ORDERS_PORT}"
export ORDERS_BASE_URL="http://${ORDERS_LISTEN}"
# The verifier container accesses this independently owned service via the host gateway.
export INVENTORY_LISTEN="0.0.0.0:${INVENTORY_PORT}"
export INVENTORY_UPSTREAM_URL="http://127.0.0.1:${INVENTORY_PORT}"
export INVENTORY_BASE_URL="http://${PHROND_PROXY_LISTEN}/__phrond/dep/inventory"
PACT_BROKER_BASE_URL="http://127.0.0.1:${PACT_BROKER_PORT}"
BROKER_DOCKER_URL="http://pact-broker:9292"

wait_for() {
  local url="$1"
  local attempts="${2:-60}"
  for ((attempt = 0; attempt < attempts; attempt++)); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

pushd "$ROOT_DIR" >/dev/null

echo "Starting the official Pact Broker with its disposable local SQLite database..."
"${COMPOSE[@]}" up -d
BROKER_STARTED=1
if ! wait_for "${PACT_BROKER_BASE_URL}/" 120; then
  "${COMPOSE[@]}" logs pact-broker >&2 || true
  echo "Pact Broker failed to start at ${PACT_BROKER_BASE_URL}" >&2
  exit 1
fi

echo "Testing that the real Pact Broker imports and deduplicates exported Pact V3 contracts..."
PACT_BROKER_BASE_URL="$PACT_BROKER_BASE_URL" go test ./internal/coverage \
  -run '^TestPactBrokerImportsExportedContract$' -count=1 -v

go build -o "${BUILD_DIR}/phrond" ./cmd/phrond
go build -o "${BUILD_DIR}/orders-service" ./examples/demo-apps/pact-orders
go build -o "${BUILD_DIR}/inventory-service" ./examples/demo-apps/pact-inventory

"${BUILD_DIR}/phrond" >"${BUILD_DIR}/phrond.log" 2>&1 &
PHROND_PID=$!
"${BUILD_DIR}/inventory-service" >"${BUILD_DIR}/inventory.log" 2>&1 &
INVENTORY_PID=$!
"${BUILD_DIR}/orders-service" >"${BUILD_DIR}/orders.log" 2>&1 &
ORDERS_PID=$!

for service in "phrond:${PHROND_BASE_URL}" "inventory:${INVENTORY_UPSTREAM_URL}" "orders:${ORDERS_BASE_URL}"; do
  name="${service%%:*}"
  service_url="${service#*:}"
  if ! wait_for "${service_url}/healthz"; then
    echo "${name} service failed to start:" >&2
    tail -n 50 "${BUILD_DIR}/${name}.log" >&2 || true
    exit 1
  fi
done

echo "Running consumer tests against the independently owned orders and inventory services..."
node --test ./examples/pact-broker/consumer-test.js

go run ./cmd/phrond-coverage \
  --artifacts "$ARTIFACT_DIR" \
  --consumer-name orders-service \
  --provider-name inventory=inventory-service \
  --format pact \
  --output "${ARTIFACT_DIR}/pacts"

PACT_PATH="${ARTIFACT_DIR}/pacts/orders-service-inventory-service.json"
node --eval '
  const contract = require(process.argv[1]);
  if (contract.interactions.length !== 2) {
    throw new Error(`expected two deduplicated Pact interactions, got ${contract.interactions.length}`);
  }
  for (const interaction of contract.interactions) {
    if (Object.keys(interaction.response.body).sort().join(",") !== "available,productId") {
      throw new Error("the provider contract contains fields the consumer did not assert");
    }
  }
' "$PACT_PATH"

CONSUMER_VERSION="${CONSUMER_VERSION:-$(git rev-parse HEAD)}"
CONSUMER_BRANCH="${CONSUMER_BRANCH:-$(git branch --show-current)}"
PROVIDER_VERSION="${PROVIDER_VERSION:-$CONSUMER_VERSION}"

echo "Publishing the captured consumer contract with the official Pact CLI..."
docker run --rm \
  --network "${COMPOSE_PROJECT}_default" \
  --env PACT_DO_NOT_TRACK=true \
  --volume "${ARTIFACT_DIR}/pacts:/pacts:ro" \
  "$PACT_CLI_IMAGE" broker publish /pacts \
  --consumer-app-version "$CONSUMER_VERSION" \
  --branch "$CONSUMER_BRANCH" \
  --broker-base-url "$BROKER_DOCKER_URL"

echo "Fetching the published contract from the real Pact Broker..."
curl -fsS \
  "${PACT_BROKER_BASE_URL}/pacts/provider/inventory-service/consumer/orders-service/version/${CONSUMER_VERSION}" \
  -o "${ARTIFACT_DIR}/published-pact.json"

echo "Verifying inventory-service against its broker contract and publishing the provider result..."
docker run --rm \
  --network "${COMPOSE_PROJECT}_default" \
  --add-host host.docker.internal:host-gateway \
  --env PACT_DO_NOT_TRACK=true \
  "$PACT_CLI_IMAGE" verifier \
  --broker-url "$BROKER_DOCKER_URL" \
  --provider-name inventory-service \
  --hostname host.docker.internal \
  --port "$INVENTORY_PORT" \
  --state-change-url "http://host.docker.internal:${INVENTORY_PORT}/__pact/provider-states" \
  --filter-consumer orders-service \
  --publish \
  --provider-version "$PROVIDER_VERSION" \
  --provider-branch "$CONSUMER_BRANCH"

VERIFICATION_URL="$(node --eval '
  const contract = require(process.argv[1]);
  const verification = contract._links?.["pb:latest-verification-results"]?.href;
  if (!verification) throw new Error("the Pact Broker did not expose its verification-results link");
  process.stdout.write(verification);
' "${ARTIFACT_DIR}/published-pact.json")"
curl -fsS "$VERIFICATION_URL" -o "${ARTIFACT_DIR}/provider-verification.json"
node --eval '
  const result = require(process.argv[1]);
  if (result.success !== true) {
    throw new Error("the Pact Broker did not record a successful provider verification");
  }
  if (result.providerApplicationVersion !== process.argv[2]) {
    throw new Error("the Pact Broker recorded the wrong provider application version");
  }
' "${ARTIFACT_DIR}/provider-verification.json" "$PROVIDER_VERSION"

popd >/dev/null

echo "Orders/inventory capture artifacts: ${ARTIFACT_DIR}"
echo "Deduplicated Pact contract: ${PACT_PATH}"
echo "Broker-imported Pact: ${ARTIFACT_DIR}/published-pact.json"
echo "Published provider verification: ${ARTIFACT_DIR}/provider-verification.json"
echo "The official Pact verifier successfully published the provider verification result."
