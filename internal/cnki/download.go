package cnki

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

type DownloadInput struct {
	Ref            string `json:"record_ref,omitempty"`
	Title          string `json:"title,omitempty" jsonschema:"完整题名（与 record_ref 二选一）"`
	Format         string `json:"format,omitempty" jsonschema:"auto（默认，优先 PDF）、pdf、caj"`
	Filename       string `json:"filename,omitempty"`
	Subdirectory   string `json:"subdirectory,omitempty" jsonschema:"数据目录 downloads/ 下的子目录"`
	WaitSeconds    int    `json:"wait_seconds,omitempty" jsonschema:"等待传输完成的秒数，默认 120"`
	Retry          bool   `json:"retry,omitempty" jsonschema:"上次下载点击结果未知时，确认允许再次点击"`
	OnVerification string `json:"on_verification,omitempty" jsonschema:"ask（默认）或 skip"`
}

// Artifact is recorded before the download click, so an unknown outcome is
// never silently replayed (CNKI may charge per download).
type Artifact struct {
	State       string    `json:"state"` // clicked, completed
	Ref         string    `json:"record_ref"`
	Title       string    `json:"title"`
	Format      string    `json:"format"`
	Folder      string    `json:"folder"`
	Name        string    `json:"name"`
	Path        string    `json:"path,omitempty"`
	Bytes       int64     `json:"bytes,omitempty"`
	SHA256      string    `json:"sha256,omitempty"`
	GUID        string    `json:"download_guid,omitempty"`
	Incoming    string    `json:"incoming,omitempty"`
	OperationID string    `json:"operation_id"`
	Source      string    `json:"source,omitempty"`
	Updated     time.Time `json:"updated_at"`
}

var downloadGUID = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
var refusal = regexp.MustCompile(`余额不足|未订购|无权|没有权限|暂无权限|请先登录|下载失败|下载异常|超出.{0,6}限`)

func safeTitleFile(title string) string {
	title = strings.Trim(strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`/\<>:"|?*`, r) {
			return '_'
		}
		return r
	}, title), ". ")
	if runes := []rune(title); len(runes) > 75 {
		title = string(runes[:75])
	}
	if title == "" || reservedName.MatchString(title) {
		title = "论文_" + title
	}
	return title
}

func (a *App) Download(ctx context.Context, in DownloadInput) (any, error) {
	if (in.Ref == "") == (in.Title == "") {
		return nil, fail("INVALID_INPUT", "record_ref 与完整 title 二选一")
	}
	if in.Format == "" {
		in.Format = "auto"
	}
	if in.WaitSeconds == 0 {
		in.WaitSeconds = 120
	}
	if !oneOf(in.Format, "auto", "pdf", "caj") || in.WaitSeconds < 1 || in.WaitSeconds > 900 || !oneOf(in.OnVerification, "", "ask", "skip") {
		return nil, fail("INVALID_INPUT", "format、wait_seconds 或 on_verification 无效")
	}
	if in.Filename != "" {
		if err := safeName(in.Filename); err != nil {
			return nil, err
		}
	}
	for _, part := range strings.Split(filepath.ToSlash(in.Subdirectory), "/") {
		if part != "" {
			if err := safeName(part); err != nil {
				return nil, err
			}
		}
	}
	if err := a.init(); err != nil {
		return nil, err
	}
	ref := in.Ref
	if ref == "" {
		if local := a.Store.ByTitle(in.Title); len(local) == 1 {
			ref = local[0].Ref
		}
	}
	if ref != "" {
		if art, done, err := a.priorDownload(ref, in); done || err != nil {
			return art, err
		}
	}
	return a.run(ctx, "download", Budget{MaxRequests: 30, TimeoutSeconds: 1800, OnVerification: in.OnVerification}, nil, func(op *Op) (any, error) {
		var p Paper
		var err error
		if ref != "" {
			p, _ = a.Store.Paper(ref)
		} else if p, err = a.resolve(op, in.Title); err != nil {
			return nil, err
		} else if art, done, err := a.priorDownload(p.Ref, in); done || err != nil {
			return art, err
		}
		art, err := a.Site.Download(op, a.Root, p, in)
		if art.State == "" {
			return nil, err // nothing was clicked
		}
		return art, err
	})
}

// priorDownload returns a verified earlier file, finishes a late transfer, or
// refuses to click again after an uncertain attempt unless retry is set.
func (a *App) priorDownload(ref string, in DownloadInput) (any, bool, error) {
	formats := []string{in.Format}
	if in.Format == "auto" {
		formats = []string{"pdf", "caj"}
	}
	for _, f := range formats {
		art, ok := a.Store.Artifact(ref + ":" + f)
		if !ok {
			continue
		}
		if art.State == "clicked" {
			if done, err := finalize(a.Root, &art); err == nil && done {
				a.Store.PutArtifact(ref+":"+f, art)
			}
		}
		if art.State == "completed" {
			if info, err := os.Stat(art.Path); err == nil && info.Size() == art.Bytes {
				if hash, _ := fileHash(art.Path); hash == art.SHA256 {
					art.Source = "cache"
					return map[string]any{"status": "completed", "result": art}, true, nil
				}
			}
			continue // file moved or changed: download again
		}
		if !in.Retry {
			return nil, true, &Problem{Code: "SIDE_EFFECT_UNCERTAIN", Message: "上次下载已点击但结果未知；确认需要再次下载时传 retry=true", Details: art}
		}
	}
	return nil, false, nil
}

func (s *Site) Download(op *Op, root string, p Paper, in DownloadInput) (Artifact, error) {
	s.downloadMu.Lock()
	defer s.downloadMu.Unlock()
	page, bound, err := s.document(op, p)
	if err != nil {
		return Artifact{}, err
	}
	defer closePage(page)
	format := in.Format
	if format == "auto" {
		r, e := page.Context(op.ctx).Eval(`()=>[...document.querySelectorAll('a')].filter(e=>e.getBoundingClientRect().width>0).map(e=>e.textContent).join(' ')`)
		if e != nil {
			return Artifact{}, e
		}
		switch text := r.Value.Str(); {
		case strings.Contains(text, "PDF下载") || strings.Contains(text, "下载PDF"):
			format = "pdf"
		case strings.Contains(text, "CAJ下载") || strings.Contains(text, "下载CAJ"):
			format = "caj"
		default:
			return Artifact{}, fail("DOWNLOAD_ENTRY_NOT_FOUND", "页面没有可确认的 PDF 或 CAJ 入口；未点击")
		}
	}
	name := in.Filename
	if name == "" {
		name = safeTitleFile(bound.Title) + "_" + strings.TrimPrefix(p.Ref, "rec_")[:min(8, len(strings.TrimPrefix(p.Ref, "rec_")))] + "." + format
	}
	if !strings.HasSuffix(strings.ToLower(name), "."+format) {
		return Artifact{}, fail("INVALID_FILENAME", "文件扩展名必须与格式一致")
	}
	folder := filepath.Join("downloads", in.Subdirectory)
	if _, err = os.Lstat(filepath.Join(root, folder, name)); err == nil {
		return Artifact{}, fail("FILE_EXISTS", "目标文件已存在，未点击下载")
	}
	if err = s.settle(op, page); err != nil {
		return Artifact{}, err
	}
	pattern := map[string]string{"pdf": "PDF下载|下载PDF", "caj": "CAJ下载|下载CAJ"}[format]
	point, err := s.browser.PrepareClick(op.ctx, page, "a", pattern, false)
	if err != nil {
		return Artifact{}, err
	}
	client, _, err := s.browser.Client()
	if err != nil {
		return Artifact{}, err
	}
	incoming := filepath.Join("incoming", op.ID())
	if err = os.MkdirAll(filepath.Join(root, incoming), 0700); err != nil {
		return Artifact{}, err
	}
	if err = (proto.BrowserSetDownloadBehavior{Behavior: proto.BrowserSetDownloadBehaviorBehaviorAllowAndName, DownloadPath: filepath.Join(root, incoming), EventsEnabled: true}).Call(client.Context(op.ctx)); err != nil {
		return Artifact{}, err
	}
	ctx, cancel := context.WithCancel(op.ctx)
	defer cancel()
	type event struct {
		guid, state string
	}
	events := make(chan event, 256)
	watch, err := s.watchPopups(op, page)
	if err != nil {
		return Artifact{}, err
	}
	defer watch.close(page)
	go client.Context(ctx).EachEvent(func(e *proto.BrowserDownloadWillBegin) {
		select {
		case events <- event{guid: e.GUID, state: "begin"}:
		case <-ctx.Done():
		}
	}, func(e *proto.BrowserDownloadProgress) {
		if e.State != proto.BrowserDownloadProgressStateInProgress {
			select {
			case events <- event{guid: e.GUID, state: string(e.State)}:
			case <-ctx.Done():
			}
		}
	})()
	key := p.Ref + ":" + format
	art := Artifact{State: "clicked", Ref: p.Ref, Title: bound.Title, Format: format, Folder: folder, Name: name, Incoming: incoming, OperationID: op.ID()}
	s.store.PutArtifact(key, art)
	if err = s.do(op, func() error { return s.browser.DispatchClick(op.ctx, page, point) }); err != nil {
		return art, err
	}
	op.stage("waiting_for_download", nil)
	deadline := time.Now().Add(time.Duration(in.WaitSeconds) * time.Second)
	for {
		select {
		case e := <-events:
			if art.GUID == "" && e.state == "begin" {
				art.GUID = e.guid
				s.store.PutArtifact(key, art)
				op.stage("transferring", nil)
			}
			if e.guid != art.GUID {
				continue
			}
			switch e.state {
			case "completed":
				done, err := finalize(root, &art)
				if err != nil {
					return art, err
				}
				if done {
					s.store.PutArtifact(key, art)
					return art, nil
				}
			case "canceled":
				return art, fail("DOWNLOAD_CANCELLED", "浏览器报告传输取消；未重复点击")
			}
		case <-op.ctx.Done():
			return art, op.ctx.Err()
		case <-time.After(time.Second):
			if time.Now().After(deadline) {
				return art, &Problem{Code: "SIDE_EFFECT_UNCERTAIN", Message: "等待结束仍未完成下载；稍后用相同参数调用会先对账晚到文件，不会重复点击", Retryable: true}
			}
			if art.GUID != "" {
				continue
			}
			// The click may land on a login, verification or refusal page (often in
			// a new tab); keep waiting for the same download and never click again.
			for _, tab := range watch.tabs(page) {
				g, err := inspectPage(op.ctx, tab)
				if err != nil {
					continue // closed or navigating
				}
				if handled, err := s.gate(op, tab, g); err != nil {
					return art, err
				} else if handled {
					deadline = time.Now().Add(time.Duration(in.WaitSeconds) * time.Second)
					break
				}
				if tab == page {
					continue
				}
				r, err := tab.Context(op.ctx).Eval(`()=>({url:location.href,text:(document.body?.innerText||'').replace(/\s+/g,' ').slice(0,300)})`)
				if err != nil {
					continue
				}
				url, text := r.Value.Get("url").Str(), r.Value.Get("text").Str()
				op.stage("download_tab", map[string]any{"url": url, "text": text})
				if refusal.MatchString(text) {
					return art, &Problem{Code: "DOWNLOAD_REFUSED", Message: "知网未提供文件：" + text, Details: map[string]any{"url": url}}
				}
			}
		}
	}
}

// finalize verifies the transferred file (Chrome names it by GUID only when
// complete) and archives it under its destination name.
func finalize(root string, art *Artifact) (bool, error) {
	if art.GUID == "" || !downloadGUID.MatchString(art.GUID) {
		return false, nil
	}
	path := filepath.Join(root, art.Incoming, art.GUID)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 100 {
		return false, fail("INVALID_FILE", "下载文件不完整")
	}
	magic := make([]byte, 8)
	if _, err = io.ReadFull(file, magic); err != nil {
		return false, err
	}
	valid := strings.HasPrefix(string(magic), "%PDF-")
	if art.Format == "caj" {
		valid = strings.HasPrefix(string(magic), "CAJ") || strings.HasPrefix(string(magic), "KDH") || strings.HasPrefix(string(magic), "HN")
	}
	if !valid {
		return false, fail("INVALID_FILE", "下载内容与请求格式不一致（可能是错误页面）")
	}
	if art.Format == "pdf" {
		tail := make([]byte, min(2048, info.Size()))
		if _, err = file.ReadAt(tail, info.Size()-int64(len(tail))); err != nil && err != io.EOF {
			return false, err
		}
		if !strings.Contains(string(tail), "%%EOF") {
			return false, fail("INVALID_FILE", "PDF 缺少结束标记")
		}
	}
	if _, err = file.Seek(0, 0); err != nil {
		return false, err
	}
	dest, size, hash, err := writeArtifact(root, art.Folder, art.Name, file)
	if err != nil {
		return false, err
	}
	file.Close()
	_ = os.Remove(path)
	_ = os.Remove(filepath.Dir(path)) // only succeeds when the incoming folder is empty
	art.State, art.Path, art.Bytes, art.SHA256, art.Source = "completed", dest, size, hash, "download"
	return true, nil
}
