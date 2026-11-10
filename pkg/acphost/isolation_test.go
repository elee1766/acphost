package acphost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gfx-labs/acphost/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadIsolationAbsent(t *testing.T) {
	_, ok, err := ReadIsolation(t.TempDir())
	assert.False(t, ok, "older host")
	assert.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, IsolationFile), []byte("{"), 0o600))
	_, ok, err = ReadIsolation(dir)
	assert.False(t, ok)
	assert.Error(t, err)
}

// With no isolation fields the host still writes a record, all parts nil.
func TestIsolationEmptyRecord(t *testing.T) {
	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol, Session: "s1", Command: agent, Dir: dir, GCRootDir: dir}))
	require.NoError(t, Main(dir))
	iso, ok, err := ReadIsolation(dir)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, Isolation{}, iso)
	b, _ := os.ReadFile(filepath.Join(dir, IsolationFile))
	assert.Equal(t, "{}", string(b))
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		assert.NotContains(t, e.Name(), ".tmp", "no temp file left")
	}
}

func TestSandboxArgsNetworkApplied(t *testing.T) {
	tmp := t.TempDir()
	base := sandbox.Sandbox{Bwrap: "/bin/bwrap", Pasta: "/bin/pasta", RW: []sandbox.Mount{{Src: tmp}}}
	for _, tc := range []struct{ network, mode string }{{"", "host"}, {"host", "host"}, {"none", "none"}, {"pasta", "pasta"}} {
		s := base
		s.Network = tc.network
		argv, net, err := sandbox.BuildArgs(s, "agent", nil, tmp)
		require.NoError(t, err, tc.network)
		assert.Equal(t, &NetworkApplied{Mode: tc.mode}, net, tc.network)
		want, _ := sandbox.Args(s, "agent", nil, tmp)
		assert.Equal(t, want, argv)
	}
	if _, err := exec.LookPath("nft"); err == nil {
		s := base
		s.Network = "pasta"
		s.Deny = []string{"fd00::/8", "10.1.2.3/8"}
		_, net, err := sandbox.BuildArgs(s, "agent", nil, tmp)
		require.NoError(t, err)
		assert.Equal(t, &NetworkApplied{Mode: "pasta", Deny: []string{"10.0.0.0/8", "fd00::/8"}}, net)
	}
}
