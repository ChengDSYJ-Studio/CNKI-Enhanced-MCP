package cnki

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServerContractOverRealMCPSession(t *testing.T) {
	app, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	ctx := context.Background()
	tools, err := ExportContract(ctx, app)
	if err != nil || len(tools.Tools) != 9 {
		t.Fatalf("tools: %v %v", len(tools.Tools), err)
	}
	left, right := mcp.NewInMemoryTransports()
	ss, err := Server(app).Connect(ctx, left, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, right, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	call := func(name string, args map[string]any) (*mcp.CallToolResult, map[string]any) {
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var out map[string]any
		_ = json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &out)
		return r, out
	}
	r, out := call("status", map[string]any{"details": true})
	if d, _ := out["diagnostics"].(map[string]any); r.IsError || d["version"] != Version || out["login"] != "not_checked" {
		t.Fatalf("status: %+v", out)
	}
	if r.StructuredContent != nil {
		t.Fatal("result sent twice (text and structured content)")
	}
	// Invalid input is rejected by schema or validation before any browser starts.
	if r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "search", Arguments: map[string]any{"expression": map[string]any{"op": "or", "items": []any{map[string]any{"field": "title", "value": "a"}, map[string]any{"op": "not", "items": []any{map[string]any{"field": "title", "value": "b"}}}}}}}); err == nil && !r.IsError {
		t.Fatal("search accepted an unsupported expression")
	}
	for name, args := range map[string]map[string]any{
		"search":   {"query": "物理", "options": map[string]any{"metadata": "full"}},
		"export":   {"record_refs": []string{"x"}, "format": "nope"},
		"results":  {"search_id": "missing"},
		"download": {"record_ref": "a", "title": "b"},
		"read":     {"snapshot_id": "../x"},
	} {
		if r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args}); err == nil && !r.IsError {
			t.Fatalf("%s accepted invalid input", name)
		}
	}
	if app.Site != nil && app.Site.browser.State()["running"] == true {
		t.Fatal("browser started during validation")
	}
}
