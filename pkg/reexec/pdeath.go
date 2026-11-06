package reexec

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

// PdeathConfig is the pdeath helper's input on PdeathFd.
type PdeathConfig struct {
	Parent int `json:"parent"`
}

// pdeathChild runs os.Args[1:] and kills it when the parent dies (#76).
//
// pasta sets up its own user namespace, which clears the parent-death
// signal the host gave it, so a SIGKILLed host left pasta running, and
// with it bwrap (--die-with-parent follows pasta) and the agent. This
// helper keeps a parent-death signal of its own and turns it into a
// SIGKILL of its child.
func pdeathChild() {
	fail := failer("pdeath")
	// The parent-death signal is per thread. The host starts this helper
	// with SIGUSR1 already set on the main thread (the one it exec'd on);
	// set it on this one too, in case main runs elsewhere.
	runtime.LockOSThread()
	if err := unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(syscall.SIGUSR1), 0, 0, 0); err != nil {
		fail("pdeathsig: %v", err)
	}
	var cfg PdeathConfig
	ok, err := readConfig(PdeathFd, &cfg)
	if err != nil {
		fail("%v", err)
	}
	if !ok || cfg.Parent <= 0 {
		fail("no config on fd %d", PdeathFd)
	}
	if len(os.Args) < 2 {
		fail("no command")
	}
	died := make(chan os.Signal, 1)
	signal.Notify(died, syscall.SIGUSR1)
	// SIGTERM to the process group reaches the child too. Outlive it so
	// the host sees the child's exit status.
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	if os.Getppid() != cfg.Parent {
		os.Exit(127) // host died before the signal was set
	}
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fail("%v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case <-died:
			// bwrap's --die-with-parent is cleared the same way, so
			// killing pasta alone can leave bwrap running. The host made
			// this helper a process group leader, and pasta and the outer
			// bwrap stay in the group; the outer bwrap is pid 1 of the
			// sandbox pid namespace, so its death ends the rest.
			if unix.Getpgrp() == os.Getpid() {
				_ = unix.Kill(-os.Getpid(), unix.SIGKILL)
			}
			_ = cmd.Process.Kill()
		case s := <-term:
			_ = cmd.Process.Signal(s)
		case err := <-done:
			exitLike(err)
		}
	}
}

// exitLike exits the way the child did: same code, or killed by the
// same signal.
func exitLike(err error) {
	var ee *exec.ExitError
	if err == nil {
		os.Exit(0)
	}
	if !errors.As(err, &ee) {
		os.Exit(127)
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if ok && ws.Signaled() {
		signal.Reset(ws.Signal())
		_ = syscall.Kill(os.Getpid(), ws.Signal())
		os.Exit(128 + int(ws.Signal()))
	}
	os.Exit(ee.ExitCode())
}

// PdeathSignal is the Pdeathsig to start the pdeath helper with, not
// SIGKILL: the setting is per thread, and a SIGKILL on its main thread
// would kill the helper before it can kill its process group.
const PdeathSignal = syscall.SIGUSR1

// PdeathWrap prefixes argv with the pdeath helper and returns the config
// fd to pass at PdeathFd. The helper argv has Path /proc/self/exe, which
// still works if the binary was replaced on disk. See PdeathSignal.
func PdeathWrap(argv []string) (path string, out []string, r, w *os.File, err error) {
	if err := Check(); err != nil {
		return "", nil, nil, nil, err
	}
	r, w, err = ConfigPipe(PdeathConfig{Parent: os.Getpid()})
	if err != nil {
		return "", nil, nil, nil, err
	}
	return "/proc/self/exe", append([]string{Pdeath}, argv...), r, w, nil
}
