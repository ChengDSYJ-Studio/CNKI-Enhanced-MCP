from __future__ import annotations

from typing import Any


STRUCTURED_FIELDS = {
    "subject": "主题",
    "title_keyword_abstract": "篇关摘",
    "keyword": "关键词",
    "title": "篇名",
    "fulltext": "全文",
    "author": "作者",
    "first_author": "第一作者",
    "corresponding_author": "通讯作者",
    "institution": "作者单位",
    "fund": "基金",
    "abstract": "摘要",
    "subtitle": "小标题",
    "reference": "参考文献",
    "classification": "分类号",
    "source": "文献来源",
    "doi": "DOI",
}

OPERATORS = {"AND", "OR", "NOT"}
MATCH_MODES = {"exact": "精确", "fuzzy": "模糊"}


def normalize_conditions(conditions: list[dict[str, Any]]) -> list[dict[str, str]]:
    if not conditions:
        raise ValueError("conditions 至少需要一个检索条件")
    if len(conditions) > 10:
        raise ValueError("CNKI 高级检索最多支持 10 个条件")
    output: list[dict[str, str]] = []
    for index, raw in enumerate(conditions):
        field = str(raw.get("field", "")).strip().lower()
        value = " ".join(str(raw.get("value", "")).split())
        operator = str(raw.get("operator", "AND")).strip().upper()
        match = str(raw.get("match", "exact")).strip().lower()
        if field not in STRUCTURED_FIELDS:
            raise ValueError(f"不支持的结构化检索字段: {field}")
        if not value:
            raise ValueError(f"第 {index + 1} 个条件的 value 不能为空")
        if len(value) > 120:
            raise ValueError(f"第 {index + 1} 个条件超过 CNKI 单行 120 字符限制")
        if operator not in OPERATORS:
            raise ValueError("operator 必须是 AND、OR 或 NOT")
        if match not in MATCH_MODES:
            raise ValueError("match 必须是 exact 或 fuzzy")
        output.append(
            {
                "field": field,
                "field_label": STRUCTURED_FIELDS[field],
                "value": value,
                "operator": "AND" if index == 0 else operator,
                "match": match,
                "match_label": MATCH_MODES[match],
            }
        )
    return output


def describe_conditions(conditions: list[dict[str, str]]) -> str:
    parts: list[str] = []
    for index, item in enumerate(conditions):
        prefix = "" if index == 0 else f" {item['operator']} "
        parts.append(f"{prefix}{item['field_label']}({item['match_label']}):{item['value']}")
    return "".join(parts)
