package acphost

import (
	"context"
	"errors"
	"fmt"
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
// the dir has exit.json with the exit code, read through proc.Open.
// supervisor.pid and supervisor.lock are its only pid and lock files
// (#222).
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
	// No retry: Wait returns once supervisor.lock is released, and that
	// lock is the only one Alive reads (#222), so Alive is false at once.
	// A second lock released after it made this flaky before (#216, #262).
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

// InCgroup starts the host, and the agent it forks without Spec.Cgroup,
// in the given cgroup instead of the caller's (#223). Named TestCgroup*
// so the CI cgroup step runs it.
func TestCgroupDetachedHostInCgroup(t *testing.T) {
	parent := needCgroup(t)
	hosts := filepath.Join(parent, "hosts-"+filepath.Base(t.TempDir()))
	require.NoError(t, os.Mkdir(hosts, 0o755))
	t.Cleanup(func() {
		// Wait returns once the lock is released, a moment before the
		// host process exits (1s under -race: GORACE atexit_sleep_ms).
		cgroup.WaitEmpty(hosts, 5*time.Second)
		_ = os.Remove(hosts)
	})

	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\ncat /proc/self/cgroup >\"$0.cg\"\nexec sleep 600\n"), 0o700))
	require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol, Session: "s1", Command: agent, Dir: dir, GCRootDir: dir}))
	d, err := StartDetached(dir, nil, InCgroup(hosts))
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Stop() })

	assert.Equal(t, hosts, procCgroup(t, fmt.Sprintf("/proc/%d/cgroup", d.Pid)), "host")
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(agent + ".cg")
		return err == nil && len(b) > 0
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, hosts, procCgroup(t, agent+".cg"), "agent")

	require.NoError(t, d.Stop())
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err = d.Wait(ctx)
	require.NoError(t, err)
}

// procCgroup reads a /proc/<pid>/cgroup file and returns the v2 cgroup
// as a path under /sys/fs/cgroup.
func procCgroup(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(file)
	require.NoError(t, err)
	for _, l := range strings.Split(string(b), "\n") {
		if p, ok := strings.CutPrefix(l, "0::"); ok {
			return filepath.Join("/sys/fs/cgroup", p)
		}
	}
	t.Fatalf("%s: no cgroup v2 line: %q", file, b)
	return ""
}

// A cgroup dir that cannot be opened fails StartDetached before the host
// starts: no supervisor.pid, no exit.json.
func TestDetachedHostInCgroupMissing(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol, Session: "s1", Command: "/bin/true", Dir: dir, GCRootDir: dir}))
	missing := filepath.Join(dir, "no-such-cgroup")
	_, err := StartDetached(dir, nil, InCgroup(missing))
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.ErrorContains(t, err, missing)
	assert.NoFileExists(t, filepath.Join(dir, proc.SupervisorPidFile))
	assert.NoFileExists(t, filepath.Join(dir, ExitFile))
	assert.False(t, Alive(dir))
}
