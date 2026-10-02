package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ChengDSYJ-Studio/CNKI-Enhanced-MCP/internal/cnki"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func run() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	root := os.Getenv("CNKI_DATA_DIR")
	if root == "" {
		root = filepath.Join(filepath.Dir(executable), "data")
	}
	app, err := cnki.NewApp(root)
	if err != nil {
		return err
	}
	defer app.Close()
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	switch command {
	case "setup":
		return cnki.Setup(ctx, executable, root, os.Args[2:], os.Stdin, os.Stdout)
	case "schema":
		result, e := cnki.ExportContract(ctx, app)
		if e != nil {
			return e
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	case "config":
		client := "generic"
		if len(os.Args) > 2 {
			client = os.Args[2]
		}
		text, e := cnki.ConfigText(client, executable, root)
		if e != nil {
			return e
		}
		fmt.Print(text)
		return nil
	case "help":
		fmt.Println("用法：cnki-mcp setup | serve | config <client> | schema | doctor [--protocol] | version")
		fmt.Println("\nsetup 参数：--browser <浏览器路径> --client <client> [--install-client]")
		fmt.Println("\n可选的 client：")
		for _, c := range cnki.Clients {
			fmt.Printf("  %-16s %s\n", c.ID, c.Name)
		}
		return nil
	case "serve":
		return cnki.Server(app).Run(ctx, &mcp.StdioTransport{})
	case "version":
		fmt.Println(cnki.Version)
		return nil
	case "doctor":
		data := app.Diagnostics()
		if len(os.Args) > 2 && os.Args[2] == "--protocol" {
			probeCtx, stop := context.WithTimeout(ctx, 15*time.Second)
			defer stop()
			result, e := cnki.CheckProtocol(probeCtx, executable)
			if e != nil {
				return e
			}
			data["protocol"] = result
		}
		return json.NewEncoder(os.Stdout).Encode(data)
	default:
		return fmt.Errorf("未知命令 %q", command)
	}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
