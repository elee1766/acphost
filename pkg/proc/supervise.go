package proc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"

	"github.com/gfx-labs/acphost/pkg/reexec"
	"golang.org/x/sys/unix"
)

// The parts of a detached process, shared by StartDetached and the ACP
// host (acphost.StartDetached, #210): Detach starts this binary again in
// its own session, DetachedChild is the child's side of that handshake,
// and Supervise holds the state dir (lock, pid file, records) that Open
// reads.

// Detach handshake fds in the child: config in, status out.
const (
	detachCfgFd = 3
	detachOutFd = 4
)

// detachStatus is the one line a detached child reports on its status
// fd: the pid it started once ready, or why it did not start.
type detachStatus struct {
	Pid   int    `json:"pid,omitempty"`
	Error string `json:"error,omitempty"`
}

// DetachSpec says how Detach starts the child.
type DetachSpec struct {
	// Argv0 is the child's argv[0]. The child's main dispatches on it
	// (as MaybeChild does) and calls DetachedChild.
	Argv0 string
	// Config is passed to the child as JSON; DetachedChild decodes it.
	// It must fit in a pipe buffer (64 KiB).
	Config any
	// Dir is the child's working directory, "/" when empty.
	Dir string
	// Env is the child's environment. Nil inherits the caller's.
	Env []string
	// Log gets the child's stdout and stderr. Nil discards them.
	Log *os.File
	// CgroupDir, if set, is an existing cgroup v2 directory the child
	// starts in, so its load and everything it forks count there and
	// not in the caller's cgroup. Empty keeps the caller's cgroup.
	CgroupDir string
	// Scope, if set, starts the child in a new transient systemd scope
	// (#240) instead: the manager owns its cgroup, so the child is not
	// in the caller's unit. Not with CgroupDir.
	Scope *DetachScope
}

// DetachScope names the transient systemd scope Detach starts the child
// in: unit acphost-host-<Name>-<holder pid>.scope (see
// cgroup.HostScopeUnit) in Slice, empty for the manager's default. The
// caller needs a manager (cgroup.ManagerSocket) as the systemd cgroup
// driver does.
type DetachScope struct {
	Slice string
	Name  string
}

// Detach starts this binary (/proc/self/exe) as s.Argv0 in its own
// session with no parent-death signal, so it outlives the caller. It
// returns the child's pid once the child called ready from
// DetachedChild, or the error the child passed to ready. Needs
// MaybeChild in main.
func Detach(s DetachSpec) (int, error) {
	if err := reexec.Check(); err != nil {
		return 0, err
	}
	if s.CgroupDir != "" && s.Scope != nil {
		return 0, errors.New("proc: detach: CgroupDir and Scope are exclusive")
	}
	var cg *os.File
	cgDir := s.CgroupDir
	var hs *hostScope
	if s.Scope != nil {
		// The holder keeps the scope until the child is in it.
		var err error
		if hs, err = startHostScope(*s.Scope); err != nil {
			return 0, err
		}
		defer hs.release()
		cgDir = hs.scope.Dir
	}
	if cgDir != "" {
		f, err := os.Open(cgDir)
		if err != nil {
			return 0, fmt.Errorf("proc: cgroup: %w", err)
		}
		defer func() { _ = f.Close() }()
		cg = f
	}
	cfgR, cfgW, err := reexec.ConfigPipe(s.Config)
	if err != nil {
		return 0, err
	}
	_ = cfgW.Close()
	defer func() { _ = cfgR.Close() }()
	stR, stW, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	defer func() { _ = stR.Close() }()
	cmd := exec.Command("/proc/self/exe")
	cmd.Args = []string{s.Argv0}
	cmd.Dir = s.Dir
	if cmd.Dir == "" {
		cmd.Dir = "/"
	}
	cmd.Env = s.Env
	if s.Log != nil {
		cmd.Stdout, cmd.Stderr = s.Log, s.Log
	}
	files := reexec.SetExtraFile(nil, detachCfgFd, cfgR)
	cmd.ExtraFiles = reexec.SetExtraFile(files, detachOutFd, stW)
	// Own session and no parent-death signal: it outlives the caller.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cg != nil {
		// clone3 places the child in the cgroup before it runs, as
		// Start does for Spec.Cgroup.
		cmd.SysProcAttr.UseCgroupFD = true
		cmd.SysProcAttr.CgroupFD = int(cg.Fd())
	}
	err = cmd.Start()
	_ = stW.Close()
	if hs != nil {
		// The child holds the scope now, or it never started and
		// systemd collects the empty scope.
		hs.release()
	}
	if err != nil {
		return 0, err
	}
	// Reap it in the background while the caller lives; after that
	// init does.
	go func() { _ = cmd.Wait() }()
	b, _ := io.ReadAll(io.LimitReader(stR, 1<<16))
	var st detachStatus
	if err := json.Unmarshal(b, &st); err != nil {
		return 0, fmt.Errorf("proc: %s exited before reporting: %q", s.Argv0, b)
	}
	if st.Error != "" {
		return 0, errors.New(st.Error)
	}
	return cmd.Process.Pid, nil
}

// DetachedChild is the child side of Detach. It decodes the config into
// cfg and returns ready, which the child calls once: with the pid it
// started and nil when it is up, or with the error that stopped it,
// which Detach then returns. Later calls do nothing. Call it before
// starting other processes.
func DetachedChild(cfg any) (ready func(pid int, err error), err error) {
	// Inherited fds are not close-on-exec; a command the child starts
	// must not hold the status pipe open, or Detach never sees EOF.
	unix.CloseOnExec(detachCfgFd)
	unix.CloseOnExec(detachOutFd)
	out := os.NewFile(detachOutFd, "acphost-status")
	if out == nil {
		return nil, fmt.Errorf("status fd %d not open", detachOutFd)
	}
	ok, err := reexec.ReadConfig(detachCfgFd, cfg)
	if err == nil && !ok {
		err = errors.New("no config")
	}
	if err != nil {
		_ = out.Close()
		return nil, err
	}
	var once sync.Once
	return func(pid int, err error) {
		once.Do(func() {
			st := detachStatus{Pid: pid}
			if err != nil {
				st = detachStatus{Error: err.Error()}
			}
			b, _ := json.Marshal(st)
			_, _ = out.Write(b)
			_ = out.Close()
		})
	}, nil
}

// Supervisor holds a state dir for a detached process: an exclusive
// flock on LockFile for its lifetime and its pid in SupervisorPidFile,
// so Open, Running, Wait and Stop work on the dir.
type Supervisor struct {
	dir  string
	lock *os.File
}

// Supervise takes dir for this process. It fails if another process
// holds dir's LockFile.
func Supervise(dir string) (*Supervisor, error) {
	lock, err := os.OpenFile(filepath.Join(dir, LockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("proc: %s is locked by another supervisor", dir)
	}
	if err := os.WriteFile(filepath.Join(dir, SupervisorPidFile), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return &Supervisor{dir: dir, lock: lock}, nil
}

// WriteIsolation records iso in IsolationFile.
func (s *Supervisor) WriteIsolation(iso Isolation) error {
	b, err := json.Marshal(iso)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.dir, IsolationFile), b)
}

// WriteExit records ex in ExitFile. ex is an Exit, or a type whose JSON
// has Exit's keys and more (acphost.Exit), so Detached.Wait can read it.
// Write it before Close: waiters read it once the lock is released.
func (s *Supervisor) WriteExit(ex any) error {
	b, err := json.Marshal(ex)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.dir, ExitFile), b)
}

// Close releases the lock. The pid file and records stay for Open.
func (s *Supervisor) Close() error { return s.lock.Close() }

// writeAtomic writes b to path through a temp file and rename, so a
// reader never sees a partial file.
func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
