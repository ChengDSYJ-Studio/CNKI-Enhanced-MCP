package cnki

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// simSite emulates CNKI: every request takes a rate-limit slot and some latency.
// Channel q1 lists papers 0..119, q2 30..149, q3 60..179; feedback channels
// list fresh papers. Details carry shared keywords so feedback triggers.
type simSite struct {
	limit    *Limiter
	latency  func(kind string) time.Duration
	pageSize int
	mu       sync.Mutex
	grids    int
	details  int
	journals map[string]int
	inflight atomic.Int32
	peak     atomic.Int32
	journal  flights
	cache    sync.Map
}

func newSim(interval time.Duration, latency func(string) time.Duration) *simSite {
	return &simSite{limit: NewLimiter(interval, 6), latency: latency, pageSize: 20, journals: map[string]int{}}
}
func (s *simSite) request(op *Op, kind string) error {
	if err := op.charge(); err != nil {
		return err
	}
	release, err := s.limit.Acquire(op.ctx)
	if err != nil {
		return err
	}
	defer release()
	n := s.inflight.Add(1)
	defer s.inflight.Add(-1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	return sleep(op.ctx, s.latency(kind))
}
func simPaper(i int) Paper {
	p := Paper{Title: fmt.Sprintf("自动驾驶产品责任研究 %03d", i), Authors: []string{fmt.Sprintf("作者%d", i)}, Source: fmt.Sprintf("法学期刊%d", i%15), Year: 2020 + i%6, Type: "期刊", DBName: "CJFD", Filename: fmt.Sprintf("SIM%04d", i), URL: fmt.Sprintf("https://kns.cnki.net/kcms2/article/abstract?v=%d", i), ListedAt: time.Now()}
	p.Ref = provisionalRef(p)
	return p
}
func (s *simSite) Grid(op *Op, ch Channel, page int, in SearchInput) (Grid, error) {
	if err := s.request(op, "grid"); err != nil {
		return Grid{}, err
	}
	s.mu.Lock()
	s.grids++
	s.mu.Unlock()
	start, size := map[string]int{"q1": 0, "q2": 30, "q3": 60, "feedback1": 1000, "feedback2": 2000}[ch.ID], 120
	if ch.ID[0] == 'f' {
		size = 20
	}
	g := Grid{Page: page}
	for i := (page - 1) * s.pageSize; i < min(size, page*s.pageSize); i++ {
		g.Papers = append(g.Papers, simPaper(start+i))
	}
	total := size
	g.Total, g.Exhausted = &total, page*s.pageSize >= size
	return g, nil
}
func (s *simSite) Detail(op *Op, p Paper) (Paper, error) {
	if detailFresh(p) {
		return p, nil
	}
	if err := s.request(op, "detail"); err != nil {
		return p, err
	}
	s.mu.Lock()
	s.details++
	s.mu.Unlock()
	p.Abstract = "自动驾驶产品责任的归责与风险分配"
	p.Keywords = []string{"自动驾驶", "产品责任", "风险分配", "算法责任"}
	p.SourceURL = "https://navi.cnki.net/" + p.Source
	p.DetailAt, p.DetailParser = time.Now(), ParserVersion
	return p, nil
}
func (s *simSite) Journal(op *Op, p Paper) (*Quality, error) {
	if q, ok := s.cache.Load(p.Source); ok {
		return q.(*Quality), nil
	}
	return s.journal.do(op.ctx, p.Source, func() (*Quality, error) {
		if err := s.request(op, "journal"); err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.journals[p.Source]++
		s.mu.Unlock()
		impact := 2.5
		q := &Quality{Canonical: p.Source, Tiers: []string{"CSSCI"}, CompositeImpact: &impact, State: "present", ObservedAt: time.Now(), Parser: ParserVersion}
		s.cache.Store(p.Source, q)
		return q, nil
	})
}

func runSim(t testing.TB, site *simSite, store *Store, budget Budget, st *searchState) (OpRecord, SearchSummary, time.Duration) {
	jobs := NewJobs(store)
	r := &Researcher{Store: store, Site: site, Workers: 8}
	var summary SearchSummary
	began := time.Now()
	op := jobs.Start("search", budget, nil, func(op *Op) (any, error) {
		st.SearchID = op.ID()
		s, err := r.Run(op, st)
		summary = s
		return s, err
	})
	rec := jobs.Wait(context.Background(), op, 30*time.Minute)
	return rec, summary, time.Since(began)
}
func simInput(t testing.TB) (SearchInput, Plan) {
	in := SearchInput{Query: "自动驾驶 产品责任", Concepts: [][]string{{"自动驾驶"}, {"产品责任"}}}
	if err := in.Validate(); err != nil {
		t.Fatal(err)
	}
	plan, err := MakePlan(in)
	if err != nil {
		t.Fatal(err)
	}
	return in, plan
}

func TestPipelineCoverageFeedbackAndDedup(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	site := newSim(0, func(string) time.Duration { return time.Millisecond })
	in, plan := simInput(t)
	rec, sum, _ := runSim(t, site, store, budget(in.Options), &searchState{Input: in, Plan: plan})
	if rec.Status != "completed" {
		t.Fatalf("status %s %+v", rec.Status, rec.Errors)
	}
	// 3 channels x 5 pages (max_pages) + 2 feedback pages; 160 + 40 unique papers.
	if site.grids != 17 || sum.Candidates != 200 || len(sum.Coverage) != 5 {
		t.Fatalf("coverage: grids=%d candidates=%d coverage=%d", site.grids, sum.Candidates, len(sum.Coverage))
	}
	// Pool: EnhancementSize(160)=60 initial + EnhancementSize(40)=40 feedback.
	if site.details != 100 || sum.Enhanced != 100 {
		t.Fatalf("details=%d enhanced=%d", site.details, sum.Enhanced)
	}
	for source, n := range site.journals {
		if n != 1 {
			t.Fatalf("journal %s fetched %d times", source, n)
		}
	}
	if rec.Requests != site.grids+site.details+len(site.journals) || sum.Selected == 0 || sum.QualitySources == 0 {
		t.Fatalf("accounting: %+v %+v", rec, sum)
	}
	// Same search again within the TTL: zero requests via the persistent caches.
	before := rec.Requests
	rec2, _, _ := runSim(t, site, store, budget(in.Options), &searchState{Input: in, Plan: plan})
	if rec2.Requests != 17 || before != rec.Requests {
		t.Fatalf("repeat should only re-list (17 grid requests), got %d", rec2.Requests)
	}
}

func TestPipelineResumesWithoutRepeatingWork(t *testing.T) {
	store, _ := OpenStore(t.TempDir())
	defer store.Close()
	site := newSim(0, func(string) time.Duration { return time.Millisecond })
	in, plan := simInput(t)
	st := &searchState{Input: in, Plan: plan}
	rec, _, _ := runSim(t, site, store, Budget{MaxRequests: 50, TimeoutSeconds: 60}, st)
	if rec.Status != "partial" || asProblem(&rec.Errors[len(rec.Errors)-1]).Code != "BUDGET_EXHAUSTED" {
		t.Fatalf("expected budget stop: %+v", rec)
	}
	first := rec.Requests
	rec, sum, _ := runSim(t, site, store, Budget{MaxRequests: 500, TimeoutSeconds: 60}, st)
	if rec.Status != "completed" || first+rec.Requests != site.grids+site.details+len(site.journals) || sum.Enhanced != 100 {
		t.Fatalf("resume repeated or lost work: first=%d second=%d total=%d %+v", first, rec.Requests, site.grids+site.details+len(site.journals), rec.Errors)
	}
}

// The pipeline must be bound by the request interval, not by latency: with
// latency 3x the interval, a serial loop needs N*latency, the pipeline ~N*interval.
func TestPipelineIsRateBoundNotLatencyBound(t *testing.T) {
	store, _ := OpenStore(t.TempDir())
	defer store.Close()
	interval, latency := 10*time.Millisecond, 30*time.Millisecond
	site := newSim(interval, func(string) time.Duration { return latency })
	in, plan := simInput(t)
	rec, _, elapsed := runSim(t, site, store, budget(in.Options), &searchState{Input: in, Plan: plan})
	n := time.Duration(rec.Requests)
	bound := n*interval + 6*latency // dependency stages each add one latency
	if rec.Status != "completed" || elapsed > bound*13/10 || elapsed >= n*latency/2 {
		t.Fatalf("elapsed %v, rate bound %v, serial %v", elapsed, bound, n*latency)
	}
	if site.peak.Load() < 3 {
		t.Fatalf("requests did not overlap (peak %d)", site.peak.Load())
	}
	t.Logf("requests=%d elapsed=%v bound=%v serial=%v peak_inflight=%d", rec.Requests, elapsed, bound, n*latency, site.peak.Load())
}

func TestLimiterSpacingAndHold(t *testing.T) {
	l := NewLimiter(10*time.Millisecond, 4)
	began := time.Now()
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := l.Acquire(context.Background())
			if err == nil {
				time.Sleep(5 * time.Millisecond)
				release()
			}
		}()
	}
	wg.Wait()
	if d := time.Since(began); d < 90*time.Millisecond || d > 200*time.Millisecond {
		t.Fatalf("10 starts at 10ms spacing took %v", d)
	}
	l.Hold()
	got := make(chan struct{})
	go func() {
		release, _ := l.Acquire(context.Background())
		release()
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("request passed during human verification")
	case <-time.After(50 * time.Millisecond):
	}
	l.Release()
	<-got
}

func TestJobsAwaitUserResumeAndCancel(t *testing.T) {
	store, _ := OpenStore(t.TempDir())
	defer store.Close()
	jobs := NewJobs(store)
	op := jobs.Start("download", Budget{MaxRequests: 5, TimeoutSeconds: 1}, nil, func(op *Op) (any, error) {
		if err := op.charge(); err != nil {
			return nil, err
		}
		op.awaitUser("请验证")
		select {
		case <-op.resumeSignal():
		case <-op.ctx.Done():
			return nil, op.ctx.Err()
		}
		time.Sleep(1100 * time.Millisecond) // waiting for a person is not charged
		op.userDone()
		return map[string]any{"ok": true}, op.charge()
	})
	rec := jobs.Wait(context.Background(), op, 5*time.Second)
	if rec.Status != "awaiting_user" || rec.Message == "" {
		t.Fatalf("wait did not return for the person: %+v", rec)
	}
	began := time.Now()
	if again := jobs.Wait(context.Background(), op, 150*time.Millisecond); again.Status != "awaiting_user" || time.Since(began) < 140*time.Millisecond {
		t.Fatal("status poll during a human step must wait, not spin")
	}
	if err := jobs.Resume(op.ID()); err != nil {
		t.Fatal(err)
	}
	rec = jobs.Wait(context.Background(), op, 5*time.Second)
	if rec.Status != "partial" && rec.Status != "completed" || rec.Requests != 2 {
		t.Fatalf("after resume: %+v", rec)
	}
	slow := jobs.Start("search", Budget{}, nil, func(op *Op) (any, error) { <-op.ctx.Done(); return nil, op.ctx.Err() })
	if rec, _ = jobs.Cancel(context.Background(), slow.ID()); rec.Status != "cancelled" {
		t.Fatalf("cancel: %+v", rec)
	}
	if rec, _ := jobs.Status(context.Background(), slow.ID(), 0); rec.Status != "cancelled" {
		t.Fatal("finished op not persisted")
	}
}

func TestStoreIdentityPersistenceAndLock(t *testing.T) {
	root := t.TempDir()
	s, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	a := simPaper(1)
	saved := s.Upsert([]Paper{a})
	b := a
	b.Ref, b.Abstract = "", "摘要"
	if again := s.Upsert([]Paper{b}); again[0].Ref != saved[0].Ref || again[0].Abstract != "摘要" {
		t.Fatalf("native identity not merged: %+v", again)
	}
	c := a
	c.Ref, c.Filename, c.Authors = "", "OTHER", []string{"别人"}
	if other := s.Upsert([]Paper{c}); other[0].Ref == saved[0].Ref || len(s.ByTitle(a.Title)) != 2 {
		t.Fatal("same-title different paper collapsed")
	}
	if _, err = OpenStore(root); err == nil {
		t.Fatal("second process opened the same data directory")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if p, ok := s.Paper(saved[0].Ref); !ok || p.Abstract != "摘要" {
		t.Fatal("not persisted")
	}
}

func TestFlightsShareOneRequest(t *testing.T) {
	var g flights
	var calls atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = g.do(context.Background(), "法学研究", func() (*Quality, error) {
				calls.Add(1)
				time.Sleep(20 * time.Millisecond)
				return &Quality{}, nil
			})
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
}
