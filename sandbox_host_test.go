package acphost

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// needBwrap skips unless bwrap is on PATH and user namespaces work.
func needBwrap(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not on PATH")
	}
	if err := exec.Command(p, "--ro-bind", "/", "/", "true").Run(); err != nil {
		t.Skipf("bwrap does not work here: %v", err)
	}
	return p
}

// sandboxSpec returns a spec whose agent is script, run in dir/work with
// the system read-only and work read-write.
func sandboxSpec(t *testing.T, bwrap, dir, script string) Spec {
	t.Helper()
	work := filepath.Join(dir, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))
	agent := filepath.Join(work, "agent")
	require.NoError(t, os.WriteFile(agent, []byte("#!/bin/sh\n"+script), 0o700))
	var ro []Mount
	for _, p := range []string{"/nix", "/bin", "/usr", "/lib", "/lib64", "/etc"} {
		ro = append(ro, Mount{Src: p, SkipMissing: true})
	}
	return Spec{
		Protocol: Protocol, Session: "s1", Command: agent, Dir: work, GCRootDir: dir, LeaseMS: 60_000,
		Sandbox: &Sandbox{Bwrap: bwrap, RO: ro, RW: []Mount{{Src: work}}, EnvKeep: []string{"PATH"}},
	}
}

func TestSandboxExitCode(t *testing.T) {
	bwrap := needBwrap(t)
	dir := t.TempDir()
	s := sandboxSpec(t, bwrap, dir, "exit 3\n")
	require.NoError(t, CheckSpawn(s, false))
	require.NoError(t, WriteSpec(dir, s))
	require.NoError(t, Main(dir))
	ex, ok := ReadExit(dir)
	require.True(t, ok)
	assert.Equal(t, Exit{Reason: "agent exited", ExitCode: 3}, ex)
}

func TestSandboxMounts(t *testing.T) {
	bwrap := needBwrap(t)
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o600))
	s := sandboxSpec(t, bwrap, dir, `
r=$PWD/result
cat $PWD/secret/f >/dev/null 2>&1 && echo hide-readable >>$r
cat `+outside+` >/dev/null 2>&1 && echo outside-readable >>$r
touch $PWD/rw-ok 2>/dev/null || echo rw-failed >>$r
touch /etc/acphost-ro-test 2>/dev/null && echo ro-writable >>$r
echo $FROMSPEC >>$r
exit 0
`)
	secret := filepath.Join(dir, "work", "secret")
	require.NoError(t, os.MkdirAll(secret, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(secret, "f"), []byte("x"), 0o600))
	s.Sandbox.Hide = []string{secret}
	s.Env = []string{"FROMSPEC=yes"}
	require.NoError(t, WriteSpec(dir, s))
	require.NoError(t, Main(dir))
	ex, _ := ReadExit(dir)
	require.Equal(t, 0, ex.ExitCode, "%+v", ex)
	b, err := os.ReadFile(filepath.Join(dir, "work", "result"))
	require.NoError(t, err)
	assert.Equal(t, "yes\n", string(b))
	_, err = os.Stat(filepath.Join(dir, "work", "rw-ok"))
	assert.NoError(t, err)
}

func TestSandboxReconnectStop(t *testing.T) {
	bwrap := needBwrap(t)
	dir := t.TempDir()
	token := "acphost-sbx-" + filepath.Base(dir)
	s := sandboxSpec(t, bwrap, dir,
		`echo '{"jsonrpc":"2.0","method":"session/update"}'`+"\nexec sh -c 'cat >/dev/null; sleep 600' "+token+"\n")
	require.NoError(t, WriteSpec(dir, s))
	done := make(chan error, 1)
	go func() { done <- Main(dir) }()
	ctx := context.Background()
	var nc net.Conn
	require.Eventually(t, func() bool {
		var err error
		nc, _, _, err = Connect(ctx, dir)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { seq, _ := JournalTail(dir); return seq > 0 }, 5*time.Second, 10*time.Millisecond)
	_ = nc.Close()

	nc, err := net.Dial("unix", filepath.Join(dir, ACPSocket))
	require.NoError(t, err)
	defer func() { _ = nc.Close() }()
	since := int64(0)
	hello, _ := json.Marshal(Hello{Protocol: Protocol, Since: &since})
	_, err = nc.Write(append(hello, '\n'))
	require.NoError(t, err)
	rd := bufio.NewReader(nc)
	_, err = rd.ReadBytes('\n') // welcome
	require.NoError(t, err)
	replayed, err := rd.ReadBytes('\n')
	require.NoError(t, err)
	assert.JSONEq(t, `{"jsonrpc":"2.0","method":"session/update"}`, string(replayed))
	require.True(t, procWith(token), "agent running")

	_, err = nc.Write([]byte(`{"jsonrpc":"2.0","method":"` + MethodStop + `"}` + "\n"))
	require.NoError(t, err)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("host did not stop")
	}
	ex, _ := ReadExit(dir)
	assert.Equal(t, "controller requested stop", ex.Reason)
	assert.NotEmpty(t, ex.Signal)
	assert.Eventually(t, func() bool { return !procWith(token) }, 5*time.Second, 50*time.Millisecond, "agent killed")
}

// procWith reports whether a process has token in its command line.
func procWith(token string) bool {
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err == nil && strings.Contains(string(b), token) {
			return true
		}
	}
	return false
}
