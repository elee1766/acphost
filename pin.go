package acphost

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"syscall"

	"github.com/elee1766/acphost/internal/reexec"
)

// Pinned is a set of namespaces kept alive by a holder process, made by
// Pin. Paths maps each type to its /proc/<pid>/ns/<type> link, for
// Spec.Join. Close ends the holder; namespaces nothing else uses go away.
type Pinned struct {
	Paths map[string]string
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

// Pin creates new namespaces of the given types (see JoinTypes) and
// keeps them until Close, so several sessions can join one network or
// one user namespace. As non-root it always adds a user namespace that
// maps uid and gid 0 to the caller, since the others need one to be
// created and joined. The holder is a re-exec of this binary (call
// proc.MaybeChild first in main) and dies with the caller.
//
// A holder process is used instead of a bind mount of the ns file
// because an unprivileged caller cannot bind-mount on the host.
func Pin(types ...string) (*Pinned, error) {
	if err := reexec.Check(); err != nil {
		return nil, err
	}
	var flags uintptr
	for _, t := range types {
		f, ok := nsFlags[t]
		if !ok {
			return nil, fmt.Errorf("pin: unknown namespace type %q", t)
		}
		flags |= uintptr(f)
	}
	if os.Geteuid() != 0 && !slices.Contains(types, "user") {
		types = append(types, "user")
		flags |= syscall.CLONE_NEWUSER
	}
	cmd := exec.Command("/proc/self/exe")
	cmd.Args = []string{reexec.Pin}
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: flags, Pdeathsig: syscall.SIGKILL}
	if flags&syscall.CLONE_NEWUSER != 0 {
		cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Geteuid(), Size: 1}}
		cmd.SysProcAttr.GidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getegid(), Size: 1}}
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("pin: %w", err)
	}
	p := &Pinned{Paths: map[string]string{}, cmd: cmd, stdin: in}
	for _, t := range types {
		p.Paths[t] = fmt.Sprintf("/proc/%d/ns/%s", cmd.Process.Pid, t)
	}
	return p, nil
}

// Close ends the holder and waits for it.
func (p *Pinned) Close() error {
	_ = p.stdin.Close()
	err := p.cmd.Wait()
	if err != nil {
		return fmt.Errorf("pin: %w", err)
	}
	return nil
}
