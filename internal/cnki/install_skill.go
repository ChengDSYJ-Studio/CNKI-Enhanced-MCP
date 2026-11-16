// The bundled skills: the tool guide (assets/SKILL.md) and the research
// workflow (assets/cnki-deep-research).

package cnki

import (
	"embed"
	_ "embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed assets/SKILL.md
var SkillMarkdown string

//go:embed assets/cnki-deep-research
var researchSkill embed.FS

// InstallSkill writes SKILL.md into <dir>/cnki/ and the research skill into
// <dir>/cnki-deep-research/. With no dir it targets
// Claude Code's personal skills folder (~/.claude/skills). This is the only
// command that writes outside the data root, and only when the user runs it.
func InstallSkill(dir string) (string, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".claude", "skills")
	}
	target := filepath.Join(dir, "cnki")
	if err := os.MkdirAll(target, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(target, "SKILL.md")
	if err := os.WriteFile(path, []byte(SkillMarkdown), 0644); err != nil {
		return "", err
	}
	// The research workflow skill replaces its earlier copy file by file.
	err := fs.WalkDir(researchSkill, "assets/cnki-deep-research", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("assets", p)
		dest := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0755)
		}
		b, err := researchSkill.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0644)
	})
	return path, err
}
