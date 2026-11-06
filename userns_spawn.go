package acphost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/elee1766/acphost/internal/hostenv"
	"github.com/elee1766/acphost/userns"
	"golang.org/x/sys/unix"
)

// usernsChildEnv marks a re-exec of the host binary that must become the
// agent inside a fresh user namespace. See usernsChild.
const usernsChildEnv = "_ACPHOST_USERNS_CHILD"

// usernsSyncFd is the read end of the pipe the host closes or writes to
// once the id maps are in place (first ExtraFiles entry).
const usernsSyncFd = 3

func init() {
	if os.Getenv(usernsChildEnv) == "" {
		return
	}
	// Never returns: execs the agent or exits.
	usernsChild()
}

// usernsChild runs in the re-exec'd host binary, already in the new user
// namespace but with no ids mapped yet. It waits for the host to map
// them, switches to the requested ids and execs os.Args[1:].
func usernsChild() {
	fail := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, "acphost userns: "+format+"\n", a...)
		os.Exit(127)
	}
	u := userFromEnv(os.Getenv(usernsChildEnv))
	if u == nil {
		fail("bad %s", usernsChildEnv)
	}
	_ = os.Unsetenv(usernsChildEnv)
	ppid := os.Getppid()
	sync := os.NewFile(usernsSyncFd, "userns-sync")
	var b [1]byte
	if n, _ := sync.Read(b[:]); n != 1 {
		os.Exit(127) // host gave up (map failed) and already reported why
	}
	_ = sync.Close()
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
	if os.Getppid() != ppid {
		os.Exit(127) // host died before the signal was set
	}
	if len(os.Args) < 2 {
		fail("no command")
	}
	path, err := exec.LookPath(os.Args[1])
	if err != nil {
		fail("%v", err)
	}
	err = syscall.Exec(path, os.Args[1:], os.Environ())
	fail("exec %s: %v", path, err)
}

// userEnv encodes the inside ids for the child: "uid:gid:g1,g2".
func userEnv(u User) string {
	gs := make([]string, len(u.Groups))
	for i, g := range u.Groups {
		gs[i] = fmt.Sprint(g)
	}
	return fmt.Sprintf("%d:%d:%s", u.UID, u.GID, strings.Join(gs, ","))
}

func userFromEnv(s string) *User {
	f := strings.Split(s, ":")
	if len(f) != 3 {
		return nil
	}
	var u User
	if _, err := fmt.Sscan(f[0], &u.UID); err != nil {
		return nil
	}
	if _, err := fmt.Sscan(f[1], &u.GID); err != nil {
		return nil
	}
	if f[2] != "" {
		for _, g := range strings.Split(f[2], ",") {
			var n int
			if _, err := fmt.Sscan(g, &n); err != nil {
				return nil
			}
			u.Groups = append(u.Groups, n)
		}
	}
	return &u
}

// usernsCommand turns cmd (argv already final: the agent or bwrap) into a
// re-exec of this binary in a new user namespace. The returned start
// function starts it, maps the ids with newuidmap/newgidmap and releases
// it to exec the real command, so the pid the host waits on is the agent.
// It also returns the map lines it will write.
func usernsCommand(cmd *exec.Cmd, u User) (start func() error, applied *UserApplied, err error) {
	self := os.Geteuid()
	subuid, err := userns.ReadSubIDs(hostenv.SubUIDPath, self)
	if err != nil {
		return nil, nil, Permanentf("user: %s: %w", hostenv.SubUIDPath, err)
	}
	subgid, err := userns.ReadSubIDs(hostenv.SubGIDPath, self)
	if err != nil {
		return nil, nil, Permanentf("user: %s: %w", hostenv.SubGIDPath, err)
	}
	uidArgs, gidArgs, err := userns.Maps(u, self, hostenv.SubUIDPath, hostenv.SubGIDPath, subuid, subgid)
	if err != nil {
		return nil, nil, err
	}
	applied = &UserApplied{UIDMap: mapLines(uidArgs), GIDMap: mapLines(gidArgs)}
	// /proc/self/exe still works if the binary was replaced on disk.
	argv := append([]string{"acphost-userns"}, cmd.Args...)
	cmd.Path, cmd.Args, cmd.Err = "/proc/self/exe", argv, nil
	cmd.Env = append(cmd.Env, usernsChildEnv+"="+userEnv(u))
	cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWUSER
	// The helper execs before the maps exist, so execve drops the caps
	// the new namespace gave it. Switching to ids other than the caller's
	// own (subordinate ids) needs CAP_SETUID/CAP_SETGID, so keep them as
	// ambient caps; usernsChild clears them before running the agent.
	cmd.SysProcAttr.AmbientCaps = append(cmd.SysProcAttr.AmbientCaps, unix.CAP_SETUID, unix.CAP_SETGID)
	return func() error {
		r, w, err := os.Pipe()
		if err != nil {
			return err
		}
		defer func() { _ = w.Close() }()
		cmd.ExtraFiles = []*os.File{r}
		err = cmd.Start()
		_ = r.Close()
		if err != nil {
			return err
		}
		pid := fmt.Sprint(cmd.Process.Pid)
		for _, m := range []struct {
			tool string
			args []string
		}{{"newuidmap", uidArgs}, {"newgidmap", gidArgs}} {
			var stderr bytes.Buffer
			traceEvent("run:" + m.tool)
			c := exec.Command(m.tool, append([]string{pid}, m.args...)...)
			c.Stderr = &stderr
			if err := c.Run(); err != nil {
				_ = w.Close() // child reads EOF and exits
				_ = cmd.Wait()
				return Permanentf("user: %s: %v: %s", m.tool, err, strings.TrimSpace(stderr.String()))
			}
		}
		if _, err := w.Write([]byte{1}); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("user: release agent: %w", err)
		}
		return nil
	}, applied, nil
}
