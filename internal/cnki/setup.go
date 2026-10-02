package cnki

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func BrowserCandidates() []string {
	home, _ := os.UserHomeDir()
	paths := []string{}
	if value := os.Getenv("CNKI_BROWSER_PATH"); value != "" {
		paths = append(paths, value)
	}
	switch runtime.GOOS {
	case "darwin":
		for _, base := range []string{"/Applications", filepath.Join(home, "Applications")} {
			for _, name := range []string{"Google Chrome", "Microsoft Edge", "Chromium", "Google Chrome for Testing"} {
				paths = append(paths, filepath.Join(base, name+".app", "Contents/MacOS", name))
			}
		}
	case "windows":
		for _, base := range []string{os.Getenv("PROGRAMFILES"), os.Getenv("PROGRAMFILES(X86)"), os.Getenv("LOCALAPPDATA")} {
			if base != "" {
				paths = append(paths, filepath.Join(base, "Google/Chrome/Application/chrome.exe"), filepath.Join(base, "Microsoft/Edge/Application/msedge.exe"))
			}
		}
	default:
		for _, name := range []string{"google-chrome-stable", "google-chrome", "chromium", "chromium-browser", "microsoft-edge"} {
			if path, err := exec.LookPath(name); err == nil {
				paths = append(paths, path)
			}
		}
	}
	out := []string{}
	for _, path := range uniqueStrings(paths) {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			out = append(out, path)
		}
	}
	return out
}

// openLink hands a custom-scheme import link to the operating system.
func openLink(link string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", link).Run()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", link).Run()
	default:
		return exec.Command("xdg-open", link).Run()
	}
}
func Setup(ctx context.Context, binary, root string, args []string, in io.Reader, out io.Writer) error {
	browser, client, apply := "", "", false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser", "--client":
			if i+1 >= len(args) {
				return fail("INVALID_ARGUMENT", "参数缺少值")
			}
			i++
			if args[i-1] == "--browser" {
				browser = args[i]
			} else {
				client = args[i]
			}
		case "--install-client":
			apply = true
		default:
			return fail("INVALID_ARGUMENT", "未知 setup 参数 "+args[i])
		}
	}
	scanner := bufio.NewScanner(in)
	ask := func(prompt string) string {
		fmt.Fprint(out, prompt)
		if scanner.Scan() {
			return strings.TrimSpace(scanner.Text())
		}
		return ""
	}
	interactive := len(args) == 0
	fmt.Fprintln(out, "CNKI MCP 安装设置（无需 Python、Node 或 Go）")
	if browser == "" {
		paths := BrowserCandidates()
		if len(paths) > 0 {
			for i, path := range paths {
				fmt.Fprintf(out, "  %d. %s\n", i+1, path)
			}
			selection := "1"
			if interactive {
				selection = ask("浏览器编号，回车使用 1；也可输入完整路径： ")
			}
			if selection == "" {
				selection = "1"
			}
			if n, e := strconv.Atoi(selection); e == nil && n >= 1 && n <= len(paths) {
				browser = paths[n-1]
			} else {
				browser = strings.Trim(selection, "\"")
			}
		} else if interactive {
			browser = strings.Trim(ask("请输入 Chrome、Edge 或 Chromium 可执行文件的完整路径： "), "\"")
		}
	}
	if browser == "" {
		return fail("BROWSER_NOT_FOUND", "没有找到浏览器；用 --browser 指定已安装 Chrome/Edge 路径")
	}
	browser, err := filepath.Abs(browser)
	if err != nil {
		return err
	}
	if info, e := os.Stat(browser); e != nil || info.IsDir() {
		return fail("BROWSER_NOT_FOUND", "浏览器可执行文件不存在")
	}
	probe, err := os.MkdirTemp("", "cnki-browser-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(probe)
	b := NewBrowser(probe, browser, true)
	probeCtx, stop := context.WithTimeout(ctx, 20*time.Second)
	_, err = b.NewPage(probeCtx)
	stop()
	_ = b.Close()
	if err != nil {
		return fmt.Errorf("浏览器检查未通过: %w", err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(Config{BrowserPath: browser}, "", "  ")
	if err = os.WriteFile(filepath.Join(root, "config.json"), data, 0600); err != nil {
		return err
	}
	if client == "" {
		if interactive {
			for i, c := range Clients {
				mode := "生成配置片段"
				if clientPath(c.ID) != "" {
					mode = "可自动写入"
				}
				fmt.Fprintf(out, "  %2d. %-30s %s\n", i+1, c.Name, mode)
			}
			choice := ask("选择 MCP 客户端编号（回车为通用 JSON）： ")
			if n, e := strconv.Atoi(choice); e == nil && n >= 1 && n <= len(Clients) {
				client = Clients[n-1].ID
			}
		}
		if client == "" {
			client = "generic"
		}
	}
	spec, ok := clientSpec(client)
	if !ok {
		return fail("UNKNOWN_CLIENT", "不支持此客户端；运行 cnki-mcp help 查看可选客户端")
	}
	text, err := ConfigText(client, binary, root)
	if err != nil {
		return err
	}
	snippet := filepath.Join(root, "cnki-"+client+"."+spec.extension())
	if err = os.WriteFile(snippet, []byte(text), 0600); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%s\n配置片段：%s\n", text, snippet)
	path := clientPath(client)
	if interactive && path != "" {
		answer := ask("写入 " + path + " 并备份原配置？[Y/n] ")
		apply = answer == "" || strings.EqualFold(answer, "y")
	}
	if link := ImportLink(client, binary, root); link != "" {
		fmt.Fprintf(out, "\n一键导入链接（在已安装 %s 的电脑上打开，客户端会请你确认）：\n%s\n", spec.Name, link)
		open := apply
		if interactive {
			answer := ask("现在打开这个链接？[Y/n] ")
			open = answer == "" || strings.EqualFold(answer, "y")
		}
		if open {
			if err = openLink(link); err != nil {
				fmt.Fprintln(out, "无法自动打开链接，请手动复制到浏览器或客户端：", err)
			}
		}
		apply = false
	}
	if path == "" && spec.Import != "" {
		fmt.Fprintf(out, "也可以在 %s 中粘贴上面的配置。\n", spec.Import)
	}
	if apply {
		if path == "" {
			return fail("CLIENT_IMPORT_REQUIRED", "此客户端请手动导入已生成的配置片段")
		}
		if err = installConfig(client, path, text); err != nil {
			return err
		}
		fmt.Fprintln(out, "客户端配置已更新：", path)
	}
	result, err := CheckProtocol(ctx, binary)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "安装检查通过：%v 个 MCP 工具。请重启或重新连接客户端。\n", result["tools"])
	return nil
}
