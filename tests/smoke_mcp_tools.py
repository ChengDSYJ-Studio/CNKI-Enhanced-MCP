"""Local transport smoke test for tool discovery and citation export."""

from __future__ import annotations

import asyncio
import json
import sys

from fastmcp import Client

from cnki_mcp.server import browser, mcp, store


async def run() -> None:
    try:
        async with Client(mcp) as client:
            tools = await client.list_tools()
            names = [tool.name for tool in tools]
            output = {"tools": names}
            if len(sys.argv) > 1:
                exported = await client.call_tool(
                    "cnki_export_citations",
                    {"titles": [sys.argv[1]], "format": "bibtex"},
                )
                output["export"] = exported.structured_content
            print(json.dumps(output, ensure_ascii=False))
    finally:
        await browser.close()
        store.close()


if __name__ == "__main__":
    asyncio.run(run())
