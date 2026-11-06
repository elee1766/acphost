package acphost

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/elee1766/acphost/pkg/proc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// flock takes how (LOCK_EX or LOCK_SH) on name in dir until the test ends.
func flock(t *testing.T, dir, name string, how int) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	require.NoError(t, unix.Flock(int(f.Fd()), how|unix.LOCK_NB))
}

// Main holds supervisor.lock and writes supervisor.pid, and nothing else:
// no host.lock or host.pid (#222).
func TestMainLockIsSupervisorLock(t *testing.T) {
	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\nread x\nexit 0\n"), 0o700))
	require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol, Session: "s1", Command: agent, Dir: dir, GCRootDir: dir, LeaseMS: 60_000}))
	done := make(chan error, 1)
	go func() { done <- Main(dir) }()
	nc, _, w, err := Connect(t.Context(), dir)
	for i := 0; err != nil && i < 100; i++ {
		time.Sleep(50 * time.Millisecond)
		nc, _, w, err = Connect(t.Context(), dir)
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(filepath.Join(dir, proc.SupervisorPidFile)) })

	assert.True(t, Alive(dir))
	d, err := proc.Open(dir)
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), d.Pid)
	assert.Equal(t, w.Pid, d.Pid)
	assert.True(t, d.Running())
	assert.NoFileExists(t, filepath.Join(dir, "host.lock"))
	assert.NoFileExists(t, filepath.Join(dir, "host.pid"))
	b, err := os.ReadFile(filepath.Join(dir, proc.SupervisorPidFile))
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(os.Getpid()), string(b))

	// A second host on the dir is refused by supervisor.lock.
	require.Error(t, Main(dir))
	// So is CompressJournal while the host runs.
	require.Error(t, CompressJournal(dir, 3))

	_, err = nc.Write([]byte(`{"jsonrpc":"2.0","method":"x"}` + "\n"))
	require.NoError(t, err)
	_ = nc.Close()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("host did not exit")
	}
	assert.False(t, Alive(dir))
	assert.False(t, d.Running())
	assert.NoFileExists(t, filepath.Join(dir, "host.lock"))
	assert.NoFileExists(t, filepath.Join(dir, "host.pid"))
}

// supervisor.lock decides: a held supervisor.lock means a host, and a
// held host.lock (a host from before #222) does not.
func TestAliveReadsSupervisorLock(t *testing.T) {
	t.Run("supervisor", func(t *testing.T) {
		dir := t.TempDir()
		sup, err := proc.Supervise(dir)
		require.NoError(t, err)
		assert.True(t, Alive(dir))
		require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol, Session: "s1", Command: "/bin/true", Dir: dir, GCRootDir: dir}))
		assert.ErrorContains(t, Main(dir), "locked by another supervisor")
		require.NoError(t, sup.Close())
		assert.False(t, Alive(dir))
	})
	t.Run("legacy host.lock", func(t *testing.T) {
		dir := t.TempDir()
		flock(t, dir, "host.lock", unix.LOCK_EX)
		assert.False(t, Alive(dir))
	})
	t.Run("no lock file", func(t *testing.T) {
		assert.False(t, Alive(t.TempDir()))
	})
	// proc's Wait holds a shared lock for a moment after the host exits.
	// Alive must not take that waiter for a host.
	t.Run("waiter", func(t *testing.T) {
		dir := t.TempDir()
		flock(t, dir, proc.LockFile, unix.LOCK_SH)
		assert.False(t, Alive(dir))
	})
}

// CompressJournal takes supervisor.lock for its run, so a host holding
// it makes it fail, and an idle dir compresses.
func TestCompressJournalLock(t *testing.T) {
	dir := t.TempDir()
	writeJournal(t, dir, 50, 0)
	sup, err := proc.Supervise(dir)
	require.NoError(t, err)
	require.Error(t, CompressJournal(dir, 3))
	require.NoError(t, sup.Close())
	require.NoError(t, CompressJournal(dir, 3))
	assert.False(t, Alive(dir))
	assert.NoFileExists(t, filepath.Join(dir, "host.lock"))
}
