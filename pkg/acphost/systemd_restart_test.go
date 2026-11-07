package acphost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/elee1766/acphost/pkg/cgroup"
	"github.com/elee1766/acphost/pkg/proc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unitCaller is the program TestSystemdUnitRestart runs as a service,
// like jobshop's controller: it starts a detached host in a scope
// unless one runs already (a restart runs it again) and then stays
// until the unit stops it.
func unitCaller(dir, slice string) {
	if !Alive(dir) {
		if _, err := StartDetached(dir, nil, InScope(slice)); err != nil {
			fmt.Fprintf(os.Stderr, "unit caller: %v\n", err)
			os.Exit(1)
		}
	}
	// SIGTERM from the unit's stop ends it, the Go default.
	select {}
}

// systemctlUser runs systemctl --user and returns its output.
func systemctlUser(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	require.NoError(t, err, "systemctl --user %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// unitProp reads one property of a user unit.
func unitProp(t *testing.T, unit, prop string) string {
	t.Helper()
	return systemctlUser(t, "show", "-p", prop, "--value", unit)
}

// limitFiles reads the limit files of a session scope.
func limitFiles(t *testing.T, scope string) string {
	t.Helper()
	var b strings.Builder
	for _, f := range []string{"memory.max", "pids.max"} {
		v, err := os.ReadFile(filepath.Join(scope, f))
		require.NoError(t, err)
		fmt.Fprintf(&b, "%s=%s", f, v)
	}
	return b.String()
}

// Restarting the unit that started a detached host (jobshop's
// controller, JOB-97, #226) leaves the host and the agent's session
// scope alive with their limits, and the restart itself succeeds: with
// Delegate= and the cgroupfs driver the unit's cgroup would have to
// enable controllers for session cgroups below it, and the next start
// failed with EBUSY (#241).
func TestSystemdUnitRestart(t *testing.T) {
	slice := needSystemd(t)
	for _, bin := range []string{"systemd-run", "systemctl"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("no %s: %v", bin, err)
		}
	}
	exe, err := os.Executable()
	require.NoError(t, err)

	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\nexec sleep 600\n"), 0o700))
	session := "ur-" + filepath.Base(dir)
	s := Spec{
		Protocol: Protocol, Session: session, Command: agent, Dir: dir, GCRootDir: dir,
		Cgroup: &cgroup.Cgroup{Driver: cgroup.DriverSystemd, Parent: slice, MemoryMax: "268435456", PidsMax: 64},
	}
	require.NoError(t, WriteSpec(dir, s))

	unit := "acphost-test-caller-" + filepath.Base(dir) + ".service"
	// A service like jobshop's: delegated, so the unit may make cgroups
	// below it, as the cgroupfs driver needs.
	out, err := exec.Command("systemd-run", "--user", "--quiet", "--collect", "--unit="+unit,
		"--property=Delegate=yes",
		"--setenv=ACPHOST_TEST_UNIT_CALLER="+dir, "--setenv=ACPHOST_TEST_UNIT_SLICE="+slice,
		exe, "-test.run=^$").CombinedOutput()
	require.NoError(t, err, "systemd-run: %s", out)
	t.Cleanup(func() {
		_ = exec.Command("systemctl", "--user", "stop", unit).Run()
		if d, err := proc.Open(dir); err == nil && d.Running() {
			_ = d.Stop()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = d.Wait(ctx)
		}
	})

	// The host answers once the caller started it.
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var hostPid int
	require.Eventually(t, func() bool {
		nc, _, w, err := Connect(ctx, dir)
		if err != nil {
			return false
		}
		_ = nc.Close()
		hostPid = w.Pid
		return true
	}, 20*time.Second, 50*time.Millisecond, "host did not answer: %s", systemctlOutput(unit))
	iso, ok, err := ReadIsolation(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, iso.Cgroup)
	sessionScope := iso.Cgroup.Dir
	hostScope := procCgroup(t, fmt.Sprintf("/proc/%d/cgroup", hostPid))
	assert.Equal(t, slice, filepath.Base(filepath.Dir(hostScope)), "host scope")
	assert.Equal(t, slice, filepath.Base(filepath.Dir(sessionScope)), "session scope")
	limits := limitFiles(t, sessionScope)
	assert.Equal(t, "memory.max=268435456\npids.max=64\n", limits)
	// Neither scope is in the unit.
	unitCg := unitProp(t, unit, "ControlGroup")
	assert.NotContains(t, hostScope, unitCg)
	assert.NotContains(t, sessionScope, unitCg)

	pidBefore := unitProp(t, unit, "MainPID")
	require.NotEqual(t, "0", pidBefore)
	systemctlUser(t, "restart", unit)
	assert.Equal(t, "active", unitProp(t, unit, "ActiveState"), "after restart")
	assert.Equal(t, "success", unitProp(t, unit, "Result"))
	assert.NotEqual(t, pidBefore, unitProp(t, unit, "MainPID"), "the restart started a new process")

	// The scopes, their limits and the host survived.
	assert.DirExists(t, hostScope)
	assert.DirExists(t, sessionScope)
	assert.Equal(t, limits, limitFiles(t, sessionScope))
	assert.True(t, Alive(dir))
	require.NoError(t, syscall.Kill(hostPid, 0), "host %d gone", hostPid)
	nc, _, w, err := Connect(ctx, dir)
	require.NoError(t, err)
	_ = nc.Close()
	assert.Equal(t, hostPid, w.Pid)
	assert.Equal(t, hostScope, procCgroup(t, fmt.Sprintf("/proc/%d/cgroup", hostPid)))

	// Stopping the host collects both scopes.
	d, err := proc.Open(dir)
	require.NoError(t, err)
	require.NoError(t, d.Stop())
	wctx, wcancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer wcancel()
	_, err = d.Wait(wctx)
	require.NoError(t, err)
	for _, p := range []string{hostScope, sessionScope} {
		require.Eventually(t, func() bool {
			_, err := os.Stat(p)
			return os.IsNotExist(err)
		}, 10*time.Second, 10*time.Millisecond, "scope %s not collected", p)
	}
}

// systemctlOutput is the unit's status, for a failure message.
func systemctlOutput(unit string) string {
	out, _ := exec.Command("systemctl", "--user", "status", "--no-pager", "-n", "20", unit).CombinedOutput()
	return string(out)
}
