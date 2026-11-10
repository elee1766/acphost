package acphost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/gfx-labs/acphost/pkg/cgroup"
	"github.com/gfx-labs/acphost/pkg/proc"
	"github.com/gfx-labs/acphost/pkg/reexec"
)

// hostArgv0 is the detached host's argv[0], dispatched by MaybeChild.
const hostArgv0 = "acphost-host"

// hostConfig is what StartDetached passes to the host child.
type hostConfig struct {
	Dir string `json:"dir"`
}

// ErrNoMaybeChild is returned by StartDetached when the program did not
// call acphost.MaybeChild (proc.MaybeChild is not enough: it does not
// know the host child, so the re-exec would run the program's own main).
var ErrNoMaybeChild = errors.New("acphost: StartDetached needs acphost.MaybeChild() first in main (and in TestMain)")

var maybeChildCalled atomic.Bool

// MaybeChild is proc.MaybeChild plus the detached host started by
// StartDetached: when this process is that host it runs Main with opts
// and exits, else it returns at once. Call it first in main (and in
// TestMain) instead of proc.MaybeChild. opts apply to every host this
// binary starts detached.
func MaybeChild(opts ...Option) {
	maybeChildCalled.Store(true)
	if len(os.Args) > 0 && os.Args[0] == hostArgv0 {
		reexec.SetCalled(true)
		hostChild(opts)
	}
	proc.MaybeChild()
}

// DetachOption configures StartDetached.
type DetachOption func(*detachOptions)

type detachOptions struct {
	cgroupDir string
	scope     *string
}

// InCgroup starts the host in the existing cgroup v2 directory dir, so
// the host and an agent without Spec.Cgroup count there and not in the
// caller's cgroup (jobshop keeps session hosts out of the controller's
// share). The caller needs write access to dir's cgroup.procs. A dir
// that cannot be opened fails StartDetached before anything starts.
func InCgroup(dir string) DetachOption {
	return func(o *detachOptions) { o.cgroupDir = dir }
}

// InScope starts the host in a new transient systemd scope,
// acphost-host-<session>-<pid>.scope, in slice (empty: the manager's
// default), as the systemd cgroup driver does for an agent (#240). The
// manager owns that cgroup, so the host and an agent without
// Spec.Cgroup are outside the caller's unit and a restart of the unit
// does not touch them. systemd collects the scope once the host and
// what it forked are gone. Needs the systemd manager the driver needs
// (cgroup.ManagerSocket). Not with InCgroup.
func InScope(slice string) DetachOption {
	return func(o *detachOptions) { o.scope = &slice }
}

// StartDetached starts a host for the session in dir, whose spec is
// already written (WriteSpec), in a process that outlives the caller:
// this binary re-exec'd in its own session, as proc.StartDetached runs
// a command. The host's stderr goes to LogFile in dir; env is its
// environment, which the agent inherits (nil inherits the caller's).
//
// It returns once the agent started. A setup error before then is
// returned here, as a *SpawnError when the host wrote one to exit.json.
// The returned Detached works as with proc: Running, Stop (the host
// stops the agent), and Wait, which returns exit.json with the agent's
// exit code, signal and cgroup stats; ReadExit has the reason. Needs
// MaybeChild in main.
func StartDetached(dir string, env []string, opts ...DetachOption) (*proc.Detached, error) {
	if !maybeChildCalled.Load() {
		return nil, ErrNoMaybeChild
	}
	var o detachOptions
	for _, f := range opts {
		f(&o)
	}
	if o.cgroupDir != "" && o.scope != nil {
		return nil, Permanentf("acphost: InCgroup and InScope are exclusive")
	}
	if o.scope != nil && *o.scope != "" && !cgroup.ValidSlice(*o.scope) {
		return nil, Permanentf("acphost: InScope slice %q is not a slice name (want <name>.slice)", *o.scope)
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	spec, err := ReadSpec(dir)
	if err != nil {
		return nil, err
	}
	if Alive(dir) {
		return nil, fmt.Errorf("another host owns %s", dir)
	}
	if err := proc.PrepareDetached(dir); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(filepath.Join(dir, LogFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() { _ = logf.Close() }()
	ds := proc.DetachSpec{Argv0: hostArgv0, Config: hostConfig{Dir: dir}, Dir: dir, Env: env, Log: logf, CgroupDir: o.cgroupDir}
	if o.scope != nil {
		ds.Scope = &proc.DetachScope{Slice: *o.scope, Name: spec.Session}
	}
	pid, err := proc.Detach(ds)
	if err != nil {
		// exit.json was removed above, so one here is this host's.
		if ex, ok := ReadExit(dir); ok && ex.Reason == ReasonSpawnFailed {
			return nil, &SpawnError{Exit: ex, Err: errors.New(ex.Error)}
		}
		return nil, err
	}
	return &proc.Detached{Dir: dir, Pid: pid}, nil
}

// hostChild is the detached host: Main, reporting to StartDetached once
// the agent started or why it did not.
func hostChild(opts []Option) {
	var cfg hostConfig
	ready, err := proc.DetachedChild(&cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "acphost host: %v\n", err)
		os.Exit(127)
	}
	opts = append(opts, func(o *options) {
		o.started = func(pid int) { ready(pid, nil) }
	})
	if err := Main(cfg.Dir, opts...); err != nil {
		// Does nothing if the agent had started: then StartDetached has
		// returned and the error is only in the log and exit.json.
		ready(0, err)
		fmt.Fprintf(os.Stderr, "acphost host: %v\n", err)
		os.Exit(1)
	}
	// Main returns nil only after the agent ran, so ready was called.
	ready(0, errors.New("acphost: host exited before the agent started"))
	os.Exit(0)
}
