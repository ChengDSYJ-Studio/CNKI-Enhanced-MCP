"""Developer diagnostic for the authorized CNKI HTML reader DOM and responses."""

from __future__ import annotations

import asyncio
import json
import re
import sys
from urllib.parse import urljoin, urlsplit

from cnki_mcp.server import browser, store


async def run() -> None:
    if len(sys.argv) < 2:
        raise SystemExit("usage: inspect_online_html.py PAPER_ID [PROFILE]")
    paper = store.get_paper(sys.argv[1])
    if not paper:
        raise SystemExit("paper not found")
    responses = []

    def safe_url(value: str) -> str:
        parsed = urlsplit(value)
        return f"{parsed.scheme}://{parsed.netloc}{parsed.path}"

    async def inspect(page, _context):
        page.on(
            "response",
            lambda response: responses.append(
                {"url": safe_url(response.url), "status": response.status, "type": response.request.resource_type}
            ) if response.request.resource_type in {"document", "xhr", "fetch"} else None,
        )
        await page.goto(paper.detail_url, wait_until="domcontentloaded", timeout=60_000)
        link = page.get_by_text(re.compile(r"^HTML\s*阅读$")).first
        href = await link.get_attribute("href")
        await page.goto(urljoin(page.url, href or ""), wait_until="commit", timeout=60_000)
        await page.wait_for_timeout(8_000)
        frames = []
        for frame in page.frames:
            try:
                body = await frame.locator("body").inner_text(timeout=3_000)
                html = await frame.locator("html").inner_html(timeout=3_000)
            except Exception:
                body, html = "", ""
            frames.append({"url": frame.url, "body_length": len(body), "body": body[:1000], "html_length": len(html)})
        return {
            "url": safe_url(page.url),
            "title": await page.title(),
            "frames": frames,
            "responses": responses[-100:],
            "scripts": [safe_url(url) for url in await page.locator("script[src]").evaluate_all("els => els.map(e => e.src)")],
        }

    try:
        print(json.dumps(await browser.run(inspect, profile=sys.argv[2] if len(sys.argv) > 2 else "default", visible=True, background=True), ensure_ascii=False, indent=2))
    finally:
        await browser.close()
        store.close()


if __name__ == "__main__":
    asyncio.run(run())
