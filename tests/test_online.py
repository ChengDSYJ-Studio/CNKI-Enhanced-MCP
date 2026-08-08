from cnki_mcp.online import clean_online_text, is_cnki_url, reader_access, slice_online_text


def test_cnki_url_allowlist() -> None:
    assert is_cnki_url("https://bar.cnki.net/path")
    assert is_cnki_url("https://cnki.net/path")
    assert not is_cnki_url("https://cnki.net.example.com/path")
    assert not is_cnki_url("javascript:alert(1)")


def test_clean_and_slice_online_text() -> None:
    text = clean_online_text("Loading [MathJax]/font.js\n第一章  \r\n\r\n  内容一\n内容一\n\n第二章\n内容二")
    assert text == "第一章\n\n内容一\n\n第二章\n内容二"
    result = slice_online_text(text * 100, 0, 500)
    assert result["content"]
    assert result["has_more"] is True
    assert result["next_offset"] is not None


def test_reader_access_removes_temporary_tokens() -> None:
    status, safe_url, evidence = reader_access(
        "https://kns.cnki.net/reader/xml?invoice=secret&nonce=secret&loginType=trialRead",
        "完整正文和参考文献",
    )
    assert status == "authorized"
    assert safe_url == "https://kns.cnki.net/reader/xml"
    assert "secret" not in safe_url
    assert evidence == "no_trial_truncation_marker"


def test_reader_access_uses_explicit_trial_marker() -> None:
    status, _, evidence = reader_access("https://kns.cnki.net/reader/xml", "正文片段\n试读结束")
    assert status == "trial"
    assert evidence == "explicit_marker:试读结束"
