"""Live MCP smoke test: use an exact title that has never been searched locally."""

from __future__ import annotations

import asyncio
import json
import sys

from fastmcp import Client

from cnki_mcp.server import browser, mcp, store


async def run() -> None:
    if len(sys.argv) < 2:
        raise SystemExit("usage: smoke_title_resolution.py FULL_TITLE [PROFILE]")
    title = sys.argv[1]
    profile = sys.argv[2] if len(sys.argv) > 2 else "default"
    try:
        async with Client(mcp, timeout=180) as client:
            result = await client.call_tool(
                "cnki_get_metadata",
                {"titles": [title], "refresh": True, "profile": profile},
                timeout=180,
            )
            payload = result.structured_content or {}
            data = (payload.get("results") or [{}])[0]
            print(
                json.dumps(
                    {
                        "title": data.get("title"),
                        "authors": data.get("authors"),
                        "source": data.get("source"),
                        "year": data.get("year"),
                        "has_abstract": bool(data.get("abstract")),
                        "contains_internal_id": "paper_id" in data,
                    },
                    ensure_ascii=False,
                )
            )
    finally:
        await browser.close()
        store.close()


if __name__ == "__main__":
    asyncio.run(run())
