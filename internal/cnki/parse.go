package cnki

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

var (
	hiddenStyle   = regexp.MustCompile(`(?i)display\s*:\s*none|visibility\s*:\s*hidden`)
	integerText   = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(万)?$`)
	yearText      = regexp.MustCompile(`(?:18|19|20|21)\d{2}`)
	pageText      = regexp.MustCompile(`(\d+)\s*/\s*(\d+)`)
	noResults     = regexp.MustCompile(`未找到|没有找到|暂无.{0,6}结果`)
	tempEmpty     = regexp.MustCompile(`暂无数据|稍后重试|服务繁忙`)
	issueText     = regexp.MustCompile(`((?:19|20)\d{2})\s*[,，.]?\s*(\d+)?\s*[（(]([^）)]+)[）)]\s*[:：]?\s*(\d+(?:[-—–]\d+)?)?`)
	keywordSplit  = regexp.MustCompile(`[;；,，]`)
	doiText       = regexp.MustCompile(`(?i)DOI\s*[:：]\s*(10\.\d{4,9}/[^\s<>]+)`)
	formerTitle   = regexp.MustCompile(`^曾用刊名[：:]\s*(.*)$`)
	semicolons    = regexp.MustCompile(`[;；]`)
	tierText      = regexp.MustCompile(`(?i)CSSCI(?:来源期刊|扩展版)?|CSCD(?:来源期刊|核心库|扩展库)?|北大核心|SSCI|SCI(?:-E)?|A&HCI|EI(?:来源期刊)?|AMI(?:综合评价)?(?:顶级|权威|核心|扩展|入库)`)
	tierLabel     = regexp.MustCompile(`^(收录|数据库收录|来源类别|期刊荣誉|期刊收录)[：:]`)
	metricYear    = regexp.MustCompile(`[（(](20\d{2})版[）)]\s*(?:复合|综合)影响因子`)
	compositeText = regexp.MustCompile(`复合影响因子\s*[:：]?\s*(\d+(?:\.\d+)?)`)
	aggregateText = regexp.MustCompile(`综合影响因子\s*[:：]?\s*(\d+(?:\.\d+)?)`)
	trialText     = regexp.MustCompile(`仅供试读|试读结束|购买后阅读全文`)
	authorRole    = regexp.MustCompile(`\s*[（(](?:文|著|编|译|主编|编著|编译|撰|撰文|执笔|整理|口述|摄|图|供稿)[）)]$`)
)

// authors drops role suffixes such as "林倩(文)" that some detail pages append.
func authors(names []string) []string {
	for i, n := range names {
		names[i] = authorRole.ReplaceAllString(n, "")
	}
	return uniqueStrings(names)
}

func document(markup string) (*goquery.Document, error) {
	if len(markup) > 8<<20 {
		return nil, fail("RESPONSE_TOO_LARGE", "HTML 超过解析上限")
	}
	d, err := goquery.NewDocumentFromReader(strings.NewReader(markup))
	if err != nil {
		return nil, err
	}
	d.Find("script,style,noscript,[hidden],[aria-hidden=true]").Remove()
	d.Find("[style]").Each(func(_ int, s *goquery.Selection) {
		if style, _ := s.Attr("style"); hiddenStyle.MatchString(style) {
			s.Remove()
		}
	})
	return d, nil
}

func clean(s string) string { return strings.Join(strings.Fields(s), " ") }
func parseCount(s string) *int {
	m := integerText.FindStringSubmatch(strings.ReplaceAll(strings.TrimSpace(s), ",", ""))
	if len(m) == 0 {
		return nil
	}
	x, _ := strconv.ParseFloat(m[1], 64)
	if m[2] != "" {
		x *= 10000
	}
	n := int(x + .5)
	return &n
}
func texts(s *goquery.Selection) []string {
	out := []string{}
	s.Each(func(_ int, e *goquery.Selection) {
		c := e.Clone()
		c.Find("sup").Remove()
		out = append(out, clean(c.Text()))
	})
	return uniqueStrings(out)
}
func resolved(raw, base string) string {
	u, e := url.Parse(raw)
	b, be := url.Parse(base)
	if e != nil || be != nil || raw == "" {
		return ""
	}
	u = b.ResolveReference(u)
	if !siteURL(u) {
		return ""
	}
	return u.String()
}

// provisionalRef derives a stable record reference from the strongest identity available.
func provisionalRef(p Paper) string {
	key := nativeKey(p)
	if key == "" {
		key = doiKey(p.DOI)
	}
	if key == "" {
		key = bibliographyKey(p)
	}
	if key == "" {
		key = p.Title + "\x00" + p.locator()
	}
	return "rec_" + fingerprint(key)[:24]
}

type Grid struct {
	Papers    []Paper
	Total     *int
	Page      int
	LastPage  int
	Exhausted bool
	Cursor    string
}

func ParseGrid(markup, base string) (Grid, error) {
	d, err := document(markup)
	if err != nil {
		return Grid{}, err
	}
	out := Grid{}
	now := time.Now().UTC()
	d.Find("table.result-table-list tbody tr,table.GridTableContent tbody tr").Each(func(_ int, row *goquery.Selection) {
		a := row.Find("td.name a,a.fz14").First()
		title := clean(a.Text())
		if title == "" {
			return
		}
		href, _ := a.Attr("href")
		locator := resolved(href, base)
		u, _ := url.Parse(locator)
		ids := row.Find("[data-filename]").First()
		value := func(key string) string {
			v, _ := ids.Attr("data-" + key)
			if v == "" && u != nil {
				v = u.Query().Get(key)
			}
			return v
		}
		p := Paper{Title: title, Authors: authors(texts(row.Find("td.author a"))), Source: strings.TrimRight(clean(row.Find("td.source").Text()), " .。"), Date: clean(row.Find("td.date").Text()), Type: clean(row.Find("td.data").Text()), Cited: parseCount(row.Find("td.quote").Text()), Downloaded: parseCount(row.Find("td.download").Text()), DBCode: value("dbcode"), DBName: value("dbname"), Filename: value("filename"), URL: PublicURL(locator), Locator: locator, Abstract: clean(row.Find(".abstract").Text()), Keywords: texts(row.Find(".keywords a")), ListedAt: now}
		p.Year, _ = strconv.Atoi(yearText.FindString(p.Date))
		p.Ref = provisionalRef(p)
		out.Papers = append(out.Papers, p)
	})
	out.Total = parseCount(d.Find(".pagerTitleCell em").Text())
	if match := pageText.FindStringSubmatch(d.Find(".countPageMark").Text()); len(match) > 0 {
		out.Page, _ = strconv.Atoi(match[1])
		out.LastPage, _ = strconv.Atoi(match[2])
	}
	if len(out.Papers) == 0 && (out.Total == nil || *out.Total != 0) && !noResults.MatchString(d.Text()) {
		if tempEmpty.MatchString(d.Text()) {
			return out, &Problem{Code: "SITE_TEMPORARILY_EMPTY", Message: "站点返回临时空页面，不能判定零结果", Retryable: true}
		}
		preview := []rune(clean(d.Text()))
		return out, fail("PARSER_UNSUPPORTED", "响应没有可辨认题录或明确零结果证据："+string(preview[:min(160, len(preview))]))
	}
	out.Cursor, _ = d.Find("#hidTurnPage").Attr("value")
	out.Exhausted = (out.Total != nil && len(out.Papers) >= *out.Total) || (out.LastPage > 0 && out.Page >= out.LastPage)
	return out, nil
}

func meta(d *goquery.Document, names ...string) []string {
	out := []string{}
	d.Find("meta[name],meta[property]").Each(func(_ int, s *goquery.Selection) {
		name, _ := s.Attr("name")
		if name == "" {
			name, _ = s.Attr("property")
		}
		for _, n := range names {
			if strings.EqualFold(name, n) {
				v, _ := s.Attr("content")
				out = append(out, v)
				break
			}
		}
	})
	return uniqueStrings(out)
}
func first(xs []string) string {
	if len(xs) > 0 {
		return xs[0]
	}
	return ""
}

// ParseDetail returns the paper on an abstract page and the journal page link.
func ParseDetail(markup, locator string) (Paper, string, error) {
	d, err := document(markup)
	if err != nil {
		return Paper{}, "", err
	}
	get := func(names ...string) string { return first(meta(d, names...)) }
	u, _ := url.Parse(locator)
	ids := d.Find("[data-filename]").First()
	id := func(key string) string {
		v, _ := ids.Attr("data-" + key)
		if v == "" {
			v, _ = d.Find(fmt.Sprintf("input[name='%s'],input[id='%s']", key, key)).First().Attr("value")
		}
		if v == "" && u != nil {
			v = u.Query().Get(key)
		}
		return v
	}
	p := Paper{Title: get("citation_title", "dc.title"), Authors: authors(meta(d, "citation_author", "dc.creator")), Source: get("citation_journal_title", "citation_conference_title", "dc.source"), Date: get("citation_publication_date", "citation_date", "dc.date"), DOI: get("citation_doi", "dc.identifier.doi"), Volume: get("citation_volume"), Issue: get("citation_issue"), Pages: get("citation_pages"), DBName: id("dbname"), DBCode: id("dbcode"), Filename: id("filename"), URL: PublicURL(locator), Locator: locator}
	if p.Title == "" {
		p.Title = clean(d.Find(".wx-tit h1").First().Text())
	}
	if p.Title == "" { // never take a heading from the site's login widgets
		d.Find("h1").EachWithBreak(func(_ int, h *goquery.Selection) bool {
			if h.Closest(`[class*=login],[class*=Login],[id*=login],[id*=Login],[class*=ecp_]`).Length() == 0 {
				p.Title = clean(h.Text())
			}
			return p.Title == ""
		})
	}
	if p.Title == "" {
		return p, "", fail("PARSER_UNSUPPORTED", "详情页缺少可信题名")
	}
	if len(p.Authors) == 0 {
		root := d.Find("#authorpart").First()
		nodes := root.ChildrenFiltered("span")
		if nodes.Length() == 0 {
			nodes = root.Find("a")
		}
		if nodes.Length() == 0 {
			nodes = d.Find(".wx-tit > h3.author").First().Find("a,span")
		}
		p.Authors = authors(texts(nodes))
	}
	p.Institutions = uniqueStrings(append(meta(d, "citation_author_institution", "citation_author_affiliation"), texts(d.Find("#authorpart").SiblingsFiltered("h3.author").Find("a,span"))...))
	if len(p.Institutions) == 0 {
		p.Institutions = texts(d.Find(".orgn a,.author-org a,#catalog_ORG"))
	}
	sourceLink := d.Find(".top-tip .source a,.top-tip a[href*='navi.cnki.net']").First()
	if p.Source == "" {
		p.Source = clean(sourceLink.Text())
	}
	tip := clean(d.Find(".top-tip").Text())
	if m := issueText.FindStringSubmatch(tip); len(m) > 0 {
		for i, dst := range []*string{&p.Date, &p.Volume, &p.Issue, &p.Pages} {
			if *dst == "" {
				*dst = m[i+1]
			}
		}
	}
	if p.Date == "" {
		p.Date = clean(d.Find(".top-tip .date,#catalog_PUBLISHDATE").First().Text())
	}
	p.Source = strings.TrimRight(clean(p.Source), " .。")
	p.Year, _ = strconv.Atoi(yearText.FindString(p.Date))
	if y := yearText.FindString(tip); y != "" {
		p.Year, _ = strconv.Atoi(y) // the issue year is the citation year
	}
	summary := d.Find("#ChDivSummary").First()
	if summary.Length() == 0 {
		summary = d.Find(".abstract-text,#abstract_text:not(input)").First()
	}
	if parent := summary.Parent().Text(); strings.Contains(parent, "正文快照") || strings.Contains(parent, "全文快照") {
		p.Excerpt = clean(summary.Text())
	} else {
		p.Abstract = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(clean(summary.Text()), "摘要："), "摘要:"))
		if p.Abstract == "" {
			p.Abstract = get("citation_abstract", "dc.description")
		}
	}
	keywords := get("citation_keywords", "dc.subject")
	if keywords == "" {
		keywords = clean(d.Find(".keywords,#ChDivKeyWord,#catalog_KEYWORD").First().Text())
	}
	keywords = strings.TrimPrefix(strings.TrimPrefix(keywords, "关键词："), "关键词:")
	p.Keywords = uniqueStrings(keywordSplit.Split(keywords, -1))
	p.Funds = texts(d.Find("#catalog_FUND,.funds a"))
	if p.Pages == "" {
		p.Pages = strings.Join(uniqueStrings([]string{get("citation_firstpage"), get("citation_lastpage")}), "-")
	}
	if p.DOI == "" {
		if m := doiText.FindStringSubmatch(d.Text()); len(m) > 1 {
			p.DOI = m[1]
		}
	}
	p.DetailAt, p.DetailParser = time.Now().UTC(), ParserVersion
	source, _ := sourceLink.Attr("href")
	p.SourceURL = resolved(source, locator)
	return p, p.SourceURL, nil
}

// ParseQuality reads source tiers and impact metrics, but only when the page's
// current, translated or former journal title matches the requested source.
func ParseQuality(markup, source, sourceURL string) (*Quality, error) {
	d, err := document(markup)
	if err != nil {
		return nil, err
	}
	root := d.Find(".journalInfo .infobox").First()
	heading := root.Find("h3.titbox").First().Clone()
	heading.Children().Remove()
	name := clean(heading.Text())
	if root.Length() == 0 || name == "" {
		return nil, fail("SOURCE_SCHEMA_UNSUPPORTED", "来源页没有可辨认的期刊实体")
	}
	names := map[string]string{sourceKey(name): "current_title"}
	add := func(text, basis string) {
		if k := sourceKey(text); text != "" && names[k] == "" {
			names[k] = basis
		}
	}
	root.Find("h3.titbox p").Each(func(_ int, n *goquery.Selection) { add(clean(n.Text()), "translated_title") })
	root.Find("p,li").Each(func(_ int, n *goquery.Selection) {
		if m := formerTitle.FindStringSubmatch(clean(n.Text())); len(m) > 0 {
			for _, old := range semicolons.Split(m[1], -1) {
				add(clean(old), "former_title")
			}
		}
	})
	basis := names[sourceKey(source)]
	if basis == "" {
		return nil, &Problem{Code: "SOURCE_IDENTITY_UNCONFIRMED", Message: "期刊现名及曾用刊名均与请求来源不符", Details: map[string]any{"requested_source": source, "observed_source": name}}
	}
	q := &Quality{Canonical: name, Basis: basis, SourceURL: sourceURL, State: "present", ObservedAt: time.Now().UTC(), Parser: ParserVersion}
	isWord := func(b byte) bool { return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' }
	// Only label-sized elements count; English word fragments and prose are not tags.
	root.Find("p,span,a,li").Each(func(_ int, node *goquery.Selection) {
		if node.ParentsFiltered("h3,h2,h1").Length() > 0 {
			return
		}
		c := node.Clone()
		c.Children().Remove()
		text := clean(c.Text())
		if text == "" {
			return
		}
		tags := []string{}
		for _, at := range tierText.FindAllStringIndex(text, -1) {
			if at[0] > 0 && isWord(text[at[0]-1]) || at[1] < len(text) && isWord(text[at[1]]) {
				continue
			}
			tags = append(tags, text[at[0]:at[1]])
		}
		residue := strings.Trim(tierText.ReplaceAllString(text, ""), " ，,；;、:：()（）[] ")
		if residue != "" && !tierLabel.MatchString(text) {
			return
		}
		for _, tag := range tags {
			q.Tiers = append(q.Tiers, tag)
			q.TierEvidence = append(q.TierEvidence, TierEvidence{Tier: tag, Text: text})
		}
	})
	q.Tiers = uniqueStrings(q.Tiers)
	body := clean(root.Text())
	metric := func(re *regexp.Regexp) *float64 {
		matches := re.FindAllStringSubmatch(body, -1)
		if len(matches) != 1 {
			return nil
		}
		x, _ := strconv.ParseFloat(matches[0][1], 64)
		return &x
	}
	q.CompositeImpact, q.AggregateImpact = metric(compositeText), metric(aggregateText)
	if m := metricYear.FindStringSubmatch(body); len(m) > 1 {
		q.MetricYear, _ = strconv.Atoi(m[1])
	}
	if len(q.Tiers) == 0 && q.CompositeImpact == nil && q.AggregateImpact == nil {
		q.State = "unknown"
	}
	return q, nil
}

type Section struct {
	Level  int    `json:"level"`
	Title  string `json:"title"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}
type ContentCoverage struct {
	Container           string   `json:"container"`
	DOMCharacters       int      `json:"dom_characters"`
	ExtractedCharacters int      `json:"extracted_characters"`
	MissingHeadings     []string `json:"missing_catalog_headings,omitempty"`
	NonTextElements     int      `json:"non_text_elements"`
	Scope               string   `json:"scope"` // unknown, partial, trial
}
type ReaderContent struct {
	Text     []rune
	Sections []Section
	Coverage ContentCoverage
	Trial    bool
}

func contentCharacters(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

// contentBlocks walks the article tree once, emitting block text and heading offsets (in runes).
func contentBlocks(root *goquery.Selection) (string, []Section) {
	var output strings.Builder
	sections := []Section{}
	offset := 0
	emit := func(text string, level int) {
		if text = clean(text); text == "" {
			return
		}
		if output.Len() > 0 {
			output.WriteString("\n\n")
			offset += 2
		}
		if level > 0 {
			sections = append(sections, Section{Title: text, Offset: offset, Level: level})
		}
		output.WriteString(text)
		offset += len([]rune(text))
	}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && len(node.Data) == 2 && node.Data[0] == 'h' && node.Data[1] >= '1' && node.Data[1] <= '6' {
			level := int(node.Data[1] - '0')
			for _, a := range node.Attr {
				if a.Key == "id" && (a.Val == "bibliography" || a.Val == "footnote") {
					level = 1
				}
			}
			emit(goquery.NewDocumentFromNode(node).Text(), level)
			return
		}
		var inline strings.Builder
		flush := func() { emit(inline.String(), 0); inline.Reset() }
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			switch child.Type {
			case html.TextNode:
				inline.WriteString(child.Data)
			case html.ElementNode:
				switch child.Data {
				case "div", "section", "article", "p", "li", "tr", "blockquote", "figure", "figcaption", "h1", "h2", "h3", "h4", "h5", "h6":
					flush()
					walk(child)
				case "br":
					inline.WriteRune('\n')
				default:
					inline.WriteString(goquery.NewDocumentFromNode(child).Text())
				}
			}
		}
		flush()
	}
	for _, node := range root.Nodes {
		walk(node)
	}
	for i := range sections {
		end := offset
		for j := i + 1; j < len(sections); j++ {
			if sections[j].Level <= sections[i].Level {
				end = sections[j].Offset - 2 // exclude the block separator
				break
			}
		}
		sections[i].Length = end - sections[i].Offset
	}
	return output.String(), sections
}

// ParseReader extracts the HTML reader body and reports gaps against its own catalog.
func ParseReader(markup, expected string) (ReaderContent, error) {
	var out ReaderContent
	d, err := document(markup)
	if err != nil {
		return out, err
	}
	root := d.Find("#paperRead").First()
	if root.Length() == 0 {
		root = d.Find("#XMLContent,.reader-content,.html-content").First()
	}
	if root.Length() == 0 {
		return out, fail("READER_UNSUPPORTED", "没有识别到正文容器")
	}
	title := first(meta(d, "citation_title"))
	if title == "" {
		title = clean(root.Find(".js-prefix h1.Chapter,.article-title,.paper-title").First().Text())
	}
	if title != "" && titleKey(title) != titleKey(expected) {
		return out, fail("IDENTITY_CONFLICT", "正文题名与请求不一致")
	}
	catalog := texts(d.Find(".catalog a[href^='#'],.toc a[href^='#'],.catalogboxs .tit"))
	root.Find("nav,header,footer,aside,button,.toolbar,.ai-panel,.catalog").Remove()
	text, sections := contentBlocks(root)
	if len(text) < 150 {
		return out, fail("READER_UNSUPPORTED", "正文不足，未将摘要当全文")
	}
	out.Text, out.Sections = []rune(text), sections
	out.Trial = trialText.MatchString(d.Text())
	c := ContentCoverage{Scope: "unknown", DOMCharacters: contentCharacters(root.Text()), ExtractedCharacters: contentCharacters(text), NonTextElements: root.Find("img,canvas,svg,math,iframe,object").Length()}
	if id, ok := root.Attr("id"); ok {
		c.Container = "#" + id
	} else {
		c.Container = goquery.NodeName(root)
	}
	headings := map[string]bool{}
	for _, h := range sections {
		headings[titleKey(h.Title)] = true
	}
	for _, title := range catalog {
		if !headings[titleKey(title)] {
			c.MissingHeadings = append(c.MissingHeadings, title)
		}
	}
	if out.Trial {
		c.Scope = "trial"
	} else if len(c.MissingHeadings) > 0 || c.ExtractedCharacters < c.DOMCharacters {
		c.Scope = "partial"
	}
	out.Coverage = c
	return out, nil
}
