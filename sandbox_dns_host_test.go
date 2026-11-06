package acphost

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSandboxPastaDNS runs on a host whose resolv.conf names a loopback
// resolver. Without sandbox.dns the namespace cannot reach it; with a
// forward and a resolv.conf naming the forward address, both
// CheckSandboxDNS and a real agent resolve the name.
func TestSandboxPastaDNS(t *testing.T) {
	bwrap := needBwrap(t)
	pasta, _ := needPasta(t)
	// A loopback resolver, as systemd-resolved's 127.0.0.53 is. Not
	// .53 or .54: resolved listens on both. Not the host's real stub
	// either: that answers from the network, which made this test flaky
	// in CI (#131). Port 53 needs privilege; CI lowers
	// net.ipv4.ip_unprivileged_port_start.
	const stub, name, want = "127.0.0.77", "agent.test", "192.0.2.7"
	if !fakeResolver(t, stub+":53") {
		t.Skip("cannot bind " + stub + ":53 (set net.ipv4.ip_unprivileged_port_start=53)")
	}

	tmp := t.TempDir()
	hostConf := filepath.Join(tmp, "host-resolv.conf")
	require.NoError(t, os.WriteFile(hostConf, []byte("nameserver "+stub+"\nsearch example\n"), 0o644))
	sbConf := filepath.Join(tmp, "sandbox-resolv.conf")
	require.NoError(t, os.WriteFile(sbConf, []byte("nameserver 169.254.1.1\nsearch example\n"), 0o644))

	// No forward: the stub is the namespace's own loopback.
	err := CheckSandboxDNS(Sandbox{Network: "pasta", Pasta: pasta, ResolvConf: hostConf}, name)
	var pe *PermanentError
	require.ErrorAs(t, err, &pe)
	assert.Contains(t, err.Error(), "sandbox.dns")

	sb := Sandbox{
		Network: "pasta", Pasta: pasta,
		Deny:       []string{"10.0.0.0/8", "100.64.0.0/10", "192.168.0.0/16"},
		DNS:        &DNS{Forward: "169.254.1.1", Host: stub},
		ResolvConf: sbConf,
	}
	if _, err := exec.LookPath("nft"); err != nil {
		sb.Deny = nil
	}
	require.NoError(t, CheckSandboxDNS(sb, name))

	// A real agent: getent through the bound resolv.conf.
	getent, err := exec.LookPath("getent")
	if err != nil {
		t.Skip("getent not on PATH")
	}
	dir := t.TempDir()
	s := sandboxSpec(t, bwrap, dir, getent+" hosts "+name+" >$PWD/result 2>&1\n")
	s.Sandbox.RO = append(s.Sandbox.RO, Mount{Src: filepath.Dir(getent)})
	s.Sandbox.Network, s.Sandbox.Pasta = "pasta", pasta
	s.Sandbox.Deny, s.Sandbox.DNS, s.Sandbox.ResolvConf = sb.Deny, sb.DNS, sb.ResolvConf
	require.NoError(t, CheckSpawn(s, false))
	require.NoError(t, WriteSpec(dir, s))
	done := make(chan error, 1)
	go func() { done <- Main(dir) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("agent did not exit")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "work", "result"))
	ex, _ := ReadExit(dir)
	require.Equal(t, 0, ex.ExitCode, "%+v: %s", ex, b)
	assert.Contains(t, string(b), want)
	iso, ok, err := ReadIsolation(dir)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "169.254.1.1", iso.Network.DNSForward)
	assert.Equal(t, sbConf, iso.Network.ResolvConf)
}

// fakeResolver answers A queries with 192.0.2.7 over UDP on addr,
// standing in for a loopback stub such as systemd-resolved's. It reports
// false when it cannot bind (port 53 needs privilege).
func fakeResolver(t *testing.T, addr string) bool {
	t.Helper()
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return false
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if resp := dnsAnswer(buf[:n]); resp != nil {
				_, _ = pc.WriteTo(resp, from)
			}
		}
	}()
	return true
}

// dnsAnswer builds a reply to a one-question query: an A record
// 192.0.2.7 for type A, no answers otherwise.
func dnsAnswer(q []byte) []byte {
	if len(q) < 12 {
		return nil
	}
	i := 12
	for i < len(q) && q[i] != 0 {
		i += int(q[i]) + 1
	}
	if i+5 > len(q) {
		return nil
	}
	qend := i + 5
	qtype := uint16(q[i+1])<<8 | uint16(q[i+2])
	r := append([]byte{}, q[:qend]...)
	r[2] = 0x81 // response, recursion desired
	r[3] = 0x80 // recursion available, no error
	r[6], r[7] = 0, 0
	r[8], r[9], r[10], r[11] = 0, 0, 0, 0
	if qtype == 1 {
		r[7] = 1
		r = append(r, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 192, 0, 2, 7)
	}
	return r
}
