package cnki

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A 2.0 data directory (research.json, no owner marker) is claimed in place and
// its papers, journal evidence and file records are imported once.
func TestLegacyImportAndIdentity(t *testing.T) {
	root := t.TempDir()
	cited := 7
	legacy := map[string]any{"version": 5,
		"papers":    map[string]any{"rec_aaaaaaaaaaaaaaaaaaaaaaaa": Paper{Ref: "rec_aaaaaaaaaaaaaaaaaaaaaaaa", Title: "违约金调整研究", Authors: []string{"甲"}, Source: "法学研究", Year: 2020, DBCode: "CJFD", Filename: "FXYJ202001001", Cited: &cited}},
		"sources":   map[string]any{"法学研究": Quality{Canonical: "法学研究", Tiers: []string{"CSSCI"}, State: "present", Parser: ParserVersion, ObservedAt: time.Now()}},
		"artifacts": map[string]any{"rec_aaaaaaaaaaaaaaaaaaaaaaaa:pdf": Artifact{State: "completed", Ref: "rec_aaaaaaaaaaaaaaaaaaaaaaaa", Format: "pdf"}},
		"searches":  map[string]any{}, "operations": map[string]any{}}
	raw, _ := json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(root, "research.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := s.Paper("rec_aaaaaaaaaaaaaaaaaaaaaaaa")
	if !ok || p.Title != "违约金调整研究" || *p.Cited != 7 {
		t.Fatalf("paper not imported: %+v", p)
	}
	if q, ok := s.Source("法学研究"); !ok || q.Tiers[0] != "CSSCI" {
		t.Fatal("journal evidence not imported")
	}
	if _, ok := s.Artifact("rec_aaaaaaaaaaaaaaaaaaaaaaaa:pdf"); !ok {
		t.Fatal("file record not imported")
	}
	// A later list observation of the same paper merges into the old record
	// and keeps its locator across a restart.
	merged := s.Upsert([]Paper{{Title: "违约金调整研究", Authors: []string{"甲"}, Source: "法学研究", Year: 2020, DBCode: "CJFD", Filename: "FXYJ202001001", Locator: "https://kns.cnki.net/kcms2/article/abstract?v=x"}})
	if merged[0].Ref != "rec_aaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("identity not matched: %s", merged[0].Ref)
	}
	fresh := s.Upsert([]Paper{{Title: "另一篇", DBCode: "CJFD", Filename: "FXYJ202002002"}})
	if !strings.HasPrefix(fresh[0].Ref, "cr_") || len(fresh[0].Ref) != 19 {
		t.Fatalf("new ref format: %s", fresh[0].Ref)
	}
	s.Close()
	if info, err := os.Stat(filepath.Join(root, "cnki.sqlite-wal")); err == nil && info.Size() > 0 {
		t.Fatalf("WAL not truncated on close: %d bytes", info.Size())
	}
	if _, err := os.Stat(filepath.Join(root, "research.json")); err != nil {
		t.Fatal("legacy file must be left in place")
	}
	s, err = OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if p, _ := s.Paper("rec_aaaaaaaaaaaaaaaaaaaaaaaa"); p.Locator == "" {
		t.Fatal("locator not persisted")
	}
	if len(s.ByTitle("违约金调整研究")) != 1 || len(s.Papers([]string{fresh[0].Ref, "missing", "rec_aaaaaaaaaaaaaaaaaaaaaaaa"})) != 2 {
		t.Fatal("lookup by title or refs")
	}
}

// A complete reading is served from its snapshot; trial text is not reused.
func TestReadReusesSnapshots(t *testing.T) {
	root := t.TempDir()
	s, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{Root: root, Store: s, Jobs: NewJobs(s), Site: &Site{}, wait: time.Second}
	defer s.Close()
	p := s.Upsert([]Paper{{Title: "正文测试", DBCode: "CJFD", Filename: "X1"}})[0]
	text := strings.Repeat("正", 9000)
	s.PutSnapshot(Snapshot{ID: "cn_0123456789abcdef", Ref: p.Ref, Title: p.Title, Text: text, Sections: []Section{{Level: 1, Title: "一", Offset: 0, Length: 9000}}, Coverage: ContentCoverage{Scope: "unknown"}, Observed: time.Now()})
	v, err := app.Read(context.Background(), ReadArgs{RecordRef: p.Ref})
	out, _ := v.(map[string]any)
	if err != nil || out["reused"] != true || len([]rune(out["text"].(string))) != 8000 || out["next_offset"] != 8000 || out["sections"] == nil {
		t.Fatalf("first page: %v %v", err, out["next_offset"])
	}
	v, _ = app.Read(context.Background(), ReadArgs{SnapshotID: "cn_0123456789abcdef", Offset: 8000})
	if out = v.(map[string]any); len([]rune(out["text"].(string))) != 1000 || out["sections"] != nil || out["next_offset"] != nil {
		t.Fatalf("second page: %+v", out["next_offset"])
	}
	if _, err = app.Read(context.Background(), ReadArgs{SnapshotID: "../x"}); err == nil {
		t.Fatal("unsafe snapshot id accepted")
	}
}

// Transfer leftovers are removed unless a pending download still waits for them.
func TestSweepIncoming(t *testing.T) {
	root := t.TempDir()
	s, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, d := range []string{"incoming/op_old", "incoming/cs_pending"} {
		_ = os.MkdirAll(filepath.Join(root, d), 0700)
		_ = os.WriteFile(filepath.Join(root, d, "part.crdownload"), []byte("x"), 0600)
	}
	_ = os.WriteFile(filepath.Join(root, "incoming", "5ccc928c-loose"), []byte("x"), 0600)
	s.PutArtifact("r:pdf", Artifact{State: "clicked", Incoming: filepath.Join("incoming", "cs_pending")})
	// An old pending record (imported from 2.0) no longer protects its folder.
	_ = saveJSON(s.db, "artifacts", "key", "old:pdf", Artifact{State: "clicked", Incoming: filepath.Join("incoming", "op_old"), Updated: time.Now().Add(-30 * 24 * time.Hour)})
	sweepIncoming(root, s)
	entries, _ := os.ReadDir(filepath.Join(root, "incoming"))
	if len(entries) != 1 || entries[0].Name() != "cs_pending" {
		t.Fatalf("left: %v", entries)
	}
	s.PutArtifact("r:pdf", Artifact{State: "completed"})
	sweepIncoming(root, s)
	if _, err := os.Stat(filepath.Join(root, "incoming")); !os.IsNotExist(err) {
		t.Fatal("empty incoming folder kept")
	}
}

// Search output is columnar, reports the plan as CNKI expressions and pages locally.
func TestSearchOutputShape(t *testing.T) {
	root := t.TempDir()
	s, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	app := &App{Root: root, Store: s, Jobs: NewJobs(s), Site: &Site{}, wait: time.Second}
	rec := &SearchRecord{ID: "cs_0000000000000001", Status: "completed", Plan: Plan{Method: "lexical", Channels: []Channel{{ID: "subject", Weight: 1, Expression: Expression{Op: "and", Items: []Expression{{Field: "subject", Value: "违约金"}, {Op: "or", Items: []Expression{{Field: "subject", Value: "调整"}, {Field: "subject", Value: "酌减"}}}}}}}}}
	for i := range 15 {
		p := s.Upsert([]Paper{{Title: "论文" + string(rune('A'+i)), Authors: []string{"甲", "乙"}, Source: "法学研究", Year: 2020, DBCode: "CJFD", Filename: "F" + string(rune('A'+i))}})[0]
		rec.Entries = append(rec.Entries, Entry{Ref: p.Ref, Selected: true, Eligible: true, Score: 1})
	}
	s.PutSearch(rec, "")
	out := app.searchOutput(rec, summaryOf(s, rec), 10, "")
	b, _ := marshal(out)
	if !strings.Contains(string(b), `"fields":["record_ref","title","authors","source","year"]`) || !strings.Contains(string(b), `"甲; 乙"`) || out["next_offset"] != 10 {
		t.Fatalf("search output: %s", b)
	}
	if e := planView(rec.Plan)["channels"].(map[string]any)["rows"].([][]any)[0][1]; e != "SU='违约金' AND (SU='调整' OR SU='酌减')" {
		t.Fatalf("expression: %v", e)
	}
	v, err := app.Results(context.Background(), ResultsArgs{SearchID: rec.ID, Offset: 10})
	if r := v.(map[string]any); err != nil || len(r["rows"].([][]any)) != 5 || r["next_offset"] != nil {
		t.Fatalf("results: %v %+v", err, r)
	}
}
