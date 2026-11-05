package acphost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCgroup makes a temp dir standing in for a delegated cgroup with
// the given controllers enabled for children.
func fakeCgroup(t *testing.T, controllers string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cgroup.subtree_control"), []byte(controllers+"\n"), 0o644))
	return dir
}

func TestPrepareCgroupWritesLimits(t *testing.T) {
	parent := fakeCgroup(t, "cpu memory pids")
	c := Cgroup{Parent: parent, CPUMax: "50000 100000", MemoryMax: "1073741824", MemoryHigh: "max", SwapMax: "0", PidsMax: 512}
	require.NoError(t, checkCgroup(c))
	dir, err := prepareCgroup(c, "s1")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(parent, "s1"), dir)
	for file, want := range map[string]string{"cpu.max": "50000 100000", "memory.max": "1073741824", "memory.high": "max", "memory.swap.max": "0", "pids.max": "512"} {
		b, err := os.ReadFile(filepath.Join(dir, file))
		require.NoError(t, err, file)
		assert.Equal(t, want, string(b), file)
	}
	// Real cgroupfs rmdir ignores interface files, a temp dir does not.
	for _, file := range []string{"cpu.max", "memory.max", "memory.high", "memory.swap.max", "pids.max"} {
		require.NoError(t, os.Remove(filepath.Join(dir, file)))
	}
	require.NoError(t, removeCgroup(dir))
	assert.NoDirExists(t, dir)
	assert.NoError(t, removeCgroup(dir), "already gone")
}

func TestPrepareCgroupOnlySetFields(t *testing.T) {
	parent := fakeCgroup(t, "pids")
	dir, err := prepareCgroup(Cgroup{Parent: parent, PidsMax: 10}, "s")
	require.NoError(t, err)
	ents, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, ents, 1)
	assert.Equal(t, "pids.max", ents[0].Name())
}

func TestCheckCgroupErrors(t *testing.T) {
	parent := fakeCgroup(t, "cpu")
	cases := map[string]Cgroup{
		"relative":       {Parent: "rel/path"},
		"missing parent": {Parent: filepath.Join(parent, "gone")},
		"bad cpu":        {Parent: parent, CPUMax: "50000"},
		"zero period":    {Parent: parent, CPUMax: "max 0"},
		"bad memory":     {Parent: parent, MemoryMax: "1G"},
		"bad swap":       {Parent: parent, SwapMax: "-1"},
		"negative pids":  {Parent: parent, PidsMax: -1},
	}
	for name, c := range cases {
		err := checkCgroup(c)
		assert.Error(t, err, name)
		assert.True(t, Permanent(err), name)
	}
	assert.NoError(t, checkCgroup(Cgroup{Parent: parent, CPUMax: "max 100000"}))

	err := checkCgroup(Cgroup{Parent: parent, MemoryMax: "max"})
	require.Error(t, err)
	assert.True(t, Permanent(err))
	assert.Contains(t, err.Error(), `"memory"`)

	err = checkCgroup(Cgroup{Parent: parent, SwapMax: "0"})
	require.Error(t, err, "swap_max needs the memory controller")
	assert.Contains(t, err.Error(), `"memory"`)

	err = CheckSpawn(Spec{Dir: t.TempDir(), Command: "x", Cgroup: &Cgroup{Parent: parent, PidsMax: 1}}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"pids"`)
}

func TestPrepareCgroupRejectsEscape(t *testing.T) {
	parent := fakeCgroup(t, "pids")
	for _, s := range []string{"", ".", "..", "../x", "a/b", "/abs"} {
		_, err := prepareCgroup(Cgroup{Parent: parent, PidsMax: 1}, s)
		assert.Error(t, err, s)
	}
	_, err := os.Stat(filepath.Join(filepath.Dir(parent), "x"))
	assert.True(t, os.IsNotExist(err))
}

func TestNilCgroupTouchesNothing(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "agent")
	require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o700))
	s := Spec{Dir: t.TempDir(), Command: exe}
	assert.NoError(t, CheckSpawn(s, false))
	assert.Nil(t, s.Cgroup)
}

func TestDelegatedCgroup(t *testing.T) {
	root := t.TempDir()
	cg := filepath.Join(root, "user.slice", "app.scope")
	require.NoError(t, os.MkdirAll(cg, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cg, "cgroup.controllers"), []byte("cpu memory pids\n"), 0o644))
	proc := filepath.Join(t.TempDir(), "cgroup")
	require.NoError(t, os.WriteFile(proc, []byte("0::/user.slice/app.scope\n"), 0o644))

	info, err := delegatedCgroup(proc, root)
	require.NoError(t, err)
	assert.Equal(t, cg, info.Path)
	assert.True(t, info.Writable)
	assert.Equal(t, []string{"cpu", "memory", "pids"}, info.Controllers)

	require.NoError(t, os.WriteFile(proc, []byte("1:name=systemd:/x\n"), 0o644))
	_, err = delegatedCgroup(proc, root)
	assert.Error(t, err, "no v2 line")
}

func fakeDelegation(t *testing.T, controllers string) string {
	t.Helper()
	own := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(own, "cgroup.controllers"), []byte(controllers+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(own, "cgroup.subtree_control"), nil, 0o644))
	return own
}

func readString(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(file)
	require.NoError(t, err)
	return string(b)
}

func TestSetupDelegation(t *testing.T) {
	own := fakeDelegation(t, "cpu memory pids")
	got, err := setupDelegation(own, 4242, []string{"memory", "pids"})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(own, "sessions"), got)
	assert.Equal(t, "4242", readString(t, filepath.Join(own, "controller", "cgroup.procs")))
	assert.Equal(t, "+memory +pids", readString(t, filepath.Join(own, "cgroup.subtree_control")))
	assert.Equal(t, "+memory +pids", readString(t, filepath.Join(got, "cgroup.subtree_control")))

	// A real cgroupfs reads back names without "+". Mimic that, then
	// make every file read-only: a repeat call must not write anything.
	for _, f := range []string{filepath.Join(own, "cgroup.subtree_control"), filepath.Join(got, "cgroup.subtree_control")} {
		require.NoError(t, os.WriteFile(f, []byte("memory pids\n"), 0o444))
	}
	require.NoError(t, os.Chmod(filepath.Join(own, "controller", "cgroup.procs"), 0o444))
	again, err := setupDelegation(own, 4242, []string{"memory", "pids"})
	require.NoError(t, err)
	assert.Equal(t, got, again)
}

func TestSetupDelegationMissingController(t *testing.T) {
	own := fakeDelegation(t, "cpu pids")
	_, err := setupDelegation(own, 4242, []string{"memory", "pids"})
	var pe *PermanentError
	require.ErrorAs(t, err, &pe)
	assert.Contains(t, err.Error(), filepath.Join(own, "cgroup.controllers"))
	assert.NoDirExists(t, filepath.Join(own, "sessions"))
}

func TestSetupDelegationReadOnly(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores file modes")
	}
	own := fakeDelegation(t, "memory pids")
	file := filepath.Join(own, "cgroup.subtree_control")
	require.NoError(t, os.Chmod(file, 0o444))
	_, err := setupDelegation(own, 4242, []string{"memory"})
	var pe *PermanentError
	require.ErrorAs(t, err, &pe)
	assert.Contains(t, err.Error(), file)
}
