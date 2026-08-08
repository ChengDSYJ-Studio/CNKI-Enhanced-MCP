import pytest

from cnki_mcp.structured import describe_conditions, normalize_conditions


def test_normalize_structured_conditions() -> None:
    result = normalize_conditions(
        [
            {"field": "subject", "value": "  违约金  ", "operator": "OR", "match": "fuzzy"},
            {"field": "author", "value": "王利明", "operator": "and", "match": "exact"},
            {"field": "source", "value": "法学研究", "operator": "NOT", "match": "exact"},
        ]
    )
    assert result[0]["operator"] == "AND"
    assert result[0]["field_label"] == "主题"
    assert result[1]["operator"] == "AND"
    assert result[2]["operator"] == "NOT"
    assert "作者(精确):王利明" in describe_conditions(result)


@pytest.mark.parametrize("field", ["institution", "fund", "abstract", "doi", "classification"])
def test_extended_fields_are_supported(field: str) -> None:
    assert normalize_conditions([{"field": field, "value": "test"}])[0]["field"] == field


def test_structured_search_rejects_invalid_input() -> None:
    with pytest.raises(ValueError):
        normalize_conditions([])
    with pytest.raises(ValueError):
        normalize_conditions([{"field": "unknown", "value": "x"}])
    with pytest.raises(ValueError):
        normalize_conditions([{"field": "title", "value": "x" * 121}])
