from __future__ import annotations

import re
from typing import Any
from urllib.parse import urlparse


ACCESS_DENIED_MARKERS = (
    "暂无权限",
    "没有权限",
    "请购买",
    "立即购买",
    "订购后阅读",
    "机构未订购",
)

TRIAL_TRUNCATION_MARKERS = (
    "试读结束",
    "试读内容结束",
    "仅供试读",
    "购买后阅读全文",
    "订购后阅读全文",
    "剩余内容请购买",
)


def is_cnki_url(url: str) -> bool:
    hostname = (urlparse(url).hostname or "").lower()
    return hostname == "cnki.net" or hostname.endswith(".cnki.net")


def clean_online_text(value: str) -> str:
    value = value.replace("\r\n", "\n").replace("\r", "\n").replace("\u00a0", " ")
    lines = [re.sub(r"[ \t]+", " ", line).strip() for line in value.splitlines()]
    lines = [line for line in lines if not line.startswith("Loading [MathJax]")]
    output: list[str] = []
    blank = False
    for line in lines:
        if not line:
            if output and not blank:
                output.append("")
            blank = True
            continue
        blank = False
        if not output or output[-1] != line:
            output.append(line)
    return "\n".join(output).strip()


def slice_online_text(text: str, offset: int, max_characters: int) -> dict[str, Any]:
    offset = min(max(offset, 0), len(text))
    end = min(offset + max_characters, len(text))
    if end < len(text):
        boundary = text.rfind("\n", offset + max_characters // 2, end)
        if boundary > offset:
            end = boundary
    content = text[offset:end].strip()
    return {
        "content": content,
        "offset": offset,
        "next_offset": end if end < len(text) else None,
        "has_more": end < len(text),
        "total_characters": len(text),
        "returned_characters": len(content),
    }


def reader_access(url: str, text: str) -> tuple[str, str, str]:
    parsed = urlparse(url)
    marker = next((item for item in TRIAL_TRUNCATION_MARKERS if item in text), None)
    status = "trial" if marker else "authorized"
    safe_url = f"{parsed.scheme}://{parsed.netloc}{parsed.path}"
    evidence = f"explicit_marker:{marker}" if marker else "no_trial_truncation_marker"
    return status, safe_url, evidence
