package acphost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runSpawnFailure runs Main on s, which must fail during setup, and
// checks that both Main and a client polling Connect report a
// *SpawnError within 2s (#100).
func runSpawnFailure(t *testing.T, dir string, s Spec, want string) {
	t.Helper()
	require.NoError(t, WriteSpec(dir, s))
	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- Main(dir) }()

	var cerr error
	deadline := time.After(2 * time.Second)
poll:
	for {
		nc, _, _, err := Connect(context.Background(), dir)
		if err == nil {
			// The socket is up while bwrap sets up; it closes once the
			// failure is seen.
			_ = nc.Close()
		}
		var se *SpawnError
		if errors.As(err, &se) {
			cerr = err
			break
		}
		select {
		case <-deadline:
			break poll
		case <-time.After(20 * time.Millisecond):
		}
	}
	require.Error(t, cerr, "Connect did not report the spawn failure within 2s")
	assert.Less(t, time.Since(start), 2*time.Second)

	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("host did not exit")
	}
	var se *SpawnError
	require.ErrorAs(t, err, &se)
	assert.Contains(t, se.Error(), want)
	assert.Equal(t, ReasonSpawnFailed, se.Exit.Reason)
	require.ErrorAs(t, cerr, &se)
	assert.Contains(t, se.Error(), want)

	ex, ok := ReadExit(dir)
	require.True(t, ok)
	assert.Equal(t, ReasonSpawnFailed, ex.Reason)
	assert.Contains(t, ex.Error, want)
}

func TestSpawnFailurePlain(t *testing.T) {
	dir := t.TempDir()
	s := Spec{Protocol: Protocol, Session: "s1", Command: filepath.Join(dir, "missing"), Dir: dir, GCRootDir: dir, LeaseMS: 60_000}
	runSpawnFailure(t, dir, s, "no such file")
}

// TestMatrixSpawnFailure forces a setup failure in each isolation
// flavour: a bwrap argument it rejects when sandboxed, a missing command
// for the userns helper, a missing cgroup parent.
func TestMatrixSpawnFailure(t *testing.T) {
	for _, f := range matrix {
		t.Run(f.String(), func(t *testing.T) {
			dir := t.TempDir()
			s := matrixSpec(t, f, dir, "exit 0\n")
			var want string
			switch {
			case f.pasta:
				s.Sandbox.ExtraArgs = append(s.Sandbox.ExtraArgs, "--acphost-bogus")
				want = "acphost-bogus"
			case f.user:
				s.Command = filepath.Join(dir, "missing")
				want = "acphost userns"
			default:
				s.Cgroup.Parent = filepath.Join(s.Cgroup.Parent, "acphost-missing-parent")
				want = "acphost-missing-parent"
			}
			runSpawnFailure(t, dir, s, want)
		})
	}
}

// A sandbox mount that fails inside bwrap, after the socket is up.
func TestSandboxSpawnFailureMount(t *testing.T) {
	bwrap := needBwrap(t)
	dir := t.TempDir()
	s := sandboxSpec(t, bwrap, dir, "exit 0\n")
	s.Sandbox.ExtraArgs = append(s.Sandbox.ExtraArgs, "--ro-bind", filepath.Join(dir, "gone"), "/gone")
	runSpawnFailure(t, dir, s, "gone")
}

// An agent that runs and exits non-zero is not a spawn failure.
func TestSandboxAgentExitNotSpawnFailure(t *testing.T) {
	bwrap := needBwrap(t)
	dir := t.TempDir()
	s := sandboxSpec(t, bwrap, dir, "echo oops >&2\nexit 5\n")
	require.NoError(t, WriteSpec(dir, s))
	require.NoError(t, Main(dir))
	ex, _ := ReadExit(dir)
	assert.Equal(t, Exit{Reason: "agent exited", ExitCode: 5}, ex)
}

func TestSetupError(t *testing.T) {
	assert.Empty(t, tapResult{last: "x"}.setupError(true, 0))
	assert.Equal(t, "x", tapResult{last: "x"}.setupError(true, 1))
	assert.Equal(t, "sandbox exited 1 before the agent ran", tapResult{}.setupError(true, 1))
	assert.Empty(t, tapResult{ranAgent: true, last: "x"}.setupError(true, 1))
	assert.Empty(t, tapResult{last: "x"}.setupError(false, 1), "unsandboxed agent output")
	assert.Equal(t, "acphost userns: e", tapResult{helperFail: "acphost userns: e", last: "y"}.setupError(false, 127))
}

// An exit.json from an earlier run, older than the current spec, is not
// reported as this run's failure.
func TestSpawnFailureStale(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ExitFile), []byte(`{"reason":"spawn failed","error":"old"}`), 0o600))
	require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol}))
	old := time.Now().Add(-time.Minute)
	require.NoError(t, os.Chtimes(filepath.Join(dir, ExitFile), old, old))
	assert.NoError(t, spawnFailure(dir))
	require.NoError(t, os.Chtimes(filepath.Join(dir, ExitFile), time.Now().Add(time.Minute), time.Now().Add(time.Minute)))
	var se *SpawnError
	require.ErrorAs(t, spawnFailure(dir), &se)
	assert.Equal(t, "spawn failed: old", se.Error())
}
