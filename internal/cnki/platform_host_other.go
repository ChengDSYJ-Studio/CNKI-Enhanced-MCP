//go:build !darwin

package cnki

// desktopAncestor is only needed on macOS, where desktop helpers can outlive their app.
func desktopAncestor(int) int { return 0 }
