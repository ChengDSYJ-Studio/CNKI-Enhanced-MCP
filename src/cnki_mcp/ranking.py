from __future__ import annotations

import math
import re
from collections import Counter
from datetime import date
from typing import Iterable

from .models import Paper


SOURCE_TIER_WEIGHTS = {
    "SCI": 1.0,
    "SSCI": 1.0,
    "A&HCI": 1.0,
    "CSSCI": 0.96,
    "北大核心": 0.94,
    "CSCD": 0.94,
    "EI": 0.90,
    "AMI核心": 0.88,
    "AMI扩展": 0.76,
    "AMI入库": 0.64,
    "核心期刊": 0.85,
}

RESOURCE_TYPE_WEIGHTS = {
    # Type is only one quality signal. An ordinary journal is not presumed elite.
    "期刊": 0.55,
    "博士论文": 0.72,
    "学位论文": 0.50,
    "硕士论文": 0.46,
    "本科论文": 0.10,
    "会议论文": 0.52,
    "图书章节": 0.34,
    "报纸": 0.14,
    "年鉴": 0.18,
    "其他": 0.25,
}

TIME_SUFFIXES = {"年", "月", "日", "时", "点", "分", "秒", "季度"}


def normalize_title(value: str) -> str:
    return re.sub(r"[\W_]+", "", value, flags=re.UNICODE).casefold()


def tokenize(value: str) -> list[str]:
    value = value.casefold().replace("–", "-").replace("—", "-").replace("－", "-")
    latin = re.findall(r"[a-z0-9]+", value)
    identifiers: list[str] = []
    identifier_pattern = (
        r"(?:[a-z]+(?:[-_.][a-z]+)*[ -]?\d+[a-z0-9]*(?:[._/\-]+[a-z0-9]+)*"
        r"|\d+(?:[._/\-]+\d+)+[a-z]+)"
    )
    for match in re.finditer(identifier_pattern, value):
        candidate = match.group()
        if not (re.search(r"[a-z]", candidate) and re.search(r"\d", candidate)):
            continue
        compact = re.sub(r"[\s._/\-]+", "", candidate)
        following = value[match.end() : match.end() + 2]
        if compact[-1:].isdigit() and any(following.startswith(suffix) for suffix in TIME_SUFFIXES):
            continue
        identifiers.append(compact)
        if re.fullmatch(r"p\d.*", compact):
            identifiers.append(compact[1:])
    chinese_chunks = re.findall(r"[\u4e00-\u9fff]+", value)
    grams: list[str] = []
    for chunk in chinese_chunks:
        if len(chunk) == 1:
            grams.append(chunk)
        else:
            grams.extend(chunk[i : i + 2] for i in range(len(chunk) - 1))
    return latin + identifiers + grams


def token_coverage(query: str, text: str | None) -> float:
    if not text:
        return 0.0
    q = Counter(tokenize(query))
    d = Counter(tokenize(text))
    if not q:
        return 0.0
    return sum(min(count, d[token]) for token, count in q.items()) / sum(q.values())


def _identifier_anchors(value: str) -> set[str]:
    return {token for token in tokenize(value) if re.search(r"[a-z]", token) and re.search(r"\d", token)}


def _term_present(term: str, text: str) -> bool:
    anchors = _identifier_anchors(term)
    if anchors:
        return bool(anchors & set(tokenize(text)))
    compact_term = normalize_title(term)
    if re.search(r"[\u4e00-\u9fff]", term) and len(compact_term) >= 2:
        return compact_term in normalize_title(text)
    return token_coverage(term, text) >= 0.8


def relevance_evidence(
    paper: Paper,
    query_terms: Iterable[str],
    concepts: Iterable[str],
) -> tuple[bool, float]:
    title_keywords = " ".join([paper.title, *paper.keywords])
    full_text = " ".join([title_keywords, paper.abstract or ""])
    terms = [term for term in query_terms if term]
    concept_list = [concept for concept in concepts if concept]
    anchor_hit = any(_term_present(term, full_text) for term in terms)
    concept_hits = sum(1 for concept in concept_list if _term_present(concept, full_text))
    concept_ratio = concept_hits / len(concept_list) if concept_list else 0.0
    non_identifier_terms = [term for term in terms if not _identifier_anchors(term)]
    title_coverage = max((token_coverage(term, title_keywords) for term in non_identifier_terms), default=0.0)
    abstract_coverage = max((token_coverage(term, paper.abstract) for term in non_identifier_terms), default=0.0)
    eligible = anchor_hit or concept_ratio >= 0.5 or title_coverage >= 0.45 or abstract_coverage >= 0.55
    evidence = max(1.0 if anchor_hit else 0.0, concept_ratio, title_coverage, 0.85 * abstract_coverage)
    return eligible, evidence


def matches_exclusion(paper: Paper, exclude_terms: Iterable[str]) -> bool:
    full_text = " ".join([paper.title, *paper.keywords, paper.abstract or ""])
    return any(_term_present(term, full_text) for term in exclude_terms if term)


def text_similarity(query: str, text: str | None) -> float:
    if not text:
        return 0.0
    q = Counter(tokenize(query))
    d = Counter(tokenize(text))
    if not q or not d:
        return 0.0
    shared = sum(min(count, d[token]) for token, count in q.items())
    precision = shared / sum(d.values())
    recall = shared / sum(q.values())
    return (2 * precision * recall / (precision + recall)) if precision + recall else 0.0


def deduplicate(papers: list[Paper]) -> list[Paper]:
    unique: dict[str, Paper] = {}
    for paper in papers:
        # CNKI may expose conference-first, journal and online-first records with
        # different identifiers/DOIs but the same title. The default result list
        # should contain the work once and retain the richest merged metadata.
        key = normalize_title(paper.title) or (paper.doi or paper.paper_id).casefold()
        existing = unique.get(key)
        if not existing:
            unique[key] = paper
            continue
        for cell in paper.matched_cells:
            if cell not in existing.matched_cells:
                existing.matched_cells.append(cell)
        existing.ranks.update(paper.ranks)
        if not existing.abstract and paper.abstract:
            existing.abstract = paper.abstract
        if not existing.keywords and paper.keywords:
            existing.keywords = paper.keywords
        if not existing.resource_type and paper.resource_type:
            existing.resource_type = paper.resource_type
        if not existing.doi and paper.doi:
            existing.doi = paper.doi
        for tier in paper.source_tiers:
            if tier not in existing.source_tiers:
                existing.source_tiers.append(tier)
    return list(unique.values())


def source_quality_score(paper: Paper) -> float:
    return max((SOURCE_TIER_WEIGHTS.get(tier, 0.0) for tier in paper.source_tiers), default=0.0)


def resource_type_score(paper: Paper) -> float:
    return RESOURCE_TYPE_WEIGHTS.get(paper.resource_type or "其他", RESOURCE_TYPE_WEIGHTS["其他"])


def rerank(
    query: str,
    papers: list[Paper],
    cell_weights: dict[str, float],
    query_terms: list[str] | None = None,
    concepts: list[str] | None = None,
) -> list[Paper]:
    current_year = date.today().year
    citation_rates = {
        paper.paper_id: (paper.cited_by or 0) / max(current_year - (paper.year or current_year) + 1, 1)
        for paper in papers
    }
    effective_terms = query_terms or [query]
    effective_concepts = concepts or [query]
    for paper in papers:
        rrf = sum(cell_weights.get(cell, 1.0) / (60 + paper.ranks.get(cell, 100)) for cell in paper.matched_cells)
        title_score = text_similarity(query, paper.title)
        keyword_score = text_similarity(query, " ".join(paper.keywords))
        abstract_score = text_similarity(query, paper.abstract)
        cite_rate = citation_rates[paper.paper_id]
        cite_score = 1.0 - math.exp(-cite_rate / 3.0)
        repeat_score = min(len(paper.matched_cells) / 3, 1.0)
        matrix_score = min(rrf * 20.0, 1.0)
        tier_score = source_quality_score(paper)
        type_score = resource_type_score(paper)
        impact_value = paper.comprehensive_impact_factor or paper.composite_impact_factor or 0.0
        impact_score = 1.0 - math.exp(-impact_value / 2.0)
        eligible, evidence_score = relevance_evidence(paper, effective_terms, effective_concepts)
        relevance_components = {
            "title_relevance": 0.32 * title_score,
            "abstract_relevance": 0.28 * abstract_score,
            "keyword_relevance": 0.12 * keyword_score,
            "search_matrix": 0.20 * matrix_score,
            "multi_cell_match": 0.08 * repeat_score,
            "intent_evidence": 0.12 * evidence_score,
        }
        relevance_scale = sum(relevance_components.values())
        if relevance_scale:
            relevance_components = {key: value / 1.12 for key, value in relevance_components.items()}
        quality_components = {
            "source_tier_quality": 0.45 * tier_score,
            "journal_impact_quality": 0.25 * impact_score,
            "citation_quality": 0.20 * cite_score,
            "resource_type_quality": 0.10 * type_score,
        }
        relevance = sum(relevance_components.values())
        quality = sum(quality_components.values())
        quality_multiplier = 0.35 + 0.65 * quality
        adjusted_relevance = relevance * quality_multiplier
        quality_bonus = 0.20 * quality
        paper.score = adjusted_relevance + quality_bonus
        paper.score_breakdown = {
            **{key: round(value, 6) for key, value in relevance_components.items()},
            **{key: round(value, 6) for key, value in quality_components.items()},
            "relevance_raw": round(relevance, 6),
            "quality_raw": round(quality, 6),
            "quality_multiplier": round(quality_multiplier, 6),
            "adjusted_relevance": round(adjusted_relevance, 6),
            "quality_bonus": round(quality_bonus, 6),
            "relevance_eligible": 1.0 if eligible else 0.0,
        }
    return sorted(
        papers,
        key=lambda item: (item.score_breakdown.get("relevance_eligible", 0.0), item.score),
        reverse=True,
    )


def select_adaptive_results(
    papers: list[Paper],
    *,
    max_results: int = 50,
) -> tuple[list[Paper], dict[str, float | int | str]]:
    """Return as many evidence-backed papers as the result pool supports."""
    if not papers:
        return [], {
            "selection_policy": "adaptive_quality",
            "adaptive_score_floor": 0.0,
            "adaptive_max_results": max_results,
        }

    top_score = max(paper.score for paper in papers)
    score_floor = max(0.24, top_score * 0.55)
    selected: list[Paper] = []
    for paper in papers:
        breakdown = paper.score_breakdown
        relevance = float(breakdown.get("relevance_raw", 0.0))
        quality = float(breakdown.get("quality_raw", 0.0))
        # Strong quality can compensate for moderately expressed relevance;
        # moderate quality needs strong textual relevance. Unknown/weak quality
        # never enters the adaptive default merely to fill a quota.
        quality_supported = quality >= 0.45 or (quality >= 0.25 and relevance >= 0.35)
        if paper.score >= score_floor and relevance >= 0.25 and quality_supported:
            selected.append(paper)
        if len(selected) >= max_results:
            break
    return selected, {
        "selection_policy": "adaptive_quality",
        "adaptive_score_floor": round(score_floor, 6),
        "adaptive_max_results": max_results,
    }
