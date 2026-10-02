package cnki

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var Formats = []string{"gbt7714", "apa", "mla", "chicago", "vancouver", "bibtex", "ris", "endnote", "csl_json", "json", "csv", "markdown", "custom"}

type CitationResult struct {
	Format  string              `json:"format"`
	Text    string              `json:"text,omitempty"`
	Path    string              `json:"path,omitempty"`
	Count   int                 `json:"count"`
	Missing map[string][]string `json:"missing_fields,omitempty"`
}

func citationType(p Paper) (code, csl, bib, ris string) {
	switch {
	case strings.Contains(p.Type, "会议"):
		return "C", "paper-conference", "inproceedings", "CONF"
	case strings.Contains(p.Type, "博士"):
		return "D", "thesis", "phdthesis", "THES"
	case strings.Contains(p.Type, "硕士"):
		return "D", "thesis", "mastersthesis", "THES"
	case strings.Contains(p.Type, "学位"):
		return "D", "thesis", "misc", "THES"
	case strings.Contains(p.Type, "报纸"):
		return "N", "article-newspaper", "misc", "NEWS"
	case strings.Contains(p.Type, "期刊"):
		return "J", "article-journal", "article", "JOUR"
	case strings.Contains(p.Type, "图书"):
		return "M", "book", "book", "BOOK"
	default:
		return "Z", "document", "misc", "GEN"
	}
}
func bibText(s string) string {
	return strings.NewReplacer(`\`, `\textbackslash{}`, "{", `\{`, "}", `\}`, "%", `\%`, "&", `\&`, "#", `\#`, "_", `\_`, "$", `\$`).Replace(s)
}
func markdownText(s string) string {
	return strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`).Replace(s)
}
func citationTemplate(template string, values map[string]string) (string, error) {
	if len(template) > 5000 || strings.ContainsAny(template, "\x00\x01") {
		return "", fail("INVALID_TEMPLATE", "模板过长或包含无效字符")
	}
	var problem error
	escaped := strings.ReplaceAll(strings.ReplaceAll(template, "{{", "\x00"), "}}", "\x01")
	escaped = regexp.MustCompile(`\{([a-z_]+)\}`).ReplaceAllStringFunc(escaped, func(token string) string {
		key := token[1 : len(token)-1]
		v, ok := values[key]
		if !ok {
			problem = fail("INVALID_TEMPLATE", "不支持模板字段 "+key)
		}
		return strings.ReplaceAll(strings.ReplaceAll(v, "{", "\x00"), "}", "\x01")
	})
	if strings.ContainsAny(escaped, "{}") {
		problem = fail("INVALID_TEMPLATE", "模板仅支持 {title} 等字段，不执行表达式")
	}
	return strings.ReplaceAll(strings.ReplaceAll(escaped, "\x00", "{"), "\x01", "}"), problem
}
func Citations(papers []Paper, format, template string) (CitationResult, error) {
	if format == "" {
		format = "gbt7714"
	}
	if format == "gbt7714-2015" {
		format = "gbt7714"
	}
	if format == "csl-json" {
		format = "csl_json"
	}
	if !slices.Contains(Formats, format) {
		return CitationResult{}, fail("FORMAT_UNAVAILABLE", "不支持此引文格式")
	}
	out := CitationResult{Format: format, Count: len(papers), Missing: map[string][]string{}}
	for _, p := range papers {
		missing := []string{}
		if len(p.Authors) == 0 {
			missing = append(missing, "authors")
		}
		if p.Source == "" {
			missing = append(missing, "source")
		}
		if p.Year == 0 {
			missing = append(missing, "year")
		}
		if len(missing) > 0 {
			out.Missing[p.Ref] = missing
		}
	}
	if format == "json" {
		data, err := json.MarshalIndent(papers, "", "  ")
		out.Text = string(data)
		return out, err
	}
	if format == "csl_json" {
		items := []map[string]any{}
		for _, p := range papers {
			_, kind, _, _ := citationType(p)
			item := map[string]any{"id": p.Ref, "type": kind, "title": p.Title}
			authors := []map[string]string{}
			for _, name := range p.Authors {
				authors = append(authors, map[string]string{"literal": name})
			}
			if len(authors) > 0 {
				item["author"] = authors
			}
			if p.Year > 0 {
				item["issued"] = map[string]any{"date-parts": [][]int{{p.Year}}}
			}
			for k, v := range map[string]string{"container-title": p.Source, "volume": p.Volume, "issue": p.Issue, "page": p.Pages, "DOI": p.DOI, "URL": p.URL, "abstract": p.Abstract} {
				if v != "" {
					item[k] = v
				}
			}
			items = append(items, item)
		}
		data, err := json.MarshalIndent(items, "", "  ")
		out.Text = string(data)
		return out, err
	}
	if format == "csv" {
		var buffer bytes.Buffer
		writer := csv.NewWriter(&buffer)
		writer.UseCRLF = true
		_ = writer.Write([]string{"title", "authors", "source", "year", "volume", "issue", "pages", "doi", "url"})
		for _, p := range papers {
			year := ""
			if p.Year > 0 {
				year = strconv.Itoa(p.Year)
			}
			row := []string{p.Title, strings.Join(p.Authors, "; "), p.Source, year, p.Volume, p.Issue, p.Pages, p.DOI, p.URL}
			for i, v := range row {
				trim := strings.TrimLeft(v, " \t\r\n")
				if len(trim) > 0 && strings.ContainsRune("=+-@", rune(trim[0])) {
					row[i] = "'" + v
				}
			}
			_ = writer.Write(row)
		}
		writer.Flush()
		out.Text = buffer.String()
		return out, writer.Error()
	}
	parts := []string{}
	for i, p := range papers {
		code, _, bib, _ := citationType(p)
		year := "[年份未取得]"
		if p.Year > 0 {
			year = strconv.Itoa(p.Year)
		}
		authors := strings.Join(p.Authors, ", ")
		if authors == "" {
			authors = "[作者未取得]"
		}
		source := p.Source
		if source == "" {
			source = "[来源未取得]"
		}
		tail := p.Volume
		if p.Issue != "" {
			tail += "(" + p.Issue + ")"
		}
		if p.Pages != "" {
			tail += ": " + p.Pages
		}
		var text string
		if style, ok := citationStyles[format]; ok {
			text = fmt.Sprintf(style[0], i+1, authors, p.Title, code, source, year)
			if tail != "" {
				text += style[1] + tail
			}
			parts = append(parts, text+".")
			continue
		}
		switch format {
		case "bibtex":
			properties := [][2]string{{"title", p.Title}, {"year", func() string {
				if p.Year > 0 {
					return strconv.Itoa(p.Year)
				}
				return ""
			}()}, {"volume", p.Volume}, {"number", p.Issue}, {"pages", p.Pages}, {"abstract", p.Abstract}, {"keywords", strings.Join(p.Keywords, "; ")}, {"doi", p.DOI}, {"url", p.URL}}
			sourceField := "publisher"
			switch bib {
			case "article":
				sourceField = "journal"
			case "inproceedings":
				sourceField = "booktitle"
			case "phdthesis", "mastersthesis":
				sourceField = "school"
			}
			properties = append(properties, [2]string{sourceField, p.Source})
			lines := []string{"@" + bib + "{" + p.Ref + ","}
			if len(p.Authors) > 0 {
				names := []string{}
				for _, name := range p.Authors {
					names = append(names, "{"+bibText(name)+"}")
				}
				lines = append(lines, "  author = {"+strings.Join(names, " and ")+"},")
			}
			for _, kv := range properties {
				if kv[1] == "" {
					continue
				}
				v := bibText(kv[1])
				if kv[0] == "url" || kv[0] == "doi" {
					v = strings.NewReplacer("{", "%7B", "}", "%7D", "\n", "", "\r", "").Replace(kv[1])
				}
				lines = append(lines, "  "+kv[0]+" = {"+v+"},")
			}
			text = strings.Join(append(lines, "}"), "\n")
		case "ris", "endnote":
			text = taggedCitation(p, format)
		case "markdown":
			text = "- " + markdownText(p.Title)
			if p.URL != "" {
				text = "- [" + markdownText(p.Title) + "](" + p.URL + ")"
			}
		case "custom":
			var err error
			text, err = citationTemplate(template, map[string]string{"title": p.Title, "authors": authors, "source": p.Source, "journal": p.Source, "year": func() string {
				if p.Year > 0 {
					return year
				}
				return ""
			}(), "volume": p.Volume, "issue": p.Issue, "pages": p.Pages, "doi": p.DOI, "url": p.URL, "record_ref": p.Ref, "type": p.Type})
			if err != nil {
				return out, err
			}
		}
		parts = append(parts, text)
	}
	out.Text = strings.Join(parts, "\n\n")
	return out, nil
}

var citationStyles = map[string][2]string{
	"gbt7714":   {"[%[1]d] %[2]s. %[3]s[%[4]s]. %[5]s, %[6]s", ", "},
	"apa":       {"%[2]s. (%[6]s). %[3]s. %[5]s", ", "},
	"mla":       {"%[2]s. “%[3]s.” %[5]s, %[6]s", ", "},
	"chicago":   {"%[2]s. “%[3]s.” %[5]s (%[6]s)", ": "},
	"vancouver": {"%[1]d. %[2]s. %[3]s. %[5]s. %[6]s", ";"},
}

func taggedCitation(p Paper, format string) string {
	code, _, _, ris := citationType(p)
	kind := map[string]string{"J": "Journal Article", "D": "Thesis", "C": "Conference Paper", "N": "Newspaper Article", "M": "Book"}[code]
	if kind == "" {
		kind = "Generic"
	}
	year := ""
	if p.Year > 0 {
		year = strconv.Itoa(p.Year)
	}
	pages := strings.Split(p.Pages, "-")
	fields := []struct {
		ris, end string
		values   []string
	}{
		{"TI", "%T", []string{p.Title}}, {"AU", "%A", p.Authors}, {"JO", "%J", []string{p.Source}}, {"PY", "%D", []string{year}},
		{"VL", "%V", []string{p.Volume}}, {"IS", "%N", []string{p.Issue}},
		{"SP", "", []string{pages[0]}}, {"EP", "", nil}, {"", "%P", []string{p.Pages}},
		{"DO", "%R", []string{p.DOI}}, {"UR", "%U", []string{p.URL}}, {"AB", "%X", []string{p.Abstract}}, {"KW", "%K", p.Keywords},
	}
	if len(pages) > 1 {
		fields[7].values = []string{pages[len(pages)-1]}
	}
	lines := []string{"%0 " + kind}
	if format == "ris" {
		lines = []string{"TY  - " + ris}
	}
	for _, field := range fields {
		key, separator := field.end, " "
		if format == "ris" {
			key, separator = field.ris, "  - "
		}
		if key != "" {
			for _, v := range field.values {
				if v != "" {
					lines = append(lines, key+separator+clean(v))
				}
			}
		}
	}
	if format == "ris" {
		lines = append(lines, "ER  -")
	}
	return strings.Join(lines, "\n")
}
