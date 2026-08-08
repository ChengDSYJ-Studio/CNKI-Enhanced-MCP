from __future__ import annotations


def extract_marked_titles(text: str, *, minimum_length: int = 2, maximum_length: int = 200) -> list[str]:
    """Extract outer 《...》 spans while preserving nested Chinese title marks."""
    output: list[str] = []
    depth = 0
    start: int | None = None
    for index, character in enumerate(text):
        if character == "《":
            if depth == 0:
                start = index + 1
            depth += 1
        elif character == "》" and depth:
            depth -= 1
            if depth == 0 and start is not None:
                title = text[start:index].strip()
                if minimum_length <= len(title) <= maximum_length and title not in output:
                    output.append(title)
                start = None
    return output
