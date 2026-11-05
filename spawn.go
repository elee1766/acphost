package acphost

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PermanentError is a spawn failure that retrying cannot fix: the host
// config points at something that is not there (#146).
type PermanentError struct{ Err error }

// Error returns the wrapped error's message.
func (e *PermanentError) Error() string { return e.Err.Error() }

// Unwrap returns the wrapped error.
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanentf returns a PermanentError.
func Permanentf(format string, a ...any) error {
	return &PermanentError{Err: fmt.Errorf(format, a...)}
}

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
func CheckSpawn(s Spec, dirCreated bool) error {
	if s.Cgroup != nil {
		if err := checkCgroup(*s.Cgroup); err != nil {
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
		if err := checkUser(*s.User); err != nil {
			return err
		}
	}
	if s.Sandbox != nil {
		// SandboxArgs looks up bwrap on PATH and checks mounts and Dir.
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
		if _, err := SandboxArgs(*s.Sandbox, s.Command, s.Args, s.Dir); err != nil {
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

// sandboxWithEnv returns s with the spec's Env (KEY=VALUE) set inside the
// sandbox. bwrap clears the environment, and Env is what the caller asked
// the agent to have.
func sandboxWithEnv(s Sandbox, env []string) Sandbox {
	extra := make([]string, 0, 3*len(env)+len(s.ExtraArgs))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			extra = append(extra, "--setenv", k, v)
		}
	}
	s.ExtraArgs = append(extra, s.ExtraArgs...)
	return s
}

func lookCommand(cmd string, env []string) error {
	trace("look:" + cmd)
	if strings.Contains(cmd, "/") {
		st, err := os.Stat(cmd)
		if err != nil {
			return err
		}
		if st.IsDir() || st.Mode()&0o111 == 0 {
			return fmt.Errorf("not executable: %w", exec.ErrNotFound)
		}
		return nil
	}
	path := os.Getenv("PATH")
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	for _, d := range filepath.SplitList(path) {
		if d == "" {
			d = "."
		}
		if st, err := os.Stat(filepath.Join(d, cmd)); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return nil
		}
	}
	return exec.ErrNotFound
}
