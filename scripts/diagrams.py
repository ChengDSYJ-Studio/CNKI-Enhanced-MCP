"""Regenerate the README diagrams in docs/ (blue and white theme, Chinese and English)."""
import os, html
OUT = os.environ.get("OUT") or os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "docs")
ZH = "PingFang SC,Microsoft YaHei,Noto Sans CJK SC,sans-serif"
EN = "Inter,Segoe UI,Helvetica,Arial,sans-serif"
DARK, MINT, INK, SUB, LINE, BG = "#0b2c63", "#8cc8ff", "#1d5fd1", "#4f6481", "#cfdcf0", "#ffffff"
def esc(s): return html.escape(s, quote=True)
def text(x, y, s, size=16, fill=INK, weight=400, anchor="middle", font=ZH, ls=0):
    return f'<text x="{x}" y="{y}" fill="{fill}" font-family="{font}" font-size="{size}" font-weight="{weight}" text-anchor="{anchor}"{f" letter-spacing=\"{ls}\"" if ls else ""}>{esc(s)}</text>'
def box(x, y, w, h, fill="#fff", stroke=LINE, r=12, sw=1.5):
    return f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{r}" fill="{fill}" stroke="{stroke}" stroke-width="{sw}"/>'
def arrow(x1, y1, x2, y2, color="#7f95b5"):
    return f'<line x1="{x1}" y1="{y1}" x2="{x2}" y2="{y2}" stroke="{color}" stroke-width="2" marker-end="url(#a)"/>'
DEFS = '<defs><marker id="a" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto"><path d="M0 0L10 5L0 10z" fill="#7f95b5"/></marker></defs>'
def svg(w, h, label, body):
    return f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" viewBox="0 0 {w} {h}" role="img" aria-label="{esc(label)}">{DEFS}<rect x="0.5" y="0.5" width="{w - 1}" height="{h - 1}" rx="16" fill="{BG}" stroke="#dbe6f6"/>{body}</svg>\n'

# ---------- banners ----------
def banner(sub, font, label):
    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="260" viewBox="0 0 1200 260" role="img" aria-label="{esc(label)}">
<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#0a2a5e"/><stop offset="1" stop-color="#1a5bc4"/></linearGradient></defs>
<rect width="1200" height="260" rx="22" fill="url(#g)"/>
<circle cx="1100" cy="40" r="165" fill="#ffffff" fill-opacity=".06"/><circle cx="1100" cy="40" r="115" fill="none" stroke="#ffffff" stroke-opacity=".18"/>
<circle cx="980" cy="250" r="70" fill="#ffffff" fill-opacity=".05"/>
<path d="M67 74h58c21 0 32 11 32 26v105c-7-13-21-19-39-19H67zM157 100c0-15 11-26 32-26h58v112h-51c-18 0-32 6-39 19z" fill="none" stroke="#ffffff" stroke-width="4" stroke-linejoin="round"/>
<path d="M87 101h40M87 119h40M87 137h26" stroke="#8cc8ff" stroke-width="3" stroke-linecap="round"/>
<circle cx="213" cy="143" r="29" fill="#123f8c" stroke="#ffffff" stroke-width="4"/><path d="m234 164 28 28" stroke="#ffffff" stroke-width="7" stroke-linecap="round"/>
<text x="310" y="108" fill="#ffffff" font-family="{EN}" font-size="49" font-weight="700">CNKI Enhanced MCP</text>
<text x="312" y="155" fill="#dbe8ff" font-family="{font}" font-size="23">{esc(sub)}</text>
<text x="313" y="205" fill="#8cc8ff" font-family="{EN}" font-size="14" letter-spacing="3">DEEP SEARCH · LOCAL SESSION · TRACEABLE EVIDENCE</text>
</svg>
"""
open(os.path.join(OUT, "banner.svg"), "w").write(banner("让 AI 完成知网文献检索、阅读与整理", ZH, "CNKI Enhanced MCP，让 AI 完成知网文献检索、阅读与整理"))
open(os.path.join(OUT, "banner.en.svg"), "w").write(banner("Let AI search, read and organize CNKI literature", EN, "CNKI Enhanced MCP: let AI search, read and organize CNKI literature"))

# ---------- architecture ----------
def architecture(L):
    f = ZH if L["zh"] else EN
    W = 1200; b = []
    # client
    b.append(box(40, 30, 1120, 70, fill="#fff"))
    b.append(text(600, 62, L["client"], 19, INK, 700, font=f))
    b.append(text(600, 86, L["client_sub"], 14, SUB, font=f))
    b.append(arrow(600, 100, 600, 128))
    # server with tool chips
    b.append(box(40, 130, 1120, 128, fill="#ffffff", stroke=INK, sw=2))
    b.append(text(600, 160, L["server"], 19, INK, 700, font=f))
    tools = ["search", "structured_search", "search_results", "get_metadata", "export_citations", "read_online_html", "download_paper",
             "link_references", "login", "session_status", "operation_status", "operation_control", "diagnostics"]
    x, y = 70, 178
    for t in tools:
        w = 11 + len(t) * 8.6
        if x + w > 1135: x, y = 70 + 60, y + 38
        b.append(f'<rect x="{x}" y="{y}" width="{w:.0f}" height="28" rx="14" fill="#eaf2ff" stroke="#b9d1f7"/>')
        b.append(text(x + w / 2, y + 19, t, 13.5, INK, 600, font="SFMono-Regular,Menlo,Consolas,monospace"))
        x += w + 10
    for xx in (230, 600, 970): b.append(arrow(xx, 258, xx, 288))
    # three middle boxes
    mids = [(40, L["jobs"], L["jobs_sub"]), (420, L["research"], L["research_sub"]), (800, L["store"], L["store_sub"])]
    for (bx, t, sub) in mids:
        b.append(box(bx, 290, 360, 110))
        b.append(text(bx + 180, 324, t, 18, INK, 700, font=f))
        for i, line in enumerate(sub): b.append(text(bx + 180, 352 + i * 22, line, 14, SUB, font=f))
    b.append(arrow(600, 400, 600, 428))
    # site
    b.append(box(40, 430, 1120, 86))
    b.append(text(600, 462, L["site"], 18, INK, 700, font=f))
    b.append(text(600, 490, L["site_sub"], 14, SUB, font=f))
    b.append(arrow(600, 516, 600, 544))
    # limiter (accent)
    b.append(box(40, 546, 1120, 76, fill=DARK, stroke=DARK))
    b.append(text(600, 578, L["limiter"], 19, "#ffffff", 700, font=f))
    b.append(text(600, 604, L["limiter_sub"], 14, MINT, font=f))
    b.append(arrow(600, 622, 600, 650))
    # browser + cnki
    b.append(box(40, 652, 740, 70))
    b.append(text(410, 682, L["browser"], 18, INK, 700, font=f))
    b.append(text(410, 706, L["browser_sub"], 14, SUB, font=f))
    b.append(arrow(780, 687, 836, 687))
    b.append(box(840, 652, 320, 70, fill="#eaf2ff", stroke="#b9d1f7"))
    b.append(text(1000, 682, L["cnki"], 18, INK, 700, font=f))
    b.append(text(1000, 706, L["cnki_sub"], 14, SUB, font=f))
    return svg(W, 750, L["label"], "".join(b))

ARCH_ZH = dict(zh=True, label="CNKI Enhanced MCP 架构", client="MCP 客户端", client_sub="Claude、Codex、Cursor、VS Code、Gemini CLI 等（本地 stdio）",
    server="MCP 服务（13 个工具）", jobs="任务管理", jobs_sub=["后台任务、进度查询", "预算、检查点、继续执行"],
    research="检索流程", research_sub=["多字段检索、读取详情", "补充检索、排序筛选"],
    store="本地存储", store_sub=["题录、期刊信息、检索结果", "定期写入 research.json"],
    site="站点访问", site_sub="检索请求、页面解析、页面渲染、新标签页跟踪、HTML 阅读、文件下载",
    limiter="请求限速", limiter_sub="所有请求按间隔发起（默认每秒 2 次），人工验证期间暂停",
    browser="专用浏览器（Chrome / Edge / Chromium）", browser_sub="保存登录状态，后台最小化运行", cnki="中国知网", cnki_sub="kns.cnki.net、navi.cnki.net、bar.cnki.net")
ARCH_EN = dict(zh=False, label="CNKI Enhanced MCP architecture", client="MCP client", client_sub="Claude, Codex, Cursor, VS Code, Gemini CLI and others (local stdio)",
    server="MCP server (13 tools)", jobs="Operations", jobs_sub=["background tasks, progress", "budgets, checkpoints, continue"],
    research="Search workflow", research_sub=["field searches, detail pages", "follow-up search, ranking"],
    store="Local store", store_sub=["records, journals, results", "saved to research.json"],
    site="Site access", site_sub="search requests, page parsing, rendering, new-tab tracking, HTML reading, downloads",
    limiter="Request limiter", limiter_sub="requests start at a fixed interval (default 2 per second); paused during verification",
    browser="Dedicated browser (Chrome / Edge / Chromium)", browser_sub="keeps login state, runs minimized", cnki="CNKI", cnki_sub="kns.cnki.net, navi.cnki.net, bar.cnki.net")

# ---------- pipeline ----------
def pipeline(L):
    f = ZH if L["zh"] else EN
    b = []
    cols = [(30, 150), (215, 210), (460, 165), (660, 250), (945, 120), (1100 - 25, 115)]
    xs = [24, 194, 424, 604, 874, 1034]
    ws = [150, 210, 160, 250, 140, 142]
    steps = L["steps"]
    for i, (x, w) in enumerate(zip(xs, ws)):
        b.append(box(x, 40, w, 210))
        b.append(f'<circle cx="{x + 22}" cy="64" r="13" fill="{DARK}"/>')
        b.append(text(x + 22, 69, str(i + 1), 14, "#fff", 700, font=EN))
        b.append(text(x + w / 2 + 12, 69, steps[i][0], 16 if L["zh"] else 14.5, INK, 700, font=f))
        yy = 90
        for line in steps[i][1]:
            if isinstance(line, tuple):  # lane chip
                b.append(f'<rect x="{x + 12}" y="{yy}" width="{w - 24}" height="26" rx="13" fill="#eaf2ff" stroke="#b9d1f7"/>')
                b.append(text(x + w / 2, yy + 18, line[0], 13.5, INK, 600, font=f))
                yy += 34
            else:
                b.append(text(x + w / 2, yy + 18, line, 13.5, SUB, font=f))
                yy += 26
        if i < 5: b.append(arrow(x + w + 2, 145, xs[i + 1] - 4, 145))
    b.append(box(24, 272, 1152, 46, fill=DARK, stroke=DARK, r=10))
    b.append(text(600, 301, L["band1"], 15, "#fff", 600, font=f))
    b.append(box(24, 330, 1152, 46, fill="#fff", r=10))
    b.append(text(600, 359, L["band2"], 15, SUB, 400, font=f))
    return svg(1200, 400, L["label"], "".join(b))

PIPE_ZH = dict(zh=True, label="检索流程", steps=[
    ("概念规划", ["必要概念", "组间 AND", "同义词 OR"]),
    ("多字段检索", [("主题",), ("篇名",), ("关键词",), "每轮三个字段同时检索"]),
    ("去重与初排", ["合并重复记录", "初步排序", "选取需读详情", "的文献"]),
    ("读取详情", [("摘要、关键词、DOI",), ("期刊收录与影响因子",), ("必要时渲染页面",), "单篇失败不影响其他"]),
    ("补充检索", ["共同关键词", "最多 2 个条件", "保留原有概念"]),
    ("排序筛选", ["相关性", "来源质量", "筛选结果"])],
    band1="所有请求经过同一个限速器，按固定间隔发起",
    band2="出现验证码或登录页面时暂停请求，由用户在弹出的页面中处理，完成后继续")
PIPE_EN = dict(zh=False, label="Search workflow", steps=[
    ("Planning", ["required concepts", "AND between groups", "OR for synonyms"]),
    ("Field searches", [("subject",), ("title",), ("keywords",), "searched together each round"]),
    ("Dedup & rank", ["merge duplicates", "initial ranking", "choose papers", "for detail pages"]),
    ("Detail pages", [("abstract, keywords, DOI",), ("journal indexing, impact",), ("render when needed",), "one failure does not stop others"]),
    ("Follow-up", ["shared keywords", "up to 2 searches", "concepts kept"]),
    ("Ranking", ["relevance", "source quality", "selection"])],
    band1="Every request goes through one limiter and starts at a fixed interval",
    band2="On a verification or login page, requests pause; the user handles it in the opened page and the task continues")

for name, fn, zh, en in [("architecture", architecture, ARCH_ZH, ARCH_EN), ("search-flow", pipeline, PIPE_ZH, PIPE_EN)]:
    open(os.path.join(OUT, f"{name}.svg"), "w").write(fn(zh))
    open(os.path.join(OUT, f"{name}.en.svg"), "w").write(fn(en))
print("ok")
