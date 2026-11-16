// The data root (default <binary dir>/data) and its ownership markers.

package cnki

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	Application string `json:"application,omitempty"`
	BrowserPath string `json:"browser_path"`
}

// DefaultDataRoot keeps every runtime file beside the installed binary
// (<install>/data) so the service never scatters state across the system.
// CNKI_DATA_DIR overrides it; an unwritable install folder falls back
// to the per-user application directory.
func DefaultDataRoot() string {
	if r := os.Getenv("CNKI_DATA_DIR"); r != "" {
		return r
	}
	if exe, err := os.Executable(); err == nil {
		if real, e := filepath.EvalSymlinks(exe); e == nil {
			exe = real
		}
		dir := filepath.Dir(exe)
		// `go run` and `go test` binaries live in a throwaway build cache.
		if !strings.Contains(dir, "go-build") && !strings.HasSuffix(filepath.Base(exe), ".test") {
			root := filepath.Join(dir, "data")
			if os.MkdirAll(root, 0700) == nil {
				return root
			}
		}
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "CNKI-MCP-data")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cnki-mcp")
}

func realPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if v, e := filepath.EvalSymlinks(abs); e == nil {
		return v, nil
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		return abs, nil
	}
	rp, err := realPath(parent)
	return filepath.Join(rp, filepath.Base(abs)), err
}

func overlaps(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, _ = realPath(a)
	b, _ = realPath(b)
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}

// Existing installations keep their original ownership markers and browser profile.
func ownsApplication(name string) bool {
	return name == Application || name == "cnki-enhanced-mcp"
}

// ownerMarker is this service's ownership marker. Every MCP of this family
// writes one `.<application>-owner.json` into its data root.
const ownerMarker = ".cnki-owner.json"

var markerName = regexp.MustCompile(`^\.[A-Za-z0-9_.-]+-owner\.json$`)

// legacyForeign are files of services that predate owner markers.
var legacyForeign = []string{"pkulaw.sqlite", "heinonline.sqlite", "westlaw.sqlite", "lexis.json"}

// ownLegacy are the files of CNKI MCP 2.0 and earlier, which wrote no marker:
// such a directory is this service's own data and is claimed in place.
var ownLegacy = []string{"research.json", "cnki.lock", "cnki.json", ".cnki.lock"}

// ignorable entries do not make a directory "used".
func ignorable(name string) bool {
	return oneOf(name, ".DS_Store", "Thumbs.db", "desktop.ini") || strings.HasPrefix(name, ".owner-")
}

// foreignIn reports an entry in dir that belongs to another application: an
// owner marker other than this service's, or a known legacy file.
func foreignIn(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := e.Name()
		if oneOf(name, legacyForeign...) {
			return name
		}
		if !markerName.MatchString(name) {
			continue
		}
		if name != ownerMarker {
			return name
		}
		var m map[string]string
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || json.Unmarshal(b, &m) != nil || !ownsApplication(m["application"]) {
			return name
		}
	}
	return ""
}

// claimDataRoot accepts a directory only when it already carries this
// service's marker, or when it is empty and neither it nor any directory
// above it belongs to another application. The rule needs no list of other
// services: their data is never empty and always carries their own marker or
// files, so it is refused whatever the service is called. The known paths of
// sibling services are checked as well, as an extra layer.
func claimDataRoot(root string) error {
	abs, err := realPath(root)
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	protected := []string{os.Getenv("PKULAW_DATA_DIR"), filepath.Join(home, "Applications", "PKULaw-MCP", "data"), filepath.Join(home, "Library", "Application Support", "PKULawMCP"), os.Getenv("WESTLAW_DATA_DIR"), filepath.Join(home, "Applications", "Westlaw-MCP", "data"), filepath.Join(home, "Library", "Application Support", "WestlawMCP"), os.Getenv("HEINONLINE_DATA_DIR"), filepath.Join(home, "Applications", "HeinOnline-MCP", "data"), filepath.Join(home, "Library", "Application Support", "HeinOnline-MCP"), filepath.Join(home, "Library", "Application Support", "HeinOnlineMCP"), os.Getenv("SSRN_DATA_DIR"), filepath.Join(home, "Applications", "SSRN-MCP", "data"), os.Getenv("LEXIS_DATA_DIR"), filepath.Join(home, "Library", "Application Support", "LexisEnhancedMCP")}
	for _, p := range protected {
		if p != "" && overlaps(abs, p) {
			return fail("FOREIGN_DATA_DIRECTORY", "知网数据目录与其他 MCP 的目录重合，已拒绝启动")
		}
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		if name := foreignIn(dir); name != "" {
			where := "该目录"
			if dir != abs {
				where = "上层目录 " + dir
			}
			return fail("FOREIGN_DATA_DIRECTORY", where+" 属于其他应用（含 "+name+"），已拒绝使用；请选择独立的空目录")
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	marker := filepath.Join(abs, ownerMarker)
	if _, e := os.Stat(marker); e == nil {
		return nil // foreignIn has verified it is ours
	} else if !os.IsNotExist(e) {
		return e
	}
	if entries, e := os.ReadDir(abs); e == nil {
		legacy := false
		for _, entry := range entries {
			legacy = legacy || oneOf(entry.Name(), ownLegacy...)
		}
		for _, entry := range entries {
			if !legacy && !ignorable(entry.Name()) {
				return fail("UNCLAIMED_DATA_DIRECTORY", "目录不是空的且没有知网归属标记（含 "+entry.Name()+"）；请选择独立的空目录")
			}
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return err
	}
	// Publish the completed marker in a single no-replace operation. A
	// concurrent first client must never read a partially written marker.
	f, err := os.CreateTemp(abs, ".owner-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(map[string]string{"application": Application})
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Link(f.Name(), marker); os.IsExist(err) {
		return claimDataRoot(root)
	}
	return err
}

func claimProfile(path string) error {
	marker := filepath.Join(path, ".cnki-profile.json")
	if b, err := os.ReadFile(marker); err == nil {
		var m map[string]string
		if json.Unmarshal(b, &m) != nil || !ownsApplication(m["application"]) {
			return fail("FOREIGN_BROWSER_PROFILE", "浏览器配置目录不属于知网")
		}
		return nil
	}
	if entries, err := os.ReadDir(path); err == nil && len(entries) > 0 {
		return fail("FOREIGN_BROWSER_PROFILE", "拒绝接管已有浏览器身份")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return atomicJSON(marker, map[string]string{"application": Application})
}
