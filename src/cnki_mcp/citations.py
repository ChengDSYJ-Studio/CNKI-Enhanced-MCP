from __future__ import annotations

import csv
import io
import json
import re
from collections import defaultdict
from typing import Any

from .models import Paper


FORMATS = {
    "gbt7714",
    "apa",
    "mla",
    "chicago",
    "vancouver",
    "bibtex",
    "ris",
    "endnote",
    "csl_json",
    "json",
    "csv",
    "markdown",
    "custom",
}


def _authors(paper: Paper, separator: str = ", ") -> str:
    return separator.join(paper.authors) if paper.authors else "佚名"


def _type_code(paper: Paper) -> str:
    value = paper.resource_type or ""
    if "期刊" in value:
        return "J"
    if "学位" in value or "论文" in value:
        return "D"
    if "会议" in value:
        return "C"
    if "报纸" in value:
        return "N"
    return "Z"


def _pages(paper: Paper) -> str:
    return paper.pages or ""


def _publication_tail(paper: Paper) -> str:
    pieces = []
    if paper.volume:
        pieces.append(paper.volume)
    if paper.issue:
        pieces.append(f"({paper.issue})")
    if paper.pages:
        pieces.append(f": {paper.pages}")
    return "".join(pieces)


def _format_citation(paper: Paper, style: str, number: int) -> str:
    authors = _authors(paper)
    year = str(paper.year or "n.d.")
    source = paper.source or ""
    tail = _publication_tail(paper)
    doi_or_url = f" https://doi.org/{paper.doi}" if paper.doi else f" {paper.detail_url}"
    if style == "gbt7714":
        return f"{authors}. {paper.title}[{_type_code(paper)}]. {source}, {year}{tail}.{doi_or_url}".strip()
    if style == "apa":
        return f"{authors}. ({year}). {paper.title}. {source}, {tail.lstrip()}.{doi_or_url}".strip()
    if style == "mla":
        return f'{authors}. “{paper.title}.” {source}, {year}{tail}.{doi_or_url}'.strip()
    if style == "chicago":
        return f'{authors}. “{paper.title}.” {source} {tail} ({year}).{doi_or_url}'.strip()
    if style == "vancouver":
        return f"{number}. {authors}. {paper.title}. {source}. {year}{tail}.{doi_or_url}".strip()
    if style == "markdown":
        return f"{number}. [{paper.title}]({paper.detail_url}) — {authors}，{source}，{year}"
    raise ValueError(f"不支持的引文样式: {style}")


def _citation_key(paper: Paper, used: defaultdict[str, int]) -> str:
    family = re.sub(r"[^A-Za-z0-9\u4e00-\u9fff]", "", paper.authors[0] if paper.authors else "anon")
    title = re.sub(r"[^A-Za-z0-9\u4e00-\u9fff]", "", paper.title)[:12]
    base = f"{family}{paper.year or 'nd'}{title}" or "cnki"
    used[base] += 1
    return base if used[base] == 1 else f"{base}{used[base]}"


def _bibtex_escape(value: str | None) -> str:
    return (value or "").replace("\\", "\\textbackslash{}").replace("{", "\\{").replace("}", "\\}")


def _bibtex(papers: list[Paper]) -> str:
    used: defaultdict[str, int] = defaultdict(int)
    blocks = []
    for paper in papers:
        entry_type = "article" if _type_code(paper) == "J" else "phdthesis" if _type_code(paper) == "D" else "inproceedings" if _type_code(paper) == "C" else "misc"
        fields = {
            "title": paper.title,
            "author": " and ".join(paper.authors),
            "journal": paper.source if entry_type == "article" else None,
            "school": paper.institutions[0] if paper.institutions and entry_type == "phdthesis" else None,
            "year": paper.year,
            "volume": paper.volume,
            "number": paper.issue,
            "pages": paper.pages,
            "doi": paper.doi,
            "url": paper.detail_url,
            "keywords": ", ".join(paper.keywords),
        }
        lines = [f"@{entry_type}{{{_citation_key(paper, used)},"]
        present = [(key, value) for key, value in fields.items() if value not in (None, "", [])]
        lines.extend(f"  {key} = {{{_bibtex_escape(str(value))}}}{',' if index < len(present) - 1 else ''}" for index, (key, value) in enumerate(present))
        lines.append("}")
        blocks.append("\n".join(lines))
    return "\n\n".join(blocks)


def _ris(papers: list[Paper]) -> str:
    type_map = {"J": "JOUR", "D": "THES", "C": "CONF", "N": "NEWS", "Z": "GEN"}
    records = []
    for paper in papers:
        lines = [f"TY  - {type_map[_type_code(paper)]}", f"TI  - {paper.title}"]
        lines.extend(f"AU  - {author}" for author in paper.authors)
        for tag, value in (("JO", paper.source), ("PY", paper.year), ("VL", paper.volume), ("IS", paper.issue), ("SP", paper.pages), ("DO", paper.doi), ("UR", paper.detail_url), ("AB", paper.abstract)):
            if value not in (None, ""):
                lines.append(f"{tag}  - {value}")
        lines.extend(f"KW  - {keyword}" for keyword in paper.keywords)
        lines.append("ER  -")
        records.append("\n".join(lines))
    return "\n\n".join(records)


def _endnote(papers: list[Paper]) -> str:
    type_map = {"J": "Journal Article", "D": "Thesis", "C": "Conference Paper", "N": "Newspaper Article", "Z": "Generic"}
    records = []
    for paper in papers:
        lines = [f"%0 {type_map[_type_code(paper)]}", f"%T {paper.title}"]
        lines.extend(f"%A {author}" for author in paper.authors)
        for tag, value in (("%J", paper.source), ("%D", paper.year), ("%V", paper.volume), ("%N", paper.issue), ("%P", paper.pages), ("%R", paper.doi), ("%U", paper.detail_url), ("%X", paper.abstract)):
            if value not in (None, ""):
                lines.append(f"{tag} {value}")
        lines.extend(f"%K {keyword}" for keyword in paper.keywords)
        records.append("\n".join(lines))
    return "\n\n".join(records)


def _csl(paper: Paper, item_id: str) -> dict[str, Any]:
    type_map = {"J": "article-journal", "D": "thesis", "C": "paper-conference", "N": "article-newspaper", "Z": "article"}
    return {
        "id": item_id,
        "type": type_map[_type_code(paper)],
        "title": paper.title,
        "author": [{"literal": author} for author in paper.authors],
        "container-title": paper.source,
        "issued": {"date-parts": [[paper.year]]} if paper.year else None,
        "volume": paper.volume,
        "issue": paper.issue,
        "page": paper.pages,
        "DOI": paper.doi,
        "URL": paper.detail_url,
        "abstract": paper.abstract,
        "keyword": ", ".join(paper.keywords) or None,
    }


def _template_values(paper: Paper, number: int) -> dict[str, str]:
    return {
        "index": str(number),
        "title": paper.title,
        "authors": _authors(paper),
        "authors_semicolon": _authors(paper, "; "),
        "year": str(paper.year or ""),
        "source": paper.source or "",
        "resource_type": paper.resource_type or "",
        "volume": paper.volume or "",
        "issue": paper.issue or "",
        "pages": paper.pages or "",
        "doi": paper.doi or "",
        "url": paper.detail_url,
        "abstract": paper.abstract or "",
        "keywords": "; ".join(paper.keywords),
        "institutions": "; ".join(paper.institutions),
        "funds": "; ".join(paper.funds),
    }


class _StrictTemplate(dict[str, str]):
    def __missing__(self, key: str) -> str:
        raise ValueError(f"自定义模板包含未知占位符: {key}")


def export_records(papers: list[Paper], output_format: str, custom_template: str | None = None) -> tuple[str, str]:
    output_format = output_format.lower().strip()
    if output_format not in FORMATS:
        raise ValueError(f"format 必须是 {', '.join(sorted(FORMATS))}")
    if not papers:
        raise ValueError("没有可导出的论文")
    if output_format in {"gbt7714", "apa", "mla", "chicago", "vancouver", "markdown"}:
        return "\n".join(_format_citation(paper, output_format, index) for index, paper in enumerate(papers, 1)), "txt" if output_format != "markdown" else "md"
    if output_format == "bibtex":
        return _bibtex(papers), "bib"
    if output_format == "ris":
        return _ris(papers), "ris"
    if output_format == "endnote":
        return _endnote(papers), "enw"
    if output_format == "csl_json":
        return json.dumps([_csl(paper, paper.title) for paper in papers], ensure_ascii=False, indent=2), "json"
    if output_format == "json":
        records = []
        for paper in papers:
            record = paper.to_dict()
            record.pop("paper_id", None)
            records.append(record)
        return json.dumps(records, ensure_ascii=False, indent=2), "json"
    if output_format == "csv":
        stream = io.StringIO()
        fields = list(_template_values(papers[0], 1))
        writer = csv.DictWriter(stream, fieldnames=fields)
        writer.writeheader()
        for index, paper in enumerate(papers, 1):
            writer.writerow(_template_values(paper, index))
        return stream.getvalue(), "csv"
    if not custom_template:
        raise ValueError("custom 格式必须提供 custom_template")
    lines = [custom_template.format_map(_StrictTemplate(_template_values(paper, index))) for index, paper in enumerate(papers, 1)]
    return "\n".join(lines), "txt"
