#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
OUTPUT="${WIRETAP_GRPC_EXPLORATION_ARTIFACT_DIR:-${ROOT}/artifacts/grpc-exploration}"
CONTRACT_SOURCE="${WIRETAP_GRPC_CONTRACT_SOURCE:-reflection}"
mkdir -p "$OUTPUT"
OUTPUT="$(cd "$OUTPUT" && pwd)"
port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
CONTROL_PORT="$(port)"
SUT_PORT="$(port)"
cleanup() {
  local run_status=$?
  if [[ "$run_status" -ne 0 && -f "$WORK/wiretap.log" ]]; then cat "$WORK/wiretap.log" >&2; fi
  if [[ -n "${WIRETAP_PID:-}" ]]; then kill "$WIRETAP_PID" 2>/dev/null || true; wait "$WIRETAP_PID" 2>/dev/null || true; fi
  rm -rf "$WORK"
}
trap cleanup EXIT
cd "$ROOT"
SCHEMA=""
if [[ "$CONTRACT_SOURCE" == "descriptor_set" ]]; then
  go run ./examples/grpc-exploration/descriptor "$WORK/orders.protoset"
  SCHEMA=$'  schema:\n    source: descriptor_set\n    path: orders.protoset'
elif [[ "$CONTRACT_SOURCE" != "reflection" ]]; then
  echo "WIRETAP_GRPC_CONTRACT_SOURCE must be reflection or descriptor_set" >&2
  exit 2
fi
cat > "$WORK/.wiretap.yaml" <<YAML
version: 1
http_listen: 127.0.0.1:${CONTROL_PORT}
proxy_listen: 127.0.0.1:0
output_dir: ${OUTPUT}
sut:
  id: example-orders
  protocol: grpc
  url: http://127.0.0.1:${SUT_PORT}
  kind: service
  services: [wiretap.example.orders.v1.Orders]
${SCHEMA}
YAML
go build -o "$WORK/wiretap" ./cmd/wiretap
"$WORK/wiretap" --config "$WORK/.wiretap.yaml" >"$WORK/wiretap.log" 2>&1 & WIRETAP_PID=$!
for _ in {1..100}; do
  if curl -fsS "http://127.0.0.1:${CONTROL_PORT}/healthz" >/dev/null 2>&1; then break; fi
  sleep 0.1
done
curl --fail-with-body -sS "http://127.0.0.1:${CONTROL_PORT}/v1/startRun" -H 'Content-Type: application/json' -d '{}' >"$WORK/start.json"
WIRETAP_GRPC_EXPLORATION_EXAMPLE=1 \
WIRETAP_BASE_URL="http://127.0.0.1:${CONTROL_PORT}" \
WIRETAP_GRPC_CONTRACT_SOURCE="$CONTRACT_SOURCE" \
WIRETAP_GRPC_EXPLORATION_SUT_ADDR="127.0.0.1:${SUT_PORT}" \
go test -count=1 -v ./examples/grpc-exploration
curl --fail-with-body -sS "http://127.0.0.1:${CONTROL_PORT}/v1/finishRun" -H 'Content-Type: application/json' -d '{}' >"$WORK/finish.json"
BUNDLE="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["bundle_path"])' "$WORK/start.json")"
"$WORK/wiretap" report --bundle "$BUNDLE"
echo "OKF bundle: $BUNDLE"
echo "Viewer: $BUNDLE/docs/viewer.html"
