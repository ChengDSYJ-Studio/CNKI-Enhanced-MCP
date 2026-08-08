from __future__ import annotations

import re
from collections import Counter

from .models import Paper, SearchCell, SearchPlan
from .ranking import tokenize


STOP_WORDS = {
    "研究", "相关", "有关", "论文", "文献", "如何", "怎么", "怎样", "的", "了",
    "以及", "及其", "背景下", "问题", "分析", "探讨", "论", "关于",
}

FEEDBACK_STOP_WORDS = STOP_WORDS | {
    "应用", "技术", "方法", "系统", "发展", "现状", "影响", "策略", "机制",
    "协同", "融合", "评价", "实证", "理论", "综述", "创新", "中国",
}


def normalize_query(query: str) -> str:
    query = query.strip().replace("“", "").replace("”", "").replace("‘", "").replace("’", "")
    return re.sub(r"\s+", " ", query)


def identifier_variants(value: str) -> list[str]:
    """Generate punctuation variants for versioned Latin identifiers without domain knowledge."""
    normalized = normalize_query(value)
    if not (
        re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._\-/ ]*", normalized)
        and re.search(r"[A-Za-z]", normalized)
        and re.search(r"\d", normalized)
    ):
        return [normalized]
    compact = re.sub(r"[\s._\-/]+", "", normalized)
    match = re.fullmatch(r"([A-Za-z]+)(\d+[A-Za-z]*)", compact)
    variants = [normalized, compact]
    if match:
        prefix, suffix = match.groups()
        variants.extend([f"{prefix} {suffix}", f"{prefix}-{suffix}"])
    output: list[str] = []
    for item in variants:
        if item and item not in output:
            output.append(item)
    return output


def _quoted_or(terms: list[str]) -> str:
    return " + ".join(f'"{term}"' for term in terms)


def build_advanced_expression(groups: list[list[str]], *, max_length: int = 118) -> str:
    """Build CNKI's boolean expression while respecting its 120-character limit."""
    normalized = [[normalize_query(term) for term in group if normalize_query(term)] for group in groups]
    normalized = [list(dict.fromkeys(group)) for group in normalized if group]
    if not normalized:
        raise ValueError("高级检索至少需要一个有效概念组")

    selected = [[group[0]] for group in normalized]

    def render(value: list[list[str]]) -> str:
        return " * ".join(f"({_quoted_or(group)})" for group in value)

    if len(render(selected)) > max_length:
        raise ValueError("核心概念超过知网高级检索的 120 字符限制，请缩短关键词")
    for group_index, group in enumerate(normalized):
        for alias in group[1:]:
            trial = [items.copy() for items in selected]
            trial[group_index].append(alias)
            if len(render(trial)) <= max_length:
                selected = trial
    return render(selected)


def _concept_groups(
    normalized_query: str,
    concepts: list[str],
    search_terms: list[str] | None,
    concept_groups: list[list[str]] | None,
) -> list[list[str]]:
    if concept_groups:
        raw_groups = concept_groups
    elif search_terms:
        # Flat search_terms are aliases/standards for the query's core concept.
        raw_groups = [[normalized_query, *search_terms]]
    elif len(concepts) > 1:
        raw_groups = [[concept] for concept in concepts]
    else:
        raw_groups = [[normalized_query]]

    output: list[list[str]] = []
    for raw_group in raw_groups[:4]:
        group: list[str] = []
        original_terms = [normalize_query(raw_term) for raw_term in raw_group[:8]]
        for term in original_terms:
            if term and term not in group:
                group.append(term)
        # Caller/LLM-supplied terms are more valuable than mechanical spelling
        # variants, so append variants only after preserving every original.
        for term in original_terms:
            for variant in identifier_variants(term):
                if variant and variant not in group:
                    group.append(variant)
        if group:
            output.append(group)
    return output


def mine_feedback_terms(
    papers: list[Paper],
    query_terms: list[str],
    *,
    max_terms: int = 2,
) -> list[str]:
    """Mine bounded pseudo-relevance feedback terms from already verified candidates."""
    query_tokens = set(token for term in query_terms for token in tokenize(term))
    trusted = [
        paper
        for paper in papers
        if paper.metadata_complete and paper.score_breakdown.get("relevance_eligible", 0.0) > 0
    ][:10]
    counts: Counter[str] = Counter()
    for paper in trusted:
        seen: set[str] = set()
        for raw_keyword in paper.keywords:
            keyword = raw_keyword.strip(" ;；,，。:：()（）[]【】")
            compact = re.sub(r"\s+", "", keyword)
            if not (2 <= len(compact) <= 24) or compact in FEEDBACK_STOP_WORDS:
                continue
            keyword_tokens = set(tokenize(keyword))
            if not keyword_tokens or keyword_tokens.issubset(query_tokens):
                continue
            if keyword not in seen:
                counts[keyword] += 1
                seen.add(keyword)
    minimum_frequency = 2 if len(trusted) >= 3 else 1
    candidates = [item for item in counts if counts[item] >= minimum_frequency]
    candidates.sort(key=lambda item: (counts[item], min(len(item), 12)), reverse=True)
    return candidates[:max_terms]


def build_feedback_cells(
    start_id: int,
    anchor_terms: list[str],
    feedback_terms: list[str],
) -> list[SearchCell]:
    anchor = anchor_terms[0]
    return [
        SearchCell(
            f"S{start_id + index}",
            "subject",
            f'"{anchor}" * "{term}"',
            f"首轮结果反馈扩展：{term}",
            0.8,
            1,
            "advanced",
        )
        for index, term in enumerate(feedback_terms)
    ]


def extract_concepts(query: str) -> list[str]:
    quoted = re.findall(r"[《\"']([^》\"']{2,30})[》\"']", query)
    cleaned = query
    for word in STOP_WORDS:
        cleaned = cleaned.replace(word, " ")
    parts = re.split(r"[，。；、：,.!?！？]|\s+", cleaned)
    concepts: list[str] = []
    for item in quoted + parts:
        item = item.strip(" -—与和在中对")
        if 2 <= len(item) <= 30 and item not in concepts:
            concepts.append(item)
    if not concepts:
        concepts = [query]
    return concepts[:4]


def build_plan(
    query: str,
    mode: str = "balanced",
    search_terms: list[str] | None = None,
    concept_groups: list[list[str]] | None = None,
) -> SearchPlan:
    normalized = normalize_query(query)
    if not normalized:
        raise ValueError("query 不能为空")
    if mode not in {"precise", "balanced", "broad"}:
        raise ValueError("mode 必须是 precise、balanced 或 broad")

    concepts = extract_concepts(normalized)
    groups = _concept_groups(normalized, concepts, search_terms, concept_groups)
    terms = list(dict.fromkeys(term for group in groups for term in group))
    pages = 1 if mode == "precise" else 2 if mode == "broad" else 1
    multi_concept = len(groups) > 1
    multi_keyword = multi_concept or any(len(group) > 1 for group in groups)
    core_query = " * ".join(f'"{part}"' for part in concepts[:4]) if multi_concept else normalized
    title_query = " * ".join(f'"{part}"' for part in concepts[-2:]) if multi_concept else normalized
    if multi_keyword:
        expression = build_advanced_expression(groups)
        cells = [
            SearchCell("S1", "subject", expression, "高级检索：概念组内 OR、组间 AND", 1.2, pages, "advanced"),
            SearchCell("S2", "title", expression, "高级篇名检索：高精度补充", 1.3, 1, "advanced"),
        ]
        if mode != "precise":
            cells.append(SearchCell("S3", "keyword", expression, "高级关键词检索：标引补充", 1.15, 1, "advanced"))
    else:
        cells = [
            SearchCell("S1", "subject", core_query, "主题主检索", 1.0, pages),
            SearchCell("S2", "title", title_query, "篇名高精度检索", 1.25, 1),
        ]
        if mode != "precise":
            cells.append(SearchCell("S3", "keyword", core_query, "关键词标引检索", 1.15, 1))

    next_id = 4
    for term in terms[1:] if not multi_keyword else []:
        cells.append(SearchCell(f"S{next_id}", "subject", term, f"独立语义/格式变体：{term}", 0.95, 1))
        next_id += 1

    if multi_concept and mode != "precise" and len(groups) > 2:
        relaxed = " * ".join(f'"{part}"' for part in concepts[-2:])
        if relaxed != core_query:
            relaxed_expression = build_advanced_expression(groups[:2])
            cells.append(SearchCell(f"S{next_id}", "subject", relaxed_expression, "高级检索：仅保留两个核心概念组", 0.9, 1, "advanced"))

    maximum = {"precise": 3, "balanced": 8, "broad": 10}[mode]
    return SearchPlan(normalized, mode, concepts, cells[:maximum], terms, groups)
