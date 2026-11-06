package proc

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/elee1766/acphost/pkg/cgroup"
	"github.com/elee1766/acphost/pkg/reexec"
	"github.com/elee1766/acphost/pkg/sandbox"
	"github.com/elee1766/acphost/pkg/userns"
	"golang.org/x/sys/unix"
)

// Spec is a command and the isolation to run it with. Nil isolation
// fields leave that feature off.
type Spec struct {
	Command string
	Args    []string
	// Dir is the working directory. With Sandbox it is a path inside
	// the sandbox and must lie in an RW mount.
	Dir string
	// Env is added to the caller's environment (KEY=VALUE). With
	// Sandbox, bwrap clears the environment and Env is set inside it.
	Env     []string
	Sandbox *sandbox.Sandbox
	Cgroup  *cgroup.Cgroup
	User    *userns.User
	// Join maps a namespace type (see JoinTypes) to a namespace file to
	// join instead of creating one. util-linux nsenter runs in front of
	// the command (or bwrap), user first and mnt last. With pid the
	// command is a child of nsenter and runs under the pdeath helper.
	// Validate with the caller's checks first (acphost.CheckSpawn).
	Join map[string]string
	// Name names the command's cgroup (<Cgroup.Parent>/<Name>). It must
	// be one path element. Required with Cgroup.
	Name string
}

// Option sets how Start connects the command.
type Option func(*options)

type options struct {
	stdin          io.Reader
	stdout, stderr io.Writer
}

// Stdin sets the command's stdin. Default: /dev/null.
func Stdin(r io.Reader) Option { return func(o *options) { o.stdin = r } }

// Stdout sets the command's stdout. Default: /dev/null.
func Stdout(w io.Writer) Option { return func(o *options) { o.stdout = w } }

// Stderr sets the command's stderr. Default: /dev/null. With Sandbox or
// User it goes through a reader that also watches for setup errors.
func Stderr(w io.Writer) Option { return func(o *options) { o.stderr = w } }

// Exit is how a started command ended.
type Exit struct {
	// ExitCode is the exit code, -1 if a signal killed it.
	ExitCode int `json:"exit_code"`
	// Signal is the signal that killed the command, if any. A sandboxed
	// command killed by a signal is reported here too, not as bwrap's
	// 128+n.
	Signal string `json:"signal,omitempty"`
	// SetupError is set when the command never ran: bwrap, pasta, the
	// user namespace helper or the deny rules failed. Only reported for
	// an exit nobody asked for (no Stop, no ctx cancel).
	SetupError string `json:"setup_error,omitempty"`
	// OOMKill is set when the cgroup's OOM killer killed a process.
	OOMKill bool `json:"oom_kill,omitempty"`
	// Cgroup is the cgroup's final usage, read before it was removed.
	Cgroup *cgroup.Stats `json:"cgroup,omitempty"`
}

// Process is a command started by Start.
type Process struct {
	cmd       *exec.Cmd
	iso       Isolation
	cgroup    string
	oomStart  int64
	sandboxed bool
	tap       *stderrTap

	mu      sync.Mutex
	stopped bool // Stop or ctx asked for the exit
	exited  bool
	done    chan struct{}
	exit    Exit
	waitErr error
	once    sync.Once
}

// Start starts s attached to the caller: the command is in its own
// process group (and cgroup with Cgroup), gets a parent-death SIGKILL,
// and is killed when ctx is done. The parent-death signal fires when the
// thread that called Start exits, so a long-lived caller should call it
// from a goroutine locked to its thread (runtime.LockOSThread).
//
// The returned Process must be waited for with Wait, which also removes
// the cgroup.
func Start(ctx context.Context, s Spec, opts ...Option) (*Process, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	p := &Process{done: make(chan struct{}), sandboxed: s.Sandbox != nil}
	if err := p.start(s, o); err != nil {
		if p.cgroup != "" {
			_ = cgroup.Remove(p.cgroup)
		}
		return nil, err
	}
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				p.markStopped()
				p.Kill()
			case <-p.done:
			}
		}()
	}
	return p, nil
}

func (p *Process) start(s Spec, o options) error {
	cmd := exec.Command(s.Command, s.Args...)
	// iso records what is applied, from the code that applies it.
	iso := &p.iso
	// pdeathCfg is the pdeath helper's config pipe, read end (see
	// reexec.PdeathWrap). Closed once started.
	var pdeathCfg *os.File
	_, joinPid := s.Join["pid"]
	if s.Sandbox != nil {
		sb := sandboxWithEnv(*s.Sandbox, s.Env)
		// The status lines tell a bwrap setup failure from a command
		// exit (see stderrTap). bwrap closes the status fd in the
		// sandbox, so it is a dup of stderr made just for bwrap
		// (statusWrap).
		sb.ExtraArgs = append([]string{"--json-status-fd", "3"}, sb.ExtraArgs...)
		argv, net, err := sandbox.BuildArgs(sb, s.Command, s.Args, s.Dir)
		if err != nil {
			return err
		}
		if argv, err = statusWrap(argv); err != nil {
			return err
		}
		if joinPid {
			argv = dropUnsharePid(argv)
		}
		iso.Bwrap, iso.Network = argv, net
		// bwrap does the chdir (Dir is a path inside the sandbox).
		cmd = exec.Command(argv[0], argv[1:]...)
	} else {
		cmd.Dir = s.Dir
	}
	if len(s.Join) > 0 {
		// nsenter joins, then execs the command or the sandbox. Joining
		// mnt resets the cwd, so it changes to Dir after the join
		// (bwrap does its own chdir).
		wdns := ""
		if _, ok := s.Join["mnt"]; ok && s.Sandbox == nil {
			wdns, cmd.Dir = s.Dir, ""
		}
		prefix, applied, err := joinPrefix(s.Join, wdns)
		if err != nil {
			return err
		}
		iso.Join = applied
		dir := cmd.Dir
		cmd = exec.Command(prefix[0], append(prefix[1:], cmd.Args...)...)
		cmd.Dir = dir
	}
	// With pasta, and with join.pid (nsenter forks and waits), the
	// started process is not the command and a parent-death signal on
	// it would not reach the rest.
	if iso.Network != nil && iso.Network.Mode == "pasta" || joinPid {
		path, wrapped, r, w, err := reexec.PdeathWrap(cmd.Args)
		if err != nil {
			return err
		}
		_ = w.Close() // the config is already in the pipe
		pdeathCfg = r
		defer func() { _ = pdeathCfg.Close() }()
		cmd.Path, cmd.Args, cmd.Err = path, wrapped, nil
		cmd.ExtraFiles = reexec.SetExtraFile(cmd.ExtraFiles, reexec.PdeathFd, r)
		// The helper file, then nsenter, pasta and bwrap.
		iso.PdeathHelper = []string{path}
	}
	cmd.Env = append(os.Environ(), s.Env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = o.stdin, o.stdout, o.stderr
	if s.Sandbox != nil || s.User != nil || len(s.Join) > 0 {
		dst := o.stderr
		if dst == nil {
			dst = io.Discard
		}
		tap, w, err := newStderrTap(dst)
		if err != nil {
			return err
		}
		cmd.Stderr = w
		// The child holds its own copy once started.
		defer func() { _ = w.Close() }()
		p.tap = tap
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if pdeathCfg != nil {
		cmd.SysProcAttr.Pdeathsig = reexec.PdeathSignal
	}
	if s.Cgroup != nil {
		dir, limits, err := cgroup.Prepare(*s.Cgroup, s.Name)
		if dir != "" {
			p.cgroup = dir
			p.oomStart = cgroup.OOMKills(dir)
		}
		if err != nil {
			return err
		}
		iso.Cgroup = &CgroupApplied{Dir: dir, Limits: limits}
		f, err := os.Open(dir)
		if err != nil {
			return err
		}
		// clone3 places the child in the cgroup before it runs, so
		// nothing it forks can escape. Closed once started.
		defer func() { _ = f.Close() }()
		cmd.SysProcAttr.UseCgroupFD = true
		cmd.SysProcAttr.CgroupFD = int(f.Fd())
	}
	start := cmd.Start
	if s.User != nil {
		var err error
		if start, iso.User, err = usernsCommand(cmd, *s.User); err != nil {
			return err
		}
	}
	if err := start(); err != nil {
		return err
	}
	// pasta runs its command only after configuring the namespace, so a
	// started process here means pasta started; a pasta failure later
	// shows as the command's exit.
	if iso.Network != nil && iso.Network.Mode == "pasta" {
		iso.Network.PastaStarted = true
	}
	p.cmd = cmd
	return nil
}

// sandboxWithEnv returns s with env (KEY=VALUE) set inside the sandbox.
// bwrap clears the environment, and env is what the caller asked the
// command to have.
func sandboxWithEnv(s sandbox.Sandbox, env []string) sandbox.Sandbox {
	extra := make([]string, 0, 3*len(env)+len(s.ExtraArgs))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			extra = append(extra, "--setenv", k, v)
		}
	}
	s.ExtraArgs = append(extra, s.ExtraArgs...)
	return s
}

// Pid is the started process: the command, bwrap, or the pdeath helper
// with pasta. It leads the process group.
func (p *Process) Pid() int { return p.cmd.Process.Pid }

// Isolation is what Start applied.
func (p *Process) Isolation() Isolation { return p.iso }

// CgroupDir is the command's cgroup, empty without Spec.Cgroup. Removed
// by Wait.
func (p *Process) CgroupDir() string { return p.cgroup }

func (p *Process) markStopped() {
	p.mu.Lock()
	if !p.exited {
		p.stopped = true
	}
	p.mu.Unlock()
}

// Stop sends SIGTERM to the process group and, after grace, kills the
// cgroup (or group). It returns at once; Wait reports the exit. A Stop
// after the exit does nothing.
func (p *Process) Stop(grace time.Duration) {
	p.markStopped()
	select {
	case <-p.done:
		return
	default:
	}
	_ = unix.Kill(-p.Pid(), unix.SIGTERM)
	time.AfterFunc(grace, func() {
		select {
		case <-p.done:
		default:
			p.Kill()
		}
	})
}

// Kill SIGKILLs the cgroup when there is one with cgroup.kill, which
// also reaches processes that left the process group, else the group.
func (p *Process) Kill() {
	if p.cgroup != "" && cgroup.Kill(p.cgroup) {
		return
	}
	_ = unix.Kill(-p.Pid(), unix.SIGKILL)
}

// Wait waits for the command, then kills anything it left in its
// process group or cgroup, reads the cgroup's final usage and removes
// it. Safe to call more than once; later calls return the same result.
// The error is from waiting (not a non-zero exit).
func (p *Process) Wait() (Exit, error) {
	p.once.Do(p.wait)
	return p.exit, p.waitErr
}

func (p *Process) wait() {
	err := p.cmd.Wait()
	p.mu.Lock()
	p.exited = true
	stopped := p.stopped
	p.mu.Unlock()
	close(p.done)
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		p.waitErr = err
	}
	// It led its group; kill what it left there or in the cgroup.
	_ = unix.Kill(-p.Pid(), unix.SIGKILL)
	if p.cgroup != "" {
		p.Kill()
		cgroup.WaitEmpty(p.cgroup, 5*time.Second)
	}
	// The group is dead, so stderr is closed unless something left it.
	var tap tapResult
	if p.tap != nil {
		tap = p.tap.wait(2 * time.Second)
	}
	st := p.cmd.ProcessState
	ex := Exit{ExitCode: st.ExitCode()}
	if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		ex.Signal = ws.Signal().String()
	} else if p.sandboxed && ex.ExitCode > 128 && ex.ExitCode < 128+65 {
		// bwrap passes the exit code through and reports a signal death
		// as 128+n. Report it the way an unsandboxed signal death is.
		ex.Signal = syscall.Signal(ex.ExitCode - 128).String()
		ex.ExitCode = -1
	}
	// bwrap writes an exit-code status line once the command has run,
	// and the userns and pdeath helpers prefix their errors. A non-zero
	// exit nobody asked for, with no status line or with a helper error,
	// is a setup failure.
	if p.tap != nil && !stopped && ex.Signal == "" {
		ex.SetupError = tap.setupError(p.sandboxed, ex.ExitCode)
	}
	if p.cgroup != "" {
		cs := cgroup.ReadStats(p.cgroup)
		ex.Cgroup = &cs
		ex.OOMKill = cs.OOMKills > p.oomStart
		_ = cgroup.Remove(p.cgroup)
	}
	p.exit = ex
}
