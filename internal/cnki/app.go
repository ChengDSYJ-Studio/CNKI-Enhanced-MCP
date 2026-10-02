package cnki

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

const searchCacheTTL = 10 * time.Minute

type Config struct {
	BrowserPath string `json:"browser_path"`
}

// App wires the store, operations and site. Nothing touches disk or Chrome
// until a tool needs it, so discovery and diagnostics stay instant.
type App struct {
	Root   string
	Config Config
	mu     sync.Mutex
	Store  *Store
	Jobs   *Jobs
	Site   *Site
	docs   docCache
	wait   time.Duration
}

func NewApp(root string) (*App, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	a := &App{Root: root, wait: time.Duration(envInt("CNKI_INLINE_WAIT_SECONDS", 40)) * time.Second, docs: docCache{entries: map[string]docEntry{}}}
	if data, e := os.ReadFile(filepath.Join(root, "config.json")); e == nil {
		if json.Unmarshal(data, &a.Config) != nil {
			return nil, fail("CONFIG_INVALID", "配置文件无法解析")
		}
	}
	if path := os.Getenv("CNKI_BROWSER_PATH"); path != "" {
		a.Config.BrowserPath = path
	}
	return a, nil
}
func (a *App) init() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Store != nil {
		return nil
	}
	s, err := OpenStore(a.Root)
	if err != nil {
		return err
	}
	a.Store, a.Jobs = s, NewJobs(s)
	browserRoot := filepath.Join(a.Root, "browser")
	// 4.x kept the browser identity under profiles/default; keep users' logins.
	if _, err := os.Stat(browserRoot); os.IsNotExist(err) {
		_ = os.Rename(filepath.Join(a.Root, "profiles", "default"), browserRoot)
	}
	a.Site = NewSite(NewBrowser(browserRoot, a.Config.BrowserPath, false), s)
	return nil
}
func (a *App) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Store == nil {
		return nil
	}
	a.Jobs.Close()
	_ = a.Site.browser.Close()
	return a.Store.Close()
}

// run starts a network operation and waits inline for a while; long work keeps
// running and is followed with operation_status.
func (a *App) run(ctx context.Context, kind string, b Budget, prior *OpRecord, fn func(*Op) (any, error)) (any, error) {
	if err := a.init(); err != nil {
		return nil, err
	}
	op := a.Jobs.Start(kind, b, prior, fn)
	return a.Jobs.Wait(ctx, op, a.wait), nil
}
func budget(o Options) Budget {
	return Budget{MaxRequests: o.MaxRequests, TimeoutSeconds: o.TimeoutSeconds, OnVerification: o.OnVerification}
}
func (a *App) researcher() *Researcher {
	return &Researcher{Store: a.Store, Site: a.Site, Workers: envInt("CNKI_WORKERS", 8)}
}

func (a *App) Search(ctx context.Context, in SearchInput, plan *Plan) (any, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	if plan == nil {
		p, err := MakePlan(in)
		if err != nil {
			return nil, err
		}
		plan = &p
	}
	if err := a.init(); err != nil {
		return nil, err
	}
	o := in.Options
	key := fingerprint([]any{in.Query, in.Mode, in.YearFrom, in.YearTo, o.CandidateTarget, o.MaxPages, o.Metadata, o.Sort, plan.Channels, ParserVersion})
	if o.Freshness == "prefer_cache" {
		if rec, ok := a.Store.CachedSearch(key); ok {
			return map[string]any{"status": "completed", "source": "cache", "requests": 0, "result": summarize(a.Store, rec, in.Limit)}, nil
		}
	}
	st := &searchState{CacheKey: key, Input: in, Plan: *plan}
	return a.run(ctx, "search", budget(o), nil, func(op *Op) (any, error) {
		st.SearchID = op.ID()
		return a.researcher().Run(op, st)
	})
}
func summarize(s *Store, rec *SearchRecord, limit int) SearchSummary {
	out := SearchSummary{SearchID: rec.ID, Status: rec.Status, Coverage: rec.Coverage, Plan: rec.Plan, Candidates: len(rec.Entries), Results: []TitleResult{}}
	if limit == 0 {
		limit = 50
	}
	refs := []string{}
	for _, e := range rec.Entries {
		if e.Eligible {
			out.Eligible++
		}
		if e.Selected {
			out.Selected++
			if len(refs) < limit {
				refs = append(refs, e.Ref)
			}
		}
	}
	for _, p := range s.Papers(refs) {
		out.Results = append(out.Results, TitleResult{Ref: p.Ref, Title: p.Title, URL: p.URL})
	}
	return out
}

type StructuredInput struct {
	Expression map[string]any `json:"expression" jsonschema:"布尔表达式。叶子 {field,value,match}：field 为 subject、title、keywords、abstract、authors、first_author、corresponding_author、institutions、source、doi、funds、fulltext、references、classification、subtitle、title_keywords_abstract；match 为 phrase（默认）或 exact。组合 {op:and|or|not, items:[...]}。例：{op:and,items:[{field:title,value:违约金},{field:authors,value:王利明}]}"`
	Limit      int            `json:"limit,omitempty" jsonschema:"返回条数 1–100，默认 50；同时作为默认抓取范围"`
	YearFrom   int            `json:"year_from,omitempty"`
	YearTo     int            `json:"year_to,omitempty"`
	Sort       string         `json:"sort,omitempty" jsonschema:"relevance（默认）、date_desc、cited_desc、downloaded_desc"`
	Options    Options        `json:"options,omitzero"`
}

func (a *App) Structured(ctx context.Context, in StructuredInput) (any, error) {
	raw, _ := json.Marshal(in.Expression)
	var expr Expression
	if err := json.Unmarshal(raw, &expr); err != nil {
		return nil, fail("INVALID_EXPRESSION", "expression 结构无效")
	}
	if _, err := Compile(expr); err != nil {
		return nil, err
	}
	o := in.Options
	if o.Sort == "" {
		o.Sort = in.Sort
	}
	if o.Metadata == "" {
		o.Metadata = "basic"
	}
	if o.CandidateTarget == 0 {
		o.CandidateTarget = max(in.Limit, 1)
		if in.Limit == 0 {
			o.CandidateTarget = 50
		}
	}
	req := SearchInput{Query: "structured:" + fingerprint(expr), Mode: "precise", Limit: in.Limit, YearFrom: in.YearFrom, YearTo: in.YearTo, Options: o}
	plan := Plan{Method: "structured", Channels: []Channel{{ID: "native", Expression: expr, Weight: 1, Reason: "知网原生条件与排序"}}}
	return a.Search(ctx, req, &plan)
}

type ResultsInput struct {
	SearchID string `json:"search_id"`
	Scope    string `json:"scope,omitempty" jsonschema:"selected（默认）、eligible、candidates"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	View     string `json:"view,omitempty" jsonschema:"titles（默认）、compact（含摘要与评分）、records（完整题录）"`
}

func (a *App) Results(in ResultsInput) (any, error) {
	if err := a.init(); err != nil {
		return nil, err
	}
	if in.Scope == "" {
		in.Scope = "selected"
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	if !oneOf(in.Scope, "selected", "eligible", "candidates") || !oneOf(in.View, "", "titles", "compact", "records") || in.Offset < 0 || in.Limit < 1 || in.Limit > 100 {
		return nil, fail("INVALID_INPUT", "scope/view/offset/limit 无效")
	}
	rec, ok := a.Store.Search(in.SearchID)
	if !ok {
		return nil, fail("SEARCH_NOT_FOUND", "检索结果不存在")
	}
	entries := []Entry{}
	for _, e := range rec.Entries {
		if in.Scope == "candidates" || in.Scope == "eligible" && e.Eligible || in.Scope == "selected" && e.Selected {
			entries = append(entries, e)
		}
	}
	total := len(entries)
	entries = entries[min(in.Offset, total):min(in.Offset+in.Limit, total)]
	refs := make([]string, len(entries))
	for i, e := range entries {
		refs[i] = e.Ref
	}
	papers := a.Store.Papers(refs)
	rows := make([]any, 0, len(papers))
	for i, p := range papers {
		switch in.View {
		case "records":
			rows = append(rows, map[string]any{"record": p, "score": entries[i].Score, "eligible": entries[i].Eligible, "evidence": entries[i].Evidence})
		case "compact":
			rows = append(rows, map[string]any{"record_ref": p.Ref, "title": p.Title, "authors": p.Authors, "year": p.Year, "source": p.Source, "abstract": p.Abstract, "score": entries[i].Score, "eligible": entries[i].Eligible})
		default:
			rows = append(rows, TitleResult{Ref: p.Ref, Title: p.Title, URL: p.URL})
		}
	}
	var next any
	if in.Offset+len(entries) < total {
		next = in.Offset + len(entries)
	}
	return map[string]any{"search_id": rec.ID, "status": rec.Status, "scope": in.Scope, "total": total, "offset": in.Offset, "next_offset": next, "results": rows}, nil
}

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

type MetadataInput struct {
	Refs      []string `json:"record_refs,omitempty"`
	Titles    []string `json:"titles,omitempty" jsonschema:"完整题名，按题名精确定位"`
	SearchID  string   `json:"search_id,omitempty"`
	Scope     string   `json:"scope,omitempty"`
	Offset    int      `json:"offset,omitempty"`
	Limit     int      `json:"limit,omitempty"`
	Fields    []string `json:"fields,omitempty" jsonschema:"只返回这些字段（如 abstract、keywords、doi、quality）；省略返回全部"`
	Quality   bool     `json:"quality,omitempty" jsonschema:"同时采集来源期刊收录与影响因子"`
	Refresh   bool     `json:"refresh,omitempty" jsonschema:"忽略缓存重新读取详情页"`
	LocalOnly bool     `json:"local_only,omitempty" jsonschema:"只读本地已有数据，不联网"`
}

func project(p Paper, fields []string) map[string]any {
	data, _ := json.Marshal(p)
	out := map[string]any{}
	_ = json.Unmarshal(data, &out)
	if len(fields) == 0 {
		return out
	}
	result := map[string]any{"record_ref": p.Ref, "title": p.Title}
	for _, f := range fields {
		if v, ok := out[f]; ok {
			result[f] = v
		}
	}
	return result
}
func (a *App) Metadata(ctx context.Context, in MetadataInput) (any, error) {
	if err := a.init(); err != nil {
		return nil, err
	}
	if len(in.Refs)+len(in.Titles) > 100 || in.Limit > 100 {
		return nil, fail("BATCH_TOO_LARGE", "单次最多 100 篇")
	}
	refs := in.Refs
	if in.SearchID != "" {
		limit := in.Limit
		if limit == 0 {
			limit = 50
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
		return map[string]any{"status": "completed", "source": "local", "result": output(papers)}, nil
	}
	return a.run(ctx, "metadata", Budget{MaxRequests: 600, TimeoutSeconds: 900}, nil, func(op *Op) (any, error) {
		papers, err := a.papers(op, refs, in.Titles)
		if err != nil {
			return output(papers), err
		}
		err = parallel(op, len(papers), 8, "metadata", func(sub *Op, i int) error {
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

type ExportInput struct {
	Refs     []string `json:"record_refs,omitempty"`
	Titles   []string `json:"titles,omitempty"`
	SearchID string   `json:"search_id,omitempty"`
	Scope    string   `json:"scope,omitempty"`
	Format   string   `json:"format,omitempty" jsonschema:"gbt7714（默认）、apa、mla、chicago、vancouver、bibtex、ris、endnote、csl_json、json、csv、markdown、custom"`
	Template string   `json:"template,omitempty" jsonschema:"custom 格式的模板，如 {authors}. {title}[J]. {journal}, {year}"`
	Filename string   `json:"filename,omitempty" jsonschema:"保存到数据目录 exports/ 下的文件名；不覆盖"`
}

func (a *App) Export(ctx context.Context, in ExportInput) (any, error) {
	if err := a.init(); err != nil {
		return nil, err
	}
	if in.Filename != "" {
		if err := safeName(in.Filename); err != nil {
			return nil, err
		}
	}
	refs := in.Refs
	if in.SearchID != "" {
		more, err := a.searchRefs(in.SearchID, in.Scope, 0, 1000)
		if err != nil {
			return nil, err
		}
		refs = append(refs, more...)
	}
	format := func(papers []Paper) (any, error) {
		if len(papers) == 0 {
			return nil, fail("INVALID_INPUT", "没有可导出的文献")
		}
		result, err := Citations(papers, in.Format, in.Template)
		if err != nil {
			return nil, err
		}
		if in.Filename != "" {
			path, _, _, e := writeArtifact(a.Root, "exports", in.Filename, strings.NewReader(result.Text))
			if e != nil {
				return nil, e
			}
			result.Path = path
		}
		if len(result.Text) > 512<<10 {
			if in.Filename == "" {
				return nil, fail("OUTPUT_TOO_LARGE", "引文超过单次输出上限，请指定 filename")
			}
			result.Text = ""
		}
		return map[string]any{"status": "completed", "data": result}, nil
	}
	if len(in.Titles) == 0 {
		return format(a.Store.Papers(uniqueStrings(refs)))
	}
	if len(in.Titles) > 100 {
		return nil, fail("BATCH_TOO_LARGE", "单次最多 100 个题名")
	}
	return a.run(ctx, "export", Budget{MaxRequests: 300, TimeoutSeconds: 900}, nil, func(op *Op) (any, error) {
		papers, err := a.papers(op, refs, in.Titles)
		if err != nil {
			return nil, err
		}
		_ = parallel(op, len(papers), 8, "metadata", func(sub *Op, i int) error {
			fresh, err := enhance(sub, a.Site, a.Store, papers[i], false)
			papers[i] = fresh
			return err
		})
		return format(papers)
	})
}

type LinkInput struct {
	Text          string `json:"text" jsonschema:"含《完整论文题名》的文本"`
	LookupMissing bool   `json:"lookup_missing,omitempty" jsonschema:"本地没有的题名联网定位"`
}

func (a *App) Links(ctx context.Context, in LinkInput) (any, error) {
	if len(in.Text) > 512<<10 {
		return nil, fail("INPUT_TOO_LARGE", "文本超过 512 KiB")
	}
	if err := a.init(); err != nil {
		return nil, err
	}
	spans := referenceSpans(in.Text)
	if len(spans) > 100 {
		return nil, fail("TOO_MANY_REFERENCES", "一次最多处理 100 个题名")
	}
	missing := []string{}
	for _, t := range uniqueStrings(spanTitles(spans)) {
		if len(a.Store.ByTitle(t)) == 0 {
			missing = append(missing, t)
		}
	}
	if !in.LookupMissing || len(missing) == 0 {
		return renderLinks(a.Store, in.Text, spans), nil
	}
	return a.run(ctx, "links", Budget{MaxRequests: 200, TimeoutSeconds: 900}, nil, func(op *Op) (any, error) {
		_, err := a.papers(op, nil, missing)
		return renderLinks(a.Store, in.Text, spans), err
	})
}

type StatusInput struct {
	ID          string `json:"operation_id,omitempty" jsonschema:"省略时返回当前活动任务"`
	WaitSeconds int    `json:"wait_seconds,omitempty" jsonschema:"最多等待秒数（0–55），任务完成或需要人工时立即返回"`
}

func (a *App) Status(ctx context.Context, in StatusInput) (any, error) {
	if err := a.init(); err != nil {
		return nil, err
	}
	return a.Jobs.Status(ctx, in.ID, time.Duration(min(max(in.WaitSeconds, 0), 55))*time.Second)
}

type ControlInput struct {
	ID                 string `json:"operation_id,omitempty"`
	Action             string `json:"action" jsonschema:"cancel 取消；continue 继续中断的检索；resume 人工步骤已完成；show 重新显示人工窗口；recover 重启浏览器"`
	AdditionalRequests int    `json:"additional_requests,omitempty"`
	AdditionalSeconds  int    `json:"additional_seconds,omitempty"`
}

func (a *App) Control(ctx context.Context, in ControlInput) (any, error) {
	if err := a.init(); err != nil {
		return nil, err
	}
	switch in.Action {
	case "cancel":
		return a.Jobs.Cancel(ctx, in.ID)
	case "resume":
		if err := a.Jobs.Resume(in.ID); err != nil {
			return nil, err
		}
		return a.Jobs.Status(ctx, in.ID, 3*time.Second)
	case "show":
		page := a.Site.HumanPage(in.ID)
		if page == nil {
			return nil, fail("VERIFICATION_NOT_FOUND", "没有等待人工处理的页面")
		}
		return map[string]any{"status": "shown"}, a.Site.browser.Window(ctx, page, "normal", true)
	case "recover":
		a.Site.recover()
		a.Site.browser.Reset()
		return map[string]any{"status": "completed", "message": "浏览器将在下一次请求时重新启动"}, nil
	case "continue":
	default:
		return nil, fail("INVALID_ACTION", "action 为 cancel、continue、resume、show 或 recover")
	}
	if _, running := a.Jobs.Active(in.ID); running {
		return nil, fail("OPERATION_BUSY", "任务仍在运行")
	}
	prior, ok := a.Store.Op(in.ID)
	if !ok || prior.Kind != "search" || len(prior.Checkpoint) == 0 {
		return nil, fail("CONTINUE_UNAVAILABLE", "只有中断的检索可以继续；其他任务请重新调用原工具")
	}
	if prior.Status == "completed" {
		return nil, fail("OPERATION_STATE", "任务已经完成")
	}
	var st searchState
	if err := json.Unmarshal(prior.Checkpoint, &st); err != nil {
		return nil, err
	}
	b := prior.Budget
	b.MaxRequests += max(in.AdditionalRequests, 0)
	b.TimeoutSeconds += max(in.AdditionalSeconds, 0)
	if in.AdditionalRequests == 0 && prior.Requests >= b.MaxRequests {
		b.MaxRequests = prior.Requests + 200
	}
	if in.AdditionalSeconds == 0 && prior.ActiveSeconds >= float64(b.TimeoutSeconds) {
		b.TimeoutSeconds = int(prior.ActiveSeconds) + 300
	}
	return a.run(ctx, "search", b, prior, func(op *Op) (any, error) { return a.researcher().Run(op, &st) })
}

type LoginInput struct {
	URL   string `json:"remote_access_url,omitempty" jsonschema:"机构远程访问入口（HTTPS）；省略使用知网首页登录"`
	Force bool   `json:"force,omitempty" jsonschema:"即使已观察到身份也打开登录窗口"`
}

func (a *App) Login(ctx context.Context, in LoginInput) (any, error) {
	entry := advancedURL
	if in.URL != "" {
		if !strings.HasPrefix(in.URL, "https://") || strings.Contains(strings.SplitN(in.URL[8:], "/", 2)[0], "@") {
			return nil, fail("INVALID_LOGIN_URL", "登录入口须为不含凭据的 HTTPS URL")
		}
		entry = in.URL
	}
	return a.run(ctx, "login", Budget{MaxRequests: 20, TimeoutSeconds: 1800}, nil, func(op *Op) (any, error) {
		s := a.Site
		page, err := s.browser.NewPage(op.ctx)
		if err != nil {
			return nil, err
		}
		defer closePage(page)
		if err = s.open(op, page, entry, 20*time.Second, func(pageGate) (bool, error) { return true, nil }); err != nil {
			return nil, err
		}
		if in.URL == "" {
			g, err := s.waitIdentity(op, page)
			if err != nil {
				return nil, err
			}
			if !in.Force && (g.Personal || len(g.Institutions) > 0) {
				return s.rememberIdentity(g), nil
			}
		}
		// The person logs in directly in the browser; credentials never pass through MCP.
		if err = s.browser.Window(op.ctx, page, "normal", true); err != nil {
			return nil, err
		}
		op.awaitUser("请在弹出的浏览器窗口完成登录；登录成功后自动继续（机构远程入口完成后调用 operation_control resume）")
		resume := op.resumeSignal()
		deadline := time.Now().Add(20 * time.Minute)
		var g pageGate
		for found := false; !found; {
			if time.Now().After(deadline) {
				return nil, &Problem{Code: "LOGIN_TIMEOUT", Message: "等待登录超时", Retryable: true}
			}
			select {
			case <-op.ctx.Done():
				return nil, op.ctx.Err()
			case <-resume:
				found = true
			case <-time.After(1500 * time.Millisecond):
				g, found = s.identityAnywhere(op)
			}
		}
		op.userDone()
		_ = s.browser.Window(op.ctx, page, "minimized", false)
		// Confirm on a freshly loaded KNS page, where searches and downloads run.
		if err = s.open(op, page, advancedURL, 20*time.Second, func(pageGate) (bool, error) { return true, nil }); err == nil {
			if confirmed, e := s.waitIdentity(op, page); e == nil && (confirmed.Personal || len(confirmed.Institutions) > 0) {
				g = confirmed
			}
		}
		return s.rememberIdentity(g), nil
	})
}
func (a *App) Session(ctx context.Context, refresh bool) (any, error) {
	if err := a.init(); err != nil {
		return nil, err
	}
	if !refresh {
		a.Site.mu.Lock()
		defer a.Site.mu.Unlock()
		if a.Site.identity == nil {
			return map[string]any{"personal": "not_observed", "institution": "not_observed", "evidence_source": "not_checked"}, nil
		}
		out := map[string]any{"cached": true}
		for k, v := range a.Site.identity {
			out[k] = v
		}
		return out, nil
	}
	return a.run(ctx, "session", Budget{MaxRequests: 10, TimeoutSeconds: 60}, nil, func(op *Op) (any, error) {
		page, err := a.Site.browser.NewPage(op.ctx)
		if err != nil {
			return nil, err
		}
		defer closePage(page)
		if err = a.Site.open(op, page, advancedURL, 20*time.Second, func(pageGate) (bool, error) { return true, nil }); err != nil {
			return nil, err
		}
		g, err := a.Site.waitIdentity(op, page)
		if err != nil {
			return nil, err
		}
		return a.Site.rememberIdentity(g), nil
	})
}

func (a *App) Diagnostics() map[string]any {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	out := map[string]any{"status": "completed", "version": Version, "runtime": runtime.Version(), "platform": runtime.GOOS + "/" + runtime.GOARCH, "browser_configured": a.Config.BrowserPath != "", "data_dir": a.Root, "process_id": os.Getpid(), "heap_bytes": memory.HeapAlloc,
		"request_interval_ms": envInt("CNKI_REQUEST_INTERVAL_MS", 500), "max_inflight": envInt("CNKI_MAX_INFLIGHT", 6), "inline_wait_seconds": int(a.wait.Seconds())}
	a.mu.Lock()
	store, site, jobs := a.Store, a.Site, a.Jobs
	a.mu.Unlock()
	if store != nil {
		out["store"] = store.Stats()
		out["browser"] = site.browser.State()
		if op, ok := jobs.Active(""); ok {
			out["active_operation"] = op.view()
		}
	}
	return out
}
