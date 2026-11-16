# CNKI MCP

MCP server for 中国知网 (CNKI): Chinese journal articles, theses and conference papers. Site requests run inside a dedicated Chrome, which also holds the login.

## Rules

- The version is `2.1.0`; change it only when the user asks.
- Everything this service writes stays in its data root (default `<binary dir>/data`): `cnki.sqlite`, snapshots, downloads, exports, the browser profile and Chrome's cache, temp and crash files, download transfers. Remove temporary folders when done; no client-config backups.
- The service owns only its own data root, browser profile, processes, ports and client entry `cnki`. Never read or change the data, logins or configuration of other services (PKULaw, Westlaw, HeinOnline, SSRN, Lexis).
- Login: the user signs in only in the dedicated browser; passwords never pass through MCP. The login is kept by Chrome in `data/browser/profile`. Never write login material anywhere else, never return it, never save raw authenticated pages.
- The browser window is brought forward only for `login` and when CNKI itself asks for verification or login during a request; the user completes it, nothing solves challenges automatically. Otherwise the browser stays minimized and works in background tabs.
- Keep the request pacing defaults (`CNKI_REQUEST_INTERVAL_MS`, `CNKI_MAX_INFLIGHT`, `CNKI_WORKERS`) unless the user asks to change them.
- Keep schemas and default output compact without dropping observed fields.

## Code

- One Go package, `internal/cnki`, with files prefixed by layer: `mcp_` (protocol, arguments, rendering), `tool_` (one file per tool), `site_` (site access), `store` (SQLite), `install_` (setup and client entries), `platform_` (OS specifics). See `docs/ARCHITECTURE.md`.
- A new tool: its arguments in `mcp_args.go`, its handler in a `tool_*.go` file that takes those arguments directly, its registration in `mcp_server.go`, and the change in `internal/cnki/assets/SKILL.md` (and in `assets/cnki-deep-research/` when the research workflow changes).
- Tests sit beside the file they test (`x.go` → `x_test.go`).

## Checks and releases

- `scripts/check.sh` runs the race tests, vet, the cross-builds for the other platforms, the protocol self-check, regenerates `docs/tool-schema.json` and runs the multi-client session check. It must pass before a release.
- After a tool change, regenerate `docs/tool-schema.json` (`cnki-mcp schema`, or `scripts/check.sh`).
- Packages are built by `scripts/release.py` into `dist/` only. `releases/2.0.0-original/` holds the original packages; never modify it.
- The changelog has one section per version describing what that version contains, in Chinese: no dates, no "Unreleased", no history of fixes.
- `docs/VALIDATION.md` records observed results by version, without dates.

## Evidence

- List rows and abstracts are not full text; trial or partial readings are not the article; journal tiers are what the journal page showed. Record bounded coverage.
