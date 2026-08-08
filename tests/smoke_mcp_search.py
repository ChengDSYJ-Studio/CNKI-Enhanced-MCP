"""Optional live test that calls cnki_search through a FastMCP client."""

from __future__ import annotations

import asyncio
import json
import sys

from fastmcp import Client

from cnki_mcp.server import browser, mcp, store


async def run() -> None:
    query = sys.argv[1] if len(sys.argv) > 1 else "悬赏广告"
    try:
        async with Client(mcp, timeout=180) as client:
            result = await client.call_tool(
                "cnki_search",
                {
                    "query": query,
                    "mode": "balanced",
                    "profile": "default",
                },
                timeout=180,
            )
            payload = result.structured_content
            print(json.dumps(payload, ensure_ascii=False))
    finally:
        await browser.close()
        store.close()


if __name__ == "__main__":
    asyncio.run(run())
