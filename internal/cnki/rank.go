package cnki

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

type Hit struct {
	Channel  string  `json:"channel"`
	Position int     `json:"position"`
	Weight   float64 `json:"weight"`
}
type Scores struct {
	Relevance       float64 `json:"relevance"`
	Quality         float64 `json:"quality"`
	Title           float64 `json:"title_match"`
	Abstract        float64 `json:"abstract_match"`
	Keywords        float64 `json:"keyword_match"`
	RRF             float64 `json:"rrf"`
	ConceptCoverage float64 `json:"concept_coverage"`
	SourceTier      float64 `json:"source_tier"`
	Impact          float64 `json:"impact"`
	AnnualCitations float64 `json:"annual_citations"`
	ResourceType    float64 `json:"resource_type"`
	QualityKnown    bool    `json:"quality_evidence_present"`
}
type Ranked struct {
	Paper    Paper   `json:"record"`
	Score    float64 `json:"score"`
	Eligible bool    `json:"relevance_eligible"`
	Evidence Scores  `json:"evidence"`
}

var latinToken = regexp.MustCompile(`[a-z0-9]+(?:[+._/-][a-z0-9]+)*`)

// tokenSet uses Latin tokens plus Han bigrams so scoring never needs a segmenter.
func tokenSet(text string) map[string]bool {
	text = strings.ToLower(text)
	out := map[string]bool{}
	for _, t := range latinToken.FindAllString(text, -1) {
		out[t] = true
	}
	var previous rune
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			if previous != 0 {
				out[string([]rune{previous, r})] = true
			}
			previous = r
		} else {
			previous = 0
		}
	}
	return out
}
func f1(query, document map[string]bool) float64 {
	if len(query) == 0 || len(document) == 0 {
		return 0
	}
	n := 0
	for t := range query {
		if document[t] {
			n++
		}
	}
	return 2 * float64(n) / float64(len(query)+len(document))
}
func conceptEvidence(groups [][]string, p Paper) float64 {
	if len(groups) == 0 {
		return 0
	}
	text := normalize(p.Title + " " + strings.Join(p.Keywords, " ") + " " + p.Abstract)
	matched := 0
	for _, group := range groups {
		for _, alias := range group {
			if strings.Contains(text, normalize(alias)) {
				matched++
				break
			}
		}
	}
	return float64(matched) / float64(len(groups))
}
func sourceQuality(p Paper, year int) (tier, impact, cited, kind float64, known bool) {
	if q := p.Quality; q != nil && q.State == "present" && q.Parser == ParserVersion {
		known = true
		for _, tag := range q.Tiers {
			u := strings.ToUpper(tag)
			v := 0.0
			switch {
			case strings.Contains(u, "CSSCI"):
				v = .96
			case u == "SSCI", u == "SCI", u == "SCI-E", u == "A&HCI":
				v = 1
			case strings.Contains(u, "北大核心"), strings.Contains(u, "CSCD"):
				v = .94
			case u == "EI":
				v = .9
			case strings.Contains(u, "AMI") && strings.Contains(u, "核心"):
				v = .88
			}
			tier = math.Max(tier, v)
		}
		metric := q.CompositeImpact
		if metric == nil {
			metric = q.AggregateImpact
		}
		if metric != nil {
			impact = 1 - math.Exp(-math.Max(0, *metric)/2)
		}
	}
	if p.Cited != nil && p.Year > 0 && p.Year <= year {
		known = true
		age := max(1, year-p.Year+1)
		cited = 1 - math.Exp(-float64(max(0, *p.Cited))/float64(age)/3)
	}
	switch {
	case strings.Contains(p.Type, "博士"):
		kind = .72
	case strings.Contains(p.Type, "硕士"):
		kind = .46
	case strings.Contains(p.Type, "本科"):
		kind = .1
	case strings.Contains(p.Type, "期刊"):
		kind = .55
	case strings.Contains(p.Type, "会议"):
		kind = .52
	case strings.Contains(p.Type, "报纸"):
		kind = .14
	}
	return
}

// Rank scores relevance and source quality separately; quality only reorders relevant papers.
func Rank(papers []Paper, plan Plan, hits map[string][]Hit, year int) []Ranked {
	if year == 0 {
		year = time.Now().Year()
	}
	terms := []string{}
	for _, g := range plan.Concepts {
		terms = append(terms, g...)
	}
	query := tokenSet(strings.Join(terms, " "))
	out := make([]Ranked, 0, len(papers))
	for _, p := range papers {
		e := Scores{Title: f1(query, tokenSet(p.Title)), Abstract: f1(query, tokenSet(p.Abstract)), Keywords: f1(query, tokenSet(strings.Join(p.Keywords, " "))), ConceptCoverage: conceptEvidence(plan.Concepts, p)}
		channels := map[string]bool{}
		for _, hit := range hits[p.Ref] {
			if hit.Position > 0 && !channels[hit.Channel] {
				weight := hit.Weight
				if weight == 0 {
					weight = 1
				}
				e.RRF += weight / (60 + float64(hit.Position))
				channels[hit.Channel] = true
			}
		}
		e.Relevance = (.32*e.Title + .28*e.Abstract + .12*e.Keywords + .20*math.Min(e.RRF*20, 1) + .08*math.Min(float64(len(channels))/3, 1) + .12*e.ConceptCoverage) / 1.12
		e.SourceTier, e.Impact, e.AnnualCitations, e.ResourceType, e.QualityKnown = sourceQuality(p, year)
		e.Quality = .45*e.SourceTier + .25*e.Impact + .20*e.AnnualCitations + .10*e.ResourceType
		eligible := e.Relevance >= .25 && e.ConceptCoverage >= .5
		out = append(out, Ranked{Paper: p, Score: e.Relevance*(.35+.65*e.Quality) + .20*e.Quality, Eligible: eligible, Evidence: e})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Eligible != b.Eligible {
			return a.Eligible
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.Paper.Ref < b.Paper.Ref
	})
	return out
}

// EnhancementSize grows the detail pool sub-linearly with the candidate count.
func EnhancementSize(candidates int) int {
	n := 80
	switch {
	case candidates <= 30:
		n = candidates
	case candidates <= 100:
		n = 40
	case candidates <= 300:
		n = 60
	}
	return min(candidates, n)
}
func Select(ranked []Ranked, limit int) []Ranked {
	out := []Ranked{}
	if len(ranked) == 0 {
		return out
	}
	if limit <= 0 {
		limit = 50
	}
	floor := math.Max(.24, ranked[0].Score*.55)
	for _, r := range ranked {
		if !r.Eligible || r.Score < floor {
			continue
		}
		q, rel := r.Evidence.Quality, r.Evidence.Relevance
		// Unknown quality is not proof of poor research: keep highly relevant evidence.
		if !(rel >= .35 && r.Evidence.ConceptCoverage == 1 || q >= .45 || (q >= .25 && rel >= .35) || (!r.Evidence.QualityKnown && rel >= .55)) {
			continue
		}
		if out = append(out, r); len(out) == limit {
			break
		}
	}
	return out
}

// Feedback proposes up to two extra channels from keywords shared by relevant, enriched papers.
func Feedback(ranked []Ranked, plan Plan) []Channel {
	out := []Channel{}
	if len(plan.Concepts) == 0 {
		return out
	}
	frequency, examples, original := map[string]int{}, map[string]string{}, map[string]bool{}
	for _, g := range plan.Concepts {
		for _, a := range g {
			original[normalize(a)] = true
		}
	}
	for _, r := range ranked[:min(len(ranked), 10)] {
		if !r.Eligible || len(r.Paper.Keywords) == 0 {
			continue
		}
		seen := map[string]bool{}
		for _, keyword := range r.Paper.Keywords {
			key := normalize(keyword)
			n := len([]rune(key))
			if n < 2 || n > 30 || original[key] || seen[key] {
				continue
			}
			seen[key] = true
			frequency[key]++
			examples[key] = keyword
		}
	}
	keys := []string{}
	for k, count := range frequency {
		if count >= 2 {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if frequency[keys[i]] != frequency[keys[j]] {
			return frequency[keys[i]] > frequency[keys[j]]
		}
		return keys[i] < keys[j]
	})
	anchors := conceptExpression("subject", plan.Concepts)
	for i, k := range keys[:min(len(keys), 2)] {
		out = append(out, Channel{ID: fmt.Sprintf("feedback%d", i+1), Weight: .8, Expression: combine("and", []Expression{anchors, {Field: "subject", Value: examples[k], Match: "phrase"}}), Reason: "保留所有必要概念，补充相关论文共同关键词：" + examples[k]})
	}
	return out
}
