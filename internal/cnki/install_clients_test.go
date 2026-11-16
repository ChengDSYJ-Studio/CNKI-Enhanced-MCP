package cnki

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

func TestClientInstallUninstallPreservesPeers(t *testing.T) {
	for _, client := range []string{"generic", "codex", "dsh-desktop", "zcode"} {
		t.Run(client, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config")
			var before []byte
			switch client {
			case "codex":
				before = []byte("model = 'example'\n[mcp_servers.heinonline]\ncommand = '/heinonline-mcp'\n[mcp_servers.westlaw]\ncommand = '/westlaw-mcp'\n[mcp_servers.pkulaw]\ncommand = '/pkulaw'\n[mcp_servers.pkulaw.env]\nPKULAW_DATA_DIR = '/pkulaw-data'\n")
			case "dsh-desktop":
				before = []byte("- insert:\n    - id: mcp-pkulaw\n      config:\n        serverName: pkulaw\n        command: /pkulaw\n")
			case "zcode":
				before = []byte(`{"mcp":{"servers":{"pkulaw":{"command":"/pkulaw","env":{"PKULAW_DATA_DIR":"/pkulaw-data"}},"westlaw":{"command":"/westlaw-mcp","env":{"WESTLAW_DATA_DIR":"/westlaw-data"}},"heinonline":{"command":"/heinonline-mcp"}}},"setting":true}`)
			default:
				before = []byte(`{"mcpServers":{"pkulaw":{"command":"/pkulaw","env":{"PKULAW_DATA_DIR":"/pkulaw-data"}},"westlaw":{"command":"/westlaw-mcp","env":{"WESTLAW_DATA_DIR":"/westlaw-data"}},"heinonline":{"command":"/heinonline-mcp"}},"setting":true}`)
			}
			if err := os.WriteFile(path, before, 0600); err != nil {
				t.Fatal(err)
			}
			text, err := ConfigText(client, "/cnki-mcp", filepath.Join(root, "data"))
			if err != nil {
				t.Fatal(err)
			}
			if err = installConfig(client, path, text); err != nil {
				t.Fatal(err)
			}
			if err = installConfig(client, path, text); err != nil {
				t.Fatal("not idempotent", err)
			}
			spec, _ := clientSpec(client)
			removed, err := removeClientEntry(spec, path)
			if err != nil || !removed {
				t.Fatal(err, removed)
			}
			after, _ := os.ReadFile(path)
			var a, b any
			switch client {
			case "codex":
				_ = toml.Unmarshal(before, &a)
				_ = toml.Unmarshal(after, &b)
			case "dsh-desktop":
				_ = yaml.Unmarshal(before, &a)
				_ = yaml.Unmarshal(after, &b)
			default:
				_ = json.Unmarshal(before, &a)
				_ = json.Unmarshal(after, &b)
			}
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("changed unrelated config\nbefore: %s\nafter: %s", before, after)
			}
		})
	}
}
func TestClientCollisionAndInvalidConfig(t *testing.T) {
	for _, client := range []string{"generic", "codex"} {
		root := t.TempDir()
		path := filepath.Join(root, "config")
		before := []byte(`{"mcpServers":{"cnki":{"url":"https://other.example/mcp"}}}`)
		if client == "codex" {
			before = []byte("[mcp_servers.'cnki']\nurl = 'https://other.example/mcp'\n")
		}
		_ = os.WriteFile(path, before, 0600)
		text, _ := ConfigText(client, "/cnki-mcp", filepath.Join(root, "data"))
		if err := installConfig(client, path, text); err == nil {
			t.Fatal("overwrote foreign service", client)
		}
		after, _ := os.ReadFile(path)
		if string(after) != string(before) {
			t.Fatal("changed collision config")
		}
	}
}
