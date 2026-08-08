$ErrorActionPreference = "Stop"
$ProjectDir = Split-Path -Parent $PSScriptRoot
$env:PLAYWRIGHT_BROWSERS_PATH = "$ProjectDir\.playwright-browsers"
$env:CNKI_MCP_DATA_DIR = "$ProjectDir\.cnki-data"
& "$ProjectDir\.venv\Scripts\cnki-enhanced-mcp.exe" @args
