package reexec

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	// Root keeps the helper root in the namespace (the caller is
	// mapped to root) instead of switching to UID/GID, so bwrap mounts
	// with the caller's access and UsernsInit switches ids inside the
	// sandbox (#162). UID, GID and Groups are unused.
	Root bool `json:"root,omitempty"`
}

// UsernsInitEnv carries the UsernsInit helper's ids, as "uid:gid:g1,g2".
// bwrap execs the helper inside the sandbox and passes no fds but 0-2
// through pasta, so this one helper reads the environment. It runs
// nothing but os.Args[1:] as those ids, with no caps.
const UsernsInitEnv = "_ACPHOST_USERNS_INIT"

// usernsInit runs inside the sandbox in root mode, as the command bwrap
// starts with only CAP_SETUID and CAP_SETGID. It becomes the agent's
// ids and execs os.Args[1:].
func usernsInit() {
	fail := failer("userns")
	var u UsernsConfig
	if !parseInitEnv(os.Getenv(UsernsInitEnv), &u) {
		fail("bad %s", UsernsInitEnv)
	}
	_ = os.Unsetenv(UsernsInitEnv)
	u.Parent = os.Getppid()
	becomeUser(u)
}

// InitEnv encodes ids for UsernsInitEnv.
func InitEnv(uid, gid int, groups []int) string {
	gs := make([]string, len(groups))
	for i, g := range groups {
		gs[i] = strconv.Itoa(g)
	}
	return fmt.Sprintf("%d:%d:%s", uid, gid, strings.Join(gs, ","))
}

func parseInitEnv(s string, u *UsernsConfig) bool {
	f := strings.Split(s, ":")
	if len(f) != 3 {
		return false
	}
	var err1, err2 error
	u.UID, err1 = strconv.Atoi(f[0])
	u.GID, err2 = strconv.Atoi(f[1])
	if err1 != nil || err2 != nil {
		return false
	}
	if f[2] != "" {
		for _, g := range strings.Split(f[2], ",") {
			n, err := strconv.Atoi(g)
			if err != nil {
				return false
			}
			u.Groups = append(u.Groups, n)
		}
	}
	return true
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
	if u.Root {
		// Already ns root with full caps for the execve: no id change,
		// so the parent-death signal still holds.
		execArgs(u.Path)
	}
	becomeUser(u)
}

// becomeUser switches to u's ids, drops every capability and execs
// os.Args[1:].
func becomeUser(u UsernsConfig) {
	fail := failer("userns")
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
		os.Exit(127) // parent died before the signal was set
	}
	execArgs(u.Path)
}

// execArgs execs os.Args[1:], from path or looked up on PATH.
func execArgs(path string) {
	fail := failer("userns")
	if len(os.Args) < 2 {
		fail("no command")
	}
	var err error
	if path == "" {
		if path, err = exec.LookPath(os.Args[1]); err != nil {
			fail("%v", err)
		}
	}
	err = syscall.Exec(path, os.Args[1:], os.Environ())
	fail("exec %s: %v", path, err)
}
