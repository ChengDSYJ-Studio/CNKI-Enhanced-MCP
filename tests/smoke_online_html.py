"""Optional live smoke test for authorized CNKI HTML reading."""

from __future__ import annotations

import asyncio
import json
import sys

from cnki_mcp.server import browser, engine, store


async def run() -> None:
    if len(sys.argv) < 2:
        raise SystemExit("usage: smoke_online_html.py FULL_TITLE [PROFILE]")
    try:
        result = await engine.read_online_html(
            sys.argv[1],
            max_characters=1_500,
            profile=sys.argv[2] if len(sys.argv) > 2 else "default",
        )
        result["content_tail"] = result["content"][-1000:]
        result["content"] = result["content"][:500]
        print(json.dumps(result, ensure_ascii=False))
    finally:
        await browser.close()
        store.close()


if __name__ == "__main__":
    asyncio.run(run())
