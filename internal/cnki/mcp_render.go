// Model-facing output: columnar lists, record views, operation states.

package cnki

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// marshal writes compact JSON without HTML escaping (& < > stay as is).
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

var briefColumns = []string{"record_ref", "title", "authors", "source", "year", "type", "cited", "downloads", "tiers"}
var titleColumns = []string{"record_ref", "title", "authors", "year"}

func intValue(p *int) any {
	if p == nil {
		return ""
	}
	return *p
}

// brief is one list row: the bibliographic facts a reader screens on.
func brief(p Paper) map[string]any {
	year := any("")
	if p.Year > 0 {
		year = p.Year
	}
	tiers := ""
	if p.Quality != nil {
		tiers = strings.Join(p.Quality.Tiers, "; ")
	}
	return map[string]any{"record_ref": p.Ref, "title": p.Title, "authors": strings.Join(p.Authors, "; "), "source": p.Source, "year": year, "type": p.Type, "cited": intValue(p.Cited), "downloads": intValue(p.Downloaded), "tiers": tiers}
}

// columnar turns rows into {fields, rows}; a column empty in every row is dropped.
func columnar(rows []map[string]any, order []string) map[string]any {
	fields := []string{}
	for _, f := range order {
		for _, r := range rows {
			if v, ok := r[f]; ok && v != "" && v != nil {
				fields = append(fields, f)
				break
			}
		}
	}
	out := make([][]any, len(rows))
	for i, r := range rows {
		row := make([]any, len(fields))
		for j, f := range fields {
			if v, ok := r[f]; ok && v != nil {
				row[j] = v
			} else {
				row[j] = ""
			}
		}
		out[i] = row
	}
	return map[string]any{"fields": fields, "rows": out}
}

// compactQuality keeps the journal facts and drops parser bookkeeping.
func compactQuality(q *Quality, source string) map[string]any {
	if q == nil {
		return nil
	}
	out := map[string]any{}
	if len(q.Tiers) > 0 {
		out["tiers"] = q.Tiers
	}
	if q.CompositeImpact != nil {
		out["composite_impact"] = *q.CompositeImpact
	}
	if q.AggregateImpact != nil {
		out["aggregate_impact"] = *q.AggregateImpact
	}
	if q.MetricYear > 0 {
		out["metric_year"] = q.MetricYear
	}
	if q.Canonical != "" && sourceKey(q.Canonical) != sourceKey(source) {
		out["journal_page_title"] = q.Canonical
	}
	if len(out) == 0 {
		out["state"] = "no_indexing_or_metrics_shown"
	}
	return out
}

// fullPaper is a complete record without internal locators and parser state.
func fullPaper(p Paper) map[string]any {
	out := map[string]any{"record_ref": p.Ref, "title": p.Title}
	set := func(k string, v any) {
		switch x := v.(type) {
		case string:
			if x != "" {
				out[k] = x
			}
		case []string:
			if len(x) > 0 {
				out[k] = x
			}
		case int:
			if x != 0 {
				out[k] = x
			}
		case *int:
			if x != nil {
				out[k] = *x
			}
		case map[string]any:
			if len(x) > 0 {
				out[k] = x
			}
		}
	}
	set("url", p.URL)
	set("authors", p.Authors)
	set("institutions", p.Institutions)
	set("source", p.Source)
	set("year", p.Year)
	set("date", p.Date)
	set("volume", p.Volume)
	set("issue", p.Issue)
	set("pages", p.Pages)
	set("type", p.Type)
	set("doi", p.DOI)
	set("abstract", p.Abstract)
	if p.Abstract == "" {
		set("excerpt", p.Excerpt)
	}
	set("keywords", p.Keywords)
	set("funds", p.Funds)
	set("cited", p.Cited)
	set("downloads", p.Downloaded)
	set("quality", compactQuality(p.Quality, p.Source))
	if p.DetailAt.IsZero() {
		out["evidence"] = "search_list"
	} else {
		out["detail_observed"] = p.DetailAt.UTC().Format("2006-01-02")
	}
	return out
}

// listView renders papers as columnar rows (compact, titles) or objects (full).
func listView(papers []Paper, view string, extra func(i int, row map[string]any)) map[string]any {
	if view == "full" {
		rows := make([]map[string]any, len(papers))
		for i, p := range papers {
			rows[i] = fullPaper(p)
			if extra != nil {
				extra(i, rows[i])
			}
		}
		return map[string]any{"records": rows}
	}
	order := briefColumns
	if view == "titles" {
		order = titleColumns
	}
	rows := make([]map[string]any, len(papers))
	for i, p := range papers {
		rows[i] = brief(p)
	}
	return columnar(rows, order)
}

// exprString writes an expression in CNKI's field syntax, e.g. (SU='a' OR SU='b') AND TI='c'.
func exprString(e Expression) string {
	if e.Op == "" {
		code := nativeFields[e.Field]
		if code == "" {
			code = e.Field
		}
		op := "="
		if e.Match == "exact" {
			op = "=="
		}
		return code + op + "'" + e.Value + "'"
	}
	parts := []string{}
	for _, it := range e.Items {
		s := exprString(it)
		if it.Op != "" && len(it.Items) > 1 {
			s = "(" + s + ")"
		}
		parts = append(parts, s)
	}
	if e.Op == "not" {
		return "NOT " + strings.Join(parts, " AND NOT ")
	}
	return strings.Join(parts, " "+strings.ToUpper(e.Op)+" ")
}

func planView(p Plan) map[string]any {
	rows := []map[string]any{}
	for _, c := range p.Channels {
		rows = append(rows, map[string]any{"channel": c.ID, "expression": exprString(c.Expression), "weight": c.Weight})
	}
	out := map[string]any{"method": p.Method, "channels": columnar(rows, []string{"channel", "expression", "weight"})}
	if len(p.Warnings) > 0 {
		out["warnings"] = p.Warnings
	}
	return out
}

func coverageView(cs []Coverage) map[string]any {
	rows := []map[string]any{}
	for _, c := range cs {
		total := any("")
		if c.Total != nil {
			total = *c.Total
		}
		exhausted := any("")
		if c.Exhausted {
			exhausted = true
		}
		rows = append(rows, map[string]any{"channel": c.Channel, "pages": c.Pages, "rows": c.Rows, "site_total": total, "exhausted": exhausted, "error": c.Error})
	}
	return columnar(rows, []string{"channel", "pages", "rows", "site_total", "exhausted", "error"})
}

// compactOp is the model-facing form of an operation: a finished operation is
// just its result; an unfinished one says how to wait for it.
func compactOp(r OpRecord) any {
	switch r.Status {
	case "completed":
		if len(r.Result) > 0 {
			return json.RawMessage(r.Result)
		}
		return map[string]any{"status": "completed"}
	case "running", "awaiting_user":
		out := map[string]any{"status": r.Status, "operation_id": r.ID, "next": "status(operation_id) 等待结果"}
		if r.Message != "" {
			out["message"] = r.Message
		} else if r.Stage != "" {
			out["stage"] = r.Stage
			if d, ok := r.Progress["done"]; ok {
				out["progress"] = fmt.Sprintf("%v/%v", d, r.Progress["total"])
			}
		}
		if r.Status == "awaiting_user" {
			out["next"] = "请用户在弹出的知网浏览器窗口完成验证或登录（完成后任务自动继续）；然后调用 status(operation_id) 等待结果"
		}
		return out
	}
	out := map[string]any{"status": r.Status}
	if r.ID != "" && (r.Status == "interrupted" || r.Status == "partial") {
		out["operation_id"] = r.ID
	}
	if r.Message != "" {
		out["message"] = r.Message
	}
	if len(r.Errors) > 0 {
		errs := []map[string]any{}
		for _, e := range r.Errors {
			m := map[string]any{"code": e.Code, "message": e.Message}
			if d, ok := e.Details.(map[string]any); ok {
				if t, ok := d["title"]; ok {
					m["title"] = t
				}
				if t, ok := d["record_ref"]; ok {
					m["record_ref"] = t
				}
			}
			errs = append(errs, m)
		}
		out["errors"] = errs
	}
	if r.Kind == "search" && resumable(r) {
		out["next"] = "results(search_id, continue=true) 续跑剩余阶段"
	}
	if len(r.Result) > 0 {
		out["result"] = json.RawMessage(r.Result)
	}
	return out
}

// resumable reports a search that stopped before finishing its phases.
func resumable(r OpRecord) bool {
	if r.Status == "interrupted" {
		return true
	}
	for _, e := range r.Errors {
		if oneOf(e.Code, "BUDGET_EXHAUSTED", "RATE_LIMITED", "SITE_UNAVAILABLE", "VERIFICATION_TIMEOUT", "BROWSER_UNAVAILABLE", "LOGIN_REQUIRED", "CANCELLED") {
			return true
		}
	}
	return false
}

func compactError(err error) map[string]any {
	p := asProblem(err)
	e := map[string]any{"code": p.Code, "message": p.Message}
	if p.Retryable {
		e["retryable"] = true
	}
	return map[string]any{"error": e}
}
