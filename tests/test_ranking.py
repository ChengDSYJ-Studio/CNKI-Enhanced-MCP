from cnki_mcp.models import Paper
from cnki_mcp.engine import dynamic_rerank_candidates
from cnki_mcp.ranking import (
    deduplicate,
    matches_exclusion,
    normalize_title,
    relevance_evidence,
    rerank,
    select_adaptive_results,
    text_similarity,
    tokenize,
)


def test_dynamic_quality_candidate_pool() -> None:
    assert dynamic_rerank_candidates(0) == 0
    assert dynamic_rerank_candidates(24) == 24
    assert dynamic_rerank_candidates(80) == 40
    assert dynamic_rerank_candidates(220) == 60
    assert dynamic_rerank_candidates(500) == 80
    assert dynamic_rerank_candidates(80, limit=70) == 70


def paper(identifier: str, title: str, abstract: str | None, cell: str, rank: int) -> Paper:
    return Paper(
        paper_id=identifier,
        title=title,
        detail_url=f"https://example.test/{identifier}",
        abstract=abstract,
        keywords=["违约金"],
        matched_cells=[cell],
        ranks={cell: rank},
    )


def test_normalize_title() -> None:
    assert normalize_title("《违约金：司法调整》") == "违约金司法调整"


def test_similarity_rewards_relevant_abstract() -> None:
    relevant = text_similarity("过高违约金司法调整", "法院可以酌减过高违约金并考虑实际损失")
    unrelated = text_similarity("过高违约金司法调整", "生成式人工智能教育应用研究")
    assert relevant > unrelated


def test_deduplicate_merges_matrix_hits() -> None:
    first = paper("one", "违约金司法调整研究", None, "S1", 2)
    second = paper("two", "违约金司法调整研究", "摘要", "S2", 1)
    merged = deduplicate([first, second])
    assert len(merged) == 1
    assert set(merged[0].matched_cells) == {"S1", "S2"}
    assert merged[0].abstract == "摘要"


def test_deduplicate_same_title_even_when_versions_have_different_dois() -> None:
    first = paper("one", "动态频谱协同优化框架", None, "S1", 2)
    first.doi = "10.1/conference"
    second = paper("two", "动态频谱协同优化框架", "期刊版本摘要", "S2", 1)
    second.doi = "10.1/journal"
    merged = deduplicate([first, second])
    assert len(merged) == 1
    assert merged[0].abstract == "期刊版本摘要"


def test_abstract_participates_in_rerank() -> None:
    a = paper("a", "合同问题研究", "讨论过高违约金的司法酌减标准", "S1", 5)
    b = paper("b", "合同问题研究二", "讨论人工智能技术", "S1", 1)
    ranked = rerank("过高违约金司法酌减", [a, b], {"S1": 1.0})
    assert ranked[0].paper_id == "a"


def test_source_tier_and_resource_type_participate_in_rerank() -> None:
    core = paper("core", "违约金研究", "违约金制度研究", "S1", 1)
    core.resource_type = "期刊"
    core.source_tiers = ["CSSCI", "北大核心"]
    ordinary = paper("ordinary", "违约金研究", "违约金制度研究", "S1", 1)
    ordinary.resource_type = "报纸"
    ranked = rerank("违约金", [ordinary, core], {"S1": 1.0})
    assert ranked[0].paper_id == "core"
    assert core.score_breakdown["source_tier_quality"] > ordinary.score_breakdown["source_tier_quality"]
    assert core.score_breakdown["resource_type_quality"] > ordinary.score_breakdown["resource_type_quality"]


def test_relevance_remains_more_important_than_source_tier() -> None:
    relevant = paper("relevant", "违约金司法调整标准", "法院酌减过高违约金的规则", "S1", 1)
    relevant.resource_type = "硕士论文"
    unrelated = paper("unrelated", "人工智能研究", "生成式人工智能教育应用", "S1", 1)
    unrelated.resource_type = "期刊"
    unrelated.source_tiers = ["CSSCI"]
    ranked = rerank("违约金司法调整", [unrelated, relevant], {"S1": 1.0})
    assert ranked[0].paper_id == "relevant"


def test_low_quality_high_relevance_is_demoted_by_evidence_quality() -> None:
    undergraduate = paper("undergraduate", "违约金司法调整研究", "违约金司法调整标准", "S1", 1)
    undergraduate.resource_type = "本科论文"
    established = paper("established", "违约金调整规则", "违约金司法酌减的裁判规则", "S1", 2)
    established.resource_type = "期刊"
    established.source_tiers = ["CSSCI"]
    established.comprehensive_impact_factor = 3.0
    established.cited_by = 20
    established.year = 2022
    ranked = rerank("违约金司法调整", [undergraduate, established], {"S1": 1.0})
    assert ranked[0].paper_id == "established"


def test_versioned_identifier_tokenization_is_punctuation_insensitive() -> None:
    assert "wifi8" in tokenize("Wi-Fi8毫米波通信")
    assert "80211bn" in tokenize("IEEE P802.11bn")


def test_calendar_expression_does_not_become_version_identifier() -> None:
    assert "wifi8" not in tokenize("内建WiFi 8月上市")


def test_relevance_gate_rejects_calendar_ambiguity() -> None:
    noise = paper("noise", "内建WiFi 8月上市", "产品可能在8月份发布", "S1", 1)
    relevant = paper("relevant", "Wi-Fi8毫米波通信技术", "下一代WiFi8标准", "S1", 2)
    assert relevance_evidence(noise, ["wifi8", "wifi 8", "wifi-8"], ["wifi8"])[0] is False
    assert relevance_evidence(relevant, ["wifi8", "wifi 8", "wifi-8"], ["wifi8"])[0] is True


def test_absolute_impact_scale_does_not_make_smallest_pool_member_full_score() -> None:
    low = paper("low", "违约金研究", "违约金", "S1", 1)
    low.resource_type = "期刊"
    low.comprehensive_impact_factor = 0.265
    rerank("违约金", [low], {"S1": 1.0})
    assert low.score_breakdown["journal_impact_quality"] < 0.05


def test_explicit_exclusion_term_filters_known_ambiguity() -> None:
    item = paper("noise", "WiFi 8月上市", "产品新闻", "S1", 1)
    assert matches_exclusion(item, ["8月上市"]) is True


def test_adaptive_selection_returns_quality_supported_results_without_filling_quota() -> None:
    strong = paper("strong", "自动驾驶轨迹规划", "自动驾驶避撞", "S1", 1)
    strong.score = 0.50
    strong.score_breakdown = {"relevance_raw": 0.45, "quality_raw": 0.65}
    moderate = paper("moderate", "自动驾驶决策", "自动驾驶安全", "S1", 2)
    moderate.score = 0.34
    moderate.score_breakdown = {"relevance_raw": 0.40, "quality_raw": 0.30}
    weak = paper("weak", "自动驾驶应用", "自动驾驶", "S1", 3)
    weak.score = 0.31
    weak.score_breakdown = {"relevance_raw": 0.48, "quality_raw": 0.08}
    selected, stats = select_adaptive_results([strong, moderate, weak])
    assert [item.paper_id for item in selected] == ["strong", "moderate"]
    assert stats["selection_policy"] == "adaptive_quality"


def test_adaptive_selection_can_return_fewer_than_ten() -> None:
    only = paper("only", "高质量论文", "相关摘要", "S1", 1)
    only.score = 0.48
    only.score_breakdown = {"relevance_raw": 0.40, "quality_raw": 0.70}
    selected, _ = select_adaptive_results([only])
    assert len(selected) == 1


def test_adaptive_selection_can_return_more_than_ten() -> None:
    candidates = []
    for index in range(14):
        item = paper(str(index), f"高质量论文{index}", "高度相关摘要", "S1", index + 1)
        item.score = 0.50 - index * 0.01
        item.score_breakdown = {"relevance_raw": 0.42, "quality_raw": 0.62}
        candidates.append(item)
    selected, _ = select_adaptive_results(candidates)
    assert len(selected) == 14
