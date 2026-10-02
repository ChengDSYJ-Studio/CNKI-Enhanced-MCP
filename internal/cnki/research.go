package cnki

import (
	"context"
	"sync"
	"time"
)

// Backend is the three kinds of evidence a search costs requests for.
type Backend interface {
	Grid(*Op, Channel, int, SearchInput) (Grid, error)
	Detail(*Op, Paper) (Paper, error)
	Journal(*Op, Paper) (*Quality, error)
}
type Coverage struct {
	Channel   string `json:"channel"`
	Pages     int    `json:"pages"`
	Rows      int    `json:"rows"`
	Total     *int   `json:"total"`
	Exhausted bool   `json:"exhausted"`
	Failed    bool   `json:"failed,omitempty"`
	Error     string `json:"error,omitempty"`
}

// searchState is the resumable checkpoint. Every phase is idempotent: papers
// whose detail or journal is already known cost no request when repeated.
type searchState struct {
	SearchID     string           `json:"search_id"`
	CacheKey     string           `json:"cache_key,omitempty"`
	Input        SearchInput      `json:"input"`
	Plan         Plan             `json:"plan"`
	Refs         []string         `json:"refs"`
	Hits         map[string][]Hit `json:"hits"`
	Coverage     []Coverage       `json:"coverage"`
	Retrieved    bool             `json:"retrieved"`
	Pool         []string         `json:"pool,omitempty"`
	Enriched     bool             `json:"enriched"`
	Feedback     []Channel        `json:"feedback,omitempty"`
	FeedbackDone bool             `json:"feedback_done"`
	FeedbackRefs []string         `json:"feedback_refs,omitempty"`
	FeedbackPool []string         `json:"feedback_pool,omitempty"`
	Finished     bool             `json:"finished"`
}
type SearchSummary struct {
	SearchID       string         `json:"search_id"`
	Status         string         `json:"status"`
	Candidates     int            `json:"candidate_count"`
	Eligible       int            `json:"eligible_count"`
	Selected       int            `json:"selected_count"`
	Enhanced       int            `json:"enhanced_count"`
	QualitySources int            `json:"quality_source_count"`
	Exclusions     map[string]int `json:"selection_exclusions,omitempty"`
	Coverage       []Coverage     `json:"coverage"`
	Plan           Plan           `json:"plan"`
	Results        []TitleResult  `json:"results"`
}
type TitleResult struct {
	Ref   string `json:"record_ref"`
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
}

type Researcher struct {
	Store   *Store
	Site    Backend
	Workers int
}

func (r *Researcher) Run(op *Op, st *searchState) (SearchSummary, error) {
	if st.Hits == nil {
		st.Hits = map[string][]Hit{}
	}
	if len(st.Coverage) == 0 {
		for _, ch := range st.Plan.Channels {
			st.Coverage = append(st.Coverage, Coverage{Channel: ch.ID})
		}
	}
	err := r.phases(op, st)
	op.checkpoint(st)
	status := "completed"
	if err != nil {
		status = "partial"
	}
	return r.finish(op, st, status), err
}
func (r *Researcher) phases(op *Op, st *searchState) error {
	in := st.Input
	if !st.Retrieved {
		if err := r.retrieve(op, st); err != nil {
			return err
		}
		st.Retrieved = true
		st.Pool = r.pool(st, st.Refs)
		op.checkpoint(st)
	}
	if !st.Enriched {
		if err := r.enrich(op, st.Pool, "enrichment"); err != nil {
			return err
		}
		st.Enriched = true
		op.checkpoint(st)
	}
	if !st.FeedbackDone {
		if in.Mode != "precise" && in.Options.Metadata != "basic" && st.Plan.Method != "structured" {
			if st.Feedback == nil {
				st.Feedback = Feedback(Rank(r.Store.Papers(st.Refs), st.Plan, st.Hits, 0), st.Plan)
				for _, ch := range st.Feedback {
					st.Coverage = append(st.Coverage, Coverage{Channel: ch.ID})
				}
			}
			op.stage("feedback", map[string]any{"channels": len(st.Feedback)})
			base := len(st.Plan.Channels)
			err := r.round(op, st, st.Feedback, base, func(i int) bool { return st.Coverage[base+i].Pages == 0 && !st.Coverage[base+i].Failed }, true)
			if err != nil {
				return err
			}
		}
		st.FeedbackDone = true
		st.FeedbackPool = r.pool(st, st.FeedbackRefs)
		op.checkpoint(st)
	}
	if !st.Finished {
		if err := r.enrich(op, st.FeedbackPool, "feedback_enrichment"); err != nil {
			return err
		}
		st.Finished = true
	}
	return nil
}

// retrieve fetches complete rounds: every channel's next page in parallel,
// stopping after a full round once the candidate target is reached.
func (r *Researcher) retrieve(op *Op, st *searchState) error {
	o := st.Input.Options
	n := len(st.Plan.Channels)
	for {
		lo, hi := 1<<30, 0
		for i := range n {
			if c := st.Coverage[i]; !c.Exhausted && !c.Failed {
				lo, hi = min(lo, c.Pages), max(hi, c.Pages)
			}
		}
		if hi == 0 && lo == 1<<30 || lo >= o.MaxPages || lo > 0 && lo == hi && len(st.Refs) >= o.CandidateTarget {
			return nil
		}
		op.stage("retrieval", map[string]any{"round": lo + 1, "candidates": len(st.Refs)})
		err := r.round(op, st, st.Plan.Channels, 0, func(i int) bool {
			c := st.Coverage[i]
			return !c.Exhausted && !c.Failed && c.Pages == lo
		}, false)
		op.checkpoint(st)
		if err != nil {
			return err
		}
	}
}

// round fetches the next page of each selected channel concurrently and merges
// results in channel order, so positions and candidate order are deterministic.
func (r *Researcher) round(op *Op, st *searchState, channels []Channel, base int, want func(int) bool, feedback bool) error {
	type result struct {
		grid Grid
		err  error
	}
	results := make([]result, len(channels))
	var wg sync.WaitGroup
	for i, ch := range channels {
		if !want(i) {
			results[i].err = errSkip
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].grid, results[i].err = r.Site.Grid(op, ch, st.Coverage[base+i].Pages+1, st.Input)
		}()
	}
	wg.Wait()
	var stop error
	for i, res := range results {
		c := &st.Coverage[base+i]
		switch {
		case res.err == errSkip:
		case res.err == nil:
			r.collect(st, channels[i], c, res.grid, feedback)
		case fatal(res.err):
			if stop == nil {
				stop = res.err
			}
		default:
			c.Failed, c.Error = true, asProblem(res.err).Message
			op.problem(res.err)
		}
	}
	return stop
}

var errSkip = &Problem{Code: "SKIP"}

func (r *Researcher) collect(st *searchState, ch Channel, c *Coverage, grid Grid, feedback bool) {
	in := st.Input
	valid, positions := []Paper{}, []int{}
	for i, p := range grid.Papers {
		if (in.YearFrom > 0 || in.YearTo > 0) && (p.Year == 0 || in.YearFrom > 0 && p.Year < in.YearFrom || in.YearTo > 0 && p.Year > in.YearTo) {
			continue
		}
		valid = append(valid, p)
		positions = append(positions, i)
	}
	seen := make(map[string]bool, len(st.Refs))
	for _, ref := range st.Refs {
		seen[ref] = true
	}
	for i, p := range r.Store.Upsert(valid) {
		if !seen[p.Ref] {
			seen[p.Ref] = true
			st.Refs = append(st.Refs, p.Ref)
			if feedback {
				st.FeedbackRefs = append(st.FeedbackRefs, p.Ref)
			}
		}
		found := false
		for _, h := range st.Hits[p.Ref] {
			found = found || h.Channel == ch.ID
		}
		if !found {
			st.Hits[p.Ref] = append(st.Hits[p.Ref], Hit{Channel: ch.ID, Position: c.Rows + positions[i] + 1, Weight: ch.Weight})
		}
	}
	c.Pages++
	c.Rows += len(grid.Papers)
	c.Total, c.Exhausted = grid.Total, grid.Exhausted
}

// pool picks which candidates deserve a detail request, by preliminary rank.
func (r *Researcher) pool(st *searchState, refs []string) []string {
	switch st.Input.Options.Metadata {
	case "basic":
		return nil
	case "all":
		return append([]string(nil), refs...)
	}
	ranked := Rank(r.Store.Papers(refs), st.Plan, st.Hits, 0)
	out := []string{}
	for _, p := range ranked[:EnhancementSize(len(ranked))] {
		out = append(out, p.Paper.Ref)
	}
	return out
}

// enrich runs detail and journal requests for refs concurrently.
func (r *Researcher) enrich(op *Op, refs []string, stage string) error {
	return parallel(op, len(refs), r.Workers, stage, func(sub *Op, i int) error {
		p, ok := r.Store.Paper(refs[i])
		if !ok {
			return nil
		}
		_, err := enhance(sub, r.Site, r.Store, p, true)
		return err
	})
}

// parallel runs fn for 0..n-1 on a worker pool. The limiter, not the pool size,
// sets the request rate. A fatal error cancels the remaining items; any other
// error is recorded against the operation and the batch continues.
func parallel(op *Op, n, workers int, stage string, fn func(sub *Op, i int) error) error {
	if n == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(op.ctx)
	defer cancel()
	sub := op.With(ctx)
	var mu sync.Mutex
	var stop error
	done := 0
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(n, max(1, workers)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				err := fn(sub, i)
				mu.Lock()
				done++
				if fatal(err) {
					if stop == nil && ctx.Err() == nil {
						stop = err
						// Out of budget: requests already sent may still land; stop only new work.
						if asProblem(err).Code != "BUDGET_EXHAUSTED" {
							cancel()
						}
					}
				} else if err != nil {
					op.problem(err)
				}
				progress := map[string]any{"done": done, "total": n}
				mu.Unlock()
				if stage != "" {
					op.stage(stage, progress)
				}
			}
		}()
	}
feed:
	for i := range n {
		mu.Lock()
		stopped := stop != nil
		mu.Unlock()
		if stopped {
			break
		}
		select {
		case next <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	if stop != nil {
		return stop
	}
	return op.ctx.Err()
}

// enhance shares one detail+journal path between search, metadata and citations.
func enhance(op *Op, site Backend, store *Store, p Paper, quality bool) (Paper, error) {
	fresh, err := site.Detail(op, p)
	if err != nil {
		return p, recordError(err, p, "detail")
	}
	if !fresh.DetailAt.Equal(p.DetailAt) {
		p, _ = store.Update(p.Ref, func(x *Paper) { *x = merge(*x, fresh) })
	}
	if quality {
		q, err := site.Journal(op, p)
		if err != nil {
			return p, recordError(err, p, "journal")
		}
		if q != nil && (p.Quality == nil || !p.Quality.ObservedAt.Equal(q.ObservedAt)) {
			p, _ = store.Update(p.Ref, func(x *Paper) { x.Quality = q })
		}
	}
	return p, nil
}
func recordError(err error, p Paper, stage string) error {
	if fatal(err) {
		return err
	}
	problem := *asProblem(err)
	problem.Details = map[string]any{"record_ref": p.Ref, "title": p.Title, "stage": stage, "cause": problem.Details}
	return &problem
}

func (r *Researcher) finish(op *Op, st *searchState, status string) SearchSummary {
	papers := r.Store.Papers(st.Refs)
	var ranked, selected []Ranked
	if st.Plan.Method == "structured" {
		for _, p := range papers {
			ranked = append(ranked, Ranked{Paper: p, Eligible: true})
		}
	} else {
		ranked = Rank(papers, st.Plan, st.Hits, 0)
	}
	if st.Plan.Method == "structured" || st.Input.Options.Metadata == "basic" {
		selected = ranked[:min(100, len(ranked))]
	} else {
		selected = Select(ranked, 100)
	}
	op.mu.Lock()
	if len(op.rec.Errors) > 0 && status == "completed" {
		status = "partial"
	}
	op.mu.Unlock()
	chosen := map[string]bool{}
	for _, s := range selected {
		chosen[s.Paper.Ref] = true
	}
	out := SearchSummary{SearchID: st.SearchID, Status: status, Coverage: st.Coverage, Plan: st.Plan, Candidates: len(ranked), Selected: len(selected), Exclusions: map[string]int{}, Results: []TitleResult{}}
	record := &SearchRecord{ID: st.SearchID, Status: status, Query: st.Input.Query, Plan: st.Plan, Coverage: append([]Coverage(nil), st.Coverage...), Created: time.Now().UTC()}
	sources := map[string]bool{}
	for _, e := range ranked {
		p := e.Paper
		if p.Abstract != "" && !p.DetailAt.IsZero() {
			out.Enhanced++
		}
		if p.Quality != nil && p.Quality.State == "present" {
			sources[sourceKey(p.Source)] = true
		}
		if e.Eligible {
			out.Eligible++
		}
		if !chosen[p.Ref] {
			reason := "ranking_or_quality_threshold"
			if !e.Eligible {
				reason = "insufficient_relevance_evidence"
			}
			out.Exclusions[reason]++
		}
		record.Entries = append(record.Entries, Entry{Ref: p.Ref, Score: e.Score, Eligible: e.Eligible, Selected: chosen[p.Ref], Evidence: e.Evidence})
	}
	out.QualitySources = len(sources)
	cacheKey := ""
	if status == "completed" {
		cacheKey = st.CacheKey
	}
	r.Store.PutSearch(record, cacheKey)
	limit := st.Input.Limit
	if limit == 0 {
		limit = 50
	}
	for _, s := range selected[:min(limit, len(selected))] {
		out.Results = append(out.Results, TitleResult{Ref: s.Paper.Ref, Title: s.Paper.Title, URL: s.Paper.URL})
	}
	return out
}
