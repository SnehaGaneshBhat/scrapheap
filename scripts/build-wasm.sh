#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
goroot=$(go env GOROOT)
runtime="$goroot/lib/wasm/wasm_exec.js"
if [ ! -f "$runtime" ]; then
  runtime="$goroot/misc/wasm/wasm_exec.js"
fi
if [ ! -f "$runtime" ]; then
  echo "could not find wasm_exec.js in $goroot" >&2
  exit 1
fi

cp "$runtime" "$root/web/wasm_exec.js"
GOOS=js GOARCH=wasm go build -trimpath -o "$root/web/trainer.wasm" ./cmd/wasm
