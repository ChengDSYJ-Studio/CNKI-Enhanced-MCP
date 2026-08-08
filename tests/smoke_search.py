"""Optional live smoke test: performs one small CNKI search."""

from __future__ import annotations

import asyncio
import json
import sys

from cnki_mcp.server import browser, engine, store


async def run() -> None:
    try:
        profile = sys.argv[1] if len(sys.argv) > 1 else "smoke-search"
        result = await engine.search(
            "违约金",
            mode="precise",
            limit=3,
            profile=profile,
        )
        print(json.dumps(result, ensure_ascii=False))
    finally:
        await browser.close()
        store.close()


if __name__ == "__main__":
    asyncio.run(run())
