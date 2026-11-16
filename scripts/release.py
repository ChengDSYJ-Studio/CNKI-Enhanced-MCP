#!/usr/bin/env python3
"""Reproducible runtime/source packages in dist/; runtime data is never included."""
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import zipfile

ROOT = Path(__file__).resolve().parents[1]
GO = os.environ.get("CNKI_GO") or shutil.which("go")
if GO is None:
    raise SystemExit("Building releases requires Go (see go.mod).")
VERSION = re.search(r'const Version = "([^"]+)"', (ROOT / "internal/cnki/model.go").read_text())[1]
NAME = f"CNKI-Enhanced-MCP-{VERSION}"
DIST = ROOT / "dist"
LICENSES = ROOT / "third_party_licenses"
# Pure Go (no cgo): every platform builds from one machine.
TARGETS = [("darwin", "arm64", "macOS-arm64"), ("darwin", "amd64", "macOS-x64"), ("windows", "amd64", "Windows-x64"),
           ("linux", "amd64", "Linux-x64"), ("linux", "arm64", "Linux-arm64")]
RUNTIME_NAMES = {"data", "cnki.sqlite", "research.json", "session.json", "config.json", "browser", "incoming", "downloads", "exports"}


def dependency_licenses():
    LICENSES.mkdir(exist_ok=True)
    output = subprocess.check_output([GO, "list", "-m", "-json", "all"], cwd=ROOT, text=True)
    decoder = json.JSONDecoder()
    manifest = []
    while output.strip():
        output = output.lstrip()
        module, end = decoder.raw_decode(output)
        output = output[end:]
        if module.get("Main") or not module.get("Dir"):
            continue
        for pattern in ["LICENSE*", "COPYING*", "NOTICE*"]:
            for source in Path(module["Dir"]).glob(pattern):
                if source.is_file():
                    label = module["Path"].replace("/", "_") + "@" + module["Version"] + "-" + source.name
                    shutil.copyfile(source, LICENSES / label)
                    manifest.append({"module": module["Path"], "version": module["Version"], "license_file": label})
    goroot = Path(subprocess.check_output([GO, "env", "GOROOT"], text=True).strip())
    shutil.copyfile(goroot / "LICENSE", LICENSES / "Go-LICENSE")
    (LICENSES / "manifest.json").write_text(json.dumps(manifest, indent=2))


def package(target):
    goos, arch, label = target
    folder = DIST / f"{NAME}-{label}"
    shutil.rmtree(folder, ignore_errors=True)
    folder.mkdir(parents=True)
    binary = folder / ("cnki-mcp.exe" if goos == "windows" else "cnki-mcp")
    env = dict(os.environ, GOOS=goos, GOARCH=arch, CGO_ENABLED="0")
    subprocess.run([GO, "build", "-trimpath", "-ldflags=-s -w", "-o", str(binary), "./cmd/cnki-mcp"], cwd=ROOT, env=env, check=True)
    for name in ["README.md", "README.en.md", "LICENSE", "THIRD_PARTY_NOTICES.md", "CHANGELOG.md"]:
        shutil.copyfile(ROOT / name, folder / name)
    for name in ["docs", "third_party_licenses"]:
        shutil.copytree(ROOT / name, folder / name, dirs_exist_ok=True)
    if goos == "windows":
        (folder / "安装设置.cmd").write_text('@echo off\r\ncd /d "%~dp0"\r\ncnki-mcp.exe setup\r\npause\r\n')
    else:
        script = folder / ("安装设置.command" if goos == "darwin" else "安装设置.sh")
        script.write_text('#!/bin/sh\nset -eu\nCNKI_PACKAGE_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)\nexec "$CNKI_PACKAGE_DIR/cnki-mcp" setup "$@"\n')
        script.chmod(0o755)
        binary.chmod(0o755)
    archive = Path(str(folder) + ".zip")
    with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as z:
        for path in sorted(folder.rglob("*")):
            if path.is_file():
                info = zipfile.ZipInfo.from_file(path, path.relative_to(DIST))
                with path.open("rb") as f:
                    z.writestr(info, f.read(), zipfile.ZIP_DEFLATED)
    shutil.rmtree(folder)
    print(label, archive.name, flush=True)
    return archive


def source_package():
    archive = DIST / f"{NAME}-source.zip"
    root_files = {"AGENTS.md", "README.md", "README.en.md", "LICENSE", "THIRD_PARTY_NOTICES.md", "CHANGELOG.md", "go.mod", "go.sum", ".gitignore"}
    source_dirs = {"cmd", "internal", "scripts", "docs", "third_party_licenses"}
    with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as z:
        for path in sorted(ROOT.rglob("*")):
            if not path.is_file():
                continue
            parts = path.relative_to(ROOT).parts
            if "__pycache__" in parts or path.suffix == ".pyc":
                continue
            if (len(parts) == 1 and parts[0] in root_files) or parts[0] in source_dirs:
                z.write(path, Path(f"{NAME}-source") / path.relative_to(ROOT))
    return archive


def verify(archive):
    with zipfile.ZipFile(archive) as z:
        for name in z.namelist():
            parts = set(Path(name).parts[1:])
            if parts & RUNTIME_NAMES and not name.split("/", 1)[1].startswith(("docs/", "internal/", "cmd/", "scripts/", "third_party_licenses/")):
                raise SystemExit(f"{archive.name} contains runtime data: {name}")


def main():
    DIST.mkdir(exist_ok=True)
    for old in DIST.glob(f"{NAME}-*"):
        old.unlink() if old.is_file() else shutil.rmtree(old)
    if os.environ.get("CNKI_KEEP_LICENSES") != "1":
        dependency_licenses()
    with ThreadPoolExecutor(max_workers=3) as pool:
        archives = list(pool.map(package, TARGETS))
    archives.append(source_package())
    for a in archives:
        verify(a)
    lines = [hashlib.sha256(p.read_bytes()).hexdigest() + "  " + p.name for p in archives]
    (DIST / "SHA256SUMS").write_text("\n".join(lines) + "\n")
    print(f"Release packages: {len(archives)} (verified; no runtime data)", flush=True)


if __name__ == "__main__":
    main()
