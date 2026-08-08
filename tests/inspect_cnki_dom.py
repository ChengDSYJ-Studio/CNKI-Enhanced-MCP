"""Developer diagnostic for current CNKI search/result DOM."""

from __future__ import annotations

import asyncio
import json
import os
import sys

from playwright.async_api import async_playwright

from cnki_mcp.paths import configure_environment, ensure_runtime_dirs, profile_directory


async def run() -> None:
    configure_environment()
    ensure_runtime_dirs()
    profile = sys.argv[1] if len(sys.argv) > 1 else "inspect"
    headless = os.getenv("CNKI_MCP_HEADLESS", "true").lower() == "true"
    async with async_playwright() as playwright:
        context = await playwright.chromium.launch_persistent_context(
            str(profile_directory(profile)),
            headless=headless,
            locale="zh-CN",
            viewport={"width": 1440, "height": 960},
        )
        page = context.pages[0] if context.pages else await context.new_page()
        await page.goto("https://www.cnki.net/", wait_until="domcontentloaded", timeout=60_000)
        home = {
            "url": page.url,
            "title": await page.title(),
            "search_input": await page.locator("#txt_SearchText").count(),
            "search_buttons": await page.locator(".search-btn").count(),
            "buttons": await page.get_by_role("button").all_inner_texts(),
            "inputs": await page.locator("input").evaluate_all(
                "els => els.map(e => ({id:e.id,name:e.name,placeholder:e.placeholder,type:e.type})).slice(0,30)"
            ),
            "cookie_names": sorted({cookie["name"] for cookie in await context.cookies()}),
            "browser_signals": await page.evaluate(
                "() => ({userAgent:navigator.userAgent, webdriver:navigator.webdriver, languages:navigator.languages, plugins:navigator.plugins.length})"
            ),
        }
        print("HOME", json.dumps(home, ensure_ascii=False))
        box = page.locator("#txt_SearchText").first
        if await box.count():
            await box.fill("违约金")
            button = page.locator(".search-btn").first
            if await button.count():
                await button.click()
                await page.wait_for_timeout(8_000)
        result = {
            "url": page.url,
            "title": await page.title(),
            "tables": await page.locator("table").count(),
            "known_rows": await page.locator("table.result-table-list tbody tr").count(),
            "classes": await page.locator("table").evaluate_all(
                "els => els.map(e => e.className).slice(0,20)"
            ),
            "links": (await page.locator("a").all_inner_texts())[:50],
            "body": (await page.locator("body").inner_text())[:3000],
        }
        print("RESULT", json.dumps(result, ensure_ascii=False))
        await context.close()


if __name__ == "__main__":
    asyncio.run(run())
