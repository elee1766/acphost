package proc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/elee1766/acphost/internal/hostenv"
	"github.com/elee1766/acphost/internal/reexec"
	"github.com/elee1766/acphost/internal/trace"
	"github.com/elee1766/acphost/internal/xerr"
	"github.com/elee1766/acphost/userns"
	"golang.org/x/sys/unix"
)

// usernsCommand turns cmd (argv already final: the agent, bwrap or the
// pdeath helper) into a re-exec of this binary as the userns helper
// (reexec.Userns) in a new user namespace. The returned start function
// starts it, maps the ids with newuidmap/newgidmap and then writes the
// helper's config to its fd, which releases it to exec the real command,
// so the pid the host waits on is the agent. It also returns the map
// lines it will write.
func usernsCommand(cmd *exec.Cmd, u userns.User) (start func() error, applied *UserApplied, err error) {
	if err := reexec.Check(); err != nil {
		return nil, nil, err
	}
	self := os.Geteuid()
	subuid, err := userns.ReadSubIDs(hostenv.SubUIDPath, self)
	if err != nil {
		return nil, nil, xerr.Permanentf("user: %s: %w", hostenv.SubUIDPath, err)
	}
	subgid, err := userns.ReadSubIDs(hostenv.SubGIDPath, self)
	if err != nil {
		return nil, nil, xerr.Permanentf("user: %s: %w", hostenv.SubGIDPath, err)
	}
	uidArgs, gidArgs, err := userns.Maps(u, self, hostenv.SubUIDPath, hostenv.SubGIDPath, subuid, subgid)
	if err != nil {
		return nil, nil, err
	}
	applied = &UserApplied{UIDMap: mapLines(uidArgs), GIDMap: mapLines(gidArgs)}
	cfg := reexec.UsernsConfig{UID: u.UID, GID: u.GID, Groups: u.Groups, Parent: os.Getpid()}
	if cmd.Err == nil {
		// Resolved already, or another helper (/proc/self/exe). On a
		// lookup error the helper looks it up again and reports it.
		cfg.Path = cmd.Path
	}
	// /proc/self/exe still works if the binary was replaced on disk.
	argv := append([]string{reexec.Userns}, cmd.Args...)
	cmd.Path, cmd.Args, cmd.Err = "/proc/self/exe", argv, nil
	cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWUSER
	// The helper execs before the maps exist, so execve drops the caps
	// the new namespace gave it. Switching to ids other than the caller's
	// own (subordinate ids) needs CAP_SETUID/CAP_SETGID, so keep them as
	// ambient caps; the helper clears them before running the agent.
	cmd.SysProcAttr.AmbientCaps = append(cmd.SysProcAttr.AmbientCaps, unix.CAP_SETUID, unix.CAP_SETGID)
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
