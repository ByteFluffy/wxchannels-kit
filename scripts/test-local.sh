#!/bin/bash
set -euo pipefail
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=1
TAGS='embed_inject,sqlite_only,embed_frontend_inject'
# Compile every retained source package without invoking the unrelated browser
# corpus/live-site tests inherited from the original full source baseline.
go test -mod=vendor -tags "$TAGS" -run '^$' ./...
go test -mod=vendor -tags "$TAGS" -count=1 -timeout=120s \
  ./cmd ./internal/mcpserver ./internal/mediaverify ./internal/services \
  ./internal/application ./internal/config ./internal/interceptor ./internal/api \
  ./pkg/certificate ./pkg/system ./pkg/scraper/wxchannels
./scripts/build-local.sh
WXC_TEST_BINARY="$ROOT/dist/wxc" go test -mod=vendor -tags "$TAGS" \
  -count=1 -v -timeout=120s ./tests/integration
