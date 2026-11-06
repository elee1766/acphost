// Package reexec runs the helpers acphost re-execs its own binary as
// (#157). A program opts in by calling proc.MaybeChild first in main;
// importing a package has no side effects. A helper is picked by argv[0]
// (argv[1] for the DNS check, which pasta execs by path) and reads its
// input as JSON from an inherited fd, never from the environment. Bad or
// missing input exits 127.
package reexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
)

// argv[0] (argv[1] for DNSCheck) of each helper.
const (
	Userns   = "acphost-userns"
	Pdeath   = "acphost-pdeath"
	DNSCheck = "acphost-dnscheck"
	Pin      = "acphost-pin"
)

// Config fds. Both are fixed so the userns helper can exec the pdeath
// helper with its fd still in place (user and pasta together).
const (
	UsernsFd = 3
	PdeathFd = 4
)

// ErrNotCalled is returned when a helper is needed but the program did
// not call proc.MaybeChild, so the re-exec would run the program's own
// main instead of the helper.
var ErrNotCalled = errors.New("acphost: this needs a helper re-exec: call proc.MaybeChild() first in main (and in TestMain)")

var called atomic.Bool

// Check returns ErrNotCalled unless MaybeChild ran in this process.
// /proc/self/exe is this binary, so its main calls MaybeChild too.
func Check() error {
	if !called.Load() {
		return ErrNotCalled
	}
	return nil
}

// SetCalled sets the flag Check reads and returns the old value. For
// tests.
func SetCalled(v bool) bool { return called.Swap(v) }

// MaybeChild runs the helper named by argv and never returns when there
// is one. Otherwise it records the call and returns.
func MaybeChild() {
	called.Store(true)
	if len(os.Args) == 0 {
		return
	}
	switch os.Args[0] {
	case Userns:
		usernsChild()
	case Pdeath:
		pdeathChild()
	case Pin:
		pinChild()
	}
	if len(os.Args) == 4 && os.Args[1] == DNSCheck {
		dnsCheckChild(os.Args[2], os.Args[3])
	}
}

func failer(name string) func(format string, a ...any) {
	return func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, "acphost "+name+": "+format+"\n", a...)
		os.Exit(127)
	}
}

// readConfig reads one JSON value from fd and closes it. ok is false
// when the fd is closed with nothing written.
func readConfig(fd int, v any) (ok bool, err error) {
	f := os.NewFile(uintptr(fd), "acphost-config")
	if f == nil {
		return false, fmt.Errorf("fd %d not open", fd)
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, 1<<16))
	if err != nil {
		return false, fmt.Errorf("read fd %d: %w", fd, err)
	}
	if len(b) == 0 {
		return false, nil
	}
	if err := json.Unmarshal(b, v); err != nil {
		return false, fmt.Errorf("config: %w", err)
	}
	return true, nil
}

// ConfigPipe returns a pipe whose write end already holds v. The caller
// passes r to the child and closes both after start.
func ConfigPipe(v any) (r, w *os.File, err error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, nil, err
	}
	if r, w, err = os.Pipe(); err != nil {
		return nil, nil, err
	}
	// Small enough for the pipe buffer, so this does not block.
	if _, err := w.Write(b); err != nil {
		_ = r.Close()
		_ = w.Close()
		return nil, nil, err
	}
	return r, w, nil
}

// SetExtraFile puts f at fd in files (fd 3 is index 0), padding with
// nil (closed in the child).
func SetExtraFile(files []*os.File, fd int, f *os.File) []*os.File {
	for len(files) <= fd-3 {
		files = append(files, nil)
	}
	files[fd-3] = f
	return files
}
