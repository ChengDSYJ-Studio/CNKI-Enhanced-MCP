---
name: cnki-deep-research
description: Conduct evidence-grounded deep research with CNKI MCP (2.0 or compatible) and produce a structured literature review or research report. Use when a user asks for CNKI/知网深度研究、调研报告、文献综述、研究现状、研究进展、学术争议、主题脉络、证据梳理, or an English-language literature investigation that should rely on Chinese academic sources. Trigger for both explicit `$cnki-deep-research` use and natural requests such as “帮我深度研究这个主题”.
---

# CNKI Deep Research

Use CNKI MCP (2.0 or compatible) as the literature retrieval and evidence layer. Produce a reasoned report, not a search-result dump.

## Keep the workflow portable

- Depend only on MCP tool capabilities, not local paths, operating-system commands, browser executables, or a specific agent vendor.
- Resolve namespaced variants by terminal tool name. For example, treat `mcp__cnki__search` as `search`; inspect the actual tools/list schema before calling a legacy server.
- Require CNKI MCP (2.0 or compatible) or compatible tools. If they are unavailable, state the missing dependency and stop; do not pretend to have searched CNKI.
- Use exact paper titles or public `record_ref` values returned by the tool. For same-title records, disambiguate by author and source, then use `record_ref`. Never invent internal database IDs.
- Keep account credentials in the visible login browser. Never ask the user to paste a password, cookie, token, or institutional credential into chat or tool parameters.

Read [tool-contract.md](references/tool-contract.md) before calling CNKI tools. Read [evidence-and-report.md](references/evidence-and-report.md) before building the evidence matrix or drafting the report.

## Establish the research frame

Infer a reasonable scope from the request. Ask one concise question only when an unresolved choice would materially change the report, such as jurisdiction, discipline, date range, or desired depth. Otherwise state assumptions and proceed.

Define internally:

- the central question and 3–6 subquestions;
- scope, exclusions, time range, and relevant disciplines;
- default research budget: 12–25 serious candidate papers, 4–8 full-text reads when authorized, and at least one search for conflicting evidence;
- stopping rule: stop after two useful search rounds add no material theme, mechanism, position, or source class.

Treat these as adaptive targets, not quotas. A narrow topic may support fewer sources; a broad or contested topic may require more.

## Run the research workflow

### 1. Check access

Call `session_status`. If login, institutional access, or human verification is required, call `login` (with `remote_access_url` for an institutional entry) and let the user complete the visible browser action. Continue only after the session is usable.

### 2. Map the field with review literature

Search the original question plus bounded review formulations such as `综述`, `述评`, `研究进展`, `研究现状`, or `文献回顾`. Use `search` first. Use `structured_search` only when field, author, source, DOI, date, or reproducible Boolean constraints are genuinely needed.

Select review candidates by relevance, source evidence, coverage, recency, and viewpoint diversity. Do not choose solely by title similarity or citation count.

### 3. Inspect metadata before reading

Call `get_metadata` in batches using exact titles or a returned `search_id`. Record authors, year, source, document type, source tier, abstract, keywords, DOI, field observations, ranking evidence, and separate same-title records.

Use metadata to identify:

- major concepts and terminology;
- classic and recent works;
- recurring authors, institutions, methods, and datasets;
- competing theories or conclusions;
- missing populations, periods, regions, mechanisms, or methods.

### 4. Read evidence economically

Use `read_online_html` only for papers that can change the report. Prefer a relevant `section`, with `offset` and a bounded `max_characters`; pages after the first are served from a local cache. Read the abstract, introduction/problem statement, methods when relevant, results, conclusion, limitations, and cited passages needed for claims.

Read the whole body only when it is essential and reasonably sized. Do not download or parse PDF/CAJ unless the user separately requests a download. Do not cache or reproduce large portions of copyrighted text.

### 5. Run targeted follow-up searches

Turn the field map into focused searches for:

- foundational or highly influential work;
- recent primary research;
- methods, mechanisms, populations, or jurisdictions;
- contrary findings, criticism, limitations, and failed approaches;
- terminology discovered in trusted review or primary literature.

Use bounded query expansion. Keep the original concept as an anchor, and reject expansions that produce mostly unrelated results.

### 6. Test saturation and balance

After each round, ask whether new sources add a material theme, mechanism, disagreement, method, or evidence class. Require at least one deliberate contrary-evidence search. Stop at saturation, at the agreed budget, or when CNKI access prevents further verification. Report which stopping condition applied.

### 7. Build the evidence matrix

Assign every used paper an evidence level:

- **A — verified full text:** authorized online body was read and relevant passages or sections were checked;
- **B — abstract and metadata:** claims are limited to what the abstract and bibliographic metadata support;
- **C — discovery only:** title or incomplete record only; use to describe search leads, never to support a substantive conclusion.

For every important claim, retain the exact paper title, CNKI detail URL, evidence level, supporting section or abstract basis, and limitations. Separate source statements from the agent's synthesis.

### 8. Write the report

Use the structure in [evidence-and-report.md](references/evidence-and-report.md). Link citations through the complete paper title to the CNKI detail page. Distinguish consensus, majority tendency, minority position, and the agent's inference.

Do not claim the review is systematic unless the search strategy, databases, inclusion rules, exclusion rules, and screening process actually meet that standard. Call ordinary work a literature review, scoping review, or research report as appropriate.

## Handle failures honestly

- If a browser error interrupts work, call operation_control recover; do not count remaining records as access failures. When a tool returns `awaiting_user`, tell the user to complete the verification in the browser window; the task resumes automatically. If the user prefers to skip, cancel the operation or retry with `on_verification="skip"`. Never ask to re-download after an uncertain download unless the user confirms (`retry=true`).
- Long operations return an `operation_id`; wait with `operation_status(wait_seconds=55)` instead of polling rapidly.
- If full-text permission is unavailable, downgrade the source to B or C and narrow the claims.
- If results are sparse, report the searches attempted and avoid filling the report with low-quality papers.
- If evidence conflicts, explain the disagreement and possible reasons; do not average incompatible conclusions into false consensus.
- If a title resolves to multiple records, use author, year, source, DOI, and returned public record references to disambiguate.

## Finish with transparent deliverables

Return:

1. the structured research report;
2. a compact evidence table;
3. linked references;
4. a search-method appendix listing query families, years, inclusion logic, evidence limits, and stopping condition.

Use `export_citations` only when the user requests a citation file or a specific citation style. Use `link_references` when the user supplies an existing draft that needs CNKI links.
