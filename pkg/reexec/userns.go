package reexec

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// UsernsConfig is the userns helper's input on UsernsFd. The host writes
// it only once the id maps are in place, so it is also the sync: EOF
// with nothing written means the host gave up.
type UsernsConfig struct {
	UID    int   `json:"uid"`
	GID    int   `json:"gid"`
	Groups []int `json:"groups,omitempty"`
	// Parent is the host pid. The helper exits if its parent changed.
	Parent int `json:"parent"`
	// Path is the file to exec with argv os.Args[1:]. Empty means look
	// up os.Args[1] on PATH. Set when the command is another helper
	// (argv[0] is its name, the file is /proc/self/exe).
	Path string `json:"path,omitempty"`
}

// usernsChild runs in the re-exec'd host binary, already in the new user
// namespace but with no ids mapped yet. It waits for the host to map
// them, switches to the requested ids and execs os.Args[1:].
func usernsChild() {
	fail := failer("userns")
	var u UsernsConfig
	ok, err := readConfig(UsernsFd, &u)
	if err != nil {
		fail("%v", err)
	}
	if !ok {
		os.Exit(127) // host gave up (map failed) and already reported why
	}
	if u.Parent <= 0 || os.Getppid() != u.Parent {
		os.Exit(127)
	}
	groups := u.Groups
	if groups == nil {
		groups = []int{}
	}
	if err := syscall.Setgroups(groups); err != nil && len(u.Groups) > 0 {
		// newgidmap writes setgroups=deny for a map of only the
		// caller's own gid, so an empty list may be refused harmlessly.
		fail("setgroups %v: %v", u.Groups, err)
	}
	if err := syscall.Setresgid(u.GID, u.GID, u.GID); err != nil {
		fail("setresgid %d: %v", u.GID, err)
	}
	if err := syscall.Setresuid(u.UID, u.UID, u.UID); err != nil {
		fail("setresuid %d: %v", u.UID, err)
	}
	// Drop the ambient caps so the agent's execve leaves it with none.
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil {
		fail("clear ambient caps: %v", err)
	}
	// A credential change clears the parent-death signal.
	if err := unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(syscall.SIGKILL), 0, 0, 0); err != nil {
		fail("pdeathsig: %v", err)
	}
	if os.Getppid() != u.Parent {
		os.Exit(127) // host died before the signal was set
	}
	if len(os.Args) < 2 {
		fail("no command")
	}
	path := u.Path
	if path == "" {
		if path, err = exec.LookPath(os.Args[1]); err != nil {
			fail("%v", err)
		}
	}
	err = syscall.Exec(path, os.Args[1:], os.Environ())
	fail("exec %s: %v", path, err)
}
