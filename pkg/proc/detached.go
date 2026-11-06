package proc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/elee1766/acphost/pkg/reexec"
	"golang.org/x/sys/unix"
)

// Files StartDetached writes under its dir.
const (
	// SupervisorPidFile holds the supervisor's pid, decimal. Stop
	// signals it.
	SupervisorPidFile = "supervisor.pid"
	// LockFile is flocked by the supervisor for its lifetime, so a
	// waiter blocks on it instead of polling.
	LockFile = "supervisor.lock"
	// IsolationFile holds Isolation, written once the command started.
	IsolationFile = "isolation.json"
	// ExitFile holds Exit, written when the command ended.
	ExitFile = "exit.json"
	// OutputFile gets the command's stdout and stderr.
	OutputFile = "output.log"
)

// supervisor argv[0] and fds: config in, status out.
const (
	superviseArgv0 = "acphost-supervise"
	superviseCfgFd = 3
	superviseOutFd = 4
)

type superviseConfig struct {
	Dir   string        `json:"dir"`
	Spec  Spec          `json:"spec"`
	Grace time.Duration `json:"grace"`
}

// superviseStatus is the one line the supervisor reports on its status
// fd: the command's pid once started, or why it did not start.
type superviseStatus struct {
	Pid   int    `json:"pid,omitempty"`
	Error string `json:"error,omitempty"`
}

// Detached is a command started by StartDetached or found by Open. It
// outlives the caller: a supervisor process (this binary re-exec'd, in
// its own session) runs it with Start and records it under Dir.
type Detached struct {
	Dir string
	// Pid is the supervisor's pid.
	Pid int
}

// StartDetached runs s under a supervisor that survives the caller. dir
// must exist and is owned by this command: the supervisor writes
// SupervisorPidFile, IsolationFile, ExitFile and OutputFile there and
// holds LockFile while it runs. Stop's grace before SIGKILL is grace.
// It returns once the command started; a setup error from Start is
// returned here. Needs MaybeChild in main.
func StartDetached(dir string, s Spec, grace time.Duration) (*Detached, error) {
	if err := reexec.Check(); err != nil {
		return nil, err
	}
	if d, err := Open(dir); err == nil && d.Running() {
		return nil, fmt.Errorf("proc: %s: supervisor %d already running", dir, d.Pid)
	}
	for _, f := range []string{ExitFile, IsolationFile} {
		_ = os.Remove(filepath.Join(dir, f))
	}
	cfgR, cfgW, err := reexec.ConfigPipe(superviseConfig{Dir: dir, Spec: s, Grace: grace})
	if err != nil {
		return nil, err
	}
	_ = cfgW.Close()
	defer func() { _ = cfgR.Close() }()
	stR, stW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer func() { _ = stR.Close() }()
	cmd := exec.Command("/proc/self/exe")
	cmd.Args = []string{superviseArgv0}
	cmd.Dir = "/"
	files := reexec.SetExtraFile(nil, superviseCfgFd, cfgR)
	cmd.ExtraFiles = reexec.SetExtraFile(files, superviseOutFd, stW)
	// Own session and no parent-death signal: it outlives the caller.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	_ = stW.Close()
	if err != nil {
		return nil, err
	}
	pid := cmd.Process.Pid
	// Reap it in the background while the caller lives; after that
	// init does.
	go func() { _ = cmd.Wait() }()
	b, _ := io.ReadAll(io.LimitReader(stR, 1<<16))
	var st superviseStatus
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("proc: supervisor exited before reporting: %q", b)
	}
	if st.Error != "" {
		return nil, errors.New(st.Error)
	}
	return &Detached{Dir: dir, Pid: pid}, nil
}

// Open finds the detached command recorded in dir. It works whether or
// not the supervisor is still running.
func Open(dir string) (*Detached, error) {
	b, err := os.ReadFile(filepath.Join(dir, SupervisorPidFile))
	if err != nil {
		return nil, err
	}
	pid, err := strconv.Atoi(string(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", SupervisorPidFile, err)
	}
	return &Detached{Dir: dir, Pid: pid}, nil
}

// Running reports whether the supervisor still holds the lock. It never
// blocks.
func (d *Detached) Running() bool {
	f, err := os.Open(filepath.Join(d.Dir, LockFile))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	err = unix.Flock(int(f.Fd()), unix.LOCK_SH|unix.LOCK_NB)
	return errors.Is(err, unix.EWOULDBLOCK)
}

// Exit reads ExitFile. ok is false while the command runs, or if the
// supervisor died without writing it.
func (d *Detached) Exit() (ex Exit, ok bool) {
	b, err := os.ReadFile(filepath.Join(d.Dir, ExitFile))
	if err != nil || json.Unmarshal(b, &ex) != nil {
		return Exit{}, false
	}
	return ex, true
}

// Isolation reads IsolationFile.
func (d *Detached) Isolation() (Isolation, error) {
	var iso Isolation
	b, err := os.ReadFile(filepath.Join(d.Dir, IsolationFile))
	if err != nil {
		return iso, err
	}
	return iso, json.Unmarshal(b, &iso)
}

// Wait blocks until the supervisor exits or ctx is done, then returns
// the recorded exit. A supervisor that died without recording one is an
// error.
func (d *Detached) Wait(ctx context.Context) (Exit, error) {
	f, err := os.Open(filepath.Join(d.Dir, LockFile))
	if err != nil {
		return Exit{}, err
	}
	defer func() { _ = f.Close() }()
	done := make(chan error, 1)
	go func() { done <- unix.Flock(int(f.Fd()), unix.LOCK_SH) }()
	select {
	case err := <-done:
		if err != nil {
			return Exit{}, err
		}
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	case <-ctx.Done():
		// Closing f does not cancel the flock, so the goroutine ends
		// when the supervisor does.
		return Exit{}, ctx.Err()
	}
	ex, ok := d.Exit()
	if !ok {
		return Exit{}, fmt.Errorf("proc: supervisor %d exited without %s", d.Pid, ExitFile)
	}
	return ex, nil
}

// Stop asks the supervisor to stop the command (SIGTERM, then SIGKILL
// after the grace given to StartDetached). It returns at once; use Wait.
func (d *Detached) Stop() error {
	if !d.Running() {
		return nil
	}
	return unix.Kill(d.Pid, unix.SIGTERM)
}

// superviseChild is the supervisor: it runs the command with Start,
// records it under the dir and exits when it does.
func superviseChild() {
	fail := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, "acphost supervise: "+format+"\n", a...)
		os.Exit(127)
	}
	// Inherited fds are not close-on-exec; the command must not hold
	// the status pipe open, or StartDetached never sees EOF.
	unix.CloseOnExec(superviseCfgFd)
	unix.CloseOnExec(superviseOutFd)
	out := os.NewFile(superviseOutFd, "acphost-status")
	if out == nil {
		fail("status fd %d not open", superviseOutFd)
	}
	report := func(st superviseStatus) {
		b, _ := json.Marshal(st)
		_, _ = out.Write(b)
		_ = out.Close()
	}
	var cfg superviseConfig
	ok, err := reexec.ReadConfig(superviseCfgFd, &cfg)
	if err != nil || !ok {
		fail("config: %v", err)
	}
	// The command's parent-death signal follows this thread.
	runtime.LockOSThread()
	lock, err := os.OpenFile(filepath.Join(cfg.Dir, LockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		report(superviseStatus{Error: err.Error()})
		os.Exit(1)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		report(superviseStatus{Error: "proc: " + cfg.Dir + " is locked by another supervisor"})
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(cfg.Dir, SupervisorPidFile), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		report(superviseStatus{Error: err.Error()})
		os.Exit(1)
	}
	logf, err := os.OpenFile(filepath.Join(cfg.Dir, OutputFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		report(superviseStatus{Error: err.Error()})
		os.Exit(1)
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	p, err := Start(context.Background(), cfg.Spec, Stdout(logf), Stderr(logf))
	if err != nil {
		report(superviseStatus{Error: err.Error()})
		os.Exit(1)
	}
	if b, err := json.Marshal(p.Isolation()); err == nil {
		_ = writeAtomic(filepath.Join(cfg.Dir, IsolationFile), b)
	}
	report(superviseStatus{Pid: p.Pid()})
	go func() {
		<-sigs
		p.Stop(cfg.Grace)
	}()
	ex, _ := p.Wait()
	b, _ := json.Marshal(ex)
	_ = writeAtomic(filepath.Join(cfg.Dir, ExitFile), b)
	_ = lock.Close() // releases the flock: waiters read ExitFile now
	os.Exit(0)
}

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
