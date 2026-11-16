package cnki

import (
	"os"
	"strings"
	"testing"
)

func TestArtifactSafety(t *testing.T) {
	root := t.TempDir()
	path, _, _, e := writeArtifact(root, "exports", "ok.txt", strings.NewReader("first"))
	if e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = writeArtifact(root, "exports", "ok.txt", strings.NewReader("second")); e == nil {
		t.Fatal("overwrote existing file")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "first" {
		t.Fatal(string(raw))
	}
	if safeName("../escape.txt") == nil || safeName("CON.txt") == nil {
		t.Fatal("unsafe filename")
	}
	if safeName("a/b.pdf") == nil || safeName("") == nil {
		t.Fatal("path or empty name accepted")
	}
}
