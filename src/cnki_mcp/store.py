from __future__ import annotations

import json
import sqlite3
import threading
from dataclasses import fields
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from .models import Paper, SearchPlan
from .paths import DB_PATH, ensure_runtime_dirs


class Store:
    def __init__(self, db_path: str | Path = DB_PATH) -> None:
        ensure_runtime_dirs()
        self._lock = threading.RLock()
        self._db = sqlite3.connect(Path(db_path), check_same_thread=False)
        self._db.row_factory = sqlite3.Row
        self._initialize()

    def _initialize(self) -> None:
        with self._db:
            self._db.executescript(
                """
                PRAGMA journal_mode=WAL;
                CREATE TABLE IF NOT EXISTS papers (
                    paper_id TEXT PRIMARY KEY,
                    title TEXT NOT NULL,
                    detail_url TEXT NOT NULL,
                    doi TEXT,
                    payload TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                );
                CREATE INDEX IF NOT EXISTS idx_papers_title ON papers(title);
                CREATE INDEX IF NOT EXISTS idx_papers_doi ON papers(doi);
                CREATE TABLE IF NOT EXISTS searches (
                    search_id TEXT PRIMARY KEY,
                    query TEXT NOT NULL,
                    status TEXT NOT NULL,
                    plan_json TEXT NOT NULL,
                    result_ids TEXT NOT NULL,
                    stats_json TEXT NOT NULL,
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                );
                """
            )

    @staticmethod
    def _now() -> str:
        return datetime.now(UTC).isoformat()

    def save_paper(self, paper: Paper) -> None:
        payload = json.dumps(paper.to_dict(), ensure_ascii=False)
        with self._lock, self._db:
            self._db.execute(
                """
                INSERT INTO papers(paper_id, title, detail_url, doi, payload, updated_at)
                VALUES(?, ?, ?, ?, ?, ?)
                ON CONFLICT(paper_id) DO UPDATE SET
                    title=excluded.title,
                    detail_url=excluded.detail_url,
                    doi=excluded.doi,
                    payload=excluded.payload,
                    updated_at=excluded.updated_at
                """,
                (paper.paper_id, paper.title, paper.detail_url, paper.doi, payload, self._now()),
            )

    def get_paper(self, paper_id: str) -> Paper | None:
        with self._lock:
            row = self._db.execute("SELECT payload FROM papers WHERE paper_id = ?", (paper_id,)).fetchone()
        return self._paper_from_row(row) if row else None

    def find_paper_by_title(self, title: str) -> list[Paper]:
        with self._lock:
            rows = self._db.execute(
                "SELECT payload FROM papers WHERE title = ? COLLATE NOCASE LIMIT 10", (title,)
            ).fetchall()
        return [self._paper_from_row(row) for row in rows]

    def _paper_from_row(self, row: sqlite3.Row) -> Paper:
        data: dict[str, Any] = json.loads(row["payload"])
        allowed = {item.name for item in fields(Paper)}
        return Paper(**{key: value for key, value in data.items() if key in allowed})

    def save_search(
        self,
        search_id: str,
        query: str,
        status: str,
        plan: SearchPlan,
        result_ids: list[str],
        stats: dict[str, Any],
    ) -> None:
        now = self._now()
        with self._lock, self._db:
            self._db.execute(
                """
                INSERT INTO searches(search_id, query, status, plan_json, result_ids, stats_json, created_at, updated_at)
                VALUES(?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(search_id) DO UPDATE SET
                    status=excluded.status,
                    result_ids=excluded.result_ids,
                    stats_json=excluded.stats_json,
                    updated_at=excluded.updated_at
                """,
                (
                    search_id,
                    query,
                    status,
                    json.dumps(plan.to_dict(), ensure_ascii=False),
                    json.dumps(result_ids),
                    json.dumps(stats, ensure_ascii=False),
                    now,
                    now,
                ),
            )

    def get_search(self, search_id: str) -> dict[str, Any] | None:
        with self._lock:
            row = self._db.execute("SELECT * FROM searches WHERE search_id = ?", (search_id,)).fetchone()
        if not row:
            return None
        return {
            "search_id": row["search_id"],
            "query": row["query"],
            "status": row["status"],
            "plan": json.loads(row["plan_json"]),
            "result_ids": json.loads(row["result_ids"]),
            "stats": json.loads(row["stats_json"]),
        }

    def close(self) -> None:
        with self._lock:
            self._db.close()
