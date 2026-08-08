from __future__ import annotations

import asyncio
from typing import Annotated, Any, Literal

from fastmcp import Context, FastMCP
from pydantic import BaseModel, Field

from .browser import CnkiBrowser
from .engine import CnkiEngine
from .paths import PROJECT_ROOT, ensure_runtime_dirs
from .store import Store


ensure_runtime_dirs()
store = Store()
browser = CnkiBrowser()
engine = CnkiEngine(browser, store)


class StructuredCondition(BaseModel):
    """One native CNKI advanced-search row."""

    field: Literal[
        "subject",
        "title_keyword_abstract",
        "keyword",
        "title",
        "fulltext",
        "author",
        "first_author",
        "corresponding_author",
        "institution",
        "fund",
        "abstract",
        "subtitle",
        "reference",
        "classification",
        "source",
        "doi",
    ] = Field(description="CNKI 高级检索字段")
    value: str = Field(min_length=1, max_length=120, description="该字段的检索值，可使用 CNKI 支持的表达式")
    operator: Literal["AND", "OR", "NOT"] = Field(default="AND", description="与上一行的关系；第一行忽略此值")
    match: Literal["exact", "fuzzy"] = Field(default="exact", description="精确或模糊匹配")

mcp = FastMCP(
    "CNKI-Enhanced-MCP",
    instructions=(
        "智能检索中国知网论文。搜索工具内部自动生成搜索矩阵、提取候选论文元数据和摘要、"
        "重新排序；默认按相关性和质量证据自适应决定结果数量，并只返回带 CNKI 详情页链接的论文标题。"
        "登录时账号密码必须由用户直接在可见浏览器中输入，不要把密码传入工具参数。"
    ),
)


def _markdown_results(result: dict[str, Any]) -> str:
    lines = [
        f"检索完成：原始 {result['stats']['raw_results']} 条，去重后 {result['stats']['unique_results']} 篇，"
        f"返回 {len(result['results'])} 篇。",
        "",
    ]
    for index, paper in enumerate(result["results"], start=1):
        lines.append(f"{index}. [{paper['title']}]({paper['detail_url']})")
    if result.get("warnings"):
        lines.extend(["", "提示：" + "；".join(result["warnings"])])
    return "\n".join(lines)


@mcp.tool(annotations={"readOnlyHint": False, "openWorldHint": True})
async def cnki_login(
    method: Annotated[
        str,
        Field(description="登录方式：auto、campus_ip、account、institution_remote"),
    ] = "auto",
    profile: Annotated[str, Field(description="工作区内保存的浏览器身份名称")] = "default",
    remote_access_url: Annotated[
        str | None,
        Field(description="学校图书馆/WebVPN/统一认证入口，仅 institution_remote 模式需要"),
    ] = None,
    timeout_seconds: Annotated[int, Field(ge=30, le=900, description="等待用户完成登录的最长秒数")] = 300,
) -> dict[str, Any]:
    """打开可见 Playwright 浏览器，检测校园网权限或等待用户手动完成账号/机构登录。"""
    return await engine.login(method, profile, remote_access_url, timeout_seconds)


@mcp.tool(annotations={"readOnlyHint": True, "openWorldHint": True})
async def cnki_session_status(
    profile: Annotated[str, Field(description="要检查的浏览器身份名称")] = "default",
) -> dict[str, Any]:
    """检查 CNKI 可访问性、机构/账号登录状态和验证码状态。"""
    return await engine.session_status(profile)


@mcp.tool(annotations={"readOnlyHint": True, "openWorldHint": True})
async def cnki_search(
    query: Annotated[str, Field(min_length=1, description="关键词、句子或完整研究问题")],
    ctx: Context,
    mode: Annotated[str, Field(description="precise、balanced 或 broad")] = "balanced",
    limit: Annotated[
        int | None,
        Field(ge=1, le=100, description="可选返回上限；默认按相关性和质量证据自动决定数量"),
    ] = None,
    year_from: Annotated[int | None, Field(ge=1900, le=2100)] = None,
    year_to: Annotated[int | None, Field(ge=1900, le=2100)] = None,
    profile: Annotated[str, Field(description="使用的浏览器身份名称")] = "default",
) -> dict[str, Any]:
    """内部规划多个查询，去重，并结合相关性、摘要、来源层级、文献类型和影响力重新排序。"""
    async def progress(current: int, total: int, message: str) -> None:
        try:
            await ctx.report_progress(progress=current, total=max(total, 1), message=message)
        except Exception:
            return

    result = await engine.search(
        query,
        mode=mode,
        limit=limit,
        year_from=year_from,
        year_to=year_to,
        profile=profile,
        progress=progress,
    )
    result["display"] = _markdown_results(result)
    return result


@mcp.tool(annotations={"readOnlyHint": True, "openWorldHint": True})
async def cnki_structured_search(
    conditions: Annotated[
        list[StructuredCondition],
        Field(min_length=1, max_length=10, description="1到10个 CNKI 原生高级检索条件行"),
    ],
    ctx: Context,
    year_from: Annotated[int | None, Field(ge=1900, le=2100)] = None,
    year_to: Annotated[int | None, Field(ge=1900, le=2100)] = None,
    sort_by: Annotated[
        Literal["relevance", "date", "cited", "downloads"],
        Field(description="CNKI 结果页原始排序方式"),
    ] = "relevance",
    pages: Annotated[int, Field(ge=1, le=10, description="最多抓取的 CNKI 结果页数")] = 2,
    limit: Annotated[int | None, Field(ge=1, le=100, description="可选返回上限；默认质量自适应")] = None,
    metadata_depth: Annotated[
        Literal["basic", "rerank", "full"],
        Field(description="基础结果、候选增强重排或全部结果增强"),
    ] = "rerank",
    rerank_candidates: Annotated[int, Field(ge=1, le=100)] = 30,
    source_tiers: Annotated[
        list[str] | None,
        Field(description="可选本地后置来源层级过滤，如 CSSCI、北大核心、SCI、EI"),
    ] = None,
    document_types: Annotated[
        list[str] | None,
        Field(description="可选本地后置文献类型过滤，如期刊、博士论文、硕士论文、会议论文"),
    ] = None,
    profile: str = "default",
) -> dict[str, Any]:
    """独立结构化检索工具：严格执行调用者指定的 CNKI 字段、布尔关系和匹配方式。"""
    async def progress(current: int, total: int, message: str) -> None:
        try:
            await ctx.report_progress(progress=current, total=max(total, 1), message=message)
        except Exception:
            return

    result = await engine.structured_search(
        [condition.model_dump() for condition in conditions],
        year_from=year_from,
        year_to=year_to,
        sort_by=sort_by,
        pages=pages,
        limit=limit,
        metadata_depth=metadata_depth,
        rerank_candidates=rerank_candidates,
        source_tiers=source_tiers,
        document_types=document_types,
        profile=profile,
        progress=progress,
    )
    result["display"] = _markdown_results(result)
    return result


@mcp.tool(annotations={"readOnlyHint": True, "openWorldHint": True})
async def cnki_get_metadata(
    titles: Annotated[
        list[str] | None,
        Field(description="一个或多个完整论文标题；未缓存时自动执行篇名精确检索"),
    ] = None,
    search_id: Annotated[str | None, Field(description="可选的历史搜索编号；可与 titles 同时使用")] = None,
    offset: Annotated[int, Field(ge=0)] = 0,
    limit: Annotated[int, Field(ge=1, le=100)] = 20,
    fields: Annotated[
        list[str] | None,
        Field(description="可选元数据字段列表；默认返回全部公开元数据"),
    ] = None,
    refresh: Annotated[bool, Field(description="是否重新访问详情页刷新元数据")] = False,
    profile: Annotated[str, Field(description="检索未缓存标题或刷新元数据时使用的浏览器身份")] = "default",
) -> dict[str, Any]:
    """按完整标题或 search_id 统一读取单篇/批量论文元数据。"""
    return await engine.get_metadata(
        titles=titles,
        search_id=search_id,
        offset=offset,
        limit=limit,
        fields=fields,
        refresh=refresh,
        profile=profile,
    )


@mcp.tool(annotations={"readOnlyHint": False, "openWorldHint": True})
async def cnki_download_paper(
    title: Annotated[str, Field(min_length=1, description="论文完整标题；未缓存时自动执行篇名精确检索")],
    subdirectory: Annotated[
        str | None,
        Field(description=".cnki-data/downloads 下的可选子目录，不允许写到工作区外"),
    ] = None,
    preferred_format: Annotated[str, Field(description="pdf、caj 或 auto")] = "pdf",
    overwrite: bool = False,
    profile: str = "default",
) -> dict[str, Any]:
    """使用当前合法 CNKI 权限下载论文，文件始终保存在工作区项目目录内。"""
    return await engine.download(title, subdirectory, preferred_format, overwrite, profile)


@mcp.tool(annotations={"readOnlyHint": True, "openWorldHint": True})
async def cnki_read_online_html(
    title: Annotated[str, Field(min_length=1, description="论文完整标题；未缓存时自动执行篇名精确检索")],
    read_all: Annotated[
        bool,
        Field(description="默认读取当前权限允许的全部 HTML 正文；设为 false 才启用分段读取"),
    ] = True,
    offset: Annotated[int, Field(ge=0, description="从在线正文的第几个字符开始返回")] = 0,
    max_characters: Annotated[
        int,
        Field(ge=500, le=12_000, description="仅 read_all=false 时生效的单次分段字符数"),
    ] = 5_000,
    section: Annotated[
        str | None,
        Field(description="可选章节名或正文片段；从首次出现处开始读取，offset 改为相对该位置"),
    ] = None,
    profile: str = "default",
) -> dict[str, Any]:
    """使用当前合法权限读取全部或分段的 CNKI 在线 HTML 正文；不处理 PDF/CAJ。"""
    return await engine.read_online_html(
        title,
        read_all=read_all,
        offset=offset,
        max_characters=max_characters,
        section=section,
        profile=profile,
    )


@mcp.tool(annotations={"readOnlyHint": False})
async def cnki_export_citations(
    titles: Annotated[
        list[str] | None,
        Field(description="要导出的完整论文标题列表；未缓存的标题会先自动执行篇名精确检索"),
    ] = None,
    search_id: Annotated[str | None, Field(description="要导出的历史 search_id")] = None,
    format: Annotated[
        Literal[
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
        ],
        Field(description="引文样式或题录交换格式"),
    ] = "gbt7714",
    custom_template: Annotated[
        str | None,
        Field(description="format=custom 时使用；如 {authors}. {title}[{resource_type}]. {source}, {year}. {url}"),
    ] = None,
    offset: Annotated[int, Field(ge=0)] = 0,
    limit: Annotated[int, Field(ge=1, le=100)] = 100,
    save_filename: Annotated[
        str | None,
        Field(description="可选文件名；保存到工作区 .cnki-data/exports，不允许写到外部"),
    ] = None,
    profile: Annotated[str, Field(description="自动检索未缓存标题时使用的浏览器身份名称")] = "default",
) -> dict[str, Any]:
    """将缓存题录导出为常用引文、BibTeX/RIS/EndNote/CSL-JSON/CSV 或安全自定义模板。"""
    return await engine.export_citations(
        titles=titles,
        search_id=search_id,
        output_format=format,
        custom_template=custom_template,
        offset=offset,
        limit=limit,
        save_filename=save_filename,
        profile=profile,
    )


@mcp.tool(annotations={"readOnlyHint": True, "openWorldHint": True})
async def cnki_link_references(
    text: Annotated[str, Field(min_length=1, description="包含《论文标题》的 Markdown 或纯文本")],
    min_confidence: Annotated[float, Field(ge=0.7, le=1.0)] = 0.9,
    search_missing: Annotated[bool, Field(description="本地缓存无结果时是否搜索 CNKI")] = True,
    profile: str = "default",
) -> dict[str, Any]:
    """把文本中《书名号》标出的论文标题高置信度匹配到 CNKI 详情页并添加 Markdown 链接。"""
    return await engine.link_references(
        text,
        min_confidence=min_confidence,
        search_missing=search_missing,
        profile=profile,
    )


def main() -> None:
    try:
        mcp.run(transport="stdio")
    finally:
        try:
            asyncio.run(browser.close())
        except Exception:
            pass
        store.close()


if __name__ == "__main__":
    main()
