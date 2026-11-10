package proc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/gfx-labs/acphost/pkg/cgroup"
	"github.com/gfx-labs/acphost/pkg/reexec"
)

// scopeLeaf is the cgroup under a systemd scope the command runs in.
// The scope's own dir holds the holder process, and cgroup v2 allows no
// processes in an inner node with controllers enabled, so the limits
// stay on the scope and the command sits in a leaf.
const scopeLeaf = "cmd"

// scopeTimeout bounds the systemd start job.
const scopeTimeout = 30 * time.Second

// systemdScope is the systemd driver's cgroup (#226): a transient scope
// that adopted a holder process, with the command in scopeLeaf.
type systemdScope struct {
	scope  cgroup.Scope
	holder *exec.Cmd
	stdin  io.WriteCloser
}

// startScope starts a holder, the pin helper, which waits on stdin and
// dies with the caller, and asks systemd for a scope that adopts it.
// systemd cannot adopt a process that does not exist yet, and moving the
// command in after it started would let what it forks first escape the
// limits. With the holder the scope exists first, and the command is
// cloned straight into the leaf (clone3 CLONE_INTO_CGROUP), as with
// cgroupfs.
func startScope(c cgroup.Cgroup, name string) (*systemdScope, []LimitApplied, error) {
	h, in, err := startHolder()
	if err != nil {
		return nil, nil, err
	}
	s := &systemdScope{holder: h, stdin: in}
	ctx, cancel := context.WithTimeout(context.Background(), scopeTimeout)
	defer cancel()
	scope, limits, err := cgroup.StartScope(ctx, c, name, h.Process.Pid)
	s.scope = scope
	if err == nil {
		err = os.Mkdir(s.leaf(), 0o755)
		if err != nil {
			err = fmt.Errorf("cgroup: %w", err)
		}
	}
	if err != nil {
		s.remove()
		return nil, nil, err
	}
	return s, limits, nil
}

// startHolder starts the pin helper, which waits on stdin and dies with
// the caller. Closing in ends it.
func startHolder() (*exec.Cmd, io.WriteCloser, error) {
	if err := reexec.Check(); err != nil {
		return nil, nil, err
	}
	h := exec.Command("/proc/self/exe")
	h.Args = []string{reexec.Pin}
	h.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	in, err := h.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := h.Start(); err != nil {
		return nil, nil, fmt.Errorf("cgroup: scope holder: %w", err)
	}
	return h, in, nil
}

// hostScope is a detached child's transient scope (#240) while Detach
// starts the child: the holder keeps it alive until the child is in it.
type hostScope struct {
	scope  cgroup.Scope
	holder *exec.Cmd
	stdin  io.WriteCloser
	once   sync.Once
}

// startHostScope makes the scope a detached child starts in, as
// startScope does for a command: a holder first, then the scope adopts
// it, and the child is cloned into the scope dir. The scope has no
// limits and no leaf; the child is its only process once the holder
// exits, and systemd collects it when the child and what it forked
// are gone.
func startHostScope(s DetachScope) (*hostScope, error) {
	h, in, err := startHolder()
	if err != nil {
		return nil, err
	}
	hs := &hostScope{holder: h, stdin: in}
	ctx, cancel := context.WithTimeout(context.Background(), scopeTimeout)
	defer cancel()
	hs.scope, err = cgroup.StartHostScope(ctx, s.Slice, s.Name, h.Process.Pid)
	if err != nil {
		hs.release()
		return nil, err
	}
	return hs, nil
}

// release ends the holder. Once the child is in the scope that leaves
// the scope to it; before, systemd collects the empty scope. Later
// calls do nothing.
func (s *hostScope) release() {
	s.once.Do(func() {
		_ = s.stdin.Close()
		_ = s.holder.Wait()
	})
}

func (s *systemdScope) leaf() string { return filepath.Join(s.scope.Dir, scopeLeaf) }

// remove kills what is left in the scope, removes the leaf and ends the
// holder. systemd collects the scope once it is empty.
func (s *systemdScope) remove() {
	if s.scope.Dir != "" {
		leaf := s.leaf()
		if cgroup.Kill(leaf) {
			cgroup.WaitEmpty(leaf, 5*time.Second)
		}
		if err := cgroup.Remove(leaf); err != nil && !errors.Is(err, os.ErrNotExist) {
			// Something survived; the scope's kill below reaches it.
			_ = cgroup.Kill(s.scope.Dir)
		}
	}
	_ = s.stdin.Close()
	_ = s.holder.Wait()
}
