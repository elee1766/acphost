package acphost

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSandboxDNSArgs(t *testing.T) {
	tmp := t.TempDir()
	conf := filepath.Join(tmp, "resolv.conf")
	require.NoError(t, os.WriteFile(conf, []byte("nameserver 169.254.1.1\n"), 0o644))
	s := Sandbox{
		Bwrap: "/bin/bwrap", Pasta: "/bin/pasta", RW: []Mount{{Src: tmp}}, Network: "pasta",
		RO:         []Mount{{Src: tmp, Dst: "/etc"}},
		DNS:        &DNS{Forward: "169.254.1.1", Host: "127.0.0.53"},
		ResolvConf: conf,
	}
	argv, net, err := sandboxArgs(s, "agent", nil, tmp)
	require.NoError(t, err)
	want := []string{
		"/bin/pasta", "--config-net", "--foreground", "--quiet", "-t", "none", "-u", "none", "-T", "none", "-U", "none",
		"--dns-forward", "169.254.1.1", "--dns-host", "127.0.0.53", "--",
	}
	assert.Equal(t, want, argv[:len(want)])
	// The resolv.conf bind comes after the /etc bind so it is not covered.
	etc := slices.Index(argv, "/etc")
	rc := slices.Index(argv, "/etc/resolv.conf")
	require.Positive(t, rc)
	assert.Equal(t, []string{"--ro-bind", conf}, argv[rc-2:rc])
	assert.Less(t, etc, rc)
	assert.Equal(t, &NetworkApplied{Mode: "pasta", DNSForward: "169.254.1.1", DNSHost: "127.0.0.53", ResolvConf: conf}, net)

	// Host is optional.
	s.DNS = &DNS{Forward: "169.254.1.1"}
	argv, _, err = sandboxArgs(s, "agent", nil, tmp)
	require.NoError(t, err)
	assert.Equal(t, []string{"--dns-forward", "169.254.1.1", "--"}, argv[12:15])

	// Unset: no DNS options and no resolv.conf bind, as before.
	s.DNS, s.ResolvConf = nil, ""
	argv, _, err = sandboxArgs(s, "agent", nil, tmp)
	require.NoError(t, err)
	assert.NotContains(t, argv, "--dns-forward")
	assert.NotContains(t, argv, "/etc/resolv.conf")
}

func TestSandboxDNSValidation(t *testing.T) {
	tmp := t.TempDir()
	cases := []struct {
		name string
		s    Sandbox
		err  string
	}{
		{"dns with host network", Sandbox{Network: "host", DNS: &DNS{Forward: "169.254.1.1"}}, "dns needs network pasta"},
		{"dns with none", Sandbox{Network: "none", DNS: &DNS{Forward: "169.254.1.1"}}, "dns needs network pasta"},
		{"bad forward", Sandbox{Network: "pasta", DNS: &DNS{Forward: "nope"}}, "dns forward"},
		{"empty forward", Sandbox{Network: "pasta", DNS: &DNS{}}, "dns forward"},
		{"bad host", Sandbox{Network: "pasta", DNS: &DNS{Forward: "169.254.1.1", Host: "localhost"}}, "dns host"},
		{"missing resolv_conf", Sandbox{ResolvConf: filepath.Join(tmp, "nope")}, "resolv_conf"},
	}
	if _, err := exec.LookPath("nft"); err == nil {
		// Typical private-range deny lists must leave the forward
		// address alone; one that covers it is an error.
		cases = append(cases, struct {
			name string
			s    Sandbox
			err  string
		}{"forward denied", Sandbox{Network: "pasta", Deny: []string{"169.254.0.0/16"}, DNS: &DNS{Forward: "169.254.1.1"}}, "inside deny 169.254.0.0/16"})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.s.Bwrap, tc.s.Pasta = "/bin/bwrap", "/bin/pasta"
			tc.s.RW = []Mount{{Src: tmp}}
			_, err := SandboxArgs(tc.s, "agent", nil, tmp)
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("got %v, want %q", err, tc.err)
			}
		})
	}
	if _, err := exec.LookPath("nft"); err == nil {
		s := Sandbox{
			Bwrap: "/bin/bwrap", Pasta: "/bin/pasta", RW: []Mount{{Src: tmp}}, Network: "pasta",
			Deny: []string{"10.0.0.0/8", "100.64.0.0/10", "192.168.0.0/16"},
			DNS:  &DNS{Forward: "169.254.1.1", Host: "100.100.100.100"},
		}
		_, err := SandboxArgs(s, "agent", nil, tmp)
		require.NoError(t, err)
	}
}

func TestSandboxDNSJSON(t *testing.T) {
	in := `{"network":"pasta","dns":{"forward":"169.254.1.1","host":"127.0.0.53"},"resolv_conf":"/etc/acphost/resolv.conf"}`
	var s Sandbox
	require.NoError(t, json.Unmarshal([]byte(in), &s))
	assert.Equal(t, Sandbox{Network: "pasta", DNS: &DNS{Forward: "169.254.1.1", Host: "127.0.0.53"}, ResolvConf: "/etc/acphost/resolv.conf"}, s)
	b, err := json.Marshal(s)
	require.NoError(t, err)
	assert.JSONEq(t, in, string(b))
}

func TestFirstNameserver(t *testing.T) {
	p := filepath.Join(t.TempDir(), "resolv.conf")
	require.NoError(t, os.WriteFile(p, []byte("# x\nsearch a.example\nnameserver 169.254.1.1\nnameserver 1.1.1.1\n"), 0o644))
	ns, err := firstNameserver(p)
	require.NoError(t, err)
	assert.Equal(t, "169.254.1.1", ns)
	require.NoError(t, os.WriteFile(p, []byte("search a.example\n"), 0o644))
	_, err = firstNameserver(p)
	assert.ErrorContains(t, err, "no nameserver")
}

func TestCheckSandboxDNSNotPasta(t *testing.T) {
	assert.NoError(t, CheckSandboxDNS(Sandbox{Network: "none"}, "example.com"))
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

// TestSandboxPastaDNS runs on a host whose resolv.conf names a loopback
// resolver. Without sandbox.dns the namespace cannot reach it; with a
// forward and a resolv.conf naming the forward address, both
// CheckSandboxDNS and a real agent resolve the name.
func TestSandboxPastaDNS(t *testing.T) {
	bwrap := needBwrap(t)
	pasta, _ := needPasta(t)
	// A loopback resolver of our own, standing in for systemd-resolved's
	// 127.0.0.53. Port 53 needs net.ipv4.ip_unprivileged_port_start<=53
	// (CI sets it). The test used to fall back to the host's stub and a
	// real name, which failed on runners whose upstream DNS from inside
	// the namespace is not reliable (#131). Not 127.0.0.54: newer
	// systemd-resolved listens there too.
	stub, name, want := "127.0.0.153", "agent.test", "192.0.2.7"
	if !fakeResolver(t, stub+":53") {
		t.Skip("cannot bind udp " + stub + ":53: set net.ipv4.ip_unprivileged_port_start=53")
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
