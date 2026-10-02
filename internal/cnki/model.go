package cnki

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const Version = "2.0.0"

// ParserVersion invalidates cached detail/journal observations when parsing changes.
const ParserVersion = "5"

type Problem struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
	Details   any    `json:"details,omitempty"`
}

func (e *Problem) Error() string      { return e.Code + ": " + e.Message }
func fail(code, message string) error { return &Problem{Code: code, Message: message} }

func asProblem(err error) *Problem {
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	if errors.Is(err, context.Canceled) {
		return &Problem{Code: "CANCELLED", Message: "任务已取消"}
	}
	if isInfrastructure(err) {
		return &Problem{Code: "BROWSER_UNAVAILABLE", Message: "浏览器连接中断；不代表论文无权限。可用 operation_control(action=recover) 重启浏览器", Retryable: true}
	}
	return &Problem{Code: "OPERATION_FAILED", Message: err.Error()}
}

// Infrastructure failures concern the browser transport, never paper access.
func isInfrastructure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var p *Problem
	if errors.As(err, &p) {
		return p.Code == "BROWSER_UNAVAILABLE"
	}
	var n net.Error
	if errors.As(err, &n) || errors.Is(err, io.EOF) {
		return true
	}
	text := strings.ToLower(err.Error())
	for _, s := range []string{"session with given id not found", "connection closed", "closed network connection", "websocket", "target closed", "browser has been closed", "connection reset", "broken pipe", "no target with given id"} {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}

// fatal errors stop a whole batch; anything else belongs to one record or channel.
func fatal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || isInfrastructure(err) {
		return true
	}
	switch asProblem(err).Code {
	case "BUDGET_EXHAUSTED", "RATE_LIMITED", "SITE_UNAVAILABLE", "VERIFICATION_TIMEOUT", "LOGIN_REQUIRED", "QUERY_TEMPLATE_UNAVAILABLE", "QUERY_TEMPLATE_UNSUPPORTED":
		return true
	}
	return false
}

type Quality struct {
	Canonical       string         `json:"canonical_title"`
	Basis           string         `json:"identity_basis"` // current_title, translated_title, former_title
	Tiers           []string       `json:"tiers,omitempty"`
	TierEvidence    []TierEvidence `json:"tier_evidence,omitempty"`
	CompositeImpact *float64       `json:"composite_impact,omitempty"`
	AggregateImpact *float64       `json:"aggregate_impact,omitempty"`
	MetricYear      int            `json:"metric_year,omitempty"`
	SourceURL       string         `json:"source_url,omitempty"`
	State           string         `json:"state"` // present: tiers or metrics observed; unknown: page had none
	ObservedAt      time.Time      `json:"observed_at"`
	Parser          string         `json:"parser"`
}
type TierEvidence struct {
	Tier string `json:"tier"`
	Text string `json:"text"`
}

type Paper struct {
	Ref          string    `json:"record_ref"`
	Title        string    `json:"title"`
	Authors      []string  `json:"authors,omitempty"`
	Source       string    `json:"source,omitempty"`
	SourceURL    string    `json:"source_url,omitempty"`
	Year         int       `json:"year,omitempty"`
	Date         string    `json:"publication_date,omitempty"`
	Type         string    `json:"document_type,omitempty"`
	DOI          string    `json:"doi,omitempty"`
	DBCode       string    `json:"dbcode,omitempty"`
	DBName       string    `json:"dbname,omitempty"`
	Filename     string    `json:"filename,omitempty"`
	URL          string    `json:"url,omitempty"` // public abstract link
	Locator      string    `json:"-"`             // raw site link used for fetching; never reported
	Abstract     string    `json:"abstract,omitempty"`
	Excerpt      string    `json:"excerpt,omitempty"`
	Keywords     []string  `json:"keywords,omitempty"`
	Institutions []string  `json:"institutions,omitempty"`
	Funds        []string  `json:"funds,omitempty"`
	Volume       string    `json:"volume,omitempty"`
	Issue        string    `json:"issue,omitempty"`
	Pages        string    `json:"pages,omitempty"`
	Cited        *int      `json:"cited,omitempty"`
	Downloaded   *int      `json:"downloaded,omitempty"`
	Quality      *Quality  `json:"quality,omitempty"`
	ListedAt     time.Time `json:"listed_at,omitzero"`
	DetailAt     time.Time `json:"detail_observed_at,omitzero"`
	DetailParser string    `json:"detail_parser,omitempty"`
}

func (p Paper) locator() string {
	if p.Locator != "" {
		return p.Locator
	}
	return p.URL
}

// detailFresh reports whether the detail page was already parsed recently enough
// that another request would only repeat identical abstract/keyword evidence.
func detailFresh(p Paper) bool {
	return p.DetailParser == ParserVersion && time.Since(p.DetailAt) < 30*24*time.Hour
}

// merge folds a newer observation of the same paper into the stored one.
// Values are only replaced by non-empty values; a listing never overrides the
// publication date or metadata that a detail page established.
func merge(old, fresh Paper) Paper {
	out := old
	detail := !fresh.DetailAt.IsZero()
	str := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	list := func(dst *[]string, v []string) {
		if len(v) > 0 {
			*dst = v
		}
	}
	if detail || old.DetailAt.IsZero() {
		str(&out.Title, fresh.Title)
		list(&out.Authors, fresh.Authors)
		str(&out.Source, fresh.Source)
		str(&out.Date, fresh.Date)
		if fresh.Year > 0 {
			out.Year = fresh.Year
		}
	}
	str(&out.SourceURL, fresh.SourceURL)
	str(&out.Type, fresh.Type)
	str(&out.DOI, fresh.DOI)
	str(&out.DBCode, fresh.DBCode)
	str(&out.DBName, fresh.DBName)
	str(&out.Filename, fresh.Filename)
	str(&out.URL, fresh.URL)
	str(&out.Locator, fresh.Locator)
	str(&out.Abstract, fresh.Abstract)
	str(&out.Excerpt, fresh.Excerpt)
	str(&out.Volume, fresh.Volume)
	str(&out.Issue, fresh.Issue)
	str(&out.Pages, fresh.Pages)
	list(&out.Keywords, fresh.Keywords)
	list(&out.Institutions, fresh.Institutions)
	list(&out.Funds, fresh.Funds)
	if fresh.Cited != nil {
		out.Cited = fresh.Cited
	}
	if fresh.Downloaded != nil {
		out.Downloaded = fresh.Downloaded
	}
	if fresh.Quality != nil {
		out.Quality = fresh.Quality
	}
	if fresh.ListedAt.After(out.ListedAt) {
		out.ListedAt = fresh.ListedAt
	}
	if fresh.DetailAt.After(out.DetailAt) {
		out.DetailAt, out.DetailParser = fresh.DetailAt, fresh.DetailParser
	}
	return out
}

func fingerprint(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func newID(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// Titles match without erasing meaningful C++, C#, A.B and version punctuation.
func normalize(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

var titleSpacing = regexp.MustCompile(`\s*([—–：:，,；;。！!？?（()）“”「」《》])\s*`)

func titleKey(s string) string { return titleSpacing.ReplaceAllString(normalize(s), "$1") }
func sourceKey(s string) string {
	return strings.NewReplacer("（", "(", "）", ")").Replace(normalize(strings.TrimRight(clean(s), " .。")))
}
func doiKey(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	for _, prefix := range []string{"https://doi.org/", "http://doi.org/", "doi:"} {
		s = strings.TrimPrefix(s, prefix)
	}
	return s
}
func nativeKey(p Paper) string {
	if p.Filename == "" || p.DBName == "" {
		return ""
	}
	return strings.ToUpper(p.DBName + ":" + p.Filename)
}
func bibliographyKey(p Paper) string {
	if p.Title == "" || len(p.Authors) == 0 || p.Source == "" || p.Year == 0 {
		return ""
	}
	return fingerprint([]any{titleKey(p.Title), normalize(strings.Join(p.Authors, "|")), normalize(p.Source), p.Year})
}
func identityKeys(p Paper) []string {
	out := []string{}
	if x := nativeKey(p); x != "" {
		out = append(out, "native:"+x)
	}
	if x := doiKey(p.DOI); x != "" {
		out = append(out, "doi:"+x)
	}
	if x := bibliographyKey(p); x != "" {
		out = append(out, "bib:"+x)
	}
	return out
}
func conflicting(a, b Paper) bool {
	return (a.DOI != "" && b.DOI != "" && doiKey(a.DOI) != doiKey(b.DOI)) ||
		(nativeKey(a) != "" && nativeKey(b) != "" && nativeKey(a) != nativeKey(b)) ||
		(a.DBCode != "" && b.DBCode != "" && !strings.EqualFold(a.DBCode, b.DBCode))
}

// SamePaper never merges on a normalized title alone; it needs a native
// identifier, DOI, or the full bibliography to agree.
func SamePaper(a, b Paper) bool {
	if conflicting(a, b) {
		return false
	}
	if a.DOI != "" && b.DOI != "" {
		return true
	}
	if nativeKey(a) != "" && nativeKey(b) != "" {
		return true
	}
	x, y := bibliographyKey(a), bibliographyKey(b)
	return x != "" && x == y
}

// A detail response is already bound to a verified locator; formatting may
// differ, but conflicting identifiers or a different first author never match.
func detailMatches(a, b Paper) bool {
	if conflicting(a, b) || titleKey(a.Title) != titleKey(b.Title) {
		return false
	}
	if len(a.Authors) > 0 && len(b.Authors) > 0 && normalize(a.Authors[0]) != normalize(b.Authors[0]) {
		return false
	}
	return a.Source == "" || b.Source == "" || sourceKey(a.Source) == sourceKey(b.Source)
}

// PublicURL keeps only https CNKI abstract locators without credential-like parameters.
func PublicURL(raw string) string {
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || !siteURL(u) {
		return ""
	}
	if !strings.Contains(strings.ToLower(u.Path), "abstract") {
		return ""
	}
	for k := range u.Query() {
		for _, s := range []string{"token", "ticket", "session", "password", "signature"} {
			if strings.Contains(strings.ToLower(k), s) {
				return ""
			}
		}
	}
	return u.String()
}

// siteURL accepts only https CNKI hosts (tests may also allow a local server).
var siteURL = func(u *url.URL) bool {
	return u.Scheme == "https" && (u.Hostname() == "cnki.net" || strings.HasSuffix(u.Hostname(), ".cnki.net"))
}

func uniqueStrings(x []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range x {
		s = strings.TrimSpace(s)
		k := normalize(s)
		if s != "" && !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
	}
	return out
}
