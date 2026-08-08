from cnki_mcp.models import Paper
from cnki_mcp.planner import (
    build_feedback_cells,
    build_advanced_expression,
    build_plan,
    extract_concepts,
    identifier_variants,
    mine_feedback_terms,
    normalize_query,
)


def test_normalize_query() -> None:
    assert normalize_query("  “违约金”   研究 ") == "违约金 研究"


def test_single_term_builds_balanced_matrix() -> None:
    plan = build_plan("违约金")
    assert plan.original_query == "违约金"
    assert [cell.field for cell in plan.cells[:3]] == ["subject", "title", "keyword"]
    assert all(cell.strategy == "quick" for cell in plan.cells)
    assert len(plan.cells) <= 3


def test_precise_mode_is_bounded() -> None:
    plan = build_plan("民法典背景下法院如何调整过高的违约金", "precise")
    assert len(plan.cells) <= 3
    assert {cell.field for cell in plan.cells}.issubset({"subject", "title", "keyword"})


def test_extracts_sentence_concepts() -> None:
    concepts = extract_concepts("民法典背景下法院如何调整过高的违约金")
    assert "民法典" in concepts
    assert "法院" in concepts
    assert "违约金" in concepts
    assert len(concepts) <= 4


def test_sentence_plan_uses_concepts_not_whole_question() -> None:
    plan = build_plan("民法典背景下法院如何调整过高的违约金")
    assert "如何" not in plan.cells[0].query
    assert "违约金" in plan.cells[0].query
    assert " * " in plan.cells[0].query
    assert plan.cells[0].strategy == "advanced"


def test_versioned_identifier_gets_generic_format_variants() -> None:
    assert identifier_variants("wifi8") == ["wifi8", "wifi 8", "wifi-8"]
    plan = build_plan("wifi8")
    assert plan.cells[0].strategy == "advanced"
    assert '"wifi8" + "wifi 8" + "wifi-8"' in plan.cells[0].query


def test_caller_supplied_semantic_terms_join_the_same_search_matrix() -> None:
    plan = build_plan("wifi8", search_terms=["Wi-Fi 8", "802.11bn", "超高可靠 无线局域网"])
    assert any("802.11bn" in cell.query for cell in plan.cells)
    assert "超高可靠 无线局域网" in plan.search_terms
    assert all(cell.strategy == "advanced" for cell in plan.cells)


def test_explicit_concept_groups_use_or_within_and_between() -> None:
    plan = build_plan(
        "下一代无线局域网可靠性",
        concept_groups=[["Wi-Fi 8", "802.11bn"], ["可靠性", "低时延"]],
    )
    expression = plan.cells[0].query
    assert '"Wi-Fi 8"' in expression and '"802.11bn"' in expression
    assert '"可靠性" + "低时延"' in expression
    assert ") * (" in expression


def test_advanced_expression_respects_cnki_limit() -> None:
    expression = build_advanced_expression([["核心词", *[f"同义词{i}" for i in range(30)]], ["限定词"]])
    assert len(expression) <= 118


def test_feedback_terms_only_come_from_relevant_enriched_results() -> None:
    relevant = Paper(
        "p1", "Wi-Fi8通信", "https://example.test/1", keywords=["毫米波", "高速通信", "Wi-Fi8"],
        abstract="下一代WiFi8标准", metadata_complete=True,
        score_breakdown={"relevance_eligible": 1.0},
    )
    noise = Paper(
        "p2", "无关结果", "https://example.test/2", keywords=["电视销售"],
        abstract="无关", metadata_complete=True,
        score_breakdown={"relevance_eligible": 0.0},
    )
    terms = mine_feedback_terms([relevant, noise], ["wifi8"])
    assert "电视销售" not in terms
    assert any(term in terms for term in ("高速通信", "毫米波"))
    cells = build_feedback_cells(4, ["wifi8", "Wi-Fi 8"], terms)
    assert all(" * " in cell.query for cell in cells)
