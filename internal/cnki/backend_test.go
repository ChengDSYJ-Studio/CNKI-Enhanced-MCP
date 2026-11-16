package cnki

import (
	"context"
	"os"
	"testing"
	"time"
)

// The last socket closing must also reap an already-running Chrome, even
// if the client never sent MCP shutdown or explicitly recovered the browser.
func TestLastSessionReapsOwnedBrowser(t *testing.T) {
	bin := os.Getenv("CNKI_TEST_BROWSER")
	if bin == "" {
		t.Skip("requires Chromium")
	}
	root := t.TempDir()
	a, e := NewApp(root)
	if e != nil {
		t.Fatal(e)
	}
	a.Config.BrowserPath = bin
	if e = a.init(); e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, e = a.Site.browser.NewPage(ctx); e != nil {
		t.Fatal(e)
	}
	pid := a.Site.browser.launcher.PID()
	done := make(chan error, 1)
	go func() { done <- RunWorker(ctx, a) }()
	for {
		conn, e := dialWorker(ctx, root)
		if e == nil {
			conn.Close()
			break
		}
		if err := sleep(ctx, 20*time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("backend did not exit")
	}
	if processAlive(pid) {
		t.Fatal("owned browser process is still alive")
	}
	reopened, e := OpenStore(root)
	if e != nil {
		t.Fatal("database lock survived backend", e)
	}
	reopened.Close()
}
