package acphost

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPermanent(t *testing.T) {
	dir := t.TempDir()
	_, statErr := os.Stat(filepath.Join(dir, "gone"))
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"not exist":   {fmt.Errorf("wrap: %w", statErr), true},
		"not found":   {fmt.Errorf("x: %w", exec.ErrNotFound), true},
		"marked":      {Permanentf("bad"), true},
		"transient":   {errors.New("git fetch: timeout"), false},
		"nil-wrapped": {fmt.Errorf("connect: %w", os.ErrDeadlineExceeded), false},
	} {
		assert.Equal(t, tc.want, Permanent(tc.err), name)
	}
}

func TestCheckSpawn(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "agent")
	assert.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o700))
	assert.NoError(t, CheckSpawn(Spec{Dir: dir, Command: exe}, false))
	assert.NoError(t, CheckSpawn(Spec{Dir: dir, Command: "agent", Env: []string{"PATH=" + dir}}, false), "PATH from the spec env")
	assert.NoError(t, CheckSpawn(Spec{Dir: filepath.Join(dir, "later"), Command: exe}, true), "worktree made by the caller")

	err := CheckSpawn(Spec{Dir: filepath.Join(dir, "gone"), Command: exe}, false)
	assert.True(t, Permanent(err), "missing dir: %v", err)
	err = CheckSpawn(Spec{Dir: dir, Command: "agent", Env: []string{"PATH=/nonexistent"}}, false)
	assert.True(t, Permanent(err), "agent not on PATH: %v", err)
	err = CheckSpawn(Spec{Dir: dir, Command: filepath.Join(dir, "gone")}, false)
	assert.True(t, Permanent(err))
}

func TestCheckSpawnSandbox(t *testing.T) {
	dir := t.TempDir()
	bw := filepath.Join(t.TempDir(), "bwrap-stub")
	require.NoError(t, os.WriteFile(bw, []byte("#!/bin/sh\n"), 0o700))
	sb := Sandbox{Bwrap: bw, RW: []Mount{{Src: dir}}}
	assert.NoError(t, CheckSpawn(Spec{Dir: dir, Command: "agent", Sandbox: &sb}, false))

	err := CheckSpawn(Spec{Dir: "/elsewhere", Command: "agent", Sandbox: &sb}, false)
	assert.True(t, Permanent(err), "dir outside rw: %v", err)
	bad := Sandbox{Bwrap: bw, RW: []Mount{{Src: filepath.Join(dir, "gone")}}}
	err = CheckSpawn(Spec{Dir: filepath.Join(dir, "gone"), Command: "agent", Sandbox: &bad}, false)
	assert.True(t, Permanent(err), "missing rw: %v", err)
	t.Setenv("PATH", dir)
	noBwrap := Sandbox{RW: []Mount{{Src: dir}}}
	err = CheckSpawn(Spec{Dir: dir, Command: "agent", Sandbox: &noBwrap}, false)
	assert.True(t, Permanent(err), "bwrap not on PATH: %v", err)
	gone := Sandbox{Bwrap: filepath.Join(dir, "bwrap"), RW: []Mount{{Src: dir}}}
	err = CheckSpawn(Spec{Dir: dir, Command: "agent", Sandbox: &gone}, false)
	assert.True(t, Permanent(err), "missing bwrap path: %v", err)
}
