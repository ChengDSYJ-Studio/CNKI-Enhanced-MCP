package cnki

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Options struct {
	OnVerification  string `json:"on_verification,omitempty" jsonschema:"人工验证策略：ask 弹窗等待用户（默认），skip 跳过该资源"`
	Sort            string `json:"sort,omitempty" jsonschema:"知网排序：relevance、date_desc、cited_desc、downloaded_desc"`
	CandidateTarget int    `json:"candidate_target,omitempty" jsonschema:"候选目标数，默认 300（结构化检索默认等于 limit）"`
	MaxPages        int    `json:"max_pages,omitempty" jsonschema:"每通道最多页数，默认 5"`
	MaxRequests     int    `json:"max_requests,omitempty" jsonschema:"请求预算，默认 600"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty" jsonschema:"活动时间预算（秒），默认 900"`
	Metadata        string `json:"metadata,omitempty" jsonschema:"详情增强范围：ranked（默认，按初排动态选池）、all、basic（不增强）"`
	Freshness       string `json:"freshness,omitempty" jsonschema:"prefer_cache（默认，10 分钟内同范围检索直接复用，0 请求）或 live"`
}
type SearchInput struct {
	Query    string     `json:"query" jsonschema:"研究问题或检索词"`
	Mode     string     `json:"mode,omitempty" jsonschema:"precise 不做反馈检索；balanced（默认）；broad"`
	Limit    int        `json:"limit,omitempty" jsonschema:"展示条数 1–100，不影响检索范围"`
	YearFrom int        `json:"year_from,omitempty"`
	YearTo   int        `json:"year_to,omitempty"`
	Concepts [][]string `json:"concept_groups,omitempty" jsonschema:"必要概念组：组间 AND，组内为同义词 OR"`
	Options  Options    `json:"options,omitzero"`
}

func validYear(from, to int) error {
	if from < 0 || to < 0 || from > 2200 || to > 2200 || (from > 0 && to > 0 && from > to) {
		return fail("INVALID_YEAR", fmt.Sprintf("无效年份范围 %d–%d", from, to))
	}
	return nil
}
func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

func (s *SearchInput) Validate() error {
	s.Query = strings.TrimSpace(s.Query)
	if s.Query == "" || utf8.RuneCountInString(s.Query) > 1000 {
		return fail("INVALID_QUERY", "研究问题长度须为 1–1000 字")
	}
	if s.Mode == "" {
		s.Mode = "balanced"
	}
	if !oneOf(s.Mode, "precise", "balanced", "broad") {
		return fail("INVALID_MODE", "mode 为 precise、balanced 或 broad")
	}
	if s.Limit < 0 || s.Limit > 100 {
		return fail("INVALID_LIMIT", "limit 为 1–100")
	}
	if err := validYear(s.YearFrom, s.YearTo); err != nil {
		return err
	}
	return s.Options.normalize(300)
}
func (o *Options) normalize(target int) error {
	if !oneOf(o.OnVerification, "", "ask", "skip") {
		return fail("INVALID_POLICY", "on_verification 为 ask 或 skip")
	}
	if !oneOf(o.Sort, "", "relevance", "date_desc", "cited_desc", "downloaded_desc") {
		return fail("INVALID_SORT", "sort 为 relevance、date_desc、cited_desc 或 downloaded_desc")
	}
	defaults := []struct {
		v        *int
		def, max int
		name     string
	}{{&o.CandidateTarget, target, 3000, "candidate_target"}, {&o.MaxPages, 5, 100, "max_pages"}, {&o.MaxRequests, 600, 5000, "max_requests"}, {&o.TimeoutSeconds, 900, 7200, "timeout_seconds"}}
	for _, d := range defaults {
		if *d.v == 0 {
			*d.v = d.def
		}
		if *d.v < 1 || *d.v > d.max {
			return fail("INVALID_OPTION", fmt.Sprintf("%s 须为 1–%d", d.name, d.max))
		}
	}
	if o.Metadata == "" {
		o.Metadata = "ranked"
	}
	if !oneOf(o.Metadata, "ranked", "all", "basic") {
		return fail("INVALID_METADATA", "metadata 为 ranked、all 或 basic")
	}
	if o.Freshness == "" {
		o.Freshness = "prefer_cache"
	}
	if !oneOf(o.Freshness, "live", "prefer_cache") {
		return fail("INVALID_FRESHNESS", "freshness 为 live 或 prefer_cache")
	}
	return nil
}

type Expression struct {
	Field string       `json:"field,omitempty" jsonschema:"subject、title、keywords、abstract、authors、first_author、corresponding_author、institutions、source、doi、funds、fulltext、references、classification、subtitle、title_keywords_abstract"`
	Value string       `json:"value,omitempty"`
	Match string       `json:"match,omitempty" jsonschema:"phrase（默认，模糊）或 exact（精确）"`
	Op    string       `json:"op,omitempty" jsonschema:"and、or、not"`
	Items []Expression `json:"items,omitempty"`
}
type Channel struct {
	ID         string     `json:"id"`
	Expression Expression `json:"expression"`
	Weight     float64    `json:"weight"`
	Reason     string     `json:"reason,omitempty"`
}
type Plan struct {
	Concepts [][]string `json:"concepts"`
	Channels []Channel  `json:"channels"`
	Method   string     `json:"method"`
	Warnings []string   `json:"warnings,omitempty"`
}

var nativeFields = map[string]string{"subject": "SU", "title": "TI", "keywords": "KY", "abstract": "AB", "authors": "AU", "first_author": "FI", "corresponding_author": "RP", "institutions": "AF", "source": "LY", "doi": "DOI", "funds": "FU", "fulltext": "FT", "references": "RF", "classification": "CLC", "subtitle": "ST", "title_keywords_abstract": "TKA"}
var wifi = regexp.MustCompile(`(?i)wi[- ]?fi\s*([0-9]+)`)
var ieee = regexp.MustCompile(`(?i)(?:ieee\s*)?p?(802\.[0-9]+[a-z]*)`)

// aliases adds spelling variants for version identifiers that CNKI indexes inconsistently.
func aliases(text string) []string {
	out := []string{strings.TrimSpace(text)}
	if m := wifi.FindStringSubmatchIndex(text); m != nil {
		next, _ := utf8.DecodeRuneInString(text[m[1]:])
		if !strings.ContainsRune("月日年次个分时", next) {
			n := text[m[2]:m[3]]
			for _, variant := range []string{"Wi-Fi " + n, "WiFi" + n, "Wi-Fi" + n} {
				out = append(out, text[:m[0]]+variant+text[m[1]:])
			}
		}
	}
	if m := ieee.FindStringSubmatch(text); len(m) > 1 {
		for _, variant := range []string{m[1], "IEEE " + m[1], "IEEE P" + m[1]} {
			out = append(out, strings.Replace(text, m[0], variant, 1))
		}
	}
	return uniqueStrings(out)
}

var requestPrefix = regexp.MustCompile(`^(?:请|帮我|检索|搜索|研究一下|关于|有关|查找|分析一下|一下)+`)

// Lexical planning never segments inside a word; only explicit separators split concepts.
var connective = regexp.MustCompile(`(?i)(?:\s+and\s+|[、，；,;]+)`)

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
func lexicalParts(query string) []string {
	out := []string{}
	for _, segment := range connective.Split(query, -1) {
		parts := []string{}
		for _, word := range strings.Fields(segment) {
			if len(parts) > 0 && !hasHan(word) && !hasHan(parts[len(parts)-1]) {
				parts[len(parts)-1] += " " + word
			} else {
				parts = append(parts, word)
			}
		}
		out = append(out, parts...)
	}
	return out
}

// MakePlan keeps every necessary concept in every channel; channels differ only by field.
func MakePlan(s SearchInput) (Plan, error) {
	p := Plan{Concepts: s.Concepts, Method: "explicit"}
	if len(p.Concepts) == 0 {
		q := strings.Trim(strings.TrimSpace(requestPrefix.ReplaceAllString(s.Query, "")), "？?。")
		for _, part := range lexicalParts(q) {
			if part = strings.TrimSpace(part); part != "" {
				p.Concepts = append(p.Concepts, aliases(part))
			}
		}
		if len(p.Concepts) == 0 {
			p.Concepts = [][]string{aliases(s.Query)}
		}
		p.Method = "lexical"
		p.Warnings = []string{"词项规划只按显式分隔符切分；可提供 concept_groups 保留专业同义词。"}
	}
	if len(p.Concepts) > 8 {
		return p, fail("INVALID_PLAN", "概念组最多 8 组")
	}
	for i, g := range p.Concepts {
		if len(g) == 0 || len(g) > 6 {
			return p, fail("INVALID_PLAN", "每组须有 1–6 个同义词")
		}
		for _, x := range g {
			if strings.TrimSpace(x) == "" || utf8.RuneCountInString(x) > 80 {
				return p, fail("INVALID_PLAN", "检索词须为 1–80 字")
			}
		}
		p.Concepts[i] = uniqueStrings(g)
	}
	for i, field := range []string{"subject", "title", "keywords"} {
		p.Channels = append(p.Channels, Channel{ID: fmt.Sprintf("q%d", i+1), Expression: conceptExpression(field, p.Concepts), Weight: 1, Reason: field + "：完整概念组合"})
	}
	return p, nil
}
func conceptExpression(field string, groups [][]string) Expression {
	items := []Expression{}
	for _, group := range groups {
		alternatives := []Expression{}
		for _, term := range group {
			alternatives = append(alternatives, Expression{Field: field, Value: term, Match: "phrase"})
		}
		items = append(items, combine("or", alternatives))
	}
	return combine("and", items)
}
func combine(op string, x []Expression) Expression {
	if len(x) == 1 {
		return x[0]
	}
	return Expression{Op: op, Items: x}
}

// Compile converts an expression to bounded DNF in CNKI's QueryJson group shape,
// so NOT is applied to a condition and never confused with OR.
type literal struct {
	expression Expression
	negative   bool
}

func disjunction(e Expression, negative bool, depth int, nodes *int) ([][]literal, error) {
	*nodes++
	if depth > 8 || *nodes > 96 {
		return nil, fail("QUERY_TOO_COMPLEX", "表达式最多 96 节点、8 层")
	}
	if e.Op == "" {
		if nativeFields[e.Field] == "" || strings.TrimSpace(e.Value) == "" || utf8.RuneCountInString(e.Value) > 120 || len(e.Items) > 0 {
			return nil, fail("INVALID_CONDITION", "无效字段或检索词")
		}
		if !oneOf(e.Match, "", "phrase", "exact") {
			return nil, fail("INVALID_MATCH", "match 为 phrase 或 exact")
		}
		return [][]literal{{{expression: e, negative: negative}}}, nil
	}
	if e.Value != "" || e.Field != "" {
		return nil, fail("INVALID_EXPRESSION", "运算节点不能同时声明字段")
	}
	if e.Op == "not" {
		if len(e.Items) != 1 {
			return nil, fail("INVALID_NOT", "NOT 恰好需要一个子节点")
		}
		return disjunction(e.Items[0], !negative, depth+1, nodes)
	}
	if !oneOf(e.Op, "and", "or") || len(e.Items) < 2 || len(e.Items) > 12 {
		return nil, fail("INVALID_OPERATOR", "AND/OR 须有 2–12 个子节点")
	}
	or := (e.Op == "or") != negative
	result := [][]literal{{}}
	if or {
		result = nil
	}
	for _, child := range e.Items {
		part, err := disjunction(child, negative, depth+1, nodes)
		if err != nil {
			return nil, err
		}
		if or {
			result = append(result, part...)
		} else {
			if len(result)*len(part) > 32 {
				return nil, fail("QUERY_TOO_COMPLEX", "布尔展开超过 32 分支")
			}
			product := [][]literal{}
			for _, a := range result {
				for _, z := range part {
					product = append(product, append(append([]literal{}, a...), z...))
				}
			}
			result = product
		}
		if len(result) > 32 {
			return nil, fail("QUERY_TOO_COMPLEX", "布尔展开超过 32 分支")
		}
	}
	return result, nil
}
func Compile(e Expression) (map[string]any, error) {
	n := 0
	branches, err := disjunction(e, false, 0, &n)
	if err != nil {
		return nil, err
	}
	groups := []any{}
	for i, branch := range branches {
		var positives, negatives []literal
		for _, x := range branch {
			if x.negative {
				negatives = append(negatives, x)
			} else {
				positives = append(positives, x)
			}
		}
		if len(positives) == 0 {
			return nil, fail("UNBOUNDED_QUERY", "每个 OR 分支都需要正向检索条件")
		}
		items := []any{}
		for _, x := range append(positives, negatives...) {
			c := x.expression
			value := c.Value
			if strings.ContainsAny(value, "+*()/%=- \t\"'") {
				quote := "\""
				if strings.Contains(value, quote) {
					quote = "'"
				}
				if strings.Contains(value, quote) {
					return nil, fail("QUERY_UNSUPPORTED", "无法可靠引用含两种引号的词项")
				}
				value = quote + value + quote
			}
			operator := "FUZZY"
			if c.Match == "exact" {
				operator = "DEFAULT"
			}
			if c.Field == "subject" {
				if c.Match == "exact" {
					return nil, fail("QUERY_UNSUPPORTED", "主题不支持严格精确匹配；可选择篇名或关键词")
				}
				operator = "TOPRANK"
			}
			logic := 0
			if x.negative {
				logic = 2
			}
			items = append(items, map[string]any{"Key": "mcp", "Title": c.Field, "Logic": logic, "Field": nativeFields[c.Field], "Operator": operator, "Value": value, "Value2": "", "options": map[string]any{}})
		}
		logic := 0
		if i > 0 {
			logic = 1
		}
		groups = append(groups, map[string]any{"Key": "mcp", "Title": "", "Logic": logic, "Items": items, "ChildItems": []any{}})
	}
	return map[string]any{"Key": "Subject", "Title": "", "Logic": 0, "Items": []any{}, "ChildItems": groups}, nil
}

// anchor picks a positive term to type into the real search form when capturing the request template.
func anchor(e Expression) string {
	if e.Op == "" {
		return e.Value
	}
	for _, x := range e.Items {
		if x.Op != "not" {
			return anchor(x)
		}
	}
	return ""
}
