"""Confirm that automated headed-browser work runs in a minimized window."""

from __future__ import annotations

import asyncio
import json
import sys

from cnki_mcp.server import browser, store


async def run() -> None:
    profile = sys.argv[1] if len(sys.argv) > 1 else "default"

    async def inspect(page, context):
        session = await context.new_cdp_session(page)
        try:
            window = await session.send("Browser.getWindowForTarget")
            return window.get("bounds", {})
        finally:
            await session.detach()

    try:
        bounds = await browser.run(inspect, profile=profile, visible=True, background=True)
        print(json.dumps(bounds, ensure_ascii=False))
    finally:
        await browser.close()
        store.close()


if __name__ == "__main__":
    asyncio.run(run())
