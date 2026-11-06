package sandbox

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSandboxDNSArgs(t *testing.T) {
	old := etcResolvConf
	t.Cleanup(func() { etcResolvConf = old })
	etcResolvConf = "/nonexistent/resolv.conf" // not a link: bound as is
	tmp := t.TempDir()
	conf := filepath.Join(tmp, "resolv.conf")
	require.NoError(t, os.WriteFile(conf, []byte("nameserver 169.254.1.1\n"), 0o644))
	s := Sandbox{
		Bwrap: "/bin/bwrap", Pasta: "/bin/pasta", RW: []Mount{{Src: tmp}}, Network: "pasta",
		RO:         []Mount{{Src: tmp, Dst: "/etc"}},
		DNS:        &DNS{Forward: "169.254.1.1", Host: "127.0.0.53"},
		ResolvConf: conf,
	}
	argv, net, err := BuildArgs(s, "agent", nil, tmp)
	require.NoError(t, err)
	want := []string{
		"/bin/pasta", "--config-net", "--foreground", "--quiet", "-t", "none", "-u", "none", "-T", "none", "-U", "none",
		"--dns-forward", "169.254.1.1", "--dns-host", "127.0.0.53", "--",
	}
	assert.Equal(t, want, argv[:len(want)])
	// The resolv.conf bind comes after the /etc bind so it is not covered.
	etc := slices.Index(argv, "/etc")
	rc := slices.Index(argv, "/nonexistent/resolv.conf")
	require.Positive(t, rc)
	assert.Equal(t, []string{"--ro-bind", conf}, argv[rc-2:rc])
	assert.Less(t, etc, rc)
	assert.Equal(t, &NetworkApplied{Mode: "pasta", DNSForward: "169.254.1.1", DNSHost: "127.0.0.53", ResolvConf: conf}, net)

	// Host is optional.
	s.DNS = &DNS{Forward: "169.254.1.1"}
	argv, _, err = BuildArgs(s, "agent", nil, tmp)
	require.NoError(t, err)
	assert.Equal(t, []string{"--dns-forward", "169.254.1.1", "--"}, argv[12:15])

	// Unset: no DNS options and no resolv.conf bind, as before.
	s.DNS, s.ResolvConf = nil, ""
	argv, _, err = BuildArgs(s, "agent", nil, tmp)
	require.NoError(t, err)
	assert.NotContains(t, argv, "--dns-forward")
	assert.NotContains(t, argv, "/nonexistent/resolv.conf")
}

func TestResolvConfDst(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	require.NoError(t, os.WriteFile(plain, nil, 0o644))
	assert.Equal(t, plain, resolvConfDst(plain))
	assert.Equal(t, filepath.Join(dir, "missing"), resolvConfDst(filepath.Join(dir, "missing")))
	// /etc/resolv.conf -> ../run/systemd/resolve/stub-resolv.conf, dangling.
	etc := filepath.Join(dir, "etc")
	require.NoError(t, os.Mkdir(etc, 0o755))
	require.NoError(t, os.Symlink("../run/systemd/resolve/stub-resolv.conf", filepath.Join(etc, "resolv.conf")))
	assert.Equal(t, filepath.Join(dir, "run/systemd/resolve/stub-resolv.conf"), resolvConfDst(filepath.Join(etc, "resolv.conf")))
	// Two hops, absolute (NixOS: /etc/resolv.conf -> /etc/static/resolv.conf -> ...).
	require.NoError(t, os.Symlink(filepath.Join(etc, "resolv.conf"), filepath.Join(dir, "hop")))
	assert.Equal(t, filepath.Join(dir, "run/systemd/resolve/stub-resolv.conf"), resolvConfDst(filepath.Join(dir, "hop")))
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
			_, err := Args(tc.s, "agent", nil, tmp)
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
		_, err := Args(s, "agent", nil, tmp)
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
	assert.NoError(t, CheckDNS(Sandbox{Network: "none"}, "example.com"))
}
