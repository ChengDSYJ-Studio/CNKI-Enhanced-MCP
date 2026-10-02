package cnki

import (
	"strings"
	"unicode/utf8"
)

type titleSpan struct {
	Start, End int
	Title      string
}

// referenceSpans finds 《》 titles (nesting allowed), skipping code and existing Markdown links.
func referenceSpans(text string) []titleSpan {
	out := []titleSpan{}
	for i := 0; i < len(text); {
		if text[i] == '`' {
			n := 1
			for i+n < len(text) && text[i+n] == '`' {
				n++
			}
			if end := strings.Index(text[i+n:], strings.Repeat("`", n)); end >= 0 {
				i += n + end + n
			} else {
				i += n
			}
			continue
		}
		if text[i] == '[' {
			if close := strings.Index(text[i:], "]("); close >= 0 {
				if end := strings.Index(text[i+close+2:], ")"); end >= 0 {
					i += close + 2 + end + 1
					continue
				}
			}
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if r != '《' {
			i += size
			continue
		}
		start, inside, depth := i, i+size, 1
		i += size
		for i < len(text) && depth > 0 {
			r, size = utf8.DecodeRuneInString(text[i:])
			if r == '《' {
				depth++
			}
			if r == '》' {
				if depth--; depth == 0 {
					out = append(out, titleSpan{Start: start, End: i + size, Title: text[inside:i]})
				}
			}
			i += size
		}
	}
	return out
}
func spanTitles(spans []titleSpan) []string {
	out := make([]string, len(spans))
	for i, s := range spans {
		out[i] = s.Title
	}
	return out
}
func renderLinks(store *Store, text string, spans []titleSpan) map[string]any {
	var b strings.Builder
	references := []map[string]any{}
	offset := 0
	for _, span := range spans {
		papers := store.ByTitle(span.Title)
		raw := text[span.Start:span.End]
		status := "unresolved"
		refs := []string{}
		for _, p := range papers {
			refs = append(refs, p.Ref)
		}
		if len(papers) > 1 {
			status = "ambiguous"
		} else if len(papers) == 1 {
			status = "matched_without_link"
			if papers[0].URL != "" {
				status = "linked"
				raw = "[" + markdownText(raw) + "](" + strings.NewReplacer("(", "%28", ")", "%29").Replace(papers[0].URL) + ")"
			}
		}
		b.WriteString(text[offset:span.Start])
		b.WriteString(raw)
		offset = span.End
		references = append(references, map[string]any{"title": span.Title, "status": status, "record_refs": refs})
	}
	b.WriteString(text[offset:])
	return map[string]any{"status": "completed", "text": b.String(), "references": references}
}
