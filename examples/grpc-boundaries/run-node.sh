#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
EXAMPLE="$ROOT/examples/grpc-boundaries/node"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
if [[ ! -d "$EXAMPLE/node_modules/@grpc/grpc-js" || ! -d "$ROOT/sdk/js/node_modules/@opentelemetry/api" ]]; then
  echo 'Install example dependencies first: npm ci --prefix sdk/js && npm ci --prefix examples/grpc-boundaries/node' >&2
  exit 2
fi
cd "$ROOT"
go build -o "$WORK/wiretap" ./cmd/wiretap
go build -o "$WORK/go-server" ./examples/grpc-boundaries/server
# A fresh local CA/certificate is used only by this fixture. No private key is bundled.
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=localhost' \
  -addext 'subjectAltName=DNS:localhost,IP:127.0.0.1' \
  -keyout "$WORK/tls.key" -out "$WORK/tls.crt" >"$WORK/openssl.log" 2>&1
export WIRETAP_EXAMPLE_BINARY="$WORK/wiretap"
export WIRETAP_EXAMPLE_GO_SERVER="$WORK/go-server"
export WIRETAP_EXAMPLE_TLS_DIR="$WORK"
export WIRETAP_GRPC_ARTIFACT_DIR="${WIRETAP_GRPC_ARTIFACT_DIR:-$ROOT/artifacts/grpc-node}"
cd "$EXAMPLE"
npm test
echo "Raw captures and JSON coverage: $WIRETAP_GRPC_ARTIFACT_DIR"
