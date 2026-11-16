# Tool contract · 2.1

The generated `tool-schema.json` is the authoritative JSON input schema. There are nine tools with short names; clients tell them apart by the server name `cnki` (Claude Code and Codex show `mcp__cnki__search`). The bundled skills (`internal/cnki/assets/SKILL.md`, resource `cnki://skill`, and `assets/cnki-deep-research/`) are the model-facing usage guides. The command-line `call` also accepts the 2.0 tool names.

| 2.0 | 2.1 |
| --- | --- |
| `search` | `search(query=…)` or `search(concept_groups=…)` |
| `structured_search` | `search(expression=…)` |
| `search_results` | `results` |
| `operation_control(continue)` | `results(search_id, continue=true)` |
| `get_metadata` | `get_metadata` |
| `read_online_html` | `read` (plus `snapshot_id`) |
| `download_paper` | `download` |
| `export_citations` | `export` |
| `link_references` | `link_references` |
| `login` | `login` |
| `session_status` | `status()`; `status(check_login=true)` checks again |
| `operation_status` | `status(operation_id=…)` |
| `operation_control(cancel/resume/show/recover)` | `status(cancel / user_done / show / recover=true)` |
| `diagnostics` | `status(details=true)` or `cnki-mcp doctor` |

## Output conventions

- A finished call returns its result object directly. An unfinished one returns `{"status":"running"|"awaiting_user","operation_id","next"}` (plus `stage` and `progress`, or the `message` for the user); `status(operation_id)` waits up to 50 s (max 55) and returns the result itself when done. A partial operation returns `{"status":"partial","operation_id","errors","result"}`, with `next` when a search can be continued.
- Errors are `{"error":{"code","message","retryable?"}}` with `isError=true`.
- Results are sent once, as JSON text, without HTML escaping. `CNKI_MCP_OUTPUT=structured` (structured content only) or `dual` (both) changes that for clients that want it.
- Lists are columnar: `{"fields":[…],"rows":[[…],…]}`; missing cells are `""` and columns empty in every row are omitted. Default list rows (`view="compact"`) carry `record_ref`, `title`, `authors` (joined with "; "), `source`, `year`, `type`, `cited`, `downloads` and `tiers` (journal indexing observed). `view="titles"` gives ref, title, authors and year; `view="full"` gives record objects (as `get_metadata`) and, in `results`, the ranking `score` and `eligible`.
- IDs are short: records `cr_`+16 hex (2.0 records `rec_…` remain valid), searches/operations `cs_`+16 hex, snapshots `cn_`+16 hex.

## Login and status

`login` opens CNKI's advanced-search page in the dedicated browser; when a personal or institutional login is already observed it returns at once (`via:"already_logged_in"`). Otherwise, or with `force=true`, the window comes to the front (`awaiting_user`) and the user signs in there; the login is detected in any CNKI tab and confirmed on a fresh search page (`via:"browser_login"`). `remote_access_url` opens an institutional remote-access entry (HTTPS, no credentials in the URL); after such a login call `status(operation_id, user_done=true)`. The result reports `personal` (`logged_in` / `not_observed`) and `institution` (the names shown, or `not_observed`).

`status` without arguments reports `login` (the last observation, or `not_checked`), `data_dir` and any active operation of this client; `details=true` adds diagnostics (request interval, in-flight limit, workers, store counts, browser state, requests sent, verifications seen). `check_login=true` checks again on a background page. `cancel`, `user_done` and `show` act on the given (or the active) operation of this client; `recover=true` drops the browser connection so the next request relaunches Chrome.

## Search

`search` takes one of `query` (with optional `concept_groups`), `concept_groups` alone, or `expression`.

- `query`: a research question or keywords (1–1000 characters), planned into concept groups — by the client's own model through MCP sampling when the client supports it, else lexically (explicit separators only). `concept_groups`: AND of synonym OR-groups (≤8 groups of ≤6 terms). Channels are subject, title and keywords; `mode="balanced"` (default) and `"broad"` add a keyword feedback round, `"precise"` does not.
- `expression`: CNKI field search, leaf `{field, value, match}` (`match` `phrase` default or `exact`) or `{op: and|or|not, items}`. Fields: subject, title, keywords, abstract, authors, first_author, corresponding_author, institutions, source, doi, funds, fulltext, references, classification, subtitle, title_keywords_abstract. It runs as one channel, reads no detail pages by default (`options.metadata="basic"`) and collects 50 candidates.
- `year_from`/`year_to`; `sort` relevance (default), date_desc, cited_desc, downloaded_desc; `limit` rows shown (default 10, max 50); `view`; `refresh=true` ignores the 10-minute cache.
- `options`: `metadata` (`ranked` default: the detail pages of the best candidates; `all`; `basic`: none), `candidate_target` (default 300), `max_pages` per channel (default 5), `max_requests` (default 600), `timeout_seconds` (default 900), `on_verification` (`ask` default, `skip`).

Output: `search_id`, the first selected rows, `candidates`, `eligible`, `selected`, `details_read`, `journals_with_quality`, `not_selected` (reasons with counts), `plan` (`method` and per channel its CNKI expression and weight), `coverage` (per channel `pages`, `rows`, `site_total`, `exhausted`, `error`), `next_offset`, and `cached:true` when served from cache. Coverage is bounded; it is never claimed exhaustive.

`results(search_id, scope, offset, limit, view)` pages the stored entries without site access: `scope` selected (default, ≤100), eligible, candidates. `continue=true` resumes a partial or interrupted search from its checkpoint with `additional_requests` (default 200) more budget; finished phases cost nothing again.

## Records and text

`get_metadata` resolves `record_refs`, exact `titles` (up to 10 located with one search; same-title papers are reported as `IDENTITY_AMBIGUOUS`) and/or rows of a `search_id` (1–100) and reads their detail pages (30-day cache; `refresh=true` reads again). A record has `url`, `authors`, `institutions`, `source`, `year`, `date`, `volume`, `issue`, `pages`, `type`, `doi`, `abstract` (or the list `excerpt`), `keywords`, `funds`, `cited`, `downloads`, `quality` (with `quality=true`: `tiers`, `composite_impact`, `aggregate_impact`, `metric_year`, and `journal_page_title` when it differs from the source), and `detail_observed` (date) or `evidence:"search_list"`. `fields` projects; `local_only=true` forbids site access. Per-record failures are listed in the operation's `errors`.

`read` opens the paper's HTML reader (`record_ref` or exact `title`) and returns 8,000 characters (`max_characters` ≤ 30,000) from `offset` (code points). The first page adds `title`, `sections` (columnar `level`, `title`, `offset`, `length`), `coverage` (`scope`, `missing_headings`, `non_text_elements`) and a note on non-text content; `section` reads one section. Output: `text`, `offset`, `next_offset`, `total_characters`, `snapshot_id`, `trial:true` when CNKI served only a preview, `complete_article:false` for a trial or a partial catalog. The reading is saved as a snapshot (60 days): `read(snapshot_id, offset)` continues without network, and a later `read` of the same paper reuses a complete snapshot (`reused:true`, `observed`) unless `refresh=true`.

## Files

`export`: `gbt7714` (default), `apa`, `mla`, `chicago`, `vancouver`, `bibtex`, `ris`, `endnote`, `csl_json`, `json`, `csv`, `markdown`, `custom` (`template` with `{authors}`, `{title}`, `{journal}`, `{year}`…). Stored records; exact `titles` not stored are located first. Output: `format`, `count`, `text` (or `path` with `filename`, saved under `data/exports/`, never overwritten), `missing_fields`.

`link_references(text, lookup_missing)`: each 《full title》 (nesting allowed; code spans and existing Markdown links are skipped) is linked to its CNKI page when exactly one stored record matches. Output: `text` and `references` with `status` linked, matched_without_link, ambiguous or unresolved.

`download`: the site's PDF (`format="auto"` prefers PDF) or CAJ, clicked in the dedicated browser; the transfer lands in `data/incoming/` and, after its signature (and the PDF end marker) is checked, is moved to `data/downloads/` (`subdirectory`, `filename`; never overwritten) with its SHA-256. The click is recorded first: when the outcome is unknown the next call reports `SIDE_EFFECT_UNCERTAIN` instead of clicking again, unless `retry=true`; a transfer that finishes late is picked up then. A verified earlier file is reused (`reused:true`). Output: `state`, `record_ref`, `title`, `format`, `path`, `bytes`, `sha256`.

## Runtime limits

Requests start at least `CNKI_REQUEST_INTERVAL_MS` (500) apart with at most `CNKI_MAX_INFLIGHT` (6) in flight; detail reads use `CNKI_WORKERS` (8) workers; rate limits and human steps widen the spacing. Network tools wait `CNKI_INLINE_WAIT_SECONDS` (40) inline. Stdio connections share one backend per data root; closing a connection cancels only its own operations; the backend exits after the last client.
