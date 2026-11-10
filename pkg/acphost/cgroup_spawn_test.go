package acphost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gfx-labs/acphost/pkg/cgroup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckSpawnCgroupController(t *testing.T) {
	parent := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(parent, "cgroup.subtree_control"), []byte("cpu\n"), 0o644))
	err := CheckSpawn(Spec{Dir: t.TempDir(), Command: "x", Cgroup: &cgroup.Cgroup{Parent: parent, PidsMax: 1}}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"pids"`)
}

func TestNilCgroupTouchesNothing(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "agent")
	require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o700))
	s := Spec{Dir: t.TempDir(), Command: exe}
	assert.NoError(t, CheckSpawn(s, false))
	assert.Nil(t, s.Cgroup)
}
