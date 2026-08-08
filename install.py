#!/usr/bin/env python3
"""Cross-platform workspace-isolated installer for CNKI-Enhanced-MCP."""

from __future__ import annotations

import argparse
import json
import os
import platform
import shutil
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parent
VENV = ROOT / ".venv"
DATA = ROOT / ".cnki-data"
BROWSERS = ROOT / ".playwright-browsers"


def run(command: list[str], *, env: dict[str, str] | None = None) -> None:
    print("+", " ".join(command))
    subprocess.run(command, cwd=ROOT, env=env, check=True)


def venv_python() -> Path:
    return VENV / ("Scripts/python.exe" if os.name == "nt" else "bin/python")


def server_executable() -> Path:
    return VENV / ("Scripts/cnki-enhanced-mcp.exe" if os.name == "nt" else "bin/cnki-enhanced-mcp")


def main() -> None:
    parser = argparse.ArgumentParser(description="Install CNKI-Enhanced-MCP entirely inside this project")
    parser.add_argument("--python", default=None, help="Python executable or uv Python version (default: current Python)")
    parser.add_argument("--dev", action="store_true", help="also install test dependencies")
    parser.add_argument(
        "--with-system-deps",
        action="store_true",
        help="on Linux, ask Playwright to install Chromium OS packages (may require sudo)",
    )
    args = parser.parse_args()
    if sys.version_info < (3, 11):
        raise SystemExit("Python 3.11 or newer is required")

    DATA.mkdir(parents=True, exist_ok=True)
    BROWSERS.mkdir(parents=True, exist_ok=True)
    environment = os.environ.copy()
    environment["PLAYWRIGHT_BROWSERS_PATH"] = str(BROWSERS)
    environment["CNKI_MCP_DATA_DIR"] = str(DATA)
    environment["UV_CACHE_DIR"] = str(DATA / "uv-cache")
    package = f"{ROOT}[dev]" if args.dev else str(ROOT)
    uv = shutil.which("uv")
    existing_python = venv_python()
    if existing_python.exists():
        print(f"Reusing existing virtual environment: {VENV}")
        if args.python:
            print("Note: --python only applies when .venv is first created.")
    elif uv:
        run([uv, "venv", "--python", args.python or sys.executable, str(VENV)], env=environment)
    else:
        creator = args.python or sys.executable
        run([creator, "-m", "venv", str(VENV)], env=environment)

    if uv:
        run([uv, "pip", "install", "--python", str(venv_python()), "-e", package], env=environment)
    else:
        run([str(venv_python()), "-m", "pip", "install", "--upgrade", "pip"], env=environment)
        run([str(venv_python()), "-m", "pip", "install", "-e", package], env=environment)

    playwright = [str(venv_python()), "-m", "playwright", "install"]
    if args.with_system_deps and platform.system() == "Linux":
        playwright.append("--with-deps")
    playwright.append("chromium")
    run(playwright, env=environment)

    config = {
        "mcpServers": {
            "cnki": {
                "command": str(server_executable()),
                "env": {
                    "PLAYWRIGHT_BROWSERS_PATH": str(BROWSERS),
                    "CNKI_MCP_DATA_DIR": str(DATA),
                },
            }
        }
    }
    generated = ROOT / "mcp-config.generated.json"
    generated.write_text(json.dumps(config, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print("\nCNKI-Enhanced-MCP installation completed.")
    print(f"Platform: {platform.system()} {platform.machine()}")
    print(f"Server:   {server_executable()}")
    print(f"Config:   {generated}")
    print("Copy the generated cnki entry into your MCP client's configuration, then restart the client.")


if __name__ == "__main__":
    main()
