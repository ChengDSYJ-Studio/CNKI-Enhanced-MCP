#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
export PLAYWRIGHT_BROWSERS_PATH="$PROJECT_DIR/.playwright-browsers"
export CNKI_MCP_DATA_DIR="$PROJECT_DIR/.cnki-data"
exec "$PROJECT_DIR/.venv/bin/cnki-enhanced-mcp" "$@"
