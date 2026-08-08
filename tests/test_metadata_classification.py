from cnki_mcp.engine import _decimal_after_label, _record_context, _resource_type, _source_tiers


def test_resource_type_from_database_code_and_labels() -> None:
    assert _resource_type("dbcode=CJFD") == "期刊"
    assert _resource_type("databaseName=CMFD") == "硕士论文"
    assert _resource_type("博士学位论文") == "博士论文"
    assert _resource_type("学术会议论文") == "会议论文"
    assert _resource_type("本科毕业论文") == "本科论文"
    assert _resource_type("导师：张三 学科专业：法律硕士（专业学位） 章节下载") == "硕士论文"


def test_record_context_excludes_global_navigation() -> None:
    body = "图书 学位论文 期刊 全站导航\n" + "目标论文" + "\n导师：张三\n学科专业：民商法学"
    context = _record_context(body, "目标论文")
    assert context.endswith("学科专业：民商法学")
    assert _resource_type(context) == "学位论文"


def test_source_tiers_from_cnki_journal_navigation_text() -> None:
    value = "北大核心 AMI核心 CSSCI 北京大学《中文核心期刊要目总览》来源期刊"
    assert _source_tiers(value) == ["CSSCI", "北大核心", "AMI核心"]


def test_ssci_is_not_accidentally_extracted_from_cssci() -> None:
    assert _source_tiers("CSSCI") == ["CSSCI"]


def test_cnki_impact_factors_are_extracted() -> None:
    body = "(2025版)复合影响因子：8.963\n(2025版)综合影响因子：4.787"
    assert _decimal_after_label(body, "复合影响因子") == 8.963
    assert _decimal_after_label(body, "综合影响因子") == 4.787
