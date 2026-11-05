package acphosttest_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elee1766/acphost"
	"github.com/elee1766/acphost/acphosttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func agent(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700))
	return p
}

// goroutines returns the stacks of all goroutines except os/signal.loop,
// which the runtime starts on the first signal.Notify and keeps forever.
func goroutines() []string {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for g := range strings.SplitSeq(string(buf), "\n\n") {
		if !strings.Contains(g, "os/signal.loop") {
			out = append(out, g)
		}
	}
	return out
}

// noLeak fails if goroutines outlive the host. Without goleak, it waits
// for the count to return to the start and prints the stacks if not.
func noLeak(t *testing.T) {
	before := len(goroutines())
	t.Cleanup(func() {
		var gs []string
		for range 200 {
			if gs = goroutines(); len(gs) <= before {
				return
			}
			time.Sleep(10 * time.Millisecond) // goroutines exit asynchronously
		}
		t.Errorf("goroutines: %d before, %d after\n%s", before, len(gs), strings.Join(gs, "\n\n"))
	})
}

func TestStartConnectStop(t *testing.T) {
	noLeak(t)
	dir := t.TempDir()
	var h *acphosttest.Host
	t.Run("host", func(t *testing.T) {
		h = acphosttest.Start(t, dir, acphost.Spec{
			Session: "s1", Command: agent(t, dir, "exec sleep 600"), Dir: dir, GCRootDir: dir, LeaseMS: 60_000,
		})
		nc, _, w, err := acphost.Connect(t.Context(), dir)
		require.NoError(t, err)
		assert.Equal(t, acphost.Protocol, w.Protocol)
		_ = nc.Close()
	})
	// The subtest's cleanup has stopped the host.
	select {
	case <-h.Done():
	default:
		t.Fatal("host still running after cleanup")
	}
	require.NoError(t, h.Err())
	ex, ok := acphost.ReadExit(dir)
	require.True(t, ok)
	assert.Equal(t, "controller requested stop", ex.Reason)
	assert.NoFileExists(t, filepath.Join(dir, acphost.PidFile))
	assert.NoFileExists(t, filepath.Join(dir, acphost.ACPSocket))
}

func TestAgentExit(t *testing.T) {
	noLeak(t)
	dir := t.TempDir()
	// The agent waits for a line on stdin, then exits 3.
	h := acphosttest.Start(t, dir, acphost.Spec{
		Session: "s1", Command: agent(t, dir, "read x; exit 3"), Dir: dir, GCRootDir: dir,
	})
	nc, _, _, err := acphost.Connect(t.Context(), dir)
	require.NoError(t, err)
	_, err = nc.Write([]byte(`{"jsonrpc":"2.0","method":"x"}` + "\n"))
	require.NoError(t, err)
	ex, ok := h.Exit()
	_ = nc.Close()
	require.True(t, ok)
	assert.Equal(t, acphost.Exit{Reason: "agent exited", ExitCode: 3}, ex)
	h.Stop()
	h.Stop() // idempotent
	assert.NoFileExists(t, filepath.Join(dir, acphost.PidFile))
}

func TestCustomPrefix(t *testing.T) {
	noLeak(t)
	dir := t.TempDir()
	h := acphosttest.Start(t, dir, acphost.Spec{
		Session: "s1", Command: agent(t, dir, "exec sleep 600"), Dir: dir, GCRootDir: dir, MethodPrefix: "_x/",
	})
	h.Stop()
	ex, ok := h.Exit()
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(ex.Reason, "controller requested stop"), ex.Reason)
}
