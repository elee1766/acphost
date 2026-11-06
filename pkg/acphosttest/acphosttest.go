// Package acphosttest runs an acphost host in the test process, so a test
// that needs a live host does not pay for spawning a host binary.
//
// A test using a user namespace or network pasta needs a TestMain that
// calls proc.MaybeChild first: the host re-execs the test binary as a
// helper. Without it the spawn fails with proc.ErrNoMaybeChild.
//
// Exit semantics:
//   - The host runs acphost.Main(dir) in a goroutine until the agent exits.
//     When the agent exits by itself, Main returns, Done is closed and
//     exit.json says why. The test keeps running.
//   - Stop, and the t.Cleanup that Start registers, send the stop method
//     over the socket (the agent gets SIGTERM, then SIGKILL after the
//     host's grace period), wait for Main to return and remove host.pid,
//     which Main leaves behind. Cleanup fails the test if Main does not
//     return within StopTimeout.
//   - Main installs SIGTERM and SIGINT handlers and ignores SIGHUP and
//     SIGPIPE while it runs, in the whole test process. A SIGTERM sent to
//     the test binary stops the host instead of killing the test.
//   - Main locks its goroutine to an OS thread, and the agent is started
//     from it with Pdeathsig, so an agent dies with the test process.
package acphosttest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elee1766/acphost/pkg/acphost"
)

// StopTimeout bounds how long Stop waits for Main to return.
var StopTimeout = 30 * time.Second

// Host is a host running in the test process.
type Host struct {
	Dir  string
	Spec acphost.Spec

	t    testing.TB
	done chan struct{}
	err  error
}

// Start writes spec into dir, runs acphost.Main(dir, opts...) in a
// goroutine and returns once acp.sock accepts a connection or Main has
// returned (an agent that exits at once). Spec.Protocol is filled in when
// zero. A spawn failure fails the test. Cleanup stops the host.
func Start(t testing.TB, dir string, spec acphost.Spec, opts ...acphost.Option) *Host {
	t.Helper()
	if spec.Protocol == 0 {
		spec.Protocol = acphost.Protocol
	}
	if err := acphost.WriteSpec(dir, spec); err != nil {
		t.Fatalf("acphosttest: write spec: %v", err)
	}
	return start(t, dir, spec, opts)
}

// StartDir is Start for a dir whose spec is already written, as by a
// caller that builds the spec itself and only hands over the dir (#127).
func StartDir(t testing.TB, dir string, opts ...acphost.Option) *Host {
	t.Helper()
	spec, err := acphost.ReadSpec(dir)
	if err != nil {
		t.Fatalf("acphosttest: read spec: %v", err)
	}
	return start(t, dir, spec, opts)
}

func start(t testing.TB, dir string, spec acphost.Spec, opts []acphost.Option) *Host {
	t.Helper()
	h := &Host{Dir: dir, Spec: spec, t: t, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		h.err = acphost.Main(dir, opts...)
	}()
	t.Cleanup(h.Stop)

	deadline := time.Now().Add(StopTimeout)
	for {
		nc, _, _, err := acphost.Connect(context.Background(), dir, acphost.WithMethodPrefix(spec.MethodPrefix))
		if err == nil {
			_ = nc.Close()
			return h
		}
		var se *acphost.SpawnError
		if errors.As(err, &se) {
			t.Fatalf("acphosttest: %v", err)
		}
		select {
		case <-h.done:
			if h.err != nil {
				t.Fatalf("acphosttest: host: %v", h.err)
			}
			return h
		case <-time.After(5 * time.Millisecond): // no event for "socket listening" outside Main
		}
		if time.Now().After(deadline) {
			t.Fatalf("acphosttest: host did not listen: %v", err)
		}
	}
}

// Done is closed when Main has returned.
func (h *Host) Done() <-chan struct{} { return h.done }

// Err is Main's return value. It is valid once Done is closed.
func (h *Host) Err() error {
	<-h.done
	return h.err
}

// Exit waits for Main to return and reads exit.json.
func (h *Host) Exit() (acphost.Exit, bool) {
	<-h.done
	return acphost.ReadExit(h.Dir)
}

// Stop asks the host to stop, waits for Main to return and removes
// host.pid. It is safe to call more than once and after the agent exited.
func (h *Host) Stop() {
	h.t.Helper()
	select {
	case <-h.done:
	default:
		h.sendStop()
		select {
		case <-h.done:
		case <-time.After(StopTimeout):
			h.t.Errorf("acphosttest: host in %s did not exit within %s", h.Dir, StopTimeout)
			return
		}
	}
	if err := os.Remove(filepath.Join(h.Dir, acphost.PidFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		h.t.Errorf("acphosttest: remove pid file: %v", err)
	}
}

// sendStop writes the stop notification. A failed connect means the host
// is already exiting, which Stop then waits for.
func (h *Host) sendStop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	nc, _, _, err := acphost.Connect(ctx, h.Dir, acphost.WithMethodPrefix(h.Spec.MethodPrefix))
	if err != nil {
		return
	}
	defer func() { _ = nc.Close() }()
	stop := acphost.MethodName(h.Spec.MethodPrefix, "stop")
	_, _ = nc.Write([]byte(`{"jsonrpc":"2.0","method":"` + stop + `"}` + "\n"))
	// Hold the connection until Main returns, so the stop is read.
	select {
	case <-h.done:
	case <-time.After(StopTimeout):
	}
}
