// Tool read: the HTML reader's text, saved as a snapshot and paged locally.

package cnki

import (
	"context"
	"regexp"
	"time"
)

var snapshotName = regexp.MustCompile(`^cn_[a-f0-9]{16}$`)

func (a *App) Read(ctx context.Context, in ReadArgs) (any, error) {
	if in.Offset < 0 || in.MaxChars < 0 || in.MaxChars > 30000 || !oneOf(in.OnVerification, "", "ask", "skip") {
		return nil, fail("INVALID_INPUT", "offset、max_characters（≤30000）或 on_verification 无效")
	}
	if err := a.init(); err != nil {
		return nil, err
	}
	if in.SnapshotID != "" {
		if !snapshotName.MatchString(in.SnapshotID) {
			return nil, fail("INVALID_SNAPSHOT", "snapshot_id 格式无效")
		}
		snap, ok := a.Store.Snapshot(in.SnapshotID)
		if !ok {
			return nil, fail("SNAPSHOT_NOT_FOUND", "快照不存在或已过期（保留 60 天）")
		}
		return sliceSnapshot(snap, in, true)
	}
	if (in.RecordRef == "") == (in.Title == "") {
		return nil, fail("INVALID_INPUT", "record_ref、title 或 snapshot_id 三选一")
	}
	ref := in.RecordRef
	if ref == "" {
		if local := a.Store.ByTitle(in.Title); len(local) == 1 {
			ref = local[0].Ref
		}
	}
	// A complete earlier reading is reused: the reader page is where CNKI
	// most often asks for verification, so it is opened only when needed.
	if ref != "" && !in.Refresh {
		if snap, ok := a.Store.LatestSnapshot(ref); ok && !snap.Trial && snap.Coverage.Scope != "partial" {
			return sliceSnapshot(snap, in, true)
		}
	}
	return a.run(ctx, "read", Budget{MaxRequests: 60, TimeoutSeconds: 900, OnVerification: in.OnVerification}, nil, func(op *Op) (any, error) {
		var p Paper
		var err error
		if ref != "" {
			var ok bool
			if p, ok = a.Store.Paper(ref); !ok {
				return nil, fail("DOCUMENT_NOT_FOUND", "记录不存在；可改用完整题名")
			}
		} else if p, err = a.resolve(op, in.Title); err != nil {
			return nil, err
		}
		content, err := a.Site.Read(op, p)
		if err != nil {
			return nil, err
		}
		snap := Snapshot{ID: newID("cn_"), Ref: p.Ref, Title: p.Title, Text: string(content.Text), Sections: content.Sections, Coverage: content.Coverage, Trial: content.Trial, Observed: time.Now().UTC()}
		a.Store.PutSnapshot(snap)
		return sliceSnapshot(snap, in, false)
	})
}

// sliceSnapshot returns one page of a snapshot. Offsets count characters
// (code points). Sections and coverage are reported on the first page only.
func sliceSnapshot(snap Snapshot, in ReadArgs, reused bool) (map[string]any, error) {
	text := []rune(snap.Text)
	start, end := 0, len(text)
	if in.Section != "" {
		var match *Section
		for i, s := range snap.Sections {
			if normalize(s.Title) == normalize(in.Section) {
				if match != nil {
					return nil, fail("SECTION_AMBIGUOUS", "章节名称不唯一，请使用 sections 中的标题")
				}
				match = &snap.Sections[i]
			}
		}
		if match == nil {
			return nil, fail("SECTION_NOT_FOUND", "章节不存在，请使用 sections 中的标题")
		}
		start, end = match.Offset, min(len(text), match.Offset+match.Length)
	}
	limit := in.MaxChars
	if limit == 0 {
		limit = 8000
	}
	from := min(end, start+in.Offset)
	to := min(end, from+limit)
	body := string(text[from:to])
	out := map[string]any{"record_ref": snap.Ref, "snapshot_id": snap.ID, "text": body, "offset": in.Offset, "total_characters": end - start}
	if to < end {
		out["next_offset"] = in.Offset + to - from
	}
	if in.Section != "" {
		out["section"] = in.Section
	}
	if in.Offset == 0 {
		out["title"] = snap.Title
		if in.Section == "" && len(snap.Sections) > 0 {
			sections := []map[string]any{}
			for _, s := range snap.Sections {
				sections = append(sections, map[string]any{"level": s.Level, "title": s.Title, "offset": s.Offset, "length": s.Length})
			}
			out["sections"] = columnar(sections, []string{"level", "title", "offset", "length"})
		}
		cov := map[string]any{"scope": snap.Coverage.Scope}
		if len(snap.Coverage.MissingHeadings) > 0 {
			cov["missing_headings"] = snap.Coverage.MissingHeadings
		}
		if snap.Coverage.NonTextElements > 0 {
			cov["non_text_elements"] = snap.Coverage.NonTextElements
		}
		out["coverage"] = cov
		out["note"] = "图片、公式和 Canvas 内容可能无法提取；提取结束不代表整篇完整"
	}
	if snap.Trial {
		out["trial"] = true
	}
	if snap.Trial || snap.Coverage.Scope == "partial" {
		out["complete_article"] = false
	}
	if reused {
		out["reused"] = true
		out["observed"] = snap.Observed.Format("2006-01-02")
	}
	return out, nil
}
