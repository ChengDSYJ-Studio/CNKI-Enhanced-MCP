from __future__ import annotations

from dataclasses import asdict, dataclass, field
from typing import Any


@dataclass(slots=True)
class SearchCell:
    cell_id: str
    field: str
    query: str
    purpose: str
    weight: float = 1.0
    pages: int = 1
    strategy: str = "quick"

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)


@dataclass(slots=True)
class SearchPlan:
    original_query: str
    mode: str
    concepts: list[str]
    cells: list[SearchCell]
    search_terms: list[str] = field(default_factory=list)
    concept_groups: list[list[str]] = field(default_factory=list)

    def to_dict(self) -> dict[str, Any]:
        return {
            "original_query": self.original_query,
            "mode": self.mode,
            "concepts": self.concepts,
            "search_terms": self.search_terms,
            "concept_groups": self.concept_groups,
            "cells": [cell.to_dict() for cell in self.cells],
        }


@dataclass(slots=True)
class Paper:
    paper_id: str
    title: str
    detail_url: str
    authors: list[str] = field(default_factory=list)
    institutions: list[str] = field(default_factory=list)
    abstract: str | None = None
    abstract_en: str | None = None
    keywords: list[str] = field(default_factory=list)
    source: str | None = None
    resource_type: str | None = None
    source_tiers: list[str] = field(default_factory=list)
    composite_impact_factor: float | None = None
    comprehensive_impact_factor: float | None = None
    year: int | None = None
    volume: str | None = None
    issue: str | None = None
    pages: str | None = None
    doi: str | None = None
    funds: list[str] = field(default_factory=list)
    classification: list[str] = field(default_factory=list)
    cited_by: int | None = None
    download_count: int | None = None
    publish_date: str | None = None
    matched_cells: list[str] = field(default_factory=list)
    ranks: dict[str, int] = field(default_factory=dict)
    score: float = 0.0
    score_breakdown: dict[str, float] = field(default_factory=dict)
    local_file: str | None = None
    metadata_complete: bool = False

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)
