// Tool get_metadata, plus record resolution shared by all tools.

package cnki

import (
	"context"
	"slices"
	"strings"
	"time"
)

// resolve finds a paper by exact full title, locally first, then on CNKI.
func (a *App) resolve(op *Op, title string) (Paper, error) {
	local := a.Store.ByTitle(title)
	if len(local) == 1 {
		return local[0], nil
	}
	if len(local) > 1 {
		return Paper{}, fail("IDENTITY_AMBIGUOUS", "完整题名对应多篇本地文献，请使用 record_ref")
	}
	found, err := a.Site.locate(op, Paper{Title: title})
	if err != nil {
		return Paper{}, recordError(err, Paper{Title: title}, "locate")
	}
	return a.Store.Upsert([]Paper{found})[0], nil
}

// papers resolves refs and titles. Titles unknown locally are located in
// batches of ten per search request; unresolved titles are recorded, not fatal.
func (a *App) papers(op *Op, refs, titles []string) ([]Paper, error) {
	out := a.Store.Papers(uniqueStrings(refs))
	missing := []string{}
	for _, t := range uniqueStrings(titles) {
		switch local := a.Store.ByTitle(t); len(local) {
		case 1:
			out = append(out, local[0])
		case 0:
			missing = append(missing, t)
		default:
			op.problem(&Problem{Code: "IDENTITY_AMBIGUOUS", Message: "完整题名对应多篇本地文献，请使用 record_ref", Details: map[string]any{"title": t}})
		}
	}
	batches := (len(missing) + 9) / 10
	found := make([][]Paper, batches)
	err := parallel(op, batches, 4, "locate", func(sub *Op, i int) error {
		chunk := missing[i*10 : min(len(missing), i*10+10)]
		rows, err := a.Site.locateTitles(sub, chunk)
		if err != nil {
			return recordError(err, Paper{Title: strings.Join(chunk, "；")}, "locate")
		}
		for _, t := range chunk {
			switch m := rows[titleKey(t)]; len(m) {
			case 1:
				found[i] = append(found[i], m[0])
			case 0:
				op.problem(&Problem{Code: "DOCUMENT_NOT_FOUND", Message: "未检索到该完整题名", Details: map[string]any{"title": t}})
			default:
				op.problem(&Problem{Code: "IDENTITY_AMBIGUOUS", Message: "同题名存在多篇文献，请先用检索选择", Details: map[string]any{"title": t}})
			}
		}
		return nil
	})
	for _, f := range found {
		out = append(out, a.Store.Upsert(f)...)
	}
	return out, err
}
func (a *App) searchRefs(id, scope string, offset, limit int) ([]string, error) {
	rec, ok := a.Store.Search(id)
	if !ok {
		return nil, fail("SEARCH_NOT_FOUND", "检索结果不存在")
	}
	if scope == "" {
		scope = "selected"
	}
	refs := []string{}
	for _, e := range rec.Entries {
		if scope == "candidates" || scope == "eligible" && e.Eligible || scope == "selected" && e.Selected {
			refs = append(refs, e.Ref)
		}
	}
	if limit <= 0 {
		limit = 100
	}
	return refs[min(offset, len(refs)):min(offset+limit, len(refs))], nil
}

// project keeps record_ref, title and the requested keys of a full record.
func project(p Paper, fields []string) map[string]any {
	full := fullPaper(p)
	if len(fields) == 0 {
		return full
	}
	out := map[string]any{"record_ref": p.Ref, "title": p.Title}
	for _, f := range fields {
		if v, ok := full[f]; ok {
			out[f] = v
		}
	}
	return out
}

func (a *App) Metadata(ctx context.Context, in MetadataArgs) (any, error) {
	if err := a.init(); err != nil {
		return nil, err
	}
	if len(in.RecordRefs)+len(in.Titles) > 100 || in.Limit > 100 {
		return nil, fail("BATCH_TOO_LARGE", "单次最多 100 篇")
	}
	refs := in.RecordRefs
	if in.SearchID != "" {
		limit := in.Limit
		if limit == 0 {
			limit = 10
		}
		more, err := a.searchRefs(in.SearchID, in.Scope, in.Offset, limit)
		if err != nil {
			return nil, err
		}
		refs = append(refs, more...)
	}
	if len(refs)+len(in.Titles) == 0 {
		return nil, fail("INVALID_INPUT", "提供 record_refs、titles 或 search_id")
	}
	quality := in.Quality || slices.Contains(in.Fields, "quality")
	output := func(papers []Paper) map[string]any {
		records := []map[string]any{}
		for _, p := range papers {
			records = append(records, project(p, in.Fields))
		}
		return map[string]any{"records": records}
	}
	if in.LocalOnly {
		papers := a.Store.Papers(uniqueStrings(refs))
		for _, t := range in.Titles {
			if local := a.Store.ByTitle(t); len(local) == 1 {
				papers = append(papers, local[0])
			}
		}
		return output(papers), nil
	}
	return a.run(ctx, "metadata", Budget{MaxRequests: 600, TimeoutSeconds: 900}, nil, func(op *Op) (any, error) {
		papers, err := a.papers(op, refs, in.Titles)
		if err != nil {
			return output(papers), err
		}
		err = parallel(op, len(papers), envInt("CNKI_WORKERS", 8), "metadata", func(sub *Op, i int) error {
			p := papers[i]
			if in.Refresh {
				p.DetailAt = time.Time{}
			}
			fresh, err := enhance(sub, a.Site, a.Store, p, quality)
			papers[i] = fresh
			return err
		})
		return output(papers), err
	})
}
