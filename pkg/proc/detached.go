package proc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

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

// superviseArgv0 is the supervisor's argv[0], dispatched by MaybeChild.
const superviseArgv0 = "acphost-supervise"

type superviseConfig struct {
	Dir   string        `json:"dir"`
	Spec  Spec          `json:"spec"`
	Grace time.Duration `json:"grace"`
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
	if err := PrepareDetached(dir); err != nil {
		return nil, err
	}
	pid, err := Detach(DetachSpec{Argv0: superviseArgv0, Config: superviseConfig{Dir: dir, Spec: s, Grace: grace}})
	if err != nil {
		return nil, err
	}
	return &Detached{Dir: dir, Pid: pid}, nil
}

// PrepareDetached refuses dir while a supervisor holds it, and removes
// the previous run's ExitFile and IsolationFile, so a reader does not
// take them for the next run's.
func PrepareDetached(dir string) error {
	if d, err := Open(dir); err == nil && d.Running() {
		return fmt.Errorf("proc: %s: supervisor %d already running", dir, d.Pid)
	}
	for _, f := range []string{ExitFile, IsolationFile} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
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
	// The goroutine owns f: closing it here while the flock blocks would
	// race with its Fd and, once closed, make the flock fail with EBADF.
	// Closing does not cancel a blocked flock anyway, so after ctx ends
	// the goroutine stays until the supervisor exits.
	done := make(chan error, 1)
	go func() {
		err := unix.Flock(int(f.Fd()), unix.LOCK_SH)
		_ = f.Close() // releases the shared lock
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return Exit{}, err
		}
	case <-ctx.Done():
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
	var cfg superviseConfig
	ready, err := DetachedChild(&cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "acphost supervise: %v\n", err)
		os.Exit(127)
	}
	fail := func(err error) {
		ready(0, err)
		os.Exit(1)
	}
	// The command's parent-death signal follows this thread.
	runtime.LockOSThread()
	sup, err := Supervise(cfg.Dir)
	if err != nil {
		fail(err)
	}
	logf, err := os.OpenFile(filepath.Join(cfg.Dir, OutputFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fail(err)
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	p, err := Start(context.Background(), cfg.Spec, Stdout(logf), Stderr(logf))
	if err != nil {
		fail(err)
	}
	_ = sup.WriteIsolation(p.Isolation())
	ready(p.Pid(), nil)
	go func() {
		<-sigs
		p.Stop(cfg.Grace)
	}()
	ex, _ := p.Wait()
	_ = sup.WriteExit(ex)
	_ = sup.Close() // releases the flock: waiters read ExitFile now
	os.Exit(0)
}
