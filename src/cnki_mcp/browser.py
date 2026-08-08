from __future__ import annotations

import asyncio
import os
import re
from pathlib import Path
from typing import Any, Awaitable, Callable, TypeVar

from .paths import DIAGNOSTICS_DIR, configure_environment, ensure_runtime_dirs, profile_directory

configure_environment()

from playwright.async_api import BrowserContext, Page, Playwright, async_playwright  # noqa: E402


T = TypeVar("T")


class CnkiBrowser:
    def __init__(self) -> None:
        ensure_runtime_dirs()
        self._playwright: Playwright | None = None
        self._context: BrowserContext | None = None
        self._profile: str | None = None
        self._visible = False
        self._lock = asyncio.Lock()

    async def _ensure_context(
        self, profile: str = "default", *, visible: bool = False, background: bool = False
    ) -> BrowserContext:
        if self._context and self._profile == profile and (self._visible or not visible):
            return self._context
        await self._close_unlocked()
        self._playwright = await async_playwright().start()
        headless_setting = os.getenv("CNKI_MCP_HEADLESS", "false").lower() == "true"
        launch_args = [
            "--disable-dev-shm-usage",
            "--disable-background-timer-throttling",
            "--disable-backgrounding-occluded-windows",
            "--disable-renderer-backgrounding",
        ]
        if visible and background:
            launch_args.append("--start-minimized")
        launch_options: dict[str, Any] = {}
        executable = os.getenv("CNKI_MCP_BROWSER_EXECUTABLE", "").strip()
        channel = os.getenv("CNKI_MCP_BROWSER_CHANNEL", "").strip()
        if executable:
            executable_path = Path(executable).expanduser().resolve()
            if not executable_path.is_file():
                raise ValueError(f"CNKI_MCP_BROWSER_EXECUTABLE 不存在: {executable_path}")
            launch_options["executable_path"] = str(executable_path)
        elif channel:
            if channel not in {"chrome", "chrome-beta", "chrome-dev", "chrome-canary", "msedge", "msedge-beta", "msedge-dev", "msedge-canary"}:
                raise ValueError("CNKI_MCP_BROWSER_CHANNEL 不是 Playwright 支持的 Chrome/Edge 通道")
            launch_options["channel"] = channel
        self._context = await self._playwright.chromium.launch_persistent_context(
            str(profile_directory(profile)),
            headless=False if visible else headless_setting,
            accept_downloads=True,
            locale="zh-CN",
            viewport={"width": 1440, "height": 960},
            args=launch_args,
            **launch_options,
        )
        self._profile = profile
        self._visible = not (False if visible else headless_setting)
        return self._context

    async def run(
        self,
        operation: Callable[[Page, BrowserContext], Awaitable[T]],
        *,
        profile: str = "default",
        visible: bool = False,
        background: bool = False,
        keep_page: bool = False,
    ) -> T:
        async with self._lock:
            context = await self._ensure_context(profile, visible=visible, background=background)
            page = context.pages[0] if visible and context.pages else await context.new_page()
            if visible:
                await self._set_window_minimized(context, page, minimized=background)
            try:
                return await operation(page, context)
            except Exception:
                await self.capture_diagnostic(page, "operation-error")
                raise
            finally:
                if not keep_page and not page.is_closed():
                    await page.close()

    @staticmethod
    async def _set_window_minimized(context: BrowserContext, page: Page, *, minimized: bool) -> None:
        """Keep headed Chromium out of the way without changing its browser mode."""
        session = None
        try:
            session = await context.new_cdp_session(page)
            window = await session.send("Browser.getWindowForTarget")
            window_id = window.get("windowId")
            if window_id is None:
                return
            state = window.get("bounds", {}).get("windowState")
            target_state = "minimized" if minimized else "normal"
            if state != target_state:
                await session.send(
                    "Browser.setWindowBounds",
                    {"windowId": window_id, "bounds": {"windowState": target_state}},
                )
        except Exception:
            # Window management is best-effort; search must remain functional on
            # platforms where Chromium does not expose window bounds over CDP.
            return
        finally:
            if session is not None:
                try:
                    await session.detach()
                except Exception:
                    pass

    async def capture_diagnostic(self, page: Page, name: str) -> None:
        safe = re.sub(r"[^a-zA-Z0-9_-]+", "-", name)
        try:
            await page.screenshot(path=str(DIAGNOSTICS_DIR / f"{safe}.png"), full_page=True)
            (DIAGNOSTICS_DIR / f"{safe}.html").write_text(await page.content(), encoding="utf-8")
        except Exception:
            return

    async def _close_unlocked(self) -> None:
        if self._context:
            await self._context.close()
            self._context = None
        if self._playwright:
            await self._playwright.stop()
            self._playwright = None
        self._profile = None
        self._visible = False

    async def close(self) -> None:
        async with self._lock:
            await self._close_unlocked()


async def page_text(page: Page) -> str:
    try:
        return await page.locator("body").inner_text(timeout=10_000)
    except Exception:
        return ""


async def detect_access(page: Page) -> dict[str, Any]:
    body = await page_text(page)
    try:
        cookies = await page.context.cookies(
            ["https://www.cnki.net/", "https://kns.cnki.net/", "https://login.cnki.net/"]
        )
    except Exception:
        cookies = []
    cookie_names = {str(cookie.get("name", "")) for cookie in cookies}
    authenticated_by_cookie = has_auth_cookie(cookie_names)
    login_markers = ("退出登录", "个人中心", "我的知网", "欢迎您")
    institution_patterns = (
        r"欢迎.*?大学", r"欢迎.*?学院", r"机构用户[:：]?\s*([^\n]+)", r"IP登录[:：]?\s*([^\n]+)"
    )
    institution = None
    for pattern in institution_patterns:
        match = re.search(pattern, body)
        if match:
            institution = match.group(0).strip()[:100]
            break
    captcha_required = "/verify/" in page.url
    if not captcha_required:
        result_rows = await page.locator("table.result-table-list tbody tr").count()
        if not result_rows:
            for marker in ("请完成安全验证", "向右滑动完成验证"):
                try:
                    locator = page.get_by_text(marker, exact=False).first
                    if await locator.count() and await locator.is_visible():
                        captcha_required = True
                        break
                except Exception:
                    continue
    return {
        "authenticated": authenticated_by_cookie or any(marker in body for marker in login_markers) or institution is not None,
        "institution": institution,
        "captcha_required": captcha_required,
        "authentication_evidence": "session_cookie" if authenticated_by_cookie else "page_marker" if any(marker in body for marker in login_markers) or institution else None,
        "url": page.url,
    }


def has_auth_cookie(cookie_names: set[str]) -> bool:
    """Recognize CNKI login cookies by name without reading or exposing values."""
    strong_markers = {"Ecp_LoginStuts", "LID", "c_m_LinID"}
    federated_markers = {"SID_fsso"}
    return bool(cookie_names & strong_markers) or federated_markers.issubset(cookie_names)


def validate_remote_url(value: str | None) -> str:
    if not value or not value.startswith(("https://", "http://")):
        raise ValueError("institution_remote 模式需要有效的 remote_access_url")
    return value
