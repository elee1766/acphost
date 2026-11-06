package acphost

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elee1766/acphost/pkg/cgroup"
	"github.com/elee1766/acphost/pkg/proc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A host started detached survives its caller, and once the agent exits
// the dir has exit.json with the exit code, read through proc.Open, and
// host.pid, host.lock and supervisor.pid for both kinds of reader (#210).
func TestDetachedHostSurvivesCaller(t *testing.T) {
	dir := t.TempDir()
	// The agent exits 3 after one line on stdin.
	agent := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\nread x\nexit 3\n"), 0o700))
	require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol, Session: "s1", Command: agent, Dir: dir, GCRootDir: dir, LeaseMS: 60_000}))

	exe, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Env = append(os.Environ(), "ACPHOST_TEST_DETACH_CHILD="+dir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "caller: %s", out)
	hostPid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	require.NoError(t, err, "%s", out)

	// The caller has exited; the host still runs and answers.
	d, err := proc.Open(dir)
	require.NoError(t, err)
	assert.Equal(t, hostPid, d.Pid)
	require.True(t, d.Running())
	require.True(t, Alive(dir))
	b, err := os.ReadFile(filepath.Join(dir, PidFile))
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(hostPid), string(b), "host.pid for older clients")
	nc, _, w, err := Connect(t.Context(), dir)
	require.NoError(t, err)
	assert.Equal(t, hostPid, w.Pid)
	assert.Positive(t, w.AgentPid)
	_, err = nc.Write([]byte(`{"jsonrpc":"2.0","method":"x"}` + "\n"))
	require.NoError(t, err)
	_ = nc.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ex, err := d.Wait(ctx)
	require.NoError(t, err)
	assert.Equal(t, proc.Exit{ExitCode: 3}, ex)
	hex, ok := ReadExit(dir)
	require.True(t, ok)
	assert.Equal(t, Exit{Reason: "agent exited", ExitCode: 3}, hex)
	assert.False(t, Alive(dir))
	_, ok, err = ReadIsolation(dir)
	require.NoError(t, err)
	assert.True(t, ok)
}

// detachChild is the caller in TestDetachedHostSurvivesCaller: it starts
// the host detached, prints its pid and exits.
func detachChild(dir string) {
	d, err := StartDetached(dir, nil)
	if err != nil {
		_, _ = os.Stdout.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	_, _ = os.Stdout.WriteString(strconv.Itoa(d.Pid) + "\n")
	os.Exit(0)
}

func TestDetachedHostStop(t *testing.T) {
	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\nexec sleep 600\n"), 0o700))
	require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol, Session: "s1", Command: agent, Dir: dir, GCRootDir: dir}))
	d, err := StartDetached(dir, nil)
	require.NoError(t, err)
	_, err = StartDetached(dir, nil)
	assert.ErrorContains(t, err, "another host owns")
	require.NoError(t, d.Stop())
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ex, err := d.Wait(ctx)
	require.NoError(t, err)
	assert.Equal(t, "terminated", ex.Signal)
	hex, _ := ReadExit(dir)
	assert.Equal(t, "signal terminated", hex.Reason)
}

// A spawn failure is StartDetached's error, as a *SpawnError.
func TestDetachedHostSpawnFailure(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol, Session: "s1", Command: missing, Dir: dir, GCRootDir: dir}))
	_, err := StartDetached(dir, nil)
	var se *SpawnError
	require.ErrorAs(t, err, &se)
	assert.Contains(t, se.Error(), missing)
	assert.Equal(t, ReasonSpawnFailed, se.Exit.Reason)
	assert.False(t, Alive(dir))
	d, err := proc.Open(dir)
	require.NoError(t, err)
	ex, err := d.Wait(t.Context())
	require.NoError(t, err)
	assert.Equal(t, -1, ex.ExitCode)
	assert.Contains(t, ex.SetupError, missing)
}

func TestDetachedHostNoSpec(t *testing.T) {
	_, err := StartDetached(t.TempDir(), nil)
	assert.True(t, errors.Is(err, os.ErrNotExist), "%v", err)
}

func TestDetachedHostNoMaybeChild(t *testing.T) {
	old := maybeChildCalled.Swap(false)
	t.Cleanup(func() { maybeChildCalled.Store(old) })
	_, err := StartDetached(t.TempDir(), nil)
	assert.ErrorIs(t, err, ErrNoMaybeChild)
}

// A detached host's exit.json, read through proc.Open, has the agent's
// cgroup stats (#210). Named TestCgroup* so the CI cgroup step runs it.
func TestCgroupDetachedHostExitStats(t *testing.T) {
	parent := needCgroup(t)
	dir := t.TempDir()
	s := cgroupSpec(t, dir, parent, "exit 3\n", cgroup.Cgroup{PidsMax: 64})
	s.Session = "detached-" + filepath.Base(dir)
	require.NoError(t, WriteSpec(dir, s))
	d, err := StartDetached(dir, nil)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ex, err := d.Wait(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, ex.ExitCode)
	if assert.NotNil(t, ex.Cgroup, "%+v", ex) {
		assert.Positive(t, ex.Cgroup.CPUUsageUsec)
	}
	hex, _ := ReadExit(dir)
	assert.Equal(t, "agent exited", hex.Reason)
	assert.NoDirExists(t, filepath.Join(parent, s.Session))
}
