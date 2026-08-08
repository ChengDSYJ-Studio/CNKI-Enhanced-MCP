from cnki_mcp.engine import _paper_id


def test_paper_id_is_stable_without_expiring_detail_url() -> None:
    first = _paper_id("再论违约金调整", ["王利明"], "现代法学", 2025)
    second = _paper_id("再论违约金调整", ["王利明"], "现代法学", 2025)
    assert first == second


def test_paper_id_distinguishes_bibliographic_records() -> None:
    journal = _paper_id("同名论文", ["张三"], "法学研究", 2025)
    thesis = _paper_id("同名论文", ["张三"], "示例大学", 2025)
    assert journal != thesis
