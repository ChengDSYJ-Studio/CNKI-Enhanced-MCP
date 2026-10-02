package cnki

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeKNS imitates the KNS pages and APIs the site layer relies on.
type fakeKNS struct {
	mu        sync.Mutex
	grids     int
	pageSizes map[string]int
	details   int
	journals  int
	files     int
	inflight  atomic.Int32
	peak      atomic.Int32
}

func (f *fakeKNS) busy() func() {
	n := f.inflight.Add(1)
	for p := f.peak.Load(); n > p && !f.peak.CompareAndSwap(p, n); p = f.peak.Load() {
	}
	time.Sleep(120 * time.Millisecond)
	return func() { f.inflight.Add(-1) }
}
func (f *fakeKNS) handler(t *testing.T) http.Handler {
	html := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, body)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/kns8s/AdvSearch", func(w http.ResponseWriter, r *http.Request) {
		html(w, `<html><head><title>高级检索</title></head><body><div class="ecp_header_login_area">登录 注册</div>
<div id="gradetxt"><input data-tipid="1" style="width:200px"></div>
<input type="button" class="btn-search" value="检索" style="width:80px;height:30px" onclick="go()"><div id="briefBox"></div>
<script>window.cnkiSearch={getSearchJsonInfo(){return {}}};
function go(){const q={QNode:{QGroup:[{Key:'Subject',Title:'',Logic:0,Items:[],ChildItems:[]},{Key:'ControlGroup',Title:'',Logic:0,Items:[],ChildItems:[]}]}};
fetch('/kns8s/brief/grid',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:'boolSearch=true&QueryJson='+encodeURIComponent(JSON.stringify(q))+'&pageNum=1&pageSize=20&CurPage=1&dstyle=listmode'});}</script></body></html>`)
	})
	mux.HandleFunc("/kns8s/brief/grid", func(w http.ResponseWriter, r *http.Request) {
		defer f.busy()()
		_ = r.ParseForm()
		var q map[string]any
		_ = json.Unmarshal([]byte(r.Form.Get("QueryJson")), &q)
		values := []string{}
		for _, g := range q["QNode"].(map[string]any)["QGroup"].([]any) {
			group := g.(map[string]any)
			if group["Key"] != "Subject" {
				continue
			}
			for _, c := range group["ChildItems"].([]any) {
				for _, item := range c.(map[string]any)["Items"].([]any) {
					values = append(values, strings.Trim(item.(map[string]any)["Value"].(string), `"'`))
				}
			}
		}
		page, _ := strconv.Atoi(r.Form.Get("pageNum"))
		size, _ := strconv.Atoi(r.Form.Get("pageSize"))
		f.mu.Lock()
		f.grids++
		f.pageSizes[r.Form.Get("pageSize")]++
		f.mu.Unlock()
		rows := []int{}
		for _, v := range values {
			if n, err := strconv.Atoi(strings.TrimPrefix(v, "违约金研究 ")); err == nil {
				rows = append(rows, n) // exact title lookup
			}
		}
		total := len(rows)
		if len(rows) == 0 {
			total = 60
			if page > 1 && r.Form.Get("turnpage") != "cursor" {
				http.Error(w, "missing cursor", 400)
				return
			}
			for i := (page - 1) * size; i < min(total, page*size); i++ {
				rows = append(rows, i)
			}
		}
		var b strings.Builder
		b.WriteString(`<table class="result-table-list"><tbody>`)
		for _, i := range rows {
			fmt.Fprintf(&b, `<tr><td class="name"><a href="/detail?i=%d">违约金研究 %d</a><i data-dbname="CJFD" data-filename="F%d"></i></td><td class="author"><a>作者%d</a></td><td class="source">法学期刊%d</td><td class="date">2024-03-01</td><td class="data">期刊</td><td class="quote">%d</td></tr>`, i, i, i, i, i%3, i)
		}
		fmt.Fprintf(&b, `</tbody></table><div class="pagerTitleCell">共<em>%d</em>条</div><span class="countPageMark">%d/%d</span><input id="hidTurnPage" value="cursor">`, total, page, (total+size-1)/max(size, 1))
		html(w, b.String())
	})
	mux.HandleFunc("/detail", func(w http.ResponseWriter, r *http.Request) {
		defer f.busy()()
		i, _ := strconv.Atoi(r.URL.Query().Get("i"))
		f.mu.Lock()
		f.details++
		f.mu.Unlock()
		body := fmt.Sprintf(`<div class="wx-tit"><h1>违约金研究 %d</h1><h3 id="authorpart"><span><a>作者%d</a></span></h3></div><div class="top-tip"><span class="source"><a href="/journal?s=%d">法学期刊%d</a></span> 2024,(3):1-10</div><span id="ChDivSummary">摘要：违约金的调整规则 %d</span><p class="keywords">关键词：违约金;损害赔偿;合同法</p><a href="/file?i=%d">PDF下载</a> <a href="/reader?i=%d">HTML阅读</a>`, i, i, i%3, i%3, i, i, i)
		if i == 13 { // rendered by script: the fetched HTML is unusable, the tab is not
			html(w, `<div id="root"></div><script>setTimeout(()=>{document.getElementById('root').innerHTML=`+strconv.Quote(body)+`},200)</script>`)
			return
		}
		html(w, `<meta name="citation_title" content="违约金研究 `+strconv.Itoa(i)+`">`+body)
	})
	mux.HandleFunc("/journal", func(w http.ResponseWriter, r *http.Request) {
		defer f.busy()()
		f.mu.Lock()
		f.journals++
		f.mu.Unlock()
		html(w, `<div class="journalInfo"><div class="infobox"><h3 class="titbox">法学期刊`+r.URL.Query().Get("s")+`</h3><p>CSSCI</p><p>(2025版)复合影响因子：3.1</p></div></div>`)
	})
	mux.HandleFunc("/reader", func(w http.ResponseWriter, r *http.Request) {
		i := r.URL.Query().Get("i")
		html(w, `<meta name="citation_title" content="违约金研究 `+i+`"><div class="catalog"><a href="#a">一、问题</a><a href="#b">二、规则</a></div><div id="paperRead"><h2>一、问题</h2><p>`+strings.Repeat("违约金调整", 40)+`</p><h2>二、规则</h2><p>结论</p></div>`)
	})
	payload := []byte("%PDF-1.4\n" + strings.Repeat("test data\n", 300) + "%%EOF\n")
	mux.HandleFunc("/file", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.files++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", "attachment; filename=paper.pdf")
		_, _ = w.Write(payload)
	})
	return mux
}

func TestSiteAgainstLocalKNS(t *testing.T) {
	executable := os.Getenv("CNKI_TEST_BROWSER")
	if executable == "" {
		t.Skip("set CNKI_TEST_BROWSER to a Chrome/Chromium executable")
	}
	fake := &fakeKNS{pageSizes: map[string]int{}}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	restoreURL, restoreSite := advancedURL, siteURL
	advancedURL = server.URL + "/kns8s/AdvSearch"
	siteURL = func(u *url.URL) bool { return u.Host == host }
	defer func() { advancedURL, siteURL = restoreURL, restoreSite }()

	root := t.TempDir()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	browser := NewBrowser(filepath.Join(root, "browser"), executable, true)
	defer browser.Close()
	site := NewSite(browser, store)
	site.limit = NewLimiter(40*time.Millisecond, 6)
	jobs := NewJobs(store)

	// Deep search: template capture, concurrent channels, larger pages, cursors,
	// concurrent detail/journal loads, render fallback and journal dedup.
	in := SearchInput{Query: "违约金", Concepts: [][]string{{"违约金"}}, Options: Options{MaxPages: 2, CandidateTarget: 1000, Metadata: "all"}}
	if err = in.Validate(); err != nil {
		t.Fatal(err)
	}
	plan, _ := MakePlan(in)
	st := &searchState{Input: in, Plan: plan}
	began := time.Now()
	op := jobs.Start("search", budget(in.Options), nil, func(op *Op) (any, error) {
		st.SearchID = op.ID()
		return (&Researcher{Store: store, Site: site, Workers: 8}).Run(op, st)
	})
	rec := jobs.Wait(context.Background(), op, 2*time.Minute)
	var sum SearchSummary
	_ = json.Unmarshal(rec.Result, &sum)
	t.Logf("search: %s requests=%d %.2fs grids=%d pageSizes=%v details=%d journals=%d peak=%d", rec.Status, rec.Requests, time.Since(began).Seconds(), fake.grids, fake.pageSizes, fake.details, fake.journals, fake.peak.Load())
	if rec.Status != "completed" || sum.Candidates != 60 || sum.Enhanced != 60 || len(sum.Coverage) != 5 {
		t.Fatalf("search: %+v errors=%+v", sum, rec.Errors)
	}
	if fake.pageSizes["50"] == 0 || fake.journals != 3 || fake.peak.Load() < 2 {
		t.Fatalf("expected 50-row pages, 3 journals and overlap: %+v", fake)
	}
	for _, ref := range st.Refs {
		if p, _ := store.Paper(ref); p.Title == "违约金研究 13" && (p.Abstract == "" || p.Quality == nil) {
			t.Fatalf("render fallback did not enrich: %+v", p)
		}
	}

	// Batched title lookup, HTML reader and a single-click download.
	app := &App{Root: root, Store: store, Jobs: jobs, Site: site, docs: docCache{entries: map[string]docEntry{}}, wait: time.Minute}
	before := fake.grids
	result := jobs.Wait(context.Background(), jobs.Start("metadata", Budget{MaxRequests: 50}, nil, func(op *Op) (any, error) {
		store.mu.Lock()
		store.data.Papers, store.index = map[string]*Paper{}, map[string][]string{} // force online lookup
		store.mu.Unlock()
		return app.papers(op, nil, []string{"违约金研究 3", "违约金研究 4", "违约金研究 5"})
	}), time.Minute)
	var found []Paper
	_ = json.Unmarshal(result.Result, &found)
	if len(found) != 3 || fake.grids-before != 1 {
		t.Fatalf("batched locate: %d papers, %d searches, %+v", len(found), fake.grids-before, result.Errors)
	}
	result = jobs.Wait(context.Background(), jobs.Start("read", Budget{MaxRequests: 20}, nil, func(op *Op) (any, error) {
		c, err := site.Read(op, found[0])
		return map[string]any{"sections": len(c.Sections), "scope": c.Coverage.Scope}, err
	}), time.Minute)
	if result.Status != "completed" || !strings.Contains(string(result.Result), `"sections":2`) {
		t.Fatalf("reader: %+v %s", result, result.Result)
	}
	result = jobs.Wait(context.Background(), jobs.Start("download", Budget{MaxRequests: 20}, nil, func(op *Op) (any, error) {
		return site.Download(op, root, found[0], DownloadInput{Format: "auto", WaitSeconds: 20})
	}), time.Minute)
	var art Artifact
	_ = json.Unmarshal(result.Result, &art)
	if result.Status != "completed" || art.State != "completed" || art.Bytes == 0 || fake.files != 1 {
		t.Fatalf("download: %+v %+v", result, art)
	}
	if data, err := os.ReadFile(art.Path); err != nil || !strings.HasPrefix(string(data), "%PDF-") {
		t.Fatal("archived file missing", err)
	}
}
