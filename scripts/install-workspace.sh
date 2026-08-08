#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
exec "${CNKI_MCP_PYTHON:-python3}" "$PROJECT_DIR/install.py" --dev "$@"
