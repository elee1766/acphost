package proc_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gfx-labs/acphost/pkg/cgroup"
	"github.com/gfx-labs/acphost/pkg/proc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A detached command that the cgroup OOM-kills: exit.json, read back
// through Open, carries OOMKill and the cgroup's final stats, the same
// as attached Wait (#201). Named TestCgroup* so the CI cgroup step runs
// it with ACPHOST_TEST_CGROUP.
func TestCgroupDetachedOOM(t *testing.T) {
	parent := needCgroup(t)
	sub, _ := os.ReadFile(filepath.Join(parent, "cgroup.subtree_control"))
	if !strings.Contains(string(sub), "memory") {
		if err := os.WriteFile(filepath.Join(parent, "cgroup.subtree_control"), []byte("+memory"), 0); err != nil {
			t.Skipf("enable memory: %v", err)
		}
	}
	// With swap the command pages out instead of being OOM-killed (#41).
	c := cgroup.Cgroup{Parent: parent, MemoryMax: "33554432", PidsMax: 64}
	if _, err := os.Stat(filepath.Join(parent, "memory.swap.max")); err == nil {
		c.SwapMax = "0"
	}
	dir := t.TempDir()
	d, err := proc.StartDetached(dir, proc.Spec{
		Command: "sh",
		Args:    []string{"-c", "x=$(head -c 1048576 /dev/zero | tr '\\0' a)\nwhile :; do x=\"$x$x\"; done"},
		Dir:     dir,
		Cgroup:  &c,
		Name:    "oom-" + filepath.Base(dir),
	}, time.Second)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	o, err := proc.Open(dir)
	require.NoError(t, err)
	ex, err := o.Wait(ctx)
	require.NoError(t, err)
	assert.True(t, ex.OOMKill, "%+v", ex)
	assert.Equal(t, "killed", ex.Signal)
	if assert.NotNil(t, ex.Cgroup, "%+v", ex) {
		assert.Positive(t, ex.Cgroup.OOMKills)
		assert.Positive(t, ex.Cgroup.MemoryMaxEvents)
	}
	// Exit() reads the same file without blocking.
	ex2, ok := d.Exit()
	assert.True(t, ok)
	assert.Equal(t, ex, ex2)
	assert.NoDirExists(t, filepath.Join(parent, "oom-"+filepath.Base(dir)))
}
