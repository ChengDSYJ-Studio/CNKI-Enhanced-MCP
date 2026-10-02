package cnki

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

var advancedURL = "https://kns.cnki.net/kns8s/AdvSearch"

const gridPageSize = "50"

// Site performs every CNKI interaction through one shared rate limiter. Reads
// run concurrently; human verification pauses all of them and resumes them
// automatically once the page no longer shows a challenge.
type Site struct {
	browser *Browser
	store   *Store
	limit   *Limiter

	mu      sync.Mutex
	gen     uint64
	work    *rod.Page // background page: search template, fetch and resource loads
	form    url.Values
	gridURL string
	cursors map[string]string
	bootMu  sync.Mutex

	renderMu sync.Mutex
	render   *rod.Page

	humanMu   sync.Mutex
	epoch     atomic.Uint64
	humanOp   string
	humanPage *rod.Page

	journals flights

	identity   map[string]any
	downloadMu sync.Mutex
}
type flight struct {
	done chan struct{}
	q    *Quality
	err  error
}

func NewSite(browser *Browser, store *Store) *Site {
	interval := time.Duration(envInt("CNKI_REQUEST_INTERVAL_MS", 500)) * time.Millisecond
	return &Site{browser: browser, store: store, limit: NewLimiter(interval, envInt("CNKI_MAX_INFLIGHT", 6)), cursors: map[string]string{}}
}

// do charges the operation budget and runs one request inside a rate-limit slot.
func (s *Site) do(op *Op, fn func() error) error {
	if err := op.charge(); err != nil {
		return err
	}
	release, err := s.limit.Acquire(op.ctx)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}

func (s *Site) workPage(op *Op) (*rod.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, gen, err := s.browser.Client()
	if err != nil {
		return nil, err
	}
	if s.work != nil && s.gen == gen {
		return s.work, nil
	}
	p, err := s.browser.NewPage(op.ctx)
	if err != nil {
		return nil, err
	}
	s.work, s.render, s.gen, s.form, s.cursors = p, nil, gen, nil, map[string]string{}
	return p, nil
}
func (s *Site) renderPage(op *Op) (*rod.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, gen, err := s.browser.Client()
	if err != nil {
		return nil, err
	}
	if s.render != nil && s.gen == gen {
		return s.render, nil
	}
	p, err := s.browser.NewPage(op.ctx)
	if err == nil && s.gen == gen {
		s.render = p
	}
	return p, err
}

// recover drops cached pages and, if the connection itself is dead, Chrome.
func (s *Site) recover() {
	s.mu.Lock()
	s.work, s.render, s.form = nil, nil, nil
	s.mu.Unlock()
	if client, _, err := s.browser.Client(); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err = (proto.BrowserGetVersion{}).Call(client.Context(ctx))
		cancel()
		if err != nil {
			s.browser.Reset()
		}
	}
}

// retry repeats a read once after transport recovery. Never used for clicks.
func (s *Site) retry(op *Op, fn func() error) error {
	err := fn()
	if isInfrastructure(err) && op.ctx.Err() == nil {
		s.recover()
		err = fn()
	}
	return err
}

var (
	challengeTitle = regexp.MustCompile(`<title>\s*安全验证\s*</title>`)
	vericodeMarker = regexp.MustCompile(`id=["']?vericode|name=["']?vericodeForm`)
)

func responseProblem(r Response) *Problem {
	switch {
	case r.Status == 429:
		return &Problem{Code: "RATE_LIMITED", Message: "知网限制当前请求频率，请稍后继续", Retryable: true}
	case challengeTitle.MatchString(r.Body) || vericodeMarker.MatchString(r.Body):
		return &Problem{Code: "VERIFICATION_REQUIRED", Message: "知网要求人工验证", Retryable: true}
	case r.Status == 401 || strings.Contains(r.URL, "//login."):
		return &Problem{Code: "LOGIN_REQUIRED", Message: "当前资源要求登录", Retryable: true}
	case r.Status == 403:
		return &Problem{Code: "ACCESS_DENIED", Message: "当前资源拒绝访问"}
	case r.Status == 404:
		return &Problem{Code: "LOCATOR_EXPIRED", Message: "文献入口失效", Retryable: true}
	case r.Status >= 500:
		return &Problem{Code: "SITE_UNAVAILABLE", Message: "知网服务暂时不可用", Retryable: true}
	}
	return nil
}

// human hands the page to the person and waits until the challenge disappears
// (or operation_control resume). Only one human step runs at a time; callers
// that raced into the same challenge simply retry after it is solved.
func (s *Site) human(op *Op, seen uint64, reason string, show func() (*rod.Page, error)) error {
	s.humanMu.Lock()
	defer s.humanMu.Unlock()
	if s.epoch.Load() != seen {
		return nil
	}
	if op.policy() == "skip" {
		return &Problem{Code: "VERIFICATION_SKIPPED", Message: "按请求跳过需要人工处理的资源，未判定资源权限", Details: map[string]any{"reason": reason}}
	}
	s.limit.Hold()
	defer s.limit.Release()
	page, err := show()
	if err != nil {
		return err
	}
	if err = s.browser.Window(op.ctx, page, "normal", true); err != nil {
		return err
	}
	s.mu.Lock()
	s.humanOp, s.humanPage = op.ID(), page
	s.mu.Unlock()
	message := "知网要求人工验证：请在弹出的浏览器窗口完成验证，完成后任务会自动继续"
	if reason == "LOGIN_REQUIRED" {
		message = "知网要求登录：请在弹出的浏览器窗口登录，完成后任务会自动继续"
	}
	op.awaitUser(message)
	defer func() {
		s.mu.Lock()
		s.humanOp, s.humanPage = "", nil
		s.mu.Unlock()
		op.userDone()
		s.epoch.Add(1)
		s.limit.Slow()
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.browser.Window(cleanup, page, "minimized", false)
	}()
	resume := op.resumeSignal()
	deadline := time.Now().Add(20 * time.Minute)
	clear := 0
	for clear < 2 {
		select {
		case <-op.ctx.Done():
			return op.ctx.Err()
		case <-resume:
			return nil
		case <-time.After(time.Second):
		}
		if time.Now().After(deadline) {
			return &Problem{Code: "VERIFICATION_TIMEOUT", Message: "人工验证等待超时", Retryable: true}
		}
		g, err := inspectPage(op.ctx, page)
		if isInfrastructure(err) {
			return err
		}
		if err == nil && g.Ready && !g.Challenge && !g.Login {
			clear++
		} else {
			clear = 0
		}
	}
	return nil
}

// HumanPage reports the page a person is currently asked to use for op.
func (s *Site) HumanPage(op string) *rod.Page {
	s.mu.Lock()
	defer s.mu.Unlock()
	if op == "" || s.humanOp == op {
		return s.humanPage
	}
	return nil
}

// waitPage polls one page until ready reports true, handing challenges to the person.
func (s *Site) waitPage(op *Op, page *rod.Page, timeout time.Duration, ready func(pageGate) (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		g, err := inspectPage(op.ctx, page)
		if err != nil {
			return err
		}
		if g.Challenge || g.Login {
			code := "VERIFICATION_REQUIRED"
			if g.Login {
				code = "LOGIN_REQUIRED"
			}
			if err = s.human(op, s.epoch.Load(), code, func() (*rod.Page, error) { return page, nil }); err != nil {
				return err
			}
			deadline = time.Now().Add(timeout)
			continue
		}
		if g.Denied {
			return fail("ACCESS_DENIED", "站点拒绝当前资源访问")
		}
		ok, err := ready(g)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return &Problem{Code: "CONTENT_NOT_READY", Message: "页面未呈现预期内容，不能判定正文不存在或无权限", Retryable: true}
		}
		if err = sleep(op.ctx, 120*time.Millisecond); err != nil {
			return err
		}
	}
}

// open navigates a page and waits until the new document satisfies ready.
func (s *Site) open(op *Op, page *rod.Page, target string, timeout time.Duration, ready func(pageGate) (bool, error)) error {
	marker := newID("nav_")
	err := s.do(op, func() error {
		if _, e := page.Context(op.ctx).Eval(`v=>{globalThis.__cnkiNavigation=v}`, marker); e != nil {
			return e
		}
		return page.Context(op.ctx).Navigate(target)
	})
	if err != nil {
		return err
	}
	return s.waitPage(op, page, timeout, func(g pageGate) (bool, error) {
		if !g.Ready || g.Marker == marker {
			return false, nil
		}
		return ready(g)
	})
}

// parsed adapts a parser into a readiness check: identity conflicts stop at
// once, other parse failures mean "not rendered yet".
func parsed[T any](page *rod.Page, op *Op, parse func(string) (T, error), out *T, last *error) func(pageGate) (bool, error) {
	return func(pageGate) (bool, error) {
		markup, err := page.Context(op.ctx).HTML()
		if err != nil {
			return false, err
		}
		v, err := parse(markup)
		if err != nil {
			*last = err
			if asProblem(err).Code == "IDENTITY_CONFLICT" {
				return false, err
			}
			return false, nil
		}
		*out = v
		return true, nil
	}
}

// load fetches a page through Chrome's network stack and parses it; only when
// the fetched HTML is unusable does it fall back to rendering a real tab.
func load[T any](s *Site, op *Op, target string, parse func(string) (T, error)) (T, error) {
	var zero T
	for attempt := 0; ; attempt++ {
		var r Response
		err := s.retry(op, func() error {
			page, e := s.workPage(op)
			if e != nil {
				return e
			}
			return s.do(op, func() (e error) { r, e = s.browser.Resource(op.ctx, page, target); return })
		})
		if err != nil {
			if fatal(err) {
				return zero, err
			}
			break
		}
		problem := responseProblem(r)
		if problem == nil {
			v, perr := parse(r.Body)
			if perr == nil {
				s.limit.Fast()
				return v, nil
			}
			if asProblem(perr).Code == "IDENTITY_CONFLICT" {
				return zero, perr
			}
			break
		}
		switch problem.Code {
		case "RATE_LIMITED", "SITE_UNAVAILABLE":
			s.limit.Slow()
			if attempt < 2 {
				if err = sleep(op.ctx, time.Duration(2<<attempt)*time.Second); err != nil {
					return zero, err
				}
				continue
			}
			return zero, problem
		case "ACCESS_DENIED", "LOCATOR_EXPIRED":
			return zero, problem
		}
		break // verification or login: render the page so the person can act
	}
	s.renderMu.Lock()
	defer s.renderMu.Unlock()
	var out T
	var last error
	err := s.retry(op, func() error {
		page, e := s.renderPage(op)
		if e != nil {
			return e
		}
		return s.open(op, page, target, 12*time.Second, parsed(page, op, parse, &out, &last))
	})
	if err != nil && asProblem(err).Code == "CONTENT_NOT_READY" && last != nil {
		return zero, last
	}
	return out, err
}

// bootstrap captures the real search request once per browser session, so the
// form template always matches what the site's own JavaScript sends.
func (s *Site) bootstrap(op *Op, term string) error {
	s.bootMu.Lock()
	defer s.bootMu.Unlock()
	p, err := s.workPage(op)
	if err != nil {
		return err
	}
	s.mu.Lock()
	ready := s.form != nil
	s.mu.Unlock()
	if ready {
		return nil
	}
	err = s.open(op, p, advancedURL, 20*time.Second, func(pageGate) (bool, error) {
		r, e := p.Context(op.ctx).Eval(`()=>document.readyState==='complete'&&typeof cnkiSearch!=='undefined'&&typeof cnkiSearch.getSearchJsonInfo==='function'&&!!document.querySelector('#gradetxt input[data-tipid]')`)
		return e == nil && r.Value.Bool(), nil
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(op.ctx, 30*time.Second)
	defer cancel()
	requests := make(chan *proto.NetworkRequestWillBeSent, 1)
	wait := p.Context(ctx).EachEvent(func(e *proto.NetworkRequestWillBeSent) bool {
		if e.Request.Method == "POST" && strings.Contains(e.Request.URL, "/brief/grid") {
			requests <- e
			return true
		}
		return false
	})
	go wait()
	if _, err = p.Context(ctx).Eval(`term=>{
 const boxes=[...document.querySelectorAll('#gradetxt input[data-tipid]')].filter(e=>e.getBoundingClientRect().width>0);
 if(!boxes.length)throw new Error('QUERY_FORM_NOT_FOUND');for(const input of boxes){input.value='';}
 boxes[0].value=term;boxes[0].dispatchEvent(new Event('change',{bubbles:true}));
 const english=document.querySelector('input[data-id=EN]');if(english?.checked)english.click();
}`, term); err != nil {
		return err
	}
	if err = s.do(op, func() error { return s.browser.Click(ctx, p, "input.btn-search,button.btn-search", "", false) }); err != nil {
		return err
	}
	var request *proto.NetworkRequestWillBeSent
	select {
	case request = <-requests:
	case <-ctx.Done():
		return &Problem{Code: "QUERY_TEMPLATE_UNAVAILABLE", Message: "没有捕获知网实际检索请求", Retryable: true}
	}
	body := request.Request.PostData
	if body == "" {
		post, e := (proto.NetworkGetRequestPostData{RequestID: request.RequestID}).Call(p.Context(ctx))
		if e != nil {
			return e
		}
		body = post.PostData
	}
	form, err := url.ParseQuery(body)
	if err != nil {
		return err
	}
	var query map[string]any
	if json.Unmarshal([]byte(form.Get("QueryJson")), &query) != nil {
		return fail("QUERY_TEMPLATE_UNSUPPORTED", "实际查询模板无法解析")
	}
	if node, ok := query["QNode"].(map[string]any); !ok || node["QGroup"] == nil {
		return fail("QUERY_TEMPLATE_UNSUPPORTED", "实际查询模板缺少 QNode/QGroup")
	}
	s.mu.Lock()
	s.form, s.gridURL = form, strings.Split(request.Request.URL, "?")[0]
	s.mu.Unlock()
	return nil
}

// gridForm rewrites the captured template with the compiled condition.
func gridForm(template url.Values, compiled map[string]any, page int, cursor string, in SearchInput) (url.Values, map[string]any, error) {
	var query map[string]any
	_ = json.Unmarshal([]byte(template.Get("QueryJson")), &query)
	groups, _ := query["QNode"].(map[string]any)["QGroup"].([]any)
	replaced := false
	for i, item := range groups {
		group, ok := item.(map[string]any)
		if !ok {
			return nil, nil, fail("QUERY_TEMPLATE_UNSUPPORTED", "查询分组形状变化")
		}
		if group["Key"] == "Subject" {
			groups[i], replaced = compiled, true
		}
		if group["Key"] == "ControlGroup" && (in.YearFrom > 0 || in.YearTo > 0) {
			from, to := "", ""
			if in.YearFrom > 0 {
				from = fmt.Sprintf("%d-01-01", in.YearFrom)
			}
			if in.YearTo > 0 {
				to = fmt.Sprintf("%d-12-31", in.YearTo)
			}
			children, _ := group["ChildItems"].([]any)
			group["ChildItems"] = append(children, map[string]any{"Key": ".tit-date-box", "Title": "发表时间", "Logic": 0, "Items": []any{map[string]any{"Key": ".tit-date-box", "Title": "发表时间", "Logic": 0, "Field": "PT", "Operator": 7, "Value": from, "Value2": to, "options": map[string]any{}}}, "ChildItems": []any{}})
		}
	}
	if !replaced {
		return nil, nil, fail("QUERY_TEMPLATE_UNSUPPORTED", "缺少知网查询条件组")
	}
	query["SearchFrom"] = 1
	if page > 1 {
		query["SearchFrom"] = 4
	}
	raw, _ := json.Marshal(query)
	form := url.Values{}
	for k, v := range template {
		form[k] = append([]string{}, v...)
	}
	sort := map[string]string{"": "FFD", "relevance": "FFD", "date_desc": "PT", "cited_desc": "CF", "downloaded_desc": "DFR"}[in.Options.Sort]
	for k, v := range map[string]string{"QueryJson": string(raw), "pageNum": fmt.Sprint(page), "boolSearch": fmt.Sprint(page == 1), "dstyle": "listmode", "sortField": sort, "sortType": "desc", "boolSortSearch": "false", "sentenceSearch": "false"} {
		form.Set(k, v)
	}
	// Larger pages return the same ranked rows with fewer requests.
	if form.Has("pageSize") {
		form.Set("pageSize", gridPageSize)
	}
	if page > 1 {
		form.Del("CurPage")
		form.Set("turnpage", cursor)
		form.Set("aside", "")
	}
	return form, query, nil
}

func (s *Site) Grid(op *Op, ch Channel, page int, in SearchInput) (Grid, error) {
	compiled, err := Compile(ch.Expression)
	if err != nil {
		return Grid{}, err
	}
	key := fingerprint([]any{ch.Expression, in.YearFrom, in.YearTo, in.Options.Sort})
	for attempt := 0; attempt < 4; attempt++ {
		if err = s.retry(op, func() error { return s.bootstrap(op, anchor(ch.Expression)) }); err != nil {
			return Grid{}, err
		}
		s.mu.Lock()
		p, template, gridURL, cursor := s.work, s.form, s.gridURL, s.cursors[key]
		s.mu.Unlock()
		if p == nil || template == nil {
			continue
		}
		if page > 1 && cursor == "" {
			if _, err = s.Grid(op, ch, 1, in); err != nil {
				return Grid{}, err
			}
			s.mu.Lock()
			cursor = s.cursors[key]
			s.mu.Unlock()
		}
		form, query, err := gridForm(template, compiled, page, cursor, in)
		if err != nil {
			return Grid{}, err
		}
		seen := s.epoch.Load()
		var r Response
		err = s.do(op, func() (e error) { r, e = s.browser.Fetch(op.ctx, p, gridURL, form.Encode()); return })
		if isInfrastructure(err) && op.ctx.Err() == nil && attempt == 0 {
			s.recover()
			continue
		}
		if err != nil {
			return Grid{}, err
		}
		if problem := responseProblem(r); problem != nil {
			switch problem.Code {
			case "VERIFICATION_REQUIRED", "LOGIN_REQUIRED":
				if err = s.gridChallenge(op, p, r, query, seen, problem.Code); err != nil {
					return Grid{}, err
				}
				continue
			case "RATE_LIMITED", "SITE_UNAVAILABLE":
				s.limit.Slow()
				if attempt < 2 {
					if err = sleep(op.ctx, time.Duration(2<<attempt)*time.Second); err != nil {
						return Grid{}, err
					}
					continue
				}
			}
			return Grid{}, problem
		}
		grid, err := ParseGrid(r.Body, r.URL)
		if err != nil {
			if asProblem(err).Code == "SITE_TEMPORARILY_EMPTY" && attempt == 0 {
				continue
			}
			return grid, err
		}
		s.limit.Fast()
		if grid.Page > 0 && grid.Page != page {
			return grid, fail("PAGINATION_MISMATCH", "站点返回的页码与请求不同")
		}
		if grid.Cursor != "" {
			s.mu.Lock()
			if len(s.cursors) > 2000 {
				s.cursors = map[string]string{} // pages >1 re-fetch page 1 if needed
			}
			s.cursors[key] = grid.Cursor
			s.mu.Unlock()
		}
		return grid, nil
	}
	return Grid{}, &Problem{Code: "VERIFICATION_REPEATED", Message: "人工处理后站点仍要求验证，已停止重试", Retryable: true}
}

// gridChallenge shows the verification the search API returned inside the
// original search page, so solving it keeps the session's search context.
func (s *Site) gridChallenge(op *Op, p *rod.Page, r Response, query map[string]any, seen uint64, code string) error {
	return s.human(op, seen, code, func() (*rod.Page, error) {
		if strings.Contains(r.Body, "vericodeForm") {
			_, err := p.Context(op.ctx).Eval(`(html,query)=>{
 const fragment=new DOMParser().parseFromString(html,'text/html').querySelector('[name=vericodeForm]');
 const box=document.querySelector('#briefBox');if(!fragment||!box)throw new Error('VERIFICATION_CONTEXT_UNAVAILABLE');
 if(query&&window.cnkiSearch?.setQueryJson)cnkiSearch.setQueryJson(query);
 box.replaceChildren(document.importNode(fragment,true));box.scrollIntoView({block:'center'});
}`, r.Body, query)
			return p, err
		}
		s.mu.Lock()
		s.form = nil // the page leaves the search origin; capture the template again afterwards
		gridURL := s.gridURL
		s.mu.Unlock()
		if r.URL == gridURL {
			return nil, fail("VERIFICATION_CONTEXT_UNAVAILABLE", "响应未提供可显示的验证入口")
		}
		return p, p.Context(op.ctx).Navigate(r.URL)
	})
}

// locate finds a fresh abstract-page locator by exact title and verified identity.
func (s *Site) locate(op *Op, p Paper) (Paper, error) {
	found, err := s.locateTitles(op, []string{p.Title})
	if err != nil {
		return p, err
	}
	var match *Paper
	for i, c := range found[titleKey(p.Title)] {
		if SamePaper(p, c) || len(identityKeys(p)) == 0 {
			if match != nil {
				return p, fail("IDENTITY_AMBIGUOUS", "完整题名对应多篇文献，请使用 record_ref")
			}
			match = &found[titleKey(p.Title)][i]
		}
	}
	if match == nil || match.locator() == "" {
		return p, fail("DOCUMENT_NOT_FOUND", "完整题名检索没有取得一致身份的文献入口")
	}
	return *match, nil
}

// locateTitles finds up to 10 exact titles with one OR search, grouping rows by title key.
func (s *Site) locateTitles(op *Op, titles []string) (map[string][]Paper, error) {
	leaves := []Expression{}
	for _, t := range uniqueStrings(titles) {
		leaves = append(leaves, Expression{Field: "title", Value: t, Match: "exact"})
	}
	if len(leaves) == 0 || len(leaves) > 10 {
		return nil, fail("INVALID_INPUT", "一次定位 1–10 个题名")
	}
	grid, err := s.Grid(op, Channel{ID: "locate", Expression: combine("or", leaves), Weight: 1}, 1, SearchInput{})
	if err != nil {
		return nil, err
	}
	out := map[string][]Paper{}
	for _, p := range grid.Papers {
		k := titleKey(p.Title)
		out[k] = append(out[k], p)
	}
	return out, nil
}

func bindDetail(markup, locator string, p Paper) (Paper, error) {
	fresh, _, err := ParseDetail(markup, locator)
	if err != nil {
		return p, err
	}
	if !detailMatches(p, fresh) {
		return p, &Problem{Code: "IDENTITY_CONFLICT", Message: "详情页与请求文献不一致", Details: map[string]any{"requested": p.Title, "observed": fresh.Title}}
	}
	fresh.Ref = p.Ref
	if fresh.URL == "" {
		fresh.URL = p.URL
	}
	fresh.Locator = locator
	for dst, v := range map[*string]string{&fresh.DBName: p.DBName, &fresh.Filename: p.Filename, &fresh.DBCode: p.DBCode} {
		if *dst == "" {
			*dst = v
		}
	}
	return fresh, nil
}

// Detail reads the abstract page, relocating once if the stored locator expired.
func (s *Site) Detail(op *Op, p Paper) (Paper, error) {
	if detailFresh(p) {
		return p, nil
	}
	target, relocated := p.locator(), false
	if target == "" {
		found, err := s.locate(op, p)
		if err != nil {
			return p, err
		}
		target, relocated = found.locator(), true
	}
	for {
		fresh, err := load(s, op, target, func(html string) (Paper, error) { return bindDetail(html, target, p) })
		if err == nil {
			return fresh, nil
		}
		if relocated || fatal(err) || asProblem(err).Code == "ACCESS_DENIED" {
			return p, err
		}
		found, lerr := s.locate(op, p)
		if lerr != nil {
			return p, err
		}
		target, relocated = found.locator(), true
	}
}

// Journal reads source quality once per journal: concurrent callers share one
// request and results are cached (present 7 days, no tiers 1 day).
func (s *Site) Journal(op *Op, p Paper) (*Quality, error) {
	if !strings.Contains(p.Type, "期刊") || p.Source == "" {
		return nil, nil
	}
	key := sourceKey(p.Source)
	if q, ok := s.store.Source(key); ok && q.Parser == ParserVersion && (q.State == "present" && time.Since(q.ObservedAt) < 7*24*time.Hour || time.Since(q.ObservedAt) < 24*time.Hour) {
		return &q, nil
	}
	if p.SourceURL == "" {
		return nil, fail("SOURCE_LOCATOR_MISSING", "详情页没有来源期刊入口")
	}
	return s.journals.do(op.ctx, key, func() (*Quality, error) {
		q, err := load(s, op, p.SourceURL, func(html string) (*Quality, error) { return ParseQuality(html, p.Source, p.SourceURL) })
		if err == nil {
			s.store.PutSource(key, *q)
		}
		return q, err
	})
}

// flights deduplicates concurrent identical work and remembers identity
// mismatches for an hour so they are not re-requested for every paper.
type flights struct {
	mu     sync.Mutex
	calls  map[string]*flight
	misses map[string]time.Time
}

func (g *flights) do(ctx context.Context, key string, fn func() (*Quality, error)) (*Quality, error) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls, g.misses = map[string]*flight{}, map[string]time.Time{}
	}
	if at, ok := g.misses[key]; ok && time.Since(at) < time.Hour {
		g.mu.Unlock()
		return nil, &Problem{Code: "SOURCE_IDENTITY_UNCONFIRMED", Message: "期刊页名称与来源不符（近期已核验）"}
	}
	if f := g.calls[key]; f != nil {
		g.mu.Unlock()
		select {
		case <-f.done:
			return f.q, f.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f := &flight{done: make(chan struct{})}
	g.calls[key] = f
	g.mu.Unlock()
	f.q, f.err = fn()
	g.mu.Lock()
	delete(g.calls, key)
	if f.err != nil && asProblem(f.err).Code == "SOURCE_IDENTITY_UNCONFIRMED" {
		g.misses[key] = time.Now()
	}
	g.mu.Unlock()
	close(f.done)
	return f.q, f.err
}

// document opens the paper's abstract page in a dedicated tab (for reading and downloads).
func (s *Site) document(op *Op, p Paper) (*rod.Page, Paper, error) {
	target := p.locator()
	if target == "" {
		found, err := s.locate(op, p)
		if err != nil {
			return nil, p, err
		}
		target = found.locator()
	}
	page, err := s.browser.NewPage(op.ctx)
	if err != nil {
		return nil, p, err
	}
	var bound Paper
	var last error
	err = s.open(op, page, target, 15*time.Second, parsed(page, op, func(html string) (Paper, error) { return bindDetail(html, target, p) }, &bound, &last))
	if err != nil {
		closePage(page)
		if asProblem(err).Code == "CONTENT_NOT_READY" && last != nil {
			err = last
		}
		return nil, p, err
	}
	return page, bound, nil
}
func closePage(p *rod.Page) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = p.Context(ctx).Close()
}

// settle waits until the page has loaded and gone idle, so links handled by
// the site's own scripts are bound before they are clicked.
func (s *Site) settle(op *Op, page *rod.Page) error {
	var since time.Time
	err := s.waitPage(op, page, 15*time.Second, func(g pageGate) (bool, error) {
		if !g.Complete || g.Pending {
			since = time.Time{}
			return false, nil
		}
		if since.IsZero() {
			since = time.Now()
		}
		return time.Since(since) > 1500*time.Millisecond, nil
	})
	if err != nil && asProblem(err).Code == "CONTENT_NOT_READY" {
		return nil
	}
	return err
}

// popups records tabs opened by one page: CNKI opens readers, download orders,
// verification and refusal pages in new tabs regardless of the link target.
type popups struct {
	mu     sync.Mutex
	ids    []proto.TargetTargetID
	client *rod.Browser
	cancel context.CancelFunc
}

func (s *Site) watchPopups(op *Op, page *rod.Page) (*popups, error) {
	client, _, err := s.browser.Client()
	if err != nil {
		return nil, err
	}
	_ = proto.TargetSetDiscoverTargets{Discover: true}.Call(client.Context(op.ctx))
	ctx, cancel := context.WithCancel(op.ctx)
	w := &popups{client: client, cancel: cancel}
	go client.Context(ctx).EachEvent(func(e *proto.TargetTargetCreated) {
		if e.TargetInfo.OpenerID == page.TargetID && e.TargetInfo.Type == proto.TargetTargetInfoTypePage {
			w.mu.Lock()
			w.ids = append(w.ids, e.TargetInfo.TargetID)
			w.mu.Unlock()
		}
	})()
	return w, nil
}

// tabs returns the opener followed by the live popups it opened.
func (w *popups) tabs(page *rod.Page) []*rod.Page {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := []*rod.Page{page}
	for _, id := range w.ids {
		if p, err := w.client.PageFromTarget(id); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// close stops watching and closes the popups (the opener belongs to the caller).
func (w *popups) close(page *rod.Page) {
	w.cancel()
	for _, p := range w.tabs(page)[1:] {
		closePage(p)
	}
}

// gate hands any tab showing a challenge to the person; it reports whether one was handled.
func (s *Site) gate(op *Op, tab *rod.Page, g pageGate) (bool, error) {
	if !g.Challenge && !g.Login {
		return false, nil
	}
	code := "VERIFICATION_REQUIRED"
	if g.Login {
		code = "LOGIN_REQUIRED"
	}
	return true, s.human(op, s.epoch.Load(), code, func() (*rod.Page, error) { return tab, nil })
}

// Read opens the HTML reader (in whichever tab the site uses) and extracts the
// body once its catalog is complete.
func (s *Site) Read(op *Op, p Paper) (ReaderContent, error) {
	var content ReaderContent
	page, _, err := s.document(op, p)
	if err != nil {
		return content, err
	}
	defer closePage(page)
	if err = s.settle(op, page); err != nil {
		return content, err
	}
	watch, err := s.watchPopups(op, page)
	if err != nil {
		return content, err
	}
	defer watch.close(page)
	if err = s.do(op, func() error { return s.browser.Click(op.ctx, page, "a", "HTML阅读|在线阅读", false) }); err != nil {
		if asProblem(err).Code == "ENTRY_NOT_FOUND" {
			return content, &Problem{Code: "HTML_ENTRY_NOT_FOUND", Message: "详情页没有可见 HTML 阅读入口；可尝试下载 PDF/CAJ"}
		}
		return content, err
	}
	var reader *rod.Page
	var last error
	deadline := time.Now().Add(20 * time.Second)
	for done := false; !done; {
		if time.Now().After(deadline) {
			break
		}
		for _, tab := range watch.tabs(page) {
			g, err := inspectPage(op.ctx, tab)
			if err != nil {
				continue // closed or navigating
			}
			if handled, err := s.gate(op, tab, g); err != nil {
				return content, err
			} else if handled {
				deadline = time.Now().Add(20 * time.Second)
				break
			}
			if g.Denied {
				return content, fail("ACCESS_DENIED", "站点拒绝当前资源访问")
			}
			markup, err := tab.Context(op.ctx).HTML()
			if err != nil {
				continue
			}
			c, err := ParseReader(markup, p.Title)
			if err != nil {
				if asProblem(err).Code == "IDENTITY_CONFLICT" {
					return content, err
				}
				last = err
				continue
			}
			content, reader = c, tab
			done = len(c.Coverage.MissingHeadings) == 0
			break
		}
		if !done {
			if err = sleep(op.ctx, 200*time.Millisecond); err != nil {
				return content, err
			}
		}
	}
	if reader == nil {
		if last == nil {
			last = &Problem{Code: "CONTENT_NOT_READY", Message: "阅读页未呈现正文，不能判定无权限", Retryable: true}
		}
		return content, last
	}
	// CNKI serves abstract-only "trial" readers without any visible notice.
	if info, e := reader.Info(); e == nil && strings.Contains(info.URL, "loginType=trialRead") {
		content.Trial, content.Coverage.Scope = true, "trial"
	} else if len(content.Coverage.MissingHeadings) > 0 {
		op.problem(&Problem{Code: "CONTENT_PARTIAL", Message: "目录中部分章节未呈现，返回已呈现部分"})
	}
	return content, nil
}

// waitIdentity accepts positive identity at once; "not logged in" needs a settled header.
func (s *Site) waitIdentity(op *Op, page *rod.Page) (pageGate, error) {
	var result pageGate
	var settled time.Time
	err := s.waitPage(op, page, 12*time.Second, func(g pageGate) (bool, error) {
		result = g
		if g.Personal || len(g.Institutions) > 0 {
			return true, nil
		}
		if !g.IdentityReady || !g.Complete || g.Pending {
			settled = time.Time{}
			return false, nil
		}
		if settled.IsZero() {
			settled = time.Now()
		}
		return time.Since(settled) >= 750*time.Millisecond, nil
	})
	return result, err
}

// identityAnywhere inspects every open CNKI tab: people often finish a login
// in a tab the site opened, or on www.cnki.net instead of the KNS page.
func (s *Site) identityAnywhere(op *Op) (pageGate, bool) {
	client, _, err := s.browser.Client()
	if err != nil {
		return pageGate{}, false
	}
	pages, err := client.Pages()
	if err != nil {
		return pageGate{}, false
	}
	for _, p := range pages {
		info, err := p.Info()
		if err != nil {
			continue
		}
		if u, err := url.Parse(info.URL); err != nil || !(siteURL(u) || strings.HasSuffix(u.Hostname(), ".cnki.net") || u.Hostname() == "cnki.net") {
			continue
		}
		ctx, cancel := context.WithTimeout(op.ctx, 2*time.Second)
		g, err := inspectPage(ctx, p)
		cancel()
		if err == nil && (g.Personal || len(g.Institutions) > 0) {
			return g, true
		}
	}
	return pageGate{}, false
}
func (s *Site) rememberIdentity(g pageGate) map[string]any {
	result := map[string]any{"personal": "not_observed", "institution": "not_observed", "permission_scope": "unknown", "observed_at": time.Now().UTC(), "evidence_source": "kns_header"}
	if g.Personal {
		result["personal"] = "authenticated"
	}
	if len(g.Institutions) > 0 {
		result["institution"] = "observed"
		result["institution_evidence"] = g.Institutions
	}
	s.mu.Lock()
	s.identity = result
	s.mu.Unlock()
	return result
}
