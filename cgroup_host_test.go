package acphost

import (
	"context"
	"fmt"
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
	assert.Equal(t, int64(1), cgroupOOMKills(dir))
	assert.False(t, cgroupPopulated(dir))
	assert.True(t, waitCgroupEmpty(dir, time.Second))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cgroup.events"), []byte("populated 1\nfrozen 0\n"), 0o644))
	assert.True(t, cgroupPopulated(dir))
	assert.False(t, waitCgroupEmpty(dir, 30*time.Millisecond))
	assert.Equal(t, int64(0), cgroupOOMKills(t.TempDir()), "missing file")
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
