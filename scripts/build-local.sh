#!/bin/bash
set -euo pipefail
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
if [[ "$(uname -s)" != "Darwin" || "$(uname -m)" != "arm64" ]]; then
  printf '%s\n' 'This first distribution targets native macOS Apple Silicon.' >&2
  exit 1
fi
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=1 GOOS=darwin GOARCH=arm64
go run -mod=vendor ./tools/sourceaudit -root .
mkdir -p dist
go build -mod=vendor -trimpath \
  -tags 'embed_inject,sqlite_only,embed_frontend_inject' \
  -ldflags='-s -w -X main.Mode=release' \
  -o dist/wxc .
file dist/wxc
shasum -a 256 dist/wxc
