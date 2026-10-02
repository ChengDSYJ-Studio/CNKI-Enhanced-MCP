package cnki

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// Browser owns one dedicated Chrome profile. Cookies and logins persist in the
// profile; work tabs are background targets in a single minimized window.
type Browser struct {
	mu         sync.Mutex
	root       string
	executable string
	headless   bool
	client     *rod.Browser
	launcher   *launcher.Launcher
	generation uint64
}

func NewBrowser(root, executable string, headless bool) *Browser {
	return &Browser{root: root, executable: executable, headless: headless}
}

// Client returns a live connection, launching Chrome when needed.
func (b *Browser) Client() (*rod.Browser, uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client != nil {
		return b.client, b.generation, nil
	}
	if b.executable == "" {
		return nil, 0, fail("BROWSER_NOT_CONFIGURED", "运行 cnki-mcp setup 选择浏览器")
	}
	profile := filepath.Join(b.root, "profile")
	if err := os.MkdirAll(profile, 0700); err != nil {
		return nil, 0, err
	}
	if data, e := os.ReadFile(filepath.Join(profile, "DevToolsActivePort")); e == nil {
		port := strings.SplitN(string(data), "\n", 2)[0]
		if conn, e := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), 200*time.Millisecond); e == nil {
			conn.Close()
			return nil, 0, fail("BROWSER_PROFILE_IN_USE", "独立浏览器身份仍在使用，请关闭旧服务后重试")
		}
	}
	// Restored tabs are stale work pages; cookies and login storage stay.
	_ = os.RemoveAll(filepath.Join(profile, "Default", "Sessions"))
	l := launcher.New().Bin(b.executable).UserDataDir(profile).Headless(b.headless).Leakless(false).
		RemoteDebuggingPort(0).Set("no-startup-window").Set("start-minimized").
		Set("disable-background-networking").Set("no-first-run").Set("no-default-browser-check").
		Delete("disable-background-timer-throttling").Delete("disable-renderer-backgrounding")
	endpoint, err := l.Launch()
	if err != nil {
		return nil, 0, fmt.Errorf("启动独立浏览器: %w", err)
	}
	client := rod.New().ControlURL(endpoint)
	if err = client.Connect(); err != nil {
		l.Kill()
		return nil, 0, &Problem{Code: "BROWSER_UNAVAILABLE", Message: err.Error(), Retryable: true}
	}
	b.client, b.launcher = client, l
	b.generation++
	return client, b.generation, nil
}

// Reset discards a broken connection; the next Client call relaunches Chrome.
func (b *Browser) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeLocked()
}
func (b *Browser) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeLocked()
	return nil
}
func (b *Browser) closeLocked() {
	if b.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if b.client.Context(ctx).Close() != nil && b.launcher != nil {
		b.launcher.Kill()
	}
	b.client, b.launcher = nil, nil
}
func (b *Browser) State() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	pid := 0
	if b.launcher != nil {
		pid = b.launcher.PID()
	}
	return map[string]any{"running": b.client != nil, "process_id": pid, "profile_directory": b.root, "connection_generation": b.generation}
}

// NewPage opens a background tab without stealing focus.
func (b *Browser) NewPage(ctx context.Context) (*rod.Page, error) {
	client, _, err := b.Client()
	if err != nil {
		return nil, err
	}
	params := map[string]any{"url": "about:blank", "background": true, "focus": false}
	if !b.headless {
		targets, e := (proto.TargetGetTargets{}).Call(client.Context(ctx))
		if e != nil {
			return nil, e
		}
		hasPage := false
		for _, t := range targets.TargetInfos {
			hasPage = hasPage || t.Type == proto.TargetTargetInfoTypePage
		}
		if !hasPage {
			params["newWindow"], params["windowState"], params["width"], params["height"] = true, "minimized", 1280, 900
		}
	}
	data, err := client.Call(ctx, "", "Target.createTarget", params)
	if err != nil {
		return nil, err
	}
	var created struct {
		TargetID proto.TargetTargetID `json:"targetId"`
	}
	if err = json.Unmarshal(data, &created); err != nil {
		return nil, err
	}
	p, err := client.PageFromTarget(created.TargetID)
	if err != nil {
		return nil, err
	}
	if !b.headless {
		if err = b.Window(ctx, p, "minimized", false); err != nil {
			_ = p.Close()
			return nil, err
		}
	}
	return p, nil
}

// Window sets and confirms a window state; only human steps activate a tab.
func (b *Browser) Window(ctx context.Context, p *rod.Page, state string, activate bool) error {
	if b.headless {
		if activate {
			return fail("DISPLAY_UNAVAILABLE", "此浏览器没有可显示窗口")
		}
		return nil
	}
	client, _, err := b.Client()
	if err != nil {
		return err
	}
	c := client.Context(ctx)
	window, err := (proto.BrowserGetWindowForTarget{TargetID: p.TargetID}).Call(c)
	if err != nil {
		return err
	}
	if window.Bounds.WindowState == proto.BrowserWindowStateFullscreen && state != "fullscreen" {
		if err = (proto.BrowserSetWindowBounds{WindowID: window.WindowID, Bounds: &proto.BrowserBounds{WindowState: proto.BrowserWindowStateNormal}}).Call(c); err != nil {
			return err
		}
	}
	if err = (proto.BrowserSetWindowBounds{WindowID: window.WindowID, Bounds: &proto.BrowserBounds{WindowState: proto.BrowserWindowState(state)}}).Call(c); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		r, e := (proto.BrowserGetWindowBounds{WindowID: window.WindowID}).Call(c)
		if e != nil {
			return e
		}
		if string(r.Bounds.WindowState) == state {
			break
		}
		if time.Now().After(deadline) {
			return fail("WINDOW_STATE_UNCONFIRMED", "浏览器没有确认所需窗口状态")
		}
		if err = sleep(ctx, 30*time.Millisecond); err != nil {
			return err
		}
	}
	if err = (proto.EmulationSetFocusEmulationEnabled{Enabled: state == "minimized"}).Call(p.Context(ctx)); err != nil {
		return err
	}
	if activate {
		return (proto.TargetActivateTarget{TargetID: p.TargetID}).Call(c)
	}
	return nil
}
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type Response struct {
	Status int    `json:"status"`
	URL    string `json:"url"`
	Body   string `json:"body"`
}

// Fetch runs inside the page's origin, so Chrome supplies cookies and headers.
// Each call has its own AbortController; concurrent fetches never cancel each other.
func (b *Browser) Fetch(ctx context.Context, p *rod.Page, target, body string) (Response, error) {
	var out Response
	id := newID("f")
	r, err := p.Context(ctx).Eval(`async (url,body,id) => {
 const ctl=new AbortController();(globalThis.__cnkiAborts??={})[id]=ctl;
 const timer=setTimeout(()=>ctl.abort(),30000);
 try {
  const response=await fetch(url,{method:body?'POST':'GET',credentials:'include',signal:ctl.signal,
    headers:body?{'Content-Type':'application/x-www-form-urlencoded; charset=UTF-8','X-Requested-With':'XMLHttpRequest'}:{},body:body||undefined});
  const reader=response.body.getReader(),decoder=new TextDecoder();let bytes=0,text='';
  for(;;){const part=await reader.read();if(part.done)break;bytes+=part.value.length;
    if(bytes>8388608){await reader.cancel();throw new Error('RESPONSE_TOO_LARGE');}text+=decoder.decode(part.value,{stream:true});}
  text+=decoder.decode();return {status:response.status,url:response.url,body:text};
 } finally {clearTimeout(timer);delete globalThis.__cnkiAborts[id];}
}`, target, body, id)
	if err != nil {
		if ctx.Err() != nil {
			abort, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, _ = p.Context(abort).Eval(`id=>globalThis.__cnkiAborts?.[id]?.abort()`, id)
		}
		return out, err
	}
	return out, r.Value.Unmarshal(&out)
}

// Resource loads any URL through Chrome's network stack (cookies included)
// without rendering; it works cross-origin and concurrently on one frame.
func (b *Browser) Resource(ctx context.Context, p *rod.Page, target string) (Response, error) {
	result, err := (proto.NetworkLoadNetworkResource{FrameID: p.FrameID, URL: target, Options: &proto.NetworkLoadNetworkResourceOptions{IncludeCredentials: true}}).Call(p.Context(ctx))
	if err != nil {
		return Response{}, err
	}
	r := result.Resource
	out := Response{URL: target}
	if r.HTTPStatusCode != nil {
		out.Status = int(*r.HTTPStatusCode)
	}
	if r.Stream == "" {
		return out, &Problem{Code: "RESOURCE_FAILED", Message: fmt.Sprintf("浏览器资源读取失败: HTTP %d %s", out.Status, r.NetErrorName), Retryable: true}
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = (proto.IOClose{Handle: r.Stream}).Call(p.Context(cleanup))
	}()
	var text strings.Builder
	size := 1 << 20
	for {
		chunk, e := (proto.IORead{Handle: r.Stream, Size: &size}).Call(p.Context(ctx))
		if e != nil {
			return out, e
		}
		data := []byte(chunk.Data)
		if chunk.Base64Encoded {
			if data, e = base64.StdEncoding.DecodeString(chunk.Data); e != nil {
				return out, e
			}
		}
		if text.Len()+len(data) > 8<<20 {
			return out, fail("RESPONSE_TOO_LARGE", "资源超过 8 MiB")
		}
		text.Write(data)
		if chunk.EOF {
			break
		}
	}
	out.Body = text.String()
	return out, nil
}

type ClickPoint struct{ X, Y float64 }

// PrepareClick finds a visible, stable, hit-testable control matching pattern.
func (b *Browser) PrepareClick(ctx context.Context, p *rod.Page, selector, pattern string, sameTab bool) (ClickPoint, error) {
	var point ClickPoint
	r, err := p.Context(ctx).Eval(`async (selector,pattern,sameTab) => {
 const regex=new RegExp(pattern,'i');
 const candidate=[...document.querySelectorAll(selector)].find(e=>{
  const r=e.getBoundingClientRect(),s=getComputedStyle(e);
  return regex.test(e.innerText||e.value||'')&&r.width>0&&r.height>0&&s.display!='none'&&s.visibility!='hidden';});
 if(!candidate)throw new Error('CONTROL_NOT_FOUND');
 candidate.scrollIntoView({block:'center',inline:'center',behavior:'instant'});
 for(let attempt=0;attempt<60;attempt++){
  const a=candidate.getBoundingClientRect();await new Promise(resolve=>setTimeout(resolve,50));
  const z=candidate.getBoundingClientRect(),x=z.x+z.width/2,y=z.y+z.height/2,hit=document.elementFromPoint(x,y);
  if(Math.abs(a.x-z.x)>1||Math.abs(a.y-z.y)>1||candidate.disabled||candidate.getAttribute('aria-disabled')==='true'||!hit||!candidate.contains(hit))continue;
  if(sameTab&&candidate.tagName==='A')candidate.target='_self';return {x,y};
 }
 throw new Error('CONTROL_NOT_ACTIONABLE');
}`, selector, pattern, sameTab)
	if err != nil {
		if strings.Contains(err.Error(), "CONTROL_NOT_FOUND") {
			return point, fail("ENTRY_NOT_FOUND", "页面没有可见的目标入口；不代表文献没有访问权限")
		}
		return point, err
	}
	return point, r.Value.Unmarshal(&point)
}
func (b *Browser) DispatchClick(ctx context.Context, p *rod.Page, point ClickPoint) error {
	c := p.Context(ctx)
	for _, e := range []proto.InputDispatchMouseEvent{
		{Type: proto.InputDispatchMouseEventTypeMouseMoved, X: point.X, Y: point.Y},
		{Type: proto.InputDispatchMouseEventTypeMousePressed, X: point.X, Y: point.Y, Button: proto.InputMouseButtonLeft, ClickCount: 1},
		{Type: proto.InputDispatchMouseEventTypeMouseReleased, X: point.X, Y: point.Y, Button: proto.InputMouseButtonLeft, ClickCount: 1},
	} {
		if err := e.Call(c); err != nil {
			return err
		}
	}
	return nil
}
func (b *Browser) Click(ctx context.Context, p *rod.Page, selector, pattern string, sameTab bool) error {
	point, err := b.PrepareClick(ctx, p, selector, pattern, sameTab)
	if err != nil {
		return err
	}
	return b.DispatchClick(ctx, p, point)
}

// pageGate is one observation of what a page shows: challenges, login, denial,
// readiness and the KNS identity header.
type pageGate struct {
	Challenge     bool     `json:"challenge"`
	Login         bool     `json:"login"`
	Denied        bool     `json:"denied"`
	Ready         bool     `json:"ready"`
	Marker        string   `json:"marker"`
	IdentityReady bool     `json:"identity_ready"`
	Complete      bool     `json:"complete"`
	Pending       bool     `json:"pending"`
	Personal      bool     `json:"personal"`
	Institutions  []string `json:"institutions"`
}

const inspectScript = `()=>{
 const visible=e=>{const r=e.getBoundingClientRect();for(let p=e;p;p=p.parentElement){const s=getComputedStyle(p);if(s.display==='none'||s.visibility==='hidden'||+s.opacity===0)return false;}return r.width>0&&r.height>0&&r.bottom>0&&r.right>0&&r.top<innerHeight&&r.left<innerWidth;};
 const any=s=>[...document.querySelectorAll(s)].some(visible);
 const text=document.body?.innerText||'';
 const headers=[...document.querySelectorAll('.ecp_header_login_area,header,.header,.topbar,.top-bar,.topLogin')].filter(visible);
 const institutions=[...document.querySelectorAll('.ecp_header_unitName,.org-name,.institution-name,[data-institution-name],#Ecp_top_orgName')].filter(visible).map(e=>e.textContent.trim()).filter(Boolean);
 const account=[...document.querySelectorAll('a,button')].filter(visible);
 return {complete:document.readyState==='complete',pending:!!window.jQuery?.active,identity_ready:headers.some(e=>/登录|退出|注册/.test(e.innerText))||institutions.length>0,personal:any('.ecp_header_personalName_loginbg')||account.some(e=>/^(退出登录|安全退出|退出账号|logout|sign out)$/i.test(e.innerText.trim())),institutions,marker:globalThis.__cnkiNavigation||'',ready:document.readyState!=='loading',challenge:[...document.querySelectorAll('iframe')].some(e=>visible(e)&&/captcha|verify/i.test(e.src+' '+e.title))||any('#tCaptchaDyMainWrap,.captcha-dialog,.geetest_panel,[id^=nc_][id$=_wrapper],.tcaptcha-transform')||(any('.read-btn-boxXml .slider-wrapper')&&/滑动验证|继续阅读全文/.test(document.querySelector('.read-btn-boxXml')?.innerText||''))||(any('.verifycode #vericode')&&any('.verifycode #changeVercode'))||document.title.trim()==='安全验证',
 login:(location.hostname.startsWith('login.')&&any('input[type=password]'))||[...document.querySelectorAll('iframe')].some(e=>visible(e)&&/login|passport|sso/i.test(e.src)),denied:/机构未订购|无权访问|您没有.{0,8}权限/.test(text)&&!document.querySelector('#paperRead,#ChDivSummary,table.result-table-list')};
}`

func inspectPage(ctx context.Context, p *rod.Page) (pageGate, error) {
	var g pageGate
	r, err := p.Context(ctx).Eval(inspectScript)
	if err != nil {
		return g, err
	}
	return g, r.Value.Unmarshal(&g)
}
