from __future__ import annotations

import asyncio
import hashlib
import os
import random
import re
import time
import uuid
from pathlib import Path
from typing import Any
from urllib.parse import urljoin

from playwright.async_api import BrowserContext, Locator, Page, TimeoutError as PlaywrightTimeoutError

from .browser import CnkiBrowser, detect_access, page_text, validate_remote_url
from .citations import export_records
from .models import Paper, SearchCell
from .models import SearchPlan
from .online import ACCESS_DENIED_MARKERS, clean_online_text, is_cnki_url, reader_access, slice_online_text
from .paths import safe_download_directory, safe_export_path
from .planner import build_feedback_cells, build_plan, mine_feedback_terms
from .references import extract_marked_titles
from .ranking import (
    deduplicate,
    normalize_title,
    rerank,
    select_adaptive_results,
    text_similarity,
)
from .store import Store
from .structured import describe_conditions, normalize_conditions


CNKI_HOME = "https://www.cnki.net/"
CNKI_ADVANCED = "https://kns.cnki.net/kns8s/AdvSearch"

FIELD_VALUES = {
    "subject": "SU$%=|",
    "title": "TI",
    "keyword": "KY",
}

FIELD_LABELS = {
    "subject": "主题",
    "title": "篇名",
    "keyword": "关键词",
}

RESOURCE_CODE_TYPES = {
    "CJFD": "期刊",
    "CJFQ": "期刊",
    "CJFN": "期刊",
    "CDFD": "博士论文",
    "CMFD": "硕士论文",
    "CPFD": "会议论文",
    "CCND": "报纸",
    "CYFD": "年鉴",
}


def _source_tiers(value: str | None) -> list[str]:
    if not value:
        return []
    upper = value.upper()
    patterns = (
        ("A&HCI", r"A\s*&\s*HCI"),
        ("CSSCI", r"(?<![A-Z])CSSCI(?![A-Z])"),
        ("SSCI", r"(?<![A-Z])SSCI(?![A-Z])"),
        ("SCI", r"(?<![A-Z])SCI(?![A-Z])"),
        ("CSCD", r"(?<![A-Z])CSCD(?![A-Z])"),
        ("EI", r"(?<![A-Z])EI(?![A-Z])"),
        ("北大核心", r"北大核心|中文核心期刊要目总览"),
        ("AMI核心", r"AMI\s*核心"),
        ("AMI扩展", r"AMI\s*扩展"),
        ("AMI入库", r"AMI\s*入库"),
    )
    tiers = [name for name, pattern in patterns if re.search(pattern, upper)]
    if not tiers and re.search(r"核心期刊", value):
        tiers.append("核心期刊")
    return tiers


def _resource_type(value: str | None) -> str | None:
    if not value:
        return None
    upper = value.upper()
    code = re.search(r"(?:DBCODE|DBNAME|DATABASENAME)[=/:%3D]+([A-Z]+)", upper)
    if code and code.group(1) in RESOURCE_CODE_TYPES:
        return RESOURCE_CODE_TYPES[code.group(1)]
    keywords = (
        ("本科论文", r"本科毕业论文|学士学位论文"),
        ("博士论文", r"博士学位论文|博士论文|学科专业[:：][^\n]*博士"),
        ("硕士论文", r"硕士学位论文|硕士论文|学科专业[:：][^\n]*硕士"),
        ("学位论文", r"学位论文|导师[:：]"),
        ("会议论文", r"会议论文|会议录"),
        ("报纸", r"报纸|报刊"),
        ("年鉴", r"年鉴"),
        ("图书章节", r"图书|专著|章节"),
        ("期刊", r"期刊|journal"),
    )
    for name, pattern in keywords:
        if re.search(pattern, value, flags=re.IGNORECASE):
            return name
    return None


def _record_context(body: str, title: str, radius: int = 2_000) -> str:
    """Keep classification local to the record, excluding global CNKI navigation labels."""
    index = body.find(title)
    if index < 0:
        return ""
    return body[index : index + radius]


def _integer(value: str | None) -> int | None:
    if not value:
        return None
    match = re.search(r"\d+", value.replace(",", ""))
    return int(match.group()) if match else None


def _decimal_after_label(value: str, label: str) -> float | None:
    match = re.search(rf"{re.escape(label)}[:：]\s*([0-9]+(?:\.[0-9]+)?)", value)
    return float(match.group(1)) if match else None


def _paper_id(title: str, authors: list[str], source: str | None, year: int | None) -> str:
    """Build an ID from bibliographic identity, not CNKI's expiring v token."""
    identity = "|".join(
        (
            normalize_title(title),
            normalize_title(authors[0]) if authors else "",
            normalize_title(source or ""),
            str(year or ""),
        )
    )
    return "cnki_" + hashlib.sha256(identity.encode("utf-8")).hexdigest()[:20]


def dynamic_rerank_candidates(unique_count: int, limit: int | None = None) -> int:
    """Choose a quality-enrichment pool without exposing an unsafe bypass."""
    if unique_count <= 0:
        return 0
    if unique_count <= 30:
        target = unique_count
    elif unique_count <= 100:
        target = 40
    elif unique_count <= 300:
        target = 60
    else:
        target = 80
    if limit is not None:
        target = max(target, limit)
    return min(unique_count, target, 100)


async def _first_text(page: Page, selectors: list[str]) -> str | None:
    for selector in selectors:
        try:
            locator = page.locator(selector).first
            if await locator.count() and await locator.is_visible(timeout=1_000):
                value = (await locator.inner_text()).strip()
                if value:
                    return value
        except Exception:
            continue
    return None


async def _meta_content(page: Page, names: list[str]) -> str | None:
    for name in names:
        for selector in (f'meta[name="{name}"]', f'meta[property="{name}"]'):
            try:
                locator = page.locator(selector).first
                if not await locator.count():
                    continue
                value = await locator.get_attribute("content", timeout=1_000)
                if value:
                    return value.strip()
            except Exception:
                continue
    return None


async def _meta_contents(page: Page, names: list[str]) -> list[str]:
    output: list[str] = []
    for name in names:
        for selector in (f'meta[name="{name}"]', f'meta[property="{name}"]'):
            try:
                values = await page.locator(selector).evaluate_all(
                    "els => els.map(e => (e.content || '').trim()).filter(Boolean)"
                )
                for value in values:
                    if value not in output:
                        output.append(value)
            except Exception:
                continue
    return output


class CnkiEngine:
    def __init__(self, browser: CnkiBrowser, store: Store) -> None:
        self.browser = browser
        self.store = store
        self._source_cache: dict[str, dict[str, Any]] = {}

    async def login(
        self,
        method: str = "auto",
        profile: str = "default",
        remote_access_url: str | None = None,
        timeout_seconds: int = 300,
    ) -> dict[str, Any]:
        allowed = {"auto", "campus_ip", "account", "institution_remote"}
        if method not in allowed:
            raise ValueError(f"method 必须是 {', '.join(sorted(allowed))}")

        async def operation(page: Page, _: BrowserContext) -> dict[str, Any]:
            target = validate_remote_url(remote_access_url) if method == "institution_remote" else CNKI_HOME
            await page.goto(target, wait_until="domcontentloaded", timeout=60_000)
            initial = await detect_access(page)
            if method in {"auto", "campus_ip"} and initial["authenticated"] and not initial["captcha_required"]:
                return {
                    "status": "authenticated",
                    "method": "campus_ip" if initial["institution"] else "existing_session",
                    "profile": profile,
                    "institution": initial["institution"],
                    "search_access": True,
                    "detail_access": True,
                    "download_access": None,
                    "message": "已检测到可用的 CNKI 会话。下载权限将在实际下载时验证。",
                }

            if method == "account" or (method == "auto" and not initial["authenticated"]):
                login_link = page.get_by_text(re.compile(r"^(登录|个人登录)$")).first
                try:
                    if await login_link.count():
                        await login_link.click()
                except Exception:
                    pass

            deadline = time.monotonic() + min(max(timeout_seconds, 30), 900)
            while time.monotonic() < deadline:
                await asyncio.sleep(2)
                state = await detect_access(page)
                if state["captcha_required"]:
                    continue
                if state["authenticated"]:
                    return {
                        "status": "authenticated",
                        "method": method,
                        "profile": profile,
                        "institution": state["institution"],
                        "search_access": True,
                        "detail_access": True,
                        "download_access": None,
                        "message": "登录状态已保存在工作区浏览器 profile 中。",
                    }
            return {
                "status": "manual_action_required",
                "method": method,
                "profile": profile,
                "message": "登录等待超时。浏览器保持登录状态；请重新调用 cnki_login 继续检测。",
            }

        return await self.browser.run(operation, profile=profile, visible=True, keep_page=True)

    async def session_status(self, profile: str = "default") -> dict[str, Any]:
        async def operation(page: Page, _: BrowserContext) -> dict[str, Any]:
            await page.goto(CNKI_HOME, wait_until="domcontentloaded", timeout=60_000)
            state = await detect_access(page)
            return {
                "status": "captcha_required" if state["captcha_required"] else "ready",
                "profile": profile,
                **state,
            }

        return await self.browser.run(operation, profile=profile, visible=True, background=True)

    async def search(
        self,
        query: str,
        *,
        mode: str = "balanced",
        limit: int | None = None,
        year_from: int | None = None,
        year_to: int | None = None,
        profile: str = "default",
        progress: Any = None,
    ) -> dict[str, Any]:
        limit = min(max(limit, 1), 100) if limit is not None else None
        plan = build_plan(query, mode)
        search_id = "search_" + uuid.uuid4().hex[:16]

        async def operation(page: Page, _: BrowserContext) -> dict[str, Any]:
            raw: list[Paper] = []
            warnings: list[str] = []
            for index, cell in enumerate(plan.cells, start=1):
                if progress:
                    await progress(index - 1, len(plan.cells), f"执行 {cell.cell_id}: {cell.purpose}")
                try:
                    raw.extend(await self._execute_cell(page, cell, year_from, year_to))
                except CaptchaRequired as exc:
                    warnings.append(str(exc))
                    break
                except Exception as exc:
                    warnings.append(f"{cell.cell_id} 执行失败: {exc}")

            unique = deduplicate(raw)
            weights = {cell.cell_id: cell.weight for cell in plan.cells}
            ranked = rerank(query, unique, weights, plan.search_terms, plan.concepts)
            base_stats = {
                "cells_planned": len(plan.cells),
                "raw_results": len(raw),
                "unique_results": len(unique),
                "metadata_enriched": 0,
            }
            for paper in ranked:
                self.store.save_paper(paper)
            self.store.save_search(
                search_id,
                query,
                "enriching",
                plan,
                [paper.paper_id for paper in ranked],
                base_stats,
            )
            rerank_candidates = dynamic_rerank_candidates(len(ranked), limit)
            detail_target = ranked[:rerank_candidates]

            detail_deadline = time.monotonic() + 75
            for index, paper in enumerate(detail_target, start=1):
                if time.monotonic() >= detail_deadline:
                    warnings.append("摘要重排达到 75 秒总预算，已使用现有元数据返回结果")
                    break
                if progress:
                    await progress(
                        len(plan.cells) + index,
                        len(plan.cells) + len(detail_target),
                        f"读取摘要 {index}/{len(detail_target)}",
                    )
                try:
                    async with asyncio.timeout(15):
                        await self._enrich_paper(page, paper)
                except TimeoutError:
                    warnings.append(f"详情页读取超时，已跳过：{paper.title}")
                except CaptchaRequired as exc:
                    warnings.append(str(exc))
                    break
                except Exception as exc:
                    warnings.append(f"元数据未完整获取：{paper.title} ({exc})")
                await self._delay(0.4, 0.9)

            feedback_terms: list[str] = []
            if mode != "precise" and time.monotonic() < detail_deadline:
                preliminary = rerank(query, unique, weights, plan.search_terms, plan.concepts)
                feedback_terms = mine_feedback_terms(preliminary, plan.search_terms)
                if feedback_terms:
                    next_id = max((int(cell.cell_id[1:]) for cell in plan.cells), default=0) + 1
                    feedback_cells = build_feedback_cells(next_id, plan.search_terms, feedback_terms)
                    for cell in feedback_cells:
                        if time.monotonic() >= detail_deadline:
                            break
                        plan.cells.append(cell)
                        try:
                            raw.extend(await self._execute_cell(page, cell, year_from, year_to))
                        except CaptchaRequired as exc:
                            warnings.append(str(exc))
                            break
                        except Exception as exc:
                            warnings.append(f"{cell.cell_id} 执行失败: {exc}")
                    unique = deduplicate(raw)
                    weights = {cell.cell_id: cell.weight for cell in plan.cells}
                    feedback_ranked = rerank(query, unique, weights, plan.search_terms, plan.concepts)
                    followup_target = [
                        paper
                        for paper in feedback_ranked
                        if not paper.metadata_complete
                        and paper.score_breakdown.get("relevance_eligible", 0.0) > 0
                    ][:5]
                    for paper in followup_target:
                        if time.monotonic() >= detail_deadline:
                            break
                        try:
                            async with asyncio.timeout(15):
                                await self._enrich_paper(page, paper)
                        except TimeoutError:
                            warnings.append(f"反馈候选详情页读取超时，已跳过：{paper.title}")
                        except CaptchaRequired as exc:
                            warnings.append(str(exc))
                            break
                        except Exception as exc:
                            warnings.append(f"反馈候选元数据未完整获取：{paper.title} ({exc})")
                        await self._delay(0.4, 0.9)

            ranked = rerank(query, unique, weights, plan.search_terms, plan.concepts)
            for paper in ranked:
                self.store.save_paper(paper)

            relevant_ranked = [
                paper
                for paper in ranked
                if paper.score_breakdown.get("relevance_eligible", 0.0) > 0
            ]

            adaptive, selection_stats = select_adaptive_results(relevant_ranked)
            selected = adaptive[:limit] if limit is not None else adaptive
            if limit is not None:
                selection_stats["selection_policy"] = "adaptive_quality_with_limit"
                selection_stats["requested_limit"] = limit

            stats = {
                "cells_planned": len(plan.cells),
                "raw_results": len(raw),
                "unique_results": len(unique),
                "metadata_enriched": sum(1 for paper in ranked if paper.metadata_complete),
                "quality_candidates_targeted": rerank_candidates,
                "relevant_results": len(relevant_ranked),
                "filtered_irrelevant": len(ranked) - len(relevant_ranked),
                "feedback_terms": feedback_terms,
                "returned_results": len(selected),
                **selection_stats,
            }
            status = "partial" if warnings else "completed"
            self.store.save_search(search_id, query, status, plan, [p.paper_id for p in relevant_ranked], stats)
            return {
                "search_id": search_id,
                "status": status,
                "query": query,
                "results": [
                    {"title": paper.title, "detail_url": paper.detail_url}
                    for paper in selected
                ],
                "stats": stats,
                "metadata_cached": True,
                "warnings": warnings,
            }

        return await self.browser.run(operation, profile=profile, visible=True, background=True)

    async def structured_search(
        self,
        conditions: list[dict[str, Any]],
        *,
        year_from: int | None = None,
        year_to: int | None = None,
        sort_by: str = "relevance",
        pages: int = 2,
        limit: int | None = None,
        metadata_depth: str = "rerank",
        rerank_candidates: int = 30,
        source_tiers: list[str] | None = None,
        document_types: list[str] | None = None,
        profile: str = "default",
        progress: Any = None,
    ) -> dict[str, Any]:
        normalized = normalize_conditions(conditions)
        if metadata_depth not in {"basic", "rerank", "full"}:
            raise ValueError("metadata_depth 必须是 basic、rerank 或 full")
        if sort_by not in {"relevance", "date", "cited", "downloads"}:
            raise ValueError("sort_by 必须是 relevance、date、cited 或 downloads")
        if year_from and year_to and year_from > year_to:
            raise ValueError("year_from 不能晚于 year_to")
        pages = min(max(pages, 1), 10)
        limit = min(max(limit, 1), 100) if limit is not None else None
        rerank_candidates = min(max(rerank_candidates, limit or 1), 100)
        source_tiers = [item.strip() for item in (source_tiers or []) if item.strip()]
        document_types = [item.strip() for item in (document_types or []) if item.strip()]
        description = describe_conditions(normalized)
        anchor_terms = [item["value"] for item in normalized if item["operator"] != "NOT"]
        anchor = " ".join(anchor_terms) or normalized[0]["value"]
        search_id = "search_" + uuid.uuid4().hex[:16]
        cell = SearchCell("STRUCTURED", "subject", description, "用户指定的多字段高级检索", 1.0, pages, "advanced")
        plan = SearchPlan(anchor, "structured", anchor_terms, [cell], anchor_terms, [])

        async def operation(page: Page, _: BrowserContext) -> dict[str, Any]:
            if progress:
                await progress(0, 2, "提交结构化高级检索")
            fixed_match_fields = await self._submit_structured_search(page, normalized, year_from, year_to)
            await self._wait_for_results(page)
            await self._check_page_state(page)
            sort_applied = await self._apply_result_sort(page, sort_by)
            raw: list[Paper] = []
            for page_number in range(1, pages + 1):
                rows = page.locator("table.result-table-list tbody tr")
                if not await rows.count():
                    rows = page.locator(".result-table-list tbody tr")
                for row_index in range(await rows.count()):
                    paper = await self._parse_row(rows.nth(row_index), cell, page_number, row_index + 1)
                    if paper and self._within_year(paper.year, year_from, year_to):
                        raw.append(paper)
                if page_number >= pages or not await self._next_page(page):
                    break
                await self._wait_for_results(page)

            unique = deduplicate(raw)
            ranked = rerank(anchor, unique, {cell.cell_id: 1.0}, anchor_terms, anchor_terms)
            for paper in ranked:
                self.store.save_paper(paper)
            detail_target = ranked if metadata_depth == "full" else ranked[:rerank_candidates] if metadata_depth == "rerank" else []
            warnings: list[str] = []
            if fixed_match_fields:
                warnings.append(
                    "CNKI 将以下字段的精确/模糊控件锁定为字段默认语义："
                    + "、".join(dict.fromkeys(fixed_match_fields))
                )
            deadline = time.monotonic() + 75
            for index, paper in enumerate(detail_target, 1):
                if time.monotonic() >= deadline:
                    warnings.append("详情增强达到 75 秒预算，已使用现有元数据返回")
                    break
                if progress:
                    await progress(index, max(len(detail_target), 1), f"读取元数据 {index}/{len(detail_target)}")
                try:
                    async with asyncio.timeout(15):
                        await self._enrich_paper(page, paper)
                except TimeoutError:
                    warnings.append(f"详情页读取超时：{paper.title}")
                except CaptchaRequired as exc:
                    warnings.append(str(exc))
                    break
                except Exception as exc:
                    warnings.append(f"元数据未完整获取：{paper.title} ({exc})")
                await self._delay(0.4, 0.9)

            ranked = rerank(anchor, unique, {cell.cell_id: 1.0}, anchor_terms, anchor_terms)
            for paper in ranked:
                # CNKI has already enforced the explicit field constraints. Keep
                # the local ranker for quality ordering without rejecting an
                # author-only or DOI-only search as semantically irrelevant.
                quality = paper.score_breakdown.get("quality_raw", 0.0)
                paper.score_breakdown["structured_constraints"] = 1.0
                paper.score_breakdown["relevance_eligible"] = 1.0
                paper.score_breakdown["relevance_raw"] = max(
                    paper.score_breakdown.get("relevance_raw", 0.0), 0.4
                )
                paper.score = max(paper.score, 0.4 * (0.35 + 0.65 * quality) + 0.2 * quality)
                self.store.save_paper(paper)

            filtered = [
                paper for paper in ranked
                if (not source_tiers or any(tier in paper.source_tiers for tier in source_tiers))
                and (not document_types or paper.resource_type in document_types)
            ]
            if metadata_depth == "basic":
                selected = filtered[: limit or 20]
                selection_stats = {"selection_policy": "structured_basic", "requested_limit": limit}
            else:
                adaptive, selection_stats = select_adaptive_results(filtered)
                selected = adaptive[:limit] if limit is not None else adaptive
                # Explicit field constraints are authoritative; when quality
                # metadata is unavailable, return the constrained result set
                # rather than pretending the search found nothing.
                if not selected and filtered:
                    selected = filtered[: limit or min(20, len(filtered))]
                    selection_stats["selection_policy"] = "structured_constraint_fallback"
            stats = {
                "raw_results": len(raw),
                "unique_results": len(unique),
                "metadata_enriched": sum(1 for paper in ranked if paper.metadata_complete),
                "post_filter_results": len(filtered),
                "returned_results": len(selected),
                "sort_requested": sort_by,
                "sort_applied": sort_applied,
                **selection_stats,
            }
            self.store.save_search(search_id, description, "partial" if warnings else "completed", plan, [paper.paper_id for paper in filtered], stats)
            return {
                "search_id": search_id,
                "status": "partial" if warnings else "completed",
                "conditions": normalized,
                "results": [{"title": paper.title, "detail_url": paper.detail_url} for paper in selected],
                "stats": stats,
                "metadata_cached": True,
                "warnings": warnings,
            }

        return await self.browser.run(operation, profile=profile, visible=True, background=True)

    async def _submit_structured_search(
        self,
        page: Page,
        conditions: list[dict[str, str]],
        year_from: int | None,
        year_to: int | None,
    ) -> list[str]:
        await page.goto(CNKI_ADVANCED, wait_until="domcontentloaded", timeout=60_000)
        await self._delay()
        rows = page.locator("#gradetxt > dd")
        while await rows.count() < len(conditions):
            add = page.locator("#gradetxt .add-group").last
            if not await add.count():
                raise RuntimeError("无法增加 CNKI 高级检索条件行，页面可能已改版")
            await add.click()
            await page.wait_for_timeout(100)
        fixed_match_fields: list[str] = []
        for index, condition in enumerate(conditions):
            row = rows.nth(index)
            field_box = row.locator(".sort.reopt").first
            await field_box.locator(".sort-default").click()
            await field_box.locator(f'a[title="{condition["field_label"]}"]').first.click()
            await row.locator("input[data-tipid]").first.fill(condition["value"])
            match_box = row.locator(".sort.special").first
            if await match_box.count():
                classes = await match_box.get_attribute("class") or ""
                if "disableclick" in classes:
                    fixed_match_fields.append(condition["field_label"])
                else:
                    desired = "=" if condition["match"] == "exact" else "%"
                    current = await match_box.locator(".sort-default span").first.get_attribute("value")
                    if current != desired:
                        await match_box.locator(".sort-default").click()
                        await match_box.locator(f'a[value="{desired}"]').first.click()
            if index:
                logical = row.locator(".sort.logical").first
                await logical.locator(".sort-default").click()
                await logical.locator(f'a[value="{condition["operator"]}"]').first.click()
        bilingual = page.locator('.gradeSearch input[data-id="EN"]:checked').first
        if await bilingual.count():
            await bilingual.uncheck()
        for selector, value in (("#datebox0", f"{year_from}-01-01" if year_from else None), ("#datebox1", f"{year_to}-12-31" if year_to else None)):
            if value:
                await page.locator(selector).evaluate(
                    "(element, newValue) => { element.value = newValue; element.dispatchEvent(new Event('change', {bubbles:true})); }",
                    value,
                )
        button = page.locator(".search-middle input.btn-search:visible").first
        if not await button.count():
            raise RuntimeError("无法定位 CNKI 高级检索按钮，页面可能已改版")
        await button.click()
        return fixed_match_fields

    async def _apply_result_sort(self, page: Page, sort_by: str) -> bool:
        if sort_by == "relevance":
            return True
        labels = {"date": r"发表时间|时间", "cited": r"被引", "downloads": r"下载"}
        try:
            locator = page.get_by_text(re.compile(rf"^({labels[sort_by]})$"), exact=False).first
            if not await locator.count() or not await locator.is_visible():
                return False
            await locator.click()
            await self._delay(0.4, 0.8)
            await self._wait_for_results(page)
            return True
        except Exception:
            return False

    async def _execute_cell(
        self, page: Page, cell: SearchCell, year_from: int | None, year_to: int | None
    ) -> list[Paper]:
        if cell.strategy == "advanced":
            await self._submit_advanced_search(page, cell, year_from, year_to)
        else:
            await page.goto(CNKI_HOME, wait_until="domcontentloaded", timeout=60_000)
            await self._delay()
            await self._select_field(page, cell.field)
            search_box = page.locator("#txt_SearchText").first
            if not await search_box.count():
                search_box = page.get_by_placeholder(re.compile(r"检索|搜索|关键词")).first
            await search_box.fill(cell.query)
            button = page.locator(".search-btn").first
            if not await button.count():
                button = page.get_by_role("button", name=re.compile(r"检索|搜索")).first
            await button.click()
        await self._wait_for_results(page)
        await self._check_page_state(page)
        papers: list[Paper] = []
        for page_number in range(1, cell.pages + 1):
            rows = page.locator("table.result-table-list tbody tr")
            count = await rows.count()
            if not count:
                rows = page.locator(".result-table-list tbody tr")
                count = await rows.count()
            for row_index in range(count):
                paper = await self._parse_row(rows.nth(row_index), cell, page_number, row_index + 1)
                if paper and self._within_year(paper.year, year_from, year_to):
                    papers.append(paper)
            if page_number >= cell.pages or not await self._next_page(page):
                break
            await self._wait_for_results(page)
        return papers

    async def _submit_advanced_search(
        self,
        page: Page,
        cell: SearchCell,
        year_from: int | None,
        year_to: int | None,
    ) -> None:
        await page.goto(CNKI_ADVANCED, wait_until="domcontentloaded", timeout=60_000)
        await self._delay()
        row = page.locator("#gradetxt > dd").first
        search_box = row.locator("input[data-tipid]").first
        if not await search_box.count():
            raise RuntimeError("无法定位知网高级检索条件框，页面可能已改版")
        if cell.field != "subject":
            field_box = row.locator(".sort.reopt").first
            await field_box.locator(".sort-default").click()
            option = field_box.locator(f'.sort-list a[title="{FIELD_LABELS[cell.field]}"]').first
            await option.click()
        await search_box.fill(cell.query)

        # The query matrix already supplies controlled aliases. Disable CNKI's
        # opaque bilingual expansion to keep recall changes explainable.
        bilingual = page.locator('.gradeSearch input[data-id="EN"]:checked').first
        if await bilingual.count():
            await bilingual.uncheck()
        for selector, value in (
            ("#datebox0", f"{year_from}-01-01" if year_from else None),
            ("#datebox1", f"{year_to}-12-31" if year_to else None),
        ):
            if value:
                await page.locator(selector).evaluate(
                    "(element, newValue) => { element.value = newValue; element.dispatchEvent(new Event('change', {bubbles:true})); }",
                    value,
                )
        button = page.locator(".search-middle input.btn-search:visible").first
        if not await button.count():
            button = page.locator("input.btn-search:visible").first
        if not await button.count():
            raise RuntimeError("无法定位知网高级检索按钮，页面可能已改版")
        await button.click()

    async def _select_field(self, page: Page, field: str) -> None:
        if field == "subject":
            return
        try:
            await page.locator("#DBFieldBox").click(timeout=5_000)
            value = FIELD_VALUES[field]
            option = page.locator(f'#DBFieldList a[value="{value}"]').first
            if not await option.count():
                option = page.get_by_text(FIELD_LABELS[field], exact=True).last
            await option.click(timeout=5_000)
        except Exception as exc:
            raise RuntimeError(f"无法选择检索字段“{FIELD_LABELS[field]}”，CNKI 页面可能已改版") from exc

    async def _wait_for_results(self, page: Page) -> None:
        try:
            await page.wait_for_url(re.compile(r"kns\.cnki\.net"), timeout=30_000)
        except PlaywrightTimeoutError:
            pass
        try:
            await page.locator("table.result-table-list, .result-table-list, .no-result").first.wait_for(
                state="visible", timeout=30_000
            )
        except PlaywrightTimeoutError:
            await self._check_page_state(page)

    async def _check_page_state(self, page: Page) -> None:
        body = await page_text(page)
        if await page.locator("table.result-table-list tbody tr").count():
            return
        state = await detect_access(page)
        if state["captcha_required"]:
            raise CaptchaRequired("CNKI 要求人工验证，已保留当前部分结果；请调用 cnki_login 在可见浏览器中完成验证")
        if any(marker in body for marker in ("访问过于频繁", "系统检测到异常访问")):
            raise CaptchaRequired("CNKI 暂时限制访问，已停止后续请求并保留部分结果")

    async def _parse_row(
        self, row: Locator, cell: SearchCell, page_number: int, rank: int
    ) -> Paper | None:
        title_link = row.locator("a.fz14").first
        if not await title_link.count():
            title_link = row.locator("td.name a, a[href*='Detail']").first
        if not await title_link.count():
            return None
        title = (await title_link.inner_text()).strip()
        href = await title_link.get_attribute("href") or ""
        url = urljoin(CNKI_HOME, href)
        author_text = await self._locator_text(row.locator("td.author"))
        source = await self._locator_text(row.locator("td.source"))
        source_link = row.locator("td.source a").first
        source_href = await source_link.get_attribute("href") if await source_link.count() else None
        row_text = await self._locator_text(row)
        marker_text = ""
        try:
            marker_text = " ".join(
                await row.locator("[title], img[alt]").evaluate_all(
                    "els => els.map(e => e.getAttribute('title') || e.getAttribute('alt') || '').filter(Boolean)"
                )
            )
        except Exception:
            pass
        classification_text = " ".join(filter(None, (url, source_href, row_text, marker_text)))
        date = await self._locator_text(row.locator("td.date"))
        cited = await self._locator_text(row.locator("td.quote"))
        year_match = re.search(r"(?:19|20)\d{2}", date or "")
        authors = [item.strip() for item in re.split(r"[,，;；\s]+", author_text or "") if item.strip()]
        year = int(year_match.group()) if year_match else None
        return Paper(
            paper_id=_paper_id(title, authors, source, year),
            title=title,
            detail_url=url,
            authors=authors,
            source=source,
            resource_type=_resource_type(classification_text),
            # Only explicit result badges are trustworthy. Opaque CNKI URLs can
            # coincidentally contain strings such as "EI" and must not be scanned.
            source_tiers=_source_tiers(marker_text),
            year=year,
            cited_by=_integer(cited),
            matched_cells=[cell.cell_id],
            ranks={cell.cell_id: (page_number - 1) * 20 + rank},
        )

    @staticmethod
    async def _locator_text(locator: Locator) -> str | None:
        try:
            if await locator.count():
                return (await locator.first.inner_text()).strip()
        except Exception:
            return None
        return None

    async def _next_page(self, page: Page) -> bool:
        button = page.locator("#PageNext").first
        if not await button.count():
            button = page.get_by_text(re.compile(r"^下一页$|^下页$")).first
        try:
            if not await button.count() or not await button.is_enabled():
                return False
            await button.click()
            await self._delay()
            return True
        except Exception:
            return False

    async def _enrich_paper(self, page: Page, paper: Paper) -> None:
        await page.goto(paper.detail_url, wait_until="commit", timeout=15_000)
        try:
            await page.wait_for_load_state("domcontentloaded", timeout=10_000)
        except PlaywrightTimeoutError:
            pass
        await self._check_page_state(page)
        body = await page_text(page)
        paper.title = (
            await _meta_content(page, ["citation_title", "DC.Title"])
            or await _first_text(page, ["h1", ".wx-tit h1", ".brief h1", ".title"])
            or paper.title
        )
        author_metas = await _meta_contents(page, ["citation_author", "DC.Creator"])
        author_text = await _first_text(page, [".author", ".authors", ".brief .author"])
        if author_metas:
            paper.authors = author_metas
        elif author_text:
            paper.authors = [x.strip() for x in re.split(r"[,，;；\s]+", author_text) if x.strip()]
        paper.abstract = (
            await _meta_content(page, ["description", "DC.Description", "citation_abstract"])
            or await _first_text(page, ["#ChDivSummary", ".abstract-text", ".abstract", "span.abstract-text"])
            or paper.abstract
        )
        keyword_text = await _meta_content(page, ["keywords", "DC.Subject"]) or await _first_text(
            page, [".keywords", "p.keywords", "#catalog_KEYWORD"]
        )
        if keyword_text:
            keyword_text = re.sub(r"^关键词[:：]?", "", keyword_text)
            paper.keywords = [x.strip() for x in re.split(r"[;；,，\s]+", keyword_text) if x.strip()]
        paper.doi = await _meta_content(page, ["citation_doi", "DC.Identifier"]) or self._extract_labeled(body, "DOI")
        journal_source = await _meta_content(page, ["citation_journal_title"])
        conference_source = await _meta_content(page, ["citation_conference_title"])
        source = journal_source or conference_source
        paper.source = source or paper.source
        paper.publish_date = await _meta_content(page, ["citation_publication_date", "DC.Date"])
        paper.volume = await _meta_content(page, ["citation_volume", "prism.volume"]) or paper.volume
        paper.issue = await _meta_content(page, ["citation_issue", "prism.number"]) or paper.issue
        first_page = await _meta_content(page, ["citation_firstpage", "prism.startingPage"])
        last_page = await _meta_content(page, ["citation_lastpage", "prism.endingPage"])
        if first_page:
            paper.pages = first_page if not last_page or last_page == first_page else f"{first_page}-{last_page}"
        institution_text = await _first_text(page, [".orgn", ".author-org", ".organization", "#catalog_ORG"])
        if institution_text:
            paper.institutions = [x.strip() for x in re.split(r"[;；\n]+", institution_text) if x.strip()]
        fund_text = await _first_text(page, [".funds", ".fund", "#catalog_FUND"])
        if fund_text:
            paper.funds = [x.strip() for x in re.split(r"[;；\n]+", re.sub(r"^基金[:：]?", "", fund_text)) if x.strip()]
        record_context = _record_context(body, paper.title)
        detected_type = "期刊" if journal_source else "会议论文" if conference_source else _resource_type(
            paper.detail_url + " " + record_context
        )
        paper.resource_type = detected_type or paper.resource_type
        if paper.resource_type == "期刊" and paper.source:
            cached_source = self._source_cache.get(paper.source)
            if cached_source is None:
                cached_source = {
                    "tiers": [],
                    "composite_impact_factor": None,
                    "comprehensive_impact_factor": None,
                }
                source_link = page.locator("a[title*='数据库收录来源']").first
                try:
                    if await source_link.count():
                        source_url = await source_link.get_attribute("href")
                        if source_url:
                            await page.goto(source_url, wait_until="commit", timeout=8_000)
                            try:
                                await page.wait_for_load_state("domcontentloaded", timeout=5_000)
                            except PlaywrightTimeoutError:
                                pass
                            source_body = await page_text(page)
                            cached_source = {
                                "tiers": _source_tiers(source_body),
                                "composite_impact_factor": _decimal_after_label(source_body, "复合影响因子"),
                                "comprehensive_impact_factor": _decimal_after_label(source_body, "综合影响因子"),
                            }
                except PlaywrightTimeoutError:
                    pass
                self._source_cache[paper.source] = cached_source
            for tier in cached_source["tiers"]:
                if tier not in paper.source_tiers:
                    paper.source_tiers.append(tier)
            paper.composite_impact_factor = cached_source["composite_impact_factor"]
            paper.comprehensive_impact_factor = cached_source["comprehensive_impact_factor"]
        paper.metadata_complete = bool(paper.abstract and paper.keywords)

    @staticmethod
    def _extract_labeled(body: str, label: str) -> str | None:
        match = re.search(rf"{re.escape(label)}[:：]\s*([^\s\n;；]+)", body, flags=re.IGNORECASE)
        return match.group(1).strip() if match else None

    async def _resolve_paper_title(
        self,
        title: str,
        profile: str = "default",
        *,
        refresh_locator: bool = False,
    ) -> Paper:
        title = title.strip()
        if not title:
            raise ValueError("title 不能为空")
        cached = self.store.find_paper_by_title(title)
        candidates = cached
        if refresh_locator or not candidates:
            online = await self._lookup_title(title, profile)
            candidates = list({paper.paper_id: paper for paper in [*cached, *online]}.values())
        scored = sorted(
            ((text_similarity(title, paper.title), paper) for paper in candidates),
            key=lambda item: (
                normalize_title(title) == normalize_title(item[1].title),
                item[0],
                item[1].metadata_complete,
                item[1].score,
            ),
            reverse=True,
        )
        exact_matches = [paper for _, paper in scored if normalize_title(title) == normalize_title(paper.title)]
        if not exact_matches:
            raise KeyError(f"未找到完整标题对应的 CNKI 论文: {title}")
        exact_matches.sort(key=lambda paper: (paper.metadata_complete, paper.score, paper.year or 0), reverse=True)
        return exact_matches[0]

    @staticmethod
    def _public_paper(paper: Paper) -> dict[str, Any]:
        data = paper.to_dict()
        data.pop("paper_id", None)
        return data

    async def get_metadata(
        self,
        *,
        titles: list[str] | None = None,
        search_id: str | None = None,
        offset: int = 0,
        limit: int = 20,
        fields: list[str] | None = None,
        refresh: bool = False,
        profile: str = "default",
    ) -> dict[str, Any]:
        if not titles and not search_id:
            raise ValueError("必须提供 titles 或 search_id")

        papers_by_id: dict[str, Paper] = {}
        unresolved_titles: list[str] = []
        for title in dict.fromkeys(item.strip() for item in (titles or []) if item.strip()):
            try:
                paper = await self._resolve_paper_title(title, profile)
                papers_by_id[paper.paper_id] = paper
            except (KeyError, ValueError):
                unresolved_titles.append(title)

        if search_id:
            search = self.store.get_search(search_id)
            if not search:
                raise KeyError(f"未找到 search_id: {search_id}")
            for internal_id in search["result_ids"]:
                paper = self.store.get_paper(internal_id)
                if paper:
                    papers_by_id.setdefault(paper.paper_id, paper)

        all_papers = list(papers_by_id.values())
        start = max(offset, 0)
        selected = all_papers[start : start + min(max(limit, 1), 100)]
        enrichment_targets = [paper for paper in selected if refresh or not paper.metadata_complete]
        if enrichment_targets:
            async def operation(page: Page, _: BrowserContext) -> None:
                deadline = time.monotonic() + 75
                for paper in enrichment_targets:
                    if time.monotonic() >= deadline:
                        break
                    try:
                        async with asyncio.timeout(15):
                            await self._enrich_paper(page, paper)
                    except (TimeoutError, CaptchaRequired):
                        break
                    except Exception:
                        continue
                    self.store.save_paper(paper)

            await self.browser.run(operation, profile=profile, visible=True, background=True)

        allowed = [field for field in (fields or []) if field != "paper_id"]
        results: list[dict[str, Any]] = []
        for paper in selected:
            public = self._public_paper(paper)
            results.append(
                {key: public.get(key) for key in allowed if key in public}
                if allowed
                else public
            )
        return {
            "search_id": search_id,
            "offset": start,
            "count": len(results),
            "total": len(all_papers),
            "has_more": start + len(results) < len(all_papers),
            "unresolved_titles": unresolved_titles,
            "results": results,
        }

    async def download(
        self,
        title: str,
        subdirectory: str | None = None,
        preferred_format: str = "pdf",
        overwrite: bool = False,
        profile: str = "default",
    ) -> dict[str, Any]:
        paper = await self._resolve_paper_title(title, profile, refresh_locator=True)
        if preferred_format not in {"pdf", "caj", "auto"}:
            raise ValueError("preferred_format 必须是 pdf、caj 或 auto")
        target_dir = safe_download_directory(subdirectory)
        if paper.local_file and Path(paper.local_file).exists() and not overwrite:
            cached_path = Path(paper.local_file)
            return {
                "status": "cached",
                "title": paper.title,
                "file_path": paper.local_file,
                "format": cached_path.suffix.lstrip(".").lower() or None,
                "file_size": cached_path.stat().st_size,
            }

        async def operation(page: Page, _: BrowserContext) -> dict[str, Any]:
            await page.goto(paper.detail_url, wait_until="domcontentloaded", timeout=60_000)
            await self._check_page_state(page)
            candidates = []
            if preferred_format in {"pdf", "auto"}:
                candidates.extend(["PDF下载", "PDF", "下载PDF"])
            if preferred_format in {"caj", "auto"}:
                candidates.extend(["CAJ下载", "CAJ", "下载CAJ"])
            link = None
            for name in candidates:
                locator = page.get_by_text(name, exact=False).first
                if await locator.count():
                    link = locator
                    break
            if link is None:
                raise PermissionError("当前详情页未发现可用下载入口，可能没有下载权限或页面结构已变化")
            async with page.expect_download(timeout=60_000) as info:
                await link.click()
            download = await info.value
            suggested = download.suggested_filename
            suffix = Path(suggested).suffix or (".pdf" if preferred_format == "pdf" else ".caj")
            safe_title = re.sub(r"[\\/:*?\"<>|]", "_", paper.title).strip()[:120]
            destination = target_dir / f"{safe_title}{suffix}"
            if destination.exists() and not overwrite:
                return {
                    "status": "cached",
                    "file_path": str(destination),
                    "format": destination.suffix.lstrip(".").lower() or None,
                    "file_size": destination.stat().st_size,
                }
            await download.save_as(str(destination))
            return {"status": "downloaded", "file_path": str(destination), "format": suffix.lstrip(".").lower(), "file_size": destination.stat().st_size}

        result = await self.browser.run(operation, profile=profile, visible=True, background=True)
        paper.local_file = result["file_path"]
        self.store.save_paper(paper)
        return {"title": paper.title, **result}

    async def read_online_html(
        self,
        title: str,
        *,
        read_all: bool = True,
        offset: int = 0,
        max_characters: int = 5_000,
        section: str | None = None,
        profile: str = "default",
    ) -> dict[str, Any]:
        paper = await self._resolve_paper_title(title, profile, refresh_locator=True)
        max_characters = min(max(max_characters, 500), 12_000)
        offset = max(offset, 0)

        async def operation(page: Page, context: BrowserContext) -> dict[str, Any]:
            await page.goto(paper.detail_url, wait_until="domcontentloaded", timeout=60_000)
            await self._check_page_state(page)
            link = page.get_by_text(re.compile(r"^HTML\s*阅读$", re.IGNORECASE)).first
            if not await link.count():
                raise PermissionError("该论文详情页没有提供“HTML阅读”入口；可能不支持在线 HTML 或当前权限不可用")
            href = await link.get_attribute("href")
            if not href:
                raise PermissionError("HTML 阅读入口没有可访问链接")
            target = urljoin(page.url, href)
            if not is_cnki_url(target):
                raise PermissionError("HTML 阅读入口跳转到了非 CNKI 域名，已拒绝访问")
            reader_page = page
            try:
                async with context.expect_page(timeout=10_000) as page_info:
                    await link.click()
                reader_page = await page_info.value
            except PlaywrightTimeoutError:
                # Some CNKI reader variants navigate the current tab instead of
                # opening a new one. The click has already happened.
                reader_page = page
            try:
                await reader_page.wait_for_load_state("domcontentloaded", timeout=30_000)
            except PlaywrightTimeoutError:
                pass
            await reader_page.wait_for_timeout(2_000)
            if not is_cnki_url(reader_page.url):
                raise PermissionError("HTML 阅读最终页面不属于 CNKI，已拒绝提取")
            state = await detect_access(reader_page)
            if state["captcha_required"]:
                raise CaptchaRequired("CNKI HTML 阅读要求人工验证，请先调用 cnki_login 完成验证")

            candidates: list[str] = []
            selectors = (
                "article",
                ".article-content",
                ".reader-content",
                ".html-content",
                ".content-main",
                "#content",
                ".content",
                "body",
            )
            for frame in reader_page.frames:
                for selector in selectors:
                    try:
                        locator = frame.locator(selector).first
                        if await locator.count():
                            text = clean_online_text(await locator.inner_text(timeout=8_000))
                            if len(text) >= 200:
                                candidates.append(text)
                    except Exception:
                        continue
            if not candidates:
                raise PermissionError("HTML 阅读页面未返回可提取正文；可能尚未获得正文权限或页面结构已变化")
            full_text = max(candidates, key=len)
            if any(marker in full_text[:2_000] for marker in ACCESS_DENIED_MARKERS):
                raise PermissionError("CNKI 返回了 HTML 正文权限提示，当前账号或机构没有可用阅读权限")
            section_start = 0
            if section:
                section_start = full_text.casefold().find(section.strip().casefold())
                if section_start < 0:
                    raise KeyError(f"在线正文中未找到章节或文本: {section}")
            start = min(section_start + offset, len(full_text))
            if read_all:
                # A generous guard prevents a malformed reader page from
                # returning unbounded navigation/debug text. Normal articles
                # are far below this ceiling and are returned in full.
                safety_limit = 500_000
                end = min(len(full_text), start + safety_limit)
                content = full_text[start:end].strip()
                sliced = {
                    "content": content,
                    "offset": start,
                    "next_offset": end if end < len(full_text) else None,
                    "has_more": end < len(full_text),
                    "total_characters": len(full_text),
                    "returned_characters": len(content),
                }
            else:
                sliced = slice_online_text(full_text, start, max_characters)
            access_status, safe_reader_url, access_evidence = reader_access(reader_page.url, full_text)
            result = {
                "status": access_status,
                "title": paper.title,
                # The query contains short-lived invoice/nonce parameters and
                # must never be returned to the model or logs.
                "reader_url": safe_reader_url,
                "access_evidence": access_evidence,
                "section": section,
                "section_start": section_start if section else None,
                "read_all": read_all,
                "complete_available_content": not sliced["has_more"],
                "complete_article": access_status == "authorized" and not sliced["has_more"],
                **sliced,
            }
            if reader_page is not page and not reader_page.is_closed():
                await reader_page.close()
            return result

        return await self.browser.run(operation, profile=profile, visible=True, background=True)

    async def export_citations(
        self,
        *,
        titles: list[str] | None = None,
        search_id: str | None = None,
        output_format: str = "gbt7714",
        custom_template: str | None = None,
        offset: int = 0,
        limit: int = 100,
        save_filename: str | None = None,
        profile: str = "default",
    ) -> dict[str, Any]:
        papers_by_id: dict[str, Paper] = {}
        unresolved_titles: list[str] = []
        for title in dict.fromkeys(titles or []):
            try:
                # Citation formatting needs bibliographic metadata, not a fresh
                # reader/download locator. An unseen title still performs an
                # exact lookup because the cache is empty.
                paper = await self._resolve_paper_title(title, profile)
                if not paper.metadata_complete:
                    async def operation(page: Page, _: BrowserContext, item: Paper = paper) -> None:
                        await self._enrich_paper(page, item)
                    await self.browser.run(operation, profile=profile, visible=True, background=True)
                    self.store.save_paper(paper)
                papers_by_id[paper.paper_id] = paper
            except Exception:
                unresolved_titles.append(title)
        ids: list[str] = []
        if search_id:
            search = self.store.get_search(search_id)
            if not search:
                raise KeyError(f"未找到 search_id: {search_id}")
            ids.extend(search["result_ids"])
        if not titles and not ids:
            raise ValueError("必须提供 titles 或 search_id")
        for internal_id in ids:
            paper = self.store.get_paper(internal_id)
            if paper:
                papers_by_id[paper.paper_id] = paper
        start = max(offset, 0)
        papers = list(papers_by_id.values())[start : start + min(max(limit, 1), 100)]
        content, extension = export_records(papers, output_format, custom_template)
        file_path = None
        if save_filename:
            filename = save_filename if Path(save_filename).suffix else f"{save_filename}.{extension}"
            destination = safe_export_path(filename)
            destination.write_text(content, encoding="utf-8", newline="")
            file_path = str(destination)
        return {
            "format": output_format,
            "count": len(papers),
            "unresolved_titles": unresolved_titles,
            "content": content if not file_path else content[:2_000],
            "content_truncated": bool(file_path and len(content) > 2_000),
            "file_path": file_path,
            "extension": extension,
        }

    async def link_references(
        self,
        text: str,
        *,
        min_confidence: float = 0.9,
        search_missing: bool = True,
        profile: str = "default",
    ) -> dict[str, Any]:
        titles = extract_marked_titles(text)
        linked = text
        matched: list[dict[str, Any]] = []
        unresolved: list[str] = []
        ambiguous: list[dict[str, Any]] = []
        for title in titles:
            candidates = self.store.find_paper_by_title(title)
            if search_missing:
                online = await self._lookup_title(title, profile)
                candidates = list({paper.paper_id: paper for paper in [*candidates, *online]}.values())
            scored = sorted(
                ((text_similarity(title, paper.title), paper) for paper in candidates),
                key=lambda item: item[0], reverse=True,
            )
            if not scored:
                unresolved.append(title)
                continue
            confidence, paper = scored[0]
            exact = normalize_title(title) == normalize_title(paper.title)
            confidence = max(confidence, 0.98 if exact else confidence)
            if confidence >= min_confidence:
                original = f"《{title}》"
                replacement = f"[《{title}》]({paper.detail_url})"
                linked = linked.replace(original, replacement)
                matched.append({"original": title, "title": paper.title, "confidence": round(confidence, 3), "detail_url": paper.detail_url})
            else:
                ambiguous.append({"original": title, "candidate": paper.title, "confidence": round(confidence, 3)})
        return {"linked_text": linked, "matched": matched, "ambiguous": ambiguous, "unresolved": unresolved}

    async def _lookup_title(self, title: str, profile: str) -> list[Paper]:
        cell = SearchCell("LINK", "title", title, "完整标题自动定位", 1.0, 3)
        async def operation(page: Page, _: BrowserContext) -> list[Paper]:
            conditions = normalize_conditions(
                [{"field": "title", "value": title, "operator": "AND", "match": "exact"}]
            )
            await self._submit_structured_search(page, conditions, None, None)
            await self._wait_for_results(page)
            await self._check_page_state(page)
            papers: list[Paper] = []
            for page_number in range(1, cell.pages + 1):
                rows = page.locator("table.result-table-list tbody tr")
                if not await rows.count():
                    rows = page.locator(".result-table-list tbody tr")
                for row_index in range(await rows.count()):
                    paper = await self._parse_row(rows.nth(row_index), cell, page_number, row_index + 1)
                    if paper:
                        papers.append(paper)
                if page_number >= cell.pages or not await self._next_page(page):
                    break
                await self._wait_for_results(page)
            merged: list[Paper] = []
            for fresh in papers[:60]:
                existing = next(
                    (
                        item
                        for item in self.store.find_paper_by_title(fresh.title)
                        if normalize_title(item.title) == normalize_title(fresh.title)
                    ),
                    None,
                )
                if existing:
                    # Refresh the expiring CNKI detail locator without replacing
                    # previously enriched abstract/keywords/quality metadata.
                    existing.detail_url = fresh.detail_url
                    existing.authors = fresh.authors or existing.authors
                    existing.source = fresh.source or existing.source
                    existing.year = fresh.year or existing.year
                    existing.resource_type = fresh.resource_type or existing.resource_type
                    for tier in fresh.source_tiers:
                        if tier not in existing.source_tiers:
                            existing.source_tiers.append(tier)
                    self.store.save_paper(existing)
                    merged.append(existing)
                else:
                    self.store.save_paper(fresh)
                    merged.append(fresh)
            return merged
        return await self.browser.run(operation, profile=profile, visible=True, background=True)

    @staticmethod
    def _within_year(year: int | None, start: int | None, end: int | None) -> bool:
        if start and year and year < start:
            return False
        if end and year and year > end:
            return False
        return True

    @staticmethod
    async def _delay(low: float | None = None, high: float | None = None) -> None:
        low = low if low is not None else float(os.getenv("CNKI_MCP_SEARCH_DELAY_MIN", "1.0"))
        high = high if high is not None else float(os.getenv("CNKI_MCP_SEARCH_DELAY_MAX", "2.0"))
        await asyncio.sleep(random.uniform(low, max(low, high)))


class CaptchaRequired(RuntimeError):
    pass
