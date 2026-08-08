from __future__ import annotations

import os
from pathlib import Path


PROJECT_ROOT = Path(__file__).resolve().parents[2]


def _inside_project(raw: str | None, default: str) -> Path:
    value = Path(raw or default)
    candidate = value if value.is_absolute() else PROJECT_ROOT / value
    candidate = candidate.resolve()
    try:
        candidate.relative_to(PROJECT_ROOT)
    except ValueError as exc:
        raise ValueError(f"运行数据目录必须位于项目工作区内: {candidate}") from exc
    return candidate


DATA_DIR = _inside_project(os.getenv("CNKI_MCP_DATA_DIR"), ".cnki-data")
BROWSERS_DIR = _inside_project(os.getenv("PLAYWRIGHT_BROWSERS_PATH"), ".playwright-browsers")
PROFILES_DIR = DATA_DIR / "browser-profiles"
DOWNLOADS_DIR = DATA_DIR / "downloads"
DIAGNOSTICS_DIR = DATA_DIR / "diagnostics"
EXPORTS_DIR = DATA_DIR / "exports"
DB_PATH = DATA_DIR / "cnki.sqlite3"


def configure_environment() -> None:
    os.environ.setdefault("PLAYWRIGHT_BROWSERS_PATH", str(BROWSERS_DIR))


def ensure_runtime_dirs() -> None:
    for path in (
        DATA_DIR,
        BROWSERS_DIR,
        PROFILES_DIR,
        DOWNLOADS_DIR,
        DIAGNOSTICS_DIR,
        EXPORTS_DIR,
    ):
        path.mkdir(parents=True, exist_ok=True)


def safe_download_directory(subdirectory: str | None = None) -> Path:
    target = DOWNLOADS_DIR if not subdirectory else (DOWNLOADS_DIR / subdirectory)
    target = target.resolve()
    try:
        target.relative_to(DOWNLOADS_DIR.resolve())
    except ValueError as exc:
        raise ValueError("下载目录必须位于项目的 .cnki-data/downloads 内") from exc
    target.mkdir(parents=True, exist_ok=True)
    return target


def safe_export_path(filename: str) -> Path:
    cleaned = "".join(c for c in Path(filename).name if c.isalnum() or c in "-_.").strip(".")
    if not cleaned:
        raise ValueError("导出文件名无效")
    target = (EXPORTS_DIR / cleaned).resolve()
    try:
        target.relative_to(EXPORTS_DIR.resolve())
    except ValueError as exc:
        raise ValueError("导出文件必须位于项目的 .cnki-data/exports 内") from exc
    EXPORTS_DIR.mkdir(parents=True, exist_ok=True)
    return target


def profile_directory(profile: str) -> Path:
    cleaned = "".join(c for c in profile if c.isalnum() or c in "-_").strip("-_")
    if not cleaned:
        raise ValueError("profile 只能包含字母、数字、短横线和下划线")
    target = PROFILES_DIR / cleaned
    target.mkdir(parents=True, exist_ok=True)
    return target
