import json

import pytest
from fastmcp import Client

from cnki_mcp.server import mcp


@pytest.mark.asyncio
async def test_public_tool_schemas_do_not_expose_internal_paper_ids() -> None:
    async with Client(mcp) as client:
        tools = await client.list_tools()
    schemas = json.dumps([tool.inputSchema for tool in tools], ensure_ascii=False)
    assert "paper_id" not in schemas
    assert "paper_ids" not in schemas


@pytest.mark.asyncio
async def test_natural_search_has_small_safe_public_schema() -> None:
    async with Client(mcp) as client:
        tools = {tool.name: tool for tool in await client.list_tools()}
    assert set(tools["cnki_search"].inputSchema["properties"]) == {
        "query", "mode", "limit", "year_from", "year_to", "profile"
    }
    assert "cnki_get_metadata" in tools
    assert "cnki_get_paper" not in tools
    assert "cnki_get_search_results" not in tools
