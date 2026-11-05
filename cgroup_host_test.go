package acphost

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventsValue(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "memory.events"), []byte("low 0\nhigh 2\nmax 5\noom 1\noom_kill 1\noom_group_kill 0\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cgroup.events"), []byte("populated 0\nfrozen 0\n"), 0o644))
	assert.Equal(t, int64(1), readCgroupStats(dir).OOMKills)
	assert.False(t, cgroupPopulated(dir))
	assert.True(t, waitCgroupEmpty(dir, time.Second))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cgroup.events"), []byte("populated 1\nfrozen 0\n"), 0o644))
	assert.True(t, cgroupPopulated(dir))
	assert.False(t, waitCgroupEmpty(dir, 30*time.Millisecond))
	assert.Equal(t, CgroupStats{}, readCgroupStats(t.TempDir()), "missing files")
}

func TestReadCgroupStats(t *testing.T) {
	dir := t.TempDir()
	write := func(name, s string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644))
	}
	write("memory.current", "1048576\n")
	write("cpu.stat", "usage_usec 12345\nuser_usec 10000\nsystem_usec 2345\n")
	write("memory.events", "low 0\nhigh 0\nmax 7\noom 2\noom_kill 2\n")
	// No memory.peak: kernels before 5.19 lack it.
	assert.Equal(t, CgroupStats{MemoryCurrent: 1048576, CPUUsageUsec: 12345, OOMKills: 2, MemoryMaxEvents: 7}, readCgroupStats(dir))
	write("memory.peak", "2097152\n")
	assert.Equal(t, int64(2097152), readCgroupStats(dir).MemoryPeak)
}

func TestCgroupStatsNotifyAndExit(t *testing.T) {
	parent := needCgroup(t)
	dir := t.TempDir()
	s := cgroupSpec(t, dir, parent, "exec cat >/dev/null\n", Cgroup{PidsMax: 64})
	require.NoError(t, WriteSpec(dir, s))
	done := make(chan error, 1)
	go func() { done <- Main(dir, func(o *options) { o.statsEvery = 50 * time.Millisecond }) }()

	var nc net.Conn
	require.Eventually(t, func() bool {
		var err error
		nc, err = net.Dial("unix", filepath.Join(dir, ACPSocket))
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	defer func() { _ = nc.Close() }()
	hello, _ := json.Marshal(Hello{Protocol: Protocol})
	_, err := nc.Write(append(hello, '\n'))
	require.NoError(t, err)
	rd := bufio.NewReader(nc)
	_ = nc.SetReadDeadline(time.Now().Add(5 * time.Second))
	var got CgroupStats
	for {
		line, err := rd.ReadBytes('\n')
		require.NoError(t, err)
		var m struct {
			Method string      `json:"method"`
			Params CgroupStats `json:"params"`
		}
		if json.Unmarshal(line, &m) == nil && m.Method == MethodCgroupStats {
			got = m.Params
			break
		}
	}
	assert.Positive(t, got.MemoryCurrent, "%+v", got)

	_, err = nc.Write([]byte(`{"jsonrpc":"2.0","method":"` + MethodStop + `"}` + "\n"))
	require.NoError(t, err)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("host did not stop")
	}
	ex, _ := ReadExit(dir)
	require.NotNil(t, ex.Cgroup, "%+v", ex)
	assert.Zero(t, ex.Cgroup.OOMKills)
	assert.NoDirExists(t, filepath.Join(parent, "s1"))
}

// needCgroup skips unless this process has a writable delegated cgroup
// that can give a child memory and pids. It returns a fresh parent
// cgroup (with those controllers enabled for its children) removed at
// the end of the test.
func needCgroup(t *testing.T) string {
	t.Helper()
	// A cgroup that holds processes cannot enable controllers for its
	// children, so a prepared empty parent can be passed in instead.
	if p := os.Getenv("ACPHOST_TEST_CGROUP"); p != "" {
		return p
	}
	info, err := DelegatedCgroup()
	if err != nil || !info.Writable {
		t.Skipf("no writable delegated cgroup: %v", err)
	}
	for _, c := range []string{"memory", "pids"} {
		if !strings.Contains(" "+strings.Join(info.Controllers, " ")+" ", " "+c+" ") {
			t.Skipf("delegated cgroup lacks %s", c)
		}
	}
	// The delegated cgroup holds processes, so it can only enable
	// controllers for children if it already does.
	sub, _ := os.ReadFile(filepath.Join(info.Path, "cgroup.subtree_control"))
	if !strings.Contains(string(sub), "memory") || !strings.Contains(string(sub), "pids") {
		t.Skipf("%s/cgroup.subtree_control lacks memory or pids", info.Path)
	}
	parent := filepath.Join(info.Path, fmt.Sprintf("acphost-test-%d", time.Now().UnixNano()))
	require.NoError(t, os.Mkdir(parent, 0o755))
	t.Cleanup(func() { _ = os.Remove(parent) })
	if err := os.WriteFile(filepath.Join(parent, "cgroup.subtree_control"), []byte("+memory +pids"), 0); err != nil {
		t.Skipf("enable controllers: %v", err)
	}
	return parent
}

func cgroupSpec(t *testing.T, dir, parent, script string, c Cgroup) Spec {
	agent := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\n"+script), 0o700))
	c.Parent = parent
	return Spec{Protocol: Protocol, Session: "s1", Command: agent, Dir: dir, GCRootDir: dir, LeaseMS: 60_000, Cgroup: &c}
}

func TestCgroupExitCodeAndRemove(t *testing.T) {
	parent := needCgroup(t)
	dir := t.TempDir()
	s := cgroupSpec(t, dir, parent, "grep -q acphost /proc/self/cgroup || exit 9\nexit 3\n", Cgroup{PidsMax: 64})
	require.NoError(t, CheckSpawn(s, false))
	require.NoError(t, WriteSpec(dir, s))
	require.NoError(t, Main(dir))
	ex, _ := ReadExit(dir)
	require.NotNil(t, ex.Cgroup)
	ex.Cgroup = nil
	assert.Equal(t, Exit{Reason: "agent exited", ExitCode: 3}, ex, "agent ran inside the cgroup")
	assert.NoDirExists(t, filepath.Join(parent, "s1"))
}

func TestCgroupOOM(t *testing.T) {
	parent := needCgroup(t)
	// With swap the agent pages out at memory.max instead of being OOM
	// killed (seen on GitHub runners, #41), so cap swap with SwapMax. Skip
	// the cap where the kernel has no swap accounting (no memory.swap.max).
	c := Cgroup{MemoryMax: "33554432"}
	if _, err := os.Stat(filepath.Join(parent, "memory.swap.max")); err == nil {
		c.SwapMax = "0"
	}
	dir := t.TempDir()
	// Grow a shell variable well past 32 MiB.
	s := cgroupSpec(t, dir, parent, "x=$(head -c 1048576 /dev/zero | tr '\\0' a)\nwhile :; do x=\"$x$x\"; done\n", c)
	require.NoError(t, WriteSpec(dir, s))
	require.NoError(t, Main(dir))
	ex, _ := ReadExit(dir)
	assert.True(t, ex.OOMKill, "%+v", ex)
	if assert.NotNil(t, ex.Cgroup) {
		assert.Positive(t, ex.Cgroup.OOMKills)
		assert.Positive(t, ex.Cgroup.MemoryMaxEvents)
	}
	assert.Equal(t, "killed", ex.Signal)
	assert.NoDirExists(t, filepath.Join(parent, "s1"))
}

func TestCgroupKillEscapedGrandchild(t *testing.T) {
	parent := needCgroup(t)
	dir := t.TempDir()
	token := "acphost-cg-" + filepath.Base(dir)
	s := cgroupSpec(t, dir, parent,
		"setsid sh -c 'sleep 600; :' "+token+" </dev/null >/dev/null 2>&1 &\nexec cat >/dev/null\n", Cgroup{PidsMax: 64})
	require.NoError(t, WriteSpec(dir, s))
	done := make(chan error, 1)
	go func() { done <- Main(dir) }()
	require.Eventually(t, func() bool { return procWith(token) }, 5*time.Second, 20*time.Millisecond)
	c, _, _, err := Connect(context.Background(), dir)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	_, err = c.Write([]byte(`{"jsonrpc":"2.0","method":"` + MethodStop + `"}` + "\n"))
	require.NoError(t, err)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("host did not stop")
	}
	assert.False(t, procWith(token), "setsid grandchild killed")
	ex, _ := ReadExit(dir)
	assert.Equal(t, "controller requested stop", ex.Reason)
	assert.NoDirExists(t, filepath.Join(parent, "s1"))
}

// TestCgroupSetupDelegation runs SetupDelegation on a fresh empty cgroup
// under the test parent, moving this test process into its controller
// leaf, then runs a session under the returned parent. The process is
// moved back afterwards.
func TestCgroupSetupDelegation(t *testing.T) {
	parent := needCgroup(t)
	orig, err := DelegatedCgroup()
	require.NoError(t, err)
	own := filepath.Join(parent, fmt.Sprintf("deleg-%d", time.Now().UnixNano()))
	require.NoError(t, os.Mkdir(own, 0o755))
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(orig.Path, "cgroup.procs"), []byte(fmt.Sprint(os.Getpid())), 0)
		for _, d := range []string{"sessions", "controller", ""} {
			_ = os.Remove(filepath.Join(own, d))
		}
	})
	sessions, err := SetupDelegation(own, []string{"memory", "pids"})
	if err != nil && strings.Contains(err.Error(), "cgroup.procs") {
		t.Skipf("cannot move into %s: %v", own, err)
	}
	require.NoError(t, err)
	again, err := SetupDelegation(own, []string{"memory", "pids"})
	require.NoError(t, err, "repeat call")
	assert.Equal(t, sessions, again)

	dir := t.TempDir()
	s := cgroupSpec(t, dir, sessions, "grep -q sessions/s1 /proc/self/cgroup || exit 9\nexit 3\n", Cgroup{PidsMax: 64, MemoryMax: "268435456"})
	require.NoError(t, CheckSpawn(s, false))
	require.NoError(t, WriteSpec(dir, s))
	require.NoError(t, Main(dir))
	ex, _ := ReadExit(dir)
	assert.Equal(t, Exit{Reason: "agent exited", ExitCode: 3}, ex)
	assert.NoDirExists(t, filepath.Join(sessions, "s1"))
}
