package cnki

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func truth(e Expression, values map[string]bool) bool {
	if e.Op == "" {
		return values[e.Value]
	}
	if e.Op == "not" {
		return !truth(e.Items[0], values)
	}
	out := e.Op == "and"
	for _, x := range e.Items {
		if e.Op == "and" {
			out = out && truth(x, values)
		} else {
			out = out || truth(x, values)
		}
	}
	return out
}

func TestBooleanCompilationTruth(t *testing.T) {
	a := Expression{Field: "title", Value: "A"}
	b := Expression{Field: "keywords", Value: "B"}
	c := Expression{Field: "authors", Value: "C"}
	not := func(e Expression) Expression { return Expression{Op: "not", Items: []Expression{e}} }
	for _, e := range []Expression{not(not(a)), combine("and", []Expression{a, combine("or", []Expression{b, not(c)})}), combine("or", []Expression{combine("and", []Expression{a, not(b)}), c}), combine("and", []Expression{a, not(combine("and", []Expression{b, c}))}), combine("and", []Expression{a, not(combine("or", []Expression{b, c}))})} {
		compiled, err := Compile(e)
		if err != nil {
			t.Fatal(err)
		}
		for mask := range 8 {
			values := map[string]bool{"A": mask&1 != 0, "B": mask&2 != 0, "C": mask&4 != 0}
			actual := false
			for _, raw := range compiled["ChildItems"].([]any) {
				branch := true
				for _, item := range raw.(map[string]any)["Items"].([]any) {
					leaf := item.(map[string]any)
					v := values[leaf["Value"].(string)]
					if leaf["Logic"].(int) == 2 {
						v = !v
					}
					branch = branch && v
				}
				actual = actual || branch
			}
			if actual != truth(e, values) {
				t.Fatalf("changed boolean meaning: %+v at %v", e, values)
			}
		}
	}
	if _, err := Compile(combine("or", []Expression{a, not(b)})); err == nil {
		t.Fatal("unbounded negative OR branch accepted")
	}
}

func TestPlanningKeepsWordsConceptsAndVersions(t *testing.T) {
	for _, q := range []string{"初中物理分层作业", "中国中医药现代化", "C++ 内存模型", "中华人民共和国 民法典", "Wi-Fi 8", "IEEE P802.11bn"} {
		p, err := MakePlan(SearchInput{Query: q})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(p.Concepts)
		for _, word := range lexicalParts(q) {
			if !strings.Contains(string(raw), word) {
				t.Fatalf("lost %q: %s", word, raw)
			}
		}
		if len(p.Channels) != 3 {
			t.Fatal("expected subject/title/keywords channels")
		}
		for _, ch := range p.Channels {
			b, _ := json.Marshal(ch.Expression)
			for _, g := range p.Concepts {
				if !strings.Contains(string(b), g[0]) {
					t.Fatalf("channel discarded a concept: %s", b)
				}
			}
		}
	}
	if len(aliases("WiFi 8月上市")) != 1 || len(aliases("Wi-Fi 8")) < 3 {
		t.Fatal("version aliasing wrong")
	}
	for _, alias := range aliases("IEEE P802.11bn吞吐量") {
		if !strings.Contains(alias, "吞吐量") {
			t.Fatal("alias removed surrounding meaning")
		}
	}
}

func TestIdentityAndMerge(t *testing.T) {
	a := Paper{Title: "初中物理作业分层设计研究——以某初中为例", Authors: []string{"张三"}, Year: 2022, Source: "某大学"}
	b := a
	b.Title = "初中物理作业分层设计研究 ——以某初中为例"
	if !SamePaper(a, b) || !detailMatches(a, b) {
		t.Fatal("formatting rejected")
	}
	b.Authors = []string{"李四"}
	if SamePaper(a, b) || detailMatches(a, b) {
		t.Fatal("same title, different author merged")
	}
	for _, pair := range [][2]string{{"C++", "C#"}, {"A B", "AB"}, {"1.2", "12"}} {
		if titleKey(pair[0]) == titleKey(pair[1]) {
			t.Fatal("meaningful punctuation erased")
		}
	}
	c, d := a, a
	c.DOI, d.DOI = "10.1/a", "10.1/b"
	if SamePaper(c, d) {
		t.Fatal("conflicting DOI merged")
	}
	// A listing never erases or overrides detail-page evidence.
	detail := Paper{Ref: "r", Title: "论文", Abstract: "摘要", Keywords: []string{"甲"}, Date: "2023", Year: 2023, DetailAt: time.Now()}
	listing := Paper{Title: "论文", Date: "2022-12-28", Year: 2022, ListedAt: time.Now()}
	m := merge(detail, listing)
	if m.Abstract != "摘要" || len(m.Keywords) != 1 || m.Year != 2023 || m.Ref != "r" {
		t.Fatalf("listing overwrote detail evidence: %+v", m)
	}
	cites := 9
	m = merge(m, Paper{Cited: &cites})
	if m.Cited == nil || *m.Cited != 9 {
		t.Fatal("fresh counts not applied")
	}
}

func samplePapers() ([]Paper, Plan, map[string][]Hit) {
	impact := 3.0
	cites := 15
	p := Plan{Concepts: [][]string{{"自动驾驶"}, {"产品责任"}}}
	q := func(tier string) *Quality {
		return &Quality{Tiers: []string{tier}, CompositeImpact: &impact, State: "present", Parser: ParserVersion}
	}
	items := []Paper{
		{Ref: "related", Title: "自动驾驶产品责任", Abstract: "自动驾驶产品责任", Keywords: []string{"自动驾驶", "产品责任", "风险分配"}, Source: "法学研究", Year: 2025, Type: "期刊", Cited: &cites, Quality: q("CSSCI")},
		{Ref: "irrelevant", Title: "材料制备技术", Abstract: "材料制备研究", Keywords: []string{"材料"}, Source: "测试", Year: 2020, Type: "期刊", Quality: q("SCI")},
	}
	hits := map[string][]Hit{"related": {{Channel: "su", Position: 1}, {Channel: "ti", Position: 1}, {Channel: "ky", Position: 2}}, "irrelevant": {{Channel: "su", Position: 1}}}
	return items, p, hits
}

func TestRankingSelectionAndFeedback(t *testing.T) {
	items, plan, hits := samplePapers()
	r := Rank(items, plan, hits, 2026)
	if r[0].Paper.Ref != "related" || !r[0].Eligible || r[1].Eligible {
		t.Fatalf("irrelevant quality escaped relevance gate: %+v", r)
	}
	if r[0].Evidence.SourceTier != .96 || r[0].Evidence.AnnualCitations <= 0 || r[0].Evidence.Impact <= 0 {
		t.Fatal("quality evidence wrong")
	}
	if got := Select(r, 0); len(got) != 1 {
		t.Fatalf("selection=%+v", got)
	}
	for _, x := range []struct{ n, want int }{{12, 12}, {60, 40}, {300, 60}, {1000, 80}} {
		if got := EnhancementSize(x.n); got != x.want {
			t.Fatalf("pool %d = %d", x.n, got)
		}
	}
	other := items[0]
	other.Ref = "second"
	items = append(items, other)
	hits[other.Ref] = hits["related"]
	feedback := Feedback(Rank(items, plan, hits, 2026), plan)
	raw, _ := json.Marshal(feedback)
	if len(feedback) != 1 || !strings.Contains(string(raw), "风险分配") || !strings.Contains(string(raw), "产品责任") {
		t.Fatalf("feedback wrong: %s", raw)
	}
}

func TestParsers(t *testing.T) {
	html := `<div class="wx-tit"><h1>真实论文</h1><h3 id="authorpart"><span><a>作者甲<sup>1</sup></a></span><span>作者乙</span></h3><h3 class="author"><span><a>某大学法学院</a></span></h3></div><span id="ChDivSummary">真实摘要</span><p>参考文献中 CSSCI</p>`
	p, _, err := ParseDetail(html, "https://kns.cnki.net/kcms2/article/abstract?v=observed")
	if err != nil || p.Title != "真实论文" || p.Abstract != "真实摘要" || len(p.Institutions) != 1 || len(p.Authors) != 2 || p.DetailAt.IsZero() {
		t.Fatalf("detail boundaries: %+v %v", p, err)
	}
	role, _, err := ParseDetail(`<meta name="citation_title" content="题名"><meta name="citation_author" content="林倩(文)"><meta name="citation_author" content="胡馨元（编）">`, "https://kns.cnki.net/kcms2/article/abstract?v=x")
	if err != nil || strings.Join(role.Authors, "|") != "林倩|胡馨元" {
		t.Fatalf("author role suffix kept: %q", role.Authors)
	}
	login, _, err := ParseDetail(`<div class="ecp_personalLoginBox"><h1>自动登录</h1></div><div class="wx-tit"><h1>真实题名</h1></div>`, "https://kns.cnki.net/kcms2/article/abstract?v=x")
	if err != nil || login.Title != "真实题名" {
		t.Fatalf("login widget heading taken as title: %q %v", login.Title, err)
	}
	news, _, err := ParseDetail(`<h1>报纸题名</h1><div>正文快照：<span class="abstract-text">正文片段</span></div>`, "https://kns.cnki.net/kcms2/article/abstract?v=x")
	if err != nil || news.Abstract != "" || news.Excerpt != "正文片段" {
		t.Fatal("snapshot mislabeled as abstract", err)
	}
	if _, err := ParseGrid("changed login page", "https://kns.cnki.net"); err == nil {
		t.Fatal("unsupported page reported zero results")
	}
	grid, err := ParseGrid(`<table class="result-table-list"><tbody><tr><td class="name"><a href="/kcms2/article/abstract?v=1&dbname=CJFD&filename=ABC">论文一</a></td><td class="author"><a>甲</a></td><td class="source">法学研究</td><td class="date">2024-01-02</td><td class="data">期刊</td><td class="quote">1.2万</td></tr></tbody></table><span class="pagerTitleCell">共<em>1</em>条</span>`, "https://kns.cnki.net/kns8s/brief/grid")
	if err != nil || len(grid.Papers) != 1 || !grid.Exhausted || *grid.Papers[0].Cited != 12000 || nativeKey(grid.Papers[0]) != "CJFD:ABC" || grid.Papers[0].URL == "" {
		t.Fatalf("grid: %+v %v", grid, err)
	}
	if _, err := ParseQuality(`<h1>别的期刊</h1><p>法学研究 CSSCI 复合影响因子：4.2</p>`, "法学研究", "https://navi.cnki.net/x"); err == nil {
		t.Fatal("unverified source quality accepted")
	}
	q, err := ParseQuality(`<dl class="journalInfo"><dd class="infobox"><h3 class="titbox">法学评论<p>Law Review</p></h3><p>北大核心 AMI核心 CSSCI</p><p>(2025版)复合影响因子：9.597</p><p>(2025版)综合影响因子：4.913</p></dd></dl><aside>SCI 复合影响因子：99.99</aside>`, "法学评论", "https://navi.cnki.net/x")
	if err != nil || q.MetricYear != 2025 || *q.CompositeImpact != 9.597 || len(q.Tiers) != 3 {
		t.Fatalf("quality scope/year: %+v %v", q, err)
	}
	former := `<div class="journalInfo"><div class="infobox"><h3 class="titbox">开放大学学报<p>Journal of Open University</p></h3><p>曾用刊名：开放大学学报;广播电视大学学报;电大学刊</p><p>AMI入库</p></div></div>`
	for _, name := range []string{"开放大学学报", "广播电视大学学报", "电大学刊", "Journal of Open University"} {
		if q, e := ParseQuality(former, name, ""); e != nil || q.Canonical != "开放大学学报" || len(q.Tiers) != 1 {
			t.Fatalf("former title lost for %s: %v", name, e)
		}
	}
	if _, e := ParseQuality(former, "大学学报", ""); e == nil {
		t.Fatal("substring matched an unrelated journal")
	}
	for _, text := range []string{"Science Education", "EIsevier", "论文引用 SCI 研究成果"} {
		markup := `<div class="journalInfo"><div class="infobox"><h3 class="titbox">测试刊物<p>` + text + `</p></h3><p>` + text + `</p></div></div>`
		if q, e := ParseQuality(markup, "测试刊物", ""); e != nil || len(q.Tiers) != 0 {
			t.Fatalf("false source tag from %q", text)
		}
	}
}

func TestReader(t *testing.T) {
	markup := `<div class="catalogboxs"><span class="tit">第一章</span><span class="tit">第二章</span><span class="tit">参考文献</span></div><div id="XMLContent"><div id="paperRead"><meta name="citation_title" content="测试论文"><h1>第一章</h1><p>` + strings.Repeat("正文", 100) + `</p><h3 id="bibliography">参考文献</h3><p>条目</p></div></div>`
	out, e := ParseReader(markup, "测试论文")
	if e != nil || out.Coverage.Container != "#paperRead" || len(out.Coverage.MissingHeadings) != 1 || out.Coverage.Scope != "partial" {
		t.Fatalf("catalog coverage: %+v %v", out.Coverage, e)
	}
	if out.Sections[1].Level != 1 || out.Sections[0].Length >= len(out.Text) {
		t.Fatal("references attributed to chapter one")
	}
	nested := `<div id="paperRead"><h2>第一章</h2><p>` + strings.Repeat("研究正文", 40) + `</p><h3>第一节</h3><p>子章节内容</p><h2>第二章</h2><p>最后内容</p></div>`
	out, e = ParseReader(nested, "title")
	if e != nil || len(out.Sections) != 3 || !strings.Contains(string(out.Text[out.Sections[0].Offset:out.Sections[0].Offset+out.Sections[0].Length]), "子章节内容") {
		t.Fatal("parent section omitted subsection", e)
	}
	if _, e = ParseReader(`<div id="ChDivSummary">摘要</div>`, "title"); e == nil {
		t.Fatal("summary accepted as body")
	}
	page, _ := sliceDocument(docEntry{Ref: "r", Content: out}, ReadInput{Section: "第一节"})
	if page["text"] != "第一节\n\n子章节内容" {
		t.Fatalf("section slice: %q", page["text"])
	}
}

func TestCitationsLinksAndFiles(t *testing.T) {
	raw, err := os.ReadFile("testdata/citations.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Papers  []Paper                   `json:"papers"`
		Results map[string]CitationResult `json:"results"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for name, want := range fixture.Results {
		if name == "json" {
			continue // raw records follow the record schema
		}
		got, err := Citations(fixture.Papers, name, "{authors}: {title} ({year})")
		if err != nil || got.Text != want.Text {
			t.Fatalf("%s changed: %v\n%s\n%s", name, err, got.Text, want.Text)
		}
	}
	if _, err := Citations(fixture.Papers, "custom", "{authors.__class__}"); err == nil {
		t.Fatal("unsafe template accepted")
	}
	spans := referenceSpans("参考《论《民法典》违约金》，已有[《另一篇》](https://example.test)，代码 `《代码》`。")
	if len(spans) != 1 || spans[0].Title != "论《民法典》违约金" {
		t.Fatalf("spans: %+v", spans)
	}
	root := t.TempDir()
	path, _, _, err := writeArtifact(root, "exports", "cite.txt", strings.NewReader("first"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = writeArtifact(root, "exports", "cite.txt", strings.NewReader("second")); err == nil {
		t.Fatal("export silently overwritten")
	}
	if data, _ := os.ReadFile(path); string(data) != "first" {
		t.Fatal("old bytes changed")
	}
}

func TestSetupConfigPreservesOtherServers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mcp.json")
	if e := os.WriteFile(path, []byte(`{"mcpServers":{"other":{"command":"keep-me"}},"setting":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	text, e := ConfigText("generic", "/a path/cnki-mcp", root)
	if e != nil {
		t.Fatal(e)
	}
	if e = installConfig("generic", path, text); e != nil {
		t.Fatal(e)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "keep-me") || !strings.Contains(string(data), "setting") {
		t.Fatal("other settings removed")
	}
	for _, client := range Clients {
		if text, e := ConfigText(client.ID, "/program/cnki-mcp", root); e != nil || text == "" {
			t.Fatal(client.ID, e)
		}
	}
	// ZCode nests servers under mcp.servers and keeps every sibling setting.
	zc := filepath.Join(t.TempDir(), "config.json")
	_ = os.WriteFile(zc, []byte(`{"theme":"dark","mcp":{"enabled":true,"servers":{"other":{"command":"x"}}}}`), 0600)
	ztext, _ := ConfigText("zcode", "/program/cnki-mcp", root)
	if e := installConfig("zcode", zc, ztext); e != nil {
		t.Fatal(e)
	}
	var z map[string]any
	raw, _ := os.ReadFile(zc)
	_ = json.Unmarshal(raw, &z)
	servers := z["mcp"].(map[string]any)["servers"].(map[string]any)
	if z["theme"] != "dark" || z["mcp"].(map[string]any)["enabled"] != true || servers["other"] == nil || servers["cnki"].(map[string]any)["type"] != "stdio" {
		t.Fatalf("zcode merge lost settings: %s", raw)
	}
	// One-click links decode back to the server entry.
	decode := func(link, param string, twice bool) map[string]any {
		u, _ := url.Parse(link)
		v := u.Query().Get(param)
		data, _ := base64.StdEncoding.DecodeString(v)
		if twice {
			s, _ := url.QueryUnescape(string(data))
			data = []byte(s)
		}
		var out map[string]any
		if json.Unmarshal(data, &out) != nil {
			t.Fatalf("undecodable link %s", link)
		}
		return out
	}
	if e := decode(ImportLink("trae", "/p/cnki-mcp", root), "config", false); e["command"] != "/p/cnki-mcp" {
		t.Fatal("trae link", e)
	}
	if e := decode(ImportLink("qoder", "/p/cnki-mcp", root), "config", true); e["command"] != "/p/cnki-mcp" {
		t.Fatal("qoder link", e)
	}
	if e := decode(ImportLink("chatbox", "/p/cnki-mcp", root), "server", false); e["name"] != "cnki" || e["command"] != "/p/cnki-mcp" {
		t.Fatal("chatbox link", e)
	}
	if e := decode(ImportLink("cherry-studio", "/p/cnki-mcp", root), "servers", false); e["mcpServers"] == nil {
		t.Fatal("cherry link", e)
	}
	// A DeepSeek Harness profile that was never started is not created by us.
	missing := filepath.Join(t.TempDir(), "profiles", "web", "cordis.patch.yml")
	if e := installConfig("dsh-web", missing, "x"); e == nil || asProblem(e).Code != "CLIENT_NOT_INITIALIZED" {
		t.Fatal("wrote into an uninitialized DSH profile", e)
	}
	// DeepSeek Harness: append to an existing patch layer, replace an empty "[]" one, never duplicate.
	for _, start := range []string{"[]\n", "- insert:\n    - id: other\n      name: x\n"} {
		patch := filepath.Join(t.TempDir(), "cordis.patch.yml")
		_ = os.WriteFile(patch, []byte(start), 0600)
		text, _ := ConfigText("dsh-desktop", "/program/cnki-mcp", root)
		if e := installConfig("dsh-desktop", patch, text); e != nil {
			t.Fatal(e)
		}
		if e := installConfig("dsh-desktop", patch, text); e != nil {
			t.Fatal("second install not idempotent", e)
		}
		got, _ := os.ReadFile(patch)
		if strings.Count(string(got), "id: mcp-cnki") != 1 || strings.Contains(string(got), "[]") || start != "[]\n" && !strings.Contains(string(got), "id: other") {
			t.Fatalf("patch layer wrong:\n%s", got)
		}
	}
}
