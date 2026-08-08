from cnki_mcp.references import extract_marked_titles


def test_extract_simple_reference_titles() -> None:
    assert extract_marked_titles("参见《违约金规则研究》和《合同法研究》。") == ["违约金规则研究", "合同法研究"]


def test_extract_title_with_nested_book_marks() -> None:
    title = "再论违约金调整——以《民法典》第585条第2款和第3款为中心"
    assert extract_marked_titles(f"本文讨论《{title}》。") == [title]


def test_unclosed_mark_is_ignored() -> None:
    assert extract_marked_titles("错误的《论文标题") == []
