// One shared backend per data root: each stdio client bridges to it over an
// authenticated loopback socket; it exits after the last client disconnects.

package cnki

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Each stdio session has its own protocol connection to one backend, which
// alone owns the jar, browser, job manager, database and site pacing.
type endpoint struct {
	Port   int    `json:"port"`
	Secret string `json:"secret"`
}

const endpointFile = ".cnki-endpoint.json"

func dialWorker(ctx context.Context, root string) (net.Conn, error) {
	raw, err := os.ReadFile(filepath.Join(root, endpointFile))
	if err != nil {
		return nil, err
	}
	var ep endpoint
	if json.Unmarshal(raw, &ep) != nil || len(ep.Secret) != 64 || ep.Port <= 0 || ep.Port > 65535 {
		return nil, errors.New("invalid local endpoint")
	}
	d := net.Dialer{Timeout: time.Second}
	conn, err := d.DialContext(ctx, "tcp4", fmt.Sprintf("127.0.0.1:%d", ep.Port))
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	mode := "t"
	switch os.Getenv("CNKI_MCP_OUTPUT") {
	case "structured":
		mode = "s"
	case "dual":
		mode = "d"
	}
	if _, err = io.WriteString(conn, ep.Secret+mode); err == nil {
		var ack [1]byte
		if _, err = io.ReadFull(conn, ack[:]); err == nil && ack[0] == 1 {
			_ = conn.SetDeadline(time.Time{})
			return conn, nil
		}
	}
	conn.Close()
	return nil, errors.New("local backend authentication failed")
}

func connectWorker(ctx context.Context, root, binary string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if conn, err := dialWorker(ctx, root); err == nil {
		return conn, nil
	}
	// Only the elected launcher starts a child. Other frontends wait for its
	// authenticated endpoint. A crashed launcher releases its OS file lock.
	var lock *os.File
	defer func() {
		if lock != nil {
			unlockDirectory(lock)
		}
	}()
	var child chan error
	for ctx.Err() == nil {
		if conn, e := dialWorker(ctx, root); e == nil {
			return conn, nil
		}
		if lock == nil {
			lock, _ = lockDirectory(filepath.Join(root, ".cnki-launch.lock"))
		}
		if lock != nil && child == nil {
			cmd := exec.Command(binary, "worker")
			cmd.Env = append(os.Environ(), "CNKI_DATA_DIR="+root)
			detachWorker(cmd)
			if e := cmd.Start(); e != nil {
				return nil, e
			}
			child = make(chan error, 1)
			go func(ch chan error) { ch <- cmd.Wait() }(child)
		}
		select {
		case <-ctx.Done():
		case <-child:
			child = nil
			if e := sleep(ctx, 150*time.Millisecond); e != nil {
				return nil, e
			}
		case <-time.After(60 * time.Millisecond):
		}
	}
	return nil, fail("BACKEND_UNAVAILABLE", "本地知网后端未能启动或连接")
}

type sessionConnection struct {
	net.Conn
	stop context.CancelFunc
}

func (c sessionConnection) Read(p []byte) (int, error) {
	n, e := c.Conn.Read(p)
	if e != nil {
		c.stop()
	}
	return n, e
}

// RunWorker exits after its last local connection disappears. No scheduled
// task, login item or system-wide service is installed.
func RunWorker(ctx context.Context, a *App) error {
	lock, e := lockDirectory(filepath.Join(a.Root, ".cnki-worker.lock"))
	if e != nil {
		return e
	}
	defer unlockDirectory(lock)
	listener, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		return e
	}
	defer listener.Close()
	var secret [32]byte
	if _, e = rand.Read(secret[:]); e != nil {
		return e
	}
	ep := endpoint{Port: listener.Addr().(*net.TCPAddr).Port, Secret: hex.EncodeToString(secret[:])}
	path := filepath.Join(a.Root, endpointFile)
	if e = atomicJSON(path, ep); e != nil {
		return e
	}
	defer os.Remove(path)
	ctx, shutdown := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { shutdown(); wg.Wait(); a.Close() }()
	var count atomic.Int32
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	for ctx.Err() == nil {
		_ = listener.SetDeadline(time.Now().Add(250 * time.Millisecond))
		conn, err := listener.AcceptTCP()
		if err != nil {
			var ne net.Error
			if !errors.As(err, &ne) || !ne.Timeout() {
				return err
			}
			if count.Load() == 0 && time.Since(time.Unix(0, last.Load())) >= 2*time.Second {
				return nil
			}
			continue
		}
		count.Add(1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { last.Store(time.Now().UnixNano()); count.Add(-1) }()
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			var key [65]byte
			if _, err := io.ReadFull(conn, key[:]); err != nil || subtle.ConstantTimeCompare(key[:64], []byte(ep.Secret)) != 1 {
				return
			}
			if _, err := conn.Write([]byte{1}); err != nil {
				return
			}
			_ = conn.SetDeadline(time.Time{})
			mode := "text"
			switch key[64] {
			case 's':
				mode = "structured"
			case 'd':
				mode = "dual"
			case 't':
			default:
				return
			}
			life, cancel := context.WithCancel(context.WithValue(ctx, outputModeKey{}, mode))
			defer cancel()
			closeOnExit := context.AfterFunc(life, func() { conn.Close() })
			defer closeOnExit()
			stream := sessionConnection{Conn: conn, stop: cancel}
			_ = sessionServer(a, life).Run(life, &mcp.IOTransport{Reader: stream, Writer: stream})
		}()
	}
	return nil
}

// Bridge EOF on either pipe (including a killed host) closes the backend
// socket immediately. The other direction may still be blocked on stdin.
func ServeStdio(ctx context.Context, root, binary string) error {
	conn, e := connectWorker(ctx, root, binary)
	if e != nil {
		return e
	}
	defer conn.Close()
	done := make(chan error, 2)
	go func() { _, e := io.Copy(conn, os.Stdin); done <- e }()
	go func() { _, e := io.Copy(os.Stdout, conn); done <- e }()
	select {
	case <-ctx.Done():
		return nil
	case e := <-done:
		return e
	}
}

// Client processes such as GUI hosts can leave the pipe's write end open in
// a helper. Watch the launching parent independently of the stdio transport.
func WatchParent(ctx context.Context, stop context.CancelFunc, parent int) {
	if parent <= 1 {
		stop()
		return
	}
	host := desktopAncestor(parent)
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if os.Getppid() != parent || !processAlive(parent) || (host > 0 && !processAlive(host)) {
				stop()
				return
			}
		}
	}
}

func CallShared(ctx context.Context, a *App, binary, name string, raw json.RawMessage) (any, error) {
	conn, e := connectWorker(ctx, a.Root, binary)
	if e != nil {
		return nil, e
	}
	defer conn.Close()
	cs, e := mcp.NewClient(&mcp.Implementation{Name: "cnki-cli", Version: Version}, nil).Connect(ctx, &mcp.IOTransport{Reader: conn, Writer: conn}, nil)
	if e != nil {
		return nil, e
	}
	defer cs.Close()
	poll := name != "status"
	for {
		out, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: raw})
		if e != nil {
			return nil, e
		}
		var b []byte
		if out.StructuredContent != nil {
			b, e = json.Marshal(out.StructuredContent)
		} else if len(out.Content) > 0 {
			if t, ok := out.Content[0].(*mcp.TextContent); ok {
				b = []byte(t.Text)
			}
		}
		if e != nil {
			return nil, e
		}
		if out.IsError {
			return nil, fmt.Errorf("知网: %s", b)
		}
		if len(b) == 0 || b[0] != '{' {
			return json.RawMessage(b), nil
		}
		var state struct {
			ID      string `json:"operation_id"`
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(b, &state); err != nil {
			return nil, err
		}
		if state.ID == "" || !oneOf(state.Status, "running", "awaiting_user") || !poll {
			return json.RawMessage(b), nil
		}
		if state.Message != "" {
			fmt.Fprintln(os.Stderr, state.Message)
		}
		name = "status"
		raw, _ = json.Marshal(StatusArgs{OperationID: state.ID, WaitSeconds: 55})
	}
}
