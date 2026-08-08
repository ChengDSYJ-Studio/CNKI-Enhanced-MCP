import json

import pytest

from cnki_mcp.citations import export_records
from cnki_mcp.models import Paper


@pytest.fixture
def paper() -> Paper:
    return Paper(
        paper_id="p1",
        title="违约金规则研究",
        detail_url="https://kns.cnki.net/example",
        authors=["张三", "李四"],
        institutions=["示例大学"],
        abstract="测试摘要",
        keywords=["违约金", "合同法"],
        source="法学研究",
        resource_type="期刊",
        year=2025,
        volume="47",
        issue="3",
        pages="1-20",
        doi="10.1234/example",
    )


@pytest.mark.parametrize(
    ("style", "expected"),
    [
        ("gbt7714", "违约金规则研究[J]"),
        ("apa", "(2025)"),
        ("mla", "法学研究"),
        ("chicago", "违约金规则研究"),
        ("vancouver", "1."),
        ("markdown", "[违约金规则研究]"),
    ],
)
def test_formatted_citations(paper: Paper, style: str, expected: str) -> None:
    content, _ = export_records([paper], style)
    assert expected in content


def test_machine_readable_exports(paper: Paper) -> None:
    bibtex, extension = export_records([paper], "bibtex")
    assert extension == "bib"
    assert "@article" in bibtex and "doi = {10.1234/example}" in bibtex
    ris, _ = export_records([paper], "ris")
    assert "TY  - JOUR" in ris and "ER  -" in ris
    endnote, _ = export_records([paper], "endnote")
    assert "%0 Journal Article" in endnote
    csl, _ = export_records([paper], "csl_json")
    assert json.loads(csl)[0]["type"] == "article-journal"
    raw, _ = export_records([paper], "json")
    assert json.loads(raw)[0]["title"] == "违约金规则研究"
    assert "paper_id" not in json.loads(raw)[0]
    csv_text, _ = export_records([paper], "csv")
    assert csv_text.startswith("index,title") and "paper_id" not in csv_text


def test_custom_export_is_data_only(paper: Paper) -> None:
    content, extension = export_records([paper], "custom", "{authors}.《{title}》.{source},{year}.{url}")
    assert content.startswith("张三, 李四.《违约金规则研究》")
    assert extension == "txt"
    with pytest.raises(ValueError, match="未知占位符"):
        export_records([paper], "custom", "{not_a_field}")
