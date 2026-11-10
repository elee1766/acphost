package acphost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gfx-labs/acphost/pkg/cgroup"
	"github.com/gfx-labs/acphost/pkg/proc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// needSystemd skips unless the systemd manager answers and this process
// can move a process into a scope: a non-root caller must sit in its
// manager's cgroup tree (CI runs these tests under systemd-run --user
// --scope). It returns the slice to use, removed by systemd once empty.
func needSystemd(t *testing.T) string {
	t.Helper()
	if err := cgroup.CheckManager(); err != nil {
		t.Skipf("no systemd manager: %v", err)
	}
	if os.Geteuid() != 0 {
		b, _ := os.ReadFile("/proc/self/cgroup")
		if !strings.Contains(string(b), "/user@") {
			t.Skipf("not in a user manager's cgroup tree: %s", strings.TrimSpace(string(b)))
		}
	}
	return "acphost-test.slice"
}

func systemdSpec(t *testing.T, dir, slice, script string, c cgroup.Cgroup) Spec {
	agent := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\n"+script), 0o700))
	c.Driver, c.Parent = cgroup.DriverSystemd, slice
	return Spec{Protocol: Protocol, Session: "sd-" + filepath.Base(dir), Command: agent, Dir: dir, GCRootDir: dir, LeaseMS: 60_000, Cgroup: &c}
}

// The agent runs in a leaf of a transient scope in the slice, with the
// limits on the scope, and the scope is gone after the exit (#226).
func TestSystemdAgentInScope(t *testing.T) {
	slice := needSystemd(t)
	dir := t.TempDir()
	s := systemdSpec(t, dir, slice,
		"cat /proc/self/cgroup >\"$0.cg\"\nd=/sys/fs/cgroup$(sed -n 's/^0:://p' /proc/self/cgroup)/..\ncat \"$d/pids.max\" \"$d/memory.max\" >\"$0.lim\"\nexit 3\n",
		cgroup.Cgroup{PidsMax: 37, MemoryMax: "268435456"})
	require.NoError(t, WriteSpec(dir, s))
	require.NoError(t, Main(dir))

	ex, ok := ReadExit(dir)
	require.True(t, ok)
	assert.Equal(t, 3, ex.ExitCode, "%+v", ex)
	if assert.NotNil(t, ex.Cgroup) {
		assert.Positive(t, ex.Cgroup.CPUUsageUsec)
	}
	iso, ok, err := ReadIsolation(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, iso.Cgroup)
	assert.Equal(t, cgroup.DriverSystemd, iso.Cgroup.Driver)
	unit, _ := cgroup.ScopeUnit(s.Session, 0)
	assert.True(t, strings.HasPrefix(iso.Cgroup.Unit, strings.TrimSuffix(unit, "0.scope")), iso.Cgroup.Unit)
	assert.Equal(t, slice, filepath.Base(filepath.Dir(iso.Cgroup.Dir)))
	assert.Equal(t, []cgroup.LimitApplied{{File: "memory.max", Value: "268435456"}, {File: "pids.max", Value: "37"}}, iso.Cgroup.Limits)

	cg, err := os.ReadFile(filepath.Join(dir, "agent.cg"))
	require.NoError(t, err)
	assert.Contains(t, string(cg), "/"+slice+"/"+iso.Cgroup.Unit+"/cmd\n")
	lim, err := os.ReadFile(filepath.Join(dir, "agent.lim"))
	require.NoError(t, err)
	assert.Equal(t, "37\n268435456\n", string(lim))
	// systemd collects the scope once the holder is gone, a moment after.
	require.Eventually(t, func() bool {
		_, err := os.Stat(iso.Cgroup.Dir)
		return os.IsNotExist(err)
	}, 5*time.Second, 10*time.Millisecond, "scope %s not collected", iso.Cgroup.Unit)
}

// An agent over MemoryMax is OOM killed and the exit says so, as with
// cgroupfs (#226).
func TestSystemdOOM(t *testing.T) {
	slice := needSystemd(t)
	dir := t.TempDir()
	c := cgroup.Cgroup{MemoryMax: "33554432", SwapMax: "0"}
	s := systemdSpec(t, dir, slice, "x=$(head -c 1048576 /dev/zero | tr '\\0' a)\nwhile :; do x=\"$x$x\"; done\n", c)
	require.NoError(t, WriteSpec(dir, s))
	require.NoError(t, Main(dir))
	ex, _ := ReadExit(dir)
	assert.True(t, ex.OOMKill, "%+v", ex)
	assert.Equal(t, cgroup.ReasonOOM, ex.Reason)
	if assert.NotNil(t, ex.Cgroup) {
		assert.Positive(t, ex.Cgroup.OOMKills)
		assert.Positive(t, ex.Cgroup.MemoryMaxEvents)
	}
	assert.Equal(t, "killed", ex.Signal)
}

// InScope starts the host, and an agent without Spec.Cgroup, in a
// transient scope acphost-host-*.scope in the slice, and systemd
// collects the scope once the host is gone (#240).
func TestSystemdDetachedHostInScope(t *testing.T) {
	slice := needSystemd(t)
	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\ncat /proc/self/cgroup >\"$0.cg\"\nexec sleep 600\n"), 0o700))
	session := "hs-" + filepath.Base(dir)
	require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol, Session: session, Command: agent, Dir: dir, GCRootDir: dir}))
	d, err := StartDetached(dir, nil, InScope(slice))
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Stop() })

	scope := procCgroup(t, fmt.Sprintf("/proc/%d/cgroup", d.Pid))
	assert.Equal(t, slice, filepath.Base(filepath.Dir(scope)), "host")
	unit := filepath.Base(scope)
	prefix, _ := cgroup.HostScopeUnit(session, 0)
	assert.True(t, strings.HasPrefix(unit, strings.TrimSuffix(prefix, "0.scope")), unit)
	assert.True(t, strings.HasPrefix(unit, "acphost-host-") && strings.HasSuffix(unit, ".scope"), unit)
	// The holder has left; the host is in the scope itself, no leaf.
	procs, err := os.ReadFile(filepath.Join(scope, "cgroup.procs"))
	require.NoError(t, err)
	assert.Contains(t, strings.Fields(string(procs)), strconv.Itoa(d.Pid))
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(agent + ".cg")
		return err == nil && len(b) > 0
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, scope, procCgroup(t, agent+".cg"), "agent")

	require.NoError(t, d.Stop())
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err = d.Wait(ctx)
	require.NoError(t, err)
	// Wait returns once the lock is released, a moment before the host
	// exits (1s under -race: GORACE atexit_sleep_ms); then systemd
	// collects the empty scope.
	require.Eventually(t, func() bool {
		_, err := os.Stat(scope)
		return os.IsNotExist(err)
	}, 10*time.Second, 10*time.Millisecond, "scope %s not collected", unit)
}

// InScope and InCgroup together, or InScope with a name that is not a
// slice, fail before anything starts (#240). Needs no manager.
func TestDetachedHostInScopeOptions(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol, Session: "s1", Command: "/bin/true", Dir: dir, GCRootDir: dir}))
	for _, opts := range [][]DetachOption{
		{InScope("acphost.slice"), InCgroup(dir)},
		{InCgroup(dir), InScope("")},
		{InScope("/sys/fs/cgroup/x")},
		{InScope("acphost.scope")},
	} {
		_, err := StartDetached(dir, nil, opts...)
		var pe *PermanentError
		assert.ErrorAs(t, err, &pe)
	}
	assert.NoFileExists(t, filepath.Join(dir, proc.SupervisorPidFile))
	assert.NoFileExists(t, filepath.Join(dir, LogFile))
	assert.False(t, Alive(dir))
}
