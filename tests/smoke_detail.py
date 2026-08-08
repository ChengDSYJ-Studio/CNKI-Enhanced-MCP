"""Optional live smoke test for one cached CNKI detail page."""

from __future__ import annotations

import asyncio
import json
import sys
import time

from cnki_mcp.server import browser, engine, store


async def run() -> None:
    if len(sys.argv) < 2:
        raise SystemExit("usage: smoke_detail.py FULL_TITLE [PROFILE]")
    title = sys.argv[1]
    profile = sys.argv[2] if len(sys.argv) > 2 else "default"
    started = time.monotonic()
    try:
        metadata = await engine.get_metadata(titles=[title], refresh=True, profile=profile)
        result = metadata["results"][0]
        summary = {
            "title": result["title"],
            "has_abstract": bool(result.get("abstract")),
            "keywords": result.get("keywords", []),
            "metadata_complete": result.get("metadata_complete"),
            "elapsed_seconds": round(time.monotonic() - started, 2),
        }
        print(json.dumps(summary, ensure_ascii=False))
    finally:
        await browser.close()
        store.close()


if __name__ == "__main__":
    asyncio.run(run())
