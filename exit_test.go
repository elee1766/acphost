package acphost

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A host that stops the agent itself records why in exit.json (#8).
// MethodStop is covered by Example.
func TestExitReasonLease(t *testing.T) {
	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700))
	require.NoError(t, WriteSpec(dir, Spec{
		Protocol: Protocol, Session: "s1", Command: agent, Dir: dir, GCRootDir: dir, LeaseMS: 100,
	}))
	done := make(chan error, 1)
	go func() { done <- Main(dir) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("host did not exit on lease")
	}
	ex, ok := ReadExit(dir)
	require.True(t, ok)
	assert.Equal(t, "no controller for 100ms", ex.Reason)
	assert.Equal(t, "terminated", ex.Signal)
}

func TestExitReasonAgentExit(t *testing.T) {
	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\nexit 3\n"), 0o700))
	require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol, Session: "s1", Command: agent, Dir: dir, GCRootDir: dir}))
	require.NoError(t, Main(dir))
	ex, ok := ReadExit(dir)
	require.True(t, ok)
	assert.Equal(t, Exit{Reason: "agent exited", ExitCode: 3}, ex)
}
