# CNKI-Enhanced-MCP

让 AI 真正使用中国知网检索、筛选和处理学术文献。

CNKI-Enhanced-MCP 是一个基于 Playwright 的本地 MCP Server。你可以直接输入一个词、一句话或完整研究问题，它会操作知网页面完成检索，读取候选论文的摘要与来源信息，综合相关性和学术质量重新排序，最后返回带知网详情页链接的论文标题。

它也支持知网原生高级检索、完整题录读取、当前权限下的在线 HTML 正文、PDF/CAJ 下载、引文导出，以及为文本中的论文标题添加知网链接。

> [!IMPORTANT]
> 本项目是非官方开源工具，与中国知网（CNKI）及同方知网数字科技有限公司没有隶属、授权或背书关系。请遵守 CNKI 服务条款、版权规则、访问频率限制和所在机构的数据库许可。本项目不会绕过验证码、付费墙或访问控制。

## 为什么使用它

普通关键词搜索往往会同时返回高度相关但质量较低的本科论文、普通校刊、低影响力会议论文，以及题目相似但主题不同的记录。CNKI-Enhanced-MCP 在知网检索结果之上增加了一层面向研究工作的处理：

- 自动把自然语言问题拆成适合知网的搜索矩阵；
- 多关键词场景自动使用高级检索，并处理组内 OR、组间 AND；
- 提取标题、作者、摘要、关键词、机构、基金、来源、文献类型、收录层级、影响因子、被引和下载量等元数据；
- 先判断是否真正相关，再结合来源与文献质量重新排序；
- 不固定返回 10 篇：优质结果多就多返回，少就少返回，不用低质量论文补足数量；
- 默认响应只展示论文标题和详情页链接，减少 MCP 上下文消耗；
- 所有单篇论文操作都使用完整标题，不要求先搜索或保存内部编号。

## 主要功能

### 智能搜索

输入“违约金”“自动驾驶”，或者“自动驾驶交通事故中生产者责任如何认定”这样的研究问题。MCP 会自行规划检索字段、搜索矩阵和候选增强过程。

自然搜索始终执行质量增强，调用者不能关闭摘要和来源检查。公开参数只有：

```text
query · mode · limit · year_from · year_to · profile
```

### 知网原生结构化检索

当你已经知道要检索哪些字段时，可以直接指定篇名、主题、关键词、作者、机构、基金、摘要、来源、DOI 等条件，并组合 AND、OR、NOT、年份、排序、来源层级和文献类型过滤。

### 元数据、正文和下载

- 按一个或多个完整标题，或者按一次搜索的 `search_id`，读取完整题录与摘要；
- 使用当前账号或机构权限读取知网提供的在线 HTML/XML 正文；
- 使用当前合法权限下载 PDF 或 CAJ，但不会解析 PDF/CAJ；
- 未搜索过的标题会自动执行知网“篇名 + 精确”检索，只有完整标题匹配才会采用，不会用相似论文代替。

### 引文和文本处理

- 支持 GB/T 7714、APA、MLA、Chicago、Vancouver；
- 支持 BibTeX、RIS、EndNote、CSL-JSON、JSON、CSV、Markdown；
- 支持使用白名单字段定义安全的自定义导出模板；
- 可以把文本中的 `《论文标题》` 转换为指向知网详情页的 Markdown 链接。

## 快速开始

### 环境要求

- Windows、macOS 或 Linux；
- Python 3.11 或更高版本；
- 能正常访问 CNKI 的网络环境；
- 在线全文和下载功能需要相应的个人账号、校园网或机构订阅权限。

项目会把 Python 环境、Playwright Chromium、登录状态和运行数据全部放在项目目录中，不会把这些文件散落到用户主目录。

### 一键安装

下载或克隆项目后，在项目目录运行：

macOS / Linux：

```bash
python3 install.py
```

Windows PowerShell：

```powershell
py install.py
```

如果 Linux 还没有 Chromium 所需的系统库：

```bash
python3 install.py --with-system-deps
```

安装器会自动：

1. 在项目中创建 `.venv`；
2. 安装 CNKI-Enhanced-MCP；
3. 把 Chromium 下载到 `.playwright-browsers`；
4. 创建 `.cnki-data` 运行目录；
5. 生成包含绝对路径的 `mcp-config.generated.json`。

### 添加到 MCP 客户端

打开安装器生成的 `mcp-config.generated.json`，把其中的 `cnki` 配置复制到支持 MCP 的客户端，然后重启客户端。

配置结构如下，实际路径以生成文件为准：

```json
{
  "mcpServers": {
    "cnki": {
      "command": "/ABSOLUTE/PATH/CNKI-Enhanced-MCP/.venv/bin/cnki-enhanced-mcp",
      "env": {
        "PLAYWRIGHT_BROWSERS_PATH": "/ABSOLUTE/PATH/CNKI-Enhanced-MCP/.playwright-browsers",
        "CNKI_MCP_DATA_DIR": "/ABSOLUTE/PATH/CNKI-Enhanced-MCP/.cnki-data"
      }
    }
  }
}
```

本项目不会自动修改任何 MCP 客户端的全局配置。

## 第一次使用

先让 MCP 调用 `cnki_login`。支持三类环境：

- 校园网或机构 IP 已经拥有访问权限；
- 知网个人账号登录；
- 学校图书馆、WebVPN 或统一身份认证等校外入口。

账号密码和学校 SSO 凭据应由你直接在打开的浏览器中输入，不要把密码发送给模型。登录完成后，浏览器 profile 会保存在项目的 `.cnki-data/browser-profiles` 中供以后复用。

除登录外，搜索使用的可见 Chromium 会自动最小化，尽量减少对桌面工作的干扰。知网可能对无头浏览器触发额外安全验证，因此当前搜索固定使用有头浏览器。

## 使用示例

### 自然语言搜索

```json
{
  "query": "自动驾驶交通事故中的生产者责任",
  "mode": "balanced",
  "year_from": 2020
}
```

`mode` 可以是：

- `precise`：更严格，适合概念明确的主题；
- `balanced`：默认选择，兼顾召回与准确性；
- `broad`：扩大候选范围，但不会降低最终质量门槛。

`limit` 是返回数量上限，不是必须凑满的数量。如果只有 3 篇达到相关性和质量标准，即使 `limit=10` 也只返回 3 篇。

### 结构化检索

```json
{
  "conditions": [
    {"field": "subject", "value": "违约金", "match": "fuzzy"},
    {"field": "author", "value": "王利明", "operator": "AND", "match": "exact"}
  ],
  "year_from": 2020,
  "sort_by": "cited",
  "pages": 3
}
```

结构化检索忠实执行调用者指定的知网字段和布尔关系，不会替代自然语言搜索。适合系统综述、查找特定作者、限定期刊或构造可复现检索式。

### 读取论文元数据

```json
{
  "titles": ["违约金酌减规则论"],
  "fields": ["title", "authors", "source", "year", "abstract", "keywords"],
  "refresh": true
}
```

也可以传入 `search_id` 分页读取整次搜索的结果。`titles` 和 `search_id` 可以同时使用，重复论文会自动合并。

### 读取在线 HTML 正文

```json
{
  "title": "完整论文标题",
  "read_all": true
}
```

默认返回当前权限允许的全部在线正文。需要控制上下文时，可以设置 `read_all=false`，再使用 `offset`、`max_characters` 或 `section` 分段读取。

返回中的：

- `complete_available_content` 表示知网已经交付的在线内容是否全部返回；
- `complete_article` 表示当前拥有完整授权，且正文已经全部返回；
- `status=trial` 只在正文出现明确试读截断证据时使用，不根据阅读器 URL 的路由参数猜测权限。

### 导出引文

```json
{
  "titles": ["违约金酌减规则论"],
  "format": "bibtex",
  "save_filename": "references.bib"
}
```

也可以用一次搜索的 `search_id` 批量导出。生成文件只会写入 `.cnki-data/exports`。

自定义格式示例：

```json
{
  "titles": ["违约金酌减规则论"],
  "format": "custom",
  "custom_template": "{authors}.《{title}》[{resource_type}].{source},{year}:{pages}.{url}"
}
```

### 为文本添加知网链接

输入：

```text
关于违约金调整，可以参考《违约金酌减规则论》。
```

工具会保留原文并把完整论文标题转换成知网详情页链接。标题中含有嵌套书名号时也可以处理。

## MCP 工具

| 工具 | 用途 |
|---|---|
| `cnki_login` | 检测校园网/IP 权限，或打开浏览器等待账号、学校校外入口登录 |
| `cnki_session_status` | 检查当前登录、机构访问和验证码状态 |
| `cnki_search` | 自然语言智能检索、搜索矩阵、摘要与质量重排 |
| `cnki_structured_search` | 执行调用者指定的知网原生高级检索条件 |
| `cnki_get_metadata` | 按完整标题或 `search_id` 读取单篇/批量元数据 |
| `cnki_download_paper` | 使用当前权限下载 PDF/CAJ 到项目目录 |
| `cnki_read_online_html` | 读取当前权限下的在线 HTML/XML 正文 |
| `cnki_export_citations` | 导出常用引文样式、交换格式或自定义格式 |
| `cnki_link_references` | 为文本中的完整论文标题添加知网链接 |

## 搜索与排序是怎样工作的

自然搜索大致经过以下过程：

1. 规范化用户输入，识别中文概念、英文缩写、标准号和版本标识；
2. 自动构造概念组和知网搜索矩阵；
3. 单一短语使用快速检索，多关键词使用高级检索；
4. 分别执行主题、篇名和关键词检索，并合并重复记录；
5. 根据标题、关键词、矩阵命中和标识符进行第一轮相关性排序；
6. 动态选择高潜力候选，读取摘要和来源质量信息；
7. 在确认相关的论文中提取少量反馈词，必要时执行一次有边界的补充检索；
8. 先应用相关性门槛，再综合相关性与质量证据重新排序；
9. 使用绝对质量门槛和头部相对门槛决定最终返回数量。

质量增强始终执行。候选不超过 30 篇时全部增强；31～100 篇以 40 篇为目标；101～300 篇以 60 篇为目标；更大结果集以 80 篇为目标。所有详情访问还受到单篇和总时间预算约束，避免失控访问。

质量证据包括：

- CSSCI、北大核心、SCI、SSCI、EI、CSCD、AMI 等可验证收录信息；
- 期刊复合影响因子和综合影响因子；
- 按发表年份校正的被引表现；
- 期刊、博士论文、硕士论文、本科论文、会议论文、报纸等文献类型；
- 摘要、关键词与原始问题的真实相关性。

系统不会维护“不知名大学”或“差期刊”黑名单。无法确认质量时会保守降权，避免某一普通校刊仅因标题高度相关就排在可靠研究之前。

## 数据、隐私与文件位置

所有运行数据默认位于项目目录：

```text
.playwright-browsers/       Playwright Chromium
.cnki-data/
├── browser-profiles/       登录状态与 Cookie
├── diagnostics/            页面改版或失败诊断
├── downloads/              下载的 PDF/CAJ
├── exports/                引文与题录导出文件
└── cnki.sqlite3            搜索记录与元数据缓存
```

删除项目目录即可一并删除运行环境和本地数据。`.cnki-data` 可能包含敏感 Cookie、机构身份和下载记录，请勿提交到 GitHub、发送给他人或包含在公开压缩包中。

本项目遵循以下边界：

- 密码不作为 MCP 参数，不进入模型上下文；
- 验证码只能由用户在可见浏览器中完成；
- 不绕过付费墙、试读限制或机构权限；
- 在线正文不持久化，阅读器临时授权参数不会返回给客户端；
- 下载和导出路径限制在项目工作区内；
- 自定义引文模板只进行白名单字段替换，不执行表达式或代码。

## 平台支持与限制

安装和路径逻辑支持 Windows、macOS 与 Linux。当前真实端到端验证环境为 macOS arm64、Python 3.13 和 Playwright Chromium；Windows/Linux 分支经过自动化测试，但仍欢迎对应平台的实际反馈。

使用时还应了解：

- CNKI 会不定期修改页面结构，页面改版后可能需要更新选择器；
- 搜索和详情访问是浏览器自动化，速度慢于官方 API；
- CNKI 风控、验证码、账号状态和机构订阅会影响实际结果；
- 在线 HTML 并非所有论文都提供，完整程度取决于当前合法权限；
- PDF/CAJ 只负责下载，不读取或解析；
- 引文格式基于知网现有元数据生成，正式投稿前仍应按目标期刊规范复核。

## 开发

安装开发依赖：

```bash
python3 install.py --dev
```

运行测试：

```bash
./scripts/test.sh
```

Windows PowerShell：

```powershell
py install.py --dev
```

当前自动化测试覆盖搜索规划、结构化检索、质量排序、动态候选池、标题精确解析、HTML 权限识别、引文导出、文本链接、路径隔离和 MCP 公开 schema。真实 CNKI 端到端验证脚本位于 `tests/verify_all_tools.py`；它会实际访问知网并可能下载论文，不应在没有合法权限时运行。

贡献前请阅读 [CONTRIBUTING.md](CONTRIBUTING.md) 和 [SECURITY.md](SECURITY.md)。

## License

本项目采用 [GNU General Public License v3.0 or later](LICENSE)。第三方组件许可见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

使用本软件不代表获得任何 CNKI 内容许可。用户仍需自行遵守 CNKI 服务条款、版权规则和所在机构的数据库使用政策。
