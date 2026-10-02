package cnki

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type ReadInput struct {
	Ref            string `json:"record_ref,omitempty"`
	Title          string `json:"title,omitempty" jsonschema:"完整题名（与 record_ref 二选一）"`
	Section        string `json:"section,omitempty" jsonschema:"只返回该章节（取返回的 sections 中的标题）"`
	Offset         int    `json:"offset,omitempty" jsonschema:"码点偏移，用于分页"`
	MaxCharacters  int    `json:"max_characters,omitempty" jsonschema:"本页最多码点数，默认 20000"`
	OnVerification string `json:"on_verification,omitempty" jsonschema:"ask（默认）或 skip"`
}

// Extracted bodies stay in memory for 30 minutes so paging costs no requests.
type docEntry struct {
	Ref, Title string
	Content    ReaderContent
	At         time.Time
}
type docCache struct {
	mu      sync.Mutex
	entries map[string]docEntry
}

func (c *docCache) get(ref string) (docEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[ref]
	return e, ok && time.Since(e.At) < 30*time.Minute
}
func (c *docCache) put(e docEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, old := range c.entries {
		if time.Since(old.At) > 30*time.Minute || len(c.entries) >= 16 {
			delete(c.entries, k)
		}
	}
	c.entries[e.Ref] = e
}

func (a *App) Read(ctx context.Context, in ReadInput) (any, error) {
	if (in.Ref == "") == (in.Title == "") {
		return nil, fail("INVALID_INPUT", "record_ref 与完整 title 二选一")
	}
	if in.Offset < 0 || in.MaxCharacters < 0 || in.MaxCharacters > 200000 || !oneOf(in.OnVerification, "", "ask", "skip") {
		return nil, fail("INVALID_INPUT", "无效分页范围或验证策略")
	}
	if err := a.init(); err != nil {
		return nil, err
	}
	ref := in.Ref
	if ref == "" {
		if local := a.Store.ByTitle(in.Title); len(local) == 1 {
			ref = local[0].Ref
		}
	}
	if e, ok := a.docs.get(ref); ok {
		return sliceDocument(e, in)
	}
	return a.run(ctx, "read", Budget{MaxRequests: 60, TimeoutSeconds: 900, OnVerification: in.OnVerification}, nil, func(op *Op) (any, error) {
		var p Paper
		var err error
		if ref != "" {
			var ok bool
			if p, ok = a.Store.Paper(ref); !ok {
				return nil, fail("DOCUMENT_NOT_FOUND", "记录不存在，可使用完整题名定位")
			}
		} else if p, err = a.resolve(op, in.Title); err != nil {
			return nil, err
		}
		content, err := a.Site.Read(op, p)
		if err != nil {
			return nil, err
		}
		e := docEntry{Ref: p.Ref, Title: p.Title, Content: content, At: time.Now()}
		if !content.Trial && content.Coverage.Scope != "partial" {
			a.docs.put(e) // partial or trial text is never served from cache as the article
		}
		return sliceDocument(e, in)
	})
}

func sliceDocument(e docEntry, in ReadInput) (map[string]any, error) {
	text := e.Content.Text
	start, end := 0, len(text)
	if in.Section != "" {
		var match *Section
		for i, s := range e.Content.Sections {
			if normalize(s.Title) == normalize(in.Section) {
				if match != nil {
					return nil, fail("SECTION_AMBIGUOUS", "章节名称不唯一，请使用返回的章节列表")
				}
				match = &e.Content.Sections[i]
			}
		}
		if match == nil {
			return nil, fail("SECTION_NOT_FOUND", "章节不存在，请使用返回的章节列表")
		}
		start, end = match.Offset, min(len(text), match.Offset+match.Length)
	}
	total := end - start
	limit := in.MaxCharacters
	if limit == 0 {
		limit = 20000
	}
	from := min(end, start+in.Offset)
	to := min(end, from+limit)
	var next any
	if to < end {
		next = in.Offset + to - from
	}
	var complete any
	if e.Content.Coverage.Scope == "trial" || e.Content.Coverage.Scope == "partial" {
		complete = false
	}
	body := string(text[from:to])
	return map[string]any{"status": "completed", "record_ref": e.Ref, "title": e.Title, "text": body, "characters": utf8.RuneCountInString(body), "sections": e.Content.Sections, "section": in.Section, "offset": in.Offset, "next_offset": next, "total_characters": total, "trial": e.Content.Trial, "coverage": e.Content.Coverage, "complete_article": complete,
		"note": strings.Join([]string{"图片、公式和 Canvas 内容可能无法提取", "提取结束不代表整篇文章完整"}, "；")}, nil
}
