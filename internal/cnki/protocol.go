package cnki

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func CheckProtocol(ctx context.Context, executable string) (map[string]any, error) {
	root, err := os.MkdirTemp("", "cnki-protocol-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	command := exec.Command(executable, "serve")
	command.Env = append(os.Environ(), "CNKI_DATA_DIR="+root)
	client := mcp.NewClient(&mcp.Implementation{Name: "cnki-doctor", Version: Version}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	if len(tools.Tools) == 0 {
		return nil, fail("PROTOCOL_CHECK_FAILED", "实际 MCP 子进程没有工具")
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "diagnostics", Arguments: map[string]any{}})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, fail("PROTOCOL_CHECK_FAILED", "实际诊断工具返回错误")
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err = json.Unmarshal(encoded, &data); err != nil {
		return nil, err
	}
	if data["version"] != Version {
		return nil, fail("PROTOCOL_CHECK_FAILED", "子进程版本不匹配")
	}
	if _, err = os.Stat(filepath.Join(root, "browser")); err == nil {
		return nil, fail("PROTOCOL_CHECK_FAILED", "发现阶段意外创建浏览器身份")
	}
	return map[string]any{"passed": true, "transport": "stdio", "tools": len(tools.Tools), "version": data["version"], "browser_started": false}, nil
}
