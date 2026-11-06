package acphost

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defaultGateway returns the host's IPv4 default gateway from
// /proc/net/route. pasta copies it into the namespace and maps it to the
// host's loopback, so a listener on 127.0.0.1 is reachable there.
func defaultGateway(t *testing.T) netip.Addr {
	t.Helper()
	f, err := os.Open("/proc/net/route")
	if err != nil {
		t.Skip("no /proc/net/route")
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) > 2 && fs[1] == "00000000" {
			b, err := hex.DecodeString(fs[2])
			if err == nil && len(b) == 4 {
				var a [4]byte
				binary.BigEndian.PutUint32(a[:], binary.LittleEndian.Uint32(b))
				return netip.AddrFrom4(a)
			}
		}
	}
	t.Skip("no IPv4 default route")
	return netip.Addr{}
}

// needPasta skips unless pasta, nft and bash are on PATH and pasta can
// create a namespace here.
func needPasta(t *testing.T) (pasta, bash string) {
	t.Helper()
	var err error
	if pasta, err = exec.LookPath("pasta"); err != nil {
		t.Skip("pasta not on PATH")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft not on PATH")
	}
	if bash, err = exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	if err := exec.Command(pasta, "--config-net", "-f", "-q", "--", "true").Run(); err != nil {
		t.Skipf("pasta does not work here: %v", err)
	}
	return pasta, bash
}

func TestSandboxPastaDeny(t *testing.T) {
	bwrap := needBwrap(t)
	pasta, bash := needPasta(t)
	gw := defaultGateway(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	run := func(deny []string) string {
		dir := t.TempDir()
		s := sandboxSpec(t, bwrap, dir, `
if `+bash+` -c 'exec 3<>/dev/tcp/`+gw.String()+`/`+port+`' 2>/dev/null; then r=reachable; else r=unreachable; fi
echo $r >$PWD/result
`)
		s.Sandbox.RO = append(s.Sandbox.RO, Mount{Src: filepath.Dir(bash)})
		s.Sandbox.Network = "pasta"
		s.Sandbox.Pasta = pasta
		s.Sandbox.Deny = deny
		require.NoError(t, CheckSpawn(s, false))
		require.NoError(t, WriteSpec(dir, s))
		require.NoError(t, Main(dir))
		ex, _ := ReadExit(dir)
		require.Equal(t, 0, ex.ExitCode, "%+v", ex)
		// pasta is bwrap's parent, so Main returning means it exited.
		assert.False(t, procWith(filepath.Join(dir, "work", "agent")), "pasta or agent still running")
		b, err := os.ReadFile(filepath.Join(dir, "work", "result"))
		require.NoError(t, err)
		return strings.TrimSpace(string(b))
	}
	assert.Equal(t, "unreachable", run([]string{gw.String() + "/32"}), "denied CIDR")
	assert.Equal(t, "reachable", run([]string{"192.0.2.0/24"}), "allowed address")
}
