package acphost

import (
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestCheckSpawnListen(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "agent")
	require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o700))
	// Real files that load: the check parses them.
	good := writeTestTLS(t, dir)
	spec := func(listen string, tls *ListenTLS) Spec {
		return Spec{Dir: dir, Command: exe, Listen: listen, TLS: tls}
	}

	assert.NoError(t, CheckSpawn(spec("", nil), false))
	assert.NoError(t, CheckSpawn(spec("127.0.0.1:0", nil), false))
	assert.NoError(t, CheckSpawn(spec("[::1]:8443", nil), false))
	assert.NoError(t, CheckSpawn(spec("0.0.0.0:8443", nil), false), "every interface, written out")
	assert.NoError(t, CheckSpawn(spec("[::]:8443", nil), false))
	assert.NoError(t, CheckSpawn(spec("host.example:8443", &good), false))

	for name, s := range map[string]Spec{
		"no port":          spec("127.0.0.1", nil),
		"empty host":       spec(":8443", nil),
		"empty host, 0":    spec(":0", nil),
		"port not a port":  spec("127.0.0.1:http", nil),
		"port too large":   spec("127.0.0.1:70000", nil),
		"garbage":          spec("not an address", nil),
		"tls, no listen":   spec("", &good),
		"no cert":          spec("127.0.0.1:0", &ListenTLS{Key: good.Key, ClientCA: good.ClientCA}),
		"no key":           spec("127.0.0.1:0", &ListenTLS{Cert: good.Cert, ClientCA: good.ClientCA}),
		"no client_ca":     spec("127.0.0.1:0", &ListenTLS{Cert: good.Cert, Key: good.Key}),
		"empty allow item": spec("127.0.0.1:0", &ListenTLS{Cert: good.Cert, Key: good.Key, ClientCA: good.ClientCA, Allow: []string{""}}),
		"missing files": spec("127.0.0.1:0", &ListenTLS{
			Cert: filepath.Join(dir, "gone.crt"), Key: filepath.Join(dir, "gone.key"), ClientCA: good.ClientCA,
		}),
		"cert not PEM": spec("127.0.0.1:0", &ListenTLS{Cert: exe, Key: good.Key, ClientCA: good.ClientCA}),
		"ca not PEM":   spec("127.0.0.1:0", &ListenTLS{Cert: good.Cert, Key: good.Key, ClientCA: exe}),
		"key mismatch": spec("127.0.0.1:0", &ListenTLS{Cert: good.Cert, Key: writeTestTLS(t, t.TempDir()).Key, ClientCA: good.ClientCA}),
	} {
		err := CheckSpawn(s, false)
		require.Error(t, err, name)
		assert.True(t, Permanent(err), "%s: %v", name, err)
	}
}

// Main refuses the same specs, for a spec written without CheckSpawn.
func TestMainRejectsBadListen(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteSpec(dir, Spec{Protocol: Protocol, Session: "s1", Command: "true", Dir: dir, GCRootDir: dir, Listen: ":8443"}))
	err := Main(dir)
	require.Error(t, err)
	assert.True(t, Permanent(err))
}

// Dead peers: accepted TCP connections get keepalive and a user timeout, so
// a peer that vanishes is noticed within deadPeerBound and dropConn clears
// the controller.
func TestListenTCPDeadPeerOptions(t *testing.T) {
	ln, err := listenTCP("127.0.0.1:0", slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		nc, err := ln.Accept()
		if err == nil {
			accepted <- nc
		}
		close(accepted)
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	nc := <-accepted
	require.NotNil(t, nc)
	t.Cleanup(func() { _ = nc.Close() })

	rc, err := nc.(*net.TCPConn).SyscallConn()
	require.NoError(t, err)
	get := func(level, opt int) (v int) {
		require.NoError(t, rc.Control(func(fd uintptr) {
			var err error
			v, err = unix.GetsockoptInt(int(fd), level, opt)
			require.NoError(t, err)
		}))
		return v
	}
	assert.Equal(t, 1, get(unix.SOL_SOCKET, unix.SO_KEEPALIVE))
	assert.Equal(t, 30, get(unix.IPPROTO_TCP, unix.TCP_KEEPIDLE))
	assert.Equal(t, 10, get(unix.IPPROTO_TCP, unix.TCP_KEEPINTVL))
	assert.Equal(t, 3, get(unix.IPPROTO_TCP, unix.TCP_KEEPCNT))
	assert.Equal(t, int(deadPeerBound/time.Millisecond), get(unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT))
}
