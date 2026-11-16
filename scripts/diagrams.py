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
    b.append(text(600, 184, L["server_sub"], 14, SUB, font=f))
    tools = ["login", "status", "search", "results", "get_metadata", "read", "download", "export", "link_references"]
    widths = [11 + len(t) * 8.6 for t in tools]
    x, y = 600 - (sum(widths) + 10 * (len(tools) - 1)) / 2, 206
    for t, w in zip(tools, widths):
        b.append(f'<rect x="{x:.0f}" y="{y}" width="{w:.0f}" height="28" rx="14" fill="#eaf2ff" stroke="#b9d1f7"/>')
        b.append(text(round(x + w / 2), y + 19, t, 13.5, INK, 600, font="SFMono-Regular,Menlo,Consolas,monospace"))
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

ARCH_ZH = dict(zh=True, label="CNKI Enhanced MCP 2.1 架构", client="MCP 客户端（可同时连接多个）", client_sub="Claude、Codex、Cursor、VS Code、Gemini CLI 等；每个客户端启动一个 stdio 桥接",
    server="共享 MCP 后端（9 个工具）", server_sub="每个数据目录一个后端进程，经本机回环地址和随机密钥连接；断开只取消该客户端的任务，最后一个客户端断开后退出",
    jobs="任务管理", jobs_sub=["请求与时间预算、检查点", "results(continue=true) 续跑"],
    research="检索流程", research_sub=["概念规划、多通道检索、读取详情", "关键词反馈补检、排序筛选"],
    store="本地存储（SQLite）", store_sub=["cnki.sqlite（WAL）：论文、期刊、", "检索、正文快照、文件、任务"],
    site="站点访问", site_sub="检索模板、每页 50 条的列表、详情页、期刊页、HTML 阅读、下载；验证或登录交给用户",
    limiter="请求限速", limiter_sub="请求开始至少间隔 500 ms，最多 6 个在途；429/503 时间隔加倍；人工验证期间暂停",
    browser="专用浏览器（Chrome / Edge / Chromium）", browser_sub="独立配置目录保存登录；最小化运行，只在登录和知网要求验证时显示窗口", cnki="中国知网", cnki_sub="kns.cnki.net、navi.cnki.net")
ARCH_EN = dict(zh=False, label="CNKI Enhanced MCP 2.1 architecture", client="MCP clients (several at once)", client_sub="Claude, Codex, Cursor, VS Code, Gemini CLI and others; each client starts one stdio bridge",
    server="Shared MCP backend (9 tools)", server_sub="one backend per data root, reached over loopback with a random secret; a disconnect cancels only that client's operations",
    jobs="Operations", jobs_sub=["request and time budgets, checkpoints", "results(continue=true) resumes"],
    research="Search pipeline", research_sub=["planning, channels, detail pages", "keyword feedback round, ranking"],
    store="Local store (SQLite)", store_sub=["cnki.sqlite (WAL): papers, journals,", "searches, snapshots, files, operations"],
    site="Site access", site_sub="search template, 50-row grid pages, detail and journal pages, HTML reader, downloads; verification goes to the user",
    limiter="Request limiter", limiter_sub="starts at least 500 ms apart, at most 6 in flight; 429/503 double the spacing; paused during verification",
    browser="Dedicated browser (Chrome / Edge / Chromium)", browser_sub="own profile keeps the login; minimized, shown only for login and CNKI verification", cnki="CNKI", cnki_sub="kns.cnki.net, navi.cnki.net")

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
    ("概念规划", ["必要概念", "组间 AND", "同义词 OR", "或直接给出检索式"]),
    ("多通道检索", [("主题",), ("篇名",), ("关键词",), "每页 50 条，通道并发"]),
    ("去重与初排", ["按知网编号、DOI", "或完整书目合并", "初步排序", "选出详情池"]),
    ("读取详情", [("摘要、关键词、DOI",), ("期刊收录与影响因子",), ("30 天内读过的不再读",), "单篇失败不影响其他"]),
    ("反馈补检", ["前 10 篇相关文献", "≥2 篇共有关键词", "最多 2 个通道"]),
    ("排序筛选", ["相关性", "来源质量", "最多选出 100 篇"])],
    band1="所有请求经过同一个限速器；完成的检索缓存 10 分钟，中断或预算用完时用 results(continue=true) 续跑",
    band2="出现验证码或登录页面时暂停全部请求，窗口切到前台由用户处理，完成后任务自动继续")
PIPE_EN = dict(zh=False, label="Search pipeline", steps=[
    ("Planning", ["required concepts", "AND between groups", "OR for synonyms", "or an expression"]),
    ("Channels", [("subject",), ("title",), ("keywords",), "50 rows a page, in parallel"]),
    ("Dedup & rank", ["merge by CNKI ID,", "DOI or bibliography", "initial ranking", "pick detail pool"]),
    ("Detail pages", [("abstract, keywords, DOI",), ("journal indexing, impact",), ("skip pages read in 30 days",), "one failure does not stop others"]),
    ("Feedback", ["top 10 relevant", "keywords in 2+", "up to 2 channels"]),
    ("Ranking", ["relevance", "source quality", "up to 100 selected"])],
    band1="Every request goes through one limiter; a finished search is cached 10 minutes; results(continue=true) resumes a stopped one",
    band2="On a verification or login page all requests pause and the window comes to the front; the task continues once the user is done")

for name, fn, zh, en in [("architecture", architecture, ARCH_ZH, ARCH_EN), ("search-flow", pipeline, PIPE_ZH, PIPE_EN)]:
    open(os.path.join(OUT, f"{name}.svg"), "w").write(fn(zh))
    open(os.path.join(OUT, f"{name}.en.svg"), "w").write(fn(en))
print("ok")
