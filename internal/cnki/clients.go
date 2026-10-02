package cnki

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ClientSpec describes how one MCP client stores local stdio servers.
type ClientSpec struct {
	ID     string
	Name   string
	Format string         // json (key → {cnki: entry}), toml (Codex), dsh (DeepSeek Harness YAML patch)
	Key    string         // JSON key holding the servers, dotted for nesting (e.g. "mcp.servers"); default "mcpServers"
	Extra  map[string]any // fields added to the server entry
	Path   func(home string) string
	// NeedDir: only write when the config folder already exists, because the
	// client creates it from its own template on first run.
	NeedDir bool
	Link    func(entry map[string]any) string // one-click import link, when the client offers one
	Import  string                            // where to paste the snippet when it cannot be written automatically
}

func appData(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support")
	case "windows":
		if v := os.Getenv("APPDATA"); v != "" {
			return v
		}
		return filepath.Join(home, "AppData", "Roaming")
	default:
		if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
			return v
		}
		return filepath.Join(home, ".config")
	}
}
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
func inHome(rel string) func(string) string {
	return func(home string) string { return filepath.Join(home, rel) }
}

func dshHome(home string) string { return envOr("DSH_HOME", filepath.Join(home, ".dsh")) }
func b64(v any) string           { data, _ := json.Marshal(v); return base64.StdEncoding.EncodeToString(data) }

// Clients lists every client setup can configure. Paths and formats follow
// each client's documentation; a nil Path means the client is configured by
// importing the generated snippet (or opening Link) in its own settings.
var Clients = []ClientSpec{
	{ID: "claude-desktop", Name: "Claude Desktop", Format: "json", Path: func(home string) string {
		if runtime.GOOS == "linux" {
			return ""
		}
		return filepath.Join(appData(home), "Claude", "claude_desktop_config.json")
	}},
	{ID: "claude-code", Name: "Claude Code", Format: "json", Extra: map[string]any{"type": "stdio"}, Path: func(home string) string {
		return filepath.Join(envOr("CLAUDE_CONFIG_DIR", home), ".claude.json")
	}},
	{ID: "codex", Name: "Codex", Format: "toml", Path: func(home string) string {
		return filepath.Join(envOr("CODEX_HOME", filepath.Join(home, ".codex")), "config.toml")
	}},
	{ID: "cursor", Name: "Cursor", Format: "json", Extra: map[string]any{"type": "stdio"}, Path: inHome(".cursor/mcp.json")},
	{ID: "gemini", Name: "Gemini CLI", Format: "json", Extra: map[string]any{"timeout": 900000}, Path: inHome(".gemini/settings.json")},
	{ID: "dsh-desktop", Name: "DeepSeek Harness 桌面版", Format: "dsh", NeedDir: true, Path: func(home string) string {
		return filepath.Join(dshHome(home), "profiles", "desktop", "cordis.patch.yml")
	}, Import: "先运行一次 DeepSeek Harness 桌面版，再重新执行 setup"},
	{ID: "dsh-web", Name: "DeepSeek Harness 网页版（dsh web）", Format: "dsh", NeedDir: true, Path: func(home string) string {
		return filepath.Join(dshHome(home), "profiles", "web", "cordis.patch.yml")
	}, Import: "先运行一次 dsh web，再重新执行 setup"},
	{ID: "workbuddy", Name: "WorkBuddy", Format: "json", Path: inHome(".workbuddy/mcp.json")},
	{ID: "zcode", Name: "ZCode", Format: "json", Key: "mcp.servers", Extra: map[string]any{"type": "stdio"}, Path: inHome(".zcode/cli/config.json")},
	{ID: "kimi-code", Name: "Kimi Code（CLI 与桌面版）", Format: "json", Path: func(home string) string {
		return filepath.Join(envOr("KIMI_CODE_HOME", filepath.Join(home, ".kimi-code")), "mcp.json")
	}},
	{ID: "codebuddy", Name: "CodeBuddy CLI", Format: "json", Extra: map[string]any{"type": "stdio"}, Path: inHome(".codebuddy/.mcp.json")},
	{ID: "qwen-code", Name: "Qwen Code", Format: "json", Extra: map[string]any{"timeout": 900000}, Path: inHome(".qwen/settings.json")},
	{ID: "qoder-cli", Name: "Qoder CLI", Format: "json", Extra: map[string]any{"type": "stdio"}, Path: inHome(".qoder/settings.json")},
	{ID: "qoder", Name: "Qoder IDE", Format: "json", Link: func(e map[string]any) string {
		data, _ := json.Marshal(e)
		inner := base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(string(data))))
		return "qoder://aicoding.aicoding-deeplink/mcp/add?name=cnki&config=" + url.QueryEscape(inner)
	}, Import: "Qoder 设置 → MCP → My Servers → + Add"},
	{ID: "trae", Name: "Trae", Format: "json", Link: func(e map[string]any) string {
		return "trae://trae.ai-ide/mcp-import?type=stdio&name=cnki&config=" + url.QueryEscape(b64(e))
	}, Import: "Trae 设置 → MCP → 添加 → 手动添加"},
	{ID: "trae-cn", Name: "Trae CN", Format: "json", Import: "Trae CN 设置 → MCP → 添加 → 手动添加"},
	{ID: "lingma", Name: "通义灵码", Format: "json", Import: "通义灵码 个人设置 → MCP 服务 → + → 配置文件添加"},
	{ID: "codebuddy-ide", Name: "CodeBuddy IDE", Format: "json", Extra: map[string]any{"type": "stdio"}, Import: "CodeBuddy 设置 → MCP → Add MCP"},
	{ID: "comate", Name: "文心快码 Comate", Format: "json", Extra: map[string]any{"type": "stdio"}, Import: "文心快码的 MCP 设置（mcp.json）"},
	{ID: "chatbox", Name: "Chatbox", Format: "json", Link: func(e map[string]any) string {
		server := map[string]any{"name": "cnki"}
		for k, v := range e {
			server[k] = v
		}
		return "chatbox://mcp/install?server=" + url.QueryEscape(b64(server))
	}, Import: "Chatbox 设置 → MCP → 添加服务器"},
	{ID: "cherry-studio", Name: "Cherry Studio", Format: "json", Link: func(e map[string]any) string {
		return "cherrystudio://mcp/install?servers=" + url.QueryEscape(b64(map[string]any{"mcpServers": map[string]any{"cnki": e}}))
	}, Import: "Cherry Studio 设置 → MCP 服务器 → 从 JSON 导入"},
	{ID: "vscode", Name: "VS Code / GitHub Copilot", Format: "json", Key: "servers", Extra: map[string]any{"type": "stdio"}, Import: "VS Code 命令面板 “MCP: Open User Configuration”"},
	{ID: "windsurf", Name: "Windsurf", Format: "json", Import: "Windsurf 的 MCP 配置（mcp_config.json）"},
	{ID: "cline", Name: "Cline", Format: "json", Extra: map[string]any{"timeout": 900, "disabled": false}, Import: "Cline 的 MCP Servers → Configure"},
	{ID: "roo", Name: "Roo Code", Format: "json", Extra: map[string]any{"timeout": 900, "disabled": false}, Import: "Roo Code 的 MCP 设置 → Edit Global MCP"},
	{ID: "lobehub", Name: "LobeHub / LobeChat", Format: "json", Import: "LobeHub 插件设置 → 自定义 MCP → 导入 JSON"},
	{ID: "generic", Name: "其他 stdio 客户端（通用 JSON）", Format: "json", Import: "客户端的 MCP 设置"},
}

func clientSpec(id string) (ClientSpec, bool) {
	for _, c := range Clients {
		if c.ID == id {
			return c, true
		}
	}
	return ClientSpec{}, false
}
func clientPath(id string) string {
	c, ok := clientSpec(id)
	if !ok || c.Path == nil {
		return ""
	}
	home, _ := os.UserHomeDir()
	return c.Path(home)
}
func (c ClientSpec) extension() string {
	switch c.Format {
	case "toml":
		return "toml"
	case "dsh":
		return "yaml"
	}
	return "json"
}

// ConfigText renders the cnki server entry in the client's own format.
func ConfigText(id, binary, root string) (string, error) {
	c, ok := clientSpec(id)
	if !ok {
		return "", fail("UNKNOWN_CLIENT", "不支持此客户端；运行 cnki-mcp help 查看可选客户端")
	}
	quote := func(v any) string { data, _ := json.Marshal(v); return string(data) }
	env := map[string]string{"CNKI_DATA_DIR": root}
	switch c.Format {
	case "toml":
		return "[mcp_servers.cnki]\ncommand = " + quote(binary) + "\nargs = [\"serve\"]\nstartup_timeout_sec = 30\ntool_timeout_sec = 900\n\n[mcp_servers.cnki.env]\nCNKI_DATA_DIR = " + quote(root) + "\n", nil
	case "dsh":
		return "# CNKI Enhanced MCP\n- insert:\n    - id: mcp-cnki\n      name: '@deepseek-ai/dsh-mcp-client'\n      config:\n        serverName: cnki\n        transport: stdio\n        command: " + quote(binary) + "\n        args: [\"serve\"]\n        env: " + quote(env) + "\n        toolCallTimeoutMs: 900000\n        failOnStartupError: false\n", nil
	}
	data, err := json.MarshalIndent(nest(c.key(), map[string]any{"cnki": c.entry(binary, root)}), "", "  ")
	return string(data) + "\n", err
}
func (c ClientSpec) key() string {
	if c.Key == "" {
		return "mcpServers"
	}
	return c.Key
}
func (c ClientSpec) entry(binary, root string) map[string]any {
	entry := map[string]any{"command": binary, "args": []string{"serve"}, "env": map[string]string{"CNKI_DATA_DIR": root}}
	for k, v := range c.Extra {
		entry[k] = v
	}
	return entry
}

// ImportLink returns the client's one-click import link, if it has one.
func ImportLink(id, binary, root string) string {
	c, ok := clientSpec(id)
	if !ok || c.Link == nil {
		return ""
	}
	return c.Link(c.entry(binary, root))
}

// nest wraps value under a dotted key: "mcp.servers" → {"mcp": {"servers": value}}.
func nest(key string, value any) map[string]any {
	parts := strings.Split(key, ".")
	out := map[string]any{parts[len(parts)-1]: value}
	for i := len(parts) - 2; i >= 0; i-- {
		out = map[string]any{parts[i]: out}
	}
	return out
}

// installConfig merges the cnki entry into a client's config file, keeping
// every other setting, backing up the original and refusing to overwrite a
// file that changed meanwhile.
func installConfig(id, path, text string) error {
	c, ok := clientSpec(id)
	if !ok {
		return fail("UNKNOWN_CLIENT", "不支持此客户端")
	}
	before, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if info, e := os.Lstat(path); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return fail("CONFIG_SYMLINK", "配置是符号链接，请使用生成的片段手动导入")
	}
	if _, e := os.Stat(filepath.Dir(path)); c.NeedDir && e != nil {
		return fail("CLIENT_NOT_INITIALIZED", "没有找到客户端的配置目录："+c.Import)
	}
	var after []byte
	switch c.Format {
	case "toml", "dsh":
		marker := "[mcp_servers.cnki]"
		if c.Format == "dsh" {
			marker = "id: mcp-cnki"
		}
		if strings.Contains(string(before), marker) {
			if strings.Contains(string(before), strings.TrimSpace(text)) {
				return nil
			}
			return fail("CONFIG_EXISTS", "已存在 cnki 配置，请对照生成的片段手动更新")
		}
		trimmed := strings.TrimSpace(string(before))
		if trimmed == "" || c.Format == "dsh" && trimmed == "[]" {
			after = []byte(text) // an empty DSH patch layer is written as "[]"
		} else {
			after = []byte(strings.TrimRight(string(before), "\n") + "\n\n" + text)
		}
	default:
		current := map[string]any{}
		if len(before) > 0 {
			if err = json.Unmarshal(before, &current); err != nil {
				return fail("CONFIG_INVALID", "原配置不是有效 JSON，未修改")
			}
		}
		// Walk (and create) the nested objects of the dotted key, keeping every sibling.
		node := current
		for _, part := range strings.Split(c.key(), ".") {
			next, ok := node[part].(map[string]any)
			if !ok && node[part] != nil {
				return fail("CONFIG_INVALID", "现有 MCP 配置结构不兼容")
			}
			if next == nil {
				next = map[string]any{}
				node[part] = next
			}
			node = next
		}
		var fresh map[string]any
		if err = json.Unmarshal([]byte(text), &fresh); err != nil {
			return err
		}
		for _, part := range strings.Split(c.key(), ".") {
			fresh, _ = fresh[part].(map[string]any)
		}
		node["cnki"] = fresh["cnki"]
		if after, err = json.MarshalIndent(current, "", "  "); err != nil {
			return err
		}
		after = append(after, '\n')
	}
	if string(before) == string(after) {
		return nil
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if len(before) > 0 {
		backup := path + ".cnki-backup-" + time.Now().Format("20060102-150405.000000000")
		if err = os.WriteFile(backup, before, 0600); err != nil {
			return err
		}
	}
	temp := path + ".cnki-" + newID("")
	if err = os.WriteFile(temp, after, 0600); err != nil {
		return err
	}
	defer os.Remove(temp)
	latest, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if string(latest) != string(before) {
		return fail("CONFIG_CHANGED", "配置已被另一程序修改，未覆盖")
	}
	return os.Rename(temp, path)
}
