#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
OUTPUT="${WIRETAP_GRPC_STREAMING_ARTIFACT_DIR:-${ROOT}/artifacts/grpc-streaming}"
mkdir -p "$OUTPUT"
OUTPUT="$(cd "$OUTPUT" && pwd)"
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
cp examples/grpc-streaming/fixture/stream.proto "$WORK/stream.proto"
cat > "$WORK/.wiretap.yaml" <<YAML
version: 1
http_listen: 127.0.0.1:${CONTROL_PORT}
proxy_listen: 127.0.0.1:0
output_dir: ${OUTPUT}
sut:
  id: stream-provider
  protocol: grpc
  url: http://127.0.0.1:${SUT_PORT}
  kind: service
  services: [wiretap.example.v1.Streams]
  schema:
    source: proto
    files: [stream.proto]
    import_paths: [./]
dependencies:
  chat:
    protocol: grpc
    url: http://127.0.0.1:${MOCK_PORT}
    kind: mock
    services: [wiretap.example.v1.Streams]
    schema:
      source: proto
      files: [stream.proto]
      import_paths: [./]
YAML

go build -o "$WORK/wiretap" ./cmd/wiretap
go build -o "$WORK/server" ./examples/grpc-streaming/server
"$WORK/wiretap" --config "$WORK/.wiretap.yaml" >"$WORK/wiretap.log" 2>&1 & WIRETAP_PID=$!
for _ in {1..100}; do
  if curl -fsS "http://127.0.0.1:${CONTROL_PORT}/healthz" >/dev/null 2>&1; then break; fi
  sleep 0.1
done
curl --fail-with-body -sS "http://127.0.0.1:${CONTROL_PORT}/v1/startRun" -H 'Content-Type: application/json' -d '{}' >"$WORK/start.json"
MOCK_CAPTURE_URL="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["dependencies"]["chat"])' "$WORK/start.json")"
"$WORK/server" --listen "127.0.0.1:${MOCK_PORT}" >"$WORK/mock.log" 2>&1 & MOCK_PID=$!
"$WORK/server" --listen "127.0.0.1:${SUT_PORT}" --dependency "$MOCK_CAPTURE_URL" >"$WORK/sut.log" 2>&1 & SUT_PID=$!
for target in "$MOCK_PORT" "$SUT_PORT"; do
  for _ in {1..100}; do
    if python3 -c 'import socket,sys;s=socket.socket();s.settimeout(.2);sys.exit(s.connect_ex(("127.0.0.1",int(sys.argv[1]))))' "$target"; then break; fi
    sleep 0.1
  done
done
WIRETAP_GRPC_STREAMING_EXAMPLE=1 WIRETAP_BASE_URL="http://127.0.0.1:${CONTROL_PORT}" go test -count=1 -v ./examples/grpc-streaming
curl --fail-with-body -sS "http://127.0.0.1:${CONTROL_PORT}/v1/finishRun" -H 'Content-Type: application/json' -d '{}' >"$WORK/finish.json"
echo "OKF bundles: $OUTPUT"
echo 'Open the latest run directory and docs/viewer.html to inspect the RPCs and their message timeline.'
