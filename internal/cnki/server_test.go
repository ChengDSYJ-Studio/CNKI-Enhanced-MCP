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
	if err != nil || len(tools.Tools) != 13 {
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
	if r, out := call("diagnostics", map[string]any{}); r.IsError || out["version"] != Version {
		t.Fatalf("diagnostics: %+v", out)
	}
	// Invalid input is rejected by schema or validation before any browser starts.
	for name, args := range map[string]map[string]any{
		"search":            {"query": "物理", "options": map[string]any{"metadata": "full"}},
		"structured_search": {"expression": map[string]any{"op": "or", "items": []any{map[string]any{"field": "title", "value": "a"}, map[string]any{"op": "not", "items": []any{map[string]any{"field": "title", "value": "b"}}}}}},
		"search_results":    {"search_id": "missing"},
		"download_paper":    {"record_ref": "a", "title": "b"},
	} {
		if r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args}); err == nil && !r.IsError {
			t.Fatalf("%s accepted invalid input", name)
		}
	}
	if app.Site != nil && app.Site.browser.State()["running"] == true {
		t.Fatal("browser started during validation")
	}
}
