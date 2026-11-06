package acphost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elee1766/acphost/internal/hostenv"
	"github.com/elee1766/acphost/userns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runAgent runs one host session whose agent is script and waits for it
// to exit.
func runAgent(t *testing.T, shared, name string, u User, script string) {
	t.Helper()
	sess := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.Mkdir(sess, 0o700))
	agent := filepath.Join(shared, name+".sh")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\n"+script), 0o755))
	s := Spec{Protocol: Protocol, Session: name, Command: agent, Dir: shared, GCRootDir: sess, LeaseMS: 60_000, User: &u}
	require.NoError(t, WriteSpec(sess, s))
	require.NoError(t, Main(sess))
	ex, ok := ReadExit(sess)
	require.True(t, ok)
	require.Equal(t, 0, ex.ExitCode, "agent %s: %+v", name, ex)
}

// TestIsolationTwoSubIDSessions checks that two sessions on separate
// AllocSubIDs blocks cannot read each other's 0700 worktree (#67).
func TestIsolationTwoSubIDSessions(t *testing.T) {
	needUserns(t)
	self := os.Geteuid()
	subuid, err := userns.ReadSubIDs(hostenv.SubUIDPath, self)
	require.NoError(t, err)
	subgid, err := userns.ReadSubIDs(hostenv.SubGIDPath, self)
	require.NoError(t, err)
	lockDir := t.TempDir()
	var users [2]User
	for i := range users {
		a, err := AllocSubIDs(lockDir, subuid, subgid, 1)
		if err != nil {
			t.Skipf("need two subid blocks for uid %d: %v", self, err)
		}
		t.Cleanup(func() { _ = a.Release() })
		users[i] = a.User(1000, 1000)
		rep, err := Preflight(Spec{User: &users[i]})
		require.NoError(t, err)
		if !rep.OK() {
			t.Skipf("preflight: %+v", rep.Checks)
		}
	}

	// The agents run as subordinate ids, so they need a path to a shared
	// world-writable dir. Each makes its own worktree there, which leaves
	// it owned by its block's uid without the test needing chown.
	root := t.TempDir()
	for d := root; d != "/" && d != os.TempDir(); d = filepath.Dir(d) {
		if st, err := os.Stat(d); err == nil && st.Mode().Perm()&0o011 != 0o011 {
			require.NoError(t, os.Chmod(d, st.Mode().Perm()|0o011))
		}
	}
	shared := filepath.Join(root, "shared")
	require.NoError(t, os.Mkdir(shared, 0o777))
	require.NoError(t, os.Chmod(shared, 0o1777))
	wt := func(i int) string { return filepath.Join(shared, fmt.Sprintf("wt%d", i)) }

	for i, u := range users {
		runAgent(t, shared, fmt.Sprintf("make%d", i), u,
			fmt.Sprintf("set -e\nmkdir -m 700 %s\necho secret%d >%s/secret\n", wt(i), i, wt(i)))
	}
	for i, u := range users {
		own, other := wt(i), wt(1-i)
		out := filepath.Join(shared, fmt.Sprintf("out%d", i))
		runAgent(t, shared, fmt.Sprintf("check%d", i), u, fmt.Sprintf(
			"cat %s/secret >%s.own 2>&1\ncat %s/secret >%s.other 2>&1\necho $? >%s.code\n",
			own, out, other, out, out))
		b, err := os.ReadFile(out + ".own")
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("secret%d\n", i), string(b), "session %d reads its own worktree", i)
		b, err = os.ReadFile(out + ".other")
		require.NoError(t, err)
		assert.Contains(t, string(b), "Permission denied", "session %d reads the other worktree", i)
		b, err = os.ReadFile(out + ".code")
		require.NoError(t, err)
		assert.NotEqual(t, "0", strings.TrimSpace(string(b)))
	}
	// Only the owner can remove its worktree; do it before TempDir cleanup.
	for i, u := range users {
		runAgent(t, shared, fmt.Sprintf("clean%d", i), u, fmt.Sprintf("rm -rf %s\n", wt(i)))
	}
}
