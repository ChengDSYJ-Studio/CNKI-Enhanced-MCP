"""End-to-end verification of every public MCP tool against a live CNKI session."""

from __future__ import annotations

import asyncio
import json
import time
from pathlib import Path
from typing import Any

from fastmcp import Client

from cnki_mcp.paths import DIAGNOSTICS_DIR
from cnki_mcp.server import browser, mcp, store


FORMATS = [
    "gbt7714",
    "apa",
    "mla",
    "chicago",
    "vancouver",
    "bibtex",
    "ris",
    "endnote",
    "csl_json",
    "json",
    "csv",
    "markdown",
    "custom",
]


def payload(result: Any) -> dict[str, Any]:
    return result.structured_content or {}


async def main() -> None:
    report: dict[str, Any] = {"started_at": time.strftime("%Y-%m-%dT%H:%M:%S%z"), "tools": {}}

    async def call(client: Client, name: str, arguments: dict[str, Any], timeout: int = 180) -> dict[str, Any] | None:
        started = time.monotonic()
        try:
            result = await client.call_tool(name, arguments, timeout=timeout)
            data = payload(result)
            report["tools"][name] = {
                "status": "passed",
                "elapsed_seconds": round(time.monotonic() - started, 2),
            }
            return data
        except Exception as exc:
            report["tools"][name] = {
                "status": "permission_limited" if name == "cnki_download_paper" else "failed",
                "elapsed_seconds": round(time.monotonic() - started, 2),
                "error_type": type(exc).__name__,
                "error": str(exc)[:500],
            }
            return None

    try:
        async with Client(mcp, timeout=240) as client:
            discovered = [tool.name for tool in await client.list_tools()]
            report["discovered_tools"] = discovered

            status = await call(client, "cnki_session_status", {"profile": "default"})
            if status:
                report["tools"]["cnki_session_status"].update(
                    authenticated=status.get("authenticated"),
                    captcha_required=status.get("captcha_required"),
                )

            login = await call(
                client,
                "cnki_login",
                {"method": "auto", "profile": "default", "timeout_seconds": 30},
                timeout=60,
            )
            if login:
                report["tools"]["cnki_login"].update(login_status=login.get("status"), method=login.get("method"))

            natural = await call(
                client,
                "cnki_search",
                {
                    "query": "违约金",
                    "mode": "precise",
                    "limit": 5,
                    "profile": "default",
                },
            )
            if natural:
                report["tools"]["cnki_search"].update(
                    search_id=natural.get("search_id"),
                    result_count=len(natural.get("results", [])),
                    raw_results=natural.get("stats", {}).get("raw_results"),
                    unique_results=natural.get("stats", {}).get("unique_results"),
                    metadata_enriched=natural.get("stats", {}).get("metadata_enriched"),
                    quality_candidates_targeted=natural.get("stats", {}).get("quality_candidates_targeted"),
                    first_title=natural.get("results", [{}])[0].get("title"),
                )

            structured = await call(
                client,
                "cnki_structured_search",
                {
                    "conditions": [
                        {"field": "subject", "value": "违约金", "match": "fuzzy"},
                        {"field": "source", "value": "法学", "operator": "AND", "match": "fuzzy"},
                    ],
                    "pages": 1,
                    "limit": 5,
                    "metadata_depth": "basic",
                    "profile": "default",
                },
            )
            if structured:
                report["tools"]["cnki_structured_search"].update(
                    search_id=structured.get("search_id"),
                    result_count=len(structured.get("results", [])),
                    raw_results=structured.get("stats", {}).get("raw_results"),
                    first_title=structured.get("results", [{}])[0].get("title"),
                    warnings=structured.get("warnings", []),
                )
            source_search = structured if structured and structured.get("results") else natural
            if not source_search or not source_search.get("results"):
                raise RuntimeError("两种搜索都没有返回可用于后续验证的论文")
            selected = source_search["results"][0]
            title = selected["title"]
            search_id = source_search["search_id"]
            report["selected_paper"] = {"title": title}

            metadata = await call(
                client,
                "cnki_get_metadata",
                {
                    "titles": [title],
                    "search_id": search_id,
                    "offset": 0,
                    "limit": 3,
                    "fields": ["title", "authors", "source", "year", "detail_url"],
                    "refresh": True,
                    "profile": "default",
                },
            )
            if metadata:
                first = (metadata.get("results") or [{}])[0]
                report["tools"]["cnki_get_metadata"].update(
                    result_count=len(metadata.get("results", [])),
                    has_more=metadata.get("has_more"),
                    first_title=first.get("title"),
                    author_count=len(first.get("authors", [])),
                )

            html = await call(
                client,
                "cnki_read_online_html",
                {"title": title, "read_all": True, "profile": "default"},
                timeout=180,
            )
            if html:
                report["tools"]["cnki_read_online_html"].update(
                    access_status=html.get("status"),
                    total_characters=html.get("total_characters"),
                    returned_characters=html.get("returned_characters"),
                    complete_available_content=html.get("complete_available_content"),
                    complete_article=html.get("complete_article"),
                )

            export_results: dict[str, Any] = {}
            for output_format in FORMATS:
                arguments: dict[str, Any] = {"titles": [title], "format": output_format, "profile": "default"}
                if output_format == "custom":
                    arguments["custom_template"] = "{authors}.《{title}》.{source},{year}.{url}"
                if output_format == "bibtex":
                    arguments["save_filename"] = "e2e-verification.bib"
                started = time.monotonic()
                try:
                    exported = payload(await client.call_tool("cnki_export_citations", arguments, timeout=60))
                    export_results[output_format] = {
                        "status": "passed",
                        "count": exported.get("count"),
                        "extension": exported.get("extension"),
                        "content_length": len(exported.get("content", "")),
                        "saved": bool(exported.get("file_path")),
                        "elapsed_seconds": round(time.monotonic() - started, 2),
                    }
                except Exception as exc:
                    export_results[output_format] = {"status": "failed", "error": str(exc)[:300]}
            report["tools"]["cnki_export_citations"] = {
                "status": "passed" if all(item["status"] == "passed" for item in export_results.values()) else "failed",
                "formats": export_results,
            }

            linked = await call(
                client,
                "cnki_link_references",
                {"text": f"本文讨论《{title}》。", "search_missing": False, "profile": "default"},
            )
            if linked:
                matched_count = len(linked.get("matched", []))
                report["tools"]["cnki_link_references"].update(
                    status="passed" if matched_count else "failed",
                    matched_count=matched_count,
                    unresolved=linked.get("unresolved", []),
                )

            download = await call(
                client,
                "cnki_download_paper",
                {
                    "title": title,
                    "subdirectory": "e2e-verification",
                    "preferred_format": "pdf",
                    "overwrite": False,
                    "profile": "default",
                },
                timeout=120,
            )
            if download:
                report["tools"]["cnki_download_paper"].update(
                    download_status=download.get("status"),
                    format=download.get("format"),
                    file_size=download.get("file_size"),
                )
    finally:
        report["overall_status"] = (
            "passed"
            if report.get("tools") and all(item.get("status") == "passed" for item in report["tools"].values())
            else "failed"
        )
        report["finished_at"] = time.strftime("%Y-%m-%dT%H:%M:%S%z")
        DIAGNOSTICS_DIR.mkdir(parents=True, exist_ok=True)
        report_path = Path(DIAGNOSTICS_DIR) / "e2e-verification.json"
        report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(json.dumps(report, ensure_ascii=False, indent=2))
        await browser.close()
        store.close()


if __name__ == "__main__":
    asyncio.run(main())
