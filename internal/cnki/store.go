package cnki

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Store keeps all research data in memory and persists one JSON snapshot in
// the background. Network work never waits for disk I/O; a crash loses at most
// the last flush interval, and every write is atomic (temp file + rename).
type Store struct {
	mu    sync.RWMutex
	root  string
	data  storeFile
	index map[string][]string // identity and title keys -> refs
	dirty bool
	lock  *os.File
	stop  chan struct{}
	done  chan struct{}
}

type storeFile struct {
	Version   int                      `json:"version"`
	Papers    map[string]*Paper        `json:"papers"`
	Sources   map[string]Quality       `json:"sources"`
	Searches  map[string]*SearchRecord `json:"searches"`
	Cache     map[string]cacheEntry    `json:"search_cache"`
	Ops       map[string]*OpRecord     `json:"operations"`
	Artifacts map[string]Artifact      `json:"artifacts"`
}
type cacheEntry struct {
	SearchID string    `json:"search_id"`
	At       time.Time `json:"at"`
}
type SearchRecord struct {
	ID       string     `json:"search_id"`
	Status   string     `json:"status"`
	Query    string     `json:"query"`
	Plan     Plan       `json:"plan"`
	Coverage []Coverage `json:"coverage"`
	Entries  []Entry    `json:"entries"`
	Created  time.Time  `json:"created_at"`
}
type Entry struct {
	Ref      string  `json:"record_ref"`
	Score    float64 `json:"score"`
	Eligible bool    `json:"eligible"`
	Selected bool    `json:"selected"`
	Evidence Scores  `json:"evidence"`
}

const storeVersion = 5
const flushInterval = 2 * time.Second

func OpenStore(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	lock, err := lockDirectory(filepath.Join(root, "cnki.lock"))
	if err != nil {
		return nil, fail("DATA_DIRECTORY_IN_USE", "另一个 MCP 进程正在使用此数据目录")
	}
	s := &Store{root: root, lock: lock, index: map[string][]string{}, stop: make(chan struct{}), done: make(chan struct{})}
	raw, err := os.ReadFile(s.path())
	if err == nil {
		if err = json.Unmarshal(raw, &s.data); err != nil || s.data.Version != storeVersion {
			_ = os.Rename(s.path(), s.path()+".unreadable-"+time.Now().Format("20060102-150405"))
			s.data = storeFile{}
		}
	}
	d := &s.data
	d.Version = storeVersion
	if d.Papers == nil {
		d.Papers = map[string]*Paper{}
	}
	if d.Sources == nil {
		d.Sources = map[string]Quality{}
	}
	if d.Searches == nil {
		d.Searches = map[string]*SearchRecord{}
	}
	if d.Cache == nil {
		d.Cache = map[string]cacheEntry{}
	}
	if d.Ops == nil {
		d.Ops = map[string]*OpRecord{}
	}
	if d.Artifacts == nil {
		d.Artifacts = map[string]Artifact{}
	}
	for _, p := range d.Papers {
		s.indexPaper(*p)
	}
	go s.flushLoop()
	return s, nil
}
func (s *Store) path() string { return filepath.Join(s.root, "research.json") }
func (s *Store) Close() error {
	close(s.stop)
	<-s.done
	err := s.Flush()
	_ = unlockDirectory(s.lock)
	return err
}
func (s *Store) flushLoop() {
	defer close(s.done)
	t := time.NewTicker(flushInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			_ = s.Flush()
		}
	}
}
func (s *Store) Flush() error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	s.trim()
	data, err := json.Marshal(&s.data)
	s.dirty = false
	s.mu.Unlock()
	if err != nil {
		return err
	}
	temp := s.path() + ".tmp"
	f, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err == nil {
		err = os.Rename(temp, s.path())
	}
	if err != nil {
		s.mu.Lock()
		s.dirty = true
		s.mu.Unlock()
	}
	return err
}

// trim bounds history; papers and journal evidence are kept.
func (s *Store) trim() {
	cut := func(n int, created func(string) time.Time, keys []string, del func(string)) {
		if len(keys) <= n {
			return
		}
		sort.Slice(keys, func(i, j int) bool { return created(keys[i]).After(created(keys[j])) })
		for _, k := range keys[n:] {
			del(k)
		}
	}
	searches := make([]string, 0, len(s.data.Searches))
	for k := range s.data.Searches {
		searches = append(searches, k)
	}
	cut(300, func(k string) time.Time { return s.data.Searches[k].Created }, searches, func(k string) { delete(s.data.Searches, k) })
	ops := make([]string, 0, len(s.data.Ops))
	for k := range s.data.Ops {
		ops = append(ops, k)
	}
	cut(200, func(k string) time.Time { return s.data.Ops[k].Created }, ops, func(k string) { delete(s.data.Ops, k) })
	for k, c := range s.data.Cache {
		if time.Since(c.At) > searchCacheTTL || s.data.Searches[c.SearchID] == nil {
			delete(s.data.Cache, k)
		}
	}
}

func (s *Store) indexPaper(p Paper) {
	for _, k := range append(identityKeys(p), "title:"+titleKey(p.Title)) {
		refs := s.index[k]
		found := false
		for _, r := range refs {
			found = found || r == p.Ref
		}
		if !found {
			s.index[k] = append(refs, p.Ref)
		}
	}
}

// Upsert merges observations into existing records by verified identity and
// returns the stored versions in input order.
func (s *Store) Upsert(fresh []Paper) []Paper {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Paper, 0, len(fresh))
	for _, p := range fresh {
		var match *Paper
		candidates := []string{}
		for _, k := range identityKeys(p) {
			candidates = append(candidates, s.index[k]...)
		}
		if p.Ref != "" {
			candidates = append(candidates, p.Ref)
		}
		sort.Strings(candidates)
		for _, ref := range candidates {
			if old := s.data.Papers[ref]; old != nil && (old.Ref == p.Ref && detailMatches(*old, p) || SamePaper(*old, p)) {
				match = old
				break
			}
		}
		if match != nil {
			merged := merge(*match, p)
			merged.Ref = match.Ref
			p = merged
		} else {
			if p.Ref == "" {
				p.Ref = provisionalRef(p)
			}
			if s.data.Papers[p.Ref] != nil {
				p.Ref = newID("rec_")
			}
		}
		stored := p
		s.data.Papers[p.Ref] = &stored
		s.indexPaper(p)
		out = append(out, p)
	}
	s.dirty = true
	return out
}

// Update applies fn to a stored paper atomically.
func (s *Store) Update(ref string, fn func(*Paper)) (Paper, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.data.Papers[ref]
	if p == nil {
		return Paper{}, false
	}
	next := *p
	fn(&next)
	next.Ref = ref
	s.data.Papers[ref] = &next
	s.indexPaper(next)
	s.dirty = true
	return next, true
}
func (s *Store) Paper(ref string) (Paper, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p := s.data.Papers[ref]; p != nil {
		return *p, true
	}
	return Paper{}, false
}
func (s *Store) Papers(refs []string) []Paper {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Paper, 0, len(refs))
	for _, ref := range refs {
		if p := s.data.Papers[ref]; p != nil {
			out = append(out, *p)
		}
	}
	return out
}
func (s *Store) ByTitle(title string) []Paper {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Paper{}
	key := titleKey(title)
	for _, ref := range s.index["title:"+key] {
		if p := s.data.Papers[ref]; p != nil && titleKey(p.Title) == key {
			out = append(out, *p)
		}
	}
	return out
}
func (s *Store) Source(key string) (Quality, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	q, ok := s.data.Sources[key]
	return q, ok
}
func (s *Store) PutSource(key string, q Quality) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Sources[key] = q
	s.dirty = true
}
func (s *Store) Search(id string) (*SearchRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.data.Searches[id]
	return r, r != nil
}

// PutSearch stores an immutable record; callers never mutate it afterwards.
func (s *Store) PutSearch(r *SearchRecord, cacheKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Searches[r.ID] = r
	if cacheKey != "" {
		s.data.Cache[cacheKey] = cacheEntry{SearchID: r.ID, At: time.Now().UTC()}
	}
	s.dirty = true
}
func (s *Store) CachedSearch(key string) (*SearchRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.data.Cache[key]
	if !ok || time.Since(c.At) > searchCacheTTL {
		return nil, false
	}
	r := s.data.Searches[c.SearchID]
	return r, r != nil
}
func (s *Store) PutOp(r *OpRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Ops[r.ID] = r
	s.dirty = true
}
func (s *Store) Op(id string) (*OpRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.data.Ops[id]
	return r, r != nil
}
func (s *Store) Artifact(key string) (Artifact, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.data.Artifacts[key]
	return a, ok
}
func (s *Store) PutArtifact(key string, a Artifact) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.Updated = time.Now().UTC()
	s.data.Artifacts[key] = a
	s.dirty = true
}
func (s *Store) Stats() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]int{"papers": len(s.data.Papers), "journals": len(s.data.Sources), "searches": len(s.data.Searches), "operations": len(s.data.Ops), "artifacts": len(s.data.Artifacts)}
}
