package acphost

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The defaults must stay the names running hosts already use.
func TestPrefixDefaults(t *testing.T) {
	assert.Equal(t, "_jobshop/stop", MethodStop)
	assert.Equal(t, "_jobshop/turn_end", MethodTurnEnd)
	assert.Equal(t, "_jobshop/export_stats", MethodExportStats)
	var s Spec
	assert.Equal(t, MethodStop, s.method("stop"))
	assert.Equal(t, MethodTurnEnd, s.method("turn_end"))
	assert.Equal(t, MethodExportStats, s.method("export_stats"))
	assert.Equal(t, "_x/stop", Spec{MethodPrefix: "_x/"}.method("stop"))

	dir := t.TempDir()
	log := slog.New(slog.DiscardHandler)
	registerGCRoot(log, Spec{Session: "s1", GCRootDir: dir}, "/nix/store/abc-x/bin/x")
	_, err := os.Lstat(filepath.Join(dir, "jobshop-host-s1"))
	require.NoError(t, err)
	registerGCRoot(log, Spec{Session: "s1", GCRootDir: dir, GCRootPrefix: "acp-"}, "/nix/store/abc-x/bin/x")
	_, err = os.Lstat(filepath.Join(dir, "acp-s1"))
	require.NoError(t, err)
}

func TestCustomPrefix(t *testing.T) {
	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700))
	require.NoError(t, WriteSpec(dir, Spec{
		Protocol: Protocol, Session: "s1", Command: agent, Dir: dir, GCRootDir: dir,
		MethodPrefix: "_acp/",
	}))
	done := make(chan error, 1)
	go func() { done <- Main(dir) }()
	ctx := context.Background()
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(dir, ACPSocket))
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)

	// A controller on the default prefix fails clearly against this host.
	_, _, _, err := Connect(ctx, dir)
	require.ErrorContains(t, err, `host uses extension method prefix "_acp/", want "_jobshop/"`)

	nc, _, w, err := Connect(ctx, dir, WithMethodPrefix("_acp/"))
	require.NoError(t, err)
	defer func() { _ = nc.Close() }()
	assert.Equal(t, "_acp/", w.MethodPrefix)

	// The default stop name is plain ACP to this host. The custom one stops it.
	_, err = nc.Write([]byte(`{"jsonrpc":"2.0","method":"_jobshop/stop"}` + "\n"))
	require.NoError(t, err)
	select {
	case err := <-done:
		t.Fatalf("host stopped on the default prefix: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	_, err = nc.Write([]byte(`{"jsonrpc":"2.0","method":"` + MethodName("_acp/", "stop") + `"}` + "\n"))
	require.NoError(t, err)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("host did not stop")
	}
}
