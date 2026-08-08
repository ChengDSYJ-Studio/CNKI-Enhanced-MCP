"""Optional live smoke test for native multi-row CNKI advanced search."""

from __future__ import annotations

import asyncio
import json

from cnki_mcp.server import browser, engine, store


async def run() -> None:
    try:
        result = await engine.structured_search(
            [
                {"field": "subject", "value": "违约金", "operator": "AND", "match": "fuzzy"},
                {"field": "source", "value": "法学", "operator": "AND", "match": "fuzzy"},
            ],
            pages=1,
            limit=3,
            metadata_depth="basic",
            profile="default",
        )
        print(json.dumps(result, ensure_ascii=False))
    finally:
        await browser.close()
        store.close()


if __name__ == "__main__":
    asyncio.run(run())
