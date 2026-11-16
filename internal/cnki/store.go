// SQLite store: papers with their identity index, journal evidence,
// searches, operations, reading snapshots and downloaded files.

package cnki

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store writes every change straight to SQLite (WAL, one connection). Nothing
// is held in memory beyond the rows a call reads, so startup is instant and a
// change costs one small transaction instead of rewriting the whole data set.
type Store struct {
	mu   sync.Mutex // serializes read-merge-write sequences (Upsert, Update)
	root string
	lock *os.File
	db   *sql.DB
	last error // last failed write, reported by Stats
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

// storedPaper keeps the site locator, which is never reported.
type storedPaper struct {
	Paper
	Locator string `json:"locator,omitempty"`
}

const schemaV1 = `CREATE TABLE papers(ref TEXT PRIMARY KEY, title_key TEXT NOT NULL, body TEXT NOT NULL, updated INTEGER NOT NULL);
 CREATE INDEX paper_title ON papers(title_key);
 CREATE TABLE paper_keys(key TEXT NOT NULL, ref TEXT NOT NULL, PRIMARY KEY(key, ref)) WITHOUT ROWID;
 CREATE TABLE sources(key TEXT PRIMARY KEY, body TEXT NOT NULL, updated INTEGER NOT NULL);
 CREATE TABLE searches(id TEXT PRIMARY KEY, cache_key TEXT NOT NULL, created INTEGER NOT NULL, body TEXT NOT NULL);
 CREATE INDEX search_cache ON searches(cache_key, created DESC);
 CREATE TABLE operations(id TEXT PRIMARY KEY, body TEXT NOT NULL, updated INTEGER NOT NULL);
 CREATE TABLE artifacts(key TEXT PRIMARY KEY, body TEXT NOT NULL, updated INTEGER NOT NULL);
 CREATE TABLE snapshots(id TEXT PRIMARY KEY, ref TEXT NOT NULL, body TEXT NOT NULL, updated INTEGER NOT NULL);
 CREATE INDEX snapshot_ref ON snapshots(ref, updated DESC);
 PRAGMA user_version=1;`

func OpenStore(root string) (*Store, error) {
	if err := claimDataRoot(root); err != nil {
		return nil, err
	}
	lock, err := lockDirectory(filepath.Join(root, ".cnki-store.lock"))
	if err != nil {
		return nil, fail("DATA_DIRECTORY_IN_USE", "该知网数据目录已被另一服务使用")
	}
	path := filepath.Join(root, "cnki.sqlite")
	_, statErr := os.Stat(path)
	fresh := os.IsNotExist(statErr)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		_ = unlockDirectory(lock)
		return nil, err
	}
	f.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		_ = unlockDirectory(lock)
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{root: root, lock: lock, db: db}
	for _, pragma := range []string{"PRAGMA auto_vacuum=INCREMENTAL", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=NORMAL", "PRAGMA busy_timeout=5000"} {
		if _, err = db.Exec(pragma); err != nil {
			break
		}
	}
	if err == nil {
		err = s.initialize()
	}
	if err == nil && fresh {
		if err = s.importLegacy(filepath.Join(root, "research.json")); err == nil {
			// A large import leaves a WAL as big as the data; fold it in and truncate.
			_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		}
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	s.prune()
	return s, nil
}

func (s *Store) initialize() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	switch version {
	case 1:
		return nil
	case 0:
		_, err := s.db.Exec(schemaV1)
		return err
	}
	return fail("STORE_INCOMPATIBLE", "未知的知网 SQLite 版本")
}

// importLegacy copies papers, journal evidence and downloaded-file records
// from the 2.0 JSON store into a new database. Searches and operations are
// not carried over (their IDs and format changed). The JSON file is left as is.
func (s *Store) importLegacy(path string) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var old struct {
		Version   int                 `json:"version"`
		Papers    map[string]*Paper   `json:"papers"`
		Sources   map[string]Quality  `json:"sources"`
		Artifacts map[string]Artifact `json:"artifacts"`
	}
	if json.Unmarshal(raw, &old) != nil || old.Version != 5 {
		return nil // unreadable or unknown: start empty, keep the file untouched
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range old.Papers {
		if p != nil && p.Ref != "" {
			if err = writePaper(tx, storedPaper{Paper: *p}); err != nil {
				return err
			}
		}
	}
	for k, q := range old.Sources {
		if err = saveJSON(tx, "sources", "key", k, q); err != nil {
			return err
		}
	}
	for k, a := range old.Artifacts {
		if err = saveJSON(tx, "artifacts", "key", k, a); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// prune bounds history. Papers, journal evidence and files are kept.
func (s *Store) prune() {
	now := time.Now().UnixNano()
	day := int64(24 * time.Hour)
	for _, q := range []struct {
		sql string
		age int64
	}{
		{"DELETE FROM operations WHERE updated < ?", 7 * day},
		{"DELETE FROM searches WHERE created < ?", 14 * day},
		{"DELETE FROM snapshots WHERE updated < ?", 60 * day},
	} {
		_, _ = s.db.Exec(q.sql, now-q.age)
	}
	_, _ = s.db.Exec("PRAGMA incremental_vacuum")
}

func (s *Store) Close() error {
	_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	err := s.db.Close()
	if e := unlockDirectory(s.lock); err == nil {
		err = e
	}
	return err
}

type executor interface {
	Exec(string, ...any) (sql.Result, error)
}

// saveJSON upserts one JSON row; table and key column are constants of this file.
func saveJSON(q executor, table, column, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = q.Exec("INSERT INTO "+table+"("+column+",body,updated) VALUES(?,?,?) ON CONFLICT("+column+") DO UPDATE SET body=excluded.body, updated=excluded.updated", id, string(b), time.Now().UnixNano())
	return err
}

func loadJSON[T any](row *sql.Row) (T, bool) {
	var v T
	var raw string
	if row.Scan(&raw) != nil || json.Unmarshal([]byte(raw), &v) != nil {
		return v, false
	}
	return v, true
}

func (s *Store) note(err error) {
	if err != nil {
		s.mu.Lock()
		s.last = err
		s.mu.Unlock()
	}
}

func writePaper(q executor, p storedPaper) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if _, err = q.Exec("INSERT INTO papers(ref,title_key,body,updated) VALUES(?,?,?,?) ON CONFLICT(ref) DO UPDATE SET title_key=excluded.title_key, body=excluded.body, updated=excluded.updated", p.Ref, titleKey(p.Title), string(b), time.Now().UnixNano()); err != nil {
		return err
	}
	for _, k := range identityKeys(p.Paper) {
		if _, err = q.Exec("INSERT OR IGNORE INTO paper_keys(key,ref) VALUES(?,?)", k, p.Ref); err != nil {
			return err
		}
	}
	return nil
}

type querier interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

func readPaper(q querier, ref string) (storedPaper, bool) {
	p, ok := loadJSON[storedPaper](q.QueryRow("SELECT body FROM papers WHERE ref=?", ref))
	if ok {
		p.Paper.Locator = p.Locator
	}
	return p, ok
}

func keyRefs(q querier, keys []string) []string {
	out := []string{}
	for _, k := range keys {
		rows, err := q.Query("SELECT ref FROM paper_keys WHERE key=?", k)
		if err != nil {
			continue
		}
		for rows.Next() {
			var r string
			if rows.Scan(&r) == nil {
				out = append(out, r)
			}
		}
		rows.Close()
	}
	return out
}

// Upsert merges observations into existing records by verified identity and
// returns the stored versions in input order.
func (s *Store) Upsert(fresh []Paper) []Paper {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Paper, 0, len(fresh))
	tx, err := s.db.Begin()
	if err != nil {
		s.last = err
		return fresh
	}
	defer tx.Rollback()
	for _, p := range fresh {
		candidates := keyRefs(tx, identityKeys(p))
		if p.Ref != "" {
			candidates = append(candidates, p.Ref)
		}
		sort.Strings(candidates)
		var match *Paper
		for _, ref := range uniqueStrings(candidates) {
			if old, ok := readPaper(tx, ref); ok && (old.Ref == p.Ref && detailMatches(old.Paper, p) || SamePaper(old.Paper, p)) {
				match = &old.Paper
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
			if _, taken := readPaper(tx, p.Ref); taken {
				p.Ref = newID("cr_")
			}
		}
		if err = writePaper(tx, storedPaper{Paper: p, Locator: p.Locator}); err != nil {
			s.last = err
		}
		out = append(out, p)
	}
	if err = tx.Commit(); err != nil {
		s.last = err
	}
	return out
}

// Update applies fn to a stored paper atomically.
func (s *Store) Update(ref string, fn func(*Paper)) (Paper, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := readPaper(s.db, ref)
	if !ok {
		return Paper{}, false
	}
	next := old.Paper
	fn(&next)
	next.Ref = ref
	if err := writePaper(s.db, storedPaper{Paper: next, Locator: next.Locator}); err != nil {
		s.last = err
	}
	return next, true
}

func (s *Store) Paper(ref string) (Paper, bool) {
	p, ok := readPaper(s.db, ref)
	return p.Paper, ok
}

// Papers returns the stored papers in the order of refs (unknown refs are skipped).
func (s *Store) Papers(refs []string) []Paper {
	found := make(map[string]Paper, len(refs))
	for start := 0; start < len(refs); start += 500 {
		chunk := refs[start:min(len(refs), start+500)]
		args := make([]any, len(chunk))
		for i, r := range chunk {
			args[i] = r
		}
		rows, err := s.db.Query("SELECT body FROM papers WHERE ref IN (?"+strings.Repeat(",?", len(chunk)-1)+")", args...)
		if err != nil {
			s.note(err)
			continue
		}
		for rows.Next() {
			var raw string
			var p storedPaper
			if rows.Scan(&raw) == nil && json.Unmarshal([]byte(raw), &p) == nil {
				p.Paper.Locator = p.Locator
				found[p.Ref] = p.Paper
			}
		}
		rows.Close()
	}
	out := make([]Paper, 0, len(refs))
	for _, r := range refs {
		if p, ok := found[r]; ok {
			out = append(out, p)
		}
	}
	return out
}

func (s *Store) ByTitle(title string) []Paper {
	rows, err := s.db.Query("SELECT ref FROM papers WHERE title_key=? ORDER BY ref", titleKey(title))
	if err != nil {
		return nil
	}
	refs := []string{}
	for rows.Next() {
		var r string
		if rows.Scan(&r) == nil {
			refs = append(refs, r)
		}
	}
	rows.Close()
	return s.Papers(refs)
}

func (s *Store) Source(key string) (Quality, bool) {
	return loadJSON[Quality](s.db.QueryRow("SELECT body FROM sources WHERE key=?", key))
}
func (s *Store) PutSource(key string, q Quality) { s.note(saveJSON(s.db, "sources", "key", key, q)) }

func (s *Store) Search(id string) (*SearchRecord, bool) {
	r, ok := loadJSON[SearchRecord](s.db.QueryRow("SELECT body FROM searches WHERE id=?", id))
	return &r, ok
}

// PutSearch stores a search; a non-empty cacheKey makes it reusable for the cache window.
func (s *Store) PutSearch(r *SearchRecord, cacheKey string) {
	b, err := json.Marshal(r)
	if err == nil {
		_, err = s.db.Exec("INSERT INTO searches(id,cache_key,created,body) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET cache_key=excluded.cache_key, created=excluded.created, body=excluded.body", r.ID, cacheKey, time.Now().UnixNano(), string(b))
	}
	s.note(err)
}
func (s *Store) CachedSearch(key string) (*SearchRecord, bool) {
	if key == "" {
		return nil, false
	}
	r, ok := loadJSON[SearchRecord](s.db.QueryRow("SELECT body FROM searches WHERE cache_key=? AND created>? ORDER BY created DESC LIMIT 1", key, time.Now().Add(-searchCacheTTL).UnixNano()))
	return &r, ok
}

func (s *Store) PutOp(r *OpRecord) { s.note(saveJSON(s.db, "operations", "id", r.ID, r)) }
func (s *Store) Op(id string) (*OpRecord, bool) {
	r, ok := loadJSON[OpRecord](s.db.QueryRow("SELECT body FROM operations WHERE id=?", id))
	return &r, ok
}

func (s *Store) Artifact(key string) (Artifact, bool) {
	return loadJSON[Artifact](s.db.QueryRow("SELECT body FROM artifacts WHERE key=?", key))
}
func (s *Store) PutArtifact(key string, a Artifact) {
	a.Updated = time.Now().UTC()
	s.note(saveJSON(s.db, "artifacts", "key", key, a))
}

// PendingIncoming lists the transfer folders of downloads still awaiting confirmation.
func (s *Store) PendingIncoming(maxAge time.Duration) map[string]bool {
	out := map[string]bool{}
	rows, err := s.db.Query("SELECT body FROM artifacts")
	if err != nil {
		return out
	}
	defer rows.Close()
	cutoff := time.Now().Add(-maxAge)
	for rows.Next() {
		var raw string
		var a Artifact
		// The record's own time counts: imported 2.0 rows are all new rows.
		if rows.Scan(&raw) == nil && json.Unmarshal([]byte(raw), &a) == nil && a.State == "clicked" && a.Incoming != "" && a.Updated.After(cutoff) {
			out[a.Incoming] = true
		}
	}
	return out
}

// PutSnapshot saves extracted reading text, so later reads cost no request.
func (s *Store) PutSnapshot(v Snapshot) {
	b, err := json.Marshal(v)
	if err == nil {
		_, err = s.db.Exec("INSERT INTO snapshots(id,ref,body,updated) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body, updated=excluded.updated", v.ID, v.Ref, string(b), time.Now().UnixNano())
	}
	s.note(err)
}
func (s *Store) Snapshot(id string) (Snapshot, bool) {
	return loadJSON[Snapshot](s.db.QueryRow("SELECT body FROM snapshots WHERE id=?", id))
}

// LatestSnapshot returns the newest snapshot of a paper.
func (s *Store) LatestSnapshot(ref string) (Snapshot, bool) {
	return loadJSON[Snapshot](s.db.QueryRow("SELECT body FROM snapshots WHERE ref=? ORDER BY updated DESC LIMIT 1", ref))
}

func (s *Store) Stats() map[string]any {
	out := map[string]any{}
	for _, t := range []string{"papers", "sources", "searches", "operations", "artifacts", "snapshots"} {
		var n int
		if s.db.QueryRow("SELECT COUNT(*) FROM "+t).Scan(&n) == nil {
			out[t] = n
		}
	}
	s.mu.Lock()
	if s.last != nil {
		out["last_write_error"] = s.last.Error()
	}
	s.mu.Unlock()
	return out
}

func atomicJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".cnki-write-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
