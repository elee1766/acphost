package reexec

import (
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// pinChild holds the namespaces it was cloned into until stdin closes or
// the parent dies, so other processes can join them by
// /proc/<pid>/ns/<type> (#166).
func pinChild() {
	_ = unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(syscall.SIGKILL), 0, 0, 0)
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}
