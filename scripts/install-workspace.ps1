$ErrorActionPreference = "Stop"
$ProjectDir = Split-Path -Parent $PSScriptRoot

if (Get-Command py -ErrorAction SilentlyContinue) {
    & py "$ProjectDir\install.py" @args
} elseif (Get-Command python -ErrorAction SilentlyContinue) {
    & python "$ProjectDir\install.py" @args
} else {
    throw "Python 3.11 or newer was not found. Install Python and retry."
}
