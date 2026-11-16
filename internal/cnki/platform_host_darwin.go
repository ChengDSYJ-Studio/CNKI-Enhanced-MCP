package cnki

import "golang.org/x/sys/unix"

// A desktop helper can outlive its GUI and keep inherited pipes open.
func desktopAncestor(pid int) int {
	for depth := 0; pid > 1 && depth < 32; depth++ {
		k, e := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if e != nil {
			return 0
		}
		switch unix.ByteSliceToString(k.Proc.P_comm[:]) {
		case "ChatGPT", "Claude", "Codex", "Cursor":
			return pid
		}
		pid = int(k.Eproc.Ppid)
	}
	return 0
}
