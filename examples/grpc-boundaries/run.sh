#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
OUTPUT="${WIRETAP_GRPC_ARTIFACT_DIR:-${ROOT}/artifacts/grpc-boundaries}"
CONTRACT_SOURCE="${WIRETAP_GRPC_CONTRACT_SOURCE:-reflection}"
mkdir -p "$OUTPUT"

port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
CONTROL_PORT="$(port)"
SUT_PORT="$(port)"
MOCK_PORT="$(port)"

cleanup() {
  for pid in "${SUT_PID:-}" "${MOCK_PID:-}" "${WIRETAP_PID:-}"; do
    if [[ -n "$pid" ]]; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
  done
  rm -rf "$WORK"
}
trap cleanup EXIT

cd "$ROOT"
SUT_SCHEMA=""
DEPENDENCY_SCHEMA=""
SUT_REFLECTION="--reflection=true"
MOCK_REFLECTION="--reflection=true"
if [[ "$CONTRACT_SOURCE" == "descriptor_set" ]]; then
  go build -o "$WORK/emit-descriptor" ./examples/grpc-boundaries/descriptor
  "$WORK/emit-descriptor" "$WORK/health.protoset"
  SUT_SCHEMA=$'  schema:\n    source: descriptor_set\n    path: health.protoset'
  DEPENDENCY_SCHEMA=$'    schema:\n      source: descriptor_set\n      path: health.protoset'
  SUT_REFLECTION="--reflection=false"
  MOCK_REFLECTION="--reflection=false"
elif [[ "$CONTRACT_SOURCE" == "proto" || "$CONTRACT_SOURCE" == "mixed" ]]; then
  cp -R "$ROOT/examples/grpc-boundaries/proto" "$WORK/proto"
  DEPENDENCY_SCHEMA=$'    schema:\n      source: proto\n      files: [health.proto]\n      import_paths: [./proto]'
  MOCK_REFLECTION="--reflection=false"
  if [[ "$CONTRACT_SOURCE" == "proto" ]]; then
    SUT_SCHEMA=$'  schema:\n    source: proto\n    files: [health.proto]\n    import_paths: [./proto]'
    SUT_REFLECTION="--reflection=false"
  fi
elif [[ "$CONTRACT_SOURCE" != "reflection" ]]; then
  echo "WIRETAP_GRPC_CONTRACT_SOURCE must be reflection, proto, descriptor_set or mixed" >&2
  exit 2
fi

cat > "$WORK/.wiretap.yaml" <<YAML
version: 1
http_listen: 127.0.0.1:${CONTROL_PORT}
proxy_listen: 127.0.0.1:0
output_dir: ${OUTPUT}
sut:
  id: example-sut
  protocol: grpc
  url: http://127.0.0.1:${SUT_PORT}
  kind: service
  services: [grpc.health.v1.Health]
${SUT_SCHEMA}
dependencies:
  inventory:
    protocol: grpc
    url: http://127.0.0.1:${MOCK_PORT}
    kind: mock
    services: [grpc.health.v1.Health]
${DEPENDENCY_SCHEMA}
YAML

go build -o "$WORK/wiretap" ./cmd/wiretap
go build -o "$WORK/server" ./examples/grpc-boundaries/server

"$WORK/wiretap" --config "$WORK/.wiretap.yaml" >"$WORK/wiretap.log" 2>&1 & WIRETAP_PID=$!
for _ in {1..100}; do
  if curl -fsS "http://127.0.0.1:${CONTROL_PORT}/healthz" >/dev/null 2>&1; then break; fi
  sleep 0.1
done
curl --fail-with-body -sS "http://127.0.0.1:${CONTROL_PORT}/v1/startRun" -H 'Content-Type: application/json' -d '{}' >"$WORK/start.json"

MOCK_CAPTURE_URL="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["dependencies"]["inventory"])' "$WORK/start.json")"
"$WORK/server" --listen "127.0.0.1:${MOCK_PORT}" "$MOCK_REFLECTION" >"$WORK/mock.log" 2>&1 & MOCK_PID=$!
"$WORK/server" --listen "127.0.0.1:${SUT_PORT}" --dependency "$MOCK_CAPTURE_URL" "$SUT_REFLECTION" >"$WORK/sut.log" 2>&1 & SUT_PID=$!
for target in "$MOCK_PORT" "$SUT_PORT"; do
  for _ in {1..100}; do
    if python3 -c 'import socket,sys;s=socket.socket();s.settimeout(.2);sys.exit(s.connect_ex(("127.0.0.1",int(sys.argv[1]))))' "$target"; then break; fi
    sleep 0.1
  done
done

WIRETAP_GRPC_EXAMPLE=1 WIRETAP_BASE_URL="http://127.0.0.1:${CONTROL_PORT}" go test -count=1 ./examples/grpc-boundaries
curl --fail-with-body -sS "http://127.0.0.1:${CONTROL_PORT}/v1/finishRun" -H 'Content-Type: application/json' -d '{}' >"$WORK/finish.json"
CAPTURE_DIR="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["bundle_path"])' "$WORK/finish.json")"
"$WORK/wiretap" report --bundle "$CAPTURE_DIR" --format json --output "$CAPTURE_DIR/coverage.json"
echo "Raw capture: $CAPTURE_DIR"
echo "JSON coverage: $CAPTURE_DIR/coverage.json"
