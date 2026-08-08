from pathlib import Path

from cnki_mcp.models import Paper
from cnki_mcp.store import Store


def test_store_roundtrip(tmp_path: Path) -> None:
    store = Store(tmp_path / "test.sqlite3")
    item = Paper(
        paper_id="test-paper",
        title="测试论文",
        detail_url="https://example.test/paper",
        abstract="摘要",
    )
    store.save_paper(item)
    restored = store.get_paper("test-paper")
    assert restored is not None
    assert restored.title == "测试论文"
    store.close()
