from pathlib import Path

import pytest

from cnki_mcp.paths import DOWNLOADS_DIR, EXPORTS_DIR, PROJECT_ROOT, safe_download_directory, safe_export_path
from cnki_mcp.browser import has_auth_cookie


def test_runtime_paths_are_inside_project() -> None:
    DOWNLOADS_DIR.resolve().relative_to(PROJECT_ROOT.resolve())


def test_download_subdirectory_cannot_escape() -> None:
    with pytest.raises(ValueError):
        safe_download_directory("../../outside")


def test_download_subdirectory_is_inside_workspace() -> None:
    target = safe_download_directory("papers")
    target.relative_to(DOWNLOADS_DIR)


def test_export_path_is_inside_workspace() -> None:
    safe_export_path("../references.bib").relative_to(EXPORTS_DIR)


def test_cnki_login_cookie_detection() -> None:
    assert has_auth_cookie({"Ecp_LoginStuts", "Ecp_session"}) is True
    assert has_auth_cookie({"Ecp_session", "Ecp_ClientId"}) is False
