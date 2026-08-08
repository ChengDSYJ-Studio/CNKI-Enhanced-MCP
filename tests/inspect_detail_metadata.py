"""Developer diagnostic for metadata and source links exposed by a CNKI detail page."""

from __future__ import annotations

import asyncio
import json
import sys
from urllib.parse import urlsplit

from cnki_mcp.server import browser, store


async def run() -> None:
    if len(sys.argv) < 2:
        raise SystemExit("usage: inspect_detail_metadata.py PAPER_ID [PROFILE]")
    paper = store.get_paper(sys.argv[1])
    if not paper:
        raise SystemExit("paper not found")
    profile = sys.argv[2] if len(sys.argv) > 2 else "default"

    async def inspect(page, _context):
        await page.goto(paper.detail_url, wait_until="domcontentloaded", timeout=30_000)
        metadata = await page.locator("meta[name], meta[property]").evaluate_all(
            "els => els.map(e => ({name:e.getAttribute('name') || e.getAttribute('property'), content:(e.content || '').slice(0,300)}))"
        )
        links = await page.locator("a").evaluate_all(
            "els => els.map(e => ({text:(e.innerText || '').trim(), href:e.href, title:e.title})).filter(x => x.text || x.title)"
        )
        markers = await page.locator("[title], img[alt]").evaluate_all(
            "els => els.map(e => ({tag:e.tagName, text:(e.innerText || '').trim(), title:e.title, alt:e.alt || ''})).filter(x => x.title || x.alt)"
        )
        source_link = page.locator("a[title*='数据库收录来源']").first
        source_page = None
        if await source_link.count():
            href = await source_link.get_attribute("href")
            if href:
                await page.goto(href, wait_until="domcontentloaded", timeout=30_000)
                source_page = {"url": page.url, "body": (await page.locator("body").inner_text())[:5000]}
        relevant_links = [
            item for item in links
            if "navi.cnki.net/knavi/detail" in item["href"]
            or any(key in (item["text"] + item["title"] + item["href"]) for key in ("收录来源", "阅读", "HTML", "html", "read", "Read"))
        ]
        for item in relevant_links:
            parsed = urlsplit(item["href"])
            if parsed.hostname and parsed.hostname.endswith("cnki.net") and parsed.query:
                item["href"] = f"{parsed.scheme}://{parsed.netloc}{parsed.path}"
        relevant_markers = [
            item for item in markers
            if any(key in (item["text"] + item["title"] + item["alt"]) for key in ("核心", "CSSCI", "学位", "图书", "期刊", "会议"))
        ]
        body_text = await page.locator("body").inner_text()
        title_index = body_text.find(paper.title)
        record_text = body_text[max(0, title_index - 300) : title_index + 1500] if title_index >= 0 else body_text[:1500]
        return {
            "metadata": metadata,
            "relevant_links": relevant_links,
            "relevant_markers": relevant_markers,
            "record_text": record_text,
            "source_page": source_page,
        }

    try:
        result = await browser.run(inspect, profile=profile, visible=True)
        print(json.dumps(result, ensure_ascii=False))
    finally:
        await browser.close()
        store.close()


if __name__ == "__main__":
    asyncio.run(run())
