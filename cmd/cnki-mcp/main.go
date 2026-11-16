package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ChengDSYJ-Studio/CNKI-Enhanced-MCP/internal/cnki"
)

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// Informational commands never create the data folder.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Println(cnki.Version)
			return nil
		case "skill":
			fmt.Print(cnki.SkillMarkdown)
			return nil
		case "install-skill":
			dir := ""
			if len(os.Args) > 2 {
				dir = os.Args[2]
			}
			path, e := cnki.InstallSkill(dir)
			if e == nil {
				fmt.Println("已安装 skill：", path, "及", filepath.Join(filepath.Dir(filepath.Dir(path)), "cnki-deep-research"))
			}
			return e
		}
	}
	app, err := cnki.NewApp(cnki.DefaultDataRoot())
	if err != nil {
		return err
	}
	defer app.Close()
	binary, _ := os.Executable()
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	encode := func(v any) error { e := json.NewEncoder(os.Stdout); e.SetIndent("", "  "); return e.Encode(v) }
	switch command {
	case "serve":
		go cnki.WatchParent(ctx, cancel, os.Getppid())
		return cnki.ServeStdio(ctx, app.Root, binary)
	case "worker":
		return cnki.RunWorker(ctx, app)
	case "setup":
		return cnki.Setup(ctx, binary, app.Root, os.Args[2:], os.Stdin, os.Stdout)
	case "config":
		client := "generic"
		if len(os.Args) > 2 {
			client = os.Args[2]
		}
		v, e := cnki.ConfigText(client, binary, app.Root)
		if e == nil {
			fmt.Print(v)
		}
		return e
	case "uninstall":
		if len(os.Args) != 3 {
			return fmt.Errorf("用法：cnki-mcp uninstall <client>；仅删除本服务配置，保留数据")
		}
		v, e := cnki.UninstallClient(os.Args[2])
		if e != nil {
			return e
		}
		return encode(v)
	case "schema":
		v, e := cnki.ExportContract(ctx, app)
		if e != nil {
			return e
		}
		return encode(v)
	case "doctor":
		v := app.Diagnostics()
		if len(os.Args) > 2 && os.Args[2] == "--protocol" {
			probe, stop := context.WithTimeout(ctx, 20*time.Second)
			defer stop()
			r, e := cnki.CheckProtocol(probe, binary, app.Root)
			if e != nil {
				return e
			}
			v["protocol"] = r
		}
		return encode(v)
	case "call":
		if len(os.Args) < 3 {
			return fmt.Errorf("用法：cnki-mcp call <工具名> [JSON 或 @参数文件]；未指定时从 stdin 读取")
		}
		var raw []byte
		if len(os.Args) > 3 {
			arg := os.Args[3]
			if strings.HasPrefix(arg, "@") {
				raw, err = os.ReadFile(strings.TrimPrefix(arg, "@"))
			} else {
				raw = []byte(arg)
			}
		} else {
			raw, err = io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
		}
		if err != nil {
			return err
		}
		if len(raw) == 0 {
			raw = []byte("{}")
		}
		go cnki.WatchParent(ctx, cancel, os.Getppid())
		v, e := cnki.CallShared(ctx, app, binary, cnki.ToolName(os.Args[2]), raw)
		if e != nil {
			return e
		}
		return encode(v)
	case "help", "--help", "-h":
		fmt.Println("CNKI MCP " + cnki.Version + "（macOS、Windows、Linux）\n用法：cnki-mcp setup | serve | config <client> | schema | doctor [--protocol] | call <tool> [JSON|@file] | skill | install-skill [skills目录] | uninstall <client> | version\n所有运行数据（登录、缓存、浏览器配置、下载）都在程序旁的 data/ 目录，可用 CNKI_DATA_DIR 改到别处；须为空目录或本服务已有的数据目录，不能属于其他应用。")
		fmt.Println("\n可选的 client（setup --client / config / uninstall）：")
		for _, c := range cnki.Clients {
			fmt.Printf("  %-16s %s\n", c.ID, c.Name)
		}
		return nil
	default:
		return fmt.Errorf("未知命令：%s", command)
	}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
