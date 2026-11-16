package cnki

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDataAndProfileIsolation(t *testing.T) {
	root := t.TempDir()
	peer := filepath.Join(root, "pkulaw")
	pku := filepath.Join(root, "westlaw")
	if e := os.MkdirAll(peer, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(pku, 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PKULAW_DATA_DIR", peer)
	t.Setenv("WESTLAW_DATA_DIR", pku)
	for _, p := range []string{root, peer, pku, filepath.Join(peer, "child"), filepath.Join(pku, "child")} {
		if e := claimDataRoot(p); e == nil {
			t.Fatal("accepted peer directory", p)
		}
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if e := os.Symlink(pku, alias); e == nil {
		if claimDataRoot(alias) == nil {
			t.Fatal("accepted peer symlink")
		}
	}
	own := filepath.Join(t.TempDir(), "cnki")
	if e := claimDataRoot(own); e != nil {
		t.Fatal(e)
	}
	if e := claimDataRoot(own); e != nil {
		t.Fatal(e)
	}
	profile := filepath.Join(t.TempDir(), "profile")
	_ = os.MkdirAll(profile, 0700)
	_ = os.WriteFile(filepath.Join(profile, "Cookies"), []byte("test"), 0600)
	if claimProfile(profile) == nil {
		t.Fatal("claimed foreign profile")
	}
}

func TestSingleWriter(t *testing.T) {
	root := t.TempDir()
	s, e := OpenStore(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if other, e := OpenStore(root); e == nil {
		other.Close()
		t.Fatal("second writer admitted")
	}
}

// Isolation works without knowing the other service: any directory owned by
// another application (its marker, or files of its own) is refused, as is
// anything inside it or any non-empty directory without this service's marker.
func TestGenericOwnershipNeedsNoServiceList(t *testing.T) {
	write := func(path, body string) {
		t.Helper()
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	base := t.TempDir()
	unknown := filepath.Join(base, "future-mcp")
	write(filepath.Join(unknown, ".future-owner.json"), `{"application":"future-mcp"}`)
	nested := filepath.Join(base, "holder")
	write(filepath.Join(nested, "inner", ".other-owner.json"), `{"application":"other"}`)
	used := filepath.Join(base, "used")
	write(filepath.Join(used, "notes.txt"), "x")
	forged := filepath.Join(base, "forged")
	write(filepath.Join(forged, ownerMarker), `{"application":"someone-else"}`)
	for _, p := range []string{unknown, filepath.Join(unknown, "child", "deeper"), nested, used, forged} {
		if claimDataRoot(p) == nil {
			t.Fatal("claimed a directory of another application", p)
		}
	}
	clean := filepath.Join(base, "clean")
	write(filepath.Join(clean, ".DS_Store"), "")
	if e := claimDataRoot(clean); e != nil {
		t.Fatal("system clutter made an empty directory unusable", e)
	}
	// The protocol self-check runs a child service inside data/tmp.
	if e := claimDataRoot(filepath.Join(clean, "tmp", "protocol-1")); e != nil {
		t.Fatal("own nested root refused", e)
	}
	if e := claimDataRoot(clean); e != nil {
		t.Fatal(e)
	}
}
