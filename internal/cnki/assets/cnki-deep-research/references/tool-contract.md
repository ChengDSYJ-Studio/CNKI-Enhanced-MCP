# CNKI MCP 2.1 工具契约

以运行程序的 `cnki-mcp schema` 输出为准。工具名前可能有客户端命名空间（如 `mcp__cnki__search`），以最终名称为准。

| 工具 | 约定 |
| --- | --- |
| `search` | 三选一：`query`（研究问题，自动规划）、`concept_groups`（组间 AND、组内 OR）、`expression`（知网字段检索：叶子 `{field,value,match}`，组合 `{op:and/or/not,items}`）；`mode=precise/balanced/broad`（precise 不做反馈检索）；`year_from/year_to`；`sort=relevance/date_desc/cited_desc/downloaded_desc`；`limit` 只影响展示（默认 10）；10 分钟内同一检索直接复用，`refresh=true` 重查 |
| `options` | `metadata=ranked`（默认，读靠前候选的详情）/`all`/`basic`（`basic` 不读摘要，不能当深度研究）；`max_pages`、`candidate_target`、`max_requests`、`timeout_seconds`；`on_verification=ask/skip` |
| `results` | 本地分页 `search_id`；`scope=selected/eligible/candidates`；`view=compact/titles/full`（full 含摘要与评分）；`continue=true` 续跑中断或预算用完的检索 |
| `get_metadata` | 批量 `record_refs`/完整 `titles`/`search_id`；`fields` 只投影需要的字段；`quality=true` 附带期刊收录与影响因子；`local_only=true` 不联网；`refresh=true` 重读详情 |
| `read` | `record_ref` 与完整 `title` 二选一；首页给出 `sections` 与 `coverage`；`section` 读单章；`snapshot_id` + `offset` 续读不联网；完整读过的正文直接复用（`reused`），`refresh=true` 重开阅读页 |
| `export` | 13 种格式（默认 gbt7714）；`format=custom` 配合 `template`；`filename` 写入数据目录 `exports/`，不覆盖 |
| `link_references` | 为《完整题名》补链接；`lookup_missing=true` 联网定位，每 10 个题名合并为一次检索 |
| `download` | 仅在用户需要文件时调用；`format=auto/pdf/caj`；点击前登记，结果未知时不再点击，需用户确认后传 `retry=true` |
| `login` | 用户在弹出的浏览器窗口直接登录，凭据不经过 MCP；机构入口用 `remote_access_url` |
| `status` | 无参数：登录与服务状态；`operation_id` 等待结果（最多 50 秒）；`cancel`、`user_done`（机构远程登录完成）、`show`（窗口到前台）、`recover`（重启浏览器）、`check_login`、`details` |

## 任务与人工步骤

- 网络工具默认在调用内等待约 40 秒。未完成时返回 `operation_id`，用 `status(operation_id)` 继续等待，不要频繁短轮询。
- `status=awaiting_user`：需要用户在弹出的浏览器窗口完成验证或登录。完成后任务**自动继续**，期间所有请求暂停；`on_verification=skip` 则跳过该资源。
- 浏览器连接中断（`BROWSER_UNAVAILABLE`）是系统问题，不代表论文无权限；用 `status(recover=true)` 后重试。
- 中断或预算用尽的检索用 `results(search_id, continue=true)` 续跑；已完成的详情与期刊不会重复请求。进程意外退出后状态显示 `interrupted`，同样可以续跑。

## 证据规则

- 所有必要概念在初始通道和反馈通道中都保留；无语义规划时只按显式分隔符切分。
- 相关性与来源质量分别评分；质量只能在相关文献之间排序，不能把不相关的高影响论文推到前面。
- 期刊收录只采用可确认的标签文本；未观察到收录不等于未收录。
- `read` 报告目录缺口与覆盖范围；`trial=true` 或 `complete_article=false` 不是完整全文，不据长度推定全文完整。
- 登录状态 `not_observed` 只表示未观察到；显示机构名称不代表机构订购全部资源。
