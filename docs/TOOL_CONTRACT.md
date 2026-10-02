# CNKI MCP 2.0 工具契约

以运行程序的 `cnki-mcp schema` 输出为准（发布包中的 `docs/tool-schema.json` 由真实 MCP 服务导出）。工具名前可能有客户端命名空间，以最终名称为准。

| 工具 | 约定 |
| --- | --- |
| `search` | 只需 `query`；`concept_groups` 组间 AND、组内 OR；`mode=precise/balanced/broad`（precise 不做反馈检索）；`limit=1..100` 只影响展示 |
| `options.metadata` | `ranked`（默认，按初排动态选详情池）/`all`/`basic`；`basic` 不读摘要，不能当深度研究 |
| `options.freshness` | `prefer_cache`（默认，10 分钟内同范围检索直接复用，0 请求）/`live` |
| `structured_search` | `expression` 为布尔树：叶子 `{field,value,match}`，组合 `{op:and/or/not,items}`；`sort=relevance/date_desc/cited_desc/downloaded_desc`；默认只抓 `limit` 条所需页数 |
| `search_results` | 本地读取 `search_id` 的固定结果；`scope=selected/eligible/candidates`；`view=titles/compact/records` |
| `get_metadata` | 批量 `record_refs`/完整 `titles`/`search_id`；`fields` 只投影需要的字段；`quality=true` 附带期刊收录与影响因子；`local_only=true` 不联网；`refresh=true` 重读详情 |
| `export_citations` | 13 种格式；`format=custom` 配合 `template`；`filename` 写入数据目录 `exports/`，不覆盖 |
| `read_online_html` | `record_ref` 与完整 `title` 二选一；`section`、`offset`、`max_characters` 分页；正文缓存 30 分钟，翻页不再请求 |
| `download_paper` | 仅在用户需要文件时调用；`format=auto/pdf/caj`；点击前登记，结果未知时不再点击，需用户确认后传 `retry=true` |
| `link_references` | 为《完整题名》补链接；`lookup_missing=true` 联网定位，每 10 个题名合并为一次检索 |
| `login` / `session_status` | 用户在弹出的浏览器窗口直接登录，凭据不经过 MCP；`session_status(refresh=true)` 后台重查 |
| `operation_status` | `wait_seconds≤55` 长轮询，任务完成或需要人工时立即返回 |
| `operation_control` | `cancel`、`continue`（继续中断的检索，可加 `additional_requests/additional_seconds`）、`resume`（人工步骤已完成，通常无需调用）、`show`（重新显示人工窗口）、`recover`（重启浏览器） |
| `diagnostics` | 本地检查，不启动浏览器 |

## 任务与人工步骤

- 网络工具默认在调用内等待约 40 秒（`CNKI_INLINE_WAIT_SECONDS`）。未完成时返回 `operation_id` 与进度，用 `operation_status(wait_seconds=55)` 继续等待，不要频繁短轮询。
- `status=awaiting_user`：需要用户在弹出的浏览器窗口完成验证或登录。完成后任务**自动继续**，期间所有请求暂停；`on_verification=skip` 则跳过该资源。
- 浏览器连接中断（`BROWSER_UNAVAILABLE`）是系统问题，不代表论文无权限；用 `operation_control(action=recover)` 后重试。
- 中断或预算用尽的检索可 `continue`；已完成的详情与期刊不会重复请求。进程意外退出后状态显示 `interrupted`，同样可以继续。

## 证据边界

- 所有必要概念在初始通道和反馈通道中都保留；无语义规划时只按显式分隔符切分。
- 相关性与来源质量分别评分；质量只能在相关文献之间排序，不能把不相关的高影响论文推到前面。
- 期刊收录只采用可确认的标签文本；来源身份按现名、译名、曾用刊名核验并给出 `identity_basis`。
- `read_online_html` 报告目录缺口与覆盖范围；没有完整性证据时 `complete_article` 为 null，不据长度推定全文完整。
- 身份 `not_observed` 只表示未观察到；显示机构名称不代表机构订购全部资源。
