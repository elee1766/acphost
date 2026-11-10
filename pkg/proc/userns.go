package proc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/gfx-labs/acphost/pkg/hostenv"
	"github.com/gfx-labs/acphost/pkg/reexec"
	"github.com/gfx-labs/acphost/pkg/trace"
	"github.com/gfx-labs/acphost/pkg/userns"
	"github.com/gfx-labs/acphost/pkg/xerr"
	"golang.org/x/sys/unix"
)

// usernsCommand turns cmd (argv already final: the agent, bwrap or the
// pdeath helper) into a re-exec of this binary as the userns helper
// (reexec.Userns) in a new user namespace. The returned start function
// starts it, maps the ids with newuidmap/newgidmap and then writes the
// helper's config to its fd, which releases it to exec the real command,
// so the pid the host waits on is the agent. It also returns the map
// lines it will write.
func usernsCommand(cmd *exec.Cmd, u userns.User, root bool) (start func() error, applied *UserApplied, err error) {
	if err := reexec.Check(); err != nil {
		return nil, nil, err
	}
	uidArgs, gidArgs, ok, err := usernsMaps(u, root)
	if err != nil {
		return nil, nil, err
	}
	if root && !ok {
		return nil, nil, fmt.Errorf("user: root mode does not apply")
	}
	applied = &UserApplied{UIDMap: mapLines(uidArgs), GIDMap: mapLines(gidArgs)}
	cfg := reexec.UsernsConfig{UID: u.UID, GID: u.GID, Groups: u.Groups, Parent: os.Getpid(), Root: root}
	if cmd.Err == nil {
		// Resolved already, or another helper (/proc/self/exe). On a
		// lookup error the helper looks it up again and reports it.
		cfg.Path = cmd.Path
	}
	// /proc/self/exe still works if the binary was replaced on disk.
	argv := append([]string{reexec.Userns}, cmd.Args...)
	cmd.Path, cmd.Args, cmd.Err = "/proc/self/exe", argv, nil
	cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWUSER
	if !root {
		// The helper execs before the maps exist, so execve drops the
		// caps the new namespace gave it. Switching to ids other than
		// the caller's own (subordinate ids) needs CAP_SETUID/CAP_SETGID,
		// so keep them as ambient caps; the helper clears them before
		// running the agent. In root mode the helper is ns root and its
		// execve of bwrap gets full caps without ambient ones.
		cmd.SysProcAttr.AmbientCaps = append(cmd.SysProcAttr.AmbientCaps, unix.CAP_SETUID, unix.CAP_SETGID)
	}
	return func() error {
		body, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		r, w, err := os.Pipe()
		if err != nil {
			return err
		}
		defer func() { _ = w.Close() }()
		cmd.ExtraFiles = reexec.SetExtraFile(cmd.ExtraFiles, reexec.UsernsFd, r)
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
			trace.Event("run:" + m.tool)
			c := exec.Command(m.tool, append([]string{pid}, m.args...)...)
			c.Stderr = &stderr
			if err := c.Run(); err != nil {
				_ = w.Close() // child reads EOF and exits
				_ = cmd.Wait()
				return xerr.Permanentf("user: %s: %v: %s", m.tool, err, strings.TrimSpace(stderr.String()))
			}
		}
		if _, err := w.Write(body); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("user: release agent: %w", err)
		}
		return nil
	}, applied, nil
}

// usernsMaps returns the newuidmap/newgidmap arguments for u. With root
// it adds the caller as root inside (userns.RootMaps) and reports
// whether that applies.
func usernsMaps(u userns.User, root bool) (uidArgs, gidArgs []string, ok bool, err error) {
	self := os.Geteuid()
	subuid, err := userns.ReadSubIDs(hostenv.SubUIDPath, self)
	if err != nil {
		return nil, nil, false, xerr.Permanentf("user: %s: %w", hostenv.SubUIDPath, err)
	}
	subgid, err := userns.ReadSubIDs(hostenv.SubGIDPath, self)
	if err != nil {
		return nil, nil, false, xerr.Permanentf("user: %s: %w", hostenv.SubGIDPath, err)
	}
	uidArgs, gidArgs, err = userns.Maps(u, self, hostenv.SubUIDPath, hostenv.SubGIDPath, subuid, subgid)
	if err != nil || !root {
		return uidArgs, gidArgs, false, err
	}
	uidArgs, gidArgs, ok = userns.RootMaps(u, self, hostenv.Getegid(), uidArgs, gidArgs)
	return uidArgs, gidArgs, ok, nil
}

// rootModeArgs are the bwrap options for root mode, after the binds:
// keep only the caps the init helper needs to switch ids, bind the host
// binary as that helper and pass it the agent's ids.
func rootModeArgs(u userns.User) ([]string, error) {
	exe, err := os.Readlink("/proc/self/exe")
	if err != nil {
		return nil, fmt.Errorf("user: host binary: %w", err)
	}
	return []string{
		"--cap-drop", "ALL", "--cap-add", "CAP_SETUID", "--cap-add", "CAP_SETGID",
		"--ro-bind", exe, reexec.UsernsInit,
		"--setenv", reexec.UsernsInitEnv, reexec.InitEnv(u.UID, u.GID, u.Groups),
	}, nil
}
