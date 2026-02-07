#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROTO_FILE="${PROTO_FILE:-$ROOT_DIR/examplepb/example.proto}"
OUT_FILE="${OUT_FILE:-$ROOT_DIR/examplepb/example.desc}"
IMPORT_PATHS="${IMPORT_PATHS:-$ROOT_DIR/examplepb}"

if ! command -v protoc >/dev/null 2>&1; then
  echo "protoc is required to generate descriptor sets" >&2
  exit 1
fi

ARGS=()
IFS=',' read -r -a PATHS <<< "$IMPORT_PATHS"
for path in "${PATHS[@]}"; do
  path="$(echo "$path" | xargs)"
  if [[ -n "$path" ]]; then
    ARGS+=("--proto_path=$path")
  fi
done

ARGS+=("--descriptor_set_out=$OUT_FILE")
ARGS+=("--include_imports")
ARGS+=("$PROTO_FILE")

protoc "${ARGS[@]}"

echo "Wrote descriptor set to $OUT_FILE"
