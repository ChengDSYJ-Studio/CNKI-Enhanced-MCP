#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
CNKI_GO_BIN="${CNKI_GO:-go}"
"$CNKI_GO_BIN" test -race ./...
"$CNKI_GO_BIN" vet ./...
for target in darwin/amd64 windows/amd64 linux/amd64 linux/arm64; do
  GOOS=${target%/*} GOARCH=${target#*/} CGO_ENABLED=0 "$CNKI_GO_BIN" build -o /dev/null ./cmd/cnki-mcp
done
mkdir -p bin
CNKI_DATA_DIR=$(mktemp -d)
export CNKI_DATA_DIR
trap 'rm -rf "$CNKI_DATA_DIR"' EXIT
"$CNKI_GO_BIN" build -o bin/cnki-mcp ./cmd/cnki-mcp
bin/cnki-mcp doctor --protocol
bin/cnki-mcp schema > docs/tool-schema.json
python3 scripts/check_sessions.py bin/cnki-mcp
