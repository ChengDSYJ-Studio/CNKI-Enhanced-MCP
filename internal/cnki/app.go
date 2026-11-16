// App wires the data root, store, operations and site together.

package cnki

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const searchCacheTTL = 10 * time.Minute

// App opens nothing until a tool needs it, so discovery and diagnostics stay
// instant and never start Chrome.
type App struct {
	Root   string
	Config Config
	mu     sync.Mutex
	closed bool
	Store  *Store
	Jobs   *Jobs
	Site   *Site
	wait   time.Duration
}

func NewApp(root string) (*App, error) {
	r, err := realPath(root)
	if err != nil {
		return nil, err
	}
	if err = claimDataRoot(r); err != nil {
		return nil, err
	}
	a := &App{Root: r, wait: time.Duration(envInt("CNKI_INLINE_WAIT_SECONDS", 40)) * time.Second}
	if raw, e := os.ReadFile(filepath.Join(r, "config.json")); e == nil {
		if json.Unmarshal(raw, &a.Config) != nil {
			return nil, fail("CONFIG_INVALID", "配置文件无法解析")
		}
		if a.Config.Application != "" && !ownsApplication(a.Config.Application) {
			return nil, fail("FOREIGN_DATA_DIRECTORY", "配置不属于知网服务")
		}
	}
	if p := os.Getenv("CNKI_BROWSER_PATH"); p != "" {
		a.Config.BrowserPath = p
	}
	if a.Config.BrowserPath == "" {
		if paths := BrowserCandidates(); len(paths) > 0 {
			a.Config.BrowserPath = paths[0]
		}
	}
	return a, nil
}

func (a *App) init() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return fail("SERVER_CLOSED", "知网服务已关闭")
	}
	if a.Store != nil {
		return nil
	}
	s, err := OpenStore(a.Root)
	if err != nil {
		return err
	}
	a.Store, a.Jobs = s, NewJobs(s)
	browserRoot := filepath.Join(a.Root, "browser")
	// 1.x kept the browser identity under profiles/default; keep users' logins.
	if _, err := os.Stat(browserRoot); os.IsNotExist(err) {
		_ = os.Rename(filepath.Join(a.Root, "profiles", "default"), browserRoot)
	}
	_ = os.Remove(filepath.Join(a.Root, "profiles")) // only when empty
	sweepIncoming(a.Root, s)
	a.Site = NewSite(NewBrowser(browserRoot, a.Config.BrowserPath, false), s)
	return nil
}

func (a *App) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	store := a.Store
	a.mu.Unlock()
	if store == nil {
		return nil
	}
	a.Jobs.Close()
	_ = a.Site.browser.Close()
	return store.Close()
}

// run starts a network operation owned by the calling client session and
// waits inline for a while; long work continues and is followed with status.
func (a *App) run(ctx context.Context, kind string, b Budget, prior *OpRecord, fn func(*Op) (any, error)) (any, error) {
	if err := a.init(); err != nil {
		return nil, err
	}
	op := a.Jobs.Start(sessionOwner(ctx), kind, b, prior, fn)
	return a.Jobs.Wait(ctx, op, a.wait), nil
}

func (a *App) Diagnostics() map[string]any {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	out := map[string]any{"application": Application, "version": Version, "platform": runtime.GOOS + "/" + runtime.GOARCH, "data_dir": a.Root, "process_id": os.Getpid(), "heap_bytes": memory.HeapAlloc,
		"browser_configured": a.Config.BrowserPath != "", "request_interval_ms": envInt("CNKI_REQUEST_INTERVAL_MS", 500), "max_inflight": envInt("CNKI_MAX_INFLIGHT", 6), "detail_workers": envInt("CNKI_WORKERS", 8), "inline_wait_seconds": int(a.wait.Seconds())}
	a.mu.Lock()
	store, site := a.Store, a.Site
	a.mu.Unlock()
	if store != nil {
		out["store"] = store.Stats()
		out["browser"] = site.browser.State()
		out["site_requests"] = site.requests.Load()
		out["verifications"] = site.epoch.Load()
	}
	return out
}
