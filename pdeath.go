package acphost

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// pdeathChildEnv marks a re-exec of the host binary that runs
// os.Args[1:] and kills it when the host dies (#76). The value is the
// host pid.
//
// pasta sets up its own user namespace, which clears the parent-death
// signal the host gave it, so a SIGKILLed host left pasta running, and
// with it bwrap (--die-with-parent follows pasta) and the agent. This
// helper keeps a parent-death signal of its own and turns it into a
// SIGKILL of its child.
const pdeathChildEnv = "_ACPHOST_PDEATH_CHILD"

func init() {
	if os.Getenv(pdeathChildEnv) == "" {
		return
	}
	// Never returns.
	pdeathChild()
}

// pdeathCommand returns argv prefixed with a re-exec of the host binary
// as a pdeath helper, and the env entry that selects it.
func pdeathCommand(argv []string) ([]string, string) {
	return append([]string{"/proc/self/exe"}, argv...), pdeathChildEnv + "=" + strconv.Itoa(os.Getpid())
}

func pdeathChild() {
	fail := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, "acphost pdeath: "+format+"\n", a...)
		os.Exit(127)
	}
	host, err := strconv.Atoi(os.Getenv(pdeathChildEnv))
	if err != nil {
		fail("bad %s", pdeathChildEnv)
	}
	_ = os.Unsetenv(pdeathChildEnv)
	if len(os.Args) < 2 {
		fail("no command")
	}
	died := make(chan os.Signal, 1)
	signal.Notify(died, syscall.SIGUSR1)
	// SIGTERM to the process group reaches the child too. Outlive it so
	// the host sees the child's exit status.
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	if err := unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(syscall.SIGUSR1), 0, 0, 0); err != nil {
		fail("pdeathsig: %v", err)
	}
	if os.Getppid() != host {
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
