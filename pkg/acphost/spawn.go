package acphost

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"

	"github.com/elee1766/acphost/pkg/cgroup"
	"github.com/elee1766/acphost/pkg/sandbox"
	"github.com/elee1766/acphost/pkg/trace"
	"github.com/elee1766/acphost/pkg/userns"
)

// Permanent reports whether a spawn error will repeat on every attempt:
// a PermanentError, a missing file or directory, or an executable not on
// PATH. Anything else (a fetch that timed out, a busy lock) is transient.
func Permanent(err error) bool {
	var pe *PermanentError
	return errors.As(err, &pe) || errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist)
}

// CheckSpawn checks what the host needs before it is started, so a bad
// path fails in the controller with a permanent error instead of as a
// host that exits at once: the working directory (unless it is created
// later, as a worktree is) and the agent command, resolved with the PATH
// in env when set.
// With WithHostDir it also checks the socket path length. The session
// name must be one path element short enough for the GC root link.
func CheckSpawn(s Spec, dirCreated bool, opts ...SpawnOption) error {
	var c spawnCheck
	for _, o := range opts {
		o(&c)
	}
	if err := checkSession(s); err != nil {
		return err
	}
	// Pure spec errors first, so they do not depend on the host.
	if err := checkJoinSpec(s); err != nil {
		return err
	}
	if err := checkListenSpec(s); err != nil {
		return err
	}
	if c.hostDir != "" {
		if err := CheckSocketPath(c.hostDir); err != nil {
			return err
		}
	}
	if err := checkListenFiles(s); err != nil {
		return err
	}
	if s.Cgroup != nil {
		if err := cgroup.Check(*s.Cgroup); err != nil {
			return err
		}
	}
	if !dirCreated && s.Sandbox == nil {
		if st, err := os.Stat(s.Dir); err != nil {
			return Permanentf("session dir %s: %w", s.Dir, err)
		} else if !st.IsDir() {
			return Permanentf("session dir %s is not a directory", s.Dir)
		}
	}
	if s.User != nil {
		if err := userns.Check(*s.User); err != nil {
			return err
		}
	}
	if err := checkJoin(s); err != nil {
		return err
	}
	if s.Sandbox != nil {
		// sandbox.Args looks up bwrap on PATH and checks mounts and Dir.
		if s.Sandbox.Bwrap != "" {
			if err := lookCommand(s.Sandbox.Bwrap, nil); err != nil {
				return &PermanentError{Err: fmt.Errorf("sandbox bwrap %q: %w", s.Sandbox.Bwrap, err)}
			}
		}
		if s.Sandbox.Network == "pasta" && s.Sandbox.Pasta != "" {
			if err := lookCommand(s.Sandbox.Pasta, nil); err != nil {
				return &PermanentError{Err: fmt.Errorf("sandbox pasta %q: %w", s.Sandbox.Pasta, err)}
			}
		}
		if _, err := sandbox.Args(*s.Sandbox, s.Command, s.Args, s.Dir); err != nil {
			return &PermanentError{Err: err}
		}
		// The command is resolved inside the sandbox, so it is not looked
		// up on the host.
		return nil
	}
	if err := lookCommand(s.Command, s.Env); err != nil {
		return &PermanentError{Err: fmt.Errorf("agent command %q: %w", s.Command, err)}
	}
	return nil
}

func lookCommand(cmd string, env []string) error { return trace.LookCommand(cmd, env) }
